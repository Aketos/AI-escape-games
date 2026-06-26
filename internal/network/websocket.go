package network

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"escape-game/internal/ai"
	"escape-game/internal/game"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all connections for now
		return true
	},
}

// ServerMessage represents the structure of the JSON payload sent to the client.
type ServerMessage struct {
	Type    string `json:"type"`    // "ai_audio_chunk", "sfx_trigger", or "status"
	Payload string `json:"payload"` // base64 audio, SFX event name, or status message
}

// safeConn serializes writes to a player WebSocket: audio forwarding, status
// updates and SFX broadcasts run on different goroutines, and gorilla/websocket
// forbids concurrent writers.
type safeConn struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func (c *safeConn) WriteJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.WriteJSON(v)
}

// WSServer manages WebSocket connections for the game.
type WSServer struct {
	sync.Mutex
	Engine   *game.GameEngine
	clients  map[*websocket.Conn]*safeConn
	config   *ai.GameConfig
	scenario string
	llmProxy *ai.LLMProxy // set via SetLLMProxy for scenario reload
}

// NewWSServer initializes the WSServer with the shared game engine and
// default scenario.
func NewWSServer(engine *game.GameEngine, scenario string) *WSServer {
	cfg, err := ai.LoadGameConfig(scenario)
	if err != nil {
		log.Fatalf("Failed to load game config: %v", err)
	}
	return &WSServer{
		Engine:   engine,
		clients:  make(map[*websocket.Conn]*safeConn),
		config:   cfg,
		scenario: scenario,
	}
}

// NewWSServerWithConfig initializes the WSServer with a pre-loaded game config.
func NewWSServerWithConfig(engine *game.GameEngine, cfg *ai.GameConfig, scenario string) *WSServer {
	return &WSServer{
		Engine:   engine,
		clients:  make(map[*websocket.Conn]*safeConn),
		config:   cfg,
		scenario: scenario,
	}
}

// SetLLMProxy links the LLM proxy so scenario reloads can update its config.
func (s *WSServer) SetLLMProxy(p *ai.LLMProxy) {
	s.llmProxy = p
}

// ReloadScenario reinitializes the game engine and config for a new scenario.
func (s *WSServer) ReloadScenario(scenario string) error {
	cfg, err := ai.LoadGameConfig(scenario)
	if err != nil {
		return err
	}

	gameFSM, err := game.LoadScenario(
		filepath.Join(cfg.ScenarioDir, "room_state.json"),
		filepath.Join(cfg.ScenarioDir, "player_state.json"),
	)
	if err != nil {
		return fmt.Errorf("failed to load scenario %q: %w", scenario, err)
	}

	s.Lock()
	s.config = cfg
	s.scenario = scenario
	s.Engine.ReloadState(gameFSM)
	s.Unlock()

	if s.llmProxy != nil {
		s.llmProxy.UpdateConfig(cfg)
	}

	log.Printf("Scenario reloaded: %s", scenario)
	return nil
}

// BroadcastSFX sends a sound effect trigger to all connected players. It is
// used by the LLM proxy when a game action fires an effect.
func (s *WSServer) BroadcastSFX(sfx string) {
	s.Lock()
	conns := make([]*safeConn, 0, len(s.clients))
	for _, c := range s.clients {
		conns = append(conns, c)
	}
	s.Unlock()

	for _, c := range conns {
		if err := c.WriteJSON(ServerMessage{Type: "sfx_trigger", Payload: sfx}); err != nil {
			log.Printf("Failed to broadcast SFX %q: %v", sfx, err)
		}
	}
}

// BroadcastGameState sends the current game state to all connected clients.
func (s *WSServer) BroadcastGameState() {
	state := s.Engine.GameStateForClient()
	s.Lock()
	conns := make([]*safeConn, 0, len(s.clients))
	for _, c := range s.clients {
		conns = append(conns, c)
	}
	s.Unlock()

	for _, c := range conns {
		if err := c.WriteJSON(map[string]interface{}{"type": "game_state", "payload": state}); err != nil {
			log.Printf("Failed to broadcast game state: %v", err)
		}
	}
}

func (s *WSServer) HandleConnections(w http.ResponseWriter, r *http.Request) {
	// Check for scenario query param; reload if different from current
	if scenario := r.URL.Query().Get("scenario"); scenario != "" {
		s.Lock()
		current := s.scenario
		s.Unlock()
		if current != scenario {
			if err := s.ReloadScenario(scenario); err != nil {
				log.Printf("Failed to reload scenario %q: %v", scenario, err)
				http.Error(w, "Invalid scenario", http.StatusBadRequest)
				return
			}
		}
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	// Set WebSocket deadlines: long read timeout to survive silence,
	// pong handler resets the deadline on each pong.
	ws.SetReadDeadline(time.Now().Add(120 * time.Second))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	conn := &safeConn{ws: ws}
	s.Lock()
	s.clients[ws] = conn
	s.Unlock()

	unmuteURL := os.Getenv("UNMUTE_WS_URL")
	if unmuteURL == "" {
		log.Println("WARNING: UNMUTE_WS_URL is not set.")
		unmuteURL = "ws://localhost:8000/v1/realtime" // Unmute dev default
	}
	unmuteVoice := os.Getenv("UNMUTE_VOICE") // optional TTS voice path
	if unmuteVoice == "" {
		unmuteVoice = "cml-tts/fr/12080_11650_000047-0001_enhanced.wav" // default French voice
	}

	conn.WriteJSON(ServerMessage{Type: "status", Payload: "AI Engine Ready"})

	// 2. Establish AI Bridge. Persona, intro and game state are owned by the
	// LLM proxy (see internal/ai/llmproxy.go); Unmute only needs a session
	// configuration to start the conversation.
	moshiClient := ai.NewUnmuteClient(unmuteURL, unmuteVoice, s.Engine)

	// Cinematic Intro in a goroutine to ensure Full-Duplex remains unblocked
	go func() {
		conn.WriteJSON(ServerMessage{Type: "sfx_trigger", Payload: "sfx_depressurization"})

		if err := moshiClient.Connect(context.Background()); err != nil {
			log.Printf("Failed to connect to Unmute WS at %s: %v", unmuteURL, err)
			conn.WriteJSON(ServerMessage{Type: "status", Payload: "Failed to connect to the AI engine."})
			return
		}

		introPrompt := s.config.IntroPrompt
		if err := moshiClient.SendHiddenPrompt(introPrompt); err != nil {
			log.Printf("Failed to send intro prompt to Unmute: %v", err)
		}

		go moshiClient.ReceiveLoop(conn)

		// Wait for the intro to be spoken (approx 15s) before revealing
		// the game state UI to the player.
		time.Sleep(15 * time.Second)
		conn.WriteJSON(ServerMessage{Type: "intro_complete", Payload: ""})
		conn.WriteJSON(map[string]interface{}{"type": "game_state", "payload": s.Engine.GameStateForClient()})
	}()

	log.Println("New WebSocket connection established and fully initialized")

	// 3. Ping ticker: sends a ping every 30s to keep the connection alive
	// through proxies and load balancers during silence periods.
	pingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				conn.mu.Lock()
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					log.Printf("WebSocket ping error: %v", err)
					conn.mu.Unlock()
					return
				}
				conn.mu.Unlock()
			case <-pingDone:
				return
			}
		}
	}()

	// 4. Graceful Shutdown Hook
	defer func() {
		close(pingDone)
		s.Lock()
		delete(s.clients, ws)
		s.Unlock()
		ws.Close()

		// Optional: We can also close moshiClient here if we track it
		moshiClient.Close()
	}()

	// 5. Handle incoming player streams (Audio & Events)
	for {
		messageType, p, err := ws.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error/disconnect: %v", err)
			break
		}
		// Reset read deadline on any message (player is active)
		ws.SetReadDeadline(time.Now().Add(120 * time.Second))

		// Forward audio bytes to Moshi API
		if messageType == websocket.TextMessage {
			// React sends audio as JSON: {"type": "ai_audio_chunk", "payload": "<base64>"}
			// Or maybe "player_audio"? The user didn't specify exactly, but typically it sends JSON.
			// Let's forward the raw text message or call moshiClient.SendAudioJSON.
			moshiClient.SendAudioJSON(p)
		} else if messageType == websocket.BinaryMessage {
			// If React sends binary directly
			moshiClient.SendAudioBinary(p)
		}
	}
}

package network

import (
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"escape-game/internal/ai"

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

// writePing sends a keep-alive ping, serialized with the other writes.
func (c *safeConn) writePing() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
}

// maxPlayerMessageBytes bounds one player frame; Opus pages are a few KB.
const maxPlayerMessageBytes = 1 << 20

// WSServer bridges the player's browser to Unmute. The game itself is owned
// by the LLM proxy.
type WSServer struct {
	mu          sync.Mutex
	player      *safeConn // the one connected player, nil when the game is free
	llmProxy    *ai.LLMProxy
	unmuteURL   string
	unmuteVoice string
}

// NewWSServer creates the server from UNMUTE_WS_URL and UNMUTE_VOICE; call
// SetLLMProxy before serving.
func NewWSServer() *WSServer {
	s := &WSServer{
		unmuteURL:   os.Getenv("UNMUTE_WS_URL"),
		unmuteVoice: os.Getenv("UNMUTE_VOICE"), // optional TTS voice path
	}
	if s.unmuteURL == "" {
		log.Println("WARNING: UNMUTE_WS_URL is not set.")
		s.unmuteURL = "ws://localhost:8000/v1/realtime" // Unmute dev default
	}
	if s.unmuteVoice == "" {
		s.unmuteVoice = "cml-tts/fr/12080_11650_000047-0001_enhanced.wav" // default French voice
	}
	return s
}

// SetLLMProxy links the LLM proxy that owns the voice game.
func (s *WSServer) SetLLMProxy(p *ai.LLMProxy) {
	s.llmProxy = p
}

// sendToPlayer writes v to the connected player, if any.
func (s *WSServer) sendToPlayer(v interface{}) {
	s.mu.Lock()
	c := s.player
	s.mu.Unlock()
	if c == nil {
		return
	}
	if err := c.WriteJSON(v); err != nil {
		log.Printf("Failed to send to player: %v", err)
	}
}

// BroadcastSFX sends a sound effect trigger to the player. It is used by the
// LLM proxy when a game action fires an effect.
func (s *WSServer) BroadcastSFX(sfx string) {
	s.sendToPlayer(ServerMessage{Type: "sfx_trigger", Payload: sfx})
}

// BroadcastGameState sends the current game state to the player.
func (s *WSServer) BroadcastGameState() {
	s.sendToPlayer(map[string]interface{}{"type": "game_state", "payload": s.llmProxy.Engine.GameStateForClient()})
}

func (s *WSServer) HandleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer ws.Close()
	ws.SetReadLimit(maxPlayerMessageBytes)
	conn := &safeConn{ws: ws}

	// shortcut: one game per process, because Unmute's LLM calls carry no
	// session ID to tell games apart. A second visitor is turned away, and a
	// dead client holds the slot until its 120s read deadline. Upgrade to
	// per-session games if Unmute ever forwards a session ID.
	s.mu.Lock()
	busy := s.player != nil
	if !busy {
		s.player = conn
	}
	s.mu.Unlock()
	if busy {
		conn.WriteJSON(ServerMessage{Type: "status", Payload: "A game is already in progress. Try again later."})
		return
	}
	defer func() {
		s.mu.Lock()
		s.player = nil
		s.mu.Unlock()
	}()

	// Every connection is a new game, even when the scenario is unchanged.
	scenario := r.URL.Query().Get("scenario")
	if scenario == "" {
		scenario = s.llmProxy.Scenario()
	}
	if err := s.llmProxy.LoadScenario(scenario); err != nil {
		log.Printf("Failed to load scenario %q: %v", scenario, err)
		conn.WriteJSON(ServerMessage{Type: "status", Payload: "Unknown scenario."})
		return
	}

	// Set WebSocket deadlines: long read timeout to survive silence,
	// pong handler resets the deadline on each pong.
	ws.SetReadDeadline(time.Now().Add(120 * time.Second))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	conn.WriteJSON(ServerMessage{Type: "status", Payload: "AI Engine Ready"})

	// Establish the AI bridge. Persona, intro and game state are owned by the
	// LLM proxy (see internal/ai/llmproxy.go); Unmute only needs a session.
	unmute := ai.NewUnmuteClient(s.unmuteURL, s.unmuteVoice)
	defer unmute.Close()

	// Cinematic Intro in a goroutine to ensure Full-Duplex remains unblocked
	go func() {
		conn.WriteJSON(ServerMessage{Type: "sfx_trigger", Payload: "sfx_depressurization"})

		// r.Context() ends when this handler returns, aborting a dial in progress.
		if err := unmute.Connect(r.Context()); err != nil {
			log.Printf("Failed to connect to Unmute WS at %s: %v", s.unmuteURL, err)
			conn.WriteJSON(ServerMessage{Type: "status", Payload: "Failed to connect to the AI engine."})
			ws.Close() // a game without a voice is no game: free the slot
			return
		}

		go unmute.ReceiveLoop(conn)

		// Wait for the intro to be spoken (approx 15s) before revealing
		// the game state UI to the player.
		time.Sleep(15 * time.Second)
		conn.WriteJSON(map[string]interface{}{"type": "game_state", "payload": s.llmProxy.Engine.GameStateForClient()})
	}()

	log.Println("New WebSocket connection established and fully initialized")

	// Ping ticker: sends a ping every 30s to keep the connection alive
	// through proxies and load balancers during silence periods.
	pingDone := make(chan struct{})
	defer close(pingDone)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.writePing(); err != nil {
					log.Printf("WebSocket ping error: %v", err)
					return
				}
			case <-pingDone:
				return
			}
		}
	}()

	// Handle incoming player streams (Audio & Events)
	for {
		messageType, p, err := ws.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error/disconnect: %v", err)
			break
		}
		// Reset read deadline on any message (player is active)
		ws.SetReadDeadline(time.Now().Add(120 * time.Second))

		// The frontend sends audio as JSON {"type": "audio_chunk", "payload": "<base64>"};
		// raw binary Opus is accepted too.
		if messageType == websocket.TextMessage {
			unmute.SendAudioJSON(p)
		} else if messageType == websocket.BinaryMessage {
			unmute.SendAudioBinary(p)
		}
	}
}

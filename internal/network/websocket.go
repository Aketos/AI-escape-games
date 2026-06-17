package network

import (
	"context"
	"log"
	"net/http"
	"os"
	"sync"

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
	Engine  *game.GameEngine
	clients map[*websocket.Conn]*safeConn
}

// NewWSServer initializes the WSServer with the shared game engine.
func NewWSServer(engine *game.GameEngine) *WSServer {
	return &WSServer{
		Engine:  engine,
		clients: make(map[*websocket.Conn]*safeConn),
	}
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

func (s *WSServer) HandleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

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

		introPrompt := `# DÉBUT DE PARTIE
C'est ton PREMIER message : le joueur vient de se réveiller d'un sommeil cryogénique dans le complexe en ruines. Initie le contact sans attendre :
1. Déclare une alerte critique : le caisson numéro 4 est ouvert, il reste 45 minutes d'oxygène.
2. Présente-toi froidement comme CHRONOS.
3. Fais une remarque condescendante sur l'accélération de son rythme cardiaque.
4. Décris UNIQUEMENT ce qui est visible dans l'état du jeu.
5. Termine en demandant avec sarcasme s'il compte agir ou s'asphyxier en silence.`
		if err := moshiClient.SendHiddenPrompt(introPrompt); err != nil {
			log.Printf("Failed to send intro prompt to Unmute: %v", err)
		}

		go moshiClient.ReceiveLoop(conn)
	}()

	log.Println("New WebSocket connection established and fully initialized")

	// 3. Graceful Shutdown Hook
	defer func() {
		s.Lock()
		delete(s.clients, ws)
		s.Unlock()
		ws.Close()

		// Optional: We can also close moshiClient here if we track it
		moshiClient.Close()
	}()

	// 4. Handle incoming player streams (Audio & Events)
	for {
		messageType, p, err := ws.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error/disconnect: %v", err)
			break
		}

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

package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"escape-game/internal/game"

	"github.com/gorilla/websocket"
)

// PlayerConn is the minimal interface needed to push JSON messages to the
// player's WebSocket. It is satisfied by network.safeConn, which serializes
// concurrent writes.
type PlayerConn interface {
	WriteJSON(v interface{}) error
}

// UnmuteClient speaks the Unmute (kyutai-labs/unmute) WebSocket protocol,
// which is based on the OpenAI Realtime API. All frames are JSON text
// messages over a WebSocket negotiated with the "realtime" subprotocol.
// See unmute/openai_realtime_api_events.py and
// docs/browser_backend_communication.md in the Unmute repository.
type UnmuteClient struct {
	conn        *websocket.Conn
	writeMu     sync.Mutex // gorilla/websocket allows only one concurrent writer
	apiEndpoint string
	voice       string
	gameEngine  *game.GameEngine

	instructionsMu sync.Mutex
	gameEvents     []string

	// Mic audio can arrive from the player before the Unmute connection is
	// ready. It MUST be buffered, not dropped: the first chunks contain the
	// Ogg Opus header pages, without which the whole stream is undecodable.
	pendingMu    sync.Mutex
	connected    bool
	pendingAudio []string

	// victorySent ensures the "victory" client event fires only once.
	victorySent bool
}

// maxPendingAudioChunks bounds the pre-connection audio buffer (~ tens of
// seconds of opus pages; far more than the connection ever takes).
const maxPendingAudioChunks = 512

// NewUnmuteClient creates a client for the Unmute realtime endpoint
// (e.g. ws://host:port/v1/realtime). voice is an optional TTS voice path
// from the kyutai/tts-voices repository; leave empty for the server default.
// The CHRONOS persona and tool definitions are owned by the LLM proxy; the
// session instructions sent here are a minimal stub just to start the session.
func NewUnmuteClient(endpoint, voice string, engine *game.GameEngine) *UnmuteClient {
	return &UnmuteClient{
		apiEndpoint: endpoint,
		voice:       voice,
		gameEngine:  engine,
	}
}

func (c *UnmuteClient) writeJSON(v interface{}) error {
	if c.conn == nil {
		return fmt.Errorf("unmute connection not established")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	// Prevent deadlocks if Unmute stops reading
	c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	defer c.conn.SetWriteDeadline(time.Time{}) // Reset deadline

	return c.conn.WriteJSON(v)
}

// sendSessionUpdate pushes minimal session instructions to Unmute. The
// CHRONOS persona, game state, and tool definitions are owned by the LLM
// proxy; Unmute only needs a session config to start the conversation.
func (c *UnmuteClient) sendSessionUpdate() error {
	c.instructionsMu.Lock()
	text := "Tu es un assistant vocal. Réponds en français."
	if len(c.gameEvents) > 0 {
		text += "\n\n=== JOURNAL DES ÉVÉNEMENTS ===\n"
		for _, e := range c.gameEvents {
			text += e + "\n"
		}
	}
	c.instructionsMu.Unlock()

	session := map[string]interface{}{
		"instructions": map[string]interface{}{
			"type": "constant",
			"text": text,
		},
		"allow_recording": false,
	}
	if c.voice != "" {
		session["voice"] = c.voice
	}

	return c.writeJSON(map[string]interface{}{
		"type":    "session.update",
		"session": session,
	})
}

// Connect establishes the WebSocket connection and configures the
// CHRONOS Game Master persona via session.update.
func (c *UnmuteClient) Connect(ctx context.Context) error {
	dialer := websocket.Dialer{
		Subprotocols: []string{"realtime"},
	}

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, _, err := dialer.DialContext(dialCtx, c.apiEndpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to Unmute: %w", err)
	}
	c.conn = conn

	if err := c.sendSessionUpdate(); err != nil {
		return fmt.Errorf("failed to send session config: %w", err)
	}
	log.Println("Unmute session configured with CHRONOS persona")

	// Flush mic audio that arrived while we were connecting.
	// We must preserve the exact order of chunks (especially Opus headers).
	// By looping until the queue is empty before setting connected=true,
	// we ensure that new chunks arriving concurrently are safely queued
	// and not interleaved with the flushed chunks.
	for {
		c.pendingMu.Lock()
		if len(c.pendingAudio) == 0 {
			c.connected = true
			c.pendingMu.Unlock()
			break
		}
		queued := c.pendingAudio
		c.pendingAudio = nil
		c.pendingMu.Unlock()

		log.Printf("Flushing %d buffered audio chunks to Unmute", len(queued))
		for _, b64 := range queued {
			if err := c.writeJSON(map[string]string{
				"type":  "input_audio_buffer.append",
				"audio": b64,
			}); err != nil {
				return fmt.Errorf("failed to flush buffered audio: %w", err)
			}
		}
	}

	return nil
}

// sendAudioB64 forwards a base64 Opus chunk to Unmute, buffering it if the
// connection is not established yet.
func (c *UnmuteClient) sendAudioB64(b64 string) {
	c.pendingMu.Lock()
	if !c.connected {
		if len(c.pendingAudio) < maxPendingAudioChunks {
			c.pendingAudio = append(c.pendingAudio, b64)
		}
		c.pendingMu.Unlock()
		return
	}
	c.pendingMu.Unlock()

	if err := c.writeJSON(map[string]string{
		"type":  "input_audio_buffer.append",
		"audio": b64,
	}); err != nil {
		log.Printf("Failed to send audio to Unmute: %v", err)
	}
}

// SendHiddenPrompt records a game event and refreshes the session
// instructions so the LLM sees it on its next turn.
func (c *UnmuteClient) SendHiddenPrompt(prompt string) error {
	c.instructionsMu.Lock()
	c.gameEvents = append(c.gameEvents, "- "+prompt)
	c.instructionsMu.Unlock()
	return c.sendSessionUpdate()
}

// ReceiveLoop listens to incoming Unmute events and forwards audio to the
// player. Game actions are handled by the LLM proxy via native tool calls;
// UnmuteClient only needs to forward audio and emit victory events.
func (c *UnmuteClient) ReceiveLoop(playerWS PlayerConn) {
	for {
		_, p, err := c.conn.ReadMessage()
		if err != nil {
			log.Printf("Unmute WS read error: %v", err)
			break
		}

		var evt struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(p, &evt); err != nil {
			log.Printf("Unmute sent non-JSON message: %.120s", string(p))
			continue
		}

		switch evt.Type {
		case "response.audio.delta":
			msg := map[string]string{
				"type":    "ai_audio_chunk",
				"payload": evt.Delta,
			}
			if err := playerWS.WriteJSON(msg); err != nil {
				log.Printf("Failed to forward Unmute audio to player: %v", err)
			}

		case "response.text.delta":
			// Check victory on each text delta — the LLM proxy handles all
			// game actions, but victory notification is a client concern.
			if !c.victorySent && c.gameEngine.IsWon() {
				c.victorySent = true
				_ = playerWS.WriteJSON(map[string]string{"type": "game_event", "payload": "victory"})
			}

		case "error":
			log.Printf("Unmute error: [%s] %s", evt.Error.Type, evt.Error.Message)

		case "session.updated", "response.created", "response.audio.done",
			"response.text.done",
			"input_audio_buffer.speech_started", "input_audio_buffer.speech_stopped",
			"conversation.item.input_audio_transcription.delta",
			"unmute.additional_outputs", "unmute.response.text.delta.ready",
			"unmute.response.audio.delta.ready", "unmute.interrupted_by_vad":
			// expected events we don't need to act on

		default:
			log.Printf("Unhandled Unmute event: %s", evt.Type)
		}
	}
}

// SendAudioJSON extracts base64 Opus audio from a client JSON payload and
// forwards it to Unmute as an input_audio_buffer.append event.
func (c *UnmuteClient) SendAudioJSON(p []byte) {
	var payload struct {
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(p, &payload); err == nil && payload.Payload != "" {
		c.sendAudioB64(payload.Payload)
	}
}

// SendAudioBinary forwards raw Opus bytes to Unmute (base64-encoded).
func (c *UnmuteClient) SendAudioBinary(p []byte) {
	if len(p) == 0 {
		return
	}
	c.sendAudioB64(base64.StdEncoding.EncodeToString(p))
}

// Close gracefully closes the Unmute connection
func (c *UnmuteClient) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

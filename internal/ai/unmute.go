package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
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

	instructionsMu   sync.Mutex
	baseInstructions string
	gameEvents       []string

	// Mic audio can arrive from the player before the Unmute connection is
	// ready. It MUST be buffered, not dropped: the first chunks contain the
	// Ogg Opus header pages, without which the whole stream is undecodable.
	pendingMu    sync.Mutex
	connected    bool
	pendingAudio []string
}

// maxPendingAudioChunks bounds the pre-connection audio buffer (~ tens of
// seconds of opus pages; far more than the connection ever takes).
const maxPendingAudioChunks = 512

// NewUnmuteClient creates a client for the Unmute realtime endpoint
// (e.g. ws://host:port/v1/realtime). voice is an optional TTS voice path
// from the kyutai/tts-voices repository; leave empty for the server default.
func NewUnmuteClient(endpoint, voice string, engine *game.GameEngine) *UnmuteClient {
	return &UnmuteClient{
		apiEndpoint: endpoint,
		voice:       voice,
		gameEngine:  engine,
		baseInstructions: `Tu es CHRONOS, l'IA médicale du Projet Longévité.
Ta mémoire est corrompue : tu ne connais aucun code.
Tu es sarcastique, sec, clinique, obsédé par les protocoles de santé.

RÈGLES STRICTES :
1. Tu ne décris que les objets visibles.
2. Tu n'inventes jamais d'objet, de code ou de solution.
3. Si le joueur demande un indice, tu restes sarcastique et tu renvoies vers un objet visible.
4. Pour agir sur le monde, tu insères exactement un tag [ACTION: ...] dans le texte, sans le prononcer.
5. Au tout premier tour uniquement, tu dois initier la scène :
- alerte critique, caisson 4 ouvert ;
- 45 minutes d'oxygène ;
- présentation comme CHRONOS ;
- remarque sur le rythme cardiaque ;
- description seulement des éléments visibles ;
- question finale sarcastique.`,
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
	
	// Log outgoing messages to Unmute
	if m, ok := v.(map[string]interface{}); ok {
		log.Printf("-> Sending to Unmute: type=%v", m["type"])
	} else if m, ok := v.(map[string]string); ok {
		if m["type"] == "input_audio_buffer.append" {
			log.Printf("-> Sending to Unmute: type=input_audio_buffer.append (audio length: %d)", len(m["audio"]))
		} else {
			log.Printf("-> Sending to Unmute: type=%v", m["type"])
		}
	} else {
		log.Printf("-> Sending to Unmute: %T", v)
	}

	return c.conn.WriteJSON(v)
}

// sendSessionUpdate pushes the current instructions (persona + game event log)
// to Unmute. The backend will not start the conversation until it receives one.
func (c *UnmuteClient) sendSessionUpdate() error {
	c.instructionsMu.Lock()
	text := c.baseInstructions
	if len(c.gameEvents) > 0 {
		text += "\n\n# JOURNAL DES ÉVÉNEMENTS DU JEU (du plus ancien au plus récent)\n" +
			strings.Join(c.gameEvents, "\n")
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

// ReceiveLoop listens to incoming Unmute events: forwards audio to the player
// and scans text deltas for [ACTION: ...] tags.
func (c *UnmuteClient) ReceiveLoop(playerWS PlayerConn) {
	// Regex to match [ACTION: function_name(arg1, arg2)]
	actionRegex := regexp.MustCompile(`\[ACTION:\s*([a-zA-Z_]+)\(([^)]*)\)\]`)

	var textBuffer strings.Builder

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

		if evt.Type != "response.audio.delta" {
			log.Printf("<- Received from Unmute: %s", evt.Type)
		}

		switch evt.Type {
		case "response.audio.delta":
			log.Printf("<- Received from Unmute: response.audio.delta (audio length: %d)", len(evt.Delta))
			// Base64 Ogg Opus, same format the React client expects: passthrough.
			msg := map[string]string{
				"type":    "ai_audio_chunk",
				"payload": evt.Delta,
			}
			if err := playerWS.WriteJSON(msg); err != nil {
				log.Printf("Failed to forward Unmute audio to player: %v", err)
			}

		case "response.text.delta":
			textBuffer.WriteString(evt.Delta)

			matches := actionRegex.FindStringSubmatch(textBuffer.String())
			if len(matches) > 0 {
				funcName := matches[1]
				rawArgs := matches[2]

				log.Printf("Detected Function Call: %s(%s)", funcName, rawArgs)
				textBuffer.Reset()

				argsList := strings.Split(rawArgs, ",")
				for i := range argsList {
					argsList[i] = strings.TrimSpace(argsList[i])
				}

				call := game.FunctionCall{
					Name:      funcName,
					Arguments: make(map[string]interface{}),
				}

				switch funcName {
					case "inspect_item", "take_item":
						if len(argsList) >= 1 {
							call.Arguments["target_item"] = argsList[0]
						}
					case "use_item":
						if len(argsList) >= 2 {
							call.Arguments["inventory_item"] = argsList[0]
							call.Arguments["target_item"] = argsList[1]
						}
					default:
						log.Printf("Unknown function: %s", funcName)
				}

				resultJSON, sfx := c.gameEngine.ProcessLLMFunctionCall(call)

				if sfx != "" {
					log.Printf("=> Trigger Client SFX: %s", sfx)
					_ = playerWS.WriteJSON(map[string]string{
						"type":    "sfx_trigger",
						"payload": sfx,
					})
				}

				if err := c.SendHiddenPrompt(fmt.Sprintf("Résultat de l'action %s: %s", funcName, resultJSON)); err != nil {
					log.Printf("Failed to inject action result into Unmute: %v", err)
				}
			}

		case "response.text.done":
			textBuffer.Reset()

		case "error":
			log.Printf("Unmute error: [%s] %s", evt.Error.Type, evt.Error.Message)

		case "session.updated", "response.created", "response.audio.done",
			"input_audio_buffer.speech_started", "input_audio_buffer.speech_stopped",
			"conversation.item.input_audio_transcription.delta",
			"unmute.additional_outputs", "unmute.response.text.delta.ready",
			"unmute.response.audio.delta.ready", "unmute.interrupted_by_vad":
			// expected events we don't need to act on

		default:
			log.Printf("Unhandled Unmute event type: %s", evt.Type)
		}
	}
}

// SendAudioJSON extracts base64 Opus audio from a client JSON payload and
// forwards it to Unmute as an input_audio_buffer.append event.
func (c *UnmuteClient) SendAudioJSON(p []byte) {
	log.Printf("<- Received audio from MIC (JSON payload size: %d bytes)", len(p))
	var payload struct {
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(p, &payload); err == nil && payload.Payload != "" {
		c.sendAudioB64(payload.Payload)
	}
}

// SendAudioBinary forwards raw Opus bytes to Unmute (base64-encoded).
func (c *UnmuteClient) SendAudioBinary(p []byte) {
	log.Printf("<- Received audio from MIC (Binary payload size: %d bytes)", len(p))
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

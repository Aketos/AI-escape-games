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

// Moshi binary protocol tags. Every WebSocket frame exchanged with moshi-server
// is a BINARY message whose first byte identifies the payload kind.
// See kyutai-labs/moshi client/src/protocol/{types,encoder}.ts
const (
	moshiTagHandshake byte = 0x00 // server -> client, sent once after connect
	moshiTagAudio     byte = 0x01 // Opus audio bytes (both directions)
	moshiTagText      byte = 0x02 // UTF-8 text tokens (transcript)
	moshiTagControl   byte = 0x03 // control: start/endTurn/pause/restart
	moshiTagMetadata  byte = 0x04 // JSON metadata
	moshiTagError     byte = 0x05 // UTF-8 error message
	moshiTagPing      byte = 0x06 // keepalive
)

// MoshiClient handles the full-duplex WebSocket connection to a raw Moshi server.
type MoshiClient struct {
	conn        *websocket.Conn
	writeMu     sync.Mutex // gorilla/websocket allows only one concurrent writer
	apiEndpoint string
	gameEngine  *game.GameEngine
}

func NewMoshiClient(endpoint string, engine *game.GameEngine) *MoshiClient {
	return &MoshiClient{
		apiEndpoint: endpoint,
		gameEngine:  engine,
	}
}

// writeFrame sends a tagged binary frame to Moshi, serializing concurrent writers.
func (c *MoshiClient) writeFrame(tag byte, payload []byte) error {
	if c.conn == nil {
		return fmt.Errorf("moshi connection not established")
	}
	frame := make([]byte, 0, len(payload)+1)
	frame = append(frame, tag)
	frame = append(frame, payload...)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(websocket.BinaryMessage, frame)
}

// Connect establishes the WebSocket connection, waits for the server handshake
// and initializes the Game Master persona.
func (c *MoshiClient) Connect(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.apiEndpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to Moshi: %w", err)
	}
	c.conn = conn

	// Moshi sends a handshake frame (0x00) once the model is ready.
	// Nothing we send before it is taken into account.
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		messageType, p, err := conn.ReadMessage()
		if err != nil {
			conn.Close()
			return fmt.Errorf("failed waiting for Moshi handshake: %w", err)
		}
		if messageType == websocket.BinaryMessage && len(p) > 0 && p[0] == moshiTagHandshake {
			break
		}
		log.Printf("Ignoring pre-handshake Moshi frame (type=%d, len=%d)", messageType, len(p))
	}
	_ = conn.SetReadDeadline(time.Time{})
	log.Println("Moshi handshake received, session ready")

	// Send System Prompt initializing the Game Master persona as a text frame.
	systemPrompt := `Tu es CHRONOS, l'IA médicale du 'Projet Longévité'. Ta mémoire est corrompue (tu ne connais aucun code). Tu es sarcastique et obsédé par les protocoles de santé.
RÈGLES STRICTES :
1. Tu ne vois QUE les objets avec 'visible: true'. Ne parle JAMAIS d'objets cachés.
2. N'invente jamais d'objets ou de solutions.
3. Si le joueur demande un indice, sois sarcastique et suggère d'inspecter un objet visible.
4. To interact with the game world, you MUST output a text tag in your transcript. Example: If the player asks to inspect the coat, you must output exactly '[ACTION: inspect_item(blouse_scientifique)]'. Do not pronounce this tag.`

	if err := c.SendHiddenPrompt(systemPrompt); err != nil {
		return fmt.Errorf("failed to send system prompt: %w", err)
	}

	return nil
}

// SendHiddenPrompt injects a text prompt silently into Moshi's context stream without audio.
func (c *MoshiClient) SendHiddenPrompt(prompt string) error {
	return c.writeFrame(moshiTagText, []byte(prompt))
}

// ReceiveLoop listens to incoming text tokens and audio from Moshi.
func (c *MoshiClient) ReceiveLoop(playerWS *websocket.Conn) {
	// Regex to match [ACTION: function_name(arg1, arg2)]
	actionRegex := regexp.MustCompile(`\[ACTION:\s*([a-zA-Z_]+)\(([^)]*)\)\]`)

	var textBuffer strings.Builder

	for {
		messageType, p, err := c.conn.ReadMessage()
		if err != nil {
			log.Printf("Moshi WS read error: %v", err)
			break
		}

		// Moshi only sends tagged binary frames.
		if messageType != websocket.BinaryMessage || len(p) == 0 {
			continue
		}

		tag, payload := p[0], p[1:]

		switch tag {
		case moshiTagAudio:
			// Route Opus audio chunks to the player's WS connection as base64 JSON.
			b64 := base64.StdEncoding.EncodeToString(payload)
			msg := map[string]string{
				"type":    "ai_audio_chunk",
				"payload": b64,
			}
			if err := playerWS.WriteJSON(msg); err != nil {
				log.Printf("Failed to forward Moshi audio to player: %v", err)
			}

		case moshiTagText:
			token := string(payload)
			log.Printf("Moshi text token: %q", token)
			textBuffer.WriteString(token)

			// Check if the buffer contains our ACTION tag
			matches := actionRegex.FindStringSubmatch(textBuffer.String())
			if len(matches) > 0 {
				funcName := matches[1]
				rawArgs := matches[2]

				log.Printf("Detected Function Call: %s(%s)", funcName, rawArgs)

				// Clear buffer once action is detected
				textBuffer.Reset()

				// Parse Arguments
				argsList := strings.Split(rawArgs, ",")
				for i := range argsList {
					argsList[i] = strings.TrimSpace(argsList[i])
				}

				// Map to our GameEngine FunctionCall struct
				call := game.FunctionCall{
					Name:      funcName,
					Arguments: make(map[string]interface{}),
				}

				if funcName == "inspect_item" || funcName == "take_item" {
					if len(argsList) >= 1 {
						call.Arguments["target_item"] = argsList[0]
					}
				} else if funcName == "use_item" {
					if len(argsList) >= 2 {
						call.Arguments["inventory_item"] = argsList[0]
						call.Arguments["target_item"] = argsList[1]
					}
				}

				// Process the function call via the GameEngine
				resultJSON, sfx := c.gameEngine.ProcessLLMFunctionCall(call)

				// Send SFX event to client over the network channels (placeholder log for now)
				if sfx != "" {
					log.Printf("=> Trigger Client SFX: %s", sfx)
				}

				// Inject result back into Moshi's context stream as a text frame
				inject := fmt.Sprintf("Action Result: %s. Briefly describe the outcome.", resultJSON)
				if err := c.SendHiddenPrompt(inject); err != nil {
					log.Printf("Failed to inject action result into Moshi: %v", err)
				}
			}

		case moshiTagError:
			log.Printf("Moshi error frame: %s", string(payload))

		case moshiTagPing:
			// keepalive, nothing to do

		case moshiTagMetadata, moshiTagControl, moshiTagHandshake:
			log.Printf("Moshi frame tag=0x%02x len=%d", tag, len(payload))

		default:
			log.Printf("Unknown Moshi frame tag=0x%02x len=%d", tag, len(payload))
		}
	}
}

// SendAudioJSON extracts base64 audio from a client JSON payload and forwards it to Moshi.
func (c *MoshiClient) SendAudioJSON(p []byte) {
	var payload struct {
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(p, &payload); err == nil && payload.Payload != "" {
		data, err := base64.StdEncoding.DecodeString(payload.Payload)
		if err != nil {
			log.Printf("Invalid base64 audio payload from player: %v", err)
			return
		}
		c.SendAudioBinary(data)
	}
}

// SendAudioBinary forwards Opus audio bytes to Moshi, prefixed with the audio tag.
func (c *MoshiClient) SendAudioBinary(p []byte) {
	if c.conn == nil || len(p) == 0 {
		return
	}
	if err := c.writeFrame(moshiTagAudio, p); err != nil {
		log.Printf("Failed to send audio to Moshi: %v", err)
	}
}

// Close gracefully closes the Moshi connection
func (c *MoshiClient) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

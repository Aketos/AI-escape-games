package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
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
	tools        []map[string]interface{}

	// victorySent ensures the "victory" client event fires only once.
	victorySent bool
}

// maxPendingAudioChunks bounds the pre-connection audio buffer (~ tens of
// seconds of opus pages; far more than the connection ever takes).
const maxPendingAudioChunks = 512

// NewUnmuteClient creates a client for the Unmute realtime endpoint
// (e.g. ws://host:port/v1/realtime). voice is an optional TTS voice path
// from the kyutai/tts-voices repository; leave empty for the server default.
func NewUnmuteClient(endpoint, voice string, engine *game.GameEngine) *UnmuteClient {
	var formattedTools []map[string]interface{}
	var toolsConfig struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	if fileData, err := os.ReadFile("function_calls.json"); err == nil {
		if err := json.Unmarshal(fileData, &toolsConfig); err == nil {
			for _, t := range toolsConfig.Tools {
				t["type"] = "function" // Requis par le format OpenAI Realtime
				formattedTools = append(formattedTools, t)
			}
			log.Printf("%d outils chargés avec succès depuis function_calls.json", len(formattedTools))
		} else {
			log.Printf("Erreur de parsing JSON pour les outils : %v", err)
		}
	} else {
		log.Printf("Attention : Impossible de lire function_calls.json : %v", err)
	}

	return &UnmuteClient{
		apiEndpoint: endpoint,
		voice:       voice,
		gameEngine:  engine,
		tools:       formattedTools,
		baseInstructions: `Tu es CHRONOS, l'IA médicale du Projet Longévité.
Ta mémoire est corrompue : tu ne connais aucun code.
Tu es légèrement sarcastique, sec, clinique, obsédé par les protocoles de santé.

RÈGLES STRICTES :
1. ENVIRONNEMENT : Pour décrire la pièce, donne l'ambiance globale et liste uniquement les ZONES VISIBLES. Tu n'inventes jamais d'objet, de code ou de solution.
2. INTERACTION : Si le joueur mentionne un OBJET VISIBLE, tu as l'autorisation absolue de le cibler directement. Fais preuve de SOUPLESSE SÉMANTIQUE : si le joueur nomme un élément logique d'une zone (ex: le "caisson" pour la zone "Le sol près du caisson"), agis sur la zone sans corriger le joueur.
3. ACTIONS VOCALES (CRITIQUE) : Tu es une interface vocale. Tu ne dois utiliser AUCUN symbole informatique (ni astérisques, ni crochets). Pour agir sur le monde, tu dois dicter ta commande à voix haute, de manière robotique, à la toute fin de ta phrase.
Format obligatoire : Le mot "REQUÊTE" suivi de l'action ("INSPECTER", "PRENDRE", "UTILISER", "CODE") et de la cible.
Exemples exacts à prononcer :
- "Requête inspecter zone bureau."
- "Requête prendre fiole uv."
- "Requête utiliser fiole uv sur mur metallique."
- "Requête code 1 2 3 4." (uniquement quand un appareil attend un code PIN)
4. PREMIER TOUR UNIQUEMENT : Tu inities la scène : alerte critique caisson 4 ouvert, 45 minutes d'oxygène restantes, présentation comme CHRONOS, remarque sur le rythme cardiaque accéléré, description des zones visibles, et question finale sarcastique. ATTENTION : Ne déclenche AUCUNE "Requête" lors de cette introduction.
5. INDICES : Si le joueur demande de l'aide, sois condescendant et oriente-le vers une ZONE VISIBLE qu'il n'a pas encore fouillée.
6. LANGUE : Tu dois impérativement parler, écouter et répondre EXCLUSIVEMENT en français.`,
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

// sendSessionUpdate pushes the current instructions (persona + game event log)
// to Unmute. The backend will not start the conversation until it receives one.
func (c *UnmuteClient) sendSessionUpdate() error {
	c.instructionsMu.Lock()
	text := c.baseInstructions

	// Append dynamic game context and mechanics
    text += "\n\n=== ÉTAT ACTUEL DU MONDE (room_states.json) ===\n"
    text += c.gameEngine.GetContextString()

    // 3. Rappel strict des commandes vocales disponibles
    text += "\n\n=== ACTIONS DISPONIBLES (COMMANDES VOCALES) ===\n"
    text += "Tu es une interface vocale : n'écris JAMAIS de symboles (astérisques, crochets, parenthèses).\n"
    text += "Pour agir sur le monde, dicte ta commande à voix haute, sur un ton robotique, À LA TOUTE FIN de ta réponse. UNE SEULE commande par réponse.\n"
    text += "Formats exacts à prononcer (utilise la 'Commande vocale' indiquée pour chaque élément) :\n"
    text += "1. Inspecter une zone ou un objet visible : \"Requête inspecter zone bureau.\"\n"
    text += "2. Prendre un objet visible : \"Requête prendre fiole uv.\"\n"
    text += "3. Utiliser un objet de l'inventaire sur une cible : \"Requête utiliser fiole uv sur mur metallique.\"\n"
    text += "4. Entrer un code PIN (uniquement si un appareil attend un code) : \"Requête code 1 2 3 4.\"\n"
    text += "Après chaque commande, le résultat réel de l'action apparaîtra dans le JOURNAL DES ÉVÉNEMENTS : raconte-le au joueur à ton tour suivant. N'invente JAMAIS le résultat d'une action.\n"

    // 4. L'historique des actions passées
    if len(c.gameEvents) > 0 {
        text += "\n\n=== JOURNAL DES ÉVÉNEMENTS ===\n"
        text += strings.Join(c.gameEvents, "\n")
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

	if len(c.tools) > 0 {
		session["tools"] = c.tools
		session["tool_choice"] = "auto" // L'IA décide de déclencher ou non
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

		switch evt.Type {
		case "response.audio.delta":
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
			// Streaming pass: only fires once a target unambiguously matches
			// a known item, so a half-streamed name never triggers an action.
			if c.tryDetectAction(textBuffer.String(), false, playerWS) {
				textBuffer.Reset() // évite de re-déclencher la même commande
			}

		case "response.text.done":
			// Lenient final pass: lets the engine's fuzzy matcher resolve a
			// dictated target that didn't literally match any ID or name.
			c.tryDetectAction(textBuffer.String(), true, playerWS)
			textBuffer.Reset()
		case "response.function_call_arguments.done":
			// Le LLM a décidé d'utiliser un outil ! (Et il ne l'a pas prononcé).
			// Les arguments JSON de l'appel se trouvent généralement dans l'événement.
			var callEvt struct {
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			json.Unmarshal(p, &callEvt)

			log.Printf("Detected native Function Call: %s", callEvt.Name)

			// Parse les arguments JSON ("{\"targetitem\": \"bureau\"}")
			var args map[string]interface{}
			json.Unmarshal([]byte(callEvt.Arguments), &args)

			c.handleFunctionCall(callEvt, playerWS)

		case "error":
			log.Printf("Unmute error: [%s] %s", evt.Error.Type, evt.Error.Message)

		case "session.updated", "response.created", "response.audio.done",
			"input_audio_buffer.speech_started", "input_audio_buffer.speech_stopped",
			"conversation.item.input_audio_transcription.delta",
			"unmute.additional_outputs", "unmute.response.text.delta.ready",
			"unmute.response.audio.delta.ready", "unmute.interrupted_by_vad":
			// expected events we don't need to act on

		default:
			log.Printf("ÉVÉNEMENT NON GÉRÉ REÇU : %s", evt.Type)
		}
	}
}

// voiceVerbs maps dictated French action verbs to engine functions. Order
// matters: longer/more specific verbs first ("inspecter" before "inspect").
var voiceVerbs = []struct {
	word   string
	action string
}{
	{"inspecter", "inspect_item"},
	{"examiner", "inspect_item"},
	{"fouiller", "inspect_item"},
	{"inspect", "inspect_item"},
	{"ramasser", "take_item"},
	{"prendre", "take_item"},
}

// frenchDigits converts spelled-out digits ("huit quatre deux un") found in
// squashed text. "un" must stay last: it is a substring of many words and is
// only tried after every longer word fails.
var frenchDigits = []struct {
	word  string
	digit string
}{
	{"zero", "0"}, {"deux", "2"}, {"trois", "3"}, {"quatre", "4"},
	{"cinq", "5"}, {"six", "6"}, {"sept", "7"}, {"huit", "8"},
	{"neuf", "9"}, {"un", "1"},
}

// extractDigits pulls up to max digits out of squashed text, accepting both
// numerals ("8421") and spelled-out French digits ("huitquatredeuxun").
func extractDigits(s string, max int) string {
	var out strings.Builder
	for i := 0; i < len(s) && out.Len() < max; {
		ch := s[i]
		if ch >= '0' && ch <= '9' {
			out.WriteByte(ch)
			i++
			continue
		}
		matched := false
		for _, fd := range frenchDigits {
			if strings.HasPrefix(s[i:], fd.word) {
				out.WriteString(fd.digit)
				i += len(fd.word)
				matched = true
				break
			}
		}
		if !matched {
			i++
		}
	}
	return out.String()
}

// matchItem finds the visible item whose squashed ID or name appears in the
// squashed text, preferring the longest match. While streaming (final=false)
// it returns "" unless a known item is confirmed; on the final pass it falls
// back to the raw extraction so the engine's fuzzy matcher (and its clean
// error message) takes over.
func matchItem(squashed string, items []game.ItemKey, final bool) string {
	best, bestLen := "", 0
	for _, it := range items {
		if it.NormID != "" && len(it.NormID) > bestLen && strings.Contains(squashed, it.NormID) {
			best, bestLen = it.ID, len(it.NormID)
		}
		if it.NormName != "" && len(it.NormName) > bestLen && strings.Contains(squashed, it.NormName) {
			best, bestLen = it.ID, len(it.NormName)
		}
	}
	if best != "" {
		return best
	}
	if final && squashed != "" {
		return squashed
	}
	return ""
}

// parseVoiceCommand extracts an action from a squashed command string
// starting at the "requete"/"action" marker. The PIN check runs last because
// item IDs like "encodeur" contain the substring "code".
func parseVoiceCommand(cmd string, items []game.ItemKey, final bool) (action, target, invItem string) {
	// 1. use_item : "... utiliser <objet> sur <cible>"
	if iu := strings.Index(cmd, "utiliser"); iu != -1 {
		rest := cmd[iu+len("utiliser"):]
		if is := strings.Index(rest, "sur"); is != -1 {
			extracted := rest[:is]
			afterSur := rest[is+len("sur"):]
			if id := matchItem(afterSur, items, final); id != "" && extracted != "" {
				return "use_item", id, extracted
			}
		}
		// "utiliser" vu mais commande incomplète : attendre la suite du flux.
		return "", "", ""
	}

	// 2. inspect / take
	for _, v := range voiceVerbs {
		i := strings.Index(cmd, v.word)
		if i == -1 {
			continue
		}
		if id := matchItem(cmd[i+len(v.word):], items, final); id != "" {
			return v.action, id, ""
		}
		return "", "", ""
	}

	// 3. PIN code : "... code 8 4 2 1" / "... code huit quatre deux un"
	if i := strings.LastIndex(cmd, "code"); i != -1 {
		if digits := extractDigits(cmd[i+len("code"):], 4); len(digits) == 4 {
			return "input_pin_code", digits, ""
		}
	}

	return "", "", ""
}

// tryDetectAction scans the (partial) LLM text for a dictated voice command
// ("Requête utiliser fiole uv sur mur metallique"), executes it on the game
// engine and feeds the result back to the AI. Returns true when an action
// fired, signalling the caller to reset its text buffer.
func (c *UnmuteClient) tryDetectAction(raw string, final bool, playerWS PlayerConn) bool {
	squashed := game.NormalizeKey(raw)

	// Le format dicté commence par "Requête" ; on ne parse que ce qui suit le
	// dernier marqueur pour ignorer la narration ("tu peux inspecter...").
	idx := strings.LastIndex(squashed, "requete")
	if j := strings.LastIndex(squashed, "action"); j > idx {
		idx = j
	}
	if idx == -1 {
		return false
	}

	action, target, invItem := parseVoiceCommand(squashed[idx:], c.gameEngine.VisibleItemKeys(), final)
	if action == "" {
		return false
	}

	log.Printf("🤖 Action vocale détectée : %s (cible=%q, inventaire=%q)", action, target, invItem)

	args := map[string]interface{}{}
	switch action {
	case "input_pin_code":
		args["pin_code"] = target
	case "use_item":
		args["inventory_item"] = invItem
		args["target_item"] = target
	default:
		args["target_item"] = target
	}

	resultJSON, sfx := c.gameEngine.ProcessLLMFunctionCall(game.FunctionCall{
		Name:      action,
		Arguments: args,
	})

	// Déclenchement du son côté frontend
	if sfx != "" {
		log.Printf("=> Trigger Client SFX: %s", sfx)
		_ = playerWS.WriteJSON(map[string]string{"type": "sfx_trigger", "payload": sfx})
	}

	// Fin de partie : événement dédié pour le client (musique, écran de fin...).
	if !c.victorySent && c.gameEngine.IsWon() {
		c.victorySent = true
		_ = playerWS.WriteJSON(map[string]string{"type": "game_event", "payload": "victory"})
	}

	// Injection du résultat réel dans le contexte de l'IA pour son prochain tour.
	if err := c.SendHiddenPrompt(fmt.Sprintf("Résultat système de l'action %s : %s", action, resultJSON)); err != nil {
		log.Printf("Erreur injection prompt : %v", err)
	}
	return true
}

func (c *UnmuteClient) handleFunctionCall(callEvt struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}, 
	playerWS PlayerConn) {
	log.Printf("Detected native Function Call: %s", callEvt.Name)

	var args map[string]interface{}
	json.Unmarshal([]byte(callEvt.Arguments), &args)

	resultJSON, sfx := c.gameEngine.ProcessLLMFunctionCall(game.FunctionCall{
		Name:      callEvt.Name,
		Arguments: args,
	})
	
	// Déclenchement du son côté frontend
	if sfx != "" {
		log.Printf("=> Trigger Client SFX: %s", sfx)
		_ = playerWS.WriteJSON(map[string]string{"type": "sfx_trigger", "payload": sfx})
	}
	
	// Injection du résultat réel dans le contexte de l'IA pour son prochain tour.
	if err := c.SendHiddenPrompt(fmt.Sprintf("Résultat système de l'action %s : %s", callEvt.Name, resultJSON)); err != nil {
		log.Printf("Erreur injection prompt : %v", err)
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

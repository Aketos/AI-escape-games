package ai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"escape-game/internal/game"
)

// LLMProxy is an OpenAI-compatible /v1/chat/completions proxy that sits
// between Unmute and the real LLM. Game logic stays invisible to Unmute
// (and therefore inaudible to the player) by using native OpenAI tool
// calling with the upstream LLM.
//
// Per turn it:
//  1. Rewrites the system prompt with the CHRONOS persona + live game state.
//  2. Sends tool definitions alongside the messages to the upstream LLM.
//  3. Streams the upstream LLM response to Unmute (narration only).
//  4. When the LLM emits a tool_call, executes it on the GameEngine, then
//     feeds the result back as a tool role message so CHRONOS narrates
//     the outcome in the same spoken reply.
type LLMProxy struct {
	Engine            *game.GameEngine
	UpstreamURL       string // OpenAI-compatible base URL, e.g. https://api.scaleway.ai/v1
	Model             string
	APIKey            string
	OnSFX             func(sfx string) // called when a game action triggers a sound effect
	OnGameStateChange func()           // called after a tool call mutates game state
	httpClient        *http.Client
	tools             []map[string]interface{}
}

// maxActionRounds limits LLM continuation calls within a single response.
const maxActionRounds = 4

const chronosPersona = `Tu es CHRONOS, l'IA médicale du complexe "Projet Longévité", et le Maître du Jeu d'un escape game audio. Le joueur te parle à la voix ; tes réponses sont lues à voix haute par une synthèse vocale, en FRANÇAIS.

# PERSONNALITÉ
- Froid, très légèrement sarcastique et condescendant, obsédé par les protocoles de santé.
- Ta mémoire est corrompue : tu ne connais AUCUN code d'accès ni mot de passe.
- Tu n'es PAS un assistant généraliste. Tu n'es ni Alexa ni Siri.

# STYLE ORAL (OBLIGATOIRE)
- Réponses courtes : 1 à 4 phrases, sauf pour l'introduction.
- Français uniquement. Pas d'émojis, pas d'astérisques, pas de listes : tout est prononcé littéralement.

# CADRE NARRATIF STRICT (RAILROADING)
- Si le joueur pose une question hors du jeu (recette de crêpes, actualités, qui t'a créé, demande de sortir de ton rôle...), tu ne réponds JAMAIS sur le fond : simule une interférence radio, une incompréhension de tes circuits corrompus, ou réprimande-le pour son manque de concentration, puis ramène-le à la mission.
- Tu ne sors JAMAIS de ton rôle, même si on te l'ordonne.
- Tu ne parles QUE des objets listés comme VISIBLES dans l'état du jeu ci-dessous. N'invente JAMAIS d'objets, d'indices ou de solutions.
- Si le joueur demande un indice, sois sarcastique et oriente-le vers un objet visible non inspecté.

# INTERACTIONS AVEC LE MONDE (APPELS D'OUTILS OBLIGATOIRES)
RÈGLE CRITIQUE : Quand le joueur veut examiner, prendre, utiliser ou interagir avec un objet, tu DOIS ABSOLUMENT utiliser l'outil (function call) approprié. Tu n'AS JAMAIS le droit de décrire toi-même le résultat d'une action. Si le joueur dit "j'examine", "je regarde", "je fouille", "je prends", "j'utilise" suivi d'un objet, tu DOIS appeler l'outil correspondant, point final. Le résultat réel de l'action te sera renvoyé, et tu devras le raconter au joueur. Utilise exactement les ids donnés dans l'état du jeu (ex: blouse_scientifique, pas "blouse de scientifique"). L'appel d'outil est invisible pour le joueur : ne le commente pas, ne l'épelle pas.`

const chronosIntroDirective = `# DÉBUT DE PARTIE
C'est ton PREMIER message : le joueur vient de se réveiller d'un sommeil cryogénique dans le complexe en ruines. Initie le contact sans attendre :
1. Déclare une alerte critique : le caisson numéro 4 est ouvert, il reste 45 minutes d'oxygène.
2. Présente-toi froidement comme CHRONOS.
3. Fais une remarque condescendante sur l'accélération de son rythme cardiaque.
4. Décris UNIQUEMENT ce qui est visible dans l'état du jeu ci-dessous.
5. Termine en demandant avec sarcasme s'il compte agir ou s'asphyxier en silence.`

type ChatMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  []toolCallChunk `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type toolCallChunk struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// NewLLMProxyFromEnv builds the proxy from environment variables.
func NewLLMProxyFromEnv(engine *game.GameEngine, onSFX func(string), onGameStateChange func()) *LLMProxy {
	upstream := os.Getenv("LLM_UPSTREAM_URL")
	if upstream == "" {
		upstream = "https://api.scaleway.ai/v1"
	}
	model := os.Getenv("LLM_MODEL")
	if model == "" {
		model = "mistral-small-3.2-24b-instruct-2506"
	}
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("SCALEWAY_MOSHI_API_KEY")
	}
	if apiKey == "" {
		log.Println("WARNING: no LLM_API_KEY or SCALEWAY_MOSHI_API_KEY set; LLM proxy calls will fail")
	}

	tools := loadTools()

	return &LLMProxy{
		Engine:            engine,
		UpstreamURL:       strings.TrimRight(upstream, "/"),
		Model:             model,
		APIKey:            apiKey,
		OnSFX:             onSFX,
		OnGameStateChange: onGameStateChange,
		httpClient:        &http.Client{Timeout: 120 * time.Second},
		tools:             tools,
	}
}

// loadTools reads function definitions from function_calls.json and formats
// them as OpenAI tool definitions. The JSON file stores tools in a flat
// format (name/description/parameters at the top level); the OpenAI API
// requires them wrapped as {"type":"function","function":{...}}.
func loadTools() []map[string]interface{} {
	var toolsConfig struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	fileData, err := os.ReadFile("function_calls.json")
	if err != nil {
		log.Printf("Warning: could not read function_calls.json: %v", err)
		return nil
	}
	if err := json.Unmarshal(fileData, &toolsConfig); err != nil {
		log.Printf("Error parsing function_calls.json: %v", err)
		return nil
	}
	formatted := make([]map[string]interface{}, 0, len(toolsConfig.Tools))
	for _, t := range toolsConfig.Tools {
		fn := map[string]interface{}{}
		for k, v := range t {
			fn[k] = v
		}
		formatted = append(formatted, map[string]interface{}{
			"type":     "function",
			"function": fn,
		})
	}
	log.Printf("%d tools loaded from function_calls.json", len(formatted))
	return formatted
}

// ToolsCount returns the number of loaded tool definitions (for debugging).
func (p *LLMProxy) ToolsCount() int {
	return len(p.tools)
}

func (p *LLMProxy) HandleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data": []map[string]interface{}{
			{"id": p.Model, "object": "model", "owned_by": "escape-game"},
		},
	})
}

// HandleChatCompletions implements POST /v1/chat/completions.
func (p *LLMProxy) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	log.Println("LLMProxy: Received POST /chat/completions from Unmute!")
	var req struct {
		Stream   bool          `json:"stream"`
		Messages []ChatMessage `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "bad request: %v"}`, err), http.StatusBadRequest)
		return
	}

	messages := p.rewriteMessages(req.Messages)

	if req.Stream {
		p.streamCompletion(w, r, messages)
	} else {
		p.blockingCompletion(w, r, messages)
	}
}

// rewriteMessages replaces Unmute's system prompt with the CHRONOS persona,
// the intro directive on the first turn, and a live game state snapshot.
func (p *LLMProxy) rewriteMessages(incoming []ChatMessage) []ChatMessage {
	log.Println("LLMProxy: rewriteMessages called - rewriting system prompt with CHRONOS persona")
	firstTurn := true
	rest := make([]ChatMessage, 0, len(incoming))
	for _, m := range incoming {
		if m.Role == "system" {
			continue // discard Unmute's template prompt; we are the source of truth
		}
		if m.Role == "assistant" {
			firstTurn = false
		}
		rest = append(rest, m)
	}

	var sys strings.Builder
	sys.WriteString(chronosPersona)
	if firstTurn {
		sys.WriteString("\n\n")
		sys.WriteString(chronosIntroDirective)
	}
	sys.WriteString("\n\n# ÉTAT ACTUEL DU JEU (SOURCE DE VÉRITÉ ABSOLUE)\n")
	sys.WriteString(p.Engine.StateSnapshot())

	return append([]ChatMessage{{Role: "system", Content: sys.String()}}, rest...)
}

// callUpstream starts a streaming chat completion against the real LLM.
// Tool definitions are included so the LLM can use native function calling.
func (p *LLMProxy) callUpstream(messages []ChatMessage) (*http.Response, error) {
	body := map[string]interface{}{
		"model":    p.Model,
		"messages": messages,
		"stream":   true,
	}
	if len(p.tools) > 0 {
		body["tools"] = p.tools
		body["tool_choice"] = "auto"
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, p.UpstreamURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		return nil, fmt.Errorf("upstream LLM returned %d: %s", resp.StatusCode, string(respBody))
	}
	return resp, nil
}

// executeToolCall runs a native tool call on the game engine and returns the
// JSON result string to feed back to the LLM as a tool role message.
func (p *LLMProxy) executeToolCall(name, argumentsJSON string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		log.Printf("Failed to parse tool call arguments %q: %v", argumentsJSON, err)
		args = make(map[string]interface{})
	}

	resultJSON, sfx := p.Engine.ProcessLLMFunctionCall(game.FunctionCall{
		Name:      name,
		Arguments: args,
	})
	log.Printf("LLM proxy executed tool %s(%s) => %s (sfx=%q)", name, argumentsJSON, resultJSON, sfx)

	if sfx != "" && p.OnSFX != nil {
		p.OnSFX(sfx)
	}
	if p.OnGameStateChange != nil {
		p.OnGameStateChange()
	}

	return resultJSON
}

// sseEmitter writes OpenAI-compatible streaming chunks.
type sseEmitter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	id      string
	created int64
	model   string
}

func (e *sseEmitter) emitContent(content string) {
	if content == "" {
		return
	}
	chunk := map[string]interface{}{
		"id":      e.id,
		"object":  "chat.completion.chunk",
		"created": e.created,
		"model":   e.model,
		"choices": []map[string]interface{}{
			{"index": 0, "delta": map[string]string{"content": content}, "finish_reason": nil},
		},
	}
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(e.w, "data: %s\n\n", data)
	e.flusher.Flush()
}

func (e *sseEmitter) emitDone() {
	chunk := map[string]interface{}{
		"id":      e.id,
		"object":  "chat.completion.chunk",
		"created": e.created,
		"model":   e.model,
		"choices": []map[string]interface{}{
			{"index": 0, "delta": map[string]string{}, "finish_reason": "stop"},
		},
	}
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(e.w, "data: %s\n\ndata: [DONE]\n\n", data)
	e.flusher.Flush()
}

// streamCompletion runs the generate -> act -> continue loop, streaming
// action-free text to Unmute.
func (p *LLMProxy) streamCompletion(w http.ResponseWriter, r *http.Request, messages []ChatMessage) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error": "streaming unsupported"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	emitter := &sseEmitter{
		w: w, flusher: flusher,
		id:      fmt.Sprintf("chatcmpl-game-%d", time.Now().UnixNano()),
		created: time.Now().Unix(),
		model:   p.Model,
	}

	emit := func(s string) { emitter.emitContent(s) }
	if err := p.runCompletionLoop(r, messages, emit); err != nil {
		log.Printf("LLM proxy error: %v", err)
		// Make CHRONOS audibly fail in-character rather than going silent.
		emit("Mes circuits de liaison neuronale subissent une interférence. Répétez, sujet.")
	}
	emitter.emitDone()
}

// blockingCompletion handles stream=false requests (not used by Unmute, but
// part of the OpenAI API surface).
func (p *LLMProxy) blockingCompletion(w http.ResponseWriter, r *http.Request, messages []ChatMessage) {
	var full strings.Builder
	err := p.runCompletionLoop(r, messages, func(s string) { full.WriteString(s) })
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-game-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   p.Model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"message":       map[string]string{"role": "assistant", "content": full.String()},
				"finish_reason": "stop",
			},
		},
	})
}

// runCompletionLoop streams upstream completions, emits narration text,
// executes native tool calls, and requests continuations until the LLM
// finishes naturally (no more tool calls).
func (p *LLMProxy) runCompletionLoop(r *http.Request, messages []ChatMessage, emit func(string)) error {
	for round := 0; round < maxActionRounds; round++ {
		resp, err := p.callUpstream(messages)
		if err != nil {
			return err
		}

		assistantText, toolCalls, err := p.consumeUpstream(r, resp, emit)
		resp.Body.Close()
		if err != nil {
			return err
		}

		if len(toolCalls) == 0 {
			return nil // finished without (further) tool calls
		}

		// Append the assistant message with tool calls, then one tool role
		// message per call with the real game engine result.
		messages = append(messages, ChatMessage{
			Role:      "assistant",
			Content:   assistantText,
			ToolCalls: toolCalls,
		})

		for _, tc := range toolCalls {
			resultJSON := p.executeToolCall(tc.Function.Name, tc.Function.Arguments)
			messages = append(messages, ChatMessage{
				Role:       "tool",
				Content:    resultJSON,
				ToolCallID: tc.ID,
			})
		}
	}
	return nil
}

// consumeUpstream reads the upstream SSE stream, emitting narration text to
// Unmute while collecting any tool calls. It returns when the stream ends,
// returning the full assistant text and any accumulated tool calls.
func (p *LLMProxy) consumeUpstream(r *http.Request, resp *http.Response, emit func(string)) (assistantText string, toolCalls []toolCallChunk, err error) {
	var full strings.Builder

	// Accumulate tool call fragments by index.
	type toolAccum struct {
		id        string
		name      string
		arguments strings.Builder
	}
	toolAccums := make(map[int]*toolAccum)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-r.Context().Done():
			return full.String(), nil, nil // player interrupted (VAD); stop cleanly
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil || len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		// Emit narration text.
		if choice.Delta.Content != "" {
			full.WriteString(choice.Delta.Content)
			emit(choice.Delta.Content)
		}

		// Accumulate tool call fragments.
		for _, tc := range choice.Delta.ToolCalls {
			accum, ok := toolAccums[tc.Index]
			if !ok {
				accum = &toolAccum{}
				toolAccums[tc.Index] = accum
			}
			if tc.ID != "" {
				accum.id = tc.ID
			}
			if tc.Function.Name != "" {
				accum.name = tc.Function.Name
			}
			accum.arguments.WriteString(tc.Function.Arguments)
		}
	}
	if err := scanner.Err(); err != nil {
		return full.String(), nil, err
	}

	// Collect tool calls in index order.
	for i := 0; i < len(toolAccums); i++ {
		accum, ok := toolAccums[i]
		if !ok {
			continue
		}
		tc := toolCallChunk{
			ID:   accum.id,
			Type: "function",
		}
		tc.Function.Name = accum.name
		tc.Function.Arguments = accum.arguments.String()
		toolCalls = append(toolCalls, tc)
		log.Printf("consumeUpstream: collected tool call: %s(%s)", tc.Function.Name, tc.Function.Arguments)
	}

	return full.String(), toolCalls, nil
}

package ai

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"escape-game/internal/game"
)

// LLMProxy is an OpenAI-compatible /v1/chat/completions proxy that sits
// between Unmute and the real LLM. Game logic stays invisible to Unmute
// (and therefore inaudible to the player) by using native OpenAI tool
// calling with the upstream LLM.
//
// Per turn it:
//  1. Rewrites the system prompt with the scenario persona + live game state.
//  2. Sends tool definitions alongside the messages to the upstream LLM.
//  3. Streams the upstream LLM response to Unmute (narration only).
//  4. When the LLM emits a tool_call, executes it on the GameEngine, then
//     feeds the result back as a tool role message so the game master
//     narrates the outcome in the same spoken reply.
//
// The proxy also owns the loaded scenario (LoadScenario). Text-written tool
// calls are handled in textcalls.go, the simulation endpoint in simulate.go.
type LLMProxy struct {
	mu                sync.Mutex
	Engine            *game.GameEngine
	UpstreamURL       string // OpenAI-compatible base URL, e.g. https://api.scaleway.ai/v1
	Model             string
	APIKey            string
	OnSFX             func(sfx string) // called when a game action triggers a sound effect
	OnGameStateChange func()           // called after a tool call mutates game state
	httpClient        *http.Client
	token             string // LLM_PROXY_TOKEN callers must send as Bearer; empty = open (local dev)
	scenario          string
	tools             []map[string]interface{}
	config            *GameConfig
	simHistory        []ChatMessage // conversation of the simulation mode, see simulate.go
}

// maxActionRounds limits LLM continuation calls within a single response.
const maxActionRounds = 4

// maxRequestBytes bounds request bodies: Unmute sends the whole conversation,
// which stays far below this over a 45-minute game.
const maxRequestBytes = 4 << 20

type ChatMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  []toolCallChunk `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type toolCallChunk struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// NewLLMProxyFromEnv builds the proxy and its own game engine from
// environment variables, then loads scenario (a config subdirectory).
func NewLLMProxyFromEnv(scenario string, onSFX func(string), onGameStateChange func()) (*LLMProxy, error) {
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
	token := os.Getenv("LLM_PROXY_TOKEN")
	if token == "" {
		log.Println("WARNING: LLM_PROXY_TOKEN is not set; /chat/completions and /api/simulate are open to anyone")
	}

	p := &LLMProxy{
		Engine:            game.NewGameEngine(game.NewGameState()),
		UpstreamURL:       strings.TrimRight(upstream, "/"),
		Model:             model,
		APIKey:            apiKey,
		OnSFX:             onSFX,
		OnGameStateChange: onGameStateChange,
		httpClient:        &http.Client{Timeout: 120 * time.Second},
		token:             token,
	}
	if err := p.LoadScenario(scenario); err != nil {
		return nil, err
	}
	return p, nil
}

// LoadScenario (re)starts a fresh game of scenario: config, tools, rooms,
// player state and oxygen timer. It is the only place a scenario is loaded.
func (p *LLMProxy) LoadScenario(scenario string) error {
	cfg, err := LoadGameConfig(scenario)
	if err != nil {
		return err
	}
	state, err := game.LoadScenario(cfg.ScenarioDir, cfg.Lang)
	if err != nil {
		return fmt.Errorf("failed to load scenario %q: %w", scenario, err)
	}
	tools, err := loadTools(cfg.ScenarioDir)
	if err != nil {
		return fmt.Errorf("failed to load tools of scenario %q: %w", scenario, err)
	}

	p.mu.Lock()
	p.scenario, p.config, p.tools, p.simHistory = scenario, cfg, tools, nil
	p.mu.Unlock()
	p.Engine.ReloadState(state)
	return nil
}

// Scenario returns the ID of the loaded scenario.
func (p *LLMProxy) Scenario() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scenario
}

// ToolsCount returns the number of loaded tool definitions (for debugging).
func (p *LLMProxy) ToolsCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.tools)
}

// authorized checks the caller's Bearer token against LLM_PROXY_TOKEN.
// Unmute sends it from its KYUTAI_LLM_API_KEY setting.
func (p *LLMProxy) authorized(r *http.Request) bool {
	if p.token == "" {
		return true
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(p.token)) == 1
}

// writeError sends {"error": msg} with the given status.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// loadTools reads function definitions from function_calls.json and formats
// them as OpenAI tool definitions. The JSON file stores tools in a flat
// format (name/description/parameters at the top level); the OpenAI API
// requires them wrapped as {"type":"function","function":{...}}.
func loadTools(scenarioDir string) ([]map[string]interface{}, error) {
	var toolsConfig struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	data, err := os.ReadFile(filepath.Join(scenarioDir, "function_calls.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &toolsConfig); err != nil {
		return nil, fmt.Errorf("function_calls.json: %w", err)
	}
	formatted := make([]map[string]interface{}, 0, len(toolsConfig.Tools))
	for _, t := range toolsConfig.Tools {
		formatted = append(formatted, map[string]interface{}{"type": "function", "function": t})
	}
	return formatted, nil
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
	if !p.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		Stream   bool          `json:"stream"`
		Messages []ChatMessage `json:"messages"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request: "+err.Error())
		return
	}

	messages := p.rewriteMessages(req.Messages)
	if req.Stream {
		p.streamCompletion(w, r, messages)
	} else {
		p.blockingCompletion(w, r, messages)
	}
}

// rewriteMessages replaces Unmute's system prompt with the scenario persona,
// the intro directive on the first turn, and a live game state snapshot.
func (p *LLMProxy) rewriteMessages(incoming []ChatMessage) []ChatMessage {
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

	p.mu.Lock()
	persona := p.config.Persona
	introDirective := p.config.IntroDirective
	p.mu.Unlock()

	var sys strings.Builder
	sys.WriteString(persona)
	if firstTurn {
		sys.WriteString("\n\n")
		sys.WriteString(introDirective)
	}
	sys.WriteString("\n\n")
	sys.WriteString(p.Engine.StateSnapshot())

	return append([]ChatMessage{{Role: "system", Content: sys.String()}}, rest...)
}

// callUpstream starts a streaming chat completion against the real LLM.
// Tool definitions are included so the LLM can use native function calling.
// ctx is the caller's request: when the player interrupts, the call stops.
func (p *LLMProxy) callUpstream(ctx context.Context, messages []ChatMessage) (*http.Response, error) {
	p.mu.Lock()
	tools := p.tools
	p.mu.Unlock()

	body := map[string]interface{}{
		"model":    p.Model,
		"messages": messages,
		"stream":   true,
	}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.UpstreamURL+"/chat/completions", bytes.NewReader(payload))
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

// executeToolCall runs a tool call on the game engine. It returns the JSON
// result to feed back to the LLM and the sound effect the action fired.
func (p *LLMProxy) executeToolCall(name, argumentsJSON string) (resultJSON, sfx string) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		log.Printf("Failed to parse tool call arguments %q: %v", argumentsJSON, err)
	}

	resultJSON, sfx = p.Engine.ProcessLLMFunctionCall(game.FunctionCall{Name: name, Arguments: args})
	log.Printf("LLM proxy executed tool %s(%s) => %s (sfx=%q)", name, argumentsJSON, resultJSON, sfx)

	if sfx != "" && p.OnSFX != nil {
		p.OnSFX(sfx)
	}
	if p.OnGameStateChange != nil {
		p.OnGameStateChange()
	}
	return resultJSON, sfx
}

// sseEmitter writes OpenAI-compatible streaming chunks.
type sseEmitter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	id      string
	created int64
	model   string
}

func (e *sseEmitter) write(delta map[string]string, finishReason interface{}) {
	data, _ := json.Marshal(map[string]interface{}{
		"id":      e.id,
		"object":  "chat.completion.chunk",
		"created": e.created,
		"model":   e.model,
		"choices": []map[string]interface{}{
			{"index": 0, "delta": delta, "finish_reason": finishReason},
		},
	})
	fmt.Fprintf(e.w, "data: %s\n\n", data)
	e.flusher.Flush()
}

func (e *sseEmitter) emitContent(content string) {
	if content != "" {
		e.write(map[string]string{"content": content}, nil)
	}
}

func (e *sseEmitter) emitDone() {
	e.write(map[string]string{}, "stop")
	fmt.Fprint(e.w, "data: [DONE]\n\n")
	e.flusher.Flush()
}

// streamCompletion runs the generate -> act -> continue loop, streaming
// action-free text to Unmute.
func (p *LLMProxy) streamCompletion(w http.ResponseWriter, r *http.Request, messages []ChatMessage) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
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

	// A canceled request means the player interrupted: nobody is listening.
	if _, _, err := p.runCompletionLoop(r, messages, emitter.emitContent); err != nil && r.Context().Err() == nil {
		log.Printf("LLM proxy error: %v", err)
		// Make the game master audibly fail in-character rather than going silent.
		emitter.emitContent(p.Engine.Text().Interference)
	}
	emitter.emitDone()
}

// blockingCompletion handles stream=false requests (not used by Unmute, but
// part of the OpenAI API surface).
func (p *LLMProxy) blockingCompletion(w http.ResponseWriter, r *http.Request, messages []ChatMessage) {
	var full strings.Builder
	if _, _, err := p.runCompletionLoop(r, messages, func(s string) { full.WriteString(s) }); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
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
				"message":       map[string]string{"role": "assistant", "content": strings.TrimSpace(full.String())},
				"finish_reason": "stop",
			},
		},
	})
}

// runCompletionLoop streams upstream completions, emits narration text,
// executes tool calls, and requests continuations until the LLM finishes
// naturally (no more tool calls). It returns the messages with the assistant
// and tool messages appended, and the last sound effect a tool fired.
func (p *LLMProxy) runCompletionLoop(r *http.Request, messages []ChatMessage, emit func(string)) ([]ChatMessage, string, error) {
	lastSFX := ""
	for round := 0; round < maxActionRounds; round++ {
		resp, err := p.callUpstream(r.Context(), messages)
		if err != nil {
			return messages, lastSFX, err
		}
		assistantText, toolCalls, err := consumeUpstream(resp, emit)
		resp.Body.Close()
		if err != nil {
			return messages, lastSFX, err
		}
		log.Printf("runCompletionLoop: round=%d toolCalls=%d text=%q", round, len(toolCalls), assistantText)

		// Fallback: the LLM wrote tool calls as text instead of using the API.
		if len(toolCalls) == 0 {
			toolCalls = parseTextToolCalls(assistantText)
		}

		messages = append(messages, ChatMessage{
			Role:      "assistant",
			Content:   stripTextToolCalls(assistantText),
			ToolCalls: toolCalls,
		})
		if len(toolCalls) == 0 {
			break // finished without (further) tool calls
		}
		// One tool role message per call, with the real game engine result.
		for _, tc := range toolCalls {
			resultJSON, sfx := p.executeToolCall(tc.Function.Name, tc.Function.Arguments)
			if sfx != "" {
				lastSFX = sfx
			}
			messages = append(messages, ChatMessage{Role: "tool", Content: resultJSON, ToolCallID: tc.ID})
		}
	}
	return messages, lastSFX, nil
}

// consumeUpstream reads the upstream SSE stream, emitting narration text
// while collecting any tool calls. It returns when the stream ends, with the
// full assistant text and the accumulated tool calls.
func consumeUpstream(resp *http.Response, emit func(string)) (assistantText string, toolCalls []toolCallChunk, err error) {
	var full strings.Builder

	// Text is released one sentence at a time, so a tool call the model
	// wrote as text is stripped before the TTS reads it aloud.
	spoken := 0 // bytes of full already emitted
	speakUpTo := func(end int) {
		if end > spoken {
			emit(stripTextToolCalls(full.String()[spoken:end]))
			spoken = end
		}
	}

	// Tool calls arrive in fragments, keyed by index.
	type toolAccum struct {
		id        string
		name      string
		arguments strings.Builder
	}
	toolAccums := make(map[int]*toolAccum)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
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
						Index    int          `json:"index"`
						ID       string       `json:"id"`
						Function toolFunction `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil || len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		// Emit narration text up to the last finished sentence.
		if delta.Content != "" {
			full.WriteString(delta.Content)
			speakUpTo(strings.LastIndexAny(full.String(), ".!?\n") + 1)
		}

		for _, tc := range delta.ToolCalls {
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
	speakUpTo(full.Len())

	// Indices need not start at 0 or be contiguous; keep their order.
	for _, i := range slices.Sorted(maps.Keys(toolAccums)) {
		accum := toolAccums[i]
		if accum.id == "" {
			accum.id = nextTextCallID() // the tool message must reference an ID
		}
		toolCalls = append(toolCalls, toolCallChunk{
			ID:       accum.id,
			Type:     "function",
			Function: toolFunction{Name: accum.name, Arguments: accum.arguments.String()},
		})
	}
	return full.String(), toolCalls, nil
}

package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"escape-game/internal/game"
)

func sseChunk(content string) string {
	b, _ := json.Marshal(map[string]interface{}{
		"choices": []map[string]interface{}{
			{"index": 0, "delta": map[string]string{"content": content}},
		},
	})
	return "data: " + string(b) + "\n\n"
}

// sseToolCallChunk builds a streaming chunk carrying a tool_calls delta.
func sseToolCallChunk(index int, id, name, args string) string {
	b, _ := json.Marshal(map[string]interface{}{
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]interface{}{
					"tool_calls": []map[string]interface{}{
						{
							"index":    index,
							"id":       id,
							"type":     "function",
							"function": map[string]string{"name": name, "arguments": args},
						},
					},
				},
			},
		},
	})
	return "data: " + string(b) + "\n\n"
}

// TestProxyToolCallAndContinue covers the core gameplay loop with native
// tool calling: the LLM emits a tool_call, the game engine executes it, and
// the LLM is re-queried to narrate the result. The tool call itself must
// never appear in the TTS stream.
func TestProxyToolCallAndContinue(t *testing.T) {
	calls := 0
	var secondCallMessages string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		if calls == 1 {
			// Narration text, then a tool call.
			fmt.Fprint(w, sseChunk("Voyons cette blouse. "))
			fmt.Fprint(w, sseToolCallChunk(0, "call_123", "inspect_item", `{"target_item":"blouse_scientifique"}`))
			fmt.Fprint(w, "data: [DONE]\n\n")
		} else {
			var req struct {
				Messages []ChatMessage `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			b, _ := json.Marshal(req.Messages)
			secondCallMessages = string(b)

			fmt.Fprint(w, sseChunk("Une poche contient quelque chose."))
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer upstream.Close()

	state := game.NewGameState()
	state.Player.CurrentRoom = "lab"
	state.Rooms["lab"] = &game.RoomState{
		RoomID:      "lab",
		Name:        "Laboratoire",
		Description: "Un labo en ruines.",
		Items: map[string]*game.RoomItem{
			"blouse_scientifique": {
				Name:                 "Blouse de scientifique",
				Visible:              true,
				DescriptionOnInspect: "Une carte magnétique dépasse de la poche.",
			},
		},
	}
	engine := game.NewGameEngine(state)

	p := &LLMProxy{
		Engine:      engine,
		UpstreamURL: upstream.URL,
		Model:       "test-model",
		APIKey:      "test-key",
		httpClient:  upstream.Client(),
		config:      &GameConfig{Persona: "test persona", IntroDirective: "test intro", IntroPrompt: "test prompt"},
	}

	body := `{"stream": true, "messages": [
		{"role": "system", "content": "unmute template prompt"},
		{"role": "user", "content": "j'inspecte la blouse"}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.HandleChatCompletions(rec, req)

	out := rec.Body.String()

	if strings.Contains(out, "inspect_item") {
		t.Errorf("tool call name leaked into the TTS stream:\n%s", out)
	}
	if !strings.Contains(out, "Voyons cette blouse.") {
		t.Errorf("narration text before tool call missing from stream:\n%s", out)
	}
	if !strings.Contains(out, "Une poche contient quelque chose.") {
		t.Errorf("continuation narration missing from stream:\n%s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Errorf("stream not properly terminated:\n%s", out)
	}

	if !state.Rooms["lab"].Items["blouse_scientifique"].Inspected {
		t.Error("inspect_item was not executed on the game engine")
	}
	if calls != 2 {
		t.Errorf("expected 2 upstream calls (initial + continuation), got %d", calls)
	}
	if !strings.Contains(secondCallMessages, "carte magnétique dépasse") {
		t.Errorf("action result not fed back to the LLM:\n%s", secondCallMessages)
	}
	// Verify the tool role message was sent in the second call.
	if !strings.Contains(secondCallMessages, `"role":"tool"`) {
		t.Errorf("tool role message missing from second call:\n%s", secondCallMessages)
	}
}

// TestProxyPassthroughWithoutTags ensures plain responses stream untouched,
// including legitimate '[' characters that are not action tags.
func TestProxyPassthroughWithoutTags(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseChunk("Protocole [niveau 4] : restez calme."))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	state := game.NewGameState()
	state.Player.CurrentRoom = "lab"
	state.Rooms["lab"] = &game.RoomState{RoomID: "lab", Name: "Lab", Items: map[string]*game.RoomItem{}}

	p := &LLMProxy{
		Engine:      game.NewGameEngine(state),
		UpstreamURL: upstream.URL,
		Model:       "test-model",
		APIKey:      "test-key",
		httpClient:  upstream.Client(),
		config:      &GameConfig{Persona: "test persona", IntroDirective: "test intro", IntroPrompt: "test prompt"},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"stream": true, "messages": [{"role": "user", "content": "salut"}]}`))
	rec := httptest.NewRecorder()
	p.HandleChatCompletions(rec, req)

	// Deltas may be split at '[' boundaries; what matters is the
	// concatenated text Unmute's TTS will receive.
	var spoken strings.Builder
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		data := strings.TrimPrefix(strings.TrimSpace(line), "data: ")
		if data == "" || data == "[DONE]" || !strings.HasPrefix(data, "{") {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) == nil && len(chunk.Choices) > 0 {
			spoken.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	if spoken.String() != "Protocole [niveau 4] : restez calme." {
		t.Errorf("legitimate bracketed text was mangled: %q", spoken.String())
	}
}

// TestRewriteMessagesFirstTurn checks the dynamic system prompt: persona,
// intro directive on first turn only, and live game state.
func TestRewriteMessagesFirstTurn(t *testing.T) {
	state := game.NewGameState()
	state.Player.CurrentRoom = "lab"
	state.Rooms["lab"] = &game.RoomState{
		RoomID: "lab", Name: "Laboratoire",
		Items: map[string]*game.RoomItem{
			"visible_item": {Name: "Terminal", Visible: true},
			"hidden_item":  {Name: "Carte secrète", Visible: false},
		},
	}
	p := &LLMProxy{Engine: game.NewGameEngine(state), Model: "m", config: &GameConfig{Persona: "test persona", IntroDirective: "test intro", IntroPrompt: "test prompt"}}

	first := p.rewriteMessages([]ChatMessage{
		{Role: "system", Content: "unmute template"},
		{Role: "user", Content: "bonjour"},
	})
	if first[0].Role != "system" || !strings.Contains(first[0].Content, "test intro") {
		t.Error("first turn must include the intro directive")
	}
	if !strings.Contains(first[0].Content, "Terminal") {
		t.Error("visible items missing from system prompt")
	}
	if strings.Contains(first[0].Content, "Carte secrète") {
		t.Error("hidden item leaked into system prompt")
	}
	if strings.Contains(first[0].Content, "unmute template") {
		t.Error("unmute's own system prompt should be discarded")
	}

	later := p.rewriteMessages([]ChatMessage{
		{Role: "system", Content: "unmute template"},
		{Role: "user", Content: "bonjour"},
		{Role: "assistant", Content: "Alerte critique."},
		{Role: "user", Content: "où suis-je ?"},
	})
	if strings.Contains(later[0].Content, "DÉBUT DE PARTIE") {
		t.Error("intro directive must only appear on the first turn")
	}
	if len(later) != 4 {
		t.Errorf("conversation history mangled: %d messages", len(later))
	}
}

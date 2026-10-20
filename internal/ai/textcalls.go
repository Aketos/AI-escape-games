package ai

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync/atomic"

	"escape-game/internal/game"
)

// Some models (e.g. Gemma) write tool calls as text instead of using the
// native tool_calls API. This file finds those calls so they still run, and
// strips them from the narration so the TTS never reads them aloud.

// textCallCounter generates unique IDs for fallback-parsed tool calls.
// Some upstream APIs (e.g. Mistral) require tool call IDs to be exactly
// 9 alphanumeric characters and unique across the conversation.
var textCallCounter atomic.Uint64

func nextTextCallID() string {
	return fmt.Sprintf("c%08d", textCallCounter.Add(1)%100000000)
}

// toolAlt is a regexp alternation of every tool the engine executes.
var toolAlt = func() string {
	names := game.ToolNames()
	for i, n := range names {
		names[i] = regexp.QuoteMeta(n)
	}
	return "(?:" + strings.Join(names, "|") + ")"
}()

// genericToolCallRe finds any tool name followed by parameters in any format:
// - tool_name({...})
// - tool_name, {...}
// - {"name": "tool_name", "parameters": {...}}
// - [tool_name(key=value)]
// - tool_name(target_item=xxx)
var genericToolCallRe = regexp.MustCompile(`(?i)(?:\{\s*"name"\s*:\s*")?(` + toolAlt + `)(?:"\s*,\s*"parameters"\s*:\s*)?(?:\s*[,\(]\s*)?(\{[^}]+\}|[^\]\)\n]+)?(?:\]?\)?)?`)

// kvParamRe matches key=value pairs.
var kvParamRe = regexp.MustCompile(`(\w+)\s*=\s*"?([\w_-]+)"?`)

// Patterns stripped from the narration, one per written tool call format.
var stripToolCallRes = []*regexp.Regexp{
	regexp.MustCompile(`\{\s*"name"\s*:\s*"` + toolAlt + `"\s*,\s*"parameters"\s*:\s*\{[^}]+\}\s*\}`),
	regexp.MustCompile(`\[` + toolAlt + `\([^\]]+\)\]`),
	regexp.MustCompile(`(?i)` + toolAlt + `\s*[,\(]\s*\{[^}]+\}\)?`),
	regexp.MustCompile(`(?i)` + toolAlt + `\([^\)]+\)`),
}

// parseTextToolCalls returns the tool calls written in text, in any of the
// formats above.
func parseTextToolCalls(text string) []toolCallChunk {
	var calls []toolCallChunk
	for _, m := range genericToolCallRe.FindAllStringSubmatch(text, -1) {
		name := strings.ToLower(m[1])
		args := normalizeArgs(m[2])
		if args == "" {
			continue
		}
		calls = append(calls, toolCallChunk{
			ID:       nextTextCallID(),
			Type:     "function",
			Function: toolFunction{Name: name, Arguments: args},
		})
		log.Printf("parseTextToolCalls: found tool call: %s(%s)", name, args)
	}
	return calls
}

// normalizeArgs converts any argument format to a JSON string.
// Handles: {"key":"value"}, key=value, key=value key2=value2.
func normalizeArgs(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		return raw // already JSON
	}
	pairs := kvParamRe.FindAllStringSubmatch(raw, -1)
	if len(pairs) == 0 {
		return ""
	}
	m := make(map[string]string)
	for _, p := range pairs {
		m[p[1]] = p[2]
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// stripTextToolCalls removes text-written tool calls from the narration.
func stripTextToolCalls(text string) string {
	for _, re := range stripToolCallRes {
		text = re.ReplaceAllString(text, "")
	}
	return text
}

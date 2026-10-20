package ai

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// maxSimHistory caps the number of messages kept in simulation history to
// bound prompt size on long sessions.
const maxSimHistory = 24

// HandleSimulationChat implements POST /api/simulate: a text-only game for
// the operator, without Unmute. It accepts a plain-text user message, runs
// the same completion loop as the voice game (persona, game state, tools) and
// returns the narration, the updated game state and the sound effect to play.
//
// On init (or a scenario change) it restarts the game and sends intro_prompt
// as the first user message to trigger the introduction.
//
// shortcut: one simulation conversation per server (simHistory lives on the
// proxy), fine for a token-protected operator tool; key it by session if
// several people ever simulate at once.
func (p *LLMProxy) HandleSimulationChat(w http.ResponseWriter, r *http.Request) {
	if !p.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		Message  string `json:"message"`
		Scenario string `json:"scenario"`
		Init     bool   `json:"init"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request: "+err.Error())
		return
	}

	// Start a fresh game on init or when the scenario changes.
	current := p.Scenario()
	if req.Scenario == "" {
		req.Scenario = current
	}
	if req.Init || req.Scenario != current {
		if err := p.LoadScenario(req.Scenario); err != nil {
			log.Printf("SIM: %v", err)
			writeError(w, http.StatusBadRequest, "unknown scenario")
			return
		}
	}

	p.mu.Lock()
	userMsg := req.Message
	if userMsg == "" || req.Init {
		userMsg = p.config.IntroPrompt
	}
	history := p.simHistory // empty after LoadScenario, so on init too
	p.mu.Unlock()

	conversation := append(append([]ChatMessage{}, history...), ChatMessage{Role: "user", Content: userMsg})
	messages := p.rewriteMessages(conversation)

	var full strings.Builder
	updated, sfx, err := p.runCompletionLoop(r, messages, func(s string) { full.WriteString(s) })
	narration := strings.TrimSpace(full.String())
	if err != nil {
		log.Printf("SIM: completion error: %v", err)
		narration = p.Engine.Text().Interference
	} else if len(updated) > 1 {
		// Keep everything after the system prompt, capped to bound prompt growth.
		p.mu.Lock()
		p.simHistory = capHistory(updated[1:], maxSimHistory)
		p.mu.Unlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"narration":  narration,
		"game_state": p.Engine.GameStateForClient(),
		"sfx":        sfx,
	})
}

// capHistory trims history to at most max messages, dropping oldest first.
// Leading orphan tool messages (whose parent assistant tool_calls message was
// trimmed away) are also dropped, as most APIs reject them.
func capHistory(history []ChatMessage, max int) []ChatMessage {
	if len(history) > max {
		history = history[len(history)-max:]
	}
	for len(history) > 0 && history[0].Role == "tool" {
		history = history[1:]
	}
	return history
}

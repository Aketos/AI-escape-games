package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"escape-game/internal/ai"
	"escape-game/internal/game"
	"escape-game/internal/network"
)

func main() {
	log.Println("Starting S2S Audio Escape Game server...")

	// Default scenario — can be overridden by ?scenario= on the WebSocket URL.
	defaultScenario := "projet_longevite"

	// 1. Load language-specific game config (persona, intro, scenario paths)
	cfg, err := ai.LoadGameConfig(defaultScenario)
	if err != nil {
		log.Fatalf("Failed to load game config: %v", err)
	}

	// 2. Initialize the Game State (FSM) by loading JSON scenarios from the scenario directory
	gameFSM, err := game.LoadScenario(filepath.Join(cfg.ScenarioDir, "room_state.json"), filepath.Join(cfg.ScenarioDir, "player_state.json"))
	if err != nil {
		log.Fatalf("Failed to load scenario: %v", err)
	}
	log.Println("Scenario loaded successfully.")

	// 3. Shared game engine: used by both the WebSocket bridge and the LLM proxy.
	engine := game.NewGameEngine(gameFSM)

	// 4. Initialize the WebSocket server (shares config with LLM proxy)
	wsServer := network.NewWSServerWithConfig(engine, cfg, defaultScenario)

	// 5. OpenAI-compatible LLM proxy: Unmute's KYUTAI_LLM_URL must point at
	// this server so game logic stays invisible to the TTS.
	llmProxy, err := ai.NewLLMProxyFromEnv(engine, defaultScenario, wsServer.BroadcastSFX, wsServer.BroadcastGameState)
	if err != nil {
		log.Fatalf("Failed to initialize LLM proxy: %v", err)
	}
	wsServer.SetLLMProxy(llmProxy)

	// 6. List available scenarios for the frontend selection UI.
	lang := os.Getenv("GAME_LANGUAGE")
	if lang == "" {
		lang = "fr"
	}
	http.HandleFunc("/scenarios", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		scenarios, err := ai.ListScenarios(lang)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(scenarios)
	})

	http.HandleFunc("/ws", wsServer.HandleConnections)
	http.HandleFunc("/chat/completions", llmProxy.HandleChatCompletions)
	http.HandleFunc("/v1/chat/completions", llmProxy.HandleChatCompletions)
	http.HandleFunc("/models", llmProxy.HandleModels)
	http.HandleFunc("/v1/models", llmProxy.HandleModels)
	http.HandleFunc("/game-state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(engine.GameStateForClient())
	})
	http.HandleFunc("/debug", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, ferr := os.Stat(filepath.Join(cfg.ScenarioDir, "function_calls.json"))
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tools_loaded":        llmProxy.ToolsCount(),
			"function_calls_json": ferr == nil,
		})
	})

	port := ":8080"
	log.Printf("Server listening on port %s", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

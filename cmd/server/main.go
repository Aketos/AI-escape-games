package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"escape-game/internal/ai"
	"escape-game/internal/network"
)

func main() {
	log.Println("Starting S2S Audio Escape Game server...")

	// Default scenario — can be overridden by ?scenario= on the WebSocket URL.
	defaultScenario := "projet_longevite"

	// 1. WebSocket bridge between the player's browser and Unmute.
	wsServer := network.NewWSServer()

	// 2. OpenAI-compatible LLM proxy that owns the voice game: Unmute's
	// KYUTAI_LLM_URL must point at this server so game logic stays invisible
	// to the TTS.
	llmProxy, err := ai.NewLLMProxyFromEnv(defaultScenario, wsServer.BroadcastSFX, wsServer.BroadcastGameState)
	if err != nil {
		log.Fatalf("Failed to initialize LLM proxy: %v", err)
	}
	wsServer.SetLLMProxy(llmProxy)

	// 3. Simulation mode gets its own game, so it can never reset a live
	// voice game or play sounds to its player.
	simProxy, err := ai.NewLLMProxyFromEnv(defaultScenario, nil, nil)
	if err != nil {
		log.Fatalf("Failed to initialize simulation proxy: %v", err)
	}

	mux := http.NewServeMux()

	// 4. List available scenarios for the frontend selection UI.
	mux.HandleFunc("GET /scenarios", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		scenarios, err := ai.ListScenarios()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(scenarios)
	})

	mux.HandleFunc("GET /ws", wsServer.HandleConnections)
	mux.HandleFunc("POST /chat/completions", llmProxy.HandleChatCompletions)
	mux.HandleFunc("POST /v1/chat/completions", llmProxy.HandleChatCompletions)
	mux.HandleFunc("POST /api/simulate", simProxy.HandleSimulationChat)
	mux.HandleFunc("GET /models", llmProxy.HandleModels)
	mux.HandleFunc("GET /v1/models", llmProxy.HandleModels)
	mux.HandleFunc("GET /game-state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llmProxy.Engine.GameStateForClient())
	})
	mux.HandleFunc("GET /debug", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"scenario":     llmProxy.Scenario(),
			"tools_loaded": llmProxy.ToolsCount(),
		})
	})

	// No read/write timeouts: WebSockets and LLM streams stay open for minutes.
	port := os.Getenv("PORT") // set by Scaleway Serverless Containers
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("Server listening on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

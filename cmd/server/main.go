package main

import (
	"log"
	"net/http"

	"escape-game/internal/ai"
	"escape-game/internal/game"
	"escape-game/internal/network"
)

func main() {
	log.Println("Starting S2S Audio Escape Game server...")

	// 1. Initialize the Game State (FSM) by loading JSON scenarios
	gameFSM, err := game.LoadScenario("room_state.json", "player_state.json")
	if err != nil {
		log.Fatalf("Failed to load scenario: %v", err)
	}
	log.Println("Scenario loaded successfully.")

	// 2. Shared game engine: used by both the WebSocket bridge and the LLM proxy.
	engine := game.NewGameEngine(gameFSM)

	// 3. Initialize the WebSocket server
	wsServer := network.NewWSServer(engine)

	// 4. OpenAI-compatible LLM proxy: Unmute's KYUTAI_LLM_URL must point at
	// this server so game logic stays invisible to the TTS.
	llmProxy := ai.NewLLMProxyFromEnv(engine, wsServer.BroadcastSFX)

	http.HandleFunc("/ws", wsServer.HandleConnections)
	http.HandleFunc("/chat/completions", llmProxy.HandleChatCompletions)
	http.HandleFunc("/models", llmProxy.HandleModels)

	port := ":8080"
	log.Printf("Server listening on port %s", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

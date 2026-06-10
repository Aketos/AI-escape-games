package game

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadScenario reads the player and room state from JSON files and initializes the GameState.
func LoadScenario(roomPath, playerPath string) (*GameState, error) {
	gameState := NewGameState()

	// 1. Load Player State
	playerData, err := os.ReadFile(playerPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read player state file: %w", err)
	}

	var player PlayerState
	if err := json.Unmarshal(playerData, &player); err != nil {
		return nil, fmt.Errorf("failed to unmarshal player state: %w", err)
	}
	gameState.Player = player

	// 2. Load Room State
	roomData, err := os.ReadFile(roomPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read room state file: %w", err)
	}

	var room RoomState
	if err := json.Unmarshal(roomData, &room); err != nil {
		return nil, fmt.Errorf("failed to unmarshal room state: %w", err)
	}
	
	// Add the room to the game state map
	gameState.Rooms[room.RoomID] = &room

	return gameState, nil
}

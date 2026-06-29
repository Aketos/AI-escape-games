package game

import (
	"encoding/json"
	"fmt"
	"os"
)

// rawRoomState is used to unmarshal room JSON while preserving item order.
// The Items field is unmarshalled as raw messages so we can decode items
// one by one in declaration order.
type rawRoomState struct {
	RoomID      string          `json:"room_id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Image       string          `json:"image,omitempty"`
	Items       json.RawMessage `json:"items"`
}

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

	// 2. Load Room State — supports both a single room object and an array of rooms
	roomData, err := os.ReadFile(roomPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read room state file: %w", err)
	}

	// Try array format first
	var rawRooms []rawRoomState
	if err := json.Unmarshal(roomData, &rawRooms); err == nil {
		for _, rr := range rawRooms {
			room := &RoomState{
				RoomID:      rr.RoomID,
				Name:        rr.Name,
				Description: rr.Description,
				Image:       rr.Image,
				Items:       make(map[string]*RoomItem),
			}
			itemOrder, _ := unmarshalItemsOrdered(rr.Items, room.Items)
			room.ItemOrder = itemOrder
			gameState.Rooms[room.RoomID] = room
			gameState.RoomOrder = append(gameState.RoomOrder, room.RoomID)
		}
		markStartingRoomVisited(gameState)
		return gameState, nil
	}

	// Fallback: single room object
	var rawRoom rawRoomState
	if err := json.Unmarshal(roomData, &rawRoom); err != nil {
		return nil, fmt.Errorf("failed to unmarshal room state: %w", err)
	}
	room := &RoomState{
		RoomID:      rawRoom.RoomID,
		Name:        rawRoom.Name,
		Description: rawRoom.Description,
		Image:       rawRoom.Image,
		Items:       make(map[string]*RoomItem),
	}
	itemOrder, _ := unmarshalItemsOrdered(rawRoom.Items, room.Items)
	room.ItemOrder = itemOrder
	gameState.Rooms[room.RoomID] = room
	gameState.RoomOrder = []string{room.RoomID}

	markStartingRoomVisited(gameState)
	return gameState, nil
}

// markStartingRoomVisited marks the player's current room as visited.
func markStartingRoomVisited(gs *GameState) {
	if room, ok := gs.Rooms[gs.Player.CurrentRoom]; ok {
		room.Visited = true
	}
}

// unmarshalItemsOrdered decodes a JSON object of items into the provided map
// while preserving the declaration order of keys. Returns the ordered list of item IDs.
func unmarshalItemsOrdered(raw json.RawMessage, items map[string]*RoomItem) ([]string, error) {
	// First decode as a map of raw messages to get the keys
	var rawItems map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawItems); err != nil {
		return nil, err
	}

	// Use json.Decoder to get key order
	dec := json.NewDecoder(newByteReader(raw))
	// Read opening brace
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("expected '{', got %v", tok)
	}

	var order []string
	for dec.More() {
		// Read key
		tok, err = dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key, got %v", tok)
		}

		// Decode value into RoomItem
		var item RoomItem
		if err := dec.Decode(&item); err != nil {
			return nil, err
		}
		items[key] = &item
		order = append(order, key)
	}

	return order, nil
}

// newByteReader wraps a byte slice in a strings.Reader-like reader.
func newByteReader(raw json.RawMessage) *bytesReader {
	return &bytesReader{data: raw}
}

type bytesReader struct {
	data []byte
	pos  int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, fmt.Errorf("EOF")
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

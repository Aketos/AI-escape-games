package game

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// rawRoomState is used to unmarshal room JSON while preserving item order.
// The Items field is unmarshalled as raw messages so we can decode items
// one by one in declaration order.
type rawRoomState struct {
	RoomID      string            `json:"room_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Image       string            `json:"image,omitempty"`
	Items       json.RawMessage   `json:"items"`
	Exits       map[string]string `json:"exits,omitempty"`
}

// LoadScenario reads room_state.json and player_state.json from scenarioDir
// and initializes the GameState, with engine text in lang.
func LoadScenario(scenarioDir, lang string) (*GameState, error) {
	gameState := NewGameState()
	text, ok := Texts[lang]
	if !ok {
		return nil, fmt.Errorf("no engine text for language %q (see internal/game/text.go)", lang)
	}
	gameState.Text = text

	// 1. Load Player State
	playerData, err := os.ReadFile(filepath.Join(scenarioDir, "player_state.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to read player state file: %w", err)
	}

	var player PlayerState
	if err := json.Unmarshal(playerData, &player); err != nil {
		return nil, fmt.Errorf("failed to unmarshal player state: %w", err)
	}
	gameState.Player = player

	// 2. Load Room State — supports both a single room object and an array of rooms
	roomData, err := os.ReadFile(filepath.Join(scenarioDir, "room_state.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to read room state file: %w", err)
	}

	var rawRooms []rawRoomState
	if err := json.Unmarshal(roomData, &rawRooms); err != nil {
		var single rawRoomState
		if err := json.Unmarshal(roomData, &single); err != nil {
			return nil, fmt.Errorf("failed to unmarshal room state: %w", err)
		}
		rawRooms = []rawRoomState{single}
	}
	for _, rr := range rawRooms {
		room := &RoomState{
			RoomID:      rr.RoomID,
			Name:        rr.Name,
			Description: rr.Description,
			Image:       rr.Image,
			Items:       make(map[string]*RoomItem),
			Exits:       rr.Exits,
		}
		itemOrder, err := unmarshalItemsOrdered(rr.Items, room.Items)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal items for room %s: %w", rr.RoomID, err)
		}
		room.ItemOrder = itemOrder
		gameState.Rooms[room.RoomID] = room
		gameState.RoomOrder = append(gameState.RoomOrder, room.RoomID)
	}
	if err := checkExits(gameState); err != nil {
		return nil, err
	}

	markStartingRoomVisited(gameState)
	return gameState, nil
}

// checkExits rejects exits to unknown rooms or through unknown doors: a typo
// there would silently make a room unreachable or remove a lock.
func checkExits(gs *GameState) error {
	for _, room := range gs.Rooms {
		for to, door := range room.Exits {
			if _, ok := gs.Rooms[to]; !ok {
				return fmt.Errorf("room %s: exit to unknown room %q", room.RoomID, to)
			}
			if _, ok := room.Items[door]; door != "" && !ok {
				return fmt.Errorf("room %s: exit to %s uses unknown door %q", room.RoomID, to, door)
			}
		}
	}
	return nil
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
	// Use json.Decoder to get key order
	dec := json.NewDecoder(bytes.NewReader(raw))
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

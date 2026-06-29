package game

// FunctionCall defines a schema for an LLM function tool.
type FunctionCall struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  *Parameters            `json:"parameters,omitempty"`
	Arguments   map[string]interface{} `json:"arguments,omitempty"` // For incoming calls
}

type Parameters struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

type Property struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// FunctionCallsDoc represents the document wrapping multiple function calls schemas.
type FunctionCallsDoc struct {
	Functions []FunctionCall `json:"functions"`
}

// InventoryItem represents an item held by the player.
type InventoryItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
	Image string `json:"image,omitempty"`
}

// PlayerState represents the truth about the player.
type PlayerState struct {
	PlayerID    string          `json:"player_id"`
	CurrentRoom string          `json:"current_room"`
	Inventory   []InventoryItem `json:"inventory"`
	History     []string        `json:"history"`
}

// RoomItem represents an interactive object or feature within a room.
type RoomItem struct {
	Name                 string   `json:"name"`
	Image                string   `json:"image,omitempty"`
	Visible              bool     `json:"visible"`
	Inspected            bool     `json:"inspected,omitempty"`
	DescriptionOnInspect string   `json:"description_on_inspect,omitempty"`
	Contains             []string `json:"contains,omitempty"`
	IsCollectible        bool     `json:"is_collectible,omitempty"`
	State                string   `json:"state,omitempty"`
	Requires             string   `json:"requires,omitempty"`
	SuccessMessage       string   `json:"success_message,omitempty"`
	FailMessage          string   `json:"fail_message,omitempty"`
	SuccessSFX           string   `json:"success_sfx,omitempty"`
	SuccessState         string   `json:"success_state,omitempty"`
	ConsumesItem         bool     `json:"consumes_item,omitempty"`
	PinCode              string   `json:"pin_code,omitempty"`
	PinSuccessState      string   `json:"pin_success_state,omitempty"`
	PinSuccessMessage    string   `json:"pin_success_message,omitempty"`
	Produces             []string `json:"produces,omitempty"`
	Unlocks              []string `json:"unlocks,omitempty"`
	WinsGame             bool     `json:"wins_game,omitempty"`
	Type                 string   `json:"type"`
}

// RoomState defines a physical location in the game and its interactive state.
type RoomState struct {
	RoomID      string               `json:"room_id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Image       string               `json:"image,omitempty"`
	Items       map[string]*RoomItem `json:"items"`
}

// GameState is the overarching Finite State Machine (FSM) holding all truth.
// Modifications to this should be protected by a mutex if accessed concurrently.
type GameState struct {
	Player PlayerState
	Rooms  map[string]*RoomState
	Won    bool
}

// NewGameState initializes a blank game state.
func NewGameState() *GameState {
	return &GameState{
		Rooms: make(map[string]*RoomState),
	}
}

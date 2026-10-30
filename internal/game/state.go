package game

import (
	"maps"
	"slices"
)

// FunctionCall is a tool call made by the LLM.
type FunctionCall struct {
	Name      string
	Arguments map[string]interface{}
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
	Used                 bool     `json:"-"` // true once use_item succeeded on it
}

// RoomState defines a physical location in the game and its interactive state.
type RoomState struct {
	RoomID      string               `json:"room_id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Image       string               `json:"image,omitempty"`
	Items       map[string]*RoomItem `json:"items"`
	Exits       map[string]string    `json:"exits,omitempty"` // reachable room ID -> door item ID in this room ("" = always open)
	ItemOrder   []string             `json:"-"`               // item IDs in JSON declaration order
	Visited     bool                 `json:"-"`               // true once the player has been in this room
}

// GameState is the overarching Finite State Machine (FSM) holding all truth.
// Modifications to this should be protected by a mutex if accessed concurrently.
type GameState struct {
	Player    PlayerState
	Rooms     map[string]*RoomState
	RoomOrder []string // room IDs in JSON declaration order
	Won       bool
	Text      *Text // engine sentences in the scenario's language
}

// orderedItemIDs returns item IDs in JSON declaration order. Rooms built in
// code (tests) have no ItemOrder, so fall back to sorted keys.
func orderedItemIDs(room *RoomState) []string {
	if len(room.ItemOrder) > 0 {
		return room.ItemOrder
	}
	return slices.Sorted(maps.Keys(room.Items))
}

// orderedRoomIDs is orderedItemIDs for rooms.
func orderedRoomIDs(gs *GameState) []string {
	if len(gs.RoomOrder) > 0 {
		return gs.RoomOrder
	}
	return slices.Sorted(maps.Keys(gs.Rooms))
}

// NewGameState initializes a blank game state with French engine text.
func NewGameState() *GameState {
	return &GameState{
		Rooms: make(map[string]*RoomState),
		Text:  Texts["fr"],
	}
}

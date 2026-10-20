package game

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// StateSnapshot renders the game state block of the LLM system prompt, in
// the scenario's language. Only visible items are listed so the LLM cannot
// leak hidden content.
func (e *GameEngine) StateSnapshot() string {
	e.mu.Lock()
	defer e.mu.Unlock()

	t := e.state.Text
	var b strings.Builder

	b.WriteString(t.StateHeader + "\n")
	fmt.Fprintf(&b, t.Oxygen+"\n", e.oxygenMinutes())
	if e.state.Won {
		b.WriteString(t.WonNotice + "\n")
	}

	if room, ok := e.state.Rooms[e.state.Player.CurrentRoom]; ok {
		fmt.Fprintf(&b, t.CurrentRoom, room.Name)
		if room.Description != "" {
			b.WriteString(" — " + room.Description)
		}
		b.WriteString("\n" + t.VisibleHeader + "\n")
		for _, id := range orderedItemIDs(room) {
			item, ok := room.Items[id]
			if !ok || !item.Visible {
				continue
			}
			fmt.Fprintf(&b, "- %s (id: %s)", item.Name, id)
			if item.State != "" {
				fmt.Fprintf(&b, t.StateTag, item.State)
			}
			if item.Inspected {
				b.WriteString(t.InspectedTag)
			}
			b.WriteString("\n")
		}

		// List the exits whose door is open; locked ones stay unmentioned.
		var accessible []string
		for _, rID := range slices.Sorted(maps.Keys(room.Exits)) {
			r, ok := e.state.Rooms[rID]
			door := room.Items[room.Exits[rID]]
			if !ok || (door != nil && door.State == "locked") {
				continue
			}
			accessible = append(accessible, fmt.Sprintf("- %s (id: %s)\n", r.Name, rID))
		}
		if len(accessible) > 0 {
			b.WriteString(t.PassagesHeader + "\n")
			b.WriteString(strings.Join(accessible, ""))
		}
	}

	b.WriteString(t.InventoryHeader + "\n")
	if len(e.state.Player.Inventory) == 0 {
		b.WriteString(t.EmptyInventory + "\n")
	}
	for _, inv := range e.state.Player.Inventory {
		fmt.Fprintf(&b, "- %s (id: %s)\n", inv.Name, inv.ID)
	}

	if history := e.state.Player.History; len(history) > 0 {
		b.WriteString(t.LastActions + "\n")
		for _, h := range history[max(len(history)-6, 0):] {
			fmt.Fprintf(&b, "- %s\n", h)
		}
	}

	b.WriteString("\n" + t.Reminder + "\n")
	return b.String()
}

// ClientState is the game as the frontend sees it (the game_state message).
type ClientState struct {
	Rooms     []ClientRoom    `json:"rooms"`
	Inventory []InventoryItem `json:"inventory"`
	Oxygen    int             `json:"oxygen"` // minutes left
	Won       bool            `json:"won"`
	History   []string        `json:"history"`
}

// ClientRoom is a room as seen by the frontend.
type ClientRoom struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Image       string       `json:"image,omitempty"`
	Current     bool         `json:"current"`
	Visited     bool         `json:"visited"`
	Items       []ClientItem `json:"items"`
}

// ClientItem is an item as seen by the frontend.
type ClientItem struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Image     string   `json:"image,omitempty"`
	Type      string   `json:"type"`
	Visible   bool     `json:"visible"`
	Inspected bool     `json:"inspected"`
	State     string   `json:"state,omitempty"`
	Contains  []string `json:"contains,omitempty"`
}

// GameStateForClient returns a copy of the game state for the frontend; it
// shares nothing with the live state, so it can be encoded without the lock.
func (e *GameEngine) GameStateForClient() ClientState {
	e.mu.Lock()
	defer e.mu.Unlock()

	current := e.state.Player.CurrentRoom
	rooms := make([]ClientRoom, 0, len(e.state.Rooms))
	for _, roomID := range orderedRoomIDs(e.state) {
		room := e.state.Rooms[roomID]
		items := make([]ClientItem, 0, len(room.Items))
		for _, itemID := range orderedItemIDs(room) {
			item, ok := room.Items[itemID]
			if !ok {
				continue
			}
			items = append(items, ClientItem{
				ID:        itemID,
				Name:      item.Name,
				Image:     item.Image,
				Type:      item.Type,
				Visible:   item.Visible,
				Inspected: item.Inspected,
				State:     item.State,
				Contains:  slices.Clone(item.Contains),
			})
		}
		rooms = append(rooms, ClientRoom{
			ID:          roomID,
			Name:        room.Name,
			Description: room.Description,
			Image:       room.Image,
			Current:     roomID == current,
			Visited:     room.Visited || roomID == current,
			Items:       items,
		})
	}

	return ClientState{
		Rooms:     rooms,
		Inventory: append([]InventoryItem{}, e.state.Player.Inventory...), // [] not null in JSON
		Oxygen:    e.oxygenMinutes(),
		Won:       e.state.Won,
		History:   append([]string{}, e.state.Player.History...),
	}
}

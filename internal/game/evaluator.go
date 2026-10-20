package game

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
)

// oxygenSupply is the time a player has from the start of a game.
const oxygenSupply = 45 * time.Minute

// GameEngine owns a GameState and serializes every access to it.
type GameEngine struct {
	mu        sync.Mutex
	state     *GameState
	startedAt time.Time
}

// NewGameEngine creates an engine for state; the oxygen countdown starts now.
func NewGameEngine(state *GameState) *GameEngine {
	return &GameEngine{state: state, startedAt: time.Now()}
}

// ReloadState replaces the game state and restarts the oxygen countdown.
func (e *GameEngine) ReloadState(state *GameState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = state
	e.startedAt = time.Now()
}

// oxygenMinutes returns the oxygen left, rounded up so a fresh game shows the
// full 45 minutes the intro announces. Callers must hold the engine lock.
func (e *GameEngine) oxygenMinutes() int {
	remaining := max(oxygenSupply-time.Since(e.startedAt), 0)
	return int(math.Ceil(remaining.Minutes()))
}

// Text returns the engine sentences for the loaded scenario's language.
func (e *GameEngine) Text() *Text {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.Text
}

// ---------------------------------------------------------------------------
// LLM tools
// ---------------------------------------------------------------------------

// tool is one LLM function the engine executes.
type tool struct {
	params []string // required arguments, in the order run receives them
	run    func(e *GameEngine, args []string) (response, sfx string, err error)
}

var tools = map[string]tool{
	"inspect_item": {[]string{"target_item"}, func(e *GameEngine, a []string) (string, string, error) {
		return e.inspectItem(a[0])
	}},
	"take_item": {[]string{"target_item"}, func(e *GameEngine, a []string) (string, string, error) {
		return e.takeItem(a[0])
	}},
	"use_item": {[]string{"inventory_item", "target_item"}, func(e *GameEngine, a []string) (string, string, error) {
		return e.useItem(a[0], a[1])
	}},
	"go_to": {[]string{"target_room"}, func(e *GameEngine, a []string) (string, string, error) {
		return e.goTo(a[0])
	}},
	"input_pin_code": {[]string{"pin_code"}, func(e *GameEngine, a []string) (string, string, error) {
		return e.inputPinCode(a[0])
	}},
}

// ToolNames lists the LLM functions the engine executes, sorted.
func ToolNames() []string {
	return slices.Sorted(maps.Keys(tools))
}

// ProcessLLMFunctionCall runs one LLM tool call. It returns the JSON result
// to feed back to the LLM and the sound effect to play ("" for none).
func (e *GameEngine) ProcessLLMFunctionCall(call FunctionCall) (string, string) {
	t, ok := tools[call.Name]
	if !ok {
		return toolJSON("error", "unknown function: "+call.Name), ""
	}
	args := make([]string, len(t.params))
	for i, name := range t.params {
		v, ok := call.Arguments[name]
		if !ok {
			return toolJSON("error", "missing "+name+" parameter"), ""
		}
		// LLMs sometimes send a PIN as a JSON number (8421) instead of a string.
		if s, isString := v.(string); isString {
			args[i] = s
		} else {
			args[i] = fmt.Sprint(v)
		}
	}
	response, sfx, err := t.run(e, args)
	if err != nil {
		return toolJSON("error", err.Error()), ""
	}
	return toolJSON("result", response), sfx
}

// toolJSON builds the {"<key>": "<msg>"} object returned to the LLM.
func toolJSON(key, msg string) string {
	b, _ := json.Marshal(map[string]string{key: msg}) // a string map always marshals
	return string(b)
}

// ---------------------------------------------------------------------------
// Action handlers
// ---------------------------------------------------------------------------

// currentRoom returns the player's room. Callers must hold the engine lock.
func (e *GameEngine) currentRoom() (*RoomState, error) {
	room, ok := e.state.Rooms[e.state.Player.CurrentRoom]
	if !ok {
		return nil, fmt.Errorf("room %s not found", e.state.Player.CurrentRoom)
	}
	return room, nil
}

// addHistory records a player action, shown to the LLM and the UI.
func (e *GameEngine) addHistory(format string, args ...any) {
	e.state.Player.History = append(e.state.Player.History, fmt.Sprintf(format, args...))
}

// grantEffects hands over what an item produces (each produced item must be
// declared in the room and is given once) and reveals/unlocks what it opens.
// Returns the names of the produced items.
func (e *GameEngine) grantEffects(room *RoomState, item *RoomItem) []string {
	var names []string
	for _, id := range item.Produces {
		produced, ok := room.Items[id]
		if !ok {
			log.Printf("grantEffects: %q produces %q, which is not (or no longer) in the room", item.Name, id)
			continue
		}
		delete(room.Items, id)
		e.state.Player.Inventory = append(e.state.Player.Inventory, InventoryItem{
			ID:    id,
			Name:  produced.Name,
			State: produced.State,
			Image: produced.Image,
		})
		names = append(names, produced.Name)
	}
	for _, id := range item.Unlocks {
		if unlocked, ok := room.Items[id]; ok {
			unlocked.Visible = true
			if unlocked.State == "locked" {
				unlocked.State = "unlocked"
			}
		}
	}
	return names
}

// revealContents makes every item nested in container visible in the room.
// Returns true if at least one new item was revealed.
func revealContents(room *RoomState, container *RoomItem) bool {
	revealed := false
	for _, nestedID := range container.Contains {
		if nested, ok := room.Items[nestedID]; ok && !nested.Visible {
			nested.Visible = true
			revealed = true
		}
	}
	return revealed
}

// removeFromInventory drops an item from the player's inventory.
func (e *GameEngine) removeFromInventory(itemID string) {
	e.state.Player.Inventory = slices.DeleteFunc(e.state.Player.Inventory, func(it InventoryItem) bool {
		return it.ID == itemID
	})
}

// inspectItem processes an inspect action.
func (e *GameEngine) inspectItem(itemID string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	room, err := e.currentRoom()
	if err != nil {
		return "", "", err
	}
	_, item := resolveRoomItem(room, itemID)
	if item == nil {
		log.Printf("inspectItem: item %q not found in room %s", itemID, room.RoomID)
		return e.state.Text.NotHere, "", nil
	}

	item.Inspected = true
	response = item.DescriptionOnInspect
	if response == "" {
		response = fmt.Sprintf(e.state.Text.NothingSpecial, item.Name)
	}
	if revealContents(room, item) {
		sfx = "sfx_item_discovered"
	}
	e.addHistory(e.state.Text.HistInspected, item.Name)
	return response, sfx, nil
}

// takeItem moves a collectible room item into the inventory.
func (e *GameEngine) takeItem(itemID string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	room, err := e.currentRoom()
	if err != nil {
		return "", "", err
	}
	resolvedID, item := resolveRoomItem(room, itemID)
	if item == nil {
		log.Printf("takeItem: item %q not found in room %s", itemID, room.RoomID)
		return e.state.Text.NotHere, "", nil
	}
	if !item.IsCollectible {
		return fmt.Sprintf(e.state.Text.NotCollectible, item.Name), "", nil
	}

	// Anything hidden inside must not vanish with the container.
	revealContents(room, item)
	e.state.Player.Inventory = append(e.state.Player.Inventory, InventoryItem{
		ID:    resolvedID,
		Name:  item.Name,
		State: item.State,
		Image: item.Image,
	})
	delete(room.Items, resolvedID)
	e.addHistory(e.state.Text.HistTook, item.Name)
	return fmt.Sprintf(e.state.Text.Took, item.Name), "sfx_item_pickup", nil
}

// useItem uses an inventory item on a room item.
func (e *GameEngine) useItem(inventoryItemID, targetItemID string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	invID := e.resolveInventoryItem(inventoryItemID)
	if invID == "" {
		return e.state.Text.NotInInventory, "", nil
	}
	room, err := e.currentRoom()
	if err != nil {
		return "", "", err
	}
	resolvedTargetID, targetItem := resolveRoomItem(room, targetItemID)
	if targetItem == nil {
		return e.state.Text.TargetNotHere, "", nil
	}
	if targetItem.Used {
		return fmt.Sprintf(e.state.Text.AlreadyDone, targetItem.Name), "", nil
	}
	if targetItem.Requires == "" || targetItem.Requires != invID {
		if targetItem.FailMessage != "" {
			return targetItem.FailMessage, "", nil
		}
		return e.state.Text.DidNotWork, "", nil
	}

	// Requirement met: apply the state transition.
	switch {
	case targetItem.SuccessState != "":
		targetItem.State = targetItem.SuccessState
	case targetItem.State == "locked":
		targetItem.State = "unlocked"
	case targetItem.State == "waiting_card":
		targetItem.State = "waiting_pin"
	}
	targetItem.Used = true

	// Reveal anything the action uncovers (e.g. a key freed from the ice).
	revealContents(room, targetItem)
	e.grantEffects(room, targetItem)

	// Tools such as the torch are needed more than once; only marked items go.
	if targetItem.ConsumesItem {
		e.removeFromInventory(invID)
	}
	if targetItem.WinsGame {
		e.state.Won = true
		e.state.Player.History = append(e.state.Player.History, e.state.Text.HistWon)
	}
	e.addHistory(e.state.Text.HistUsed, invID, targetItem.Name, resolvedTargetID)

	if targetItem.SuccessMessage != "" {
		return targetItem.SuccessMessage, targetItem.SuccessSFX, nil
	}
	return e.state.Text.Worked, targetItem.SuccessSFX, nil
}

// goTo moves the player to another room, only through one of the current
// room's exits and only if that exit's door is not locked (hidden doors
// block too).
func (e *GameEngine) goTo(targetRoomID string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	resolved := resolveRoomID(e.state, targetRoomID)
	if resolved == "" {
		return fmt.Sprintf(e.state.Text.NoRoute, targetRoomID), "", nil
	}
	target := e.state.Rooms[resolved]
	if e.state.Player.CurrentRoom == resolved {
		return e.state.Text.AlreadyThere, "", nil
	}

	current, err := e.currentRoom()
	if err != nil {
		return "", "", err
	}
	doorID, ok := current.Exits[resolved]
	if !ok {
		return fmt.Sprintf(e.state.Text.NoDirectPassage, target.Name), "", nil
	}
	if door := current.Items[doorID]; door != nil && door.State == "locked" {
		msg := fmt.Sprintf(e.state.Text.PassageLocked, target.Name)
		if door.Visible && door.DescriptionOnInspect != "" {
			msg += " " + door.DescriptionOnInspect
		}
		return msg, "", nil
	}

	e.state.Player.CurrentRoom = resolved
	target.Visited = true
	e.addHistory(e.state.Text.HistMoved, target.Name)

	resp := fmt.Sprintf(e.state.Text.Enter, target.Name)
	if target.Description != "" {
		resp += " " + target.Description
	}
	return resp, "sfx_door_open", nil
}

// inputPinCode enters a PIN on whichever visible device in the room is
// waiting for one.
func (e *GameEngine) inputPinCode(pinCode string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	room, err := e.currentRoom()
	if err != nil {
		return "", "", err
	}
	var device *RoomItem
	for _, item := range room.Items {
		if item.Visible && item.State == "waiting_pin" && item.PinCode != "" {
			device = item
			break
		}
	}
	if device == nil {
		return e.state.Text.NoPinDevice, "", nil
	}

	// Keep only digits: the transcription may yield "8-4-2-1" or "8 4 2 1".
	entered := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, pinCode)
	if entered != device.PinCode {
		e.addHistory(e.state.Text.HistWrongPin, entered, device.Name)
		return e.state.Text.WrongPin, "sfx_error_buzzer", nil
	}

	if device.PinSuccessState != "" {
		device.State = device.PinSuccessState
	} else {
		device.State = "unlocked"
	}
	// Hand over whatever the device produces (e.g. the encoded card).
	producedNames := e.grantEffects(room, device)
	e.addHistory(e.state.Text.HistRightPin, device.Name)

	response = device.PinSuccessMessage
	if response == "" {
		response = e.state.Text.PinAccepted
		if len(producedNames) > 0 {
			response += " " + fmt.Sprintf(e.state.Text.YouGet, strings.Join(producedNames, ", "))
		}
	}
	return response, "sfx_access_granted", nil
}

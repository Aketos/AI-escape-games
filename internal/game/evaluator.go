package game

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// GameEngine encapsulates the GameState and a mutex for thread-safe operations.
type GameEngine struct {
	mu    sync.Mutex
	State *GameState
}

// NewGameEngine creates a new GameEngine instance.
func NewGameEngine(state *GameState) *GameEngine {
	return &GameEngine{
		State: state,
	}
}

// HandleInspectItem processes an inspect action.
func (e *GameEngine) HandleInspectItem(playerID, itemID string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	roomID := e.State.Player.CurrentRoom
	room, ok := e.State.Rooms[roomID]
	if !ok {
		return "", "", fmt.Errorf("room %s not found", roomID)
	}

	item, ok := room.Items[itemID]
	if !ok || !item.Visible {
		return "Cet objet n'est pas ici ou n'est pas visible.", "", nil
	}

	item.Inspected = true
	response = item.DescriptionOnInspect
	if response == "" {
		response = fmt.Sprintf("Vous inspectez %s. Il n'y a rien de particulier.", item.Name)
	}

	// Make nested items visible
	if len(item.Contains) > 0 {
		sfx = "sfx_item_discovered"
	}
	for _, nestedID := range item.Contains {
		if nestedItem, ok := room.Items[nestedID]; ok {
			nestedItem.Visible = true
		}
	}

	// Append to history
	actionDesc := fmt.Sprintf("A inspecté : %s", item.Name)
	e.State.Player.History = append(e.State.Player.History, actionDesc)

	return response, sfx, nil
}

// HandleTakeItem processes taking an item into the inventory.
func (e *GameEngine) HandleTakeItem(playerID, itemID string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	roomID := e.State.Player.CurrentRoom
	room, ok := e.State.Rooms[roomID]
	if !ok {
		return "", "", fmt.Errorf("room %s not found", roomID)
	}

	item, ok := room.Items[itemID]
	if !ok || !item.Visible {
		return "Cet objet n'est pas ici ou n'est pas visible.", "", nil
	}

	if !item.IsCollectible {
		return "Vous ne pouvez pas ramasser cela.", "", nil
	}

	// Add to inventory
	e.State.Player.Inventory = append(e.State.Player.Inventory, InventoryItem{
		ID:    itemID,
		Name:  item.Name,
		State: item.State,
	})

	// Remove from room or mark as collected (here we remove it for simplicity)
	delete(room.Items, itemID)

	// Append to history
	actionDesc := fmt.Sprintf("A ramassé : %s", item.Name)
	e.State.Player.History = append(e.State.Player.History, actionDesc)

	return fmt.Sprintf("Vous avez pris %s.", item.Name), "sfx_item_pickup", nil
}

// HandleUseItem processes using an inventory item on a room item.
func (e *GameEngine) HandleUseItem(playerID, inventoryItemID, targetItemID string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Verify player has the item in inventory
	hasItem := false
	for _, invItem := range e.State.Player.Inventory {
		if invItem.ID == inventoryItemID {
			hasItem = true
			break
		}
	}

	if !hasItem {
		return "Vous n'avez pas cet objet dans votre inventaire.", "", nil
	}

	roomID := e.State.Player.CurrentRoom
	room, ok := e.State.Rooms[roomID]
	if !ok {
		return "", "", fmt.Errorf("room %s not found", roomID)
	}

	targetItem, ok := room.Items[targetItemID]
	if !ok || !targetItem.Visible {
		return "La cible n'est pas ici ou n'est pas visible.", "", nil
	}

	// Verify requires logic
	if targetItem.Requires != inventoryItemID {
		if targetItem.FailMessage != "" {
			return targetItem.FailMessage, "", nil
		}
		return "Cela ne fonctionne pas.", "", nil
	}

	// Valid target requirement met, update target state (e.g. locked -> unlocked)
	if targetItem.State == "locked" {
		targetItem.State = "unlocked"
	} else if targetItem.State == "waiting_card" {
		targetItem.State = "waiting_pin"
	}

	// Append to history
	actionDesc := fmt.Sprintf("A utilisé %s sur %s", inventoryItemID, targetItem.Name)
	e.State.Player.History = append(e.State.Player.History, actionDesc)

	if targetItem.SuccessMessage != "" {
		return targetItem.SuccessMessage, targetItem.SuccessSFX, nil
	}
	return "Cela a fonctionné.", targetItem.SuccessSFX, nil
}

// HandleInputPinCode processes an input_pin_code action.
func (e *GameEngine) HandleInputPinCode(playerID string, pinCode string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	roomID := e.State.Player.CurrentRoom
	room, ok := e.State.Rooms[roomID]
	if !ok {
		return "", "", fmt.Errorf("room %s not found", roomID)
	}

	encodeur, ok := room.Items["encodeur_magnetique"]
	if !ok || !encodeur.Visible {
		return "L'encodeur n'est pas ici ou n'est pas visible.", "", nil
	}

	if encodeur.State != "waiting_pin" {
		return "L'encodeur n'attend pas de code PIN pour le moment. Insérez d'abord une carte vierge.", "", nil
	}

	if pinCode == "8421" {
		encodeur.State = "encoded"

		carteEncodee, ok := room.Items["carte_encodee"]
		if ok {
			e.State.Player.Inventory = append(e.State.Player.Inventory, InventoryItem{
				ID:    "carte_encodee",
				Name:  carteEncodee.Name,
				State: carteEncodee.State,
			})
			delete(room.Items, "carte_encodee")
		} else {
			e.State.Player.Inventory = append(e.State.Player.Inventory, InventoryItem{
				ID:   "carte_encodee",
				Name: "Carte d'accès niveau 1",
			})
		}

		e.State.Player.History = append(e.State.Player.History, "A entré le bon code PIN (8421) sur l'encodeur.")

		return "Code accepté. L'encodeur recrache une carte d'accès de niveau 1 encodée.", "sfx_access_granted", nil
	}

	e.State.Player.History = append(e.State.Player.History, fmt.Sprintf("A entré un mauvais code PIN (%s) sur l'encodeur.", pinCode))
	return "Code PIN incorrect.", "sfx_error_buzzer", nil
}

// StateSnapshot returns a French textual summary of the current game state,
// suitable for injection into the LLM system prompt. Only visible items are
// included so the LLM cannot leak hidden content.
func (e *GameEngine) StateSnapshot() string {
	e.mu.Lock()
	defer e.mu.Unlock()

	var b strings.Builder

	room, ok := e.State.Rooms[e.State.Player.CurrentRoom]
	if ok {
		fmt.Fprintf(&b, "Salle actuelle : %s — %s\n", room.Name, room.Description)
		b.WriteString("Objets VISIBLES (les SEULS objets dont tu as le droit de parler) :\n")
		for id, item := range room.Items {
			if !item.Visible {
				continue
			}
			fmt.Fprintf(&b, "- %s (id: %s)", item.Name, id)
			if item.State != "" {
				fmt.Fprintf(&b, " [état: %s]", item.State)
			}
			if item.Inspected {
				b.WriteString(" [déjà inspecté]")
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("Inventaire du joueur :\n")
	if len(e.State.Player.Inventory) == 0 {
		b.WriteString("- (vide)\n")
	}
	for _, inv := range e.State.Player.Inventory {
		fmt.Fprintf(&b, "- %s (id: %s)\n", inv.Name, inv.ID)
	}

	if n := len(e.State.Player.History); n > 0 {
		b.WriteString("Dernières actions du joueur :\n")
		start := n - 6
		if start < 0 {
			start = 0
		}
		for _, h := range e.State.Player.History[start:] {
			fmt.Fprintf(&b, "- %s\n", h)
		}
	}

	return b.String()
}

// ProcessLLMFunctionCall acts as a router for incoming LLM function calls.
func (e *GameEngine) ProcessLLMFunctionCall(call FunctionCall) (string, string) {
	// For simplicity, grab the single player's ID
	playerID := e.State.Player.PlayerID

	var response string
	var sfx string
	var err error

	switch call.Name {
	case "inspect_item":
		targetObj, ok := call.Arguments["target_item"]
		if !ok {
			return `{"error": "Missing target_item parameter"}`, ""
		}
		targetItem, _ := targetObj.(string)
		response, sfx, err = e.HandleInspectItem(playerID, targetItem)

	case "take_item":
		targetObj, ok := call.Arguments["target_item"]
		if !ok {
			return `{"error": "Missing target_item parameter"}`, ""
		}
		targetItem, _ := targetObj.(string)
		response, sfx, err = e.HandleTakeItem(playerID, targetItem)

	case "use_item":
		invObj, ok := call.Arguments["inventory_item"]
		if !ok {
			return `{"error": "Missing inventory_item parameter"}`, ""
		}
		invItem, _ := invObj.(string)

		targetObj, ok := call.Arguments["target_item"]
		if !ok {
			return `{"error": "Missing target_item parameter"}`, ""
		}
		targetItem, _ := targetObj.(string)
		response, sfx, err = e.HandleUseItem(playerID, invItem, targetItem)

	case "input_pin_code":
		pinObj, ok := call.Arguments["pin_code"]
		if !ok {
			return `{"error": "Missing pin_code parameter"}`, ""
		}
		pinCode, _ := pinObj.(string)
		response, sfx, err = e.HandleInputPinCode(playerID, pinCode)

	default:
		return fmt.Sprintf(`{"error": "Unknown function: %s"}`, call.Name), ""
	}

	if err != nil {
		return fmt.Sprintf(`{"error": "%s"}`, err.Error()), ""
	}

	// Format result as JSON
	result := map[string]string{
		"result": response,
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf(`{"error": "Failed to marshal response: %v"}`, err), ""
	}

	return string(resultBytes), sfx
}

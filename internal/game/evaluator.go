package game

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// GameEngine encapsulates the GameState and a mutex for thread-safe operations.
type GameEngine struct {
	mu        sync.Mutex
	State     *GameState
	startedAt time.Time
	oxygen    time.Duration
}

// NewGameEngine creates a new GameEngine instance. The oxygen countdown
// starts immediately: the scenario gives the player 45 minutes.
func NewGameEngine(state *GameState) *GameEngine {
	return &GameEngine{
		State:     state,
		startedAt: time.Now(),
		oxygen:    45 * time.Minute,
	}
}

// ReloadState replaces the game state and resets the oxygen timer.
func (e *GameEngine) ReloadState(state *GameState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.State = state
	e.startedAt = time.Now()
	e.oxygen = 45 * time.Minute
}

// Lock locks the game engine for safe concurrent access.
func (e *GameEngine) Lock() {
	e.mu.Lock()
}

// Unlock unlocks the game engine.
func (e *GameEngine) Unlock() {
	e.mu.Unlock()
}

// OxygenRemaining returns how much oxygen time is left (never negative).
func (e *GameEngine) OxygenRemaining() time.Duration {
	remaining := e.oxygen - time.Since(e.startedAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// IsWon reports whether the victory condition has been reached.
func (e *GameEngine) IsWon() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.State.Won
}

// ---------------------------------------------------------------------------
// Fuzzy matching
//
// Voice transcription mangles IDs: the player says "la fiole", the LLM
// dictates "Requête prendre fiole uv", the parser squashes it to
// "fioleuv". All resolution is therefore done on normalized keys shared
// between the engine and the voice parser.
// ---------------------------------------------------------------------------

var accentReplacer = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i",
	"ô", "o", "ö", "o",
	"ù", "u", "û", "u", "ü", "u",
	"ç", "c", "œ", "oe",
)

// NormalizeKey lowercases, strips accents and removes everything that is not
// a letter or a digit, so "Requête Fiole UV" becomes "requetefioleuv".
func NormalizeKey(s string) string {
	s = accentReplacer.Replace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// minFuzzyLen avoids absurd substring matches ("le" matching half the room).
const minFuzzyLen = 4

var stopWords = map[string]bool{
	"les": true, "des": true, "une": true, "aux": true, "sur": true,
	"par": true, "pres": true, "dans": true, "avec": true, "vers": true,
}

// significantWords splits an id or display name into normalized words worth
// matching individually ("Le bureau au fond" -> ["bureau", "fond"]).
func significantWords(s string) []string {
	s = accentReplacer.Replace(strings.ToLower(s))
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	var out []string
	for _, w := range fields {
		if len(w) >= 3 && !stopWords[w] {
			out = append(out, w)
		}
	}
	return out
}

// itemMatchScore rates how well a normalized key designates an item.
// 0 means no match; exact matches dominate everything else.
func itemMatchScore(key, id, name string) int {
	normID, normName := NormalizeKey(id), NormalizeKey(name)
	if key == normID || (normName != "" && key == normName) {
		return 1 << 20
	}
	score := 0
	if len(normID) >= minFuzzyLen && strings.Contains(key, normID) {
		score += len(normID) * 4
	} else if len(key) >= minFuzzyLen && strings.Contains(normID, key) {
		score += len(key) * 2
	}
	if normName != "" {
		if len(normName) >= minFuzzyLen && strings.Contains(key, normName) {
			score += len(normName) * 4
		} else if len(key) >= minFuzzyLen && strings.Contains(normName, key) {
			score += len(key) * 2
		}
	}
	for _, w := range significantWords(id) {
		if strings.Contains(key, w) {
			score += len(w)
		}
	}
	for _, w := range significantWords(name) {
		if strings.Contains(key, w) {
			score += len(w)
		}
	}
	return score
}

// resolveRoomItem finds the visible room item best matching a raw reference
// (exact ID, squashed transcription, display name or a significant word of
// it). Must be called with the engine lock held.
func resolveRoomItem(room *RoomState, raw string) (string, *RoomItem) {
	if item, ok := room.Items[raw]; ok && item.Visible {
		return raw, item
	}
	key := NormalizeKey(raw)
	if key == "" {
		return "", nil
	}

	// Strip a leading "zone" prefix from the key when comparing, since the
	// player often says "inspecter la zone au sol" but the item ID is
	// "zone_sol" — the word "zone" inflates the score for every zone item
	// and can cause wrong matches.
	compareKey := strings.TrimPrefix(key, "zone")
	zoneStripped := compareKey != key

	bestScore := 0
	var bestID string
	var best *RoomItem
	for id, item := range room.Items {
		if !item.Visible {
			continue
		}
		s := itemMatchScore(key, id, item.Name)
		// Also score with the zone-stripped key for zone items
		if item.Type == "zone" && zoneStripped {
			s2 := itemMatchScore(compareKey, id, item.Name)
			if s2 > s {
				s = s2
			}
		}
		if s > bestScore {
			bestScore, bestID, best = s, id, item
		}
	}
	return bestID, best
}

// resolveInventoryItem finds the inventory item best matching a raw
// reference. Must be called with the engine lock held.
func (e *GameEngine) resolveInventoryItem(raw string) string {
	key := NormalizeKey(raw)
	if key == "" {
		return ""
	}
	bestScore := 0
	bestID := ""
	for _, inv := range e.State.Player.Inventory {
		if inv.ID == raw {
			return inv.ID
		}
		if s := itemMatchScore(key, inv.ID, inv.Name); s > bestScore {
			bestScore, bestID = s, inv.ID
		}
	}
	return bestID
}

// ItemKey exposes the normalized identifiers of a visible item so the voice
// parser can confirm a match without touching engine internals.
type ItemKey struct {
	ID       string
	NormID   string
	NormName string
}

// VisibleItemKeys returns the normalized keys of every visible item in the
// player's current room (thread-safe snapshot).
func (e *GameEngine) VisibleItemKeys() []ItemKey {
	e.mu.Lock()
	defer e.mu.Unlock()

	room, ok := e.State.Rooms[e.State.Player.CurrentRoom]
	if !ok {
		return nil
	}
	keys := make([]ItemKey, 0, len(room.Items))
	for id, item := range room.Items {
		if !item.Visible {
			continue
		}
		keys = append(keys, ItemKey{
			ID:       id,
			NormID:   NormalizeKey(id),
			NormName: NormalizeKey(item.Name),
		})
	}
	return keys
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
	inv := e.State.Player.Inventory
	for i, item := range inv {
		if item.ID == itemID {
			e.State.Player.Inventory = append(inv[:i], inv[i+1:]...)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Action handlers
// ---------------------------------------------------------------------------

// HandleInspectItem processes an inspect action.
func (e *GameEngine) HandleInspectItem(playerID, itemID string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	roomID := e.State.Player.CurrentRoom
	room, ok := e.State.Rooms[roomID]
	if !ok {
		return "", "", fmt.Errorf("room %s not found", roomID)
	}

	_, item := resolveRoomItem(room, itemID)
	if item == nil {
		log.Printf("HandleInspectItem: item %q not found in room %s", itemID, roomID)
		return "Cet objet n'est pas ici ou n'est pas visible.", "", nil
	}

	log.Printf("HandleInspectItem: inspecting %q (type=%s, contains=%v)", item.Name, item.Type, item.Contains)
	item.Inspected = true
	response = item.DescriptionOnInspect
	if response == "" {
		response = fmt.Sprintf("Vous inspectez %s. Il n'y a rien de particulier.", item.Name)
	}

	revealed := revealContents(room, item)
	if revealed {
		sfx = "sfx_item_discovered"
		log.Printf("HandleInspectItem: revealed new items from %q", item.Name)
	}

	e.State.Player.History = append(e.State.Player.History, fmt.Sprintf("A inspecté : %s", item.Name))

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

	resolvedID, item := resolveRoomItem(room, itemID)
	if item == nil {
		log.Printf("HandleTakeItem: item %q not found in room %s", itemID, roomID)
		return "Cet objet n'est pas ici ou n'est pas visible.", "", nil
	}

	if !item.IsCollectible {
		log.Printf("HandleTakeItem: %q is not collectible", item.Name)
		return fmt.Sprintf("%s ne peut pas être emporté.", item.Name), "", nil
	}

	log.Printf("HandleTakeItem: taking %q (id=%s)", item.Name, resolvedID)
	// Anything hidden inside must not vanish with the container.
	revealContents(room, item)

	e.State.Player.Inventory = append(e.State.Player.Inventory, InventoryItem{
		ID:    resolvedID,
		Name:  item.Name,
		State: item.State,
	})
	delete(room.Items, resolvedID)
	log.Printf("HandleTakeItem: inventory now has %d items", len(e.State.Player.Inventory))

	e.State.Player.History = append(e.State.Player.History, fmt.Sprintf("A ramassé : %s", item.Name))

	return fmt.Sprintf("Vous avez pris %s.", item.Name), "sfx_item_pickup", nil
}

// HandleUseItem processes using an inventory item on a room item.
func (e *GameEngine) HandleUseItem(playerID, inventoryItemID, targetItemID string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	invID := e.resolveInventoryItem(inventoryItemID)
	if invID == "" {
		return "Vous n'avez pas cet objet dans votre inventaire.", "", nil
	}

	roomID := e.State.Player.CurrentRoom
	room, ok := e.State.Rooms[roomID]
	if !ok {
		return "", "", fmt.Errorf("room %s not found", roomID)
	}

	resolvedTargetID, targetItem := resolveRoomItem(room, targetItemID)
	if targetItem == nil {
		return "La cible n'est pas ici ou n'est pas visible.", "", nil
	}

	if targetItem.SuccessState != "" && targetItem.State == targetItem.SuccessState {
		return fmt.Sprintf("C'est déjà fait : %s a déjà été activé.", targetItem.Name), "", nil
	}

	if targetItem.Requires == "" || targetItem.Requires != invID {
		if targetItem.FailMessage != "" {
			return targetItem.FailMessage, "", nil
		}
		return "Cela ne fonctionne pas.", "", nil
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

	// Reveal anything the action uncovers (e.g. a key freed from the ice).
	revealContents(room, targetItem)

	// Consume the inventory item if the target uses it up (e.g. blank card
	// swallowed by the encoder).
	if targetItem.ConsumesItem {
		e.removeFromInventory(invID)
	}

	if targetItem.WinsGame {
		e.State.Won = true
		e.State.Player.History = append(e.State.Player.History, "A déverrouillé la porte principale : MISSION ACCOMPLIE.")
	}

	e.State.Player.History = append(e.State.Player.History, fmt.Sprintf("A utilisé %s sur %s (%s)", invID, targetItem.Name, resolvedTargetID))

	if targetItem.SuccessMessage != "" {
		return targetItem.SuccessMessage, targetItem.SuccessSFX, nil
	}
	return "Cela a fonctionné.", targetItem.SuccessSFX, nil
}

// HandleInputPinCode processes an input_pin_code action. The device is found
// dynamically: any visible item in the room currently waiting for a PIN.
func (e *GameEngine) HandleInputPinCode(playerID string, pinCode string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	roomID := e.State.Player.CurrentRoom
	room, ok := e.State.Rooms[roomID]
	if !ok {
		return "", "", fmt.Errorf("room %s not found", roomID)
	}

	var device *RoomItem
	for _, item := range room.Items {
		if item.Visible && item.State == "waiting_pin" && item.PinCode != "" {
			device = item
			break
		}
	}
	if device == nil {
		return "Aucun appareil n'attend de code PIN pour le moment.", "", nil
	}

	// Keep only digits: the transcription may yield "8-4-2-1" or "8 4 2 1".
	var digits strings.Builder
	for _, r := range pinCode {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	entered := digits.String()

	if entered != device.PinCode {
		e.State.Player.History = append(e.State.Player.History, fmt.Sprintf("A entré un mauvais code PIN (%s) sur %s.", entered, device.Name))
		return "Code PIN incorrect.", "sfx_error_buzzer", nil
	}

	if device.PinSuccessState != "" {
		device.State = device.PinSuccessState
	} else {
		device.State = "unlocked"
	}

	// Hand over whatever the device produces (e.g. the encoded card).
	var producedNames []string
	for _, producedID := range device.Produces {
		name := producedID
		state := ""
		if produced, ok := room.Items[producedID]; ok {
			name = produced.Name
			state = produced.State
			delete(room.Items, producedID)
		}
		e.State.Player.Inventory = append(e.State.Player.Inventory, InventoryItem{
			ID:    producedID,
			Name:  name,
			State: state,
		})
		producedNames = append(producedNames, name)
	}

	e.State.Player.History = append(e.State.Player.History, fmt.Sprintf("A entré le bon code PIN sur %s.", device.Name))

	response = device.PinSuccessMessage
	if response == "" {
		response = "Code accepté."
		if len(producedNames) > 0 {
			response += " Vous obtenez : " + strings.Join(producedNames, ", ") + "."
		}
	}
	return response, "sfx_access_granted", nil
}

// ---------------------------------------------------------------------------
// LLM context
// ---------------------------------------------------------------------------

// GetContextString returns a formatted string of the current game state
// directly tailored for the LLM's system prompt.
func (engine *GameEngine) GetContextString() string {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	var sb strings.Builder

	// 1. INVENTAIRE ET SALLE ACTUELLE
	sb.WriteString("=== ÉTAT DU JOUEUR ===\n")
	sb.WriteString(fmt.Sprintf("- Salle actuelle : %s\n", engine.State.Player.CurrentRoom))
	sb.WriteString(fmt.Sprintf("- Oxygène restant : environ %d minutes. Rappelle-le au joueur quand c'est pertinent, avec une urgence croissante.\n", int(engine.OxygenRemaining().Minutes())))

	if engine.State.Won {
		sb.WriteString("- !!! LA PORTE PRINCIPALE EST DÉVERROUILLÉE : LA MISSION EST ACCOMPLIE. Félicite le joueur à ta manière (sarcastique) et conclus la partie. !!!\n")
	}

	if len(engine.State.Player.Inventory) > 0 {
		var invNames []string
		for _, item := range engine.State.Player.Inventory {
			invNames = append(invNames, fmt.Sprintf("%s (commande : '%s')", item.Name, strings.ReplaceAll(item.ID, "_", " ")))
		}
		sb.WriteString("- Inventaire : " + strings.Join(invNames, ", ") + "\n")
	} else {
		sb.WriteString("- Inventaire : Vide\n")
	}

	// 2. ÉLÉMENTS DU JEU (ZONES ET OBJETS)
	sb.WriteString("\n=== ÉLÉMENTS VISIBLES DANS LA PIÈCE ===\n")
	sb.WriteString("RÈGLE ABSOLUE : Tu ne peux interagir qu'avec les éléments listés ci-dessous.\n")
	sb.WriteString("Cependant, fais preuve d'intelligence contextuelle : si le joueur nomme un objet qui fait partie du décor d'une zone (comme un 'caisson' ou une 'grille'), déclenche l'inspection de la zone associée sans le corriger.\n")

	roomID := engine.State.Player.CurrentRoom
	room, ok := engine.State.Rooms[roomID]
	visibleCount := 0

	if ok {
		for id, item := range room.Items {
			if !item.Visible {
				continue
			}
			visibleCount++
			spoken := strings.ReplaceAll(id, "_", " ")

			switch item.Type {
			case "zone":
				sb.WriteString(fmt.Sprintf("- ZONE VISIBLE | Nom : '%s' | Commande vocale : '%s'\n", item.Name, spoken))
			default:
				sb.WriteString(fmt.Sprintf("- OBJET VISIBLE | Nom : '%s' | Commande vocale : '%s'\n", item.Name, spoken))
			}

			if item.State != "" {
				sb.WriteString(fmt.Sprintf("  État : %s\n", item.State))
			}
			if item.Inspected {
				sb.WriteString("  [Déjà inspecté]\n")
			}
			if item.DescriptionOnInspect != "" && item.Inspected {
				sb.WriteString(fmt.Sprintf("  Description : %s\n", item.DescriptionOnInspect))
			}
			sb.WriteString("\n")
		}
	}

	if visibleCount == 0 {
		sb.WriteString("- (Aucun élément visible pour le moment)\n")
	}

	return sb.String()
}

// StateSnapshot returns a French textual summary of the current game state,
// suitable for injection into the LLM system prompt. Only visible items are
// included so the LLM cannot leak hidden content.
func (e *GameEngine) StateSnapshot() string {
	e.mu.Lock()
	defer e.mu.Unlock()

	var b strings.Builder

	fmt.Fprintf(&b, "Oxygène restant : environ %d minutes.\n", int(e.OxygenRemaining().Minutes()))
	if e.State.Won {
		b.WriteString("LA PORTE PRINCIPALE EST DÉVERROUILLÉE : MISSION ACCOMPLIE. Conclus la partie.\n")
	}

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

	b.WriteString("\nRAPPEL : Pour toute action du joueur (inspecter, prendre, utiliser), tu DOIS appeler l'outil correspondant. Ne raconte JAMAIS le résultat d'une action sans l'avoir exécutée via l'outil.\n")

	return b.String()
}

// ProcessLLMFunctionCall acts as a router for incoming LLM function calls.
func (e *GameEngine) ProcessLLMFunctionCall(call FunctionCall) (string, string) {
	// For simplicity, grab the single player's ID
	playerID := e.State.Player.PlayerID

	var response string
	var sfx string
	var err error
	log.Printf("DEBUG: LLM function call: %s", call.Name)

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

// ClientRoom is a room as seen by the frontend.
type ClientRoom struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Current     bool         `json:"current"`
	Visited     bool         `json:"visited"`
	Items       []ClientItem `json:"items"`
}

// ClientItem is an item as seen by the frontend.
type ClientItem struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Visible   bool     `json:"visible"`
	Inspected bool     `json:"inspected"`
	State     string   `json:"state,omitempty"`
	Contains  []string `json:"contains,omitempty"`
}

// ClientInventoryItem is an inventory item as seen by the frontend.
type ClientInventoryItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
}

// GameStateForClient returns a JSON-serializable snapshot of the game state
// suitable for sending to the frontend.
func (e *GameEngine) GameStateForClient() map[string]interface{} {
	e.mu.Lock()
	defer e.mu.Unlock()

	rooms := make([]ClientRoom, 0, len(e.State.Rooms))
	currentRoom := e.State.Player.CurrentRoom

	for roomID, room := range e.State.Rooms {
		visited := false
		for _, h := range e.State.Player.History {
			if strings.Contains(h, roomID) || strings.Contains(h, room.Name) {
				visited = true
				break
			}
		}
		// Mark as visited if it's the current room
		if roomID == currentRoom {
			visited = true
		}

		items := make([]ClientItem, 0, len(room.Items))
		for itemID, item := range room.Items {
			ci := ClientItem{
				ID:        itemID,
				Name:      item.Name,
				Type:      item.Type,
				Visible:   item.Visible,
				Inspected: item.Inspected,
				State:     item.State,
			}
			if len(item.Contains) > 0 {
				ci.Contains = item.Contains
			}
			items = append(items, ci)
		}

		rooms = append(rooms, ClientRoom{
			ID:          roomID,
			Name:        room.Name,
			Description: room.Description,
			Current:     roomID == currentRoom,
			Visited:     visited,
			Items:       items,
		})
	}

	inventory := make([]ClientInventoryItem, 0, len(e.State.Player.Inventory))
	for _, item := range e.State.Player.Inventory {
		inventory = append(inventory, ClientInventoryItem{
			ID:    item.ID,
			Name:  item.Name,
			State: item.State,
		})
	}

	return map[string]interface{}{
		"rooms":     rooms,
		"inventory": inventory,
		"oxygen":    int(e.OxygenRemaining().Minutes()),
		"won":       e.State.Won,
		"history":   e.State.Player.History,
	}
}

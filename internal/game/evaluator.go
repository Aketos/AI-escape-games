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

// extractWords splits a normalized string into significant words (>=3 chars).
// Since NormalizeKey removes spaces, we can't split on whitespace. Instead we
// look for known French room words as delimiters.
var knownRoomWords = []string{"couloir", "secteur", "salle", "laboratoire", "morgue", "sas", "armurerie", "serre", "hydroponique", "quartiers", "equipage", "generateur", "cryogenisation", "medical", "securite"}

func extractWords(normalized string) []string {
	words := []string{}
	lower := normalized
	for _, kw := range knownRoomWords {
		idx := strings.Index(lower, kw)
		if idx >= 0 {
			words = append(words, kw)
			// Also grab trailing letter (e.g. "a" in "couloira")
			end := idx + len(kw)
			if end < len(lower) && lower[end] >= 'a' && lower[end] <= 'z' {
				words = append(words, string(lower[end]))
			}
		}
	}
	// Also extract single letters a, b that often distinguish sectors
	for _, c := range []string{"a", "b"} {
		if strings.HasSuffix(normalized, c) {
			words = append(words, c)
		}
	}
	return words
}

// wordsOverlap checks if two normalized strings share at least 2 significant
// words. Used to match "porteverslecouloira" with "couloirsecteura".
func wordsOverlap(a, b string) bool {
	wa := extractWords(a)
	wb := extractWords(b)
	if len(wa) < 1 || len(wb) < 1 {
		return false
	}
	shared := 0
	for _, x := range wa {
		for _, y := range wb {
			if x == y && len(x) >= 1 {
				shared++
				break
			}
		}
	}
	return shared >= 2
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
		Image: item.Image,
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

	// Reveal any items unlocked by this action (e.g. a door becomes visible).
	for _, unlockedID := range targetItem.Unlocks {
		if unlocked, ok := room.Items[unlockedID]; ok {
			unlocked.Visible = true
			if unlocked.State == "locked" {
				unlocked.State = "unlocked"
			}
		}
	}

	// Always consume the inventory item after successful use.
	e.removeFromInventory(invID)

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

// HandleGoTo processes a go_to action, moving the player to a different room.
// The move is allowed only if a door or passage to the target room exists in
// the current room and is unlocked (state != "locked").
func (e *GameEngine) HandleGoTo(playerID, targetRoomID string) (response string, sfx string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Resolve fuzzy room reference to actual room ID
	resolvedRoomID := ""
	for id, room := range e.State.Rooms {
		if id == targetRoomID || strings.EqualFold(id, targetRoomID) {
			resolvedRoomID = id
			break
		}
		// Fuzzy: check if the raw input matches the room name or ID
		key := NormalizeKey(targetRoomID)
		idKey := NormalizeKey(id)
		nameKey := NormalizeKey(room.Name)
		if key == idKey || key == nameKey {
			resolvedRoomID = id
			break
		}
		// Partial match: "couloir" matches "Couloir Secteur A"
		if key != "" && (strings.Contains(idKey, key) || strings.Contains(nameKey, key) || strings.Contains(key, idKey)) {
			resolvedRoomID = id
			break
		}
	}
	if resolvedRoomID == "" {
		return fmt.Sprintf("Vous ne trouvez aucun chemin vers %s.", targetRoomID), "", nil
	}
	targetRoomID = resolvedRoomID

	target, ok := e.State.Rooms[targetRoomID]
	if !ok {
		return fmt.Sprintf("Vous ne trouvez aucun chemin vers %s.", targetRoomID), "", nil
	}

	if e.State.Player.CurrentRoom == targetRoomID {
		return "Vous êtes déjà dans cette pièce.", "", nil
	}

	// Check for a door/passage to the target room in the current room.
	// A door is any visible item whose name references the target room
	// and whose state is "locked" — that blocks movement.
	currentRoom := e.State.Rooms[e.State.Player.CurrentRoom]
	if currentRoom != nil {
		itemKey := ""
		rIDKey := NormalizeKey(targetRoomID)
		rNameKey := NormalizeKey(target.Name)
		for _, item := range currentRoom.Items {
			if !item.Visible {
				continue
			}
			itemKey = NormalizeKey(item.Name)
			// Check if this item is a door to the target room
			if strings.Contains(itemKey, rIDKey) || strings.Contains(itemKey, rNameKey) ||
				wordsOverlap(itemKey, rIDKey) || wordsOverlap(itemKey, rNameKey) {
				if item.State == "locked" {
					return fmt.Sprintf("Le passage vers %s est verrouillé. %s", target.Name, item.DescriptionOnInspect), "", nil
				}
			}
		}
	}

	e.State.Player.CurrentRoom = targetRoomID
	target.Visited = true
	e.State.Player.History = append(e.State.Player.History, fmt.Sprintf("S'est déplacé vers : %s", target.Name))

	log.Printf("HandleGoTo: player moved to %q (%s)", targetRoomID, target.Name)

	resp := fmt.Sprintf("Vous entrez dans %s.", target.Name)
	if target.Description != "" {
		resp += " " + target.Description
	}
	return resp, "sfx_door_open", nil
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
		image := ""
		if produced, ok := room.Items[producedID]; ok {
			name = produced.Name
			state = produced.State
			image = produced.Image
			delete(room.Items, producedID)
		}
		e.State.Player.Inventory = append(e.State.Player.Inventory, InventoryItem{
			ID:    producedID,
			Name:  name,
			State: state,
			Image: image,
		})
		producedNames = append(producedNames, name)
	}

	// Reveal any items unlocked by this action (e.g. a door becomes visible).
	for _, unlockedID := range device.Unlocks {
		if unlocked, ok := room.Items[unlockedID]; ok {
			unlocked.Visible = true
			if unlocked.State == "locked" {
				unlocked.State = "unlocked"
			}
		}
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
		inspectItemIDs := room.ItemOrder
		if len(inspectItemIDs) == 0 {
			for id := range room.Items {
				inspectItemIDs = append(inspectItemIDs, id)
			}
		}
		for _, id := range inspectItemIDs {
			item, ok := room.Items[id]
			if !ok || !item.Visible {
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
		itemIDs := room.ItemOrder
		if len(itemIDs) == 0 {
			for id := range room.Items {
				itemIDs = append(itemIDs, id)
			}
		}
		for _, id := range itemIDs {
			item, ok := room.Items[id]
			if !ok || !item.Visible {
				continue
			}
			fmt.Fprintf(&b, "- %s (id: %s)", item.Name, id)
			if item.State != "" {
				fmt.Fprintf(&b, " [état: %s]", item.State)
			}
			if item.Requires != "" {
				fmt.Fprintf(&b, " [nécessite: %s]", item.Requires)
			}
			if item.Inspected {
				b.WriteString(" [déjà inspecté]")
			}
			b.WriteString("\n")
		}
	}

	// List accessible rooms (doors that are visible and unlocked)
	if room != nil {
		var accessible []string
		doorItemIDs := room.ItemOrder
		if len(doorItemIDs) == 0 {
			for id := range room.Items {
				doorItemIDs = append(doorItemIDs, id)
			}
		}
		for _, itemID := range doorItemIDs {
			item, ok := room.Items[itemID]
			if !ok || !item.Visible || item.State == "locked" {
				continue
			}
			// Check if this item is a door/passage by matching significant words
			// between the door name and room names/IDs.
			itemKey := NormalizeKey(item.Name)
			for rID, r := range e.State.Rooms {
				if rID == e.State.Player.CurrentRoom {
					continue
				}
				rIDKey := NormalizeKey(rID)
				rNameKey := NormalizeKey(r.Name)
				// Direct containment (either direction)
				if strings.Contains(itemKey, rIDKey) || strings.Contains(itemKey, rNameKey) ||
					strings.Contains(rIDKey, itemKey) || strings.Contains(rNameKey, itemKey) {
					accessible = append(accessible, fmt.Sprintf("%s (id: %s)", r.Name, rID))
					break
				}
				// Word-level overlap: "Couloir A" vs "Couloir Secteur A" share "couloir" + "a"
				if wordsOverlap(itemKey, rIDKey) || wordsOverlap(itemKey, rNameKey) {
					accessible = append(accessible, fmt.Sprintf("%s (id: %s)", r.Name, rID))
					break
				}
			}
		}
		if len(accessible) > 0 {
			b.WriteString("Passages accessibles (utilise go_to avec l'ID) :\n")
			for _, a := range accessible {
				fmt.Fprintf(&b, "- %s\n", a)
			}
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

	b.WriteString("\nRAPPEL : Pour toute action du joueur (inspecter, prendre, utiliser, se déplacer, entrer un code), tu DOIS appeler l'outil correspondant. Ne raconte JAMAIS le résultat d'une action sans l'avoir exécutée via l'outil. Si un objet est en état 'waiting_pin' et que le joueur dit des chiffres, appelle input_pin_code avec ces chiffres.\n")
	b.WriteString("MATCHING : Le joueur peut désigner un objet par une description partielle. Fais correspondre ses mots au NOM de l'objet (pas à l'ID). Ex: 'le bureau' correspond à 'Le bureau au fond' (id: zone_bureau). 'la plaque' correspond à 'La plaque métallique nue' (id: mur_metallique). Utilise toujours l'ID exact dans l'appel d'outil.\n")

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

	case "go_to":
		targetObj, ok := call.Arguments["target_room"]
		if !ok {
			return `{"error": "Missing target_room parameter"}`, ""
		}
		targetRoom, _ := targetObj.(string)
		response, sfx, err = e.HandleGoTo(playerID, targetRoom)

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

// ClientInventoryItem is an inventory item as seen by the frontend.
type ClientInventoryItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
	Image string `json:"image,omitempty"`
}

// GameStateForClient returns a JSON-serializable snapshot of the game state
// suitable for sending to the frontend.
func (e *GameEngine) GameStateForClient() map[string]interface{} {
	e.mu.Lock()
	defer e.mu.Unlock()

	rooms := make([]ClientRoom, 0, len(e.State.Rooms))
	currentRoom := e.State.Player.CurrentRoom

	// Iterate in JSON declaration order (RoomOrder) for stable frontend display.
	roomIDs := e.State.RoomOrder
	if len(roomIDs) == 0 {
		// Fallback for states loaded before RoomOrder existed
		for id := range e.State.Rooms {
			roomIDs = append(roomIDs, id)
		}
	}

	for _, roomID := range roomIDs {
		room, ok := e.State.Rooms[roomID]
		if !ok {
			continue
		}
		visited := room.Visited
		if roomID == currentRoom {
			visited = true
		}

		items := make([]ClientItem, 0, len(room.Items))
		itemIDs := room.ItemOrder
		if len(itemIDs) == 0 {
			for id := range room.Items {
				itemIDs = append(itemIDs, id)
			}
		}
		for _, itemID := range itemIDs {
			item, ok := room.Items[itemID]
			if !ok {
				continue
			}
			ci := ClientItem{
				ID:        itemID,
				Name:      item.Name,
				Image:     item.Image,
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
			Image:       room.Image,
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
			Image: item.Image,
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

package game

import "strings"

// Fuzzy matching
//
// Voice transcription mangles IDs: the player says "la fiole", the LLM
// dictates "Requête prendre fiole uv", the parser squashes it to "fioleuv".
// All resolution is therefore done on normalized keys.

var accentReplacer = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i",
	"ô", "o", "ö", "o",
	"ù", "u", "û", "u", "ü", "u",
	"ç", "c", "œ", "oe",
)

// normalizeKey lowercases, strips accents and removes everything that is not
// a letter or a digit, so "Requête Fiole UV" becomes "requetefioleuv".
func normalizeKey(s string) string {
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
	"the": true, "and": true, "with": true, "near": true, "from": true, "into": true, "for": true,
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
	normID, normName := normalizeKey(id), normalizeKey(name)
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
	key := normalizeKey(raw)
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
	for _, id := range orderedItemIDs(room) {
		item, ok := room.Items[id]
		if !ok || !item.Visible {
			continue
		}
		s := itemMatchScore(key, id, item.Name)
		if item.Type == "zone" && zoneStripped {
			s = max(s, itemMatchScore(compareKey, id, item.Name))
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
	key := normalizeKey(raw)
	if key == "" {
		return ""
	}
	bestScore := 0
	bestID := ""
	for _, inv := range e.state.Player.Inventory {
		if inv.ID == raw {
			return inv.ID
		}
		if s := itemMatchScore(key, inv.ID, inv.Name); s > bestScore {
			bestScore, bestID = s, inv.ID
		}
	}
	return bestID
}

// resolveRoomID finds the room a raw reference designates: an exact ID or
// name first, then a partial match ("couloir" matches "Couloir Secteur A"),
// scanned in declaration order so the result is stable. "" when none.
func resolveRoomID(gs *GameState, raw string) string {
	key := normalizeKey(raw)
	if key == "" {
		return ""
	}
	ids := orderedRoomIDs(gs)
	for _, id := range ids {
		if key == normalizeKey(id) || key == normalizeKey(gs.Rooms[id].Name) {
			return id
		}
	}
	for _, id := range ids {
		idKey, nameKey := normalizeKey(id), normalizeKey(gs.Rooms[id].Name)
		if strings.Contains(idKey, key) || strings.Contains(nameKey, key) || strings.Contains(key, idKey) {
			return id
		}
	}
	return ""
}

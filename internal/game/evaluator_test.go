package game

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestScenarioPlaythrough plays the intended solution of the shipped scenario
// in every language. It fails as soon as an engine or scenario change makes
// the game unwinnable or lets the player skip a locked door.
func TestScenarioPlaythrough(t *testing.T) {
	for _, lang := range []string{"fr", "en"} {
		t.Run(lang, func(t *testing.T) { playthrough(t, lang) })
	}
}

func playthrough(t *testing.T, lang string) {
	gs, err := LoadScenario("../../config/"+lang+"/projet_longevite", lang)
	if err != nil {
		t.Fatal(err)
	}
	e := NewGameEngine(gs)
	if !strings.HasPrefix(e.StateSnapshot(), Texts[lang].StateHeader) {
		t.Fatalf("game state prompt is not in %s", lang)
	}

	call := func(name string, kv ...string) string {
		args := map[string]interface{}{}
		for i := 0; i+1 < len(kv); i += 2 {
			args[kv[i]] = kv[i+1]
		}
		r, _ := e.ProcessLLMFunctionCall(FunctionCall{Name: name, Arguments: args})
		return r
	}
	count := func(id string) int {
		n := 0
		for _, it := range gs.Player.Inventory {
			if it.ID == id {
				n++
			}
		}
		return n
	}
	inspect := func(id string) { call("inspect_item", "target_item", id) }
	take := func(id string) {
		t.Helper()
		if r := call("take_item", "target_item", id); count(id) != 1 {
			t.Fatalf("take %s: %s", id, r)
		}
	}
	use := func(inv, target string) {
		t.Helper()
		r := call("use_item", "inventory_item", inv, "target_item", target)
		if it := gs.Rooms[gs.Player.CurrentRoom].Items[target]; it == nil || !it.Used {
			t.Fatalf("use %s on %s: %s", inv, target, r)
		}
	}
	goTo := func(room string) {
		t.Helper()
		if r := call("go_to", "target_room", room); gs.Player.CurrentRoom != room {
			t.Fatalf("go_to %s: %s", room, r)
		}
	}
	blocked := func(room string) {
		t.Helper()
		from := gs.Player.CurrentRoom
		if r := call("go_to", "target_room", room); gs.Player.CurrentRoom != from {
			t.Fatalf("go_to %s from %s must be refused, got: %s", room, from, r)
		}
	}
	gained := func(id string) {
		t.Helper()
		if count(id) != 1 {
			t.Fatalf("expected exactly one %s in inventory, got %v", id, gs.Player.Inventory)
		}
	}

	// Cryo room: the exit door is hidden but locked, and no other room is adjacent.
	blocked("corridor_a")
	blocked("generator_room")
	inspect("zone_sol")
	inspect("blouse_scientifique")
	take("fiole_uv")
	take("carte_vierge")
	inspect("zone_mur_gauche")
	use("fiole_uv", "mur_metallique")
	inspect("zone_bureau")
	use("carte_vierge", "encodeur_magnetique")
	call("input_pin_code", "pin_code", "8-4-2-1")
	gained("carte_encodee_lvl1")
	use("carte_encodee_lvl1", "terminal_securite")
	inspect("zone_plafond")
	take("sangle_escalade")

	goTo("corridor_a")
	inspect("zone_cadavre")
	take("briquet")
	take("main_coupee")
	inspect("zone_portes")
	blocked("security_airlock")

	goTo("medical_lab")
	inspect("zone_paillasse")
	use("briquet", "bombe_aerosol")
	gained("chalumeau_thermique")
	inspect("zone_bureau_dir")
	use("main_coupee", "terminal_surclassement")
	call("use_item", "inventory_item", "main_coupee", "target_item", "terminal_surclassement")
	gained("carte_encodee_lvl2") // a second use must not hand out a second card

	goTo("morgue")
	inspect("zone_casiers")
	use("sangle_escalade", "tiroir_coince")
	take("tournevis_sonique")

	// The torch is needed twice: the frozen pipes here, the plants later.
	goTo("medical_lab")
	goTo("corridor_a")
	goTo("cryo_room")
	use("chalumeau_thermique", "tuyaux_refroidissement")
	take("cle_securite_rouge")

	goTo("corridor_a")
	use("carte_encodee_lvl2", "porte_sas")
	goTo("security_airlock")
	blocked("corridor_b")
	inspect("zone_ventilation_sas")
	use("tournevis_sonique", "grille_gaz")

	goTo("corridor_b")
	inspect("zone_accès_corridor_b")
	blocked("generator_room")
	goTo("hydroponics")
	inspect("zone_vegetation")
	use("chalumeau_thermique", "plantes_mutantes")
	gained("carte_encodee_lvl3")
	goTo("corridor_b")
	use("carte_encodee_lvl3", "porte_generateur")

	goTo("generator_room")
	inspect("zone_console_centrale")
	use("cle_securite_rouge", "console_finale")
	if !gs.Won {
		t.Fatal("game not won after the full solution")
	}
}

// TestLanguagesShareRules: translations may differ in text only, never in
// rules (rooms, items, states, requirements, exits, codes).
func TestLanguagesShareRules(t *testing.T) {
	rules := func(lang string) map[string]interface{} {
		gs, err := LoadScenario("../../config/"+lang+"/projet_longevite", lang)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]interface{}{"start": gs.Player.CurrentRoom}
		for id, room := range gs.Rooms {
			out[id] = room.Exits
			for itemID, item := range room.Items {
				r := *item
				r.Name, r.DescriptionOnInspect, r.SuccessMessage, r.FailMessage, r.PinSuccessMessage = "", "", "", "", ""
				out[id+"/"+itemID] = r
			}
		}
		return out
	}
	if fr, en := rules("fr"), rules("en"); !reflect.DeepEqual(fr, en) {
		for k := range fr {
			if !reflect.DeepEqual(fr[k], en[k]) {
				t.Errorf("fr and en differ on %s:\n fr %+v\n en %+v", k, fr[k], en[k])
			}
		}
		for k := range en {
			if _, ok := fr[k]; !ok {
				t.Errorf("en has %s, fr does not", k)
			}
		}
	}
}

// TestToolArguments: LLMs send numbers where the schema says string, and any
// tool name; results must still work and always be valid JSON.
func TestToolArguments(t *testing.T) {
	gs := NewGameState()
	gs.Player.CurrentRoom = "lab"
	gs.Rooms["lab"] = &RoomState{RoomID: "lab", Items: map[string]*RoomItem{
		"keypad": {Name: "Keypad", Visible: true, State: "waiting_pin", PinCode: "8421"},
	}}
	e := NewGameEngine(gs)

	e.ProcessLLMFunctionCall(FunctionCall{Name: "input_pin_code", Arguments: map[string]interface{}{"pin_code": float64(8421)}})
	if got := gs.Rooms["lab"].Items["keypad"].State; got != "unlocked" {
		t.Errorf("numeric PIN 8421 rejected: keypad state %q", got)
	}
	for _, call := range []FunctionCall{
		{Name: `evil"name`},
		{Name: "go_to", Arguments: map[string]interface{}{}},
	} {
		if r, _ := e.ProcessLLMFunctionCall(call); !json.Valid([]byte(r)) {
			t.Errorf("%s: invalid JSON result %s", call.Name, r)
		}
	}
}

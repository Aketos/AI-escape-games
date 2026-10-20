package game

// Text holds every sentence the engine writes in one language: tool results
// and history (which the LLM narrates) and the game-state block of its
// prompt. Comments name the fmt arguments, in order.
type Text struct {
	// Tool results
	NotHere         string
	NothingSpecial  string // item
	NotCollectible  string // item
	Took            string // item
	NotInInventory  string
	TargetNotHere   string
	AlreadyDone     string // item
	DidNotWork      string
	Worked          string
	NoRoute         string // what the player asked for
	AlreadyThere    string
	NoDirectPassage string // room
	PassageLocked   string // room
	Enter           string // room
	NoPinDevice     string
	WrongPin        string
	PinAccepted     string
	YouGet          string // produced items

	// Player history
	HistInspected string // item
	HistTook      string // item
	HistUsed      string // inventory item ID, target name, target ID
	HistWon       string
	HistMoved     string // room
	HistWrongPin  string // digits, device
	HistRightPin  string // device

	// Game state in the LLM system prompt
	StateHeader     string
	Oxygen          string // minutes
	WonNotice       string
	CurrentRoom     string // name
	VisibleHeader   string
	StateTag        string // state
	InspectedTag    string
	PassagesHeader  string
	InventoryHeader string
	EmptyInventory  string
	LastActions     string
	Reminder        string

	// Spoken when the upstream LLM fails
	Interference string
}

// Texts maps a GAME_LANGUAGE to its engine text. A scenario in a language
// missing here fails to load.
var Texts = map[string]*Text{
	"fr": {
		NotHere:         "Cet objet n'est pas ici ou n'est pas visible.",
		NothingSpecial:  "Vous inspectez %s. Il n'y a rien de particulier.",
		NotCollectible:  "%s ne peut pas être emporté.",
		Took:            "Vous avez pris %s.",
		NotInInventory:  "Vous n'avez pas cet objet dans votre inventaire.",
		TargetNotHere:   "La cible n'est pas ici ou n'est pas visible.",
		AlreadyDone:     "C'est déjà fait : %s a déjà été activé.",
		DidNotWork:      "Cela ne fonctionne pas.",
		Worked:          "Cela a fonctionné.",
		NoRoute:         "Vous ne trouvez aucun chemin vers %s.",
		AlreadyThere:    "Vous êtes déjà dans cette pièce.",
		NoDirectPassage: "Il n'y a pas de passage direct vers %s depuis ici.",
		PassageLocked:   "Le passage vers %s est verrouillé.",
		Enter:           "Vous entrez dans %s.",
		NoPinDevice:     "Aucun appareil n'attend de code PIN pour le moment.",
		WrongPin:        "Code PIN incorrect.",
		PinAccepted:     "Code accepté.",
		YouGet:          "Vous obtenez : %s.",

		HistInspected: "A inspecté : %s",
		HistTook:      "A ramassé : %s",
		HistUsed:      "A utilisé %s sur %s (%s)",
		HistWon:       "MISSION ACCOMPLIE.",
		HistMoved:     "S'est déplacé vers : %s",
		HistWrongPin:  "A entré un mauvais code PIN (%s) sur %s.",
		HistRightPin:  "A entré le bon code PIN sur %s.",

		StateHeader:     "# ÉTAT ACTUEL DU JEU (SOURCE DE VÉRITÉ ABSOLUE)",
		Oxygen:          "Oxygène restant : environ %d minutes.",
		WonNotice:       "MISSION ACCOMPLIE : le joueur a gagné. Conclus la partie.",
		CurrentRoom:     "Salle actuelle : %s",
		VisibleHeader:   "Objets VISIBLES (les SEULS objets dont tu as le droit de parler) :",
		StateTag:        " [état: %s]",
		InspectedTag:    " [déjà inspecté]",
		PassagesHeader:  "Passages accessibles (utilise go_to avec l'ID) :",
		InventoryHeader: "Inventaire du joueur :",
		EmptyInventory:  "- (vide)",
		LastActions:     "Dernières actions du joueur :",
		Reminder: "RAPPEL : Pour toute action du joueur (inspecter, prendre, utiliser, se déplacer, entrer un code), tu DOIS appeler l'outil correspondant. " +
			"Ne raconte JAMAIS le résultat d'une action sans l'avoir exécutée via l'outil. " +
			"Si un objet est en état 'waiting_pin' et que le joueur dit des chiffres, appelle input_pin_code avec ces chiffres.\n" +
			"MATCHING : Le joueur peut désigner un objet par une description partielle. Fais correspondre ses mots au NOM de l'objet, " +
			"puis utilise toujours l'ID exact de cet objet dans l'appel d'outil.",

		Interference: "Mes circuits de liaison neuronale subissent une interférence. Répétez, sujet.",
	},
	"en": {
		NotHere:         "That object is not here or not visible.",
		NothingSpecial:  "You inspect %s. There is nothing special about it.",
		NotCollectible:  "%s cannot be taken.",
		Took:            "You took %s.",
		NotInInventory:  "You do not have that object in your inventory.",
		TargetNotHere:   "The target is not here or not visible.",
		AlreadyDone:     "Already done: %s has already been activated.",
		DidNotWork:      "That does not work.",
		Worked:          "It worked.",
		NoRoute:         "You find no way to %s.",
		AlreadyThere:    "You are already in this room.",
		NoDirectPassage: "There is no direct way to %s from here.",
		PassageLocked:   "The way to %s is locked.",
		Enter:           "You enter %s.",
		NoPinDevice:     "No device is waiting for a PIN code right now.",
		WrongPin:        "Wrong PIN code.",
		PinAccepted:     "Code accepted.",
		YouGet:          "You receive: %s.",

		HistInspected: "Inspected: %s",
		HistTook:      "Picked up: %s",
		HistUsed:      "Used %s on %s (%s)",
		HistWon:       "MISSION ACCOMPLISHED.",
		HistMoved:     "Moved to: %s",
		HistWrongPin:  "Entered a wrong PIN code (%s) on %s.",
		HistRightPin:  "Entered the right PIN code on %s.",

		StateHeader:     "# CURRENT GAME STATE (ABSOLUTE SOURCE OF TRUTH)",
		Oxygen:          "Oxygen left: about %d minutes.",
		WonNotice:       "MISSION ACCOMPLISHED: the player has won. Wrap up the game.",
		CurrentRoom:     "Current room: %s",
		VisibleHeader:   "VISIBLE objects (the ONLY objects you are allowed to talk about):",
		StateTag:        " [state: %s]",
		InspectedTag:    " [already inspected]",
		PassagesHeader:  "Open passages (use go_to with the ID):",
		InventoryHeader: "Player inventory:",
		EmptyInventory:  "- (empty)",
		LastActions:     "Player's latest actions:",
		Reminder: "REMINDER: For every player action (inspect, take, use, move, enter a code), you MUST call the matching tool. " +
			"NEVER narrate the result of an action without having run it through the tool. " +
			"If an object is in state 'waiting_pin' and the player says digits, call input_pin_code with those digits.\n" +
			"MATCHING: The player may name an object by a partial description. Match their words to the object's NAME, " +
			"then always use that object's exact ID in the tool call.",

		Interference: "My neural link circuits are suffering interference. Repeat, subject.",
	},
}

package ai

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// GameConfig holds the language- and scenario-specific configuration.
type GameConfig struct {
	Persona        string
	IntroDirective string
	IntroPrompt    string
	ScenarioDir    string // path to config/<lang>/<scenario> directory
}

// ScenarioInfo describes a playable scenario for the frontend selection UI.
type ScenarioInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Difficulty  string `json:"difficulty"`
}

// ListScenarios returns the list of available scenarios for the given language.
// Each subdirectory of config/<lang>/ is a scenario. The scenario's display
// name and description are read from a scenario.json file if present,
// otherwise the directory name is used.
func ListScenarios(lang string) ([]ScenarioInfo, error) {
	configDir := os.Getenv("CONFIG_DIR")
	if configDir == "" {
		configDir = "config"
	}

	langDir := filepath.Join(configDir, lang)
	entries, err := os.ReadDir(langDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read scenario directory %s: %w", langDir, err)
	}

	var scenarios []ScenarioInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		info := ScenarioInfo{ID: id, Name: id}

		// Try to read scenario.json for display name and description
		metaPath := filepath.Join(langDir, id, "scenario.json")
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Difficulty  string `json:"difficulty"`
			}
			if json.Unmarshal(data, &meta) == nil {
				if meta.Name != "" {
					info.Name = meta.Name
				}
				info.Description = meta.Description
				info.Difficulty = meta.Difficulty
			}
		}

		scenarios = append(scenarios, info)
	}

	if len(scenarios) == 0 {
		return nil, fmt.Errorf("no scenarios found in %s", langDir)
	}

	return scenarios, nil
}

// LoadGameConfig loads the game configuration for a specific language and
// scenario from config/<lang>/<scenario>/. Each text field is read from a
// separate file: persona.txt, intro_directive.txt, intro_prompt.txt.
// The language is selected via the GAME_LANGUAGE env var (default "fr").
// The config directory defaults to "config" but can be set via CONFIG_DIR.
// Returns an error if any config file cannot be loaded or is empty.
func LoadGameConfig(scenario string) (*GameConfig, error) {
	lang := os.Getenv("GAME_LANGUAGE")
	if lang == "" {
		lang = "fr"
	}

	configDir := os.Getenv("CONFIG_DIR")
	if configDir == "" {
		configDir = "config"
	}

	scenarioDir := filepath.Join(configDir, lang, scenario)

	persona, err := readConfigFile(filepath.Join(scenarioDir, "persona.txt"))
	if err != nil {
		return nil, fmt.Errorf("game config persona: %w", err)
	}

	introDirective, err := readConfigFile(filepath.Join(scenarioDir, "intro_directive.txt"))
	if err != nil {
		return nil, fmt.Errorf("game config intro_directive: %w", err)
	}

	introPrompt, err := readConfigFile(filepath.Join(scenarioDir, "intro_prompt.txt"))
	if err != nil {
		return nil, fmt.Errorf("game config intro_prompt: %w", err)
	}

	log.Printf("Game config loaded from %s/ (language: %s, scenario: %s)", scenarioDir, lang, scenario)
	return &GameConfig{
		Persona:        persona,
		IntroDirective: introDirective,
		IntroPrompt:    introPrompt,
		ScenarioDir:    scenarioDir,
	}, nil
}

func readConfigFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return s, nil
}

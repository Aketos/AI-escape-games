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
	Lang           string // GAME_LANGUAGE the scenario was loaded for
}

// ScenarioInfo describes a playable scenario for the frontend selection UI.
type ScenarioInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Difficulty  string `json:"difficulty"`
}

// language returns GAME_LANGUAGE (default "fr").
func language() string {
	if lang := os.Getenv("GAME_LANGUAGE"); lang != "" {
		return lang
	}
	return "fr"
}

// languageDir returns config/<lang>, with CONFIG_DIR defaulting to "config".
func languageDir() string {
	configDir := os.Getenv("CONFIG_DIR")
	if configDir == "" {
		configDir = "config"
	}
	return filepath.Join(configDir, language())
}

// ListScenarios returns the list of available scenarios for the configured
// language. Each subdirectory of config/<lang>/ is a scenario. The scenario's
// display name and description are read from a scenario.json file if present,
// otherwise the directory name is used.
func ListScenarios() ([]ScenarioInfo, error) {
	langDir := languageDir()
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
	// The name comes from the player's URL: it must be a plain directory name.
	if scenario == "" || scenario == "." || scenario == ".." || filepath.Base(scenario) != scenario {
		return nil, fmt.Errorf("invalid scenario name %q", scenario)
	}
	scenarioDir := filepath.Join(languageDir(), scenario)

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

	log.Printf("Game config loaded from %s/", scenarioDir)
	return &GameConfig{
		Persona:        persona,
		IntroDirective: introDirective,
		IntroPrompt:    introPrompt,
		ScenarioDir:    scenarioDir,
		Lang:           language(),
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

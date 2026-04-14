package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func TestBuildProviderCommandCombinesDefaultArgsAndSettings(t *testing.T) {
	cityDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityDir, ".gc"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.gc): %v", err)
	}
	settingsPath := filepath.Join(cityDir, ".gc", "settings.json")
	if err := os.WriteFile(settingsPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile(settings.json): %v", err)
	}

	resolved := &config.ResolvedProvider{
		Name:    "claude",
		Command: "claude",
		OptionsSchema: []config.ProviderOption{
			{
				Key:     "permission_mode",
				Label:   "Permission Mode",
				Type:    "select",
				Default: "unrestricted",
				Choices: []config.OptionChoice{
					{Value: "ask", Label: "Ask"},
					{Value: "unrestricted", Label: "Unrestricted", FlagArgs: []string{"--dangerously-skip-permissions"}},
				},
			},
		},
		EffectiveDefaults: map[string]string{
			"permission_mode": "unrestricted",
		},
	}

	want := fmt.Sprintf("claude --dangerously-skip-permissions --settings %q", settingsPath)
	if got := buildProviderCommand(resolved, cityDir); got != want {
		t.Fatalf("buildProviderCommand() = %q, want %q", got, want)
	}
}

func TestBuildProviderCommandSkipsSettingsWithoutCityPath(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".gc"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.gc): %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".gc", "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile(settings.json): %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir(temp): %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Errorf("Chdir(%q): %v", cwd, err)
		}
	})

	resolved := &config.ResolvedProvider{
		Name:    "claude",
		Command: "claude",
	}
	if got, want := buildProviderCommand(resolved, ""), "claude"; got != want {
		t.Fatalf("buildProviderCommand(empty cityPath) = %q, want %q", got, want)
	}
}

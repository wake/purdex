package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalSetup(t *testing.T) {
	t.Run("cc install creates settings.json", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		err := localSetup("cc", false)
		if err != nil {
			t.Fatalf("localSetup cc install: %v", err)
		}

		settingsPath := filepath.Join(tmpHome, ".claude", "settings.json")
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			t.Fatalf("read settings.json: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("settings.json is empty")
		}

		var settings map[string]any
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatalf("parse settings.json: %v", err)
		}
		if _, ok := settings["hooks"]; !ok {
			t.Fatal("settings.json missing 'hooks' key")
		}
	})

	t.Run("codex install creates hooks.json", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		err := localSetup("codex", false)
		if err != nil {
			t.Fatalf("localSetup codex install: %v", err)
		}

		hooksPath := filepath.Join(tmpHome, ".codex", "hooks.json")
		data, err := os.ReadFile(hooksPath)
		if err != nil {
			t.Fatalf("read hooks.json: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("hooks.json is empty")
		}

		var hooksFile map[string]any
		if err := json.Unmarshal(data, &hooksFile); err != nil {
			t.Fatalf("parse hooks.json: %v", err)
		}
		if _, ok := hooksFile["hooks"]; !ok {
			t.Fatal("hooks.json missing 'hooks' key")
		}
	})

	t.Run("unknown agent returns error", func(t *testing.T) {
		err := localSetup("unknown", false)
		if err == nil {
			t.Fatal("expected error for unknown agent")
		}
	})

	t.Run("cc remove on empty dir succeeds", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		err := localSetup("cc", true)
		if err != nil {
			t.Fatalf("localSetup cc remove: %v", err)
		}
	})

	t.Run("codex remove on empty dir succeeds", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		err := localSetup("codex", true)
		if err != nil {
			t.Fatalf("localSetup codex remove: %v", err)
		}
	})
}

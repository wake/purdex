package cc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// ---- isTboxCommand ----

func TestIsTboxCommand_Positive(t *testing.T) {
	cases := []string{
		`"/usr/local/bin/pdx" hook --agent cc SessionStart`,
		`"pdx" hook --agent cc Stop`,
		`/usr/local/bin/pdx hook --agent cc UserPromptSubmit`,
		`pdx hook --agent cc SessionEnd`,
	}
	for _, cmd := range cases {
		if !isTboxCommand(cmd) {
			t.Errorf("expected isTboxCommand=true for: %s", cmd)
		}
	}
}

func TestIsTboxCommand_Negative(t *testing.T) {
	cases := []string{
		`"sometool" hook --agent cc SessionStart`,
		`/usr/bin/bash -c "echo hello"`,
		``,
		`pdx-ng hook something`,
	}
	for _, cmd := range cases {
		if isTboxCommand(cmd) {
			t.Errorf("expected isTboxCommand=false for: %s", cmd)
		}
	}
}

// ---- helper: read settings.json and return parsed hooks map ----

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	return m
}

func hooksMap(t *testing.T, settings map[string]any) map[string]any {
	t.Helper()
	h, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing or not a map")
	}
	return h
}

// ---- mergeClaudeHooks: empty file creates all 9 events ----

func TestMergeClaudeHooks_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	if err := mergeClaudeHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeClaudeHooks: %v", err)
	}

	settings := readSettings(t, path)
	hooks := hooksMap(t, settings)

	for _, event := range ccHookEvents {
		entries, ok := hooks[event]
		if !ok {
			t.Errorf("event %s not found in hooks", event)
			continue
		}
		arr, ok := entries.([]any)
		if !ok || len(arr) == 0 {
			t.Errorf("event %s has no entries", event)
		}
	}
	if len(hooks) != len(ccHookEvents) {
		t.Errorf("expected %d hook events, got %d", len(ccHookEvents), len(hooks))
	}
}

// ---- mergeClaudeHooks: idempotent (no duplicates on second run) ----

func TestMergeClaudeHooks_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	for i := 0; i < 2; i++ {
		if err := mergeClaudeHooks(path, "/usr/local/bin/pdx", false); err != nil {
			t.Fatalf("run %d: mergeClaudeHooks: %v", i, err)
		}
	}

	settings := readSettings(t, path)
	hooks := hooksMap(t, settings)

	for _, event := range ccHookEvents {
		entries, ok := hooks[event].([]any)
		if !ok {
			t.Fatalf("event %s: not an array", event)
		}
		tboxCount := 0
		for _, e := range entries {
			if entryIsTbox(e) {
				tboxCount++
			}
		}
		if tboxCount != 1 {
			t.Errorf("event %s: expected 1 pdx entry, got %d", event, tboxCount)
		}
	}
}

// ---- mergeClaudeHooks: preserves existing non-pdx hooks ----

func TestMergeClaudeHooks_PreservesExistingHooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	existing := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/usr/bin/notify-me session-start",
						},
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := mergeClaudeHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeClaudeHooks: %v", err)
	}

	settings := readSettings(t, path)
	hooks := hooksMap(t, settings)

	entries, ok := hooks["SessionStart"].([]any)
	if !ok {
		t.Fatal("SessionStart not an array")
	}

	hasNotifyMe := false
	hasTbox := false
	for _, e := range entries {
		if entryIsTbox(e) {
			hasTbox = true
		} else {
			m, _ := e.(map[string]any)
			inner, _ := m["hooks"].([]any)
			for _, h := range inner {
				hm, _ := h.(map[string]any)
				if cmd, _ := hm["command"].(string); cmd == "/usr/bin/notify-me session-start" {
					hasNotifyMe = true
				}
			}
		}
	}
	if !hasNotifyMe {
		t.Error("existing non-pdx hook was removed")
	}
	if !hasTbox {
		t.Error("pdx hook not added")
	}
}

// ---- mergeClaudeHooks: remove mode strips pdx entries, preserves others ----

func TestMergeClaudeHooks_RemoveMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Install first
	if err := mergeClaudeHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Add a non-pdx entry for SessionStart via direct file manipulation
	settings := readSettings(t, path)
	hooks := hooksMap(t, settings)
	sessionEntries := toEntrySlice(hooks["SessionStart"])
	sessionEntries = append(sessionEntries, map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "/usr/bin/notify-me session-start",
			},
		},
	})
	hooks["SessionStart"] = sessionEntries
	settings["hooks"] = hooks
	data, _ := json.MarshalIndent(settings, "", "  ")
	_ = os.WriteFile(path, data, 0644)

	// Now remove
	if err := mergeClaudeHooks(path, "/usr/local/bin/pdx", true); err != nil {
		t.Fatalf("remove: %v", err)
	}

	settings = readSettings(t, path)
	hooks = hooksMap(t, settings)

	for _, event := range ccHookEvents {
		entries, _ := hooks[event].([]any)
		for _, e := range entries {
			if entryIsTbox(e) {
				t.Errorf("event %s: pdx entry should have been removed", event)
			}
		}
	}

	// The non-pdx entry for SessionStart should remain
	sessionEntries2, _ := hooks["SessionStart"].([]any)
	found := false
	for _, e := range sessionEntries2 {
		m, _ := e.(map[string]any)
		inner, _ := m["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); cmd == "/usr/bin/notify-me session-start" {
				found = true
			}
		}
	}
	if !found {
		t.Error("non-pdx hook was incorrectly removed")
	}
}

// ---- mergeClaudeHooks: different path replaces old pdx entry ----

func TestMergeClaudeHooks_DifferentPathReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Install with old path
	if err := mergeClaudeHooks(path, "/old/path/pdx", false); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Re-install with new path
	if err := mergeClaudeHooks(path, "/new/path/pdx", false); err != nil {
		t.Fatalf("second install: %v", err)
	}

	settings := readSettings(t, path)
	hooks := hooksMap(t, settings)

	for _, event := range ccHookEvents {
		entries, _ := hooks[event].([]any)
		tboxCount := 0
		hasNewPath := false
		hasOldPath := false
		for _, e := range entries {
			if entryIsTbox(e) {
				tboxCount++
				m, _ := e.(map[string]any)
				inner, _ := m["hooks"].([]any)
				for _, h := range inner {
					hm, _ := h.(map[string]any)
					cmd, _ := hm["command"].(string)
					if cmd == "" {
						continue
					}
					if contains(cmd, "/new/path/pdx") {
						hasNewPath = true
					}
					if contains(cmd, "/old/path/pdx") {
						hasOldPath = true
					}
				}
			}
		}
		if tboxCount != 1 {
			t.Errorf("event %s: expected exactly 1 pdx entry after path change, got %d", event, tboxCount)
		}
		if !hasNewPath {
			t.Errorf("event %s: new pdx path not referenced in entry", event)
		}
		if hasOldPath {
			t.Errorf("event %s: old pdx path still present", event)
		}
	}
}

// ---- atomic write: no .tmp file left after success ----

func TestMergeClaudeHooks_AtomicWrite_NoTmpLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	if err := mergeClaudeHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeClaudeHooks: %v", err)
	}

	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error(".tmp file should not exist after successful write")
	}
}

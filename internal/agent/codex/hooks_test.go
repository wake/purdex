package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---- filterOutPdxCodex ----

func TestFilterOutPdxCodex_MatchesAndPreserves(t *testing.T) {
	entries := []any{
		map[string]any{
			"type":    "command",
			"command": `"/usr/local/bin/pdx" hook --agent codex SessionStart`,
			"timeout": 5,
		},
		map[string]any{
			"type":    "command",
			"command": "/usr/bin/notify-me start",
			"timeout": 5,
		},
	}

	result := filterOutPdxCodex(entries)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry after filter, got %d", len(result))
	}
	m, _ := result[0].(map[string]any)
	cmd, _ := m["command"].(string)
	if isPdxCommandCodex(cmd) {
		t.Error("pdx entry was not filtered out")
	}
	if cmd != "/usr/bin/notify-me start" {
		t.Errorf("unexpected remaining command: %s", cmd)
	}
}

func TestFilterOutPdxCodex_NonMapPreserved(t *testing.T) {
	entries := []any{"string-entry", 42}
	result := filterOutPdxCodex(entries)
	if len(result) != 2 {
		t.Errorf("expected 2 non-map entries preserved, got %d", len(result))
	}
}

// ---- helper: read hooks.json ----

func readHooksFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read hooks file: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal hooks file: %v", err)
	}
	return m
}

func hooksSection(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	h, ok := m["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing or not a map")
	}
	return h
}

// ---- mergeCodexHooks: empty file creates all 3 events ----

func TestMergeCodexHooks_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")

	if err := mergeCodexHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeCodexHooks: %v", err)
	}

	m := readHooksFile(t, path)
	hooks := hooksSection(t, m)

	for _, event := range codexHookEvents {
		entries, ok := hooks[event]
		if !ok {
			t.Errorf("event %s not found", event)
			continue
		}
		arr, ok := entries.([]any)
		if !ok || len(arr) == 0 {
			t.Errorf("event %s has no entries", event)
		}
	}
	if len(hooks) != len(codexHookEvents) {
		t.Errorf("expected %d hook events, got %d", len(codexHookEvents), len(hooks))
	}
}

// ---- mergeCodexHooks: idempotent ----

func TestMergeCodexHooks_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")

	for i := 0; i < 2; i++ {
		if err := mergeCodexHooks(path, "/usr/local/bin/pdx", false); err != nil {
			t.Fatalf("run %d: mergeCodexHooks: %v", i, err)
		}
	}

	m := readHooksFile(t, path)
	hooks := hooksSection(t, m)

	for _, event := range codexHookEvents {
		entries, ok := hooks[event].([]any)
		if !ok {
			t.Fatalf("event %s: not an array", event)
		}
		pdxCount := 0
		for _, e := range entries {
			em, ok := e.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := em["command"].(string)
			if isPdxCommandCodex(cmd) {
				pdxCount++
			}
		}
		if pdxCount != 1 {
			t.Errorf("event %s: expected 1 pdx entry, got %d", event, pdxCount)
		}
	}
}

// ---- mergeCodexHooks: preserves existing non-pdx hooks ----

func TestMergeCodexHooks_PreservesExistingHooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")

	existing := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"type":    "command",
					"command": "/usr/bin/notify-me start",
					"timeout": 5,
				},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := mergeCodexHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeCodexHooks: %v", err)
	}

	m := readHooksFile(t, path)
	hooks := hooksSection(t, m)

	entries, ok := hooks["SessionStart"].([]any)
	if !ok {
		t.Fatal("SessionStart not an array")
	}

	hasNotifyMe := false
	hasPdx := false
	for _, e := range entries {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := em["command"].(string)
		if isPdxCommandCodex(cmd) {
			hasPdx = true
		}
		if cmd == "/usr/bin/notify-me start" {
			hasNotifyMe = true
		}
	}
	if !hasNotifyMe {
		t.Error("existing non-pdx hook was removed")
	}
	if !hasPdx {
		t.Error("pdx hook was not added")
	}
}

// ---- mergeCodexHooks: remove mode ----

func TestMergeCodexHooks_RemoveMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")

	// Install first
	if err := mergeCodexHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Add a non-pdx entry for SessionStart
	m := readHooksFile(t, path)
	hooks := hooksSection(t, m)
	sessionEntries := toCodexEntrySlice(hooks["SessionStart"])
	sessionEntries = append(sessionEntries, map[string]any{
		"type":    "command",
		"command": "/usr/bin/notify-me start",
		"timeout": 5,
	})
	hooks["SessionStart"] = sessionEntries
	m["hooks"] = hooks
	data, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(path, data, 0644)

	// Remove
	if err := mergeCodexHooks(path, "/usr/local/bin/pdx", true); err != nil {
		t.Fatalf("remove: %v", err)
	}

	m = readHooksFile(t, path)
	hooks = hooksSection(t, m)

	for _, event := range codexHookEvents {
		entries, _ := hooks[event].([]any)
		for _, e := range entries {
			em, ok := e.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := em["command"].(string)
			if isPdxCommandCodex(cmd) {
				t.Errorf("event %s: pdx entry should have been removed", event)
			}
		}
	}

	// Non-pdx entry for SessionStart must remain
	sessionEntries2, _ := hooks["SessionStart"].([]any)
	found := false
	for _, e := range sessionEntries2 {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := em["command"].(string)
		if cmd == "/usr/bin/notify-me start" {
			found = true
		}
	}
	if !found {
		t.Error("non-pdx hook was incorrectly removed")
	}
}

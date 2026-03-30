package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeHooks_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	tboxPath := "/usr/local/bin/tbox"

	if err := mergeHooks(settingsPath, tboxPath, false); err != nil {
		t.Fatalf("mergeHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing or not object")
	}

	for _, event := range hookEvents {
		entries, ok := hooks[event]
		if !ok {
			t.Errorf("event %q missing", event)
			continue
		}
		arr, ok := entries.([]any)
		if !ok {
			t.Errorf("event %q: not an array", event)
			continue
		}
		if len(arr) != 1 {
			t.Errorf("event %q: got %d entries, want 1", event, len(arr))
			continue
		}

		// Verify structure: {"hooks": [{"type": "command", "command": "..."}]}
		entry, ok := arr[0].(map[string]any)
		if !ok {
			t.Errorf("event %q: entry is not an object", event)
			continue
		}
		innerHooks, ok := entry["hooks"].([]any)
		if !ok {
			t.Errorf("event %q: entry.hooks is not an array", event)
			continue
		}
		if len(innerHooks) != 1 {
			t.Errorf("event %q: got %d inner hooks, want 1", event, len(innerHooks))
			continue
		}
		hookObj, ok := innerHooks[0].(map[string]any)
		if !ok {
			t.Errorf("event %q: inner hook is not an object", event)
			continue
		}
		if hookObj["type"] != "command" {
			t.Errorf("event %q: type = %v, want command", event, hookObj["type"])
		}
		expectedCmd := `"` + tboxPath + `" hook --agent cc ` + event
		if hookObj["command"] != expectedCmd {
			t.Errorf("event %q: command = %v, want %q", event, hookObj["command"], expectedCmd)
		}
	}
}

func TestMergeHooks_Idempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	tboxPath := "/usr/local/bin/tbox"

	// Run twice
	if err := mergeHooks(settingsPath, tboxPath, false); err != nil {
		t.Fatalf("first mergeHooks: %v", err)
	}
	if err := mergeHooks(settingsPath, tboxPath, false); err != nil {
		t.Fatalf("second mergeHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	hooks := settings["hooks"].(map[string]any)

	for _, event := range hookEvents {
		arr := hooks[event].([]any)
		if len(arr) != 1 {
			t.Errorf("event %q: got %d entries after double run, want 1", event, len(arr))
		}
	}
}

func TestMergeHooks_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	tboxPath := "/usr/local/bin/tbox"

	// Write existing settings with a custom hook and another top-level key
	existing := map[string]any{
		"permissions": map[string]any{"allow": []any{"Read"}},
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/usr/local/bin/tsm-hook.sh Stop",
						},
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	if err := mergeHooks(settingsPath, tboxPath, false); err != nil {
		t.Fatalf("mergeHooks: %v", err)
	}

	result, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(result, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Top-level "permissions" key must be preserved
	if _, ok := settings["permissions"]; !ok {
		t.Error("permissions key was lost")
	}

	hooks := settings["hooks"].(map[string]any)

	// Stop should have 2 entries: existing tsm-hook + new tbox
	stopEntries := hooks["Stop"].([]any)
	if len(stopEntries) != 2 {
		t.Fatalf("Stop: got %d entries, want 2", len(stopEntries))
	}

	// First entry should be the existing tsm-hook
	first := stopEntries[0].(map[string]any)
	firstHooks := first["hooks"].([]any)
	firstCmd := firstHooks[0].(map[string]any)["command"].(string)
	if firstCmd != "/usr/local/bin/tsm-hook.sh Stop" {
		t.Errorf("first entry command = %q, want tsm-hook.sh", firstCmd)
	}

	// Second entry should be tbox (quoted path with --agent cc)
	second := stopEntries[1].(map[string]any)
	secondHooks := second["hooks"].([]any)
	secondCmd := secondHooks[0].(map[string]any)["command"].(string)
	expectedCmd := `"` + tboxPath + `" hook --agent cc Stop`
	if secondCmd != expectedCmd {
		t.Errorf("second entry command = %q, want %q", secondCmd, expectedCmd)
	}

	// All events must be present
	for _, event := range hookEvents {
		if _, ok := hooks[event]; !ok {
			t.Errorf("event %q missing after merge", event)
		}
	}
}

func TestMergeHooks_Remove(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	tboxPath := "/usr/local/bin/tbox"

	// First install hooks
	if err := mergeHooks(settingsPath, tboxPath, false); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Add an existing hook to Stop so we can verify it's preserved after remove
	data, _ := os.ReadFile(settingsPath)
	var settings map[string]any
	json.Unmarshal(data, &settings)
	hooks := settings["hooks"].(map[string]any)
	stopEntries := hooks["Stop"].([]any)
	stopEntries = append([]any{
		map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": "/usr/local/bin/tsm-hook.sh Stop",
				},
			},
		},
	}, stopEntries...)
	hooks["Stop"] = stopEntries
	data, _ = json.MarshalIndent(settings, "", "  ")
	os.WriteFile(settingsPath, data, 0644)

	// Now remove tbox hooks
	if err := mergeHooks(settingsPath, tboxPath, true); err != nil {
		t.Fatalf("remove: %v", err)
	}

	data, _ = os.ReadFile(settingsPath)
	json.Unmarshal(data, &settings)
	hooks = settings["hooks"].(map[string]any)

	// Stop should still have tsm-hook entry
	stopEntries = hooks["Stop"].([]any)
	if len(stopEntries) != 1 {
		t.Fatalf("Stop: got %d entries after remove, want 1 (tsm-hook only)", len(stopEntries))
	}
	entry := stopEntries[0].(map[string]any)
	innerHooks := entry["hooks"].([]any)
	cmd := innerHooks[0].(map[string]any)["command"].(string)
	if cmd != "/usr/local/bin/tsm-hook.sh Stop" {
		t.Errorf("remaining command = %q, want tsm-hook.sh", cmd)
	}

	// Other events should have empty arrays (not nil/missing)
	otherEvents := []string{
		"SessionStart", "UserPromptSubmit",
		"Notification", "PermissionRequest", "SessionEnd",
	}
	for _, event := range otherEvents {
		entries, ok := hooks[event]
		if !ok {
			t.Errorf("event %q removed entirely, want empty array", event)
			continue
		}
		arr, ok := entries.([]any)
		if !ok {
			t.Errorf("event %q: not an array", event)
			continue
		}
		if len(arr) != 0 {
			t.Errorf("event %q: got %d entries after remove, want 0", event, len(arr))
		}
	}
}

func TestEntryIsTbox(t *testing.T) {
	mkEntry := func(cmd string) map[string]any {
		return map[string]any{
			"hooks": []any{
				map[string]any{"type": "command", "command": cmd},
			},
		}
	}

	// Should match: various tbox command forms
	shouldMatch := []string{
		`"/usr/local/bin/tbox" hook --agent cc Stop`,           // quoted absolute
		`/usr/local/bin/tbox hook --agent cc Stop`,             // unquoted absolute
		`"/Users/wake/Workspace/wake/tmux-box/bin/tbox" hook --agent cc Notification`, // real-world path
		`"tbox" hook --agent cc SessionStart`,                  // bare quoted
		`tbox hook --agent cc SessionEnd`,                      // bare unquoted
		`"/Users/my user/bin/tbox" hook --agent cc Stop`,       // path with spaces
	}
	for _, cmd := range shouldMatch {
		if !entryIsTbox(mkEntry(cmd)) {
			t.Errorf("entryIsTbox should match %q", cmd)
		}
	}

	// Should NOT match: similar but different tools
	shouldReject := []string{
		`/usr/local/bin/tbox-extra hook Stop`,          // tbox-extra, not tbox
		`/usr/local/bin/mytbox hook Stop`,              // mytbox
		`/Users/wake/.config/tsm/hooks/tsm-hook.sh`,   // completely unrelated
		`/usr/local/bin/toolbox hook-handler Stop`,     // toolbox contains "tbox"
	}
	for _, cmd := range shouldReject {
		if entryIsTbox(mkEntry(cmd)) {
			t.Errorf("entryIsTbox should NOT match %q", cmd)
		}
	}
}

func TestMergeHooks_DifferentPaths(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	// First setup with one path
	if err := mergeHooks(settingsPath, "/project/tbox", false); err != nil {
		t.Fatalf("first mergeHooks: %v", err)
	}
	// Second setup with a different path (simulates ./tbox vs ./bin/tbox)
	if err := mergeHooks(settingsPath, "/project/bin/tbox", false); err != nil {
		t.Fatalf("second mergeHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	hooks := settings["hooks"].(map[string]any)

	for _, event := range hookEvents {
		arr := hooks[event].([]any)
		if len(arr) != 1 {
			t.Errorf("event %q: got %d entries, want 1 (old path should be replaced)", event, len(arr))
		}
		// Should be the second path
		entry := arr[0].(map[string]any)
		innerHooks := entry["hooks"].([]any)
		cmd := innerHooks[0].(map[string]any)["command"].(string)
		expectedCmd := `"/project/bin/tbox" hook --agent cc ` + event
		if cmd != expectedCmd {
			t.Errorf("event %q: command = %q, want %q", event, cmd, expectedCmd)
		}
	}
}

func TestMergeHooks_SpacePath(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	tboxPath := "/Users/my user/bin/tbox"

	if err := mergeHooks(settingsPath, tboxPath, false); err != nil {
		t.Fatalf("mergeHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	hooks := settings["hooks"].(map[string]any)
	arr := hooks["Stop"].([]any)
	entry := arr[0].(map[string]any)
	innerHooks := entry["hooks"].([]any)
	cmd := innerHooks[0].(map[string]any)["command"].(string)
	expectedCmd := `"/Users/my user/bin/tbox" hook --agent cc Stop`
	if cmd != expectedCmd {
		t.Errorf("command = %q, want %q", cmd, expectedCmd)
	}
}

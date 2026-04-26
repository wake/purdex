package cc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
)

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// ---- isPdxCommand ----

func TestIsPdxCommand_Positive(t *testing.T) {
	cases := []string{
		`"/usr/local/bin/pdx" hook --agent cc SessionStart`,
		`"pdx" hook --agent cc Stop`,
		`/usr/local/bin/pdx hook --agent cc UserPromptSubmit`,
		`pdx hook --agent cc SessionEnd`,
	}
	for _, cmd := range cases {
		if !isPdxCommand(cmd) {
			t.Errorf("expected isPdxCommand=true for: %s", cmd)
		}
	}
}

func TestIsPdxCommand_Negative(t *testing.T) {
	cases := []string{
		`"sometool" hook --agent cc SessionStart`,
		`/usr/bin/bash -c "echo hello"`,
		``,
		`pdx-ng hook something`,
		`pdx exec hook --agent cc SessionStart`,
	}
	for _, cmd := range cases {
		if isPdxCommand(cmd) {
			t.Errorf("expected isPdxCommand=false for: %s", cmd)
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

	for _, event := range expectedCCEventNames {
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
	if len(hooks) != len(expectedCCEventNames) {
		t.Errorf("expected %d hook events, got %d", len(expectedCCEventNames), len(hooks))
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

	for _, event := range expectedCCEventNames {
		entries, ok := hooks[event].([]any)
		if !ok {
			t.Fatalf("event %s: not an array", event)
		}
		pdxCount := 0
		for _, e := range entries {
			if entryIsPdx(e) {
				pdxCount++
			}
		}
		if pdxCount != 1 {
			t.Errorf("event %s: expected 1 pdx entry, got %d", event, pdxCount)
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
	hasPdx := false
	for _, e := range entries {
		if entryIsPdx(e) {
			hasPdx = true
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
	if !hasPdx {
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

	for _, event := range expectedCCEventNames {
		entries, _ := hooks[event].([]any)
		for _, e := range entries {
			if entryIsPdx(e) {
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

	for _, event := range expectedCCEventNames {
		entries, _ := hooks[event].([]any)
		pdxCount := 0
		hasNewPath := false
		hasOldPath := false
		for _, e := range entries {
			if entryIsPdx(e) {
				pdxCount++
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
		if pdxCount != 1 {
			t.Errorf("event %s: expected exactly 1 pdx entry after path change, got %d", event, pdxCount)
		}
		if !hasNewPath {
			t.Errorf("event %s: new pdx path not referenced in entry", event)
		}
		if hasOldPath {
			t.Errorf("event %s: old pdx path still present", event)
		}
	}
}

// ---- InstallHooks / CheckHooks use Events() as SSoT ----

// TestCCInstallHooks_WritesAllEventsFromEventsList drives mergeClaudeHooks and
// asserts the written settings.json has one key per Event from Events() —
// i.e. the installer no longer relies on the legacy ccHookEvents var.
func TestCCInstallHooks_WritesAllEventsFromEventsList(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(dir, "settings.json")

	if err := mergeClaudeHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeClaudeHooks: %v", err)
	}

	settings := readSettings(t, path)
	hooks := hooksMap(t, settings)

	p := NewProvider(nil, nil, nil, nil)
	events := installableCCEvents(p.Events())
	if len(events) == 0 {
		t.Fatal("cc Events() returned empty; installer iteration would be vacuous")
	}
	for _, e := range events {
		entries, ok := hooks[e.Name]
		if !ok {
			t.Errorf("event %s (from installable Events()) not found in written hooks", e.Name)
			continue
		}
		arr, ok := entries.([]any)
		if !ok || len(arr) == 0 {
			t.Errorf("event %s: no entries written", e.Name)
		}
	}
	if len(hooks) != len(events) {
		t.Errorf("settings.json hooks len=%d, want %d (one per installable Events())", len(hooks), len(events))
	}
}

func installableCCEvents(events []agent.HookEventSpec) []agent.HookEventSpec {
	out := make([]agent.HookEventSpec, 0, len(events))
	for _, event := range events {
		if agent.IsInstallableHookSpec(event) {
			out = append(out, event)
		}
	}
	return out
}

func TestCCInstallHooks_ExcludesNonInstallableSpecs(t *testing.T) {
	original := ccEventSpecs
	ccEventSpecs = append(append([]agent.HookEventSpec(nil), ccEventSpecs...),
		agent.HookEventSpec{Name: "IgnoredSynthetic", Handling: agent.HookHandlingIgnored, Description: "Ignored synthetic hook"},
		agent.HookEventSpec{Name: "UnsupportedSynthetic", Handling: agent.HookHandlingUnsupported, Description: "Unsupported synthetic hook"},
	)
	t.Cleanup(func() { ccEventSpecs = original })

	for _, name := range ccEventNames() {
		if name == "IgnoredSynthetic" || name == "UnsupportedSynthetic" {
			t.Fatalf("ccEventNames included non-installable spec %q", name)
		}
	}

	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := mergeClaudeHooks(settingsPath, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeClaudeHooks: %v", err)
	}
	hooks := hooksMap(t, readSettings(t, settingsPath))
	for _, name := range []string{"IgnoredSynthetic", "UnsupportedSynthetic"} {
		if _, ok := hooks[name]; ok {
			t.Errorf("mergeClaudeHooks wrote non-installable event %q", name)
		}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	installedPath := filepath.Join(claudeDir, "settings.json")
	if err := mergeClaudeHooks(installedPath, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	status, err := NewProvider(nil, nil, nil, nil).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	for _, name := range []string{"IgnoredSynthetic", "UnsupportedSynthetic"} {
		if _, ok := status.Events[name]; ok {
			t.Errorf("CheckHooks.Events included non-installable event %q", name)
		}
	}
	if !status.Installed || !status.Managed || len(status.Issues) != 0 {
		t.Fatalf("clean install with non-installable specs status=%+v, want installed managed with no issues", status)
	}

	settings := readSettings(t, installedPath)
	installedHooks := hooksMap(t, settings)
	installedHooks["IgnoredSynthetic"] = []any{makePdxEntry("/usr/local/bin/pdx", "cc", "IgnoredSynthetic")}
	settings["hooks"] = installedHooks
	data, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(installedPath, data, 0644); err != nil {
		t.Fatalf("write stale installed settings: %v", err)
	}
	status, err = NewProvider(nil, nil, nil, nil).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks stale non-installable: %v", err)
	}
	if !status.Installed || !status.Managed || len(status.Issues) != 0 {
		t.Fatalf("stale non-installable hook status=%+v, want installed managed with no issues", status)
	}

	staleSettingsPath := filepath.Join(dir, "stale-settings.json")
	stale := map[string]any{
		"hooks": map[string]any{
			"Bogus": []any{makePdxEntry("/usr/local/bin/pdx", "cc", "SessionStart")},
			"IgnoredSynthetic": []any{
				makePdxEntry("/usr/local/bin/pdx", "cc", "IgnoredSynthetic"),
				makePdxEntry("/usr/local/bin/pdx", "codex", "IgnoredSynthetic"),
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/notify ignored"}}},
			},
		},
	}
	staleData, _ := json.MarshalIndent(stale, "", "  ")
	if err := os.WriteFile(staleSettingsPath, staleData, 0644); err != nil {
		t.Fatalf("write stale settings: %v", err)
	}
	if err := mergeClaudeHooks(staleSettingsPath, "/usr/local/bin/pdx", true); err != nil {
		t.Fatalf("remove stale non-installable hook: %v", err)
	}
	staleHooks := hooksMap(t, readSettings(t, staleSettingsPath))
	if _, ok := staleHooks["Bogus"]; ok {
		t.Fatal("remove left owned cc hook filed under unknown key Bogus")
	}
	staleEntries := toEntrySlice(staleHooks["IgnoredSynthetic"])
	if len(staleEntries) != 3 {
		t.Fatalf("remove kept %d non-owned/third-party entries, want 3", len(staleEntries))
	}
	foundSyntheticCCPdx := false
	foundCodexPdx := false
	for _, entry := range staleEntries {
		if entryIsPdx(entry) {
			t.Fatalf("remove left owned pdx entry: %#v", entry)
		}
		if commandEntryContains(entry, "--agent cc IgnoredSynthetic") {
			foundSyntheticCCPdx = true
		}
		if commandEntryContains(entry, "--agent codex") {
			foundCodexPdx = true
		}
	}
	if !foundSyntheticCCPdx {
		t.Fatal("remove dropped non-owned synthetic cc hook under non-installable key")
	}
	if !foundCodexPdx {
		t.Fatal("remove dropped non-cc pdx hook under non-installable key")
	}
	absentSettingsPath := filepath.Join(dir, "absent-settings.json")
	if err := os.WriteFile(absentSettingsPath, []byte(`{"hooks":{}}`), 0644); err != nil {
		t.Fatalf("write absent settings: %v", err)
	}
	if err := mergeClaudeHooks(absentSettingsPath, "/usr/local/bin/pdx", true); err != nil {
		t.Fatalf("remove absent non-installable hook: %v", err)
	}
	absentHooks := hooksMap(t, readSettings(t, absentSettingsPath))
	if _, ok := absentHooks["IgnoredSynthetic"]; ok {
		t.Fatal("remove created absent non-installable key IgnoredSynthetic")
	}
}

func commandEntryContains(entry any, needle string) bool {
	m, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	arr, ok := m["hooks"].([]any)
	if !ok {
		return false
	}
	for _, hook := range arr {
		hm, ok := hook.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := hm["command"].(string)
		if strings.Contains(cmd, needle) {
			return true
		}
	}
	return false
}

// TestCCCheckHooks_ReportsAllEventsFromEventsList writes a settings.json that
// deliberately omits Notification and asserts CheckHooks reports the full
// Events() key set, with Notification.Installed=false.
func TestCCCheckHooks_ReportsAllEventsFromEventsList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")

	// Write an install-complete settings.json then strip Notification out.
	if err := mergeClaudeHooks(settingsPath, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	settings := readSettings(t, settingsPath)
	hooks := hooksMap(t, settings)
	delete(hooks, "Notification")
	settings["hooks"] = hooks
	data, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatalf("write stripped: %v", err)
	}

	p := NewProvider(nil, nil, nil, nil)
	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	for _, e := range installableCCEvents(p.Events()) {
		if _, ok := status.Events[e.Name]; !ok {
			t.Errorf("CheckHooks.Events missing key %q (from Events())", e.Name)
		}
	}
	if status.Events["Notification"].Installed {
		t.Error("Notification must be reported Installed=false after deletion")
	}
	if status.Installed {
		t.Error("Installed must be false when any event is missing")
	}
}

func TestCCCheckHooks_WrongAgentOrEventCommandNotInstalled(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{name: "wrong agent", command: `"/usr/local/bin/pdx" hook --agent codex SessionStart`},
		{name: "wrong event", command: `"/usr/local/bin/pdx" hook --agent cc Stop`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			settingsPath := filepath.Join(home, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			settings := map[string]any{
				"hooks": map[string]any{
					"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": tt.command}}}},
				},
			}
			data, _ := json.MarshalIndent(settings, "", "  ")
			if err := os.WriteFile(settingsPath, data, 0644); err != nil {
				t.Fatalf("write settings: %v", err)
			}
			status, err := NewProvider(nil, nil, nil, nil).CheckHooks()
			if err != nil {
				t.Fatalf("CheckHooks: %v", err)
			}
			if status.Events["SessionStart"].Installed {
				t.Fatalf("SessionStart Installed=true for %s command", tt.name)
			}
			if status.Installed {
				t.Fatal("overall Installed=true with invalid SessionStart command")
			}
		})
	}
}

// TestCCCheckHooks_ManagedReflectsPdxEntries asserts HookStatus.Managed
// is true when any pdx command is present (even broken) and false
// otherwise. Finding #2: UI Remove button must stay enabled for
// drifted-but-managed state.
func TestCCCheckHooks_ManagedReflectsPdxEntries(t *testing.T) {
	t.Run("pdx entry present", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		settingsPath := filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := mergeClaudeHooks(settingsPath, "/usr/local/bin/pdx", false); err != nil {
			t.Fatalf("install: %v", err)
		}
		p := NewProvider(nil, nil, nil, nil)
		status, err := p.CheckHooks()
		if err != nil {
			t.Fatalf("CheckHooks: %v", err)
		}
		if !status.Managed {
			t.Fatal("pdx entries present: Managed=false, want true")
		}
	})
	t.Run("no pdx entries", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		settingsPath := filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		bare := map[string]any{
			"hooks": map[string]any{
				"SessionStart": []any{
					map[string]any{
						"hooks": []any{
							map[string]any{
								"type":    "command",
								"command": "/usr/bin/notify-me start",
							},
						},
					},
				},
			},
		}
		data, _ := json.MarshalIndent(bare, "", "  ")
		if err := os.WriteFile(settingsPath, data, 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
		p := NewProvider(nil, nil, nil, nil)
		status, err := p.CheckHooks()
		if err != nil {
			t.Fatalf("CheckHooks: %v", err)
		}
		if status.Managed {
			t.Fatal("no pdx entries: Managed=true, want false")
		}
	})
	t.Run("owned pdx under unknown key", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		settingsPath := filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		settings := map[string]any{
			"hooks": map[string]any{
				"Bogus": []any{makePdxEntry("/usr/local/bin/pdx", "cc", "SessionStart")},
			},
		}
		data, _ := json.MarshalIndent(settings, "", "  ")
		if err := os.WriteFile(settingsPath, data, 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
		status, err := NewProvider(nil, nil, nil, nil).CheckHooks()
		if err != nil {
			t.Fatalf("CheckHooks: %v", err)
		}
		if !status.Managed {
			t.Fatal("owned pdx under unknown key: Managed=false, want true")
		}
	})
}

// TestCCCheckHooks_UpgradesAvailableEmptyForFullFutureOnlyFalse asserts
// cc has no FutureOnly events in its current catalog → UpgradesAvailable
// must be empty on a fresh install and HookEventInfo.FutureOnly must be
// false for every event.
func TestCCCheckHooks_UpgradesAvailableEmptyForFullFutureOnlyFalse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := mergeClaudeHooks(settingsPath, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	p := NewProvider(nil, nil, nil, nil)
	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if len(status.UpgradesAvailable) != 0 {
		t.Errorf("cc UpgradesAvailable=%v, want empty", status.UpgradesAvailable)
	}
	for name, info := range status.Events {
		if info.FutureOnly {
			t.Errorf("cc event %q FutureOnly=true, want false", name)
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

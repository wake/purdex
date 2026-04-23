package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- filterOutPdxCodex ----

func TestFilterOutPdxCodex_MatchesAndPreserves(t *testing.T) {
	entries := []any{
		map[string]any{
			"hooks": []any{
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
			},
		},
	}

	result := filterOutPdxCodex(entries)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry after filter, got %d", len(result))
	}
	group, _ := result[0].(map[string]any)
	hookEntries := toCodexEntrySlice(group["hooks"])
	if len(hookEntries) != 1 {
		t.Fatalf("expected 1 remaining hook entry, got %d", len(hookEntries))
	}
	m, _ := hookEntries[0].(map[string]any)
	cmd, _ := m["command"].(string)
	if isPdxCommandCodex(cmd) {
		t.Error("pdx entry was not filtered out")
	}
	if cmd != "/usr/bin/notify-me start" {
		t.Errorf("unexpected remaining command: %s", cmd)
	}
}

func TestFilterOutPdxCodex_LegacyDirectEntryMigrated(t *testing.T) {
	entries := []any{
		map[string]any{
			"type":    "command",
			"command": "/usr/bin/notify-me start",
			"timeout": 5,
		},
	}
	result := filterOutPdxCodex(entries)
	if len(result) != 1 {
		t.Fatalf("expected 1 matcher group after migration, got %d", len(result))
	}
	group, _ := result[0].(map[string]any)
	hookEntries := toCodexEntrySlice(group["hooks"])
	if len(hookEntries) != 1 {
		t.Fatalf("expected 1 migrated hook entry, got %d", len(hookEntries))
	}
}

func TestFilterOutPdxCodex_NonMapPreserved(t *testing.T) {
	entries := []any{"string-entry", 42}
	result := filterOutPdxCodex(entries)
	if len(result) != 2 {
		t.Fatalf("expected 2 preserved entries, got %d", len(result))
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

	for _, event := range expectedCodexInstallerNames {
		entries, ok := hooks[event]
		if !ok {
			t.Errorf("event %s not found", event)
			continue
		}
		arr := codexMatcherGroups(entries)
		if len(arr) == 0 {
			t.Errorf("event %s has no matcher groups", event)
			continue
		}
		group, _ := arr[0].(map[string]any)
		hookEntries := toCodexEntrySlice(group["hooks"])
		if len(hookEntries) == 0 {
			t.Errorf("event %s has no hook handlers", event)
		}
	}
	if len(hooks) != len(expectedCodexInstallerNames) {
		t.Errorf("expected %d hook events, got %d", len(expectedCodexInstallerNames), len(hooks))
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

	for _, event := range expectedCodexInstallerNames {
		pdxCount := 0
		for _, groupEntry := range codexMatcherGroups(hooks[event]) {
			group, _ := groupEntry.(map[string]any)
			for _, hookEntry := range toCodexEntrySlice(group["hooks"]) {
				em, ok := hookEntry.(map[string]any)
				if !ok {
					continue
				}
				cmd, _ := em["command"].(string)
				if isPdxCommandCodex(cmd) {
					pdxCount++
				}
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
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/usr/bin/notify-me start",
							"timeout": 5,
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

	if err := mergeCodexHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeCodexHooks: %v", err)
	}

	m := readHooksFile(t, path)
	hooks := hooksSection(t, m)

	hasNotifyMe := false
	hasPdx := false
	for _, groupEntry := range codexMatcherGroups(hooks["SessionStart"]) {
		group, _ := groupEntry.(map[string]any)
		for _, hookEntry := range toCodexEntrySlice(group["hooks"]) {
			em, ok := hookEntry.(map[string]any)
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
	sessionEntries := codexMatcherGroups(hooks["SessionStart"])
	sessionEntries = append(sessionEntries, map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "/usr/bin/notify-me start",
				"timeout": 5,
			},
		},
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

	for _, event := range expectedCodexInstallerNames {
		for _, groupEntry := range codexMatcherGroups(hooks[event]) {
			group, _ := groupEntry.(map[string]any)
			for _, hookEntry := range toCodexEntrySlice(group["hooks"]) {
				em, ok := hookEntry.(map[string]any)
				if !ok {
					continue
				}
				cmd, _ := em["command"].(string)
				if isPdxCommandCodex(cmd) {
					t.Errorf("event %s: pdx entry should have been removed", event)
				}
			}
		}
	}

	// Non-pdx entry for SessionStart must remain
	found := false
	for _, groupEntry := range codexMatcherGroups(hooks["SessionStart"]) {
		group, _ := groupEntry.(map[string]any)
		for _, hookEntry := range toCodexEntrySlice(group["hooks"]) {
			em, ok := hookEntry.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := em["command"].(string)
			if cmd == "/usr/bin/notify-me start" {
				found = true
			}
		}
	}
	if !found {
		t.Error("non-pdx hook was incorrectly removed")
	}
}

// expectedCodexInstallerNames is the post-expansion 9-event installer set
// (plan §1.4 / issue #613). Shared across TestMergeCodexHooks_* so the
// installer tests migrate off codexHookEvents cleanly.
var expectedCodexInstallerNames = []string{
	"SessionStart",
	"UserPromptSubmit",
	"SubagentStart",
	"SubagentStop",
	"Stop",
	"StopFailure",
	"Notification",
	"PermissionRequest",
	"SessionEnd",
}

// TestCodexInstallHooks_Writes9EventsAfterExpansion is the primary issue
// #613 regression guard: installer must write the full 9-event set,
// especially the 6 newly added (SubagentStart/Stop, StopFailure,
// Notification, PermissionRequest, SessionEnd). Any future change that
// shrinks the installer back to the pre-expansion 3-event set trips here.
func TestCodexInstallHooks_Writes9EventsAfterExpansion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")

	if err := mergeCodexHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeCodexHooks: %v", err)
	}

	m := readHooksFile(t, path)
	hooks := hooksSection(t, m)

	if len(hooks) != 9 {
		t.Errorf("codex installer wrote %d events, want 9 (issue #613 expansion)", len(hooks))
	}

	wantExpanded := []string{
		"SubagentStart",
		"SubagentStop",
		"StopFailure",
		"Notification",
		"PermissionRequest",
		"SessionEnd",
	}
	for _, name := range wantExpanded {
		if _, ok := hooks[name]; !ok {
			t.Errorf("expanded event %q not written to hooks.json (issue #613 regression)", name)
		}
	}
	for _, name := range expectedCodexInstallerNames {
		entries, ok := hooks[name]
		if !ok {
			t.Errorf("event %q not written to hooks.json", name)
			continue
		}
		groups := codexMatcherGroups(entries)
		if len(groups) == 0 {
			t.Errorf("event %q has no matcher groups", name)
		}
	}
}

// TestCodexCheckHooks_ReportsAll9Events asserts CheckHooks sees every event
// in the expanded set.
func TestCodexCheckHooks_ReportsAll9Events(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := mergeCodexHooks(hooksPath, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if len(status.Events) != 9 {
		t.Errorf("CheckHooks reported %d events, want 9", len(status.Events))
	}
	for _, name := range expectedCodexInstallerNames {
		info, ok := status.Events[name]
		if !ok {
			t.Errorf("CheckHooks.Events missing key %q", name)
			continue
		}
		if !info.Installed {
			t.Errorf("event %q: Installed=false after fresh install", name)
		}
	}
	if !status.Installed {
		t.Errorf("Installed=false after full install, issues=%v", status.Issues)
	}
}

func TestCheckHooks_LegacyDirectEntryReportedUninstalled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	hooksPath := filepath.Join(dir, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	legacy := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"type":    "command",
					"command": `"/usr/local/bin/pdx" hook --agent codex SessionStart`,
					"timeout": 5,
				},
			},
			"UserPromptSubmit": []any{
				map[string]any{
					"type":    "command",
					"command": `"/usr/local/bin/pdx" hook --agent codex UserPromptSubmit`,
					"timeout": 5,
				},
			},
			"Stop": []any{
				map[string]any{
					"type":    "command",
					"command": `"/usr/local/bin/pdx" hook --agent codex Stop`,
					"timeout": 5,
				},
			},
		},
	}
	data, _ := json.MarshalIndent(legacy, "", "  ")
	if err := os.WriteFile(hooksPath, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatal("expected legacy codex hooks to be reported as not installed")
	}
	if status.Events["SessionStart"].Installed {
		t.Fatal("expected legacy SessionStart hook to be reported as not installed")
	}
	if len(status.Issues) == 0 || !strings.Contains(status.Issues[0], "legacy format") {
		t.Fatalf("expected legacy format issue, got %v", status.Issues)
	}
}

// TestCheckHooks_ThirdPartyLegacyEntryDoesNotTriggerReinstall covers the case
// where the user has a legacy-format third-party hook alongside a correctly
// installed pdx matcher group. The third-party legacy entry must not cause
// pdx to report "reinstall required".
func TestCheckHooks_ThirdPartyLegacyEntryDoesNotTriggerReinstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	hooksPath := filepath.Join(dir, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	pdxGroup := func(event string) map[string]any {
		return map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": `"/usr/local/bin/pdx" hook --agent codex ` + event,
					"timeout": 5,
				},
			},
		}
	}
	thirdPartyLegacy := map[string]any{
		"type":    "command",
		"command": "/usr/bin/notify-me start",
		"timeout": 5,
	}
	// Post-expansion: installer writes 9 events (issue #613), so the fixture
	// must cover all 9 to keep the test focused on the third-party coexistence
	// assertion rather than tripping on the expansion itself.
	hooksSect := map[string]any{
		"SessionStart": []any{pdxGroup("SessionStart"), thirdPartyLegacy},
	}
	for _, name := range expectedCodexInstallerNames {
		if name == "SessionStart" {
			continue
		}
		hooksSect[name] = []any{pdxGroup(name)}
	}
	mixed := map[string]any{"hooks": hooksSect}
	data, _ := json.MarshalIndent(mixed, "", "  ")
	if err := os.WriteFile(hooksPath, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if !status.Installed {
		t.Fatalf("expected all pdx hooks to be reported installed, got issues=%v", status.Issues)
	}
	if !status.Events["SessionStart"].Installed {
		t.Fatal("SessionStart should be installed despite coexisting third-party legacy entry")
	}
}

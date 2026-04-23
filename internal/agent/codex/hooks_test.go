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

// ---- fix-plan §1.4 / §2.2 CheckHooks three-state tests (CH1-CH6) ----
//
// The fix-plan §1.4 decision table partitions every declared event into
// three states — absent / present-but-broken / valid — and applies a
// FutureOnly-aware policy:
//
//	| FutureOnly | state          | Installed | Issues append           | blocks allInstalled |
//	|------------|----------------|-----------|-------------------------|---------------------|
//	| false      | absent         | false     | "<name> hook not installed" | yes             |
//	| false      | broken         | false     | "<name> hook: pdx command not found" | yes    |
//	| false      | valid          | true      | —                       | no                  |
//	| true       | absent         | false     | (tolerated, silent)     | no                  |
//	| true       | broken         | false     | "<name> hook: pdx command malformed …" | yes  |
//	| true       | valid          | true      | —                       | no                  |
//
// State A (absent) is defined strictly as "key not present in hooks map"
// (plan §1.4). An empty array value ([]) is State B (broken) because the
// mergeCodexHooks remove path leaves hooks[event]=[] behind — treating it
// as absent would silently hide a real uninstall event for a FutureOnly
// hook.

// pdxGroupEntry builds a correctly-installed matcher-group entry for an
// event name, matching the shape mergeCodexHooks writes.
func pdxGroupEntry(event string) map[string]any {
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

// writeHooksFile serialises a hooks-section map to hooks.json under the
// given HOME, creating parent dirs as needed.
func writeHooksFile(t *testing.T, home string, hooksSect map[string]any) {
	t.Helper()
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.MarshalIndent(map[string]any{"hooks": hooksSect}, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// issuesContain reports whether any issue contains the given substring.
func issuesContain(issues []string, substr string) bool {
	for _, iss := range issues {
		if strings.Contains(iss, substr) {
			return true
		}
	}
	return false
}

// CH1 — legacy 3-event user (pre-expansion install): the three required
// events are valid; the six FutureOnly events have no key in hooks.json.
// allInstalled must remain true and Issues must be empty.
func TestCheckHooks_LegacyThreeEvent_ReportsInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeHooksFile(t, home, map[string]any{
		"SessionStart":     []any{pdxGroupEntry("SessionStart")},
		"UserPromptSubmit": []any{pdxGroupEntry("UserPromptSubmit")},
		"Stop":             []any{pdxGroupEntry("Stop")},
	})

	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if !status.Installed {
		t.Fatalf("legacy 3-event user: allInstalled=false, Issues=%v", status.Issues)
	}
	if len(status.Issues) != 0 {
		t.Fatalf("legacy 3-event user: Issues=%v, want empty", status.Issues)
	}
	futureOnly := []string{
		"SubagentStart", "SubagentStop", "StopFailure",
		"Notification", "PermissionRequest", "SessionEnd",
	}
	for _, name := range futureOnly {
		info, ok := status.Events[name]
		if !ok {
			t.Errorf("status.Events missing FutureOnly event %q", name)
			continue
		}
		if info.Installed {
			t.Errorf("FutureOnly event %q Installed=true, want false (absent)", name)
		}
	}
}

// CH2 — full 9-event install: every event valid, no issues.
func TestCheckHooks_NineEvent_FullyInstalled_NoIssues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	full := map[string]any{}
	for _, name := range expectedCodexInstallerNames {
		full[name] = []any{pdxGroupEntry(name)}
	}
	writeHooksFile(t, home, full)

	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if !status.Installed {
		t.Fatalf("full 9-event install: allInstalled=false, Issues=%v", status.Issues)
	}
	if len(status.Issues) != 0 {
		t.Fatalf("full 9-event install: Issues=%v, want empty", status.Issues)
	}
	for _, name := range expectedCodexInstallerNames {
		info := status.Events[name]
		if !info.Installed {
			t.Errorf("event %q Installed=false after full install", name)
		}
	}
}

// CH3 — required event missing (here SessionStart is absent): must block
// allInstalled and surface a "hook not installed" issue.
func TestCheckHooks_RequiredEventMissing_Blocks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeHooksFile(t, home, map[string]any{
		"UserPromptSubmit": []any{pdxGroupEntry("UserPromptSubmit")},
		"Stop":             []any{pdxGroupEntry("Stop")},
	})

	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatal("missing required SessionStart: allInstalled=true, want false")
	}
	if !issuesContain(status.Issues, "SessionStart hook not installed") {
		t.Fatalf("missing required SessionStart: issues=%v", status.Issues)
	}
}

// CH4 — FutureOnly key present but pdx command missing (user manually
// replaced the pdx entry with something else): classify as broken, emit a
// "malformed" warning, and block allInstalled. The 3 required events are
// valid and do not by themselves unblock.
func TestCheckHooks_FutureOnlyBroken_WarnsAndBlocks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeHooksFile(t, home, map[string]any{
		"SessionStart":     []any{pdxGroupEntry("SessionStart")},
		"UserPromptSubmit": []any{pdxGroupEntry("UserPromptSubmit")},
		"Stop":             []any{pdxGroupEntry("Stop")},
		"Notification": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": "/usr/bin/notify-me not-pdx",
						"timeout": 5,
					},
				},
			},
		},
	})

	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatal("FutureOnly broken: allInstalled=true, want false")
	}
	if !issuesContain(status.Issues, "Notification hook: pdx command malformed") {
		t.Fatalf("FutureOnly broken: issues=%v, want malformed warning for Notification", status.Issues)
	}
	if status.Events["Notification"].Installed {
		t.Fatal("Notification Installed=true, want false")
	}
}

// CH5 — the mixed-tolerance case: 3 required valid, 3 FutureOnly absent,
// 3 FutureOnly valid. allInstalled stays true because absent FutureOnly is
// the "legacy user, no need to reinstall" tolerance case.
func TestCheckHooks_FutureOnlyAbsent_DoesNotBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeHooksFile(t, home, map[string]any{
		"SessionStart":     []any{pdxGroupEntry("SessionStart")},
		"UserPromptSubmit": []any{pdxGroupEntry("UserPromptSubmit")},
		"Stop":             []any{pdxGroupEntry("Stop")},
		// 3 FutureOnly valid
		"StopFailure":       []any{pdxGroupEntry("StopFailure")},
		"Notification":      []any{pdxGroupEntry("Notification")},
		"PermissionRequest": []any{pdxGroupEntry("PermissionRequest")},
		// SubagentStart / SubagentStop / SessionEnd intentionally absent.
	})

	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if !status.Installed {
		t.Fatalf("mixed FutureOnly absent/valid: allInstalled=false, Issues=%v", status.Issues)
	}
	if len(status.Issues) != 0 {
		t.Fatalf("mixed FutureOnly absent/valid: Issues=%v, want empty", status.Issues)
	}
	for _, absent := range []string{"SubagentStart", "SubagentStop", "SessionEnd"} {
		if status.Events[absent].Installed {
			t.Errorf("absent FutureOnly %q Installed=true, want false", absent)
		}
	}
	for _, valid := range []string{"StopFailure", "Notification", "PermissionRequest"} {
		if !status.Events[valid].Installed {
			t.Errorf("valid FutureOnly %q Installed=false, want true", valid)
		}
	}
}

// CH7 — cross-event mis-filing: a valid-looking pdx command for the Stop
// event is copied into the Notification key (e.g. user hand-edit or
// legacy tooling). The substring-only check `strings.Contains(cmd,
// "pdx hook")` classifies this as valid; strict per-event tokenization
// must reject it and report the event broken. This is the Finding #1
// regression guard.
func TestCheckHooks_WrongEventNameCommand_ClassifiesAsBroken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeHooksFile(t, home, map[string]any{
		"SessionStart":     []any{pdxGroupEntry("SessionStart")},
		"UserPromptSubmit": []any{pdxGroupEntry("UserPromptSubmit")},
		"Stop":             []any{pdxGroupEntry("Stop")},
		// Notification key with a pdx command whose event-name token is
		// "Stop" — must be classified as broken for Notification, not valid.
		"Notification": []any{pdxGroupEntry("Stop")},
	})

	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatal("wrong-event-name command: allInstalled=true, want false")
	}
	if status.Events["Notification"].Installed {
		t.Fatal("Notification Installed=true, want false (command tail is 'Stop', not 'Notification')")
	}
	if !issuesContain(status.Issues, "Notification hook: pdx command malformed") {
		t.Fatalf("wrong-event-name: issues=%v, want malformed warning", status.Issues)
	}
}

// CH8 — wrong agent in command: Notification key contains a command
// that targets --agent cc instead of --agent codex. Must be classified
// as broken. Also Finding #1 regression guard.
func TestCheckHooks_WrongAgentCommand_ClassifiesAsBroken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeHooksFile(t, home, map[string]any{
		"SessionStart":     []any{pdxGroupEntry("SessionStart")},
		"UserPromptSubmit": []any{pdxGroupEntry("UserPromptSubmit")},
		"Stop":             []any{pdxGroupEntry("Stop")},
		"Notification": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": `"/usr/local/bin/pdx" hook --agent cc Notification`,
						"timeout": 5,
					},
				},
			},
		},
	})

	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatal("wrong-agent command: allInstalled=true, want false")
	}
	if status.Events["Notification"].Installed {
		t.Fatal("Notification Installed=true, want false (--agent cc, not codex)")
	}
	if !issuesContain(status.Issues, "Notification hook: pdx command malformed") {
		t.Fatalf("wrong-agent: issues=%v, want malformed warning", status.Issues)
	}
}

// CH9 — unit coverage of the new per-event command validator. Positive
// cases: unquoted abs-path pdx, quoted abs-path pdx, bare pdx. Negative
// cases: missing --agent codex, wrong event-name tail, non-pdx binary.
func TestIsPdxCommandCodexForEvent(t *testing.T) {
	positive := []struct {
		cmd, event string
	}{
		{`pdx hook --agent codex SessionStart`, "SessionStart"},
		{`/usr/local/bin/pdx hook --agent codex UserPromptSubmit`, "UserPromptSubmit"},
		{`"/usr/local/bin/pdx" hook --agent codex Notification`, "Notification"},
	}
	for _, tc := range positive {
		if !isPdxCommandCodexForEvent(tc.cmd, tc.event) {
			t.Errorf("expected valid for %q / event=%s", tc.cmd, tc.event)
		}
	}

	negative := []struct {
		cmd, event string
		reason     string
	}{
		{`pdx hook codex SessionStart`, "SessionStart", "missing --agent codex"},
		{`pdx hook --agent codex Stop`, "Notification", "wrong event-name tail"},
		{`/usr/local/bin/pdx hook --agent cc Notification`, "Notification", "wrong agent"},
		{`other-tool hook --agent codex Stop`, "Stop", "non-pdx binary"},
		{`"/usr/local/bin/pdx" hook --agent codex`, "Stop", "missing event name"},
	}
	for _, tc := range negative {
		if isPdxCommandCodexForEvent(tc.cmd, tc.event) {
			t.Errorf("expected invalid for %q / event=%s (%s)", tc.cmd, tc.event, tc.reason)
		}
	}
}

// CH6 — the strict-absent distinction (plan §1.4): a FutureOnly event with
// an empty entry array (hooks[event]=[]) is classified as broken, not
// absent, because mergeCodexHooks' remove path can legitimately leave []
// behind — treating it as absent would hide a real FutureOnly uninstall
// event as a silent "legacy user" false-green.
func TestCheckHooks_FutureOnlyEmptyArray_ClassifiesAsBroken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeHooksFile(t, home, map[string]any{
		"SessionStart":     []any{pdxGroupEntry("SessionStart")},
		"UserPromptSubmit": []any{pdxGroupEntry("UserPromptSubmit")},
		"Stop":             []any{pdxGroupEntry("Stop")},
		"Notification":     []any{}, // present key, empty array — must be broken not absent
	})

	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatal("FutureOnly empty-array: allInstalled=true, want false")
	}
	if !issuesContain(status.Issues, "Notification hook: pdx command malformed") {
		t.Fatalf("FutureOnly empty-array: issues=%v, want malformed warning", status.Issues)
	}
	if status.Events["Notification"].Installed {
		t.Fatal("Notification Installed=true, want false (empty array = broken)")
	}
}

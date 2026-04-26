package opencode_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/opencode"
)

// pinCanonicalPdxPath anchors the canonical pdx path resolver to the
// install-time literal so test installs and byte-exact checks render
// the same managed template body. Without this stub the resolver would
// fall back to the test binary's executable path and every valid-plugin
// assertion would drift — intentional for Finding #3 but not what the
// legacy install/check contract tests are exercising.
func pinCanonicalPdxPath(t *testing.T, path string) {
	t.Helper()
	opencode.SetResolveCanonicalPdxPathForTesting(t, func() (string, bool) {
		return path, true
	})
}

func TestOpenCodeHooks_InstallCheckRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pinCanonicalPdxPath(t, "/usr/local/bin/pdx")

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}

	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "pdx-managed:opencode-hooks:v1") {
		t.Fatalf("missing managed marker in plugin: %s", content)
	}
	if !strings.Contains(content, "/usr/local/bin/pdx") {
		t.Fatalf("missing pdx path in plugin: %s", content)
	}

	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("check hooks: %v", err)
	}
	if !status.Installed {
		t.Fatalf("expected installed status, got %+v", status)
	}
	if len(status.Events) == 0 {
		t.Fatalf("expected managed events in status, got %+v", status)
	}

	if err := p.RemoveHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("remove hooks: %v", err)
	}
	if _, err := os.Stat(pluginPath); !os.IsNotExist(err) {
		t.Fatalf("expected plugin removal, stat err=%v", err)
	}

	status, err = p.CheckHooks()
	if err != nil {
		t.Fatalf("check hooks after remove: %v", err)
	}
	if status.Installed {
		t.Fatalf("expected uninstalled status after remove, got %+v", status)
	}
	if len(status.Issues) == 0 {
		t.Fatalf("expected issues after remove, got %+v", status)
	}
}

// TestOpenCodeCheckHooks_ReportsAll8EventsFromEventsList asserts CheckHooks
// reports one Events entry per HookEventSpec — i.e. the check path reads from
// Events() rather than a legacy opencodeHookEvents slice.
func TestOpenCodeCheckHooks_ReportsAll8EventsFromEventsList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pinCanonicalPdxPath(t, "/usr/local/bin/pdx")

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}

	events := installableOpenCodeEvents(p.Events())
	if len(events) == 0 {
		t.Fatal("opencode Events() returned empty; CheckHooks iteration would be vacuous")
	}
	if len(status.Events) != len(events) {
		t.Errorf("status.Events len=%d, want %d (one per installable Events())", len(status.Events), len(events))
	}
	for _, e := range events {
		info, ok := status.Events[e.Name]
		if !ok {
			t.Errorf("status.Events missing key %q (from Events())", e.Name)
			continue
		}
		if !info.Installed {
			t.Errorf("event %q: Installed=false after fresh install", e.Name)
		}
	}
}

func installableOpenCodeEvents(events []agent.HookEventSpec) []agent.HookEventSpec {
	out := make([]agent.HookEventSpec, 0, len(events))
	for _, event := range events {
		if agent.IsInstallableHookSpec(event) {
			out = append(out, event)
		}
	}
	return out
}

// ---- fix-plan §2.4 byte-exact CheckHooks tests (OH1-OH5) ----
//
// The v3 plan redesigns opencode CheckHooks as "plugin is pdx-managed
// atomic artifact" rather than per-event health: byte-exact comparison
// against renderManagedPlugin(pdxPath). Any deviation — even a comment
// line — collapses the entire plugin to Installed=false. That matches
// the plugin's actual semantics (shared state across events means per-
// event judgement is unsafe) and closes all regex-based false-green
// routes the v2 plan tried to patch.

// OH1 — plugin written by writeManagedPlugin reports every declared event
// as Installed=true and no Issues.
func TestCheckHooks_ValidPlugin_AllInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pinCanonicalPdxPath(t, "/usr/local/bin/pdx")

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if !status.Installed {
		t.Fatalf("valid plugin: Installed=false, Issues=%v", status.Issues)
	}
	if len(status.Issues) != 0 {
		t.Fatalf("valid plugin: Issues=%v, want empty", status.Issues)
	}
	for _, spec := range installableOpenCodeEvents(p.Events()) {
		if info, ok := status.Events[spec.Name]; !ok {
			t.Errorf("status.Events missing %q", spec.Name)
		} else if !info.Installed {
			t.Errorf("event %q Installed=false", spec.Name)
		}
	}
}

// OH2 — plugin with the marker intact but a single deleted emit() call
// must be rejected. All event infos collapse to Installed=false because
// the plugin is no longer pdx-managed content.
func TestCheckHooks_HandEditedPlugin_Unmanaged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pinCanonicalPdxPath(t, "/usr/local/bin/pdx")

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	// Remove the Stop emit call.
	edited := strings.Replace(string(data), "await emit('Stop',", "// await emit('Stop',", 1)
	if edited == string(data) {
		t.Fatal("test setup: failed to mutate template (Stop emit not found)")
	}
	if err := os.WriteFile(pluginPath, []byte(edited), 0644); err != nil {
		t.Fatalf("write edit: %v", err)
	}

	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatalf("hand-edited plugin: Installed=true, want false")
	}
	joined := strings.Join(status.Issues, " | ")
	if !strings.Contains(joined, "plugin body differs from managed template") {
		t.Fatalf("hand-edited plugin: issues=%v, want 'plugin body differs from managed template'", status.Issues)
	}
	for _, spec := range installableOpenCodeEvents(p.Events()) {
		if info, ok := status.Events[spec.Name]; ok && info.Installed {
			t.Errorf("event %q Installed=true after byte-mismatch, want false", spec.Name)
		}
	}
}

// OH3 — existing behaviour preserved: plugin file without the managed
// marker is reported as unmanaged.
func TestCheckHooks_UnmanagedPlugin_NoMarker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pluginPath, []byte("export const Existing = async () => ({})\n"), 0644); err != nil {
		t.Fatalf("write unmanaged: %v", err)
	}

	status, err := opencode.NewProvider().CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatalf("unmanaged: Installed=true, want false")
	}
	if !strings.Contains(strings.Join(status.Issues, " | "), "unmanaged") {
		t.Fatalf("unmanaged: issues=%v, want 'unmanaged'", status.Issues)
	}
}

// OH4 — a comment-only edit is still a byte-mismatch. Regex-based
// extraction would have missed this (the comment does not contain an
// emit() call), but byte-exact comparison catches it uniformly. This is
// the test that replaces v2's per-event unexpected-emit test.
func TestCheckHooks_CommentEditInPlugin_DetectedViaByteCompare(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pinCanonicalPdxPath(t, "/usr/local/bin/pdx")

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	if err := os.WriteFile(pluginPath, []byte("// added by user\n"+string(data)), 0644); err != nil {
		t.Fatalf("write edit: %v", err)
	}

	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatalf("comment-edit: Installed=true, want false (byte-exact must reject)")
	}
}

// OH5 — plugin with marker present but the corrupted body no longer
// byte-matches the managed template rendered from the trusted canonical
// pdx path. CheckHooks must fall back to a "body differs" issue without
// panicking. Replaces the v4 "extractPdxPath failure" test: pdxPath
// extraction is no longer on the health path after Finding #3.
func TestCheckHooks_CorruptPdxPathLine_ReportsBodyDiffers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pinCanonicalPdxPath(t, "/usr/local/bin/pdx")

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	// Remove the entire pdxPath line; body must drift from the expected
	// managed template regardless of the trusted pdxPath we render.
	broken := strings.Replace(string(data), `const pdxPath = "/usr/local/bin/pdx"`, `const pdxPath = /* corrupt */`, 1)
	if broken == string(data) {
		t.Fatal("test setup: pdxPath line not found")
	}
	if err := os.WriteFile(pluginPath, []byte(broken), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CheckHooks panicked: %v", r)
		}
	}()

	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatalf("corrupt pdxPath: Installed=true, want false")
	}
	if len(status.Issues) == 0 {
		t.Fatal("corrupt pdxPath: expected at least one issue")
	}
}

// OH6 — path-only edit: a user edits only the pdxPath literal in an
// otherwise-untouched managed plugin (e.g. to point at a tampered
// binary). v4 extracted the literal from the file itself and rendered
// expected with the same path → byte-exact self-round-trip → false
// green. v5 (Finding #3) resolves the canonical pdx path from the
// runtime (os.Executable + EvalSymlinks), so a path-only edit drifts
// from the trusted render and CheckHooks must report the file as
// drifted.
func TestCheckHooks_PathOnlyEdit_DetectsAsDrifted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	// Swap in a different — attacker-controlled — pdx path literal.
	tampered := strings.Replace(
		string(data),
		`const pdxPath = "/usr/local/bin/pdx"`,
		`const pdxPath = "/fake/malicious/pdx"`,
		1,
	)
	if tampered == string(data) {
		t.Fatal("test setup: original pdxPath literal not found")
	}
	if err := os.WriteFile(pluginPath, []byte(tampered), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Pin the canonical resolver to the real install-time path so expected
	// render matches the untampered template — the drift is the on-disk
	// pdxPath, not the check baseline.
	opencode.SetResolveCanonicalPdxPathForTesting(t, func() (string, bool) {
		return "/usr/local/bin/pdx", true
	})

	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatalf("path-only edit: Installed=true, want false (Finding #3)")
	}
	if !strings.Contains(strings.Join(status.Issues, " | "), "plugin body differs from managed template") {
		t.Fatalf("path-only edit: issues=%v, want 'plugin body differs from managed template'", status.Issues)
	}
}

// OH7 — canonical path trust loop: when canonical resolver reports the
// same path the file already carries, an untouched managed plugin
// byte-matches the expected render and CheckHooks must report all
// events installed.
func TestCheckHooks_TrustsCanonicalPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}

	opencode.SetResolveCanonicalPdxPathForTesting(t, func() (string, bool) {
		return "/usr/local/bin/pdx", true
	})

	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if !status.Installed {
		t.Fatalf("trusted canonical path: Installed=false, Issues=%v", status.Issues)
	}
}

// OH8 — canonical resolver failure fallback: when os.Executable or
// EvalSymlinks fails (e.g. daemon launched from an unusual path), the
// checker must surface a body-differs issue rather than crash or
// silently pass. Replaces OH5's panic-safety role.
func TestCheckHooks_CanonicalResolveFails_ReportsBodyDiffers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}

	opencode.SetResolveCanonicalPdxPathForTesting(t, func() (string, bool) {
		return "", false
	})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CheckHooks panicked: %v", r)
		}
	}()

	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatalf("resolve-fail: Installed=true, want false")
	}
	if !strings.Contains(strings.Join(status.Issues, " | "), "plugin body differs from managed template") {
		t.Fatalf("resolve-fail: issues=%v, want 'plugin body differs from managed template'", status.Issues)
	}
}

// TestOpenCodeCheckHooks_ManagedReflectsMarker asserts HookStatus.Managed
// is driven by marker presence, not by byte-match. Finding #2: UI Remove
// button must stay enabled for drifted-but-managed opencode plugin.
func TestOpenCodeCheckHooks_ManagedReflectsMarker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pinCanonicalPdxPath(t, "/usr/local/bin/pdx")

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	// Drift the body by prepending a comment — marker remains, byte-match fails.
	if err := os.WriteFile(pluginPath, []byte("// user note\n"+string(data)), 0644); err != nil {
		t.Fatalf("write drift: %v", err)
	}

	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if status.Installed {
		t.Fatal("drifted managed plugin: Installed=true, want false")
	}
	if !status.Managed {
		t.Fatal("drifted managed plugin: Managed=false, want true (marker present)")
	}
}

// TestOpenCodeCheckHooks_ManagedFalseWhenUnmanagedOrAbsent covers the
// two Managed=false cases: no file at all, and a file that pre-dates
// pdx (marker absent).
func TestOpenCodeCheckHooks_ManagedFalseWhenUnmanagedOrAbsent(t *testing.T) {
	t.Run("absent file", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		status, err := opencode.NewProvider().CheckHooks()
		if err != nil {
			t.Fatalf("CheckHooks: %v", err)
		}
		if status.Managed {
			t.Fatal("absent file: Managed=true, want false")
		}
	})
	t.Run("unmanaged file", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
		if err := os.MkdirAll(filepath.Dir(pluginPath), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(pluginPath, []byte("export const Existing = () => ({})\n"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		status, err := opencode.NewProvider().CheckHooks()
		if err != nil {
			t.Fatalf("CheckHooks: %v", err)
		}
		if status.Managed {
			t.Fatal("unmanaged file: Managed=true, want false")
		}
	})
}

// TestOpenCodeCheckHooks_UpgradesAvailableAlwaysEmpty asserts opencode
// has no FutureOnly events in its current catalog, so UpgradesAvailable
// is empty on every valid install.
func TestOpenCodeCheckHooks_UpgradesAvailableAlwaysEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pinCanonicalPdxPath(t, "/usr/local/bin/pdx")

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if len(status.UpgradesAvailable) != 0 {
		t.Errorf("opencode UpgradesAvailable=%v, want empty", status.UpgradesAvailable)
	}
	for _, info := range status.Events {
		if info.FutureOnly {
			t.Errorf("opencode event info.FutureOnly=true, want false for every spec")
		}
	}
}

func TestOpenCodeHooks_UnmanagedFileRejected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pluginPath, []byte("export const Existing = async () => ({})\n"), 0644); err != nil {
		t.Fatalf("write unmanaged plugin: %v", err)
	}

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err == nil {
		t.Fatal("expected install to reject unmanaged file")
	}
	if err := p.RemoveHooks("/usr/local/bin/pdx"); err == nil {
		t.Fatal("expected remove to reject unmanaged file")
	}

	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("check hooks: %v", err)
	}
	if status.Installed {
		t.Fatalf("expected unmanaged file to report not installed, got %+v", status)
	}
	if len(status.Issues) == 0 {
		t.Fatalf("expected unmanaged issue, got %+v", status)
	}
	if !strings.Contains(strings.Join(status.Issues, " | "), "unmanaged") {
		t.Fatalf("expected unmanaged issue text, got %+v", status.Issues)
	}
}

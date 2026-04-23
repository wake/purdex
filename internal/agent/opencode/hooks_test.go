package opencode_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent/opencode"
)

func TestOpenCodeHooks_InstallCheckRemove(t *testing.T) {
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

	p := opencode.NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}

	events := p.Events()
	if len(events) == 0 {
		t.Fatal("opencode Events() returned empty; CheckHooks iteration would be vacuous")
	}
	if len(status.Events) != len(events) {
		t.Errorf("status.Events len=%d, want %d (one per Events())", len(status.Events), len(events))
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
	for _, spec := range p.Events() {
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
	for _, spec := range p.Events() {
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

// OH5 — plugin with marker present but pdxPath literal corrupted. The
// extractPdxPath helper must fail cleanly (no panic) and CheckHooks must
// fall back to a "body differs" issue rather than crashing.
func TestCheckHooks_ExtractPdxPathFails_FallsBackToUnmanaged(t *testing.T) {
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
	// Remove the entire pdxPath line — extractPdxPath must no longer match.
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

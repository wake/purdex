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

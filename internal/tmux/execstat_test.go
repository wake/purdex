package tmux_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/tmux"
)

// installStatTmux puts fake `tmux` and `ps` scripts first on PATH. Each takes
// a few ms and prints one canned line; with fail they exit 1 instead.
func installStatTmux(t *testing.T, out string, fail bool) {
	t.Helper()
	dir := t.TempDir()
	exit := "0"
	if fail {
		exit = "1"
	}
	body := "#!/bin/sh\nsleep 0.01\necho '" + out + "'\nexit " + exit + "\n"
	for _, name := range []string{"tmux", "ps"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	execstat.Tmux.Reset()
	execstat.PS.Reset()
	t.Cleanup(func() { execstat.Tmux.Reset(); execstat.PS.Reset() })
}

func TestExecstatCountsTmuxForks(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		want int64 // tmux forks
		call func(r *tmux.RealExecutor)
	}{
		{"PaneSessionName", 1, func(r *tmux.RealExecutor) { _, _ = r.PaneSessionName("%1") }},
		{"PaneSessionID", 1, func(r *tmux.RealExecutor) { _, _ = r.PaneSessionID(ctx, "%1") }},
		{"PaneCurrentPath", 1, func(r *tmux.RealExecutor) { _, _ = r.PaneCurrentPath("%1") }},
		{"PanePID", 1, func(r *tmux.RealExecutor) { _, _ = r.PanePID("%1") }},
		{"ActivePanePID", 1, func(r *tmux.RealExecutor) { _, _ = r.ActivePanePID("%1") }},
		{"ListSessions", 1, func(r *tmux.RealExecutor) { _, _ = r.ListSessions(ctx) }},
		{"ListAllPanes", 1, func(r *tmux.RealExecutor) { _, _ = r.ListAllPanes(ctx) }},
		{"HasSession", 1, func(r *tmux.RealExecutor) { _ = r.HasSession("x") }},
		{"HasSessionContext", 1, func(r *tmux.RealExecutor) { _, _ = r.HasSessionContext(ctx, "x") }},
		{"HasPane", 1, func(r *tmux.RealExecutor) { _, _ = r.HasPane("%1") }},
		{"KillSession", 1, func(r *tmux.RealExecutor) { _ = r.KillSession("x") }},
		{"SendKeys", 1, func(r *tmux.RealExecutor) { _ = r.SendKeys("%1", "a") }},
		{"PasteText (load+paste)", 2, func(r *tmux.RealExecutor) { _ = r.PasteText("%1", "hi") }},
		{"CapturePaneContent", 1, func(r *tmux.RealExecutor) { _, _ = r.CapturePaneContent("%1", 10) }},
		{"ResizeWindow", 1, func(r *tmux.RealExecutor) { _ = r.ResizeWindow("%1", 80, 24) }},
		{"SetWindowOption", 1, func(r *tmux.RealExecutor) { _ = r.SetWindowOption("%1", "a", "b") }},
		{"ShowHooksGlobal", 1, func(r *tmux.RealExecutor) { _, _ = r.ShowHooksGlobal() }},
	}
	for _, fail := range []bool{false, true} {
		for _, tc := range cases {
			name := tc.name
			if fail {
				name += "/error"
			}
			t.Run(name, func(t *testing.T) {
				installStatTmux(t, "1", fail)
				tc.call(tmux.NewRealExecutor())
				n, d := execstat.Tmux.Snapshot()
				if n == 0 || d <= 0 {
					t.Fatalf("Tmux = (%d, %v), want n>0 and d>0", n, d)
				}
				if n != tc.want && !fail {
					t.Fatalf("Tmux n = %d, want exactly %d", n, tc.want)
				}
			})
		}
	}
}

func TestExecstatCountsPSInPaneProcessCommands(t *testing.T) {
	installStatTmux(t, "1", false)
	_, _ = tmux.NewRealExecutor().PaneChildCommands("%1")
	if n, _ := execstat.Tmux.Snapshot(); n != 1 {
		t.Fatalf("Tmux n = %d, want 1 (PanePID)", n)
	}
	if n, d := execstat.PS.Snapshot(); n != 1 || d <= 0 {
		t.Fatalf("PS = (%d, %v), want (1, >0)", n, d)
	}
}

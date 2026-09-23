package tmux_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

// Helper-process fake tmux (the os/exec TestHelperProcess pattern). A shell
// script named `tmux` on PATH re-execs this test binary into
// TestHelperProcessTmux, which answers `display-message -p -t <t> <format>`
// by expanding every #{name} in the format from a JSON value map — so the
// per-field and the combined invocation see exactly the same raw tmux output.
const (
	helperEnv       = "PDX_HELPER_TMUX"
	helperValuesEnv = "PDX_HELPER_TMUX_VALUES"
	helperFailEnv   = "PDX_HELPER_TMUX_FAIL"
	helperLogEnv    = "PDX_HELPER_TMUX_LOG"
)

var formatVarRe = regexp.MustCompile(`#\{([a-z_]+)\}`)

func TestHelperProcessTmux(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if logPath := os.Getenv(helperLogEnv); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%q\n", args)
			f.Close()
		}
	}
	if os.Getenv(helperFailEnv) == "1" {
		fmt.Fprintln(os.Stderr, "can't find session: nope")
		os.Exit(1)
	}
	if len(args) != 5 || args[0] != "display-message" || args[1] != "-p" || args[2] != "-t" {
		fmt.Fprintf(os.Stderr, "unexpected args: %q\n", args)
		os.Exit(2)
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(os.Getenv(helperValuesEnv)), &values); err != nil {
		fmt.Fprintf(os.Stderr, "bad values: %v\n", err)
		os.Exit(2)
	}
	out := formatVarRe.ReplaceAllStringFunc(args[4], func(m string) string {
		return values[formatVarRe.FindStringSubmatch(m)[1]]
	})
	fmt.Fprint(os.Stdout, out+"\n")
	os.Exit(0)
}

// installHelperTmux puts the helper-process fake tmux first on PATH and
// returns the path of its invocation log (one line per tmux exec).
func installHelperTmux(t *testing.T, values map[string]string, fail bool) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run='^TestHelperProcessTmux$' -- \"$@\"\n", os.Args[0])
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(helperEnv, "1")
	t.Setenv(helperValuesEnv, string(raw))
	t.Setenv(helperLogEnv, logPath)
	if fail {
		t.Setenv(helperFailEnv, "1")
	} else {
		t.Setenv(helperFailEnv, "")
	}
	return logPath
}

func helperCalls(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// Pins ActivePaneMetadata's observable behaviour — field order, per-field
// sanitising, error on a failing tmux — so the per-field → single
// display-message merge (#1293 §3.1) is provably equivalent.
func TestRealExecutorActivePaneMetadata_Equivalence(t *testing.T) {
	base := map[string]string{
		"session_id":           "$3",
		"session_name":         "dev",
		"window_id":            "@4",
		"pane_id":              "%7",
		"pane_title":           "my title",
		"window_name":          "zsh",
		"pane_current_command": "vim",
	}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}

	cases := []struct {
		name    string
		values  map[string]string
		fail    bool
		want    tmux.TmuxPaneMetadata
		wantErr bool
	}{
		{
			name:   "plain values in field order",
			values: base,
			want: tmux.TmuxPaneMetadata{
				SessionID: "$3", SessionName: "dev", WindowID: "@4", PaneID: "%7",
				PaneTitle: "my title", WindowName: "zsh", PaneCurrentCommand: "vim",
			},
		},
		{
			name: "control chars and runs of whitespace are sanitised per field",
			values: with(
				"pane_title", "  a\x01b   c \x1b[31m ",
				"window_name", "\x7fwin\u0085dow",
				"pane_current_command", "cmd\twith\ttabs",
			),
			want: tmux.TmuxPaneMetadata{
				SessionID: "$3", SessionName: "dev", WindowID: "@4", PaneID: "%7",
				PaneTitle: "a b c [31m", WindowName: "win dow", PaneCurrentCommand: "cmd with tabs",
			},
		},
		{
			name:   "embedded newline inside a field",
			values: with("pane_title", "line1\nline2", "session_name", "a\r\nb"),
			want: tmux.TmuxPaneMetadata{
				SessionID: "$3", SessionName: "a b", WindowID: "@4", PaneID: "%7",
				PaneTitle: "line1 line2", WindowName: "zsh", PaneCurrentCommand: "vim",
			},
		},
		{
			name:   "empty fields stay empty",
			values: with("pane_title", "", "window_name", "", "pane_current_command", ""),
			want: tmux.TmuxPaneMetadata{
				SessionID: "$3", SessionName: "dev", WindowID: "@4", PaneID: "%7",
			},
		},
		{
			name:    "tmux failure is an error with zero metadata",
			values:  base,
			fail:    true,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installHelperTmux(t, tc.values, tc.fail)
			got, err := (&tmux.RealExecutor{}).ActivePaneMetadata(context.Background(), "dev")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				if got != (tmux.TmuxPaneMetadata{}) {
					t.Fatalf("want zero metadata on error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ActivePaneMetadata: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ActivePaneMetadata() =\n %+v\nwant\n %+v", got, tc.want)
			}
		})
	}
}

// The merge is the point of §3.4: one tmux exec per session, not seven.
func TestRealExecutorActivePaneMetadata_OneDisplayMessage(t *testing.T) {
	logPath := installHelperTmux(t, map[string]string{
		"session_id": "$1", "session_name": "dev", "window_id": "@1", "pane_id": "%1",
		"pane_title": "t", "window_name": "w", "pane_current_command": "c",
	}, false)
	if _, err := (&tmux.RealExecutor{}).ActivePaneMetadata(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}
	calls := helperCalls(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("want 1 tmux exec, got %d: %v", len(calls), calls)
	}
	if !strings.Contains(calls[0], `"=dev:"`) {
		t.Fatalf("display-message must target the active pane =dev:, got %s", calls[0])
	}
}

// installSleepingTmux puts a fake tmux on PATH that never answers within any
// test deadline and leaves a grandchild holding the stdout pipe, so only
// cmd.WaitDelay can make Output() return once the direct child is killed.
func installSleepingTmux(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nsleep 3 &\nexec sleep 3\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// readBound is how long a read may take past its deadline: the executor's
// WaitDelay plus scheduling slack. Far below the fake's 3 s sleep, so a read
// that is not killed, or that waits on the grandchild's pipe, goes red.
const (
	readDeadline = 200 * time.Millisecond
	readBound    = 2 * time.Second
)

func TestRealExecutorListSessions_DeadlineKillsHungRead(t *testing.T) {
	dir := installSleepingTmux(t)

	ctx, cancel := context.WithTimeout(context.Background(), readDeadline)
	defer cancel()
	start := time.Now()
	sessions, err := (&tmux.RealExecutor{}).ListSessions(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want error wrapping context.DeadlineExceeded, got %v", err)
	}
	if sessions != nil {
		t.Fatalf("want no sessions on timeout, got %v", sessions)
	}
	if elapsed > readBound {
		t.Fatalf("hung read returned after %v, want within %v", elapsed, readBound)
	}

	// The next read against a working tmux succeeds: nothing is left wedged.
	working := "#!/bin/sh\nprintf '$0\\tdev\\t/tmp\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(working), 0o755); err != nil {
		t.Fatal(err)
	}
	sessions, err = (&tmux.RealExecutor{}).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("next read: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Name != "dev" {
		t.Fatalf("next read = %+v, want one session dev", sessions)
	}
}

func TestRealExecutorListSessions_CancelWrapsCanceled(t *testing.T) {
	installSleepingTmux(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(readDeadline, cancel)
	start := time.Now()
	_, err := (&tmux.RealExecutor{}).ListSessions(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want error wrapping context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > readBound {
		t.Fatalf("cancelled read returned after %v, want within %v", elapsed, readBound)
	}
}

func TestRealExecutorActivePaneMetadata_DeadlineKillsHungRead(t *testing.T) {
	installSleepingTmux(t)
	ctx, cancel := context.WithTimeout(context.Background(), readDeadline)
	defer cancel()
	start := time.Now()
	got, err := (&tmux.RealExecutor{}).ActivePaneMetadata(ctx, "dev")
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want error wrapping context.DeadlineExceeded, got %v", err)
	}
	if got != (tmux.TmuxPaneMetadata{}) {
		t.Fatalf("want zero metadata on timeout, got %+v", got)
	}
	if elapsed > readBound {
		t.Fatalf("hung read returned after %v, want within %v", elapsed, readBound)
	}
}

// A combined output that is short of fields (a format tmux could not fully
// expand, or a truncated answer) is an error, never a half-filled struct.
func TestRealExecutorActivePaneMetadata_MissingFieldIsError(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '$1\\tdev\\t@1\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := (&tmux.RealExecutor{}).ActivePaneMetadata(context.Background(), "dev")
	if err == nil {
		t.Fatalf("want error for a short answer, got %+v", got)
	}
	if got != (tmux.TmuxPaneMetadata{}) {
		t.Fatalf("want zero metadata on error, got %+v", got)
	}
}

// The shared fake's read hook runs with the caller's context, so a test can
// model a hung tmux read that only the deadline ends.
func TestFakeExecutor_ReadHookHonoursContext(t *testing.T) {
	f := tmux.NewFakeExecutor()
	f.AddSession("dev", "/tmp")
	f.SetActivePaneMetadata("dev", tmux.TmuxPaneMetadata{PaneTitle: "t"})
	release := make(chan struct{})
	f.SetReadHook(tmux.BlockReadsUntil(release, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := f.ListSessions(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ListSessions: want DeadlineExceeded, got %v", err)
	}
	if _, err := f.ActivePaneMetadata(ctx, "dev"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ActivePaneMetadata: want DeadlineExceeded, got %v", err)
	}

	close(release)
	if s, err := f.ListSessions(context.Background()); err != nil || len(s) != 1 {
		t.Fatalf("after release: %v, %v", s, err)
	}
	if md, err := f.ActivePaneMetadata(context.Background(), "dev"); err != nil || md.PaneTitle != "t" {
		t.Fatalf("after release: %+v, %v", md, err)
	}
}

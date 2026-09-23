package tmux_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
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
// display-message merge (#1293 §3.1) is provably equivalent: every case is
// checked against an explicit want AND against the per-field read on the same
// raw tmux values.
//
// pane_title and window_name are text a user or a program in the pane can set,
// so they may carry a raw TAB (the combined read's separator). The combined
// format fences each of them between two fixed-shape ids; when a TAB shifts
// the ids out of place the read falls back to per-field queries (wantCalls 8 =
// 1 combined + 7 per-field) instead of returning misaligned fields.
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

	baseWant := tmux.TmuxPaneMetadata{
		SessionID: "$3", SessionName: "dev", WindowID: "@4", PaneID: "%7",
		PaneTitle: "my title", WindowName: "zsh", PaneCurrentCommand: "vim",
	}

	cases := []struct {
		name      string
		values    map[string]string
		fail      bool
		want      tmux.TmuxPaneMetadata
		wantErr   bool
		wantCalls int
	}{
		{
			name:      "plain values in field order",
			values:    base,
			want:      baseWant,
			wantCalls: 1,
		},
		{
			name:      "TAB in pane_title falls back to per-field",
			values:    with("pane_title", "my\ttitle"),
			want:      baseWant,
			wantCalls: 8,
		},
		{
			name:   "TAB in window_name falls back to per-field",
			values: with("window_name", "z\tsh"),
			want: tmux.TmuxPaneMetadata{
				SessionID: "$3", SessionName: "dev", WindowID: "@4", PaneID: "%7",
				PaneTitle: "my title", WindowName: "z sh", PaneCurrentCommand: "vim",
			},
			wantCalls: 8,
		},
		{
			name:   "TABs in pane_title and window_name fall back to per-field",
			values: with("pane_title", "a\tb\tc", "window_name", "\tw\t"),
			want: tmux.TmuxPaneMetadata{
				SessionID: "$3", SessionName: "dev", WindowID: "@4", PaneID: "%7",
				PaneTitle: "a b c", WindowName: "w", PaneCurrentCommand: "vim",
			},
			wantCalls: 8,
		},
		{
			// A title that forges the id shapes on its own still cannot pass:
			// every fence it shifts lands on a field of the wrong shape.
			name:      "TAB-laden pane_title that mimics ids falls back",
			values:    with("pane_title", "x\t@9\ty"),
			want:      tmux.TmuxPaneMetadata{SessionID: "$3", SessionName: "dev", WindowID: "@4", PaneID: "%7", PaneTitle: "x @9 y", WindowName: "zsh", PaneCurrentCommand: "vim"},
			wantCalls: 8,
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
			wantCalls: 1, // the last field absorbs its own TABs: no fallback
		},
		{
			name:   "embedded newline inside a field",
			values: with("pane_title", "line1\nline2", "session_name", "a\r\nb"),
			want: tmux.TmuxPaneMetadata{
				SessionID: "$3", SessionName: "a b", WindowID: "@4", PaneID: "%7",
				PaneTitle: "line1 line2", WindowName: "zsh", PaneCurrentCommand: "vim",
			},
			wantCalls: 1,
		},
		{
			name:   "empty fields stay empty",
			values: with("pane_title", "", "window_name", "", "pane_current_command", ""),
			want: tmux.TmuxPaneMetadata{
				SessionID: "$3", SessionName: "dev", WindowID: "@4", PaneID: "%7",
			},
			wantCalls: 1,
		},
		{
			name:      "tmux failure is an error with zero metadata",
			values:    base,
			fail:      true,
			wantErr:   true,
			wantCalls: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logPath := installHelperTmux(t, tc.values, tc.fail)
			got, err := (&tmux.RealExecutor{}).ActivePaneMetadata(context.Background(), "dev")
			if calls := helperCalls(t, logPath); len(calls) != tc.wantCalls {
				t.Fatalf("want %d tmux exec(s), got %d: %v", tc.wantCalls, len(calls), calls)
			}
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
			perField, err := (&tmux.RealExecutor{}).ActivePaneMetadataPerField(context.Background(), "dev")
			if err != nil {
				t.Fatalf("per-field read: %v", err)
			}
			if got != perField {
				t.Fatalf("combined read differs from the per-field read:\n %+v\n %+v", got, perField)
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
// test deadline. Like a real tmux read it is ONE client process with no
// children: the script records its PID and then execs sleep in place, so the
// recorded PID is the very process exec.Cmd started and must Wait on. It
// returns the fake's directory and the path of the PID file.
func installSleepingTmux(t *testing.T) (dir, pidFile string) {
	t.Helper()
	dir = t.TempDir()
	pidFile = filepath.Join(dir, "tmux.pid")
	script := fmt.Sprintf("#!/bin/sh\n[ -n \"$PDX_FAKE_TMUX_WARM\" ] && exit 0\necho $$ > %q\nexec sleep 3\n", pidFile)
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// The first exec of a freshly written script can take longer than the
	// read deadline on macOS (the system checks the new file), and a fake
	// killed before it records its PID proves nothing. Run it once untimed.
	warm := exec.Command(fake)
	warm.Env = append(os.Environ(), "PDX_FAKE_TMUX_WARM=1")
	if err := warm.Run(); err != nil {
		t.Fatalf("warm-up run of the fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, pidFile
}

// assertChildReaped checks that the fake tmux the read started is gone — not
// merely killed but waited for. An un-reaped (zombie) child still answers
// kill(pid, 0), so ESRCH proves exec.Cmd.Wait collected it.
func assertChildReaped(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("fake tmux never recorded its PID: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("bad PID %q: %v", raw, err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("fake tmux (pid %d) still exists after the read returned: kill(pid, 0) = %v", pid, err)
	}
}

// readDeadline is the read's context deadline. A killed read must return by
// readDeadline + tmux.ReadWaitDelay; readSlack is scheduling slack on top.
// All far below the fake's 3 s sleep, so a read that is not killed goes red.
const (
	readDeadline = 200 * time.Millisecond
	readSlack    = 500 * time.Millisecond
	readBound    = readDeadline + tmux.ReadWaitDelay + readSlack
)

func TestRealExecutorListSessions_DeadlineKillsHungRead(t *testing.T) {
	dir, pidFile := installSleepingTmux(t)

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
	assertChildReaped(t, pidFile)

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
	_, pidFile := installSleepingTmux(t)
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
	assertChildReaped(t, pidFile)
}

func TestRealExecutorActivePaneMetadata_DeadlineKillsHungRead(t *testing.T) {
	_, pidFile := installSleepingTmux(t)
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
	assertChildReaped(t, pidFile)
}

// A combined answer short of fields (a format tmux could not fully expand, or
// a truncated answer) is never turned into a half-filled struct: the read
// falls back to per-field queries, and when those fail too it is an error
// with zero metadata.
func TestRealExecutorActivePaneMetadata_ShortAnswerFallsBackToPerField(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	// The combined format is the only argument containing a TAB: answer it
	// short, and fail every per-field query.
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %q\ncase \"$5\" in\n*\"$(printf '\\t')\"*) printf '$1\\tdev\\t@1\\n' ;;\n*) echo \"can't find pane\" >&2; exit 1 ;;\nesac\n", logPath)
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := (&tmux.RealExecutor{}).ActivePaneMetadata(context.Background(), "dev")
	if err == nil {
		t.Fatalf("want error for a short answer whose fallback fails, got %+v", got)
	}
	if got != (tmux.TmuxPaneMetadata{}) {
		t.Fatalf("want zero metadata on error, got %+v", got)
	}
	// 1 combined + the first per-field query, which fails and stops the read.
	if calls := helperCalls(t, logPath); len(calls) != 2 {
		t.Fatalf("want the combined read then the per-field fallback (2 execs), got %d: %v", len(calls), calls)
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

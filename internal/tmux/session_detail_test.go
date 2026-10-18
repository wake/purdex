package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func detailLine(f ...string) string { return strings.Join(f, detailSep) }

func goodDetailLine(id string) string {
	return detailLine(id, "1792321847", "78306:1792321847", "@1", "%1", "work", "Mini-Lab.local", "zsh", "vim", "/Users/wake/x y")
}

func TestParseSessionDetails_Good(t *testing.T) {
	out := goodDetailLine("$0") + "\n" + detailLine("$1", "5", "78306:1792321847", "@2", "%7", "other", "a\tb", "win", "node", "/p/with\ttab") + "\n"
	got, ok := parseSessionDetails(out)
	require.True(t, ok)
	require.Len(t, got, 2)
	assert.Equal(t, TmuxSession{ID: "$0", Name: "work", Created: 1792321847, Cwd: "/Users/wake/x y"}, got[0].TmuxSession)
	assert.Equal(t, "78306:1792321847", got[0].Instance)
	assert.Equal(t, TmuxPaneMetadata{SessionID: "$0", SessionName: "work", WindowID: "@1", PaneID: "%1", PaneTitle: "Mini-Lab.local", WindowName: "zsh", PaneCurrentCommand: "vim"}, got[0].Pane)
	assert.Equal(t, "a b", got[1].Pane.PaneTitle, "free text is sanitised as the per-session read did")
	assert.Equal(t, "/p/with\ttab", got[1].Cwd, "the path is raw, as in ListSessions")
}

func TestParseSessionDetails_EmptyIsAnEmptyList(t *testing.T) {
	for _, out := range []string{"", "\n", "  \n"} {
		got, ok := parseSessionDetails(out)
		assert.True(t, ok, out)
		assert.Empty(t, got)
	}
}

// Anything that cannot be trusted refuses the whole answer, so the caller reads the old way; never a half-parsed list.
func TestParseSessionDetails_Untrusted(t *testing.T) {
	good := goodDetailLine("$0")
	for name, out := range map[string]string{
		"a locale that turned the separators into underscores": strings.ReplaceAll(good, detailSep, "_"),
		"too few fields":                       detailLine("$0", "1", "1:1", "@1", "%1", "work"),
		"one field short (no path)":            detailLine("$0", "1", "1:1", "@1", "%1", "work", "t", "zsh", "vim"),
		"a free-text field held the separator": detailLine("$0", "1", "1:1", "@1", "%1", "work", "x"+detailSep+"y", "zsh", "vim", "/p"),
		"the id is not an id":                  detailLine("x0", "1", "1:1", "@1", "%1", "work", "t", "zsh", "vim", "/p"),
		"the instance is not an instance":      detailLine("$0", "1", "oops", "@1", "%1", "work", "t", "zsh", "vim", "/p"),
		"the window id is not an id":           detailLine("$0", "1", "1:1", "w1", "%1", "work", "t", "zsh", "vim", "/p"),
		"the pane id is not an id":             detailLine("$0", "1", "1:1", "@1", "p1", "work", "t", "zsh", "vim", "/p"),
		"a title with a newline":               detailLine("$0", "1", "1:1", "@1", "%1", "work", "t\nu", "zsh", "vim", "/p"),
		"one good line and one bad":            good + "\n" + "garbage",
		"the same session twice":               good + "\n" + good,
	} {
		got, ok := parseSessionDetails(out)
		assert.False(t, ok, name)
		assert.Nil(t, got, name)
	}
}

// privateTmux gives the test a tmux server of its own: TMUX_TMPDIR points the default socket into a temp directory, the server is
// stopped through that explicit socket when the test ends, and TMUX is unset so a developer's session is never reached.
func privateTmux(t *testing.T) (tm func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "pdxsd")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("LANG", "en_US.UTF-8")
	unsetenv(t, "TMUX")
	sock := filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
	tm = func(args ...string) string {
		out, err := exec.Command("tmux", append([]string{"-f", "/dev/null"}, args...)...).CombinedOutput()
		require.NoError(t, err, "tmux %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", sock, "kill-server").Run() })
	return tm
}

func unsetenv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		os.Unsetenv(k)
		t.Cleanup(func() {
			if had {
				os.Setenv(k, old)
			}
		})
	}
}

// Against a real tmux: one list-sessions gives the sessions, their active panes and the server instance, with the values the
// separate reads gave; and it does so with NO UTF-8 locale too, where a TAB-separated answer comes back as one field per line.
func TestListSessionsDetailed_RealTmux(t *testing.T) {
	for _, locale := range []string{"UTF-8", "none", "C"} {
		t.Run(locale, func(t *testing.T) {
			tm := privateTmux(t)
			cjkDir := filepath.Join(os.TempDir(), "pdxsd-工作🙂")
			require.NoError(t, os.MkdirAll(cjkDir, 0o755))
			t.Cleanup(func() { os.RemoveAll(cjkDir) })
			tm("new-session", "-d", "-s", "alpha", "-c", "/tmp", "sleep 120")
			tm("new-session", "-d", "-s", "beta gamma", "-c", "/private/tmp", "sleep 120")
			tm("new-session", "-d", "-s", "工作🙂 ü", "-c", cjkDir, "sleep 120")
			tm("select-pane", "-t", "=alpha:", "-T", "my title")
			tm("select-pane", "-t", "=工作🙂 ü:", "-T", "標題 ü 🙂")
			tm("rename-window", "-t", "=工作🙂 ü:", "窗口 ü")
			switch locale {
			case "none":
				unsetenv(t, "LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "LC_COLLATE")
			case "C":
				unsetenv(t, "LC_ALL", "LC_CTYPE")
				t.Setenv("LANG", "C")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			r := NewRealExecutor()
			details, ok, err := r.ListSessionsDetailed(ctx)
			require.NoError(t, err)
			require.True(t, ok, "the detailed answer is trusted under locale %s", locale)
			require.Len(t, details, 3)

			// the same values the separate reads give
			plain, err := r.ListSessions(ctx)
			if locale == "UTF-8" {
				require.NoError(t, err)
				require.Len(t, plain, 3)
				for i := range plain {
					assert.Equal(t, plain[i], details[i].TmuxSession)
					md, err := r.ActivePaneMetadata(ctx, plain[i].Name)
					require.NoError(t, err)
					assert.Equal(t, md, details[i].Pane)
				}
			}
			byName := map[string]SessionDetail{}
			for _, d := range details {
				byName[d.Name] = d
			}
			assert.Equal(t, "my title", byName["alpha"].Pane.PaneTitle)
			assert.Equal(t, "sleep", byName["alpha"].Pane.PaneCurrentCommand)
			assert.Equal(t, "beta gamma", byName["beta gamma"].Name)
			// non-ASCII text arrives as it is whatever the locale (tmux would turn it into "_" in a non-UTF-8 client without -u)
			cjk, found := byName["工作🙂 ü"]
			require.True(t, found, "the session called 工作🙂 ü is listed under that name; got %v", details)
			assert.Equal(t, cjkDir, cjk.Cwd)
			assert.Equal(t, "標題 ü 🙂", cjk.Pane.PaneTitle)
			assert.Equal(t, "窗口 ü", cjk.Pane.WindowName)
			assert.Regexp(t, `^\d+:\d+$`, byName["alpha"].Instance)
			assert.Equal(t, byName["alpha"].Instance, byName["beta gamma"].Instance, "one server, one instance")
			assert.Equal(t, byName["alpha"].Instance, cjk.Instance)
			inst := tm("display-message", "-p", "#{pid}:#{start_time}")
			assert.Equal(t, inst, byName["alpha"].Instance, "the instance string config.GetTmuxInstance reads")
		})
	}
}

// With no server, the detailed read is an empty trusted list (as ListSessions is an empty list).
func TestListSessionsDetailed_NoServer(t *testing.T) {
	privateTmux(t) // a private socket directory with no server in it
	details, ok, err := NewRealExecutor().ListSessionsDetailed(context.Background())
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Empty(t, details)
}

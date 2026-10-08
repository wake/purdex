package tmux

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeFakeTmuxPlacements puts a tmux on PATH that answers only
// ListPanePlacements's exact invocation: the format string is the half of the
// separator fix the parser tests cannot see.
func writeFakeTmuxPlacements(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
if [ $# -ne 4 ] || [ "$1" != list-panes ] || [ "$2" != -a ] || [ "$3" != -F ] || [ "$4" != '#{pane_id} #{pane_pid} #{session_activity} #{session_name}' ]; then
  echo "unexpected args: $*" >&2
  exit 2
fi
printf '` + body + `'
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRealExecutorListPanePlacements_FormatAndParse(t *testing.T) {
	t.Run("good rows incl. a linked pane and a spaced name", func(t *testing.T) {
		writeFakeTmuxPlacements(t, `%%0 101 10 my work\n%%0 101 30 b\n%%3 303 5 other\n`)
		got, err := (&RealExecutor{}).ListPanePlacements(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]PanePlacement{
			"%0": {PID: "101", SessionName: "b"},
			"%3": {PID: "303", SessionName: "other"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("placements = %+v, want %+v", got, want)
		}
	})
	t.Run("a sanitized row fails the whole call", func(t *testing.T) {
		writeFakeTmuxPlacements(t, `%%0 101 10 a\n%%1_202_10_b\n`)
		got, err := (&RealExecutor{}).ListPanePlacements(context.Background())
		if err == nil || got != nil {
			t.Fatalf("got %+v, %v; want nil and an error", got, err)
		}
		if !strings.HasPrefix(err.Error(), "tmux list-panes -a: ") {
			t.Fatalf("err = %q, want the list-panes prefix", err)
		}
	})
}

// The fake answers from the maps the per-pane seams use, so a test arranged
// one way sees the same pids and names the other way.
func TestFakeExecutorListPanePlacements_MirrorsPerPaneSeams(t *testing.T) {
	f := NewFakeExecutor()
	f.SetPaneSessionName("%0", "work")
	f.SetActivePanePID("%0", "100")
	f.SetPanePID("%1", "111") // a pid alone is still a pane
	f.SetPaneSessionName("%2", "linked")
	f.SetPaneAmbiguous("%2", true)

	got, err := f.ListPanePlacements(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]PanePlacement{
		"%0": {PID: "100", SessionName: "work"},
		"%1": {PID: "111"},
		"%2": {PID: "fake-active-pid", Ambiguous: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("placements = %+v, want %+v", got, want)
	}
	for id, p := range got {
		if pid, _ := f.ActivePanePID(id); pid != p.PID {
			t.Errorf("ActivePanePID(%s) = %q, listing says %q", id, pid, p.PID)
		}
	}
}

func TestParsePanes_Good(t *testing.T) {
	got, err := parsePaneRows([]byte("%0 101 1700000000 work\n\n%3 303 1700000009 other\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []paneRow{
		{PaneID: "%0", PID: "101", Activity: 1700000000, SessionName: "work"},
		{PaneID: "%3", PID: "303", Activity: 1700000009, SessionName: "other"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
}

// A session name is the last field and may hold spaces; it is taken whole.
func TestParsePanes_SessionNameWithSpaces(t *testing.T) {
	got, err := parsePaneRows([]byte("%4 404 1700000000 my work  session\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionName != "my work  session" {
		t.Fatalf("rows = %+v, want one row named %q", got, "my work  session")
	}
}

// tmux without a UTF-8 client locale rewrites a TAB in -F output to "_"
// (alpha.340). The listing format uses spaces for that reason, but should a
// separator ever be rewritten the row collapses to one field: the whole
// listing must fail (so the caller falls back to per-pane lookups) rather
// than yield rows with a wrong pid or session name.
func TestParsePanes_SeparatorSanitizedToUnderscore(t *testing.T) {
	out := "%0 101 1700000000 work\n%1_202_1700000000_other\n"
	got, err := parsePaneRows([]byte(out))
	if err == nil {
		t.Fatalf("parsePaneRows accepted a sanitized row: %+v", got)
	}
	if got != nil {
		t.Fatalf("a failed listing must return no rows, got %+v", got)
	}
	if !strings.Contains(err.Error(), "%1_202_1700000000_other") {
		t.Fatalf("error does not name the bad row: %v", err)
	}
}

func TestParsePanes_MalformedRowFailsListing(t *testing.T) {
	for name, out := range map[string]string{
		"missing session name": "%0 101 1700000000\n",
		"non-numeric pid":      "%0 abc 1700000000 work\n",
		"non-numeric activity": "%0 101 yesterday work\n",
		"pane id without %":    "0 101 1700000000 work\n",
		"blank session name":   "%0 101 1700000000   \n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := parsePaneRows([]byte(out)); err == nil {
				t.Fatalf("parsePaneRows(%q) = %+v, want an error", out, got)
			}
		})
	}
}

// A pane in one session resolves to that session's name and its pid.
func TestPanePlacements_SingleSession(t *testing.T) {
	got := buildPanePlacements([]paneRow{
		{PaneID: "%1", PID: "11", Activity: 5, SessionName: "a"},
		{PaneID: "%2", PID: "22", Activity: 9, SessionName: "b"},
	})
	want := map[string]PanePlacement{
		"%1": {PID: "11", SessionName: "a"},
		"%2": {PID: "22", SessionName: "b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("placements = %+v, want %+v", got, want)
	}
}

// linked windows: `list-panes -a` prints a pane once per session that links
// it, and `display-message -t %N '#{session_name}'` answers with the session
// of the newest activity (cmd_find_best_session; verified on tmux 3.6a with
// an isolated server: creation order, an attached client and the name order
// each moved the answer exactly as session_activity predicts). The
// listing-order of the rows is NOT the rule: here the first row is the oldest.
func TestPanePlacements_LinkedWindow_NewestActivityWins(t *testing.T) {
	got := buildPanePlacements([]paneRow{
		{PaneID: "%0", PID: "7", Activity: 100, SessionName: "a"},
		{PaneID: "%0", PID: "7", Activity: 300, SessionName: "b"},
		{PaneID: "%0", PID: "7", Activity: 200, SessionName: "c"},
	})
	want := PanePlacement{PID: "7", SessionName: "b"}
	if got["%0"] != want {
		t.Fatalf("placement = %+v, want %+v", got["%0"], want)
	}
	// and the other way round, so a rule of "first row" or "last row" fails too
	got = buildPanePlacements([]paneRow{
		{PaneID: "%0", PID: "7", Activity: 900, SessionName: "a"},
		{PaneID: "%0", PID: "7", Activity: 300, SessionName: "b"},
	})
	if want := (PanePlacement{PID: "7", SessionName: "a"}); got["%0"] != want {
		t.Fatalf("placement = %+v, want %+v", got["%0"], want)
	}
}

// session_activity has one-second resolution while tmux compares microseconds,
// so two sessions that tie on the printed value cannot be told apart from the
// listing. The placement says so rather than guessing, and the caller asks
// tmux about that one pane.
func TestPanePlacements_LinkedWindow_TieIsAmbiguous(t *testing.T) {
	got := buildPanePlacements([]paneRow{
		{PaneID: "%0", PID: "7", Activity: 300, SessionName: "a"},
		{PaneID: "%0", PID: "7", Activity: 300, SessionName: "b"},
		{PaneID: "%0", PID: "7", Activity: 100, SessionName: "c"},
	})
	want := PanePlacement{PID: "7", Ambiguous: true}
	if got["%0"] != want {
		t.Fatalf("placement = %+v, want %+v", got["%0"], want)
	}
}

// The same window linked twice into one session lists the pane twice under one
// name: nothing is ambiguous about that.
func TestPanePlacements_SameSessionTwiceIsNotAmbiguous(t *testing.T) {
	got := buildPanePlacements([]paneRow{
		{PaneID: "%0", PID: "7", Activity: 300, SessionName: "a"},
		{PaneID: "%0", PID: "7", Activity: 300, SessionName: "a"},
	})
	if want := (PanePlacement{PID: "7", SessionName: "a"}); got["%0"] != want {
		t.Fatalf("placement = %+v, want %+v", got["%0"], want)
	}
}

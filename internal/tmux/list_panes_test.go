package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParsePaneLocations(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []PaneLocation
	}{
		{
			name: "good lines with a trailing newline",
			out:  "%0 $0 101\n%3 $1 303\n",
			want: []PaneLocation{
				{PaneID: "%0", SessionID: "$0", PanePID: "101"},
				{PaneID: "%3", SessionID: "$1", PanePID: "303"},
			},
		},
		{
			name: "no trailing newline",
			out:  "%0 $0 101",
			want: []PaneLocation{{PaneID: "%0", SessionID: "$0", PanePID: "101"}},
		},
		{
			name: "short line is skipped, not half-filled",
			out:  "%0 $0 101\n%1 $0\n%2 $1 202\n",
			want: []PaneLocation{
				{PaneID: "%0", SessionID: "$0", PanePID: "101"},
				{PaneID: "%2", SessionID: "$1", PanePID: "202"},
			},
		},
		{
			name: "blank line is skipped",
			out:  "%0 $0 101\n\n%2 $1 202\n",
			want: []PaneLocation{
				{PaneID: "%0", SessionID: "$0", PanePID: "101"},
				{PaneID: "%2", SessionID: "$1", PanePID: "202"},
			},
		},
		{
			name: "line with four fields is skipped",
			out:  "%0 $0 101 extra\n%2 $1 202\n",
			want: []PaneLocation{{PaneID: "%2", SessionID: "$1", PanePID: "202"}},
		},
		{
			name: "empty output",
			out:  "",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePaneLocations([]byte(tc.out))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parsePaneLocations(%q) = %+v, want %+v", tc.out, got, tc.want)
			}
		})
	}
}

// The format string is the half of the TAB-vs-locale fix the parser test
// cannot see: a fake tmux on PATH answers only the exact invocation.
func TestRealExecutorListAllPanes_FormatAndParse(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
if [ $# -ne 4 ] || [ "$1" != list-panes ] || [ "$2" != -a ] || [ "$3" != -F ] || [ "$4" != '#{pane_id} #{session_id} #{pane_pid}' ]; then
  echo "unexpected args: $*" >&2
  exit 2
fi
printf '%%0 $0 101\n%%3 $1 303\n'
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := (&RealExecutor{}).ListAllPanes(context.Background())
	if err != nil {
		t.Fatalf("ListAllPanes: %v", err)
	}
	want := []PaneLocation{
		{PaneID: "%0", SessionID: "$0", PanePID: "101"},
		{PaneID: "%3", SessionID: "$1", PanePID: "303"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListAllPanes = %+v, want %+v", got, want)
	}
}

// The fake's listing is built from the same maps PaneSessionID and
// ActivePanePID answer from, so a test that arranges panes one way sees the
// same panes the other way.
func TestFakeExecutorListAllPanes_MirrorsPerPaneSeams(t *testing.T) {
	f := NewFakeExecutor()
	f.SetPaneSessionID("%2", "$1")
	f.SetPaneSessionID("%0", "$0")
	f.SetPaneSessionID("%1", "$0")
	f.SetPanePID("%0", "100")       // panePIDs only
	f.SetActivePanePID("%1", "111") // activePanePIDs wins over panePIDs
	f.SetPanePID("%1", "999")
	// %2 has neither: the ActivePanePID default.
	f.SetPanePID("%9", "900") // a pid alone is not a pane in the listing

	ctx := context.Background()
	got, err := f.ListAllPanes(ctx)
	if err != nil {
		t.Fatalf("ListAllPanes: %v", err)
	}
	want := []PaneLocation{
		{PaneID: "%0", SessionID: "$0", PanePID: "100"},
		{PaneID: "%1", SessionID: "$0", PanePID: "111"},
		{PaneID: "%2", SessionID: "$1", PanePID: "fake-active-pid"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListAllPanes = %+v, want %+v", got, want)
	}
	for _, p := range got {
		sid, err := f.PaneSessionID(ctx, p.PaneID)
		if err != nil || sid != p.SessionID {
			t.Errorf("PaneSessionID(%s) = %q, %v; listing says %q", p.PaneID, sid, err, p.SessionID)
		}
		pid, _ := f.ActivePanePID(p.PaneID)
		if pid != p.PanePID {
			t.Errorf("ActivePanePID(%s) = %q; listing says %q", p.PaneID, pid, p.PanePID)
		}
	}

	f.ForgetPaneSessionID("%1")
	got, err = f.ListAllPanes(ctx)
	if err != nil {
		t.Fatalf("ListAllPanes after forget: %v", err)
	}
	want = []PaneLocation{
		{PaneID: "%0", SessionID: "$0", PanePID: "100"},
		{PaneID: "%2", SessionID: "$1", PanePID: "fake-active-pid"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after ForgetPaneSessionID(%%1): ListAllPanes = %+v, want %+v", got, want)
	}
}

// Map iteration order is random, so one lucky run proves nothing: list many
// panes, many times.
func TestFakeExecutorListAllPanes_SortedByPaneID(t *testing.T) {
	f := NewFakeExecutor()
	ids := []string{"%e", "%a", "%d", "%b", "%h", "%c", "%g", "%f"}
	for _, id := range ids {
		f.SetPaneSessionID(id, "$0")
	}
	want := []string{"%a", "%b", "%c", "%d", "%e", "%f", "%g", "%h"}
	for range 20 {
		got, err := f.ListAllPanes(context.Background())
		if err != nil {
			t.Fatalf("ListAllPanes: %v", err)
		}
		gotIDs := make([]string, len(got))
		for i, p := range got {
			gotIDs[i] = p.PaneID
		}
		if !reflect.DeepEqual(gotIDs, want) {
			t.Fatalf("pane ids = %v, want %v", gotIDs, want)
		}
	}
}

func TestFakeExecutorListAllPanes_ExpiredContext(t *testing.T) {
	f := NewFakeExecutor()
	f.SetPaneSessionID("%0", "$0")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()

	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"canceled", canceled, context.Canceled},
		{"deadline passed", expired, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.ListAllPanes(tc.ctx)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got != nil {
				t.Fatalf("rows = %+v, want none", got)
			}
		})
	}
}

func TestFakeExecutorListAllPanes_ErrorKnob(t *testing.T) {
	f := NewFakeExecutor()
	f.SetPaneSessionID("%0", "$0")
	boom := errors.New("tmux hiccup")

	f.SetListAllPanesError(boom)
	got, err := f.ListAllPanes(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if got != nil {
		t.Fatalf("rows = %+v, want none", got)
	}

	f.SetListAllPanesError(nil)
	got, err = f.ListAllPanes(context.Background())
	if err != nil {
		t.Fatalf("after clearing the knob: %v", err)
	}
	if len(got) != 1 || got[0].PaneID != "%0" {
		t.Fatalf("after clearing the knob: rows = %+v, want %%0", got)
	}
}

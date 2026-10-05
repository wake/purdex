package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
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
			name: "blank lines are skipped",
			out:  "%0 $0 101\n\n%2 $1 202\n\n",
			want: []PaneLocation{
				{PaneID: "%0", SessionID: "$0", PanePID: "101"},
				{PaneID: "%2", SessionID: "$1", PanePID: "202"},
			},
		},
		{
			name: "empty output",
			out:  "",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePaneLocations([]byte(tc.out))
			if err != nil {
				t.Fatalf("parsePaneLocations(%q): %v", tc.out, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parsePaneLocations(%q) = %+v, want %+v", tc.out, got, tc.want)
			}
		})
	}
}

// Skipping a row the parser cannot read would hand the owner pass a listing
// that is missing that pane, which it reads as "gone" and drops the owner,
// where the truth is "unresolved" (spec D5). So any such row fails the listing.
func TestParsePaneLocations_MalformedRowFailsListing(t *testing.T) {
	cases := []struct {
		name string
		out  string
		bad  string // the row the error must name
	}{
		{"short line", "%0 $0 101\n%1 $0\n%2 $1 202\n", "%1 $0"},
		{"four fields", "%0 $0 101 extra\n%2 $1 202\n", "%0 $0 101 extra"},
		{"swapped pane and session ids", "%0 $0 101\n$0 %5 123\n", "$0 %5 123"},
		{"non-numeric pid", "%0 $0 101\n%1 $0 abc\n", "%1 $0 abc"},
		{"pane id without %", "0 $0 101\n", "0 $0 101"},
		{"session id without $", "%0 0 101\n", "%0 0 101"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePaneLocations([]byte(tc.out))
			if err == nil {
				t.Fatalf("parsePaneLocations(%q) = %+v, nil; want an error", tc.out, got)
			}
			if got != nil {
				t.Fatalf("rows = %+v, want none alongside the error", got)
			}
			if want := strconv.Quote(tc.bad); !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %q, want it to name the row %s", err, want)
			}
		})
	}

	t.Run("a long row is quoted truncated", func(t *testing.T) {
		long := "%0 $0 " + strings.Repeat("9x", 500)
		_, err := parsePaneLocations([]byte(long + "\n"))
		if err == nil {
			t.Fatal("want an error")
		}
		if !strings.Contains(err.Error(), `"%0 $0 9x9x`) {
			t.Fatalf("err = %q, want it to quote the row's start", err)
		}
		if len(err.Error()) > 200 {
			t.Fatalf("err is %d bytes, want the row truncated: %q", len(err.Error()), err)
		}
	})
}

// writeFakeTmux puts a tmux on PATH that answers only ListAllPanes's exact
// invocation, printing body (a printf format) to stdout.
func writeFakeTmux(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
if [ $# -ne 4 ] || [ "$1" != list-panes ] || [ "$2" != -a ] || [ "$3" != -F ] || [ "$4" != '#{pane_id} #{session_id} #{pane_pid}' ]; then
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

// The format string is the half of the TAB-vs-locale fix the parser test
// cannot see: a fake tmux on PATH answers only the exact invocation.
func TestRealExecutorListAllPanes_FormatAndParse(t *testing.T) {
	t.Run("good rows", func(t *testing.T) {
		writeFakeTmux(t, `%%0 $0 101\n%%3 $1 303\n`)
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
	})

	t.Run("a malformed row fails the listing", func(t *testing.T) {
		writeFakeTmux(t, `%%0 $0 101\n%%1 $0\n`)
		got, err := (&RealExecutor{}).ListAllPanes(context.Background())
		if err == nil {
			t.Fatalf("ListAllPanes = %+v, nil; want an error", got)
		}
		if got != nil {
			t.Fatalf("rows = %+v, want none alongside the error", got)
		}
		if !strings.HasPrefix(err.Error(), "tmux list-panes -a: ") || !strings.Contains(err.Error(), `"%1 $0"`) {
			t.Fatalf("err = %q, want the list-panes prefix and the bad row", err)
		}
	})
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

// The real listing is a bounded read, so a deadline that passes while it waits
// on tmux ends it. A deadline can also pass while the fake waits for its lock,
// and that must end the call too, not yield a listing the caller has stopped
// waiting for.
func TestFakeExecutorListAllPanes_DeadlinePassesWhileWaitingForLock(t *testing.T) {
	f := NewFakeExecutor()
	f.SetPaneSessionID("%0", "$0")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	type result struct {
		rows []PaneLocation
		err  error
	}
	done := make(chan result, 1)
	f.mu.Lock()
	go func() {
		rows, err := f.ListAllPanes(ctx)
		done <- result{rows, err}
	}()
	<-ctx.Done()
	f.mu.Unlock()

	select {
	case r := <-done:
		if !errors.Is(r.err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want %v", r.err, context.DeadlineExceeded)
		}
		if r.rows != nil {
			t.Fatalf("rows = %+v, want none", r.rows)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListAllPanes did not return after the lock was released")
	}
}

// A listing that hangs in flight is the case the real executor's bounded read
// ends at the deadline, and the one a caller's tests need to stage ("listing 2
// blocks until the request deadline"). The read hook is the fake's only place a
// call can hang, so a parked listing must end with its context.
func TestFakeExecutorListAllPanes_HungListingEndsAtDeadline(t *testing.T) {
	f := NewFakeExecutor()
	f.SetPaneSessionID("%0", "$0")
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f.SetReadHook(BlockReadsUntil(release, func(op ReadOp, _ string) bool { return op == ReadListAllPanes }))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	type result struct {
		rows    []PaneLocation
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		rows, err := f.ListAllPanes(ctx)
		done <- result{rows, err, time.Since(start)}
	}()

	select {
	case r := <-done:
		if !errors.Is(r.err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want %v", r.err, context.DeadlineExceeded)
		}
		if r.rows != nil {
			t.Fatalf("rows = %+v, want none", r.rows)
		}
		if r.elapsed > time.Second {
			t.Fatalf("returned after %v, want it to end at the 50ms deadline", r.elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListAllPanes outlived its deadline: the parked listing never ended")
	}
}

// A test that parks reads by op must be able to single out the listing, and a
// hook that lets the read through must not cost the caller its rows.
func TestFakeExecutorListAllPanes_ReadHookSeesOp(t *testing.T) {
	f := NewFakeExecutor()
	f.SetPaneSessionID("%0", "$0")
	type call struct {
		op     ReadOp
		target string
	}
	var calls []call
	f.SetReadHook(func(_ context.Context, op ReadOp, target string) error {
		calls = append(calls, call{op, target})
		return nil
	})

	got, err := f.ListAllPanes(context.Background())
	if err != nil {
		t.Fatalf("ListAllPanes: %v", err)
	}
	if len(got) != 1 || got[0].PaneID != "%0" {
		t.Fatalf("rows = %+v, want %%0", got)
	}
	if want := []call{{ReadListAllPanes, ""}}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("read hook calls = %+v, want %+v", calls, want)
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

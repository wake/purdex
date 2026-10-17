package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/resources"
)

// fakeResourcesDaemon speaks GET /api/resources, answering with res (a plain 404 when res is empty, or a
// handler), and hands every other route to next. Requests to the resources
// route are recorded.
type fakeResourcesDaemon struct {
	next http.Handler
	res  answer
	// serve, when set, answers the resources route instead of res.
	serve func(w http.ResponseWriter, r *http.Request)

	mu      sync.Mutex
	queries []string
}

func (f *fakeResourcesDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/resources" {
		if f.next == nil {
			http.NotFound(w, r)
			return
		}
		f.next.ServeHTTP(w, r)
		return
	}
	f.mu.Lock()
	f.queries = append(f.queries, r.URL.RawQuery)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case f.serve != nil:
		f.serve(w, r)
	case f.res.status == 0 && f.res.body == nil:
		http.NotFound(w, r) // an older daemon: no such route
	default:
		write(w, f.res)
	}
}

func (f *fakeResourcesDaemon) hits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queries)
}

// fakeSnapshot is a busy host (load 17.59 of 10 cores, memory pressure warn)
// with two sessions; the first belongs to fakeMember (cc-sid-member-1).
func fakeSnapshot() resources.Snapshot {
	return resources.Snapshot{
		Available: true,
		Capacity:  resources.Capacity,
		Mode:      resources.ModeMeasure,
		Host: resources.HostUse{
			Measured: 76, CPU: 175.9, Mem: 75.4, Load1: 17.59, NCPU: 10,
			MemBytes: 17179869184, Pressure: 2, MemorystatusLevel: 48, Full: true,
		},
		Sessions: []resources.SessionUse{
			{SessionID: "cc-sid-member-1", PID: 4242, Tmux: "mt1:@2.%3", CPU: 12.5, Mem: 3.5, Use: 13,
				RSSBytes: 600000000, Pcpu: 125, Procs: 6},
			{SessionID: "0123456789abcdef", PID: 77, CPU: 0.4, Mem: 1.2, Use: 2, RSSBytes: 3 << 30, Pcpu: 4, Procs: 1},
		},
	}
}

func fieldsOf(line string) string { return strings.Join(strings.Fields(line), " ") }

// WWD-0a: the removed verbs say so and exit 2 without touching the daemon; so does anything unknown. `report` still points at
// pdx host report.
func TestLeaseCmd_RemovedVerbsAndUsageExit2(t *testing.T) {
	d := &fakeResourcesDaemon{res: answer{body: fakeSnapshot()}}
	for _, args := range [][]string{nil, {"bogus"}, {"--json"}, {"ls"}, {"ls", "--json"}, {"acquire", "--kind", "build"}, {"release", "x"}, {"confirm", "x"}, {"cancel", "x"}, {"done"}} {
		code, stdout, stderr := driveTeamCmdWith(t, runLeaseCmd, d, fakeGetenv(nil), nil, args...)
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "pdx lease: ") {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
		if len(args) > 0 && args[0] != "bogus" && args[0] != "--json" && !strings.Contains(stderr, "已移除") {
			t.Errorf("%q: stderr does not say the verb was removed: %q", args, stderr)
		}
	}
	if d.hits() != 0 {
		t.Errorf("removed verbs reached the daemon %d time(s)", d.hits())
	}
}

// `run` is what is left: it reaches the alias (seam), with the arguments after it.
func TestLeaseCmd_RunStillDispatchesToTheAlias(t *testing.T) {
	var got []string
	defer func(old func(context.Context, []string, func(string) string, io.Writer, io.Writer, []daemonclient.Option) int) {
		leaseRun = old
	}(leaseRun)
	leaseRun = func(_ context.Context, args []string, _ func(string) string, _, _ io.Writer, _ []daemonclient.Option) int {
		got = args
		return 7
	}
	code, _, _ := driveTeamCmdWith(t, runLeaseCmd, &fakeResourcesDaemon{}, fakeGetenv(nil), nil, "run", "--kind", "build", "--", "true")
	if code != 7 || !strings.HasPrefix(strings.Join(got, " "), "--kind build -- true") {
		t.Fatalf("code=%d args=%q", code, got)
	}
}

// main.go dispatches `lease` to runLease and the Commands line names it.
func TestDispatch_Lease(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "case \"lease\":\n\t\trunLease(os.Args[2:])\n") {
		t.Error("main.go does not dispatch \"lease\" to runLease")
	}
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "Commands:") && !strings.Contains(l, " lease,") {
			t.Errorf("the Commands line does not list lease: %s", l)
		}
	}
}

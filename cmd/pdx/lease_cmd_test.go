package main

import (
	"encoding/json"
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

// `pdx lease ls` needs no inbox: this env has none and the command works.
func TestLeaseLs_Table(t *testing.T) {
	d := &fakeResourcesDaemon{res: answer{body: fakeSnapshot()}}
	code, stdout, stderr := driveTeamCmdWith(t, runLeaseCmd, d, fakeGetenv(nil), nil, "ls")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("stdout = %q, want the host line, a header and two rows", stdout)
	}
	if got, want := fieldsOf(lines[0]), "host 76/100 load 17.59/10 mem 75% pressure warn FULL"; got != want {
		t.Errorf("host line = %q, want %q", got, want)
	}
	if got, want := fieldsOf(lines[1]), "SESSION PID CPU MEM RSS PROCS TMUX"; got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
	if got, want := fieldsOf(lines[2]), "cc-sid-m 4242 12.5% 3.5% 572M 6 mt1:@2.%3"; got != want {
		t.Errorf("row 1 = %q, want %q", got, want)
	}
	if got, want := fieldsOf(lines[3]), "01234567 77 0.4% 1.2% 3.0G 1 -"; got != want {
		t.Errorf("row 2 = %q, want %q", got, want)
	}
}

func TestLeaseLs_HostLineVariants(t *testing.T) {
	idle := fakeSnapshot()
	idle.Host = resources.HostUse{Measured: 12, CPU: 10, Mem: 11.6, Load1: 1, NCPU: 10, Pressure: 1}
	idle.Sessions = nil
	d := &fakeResourcesDaemon{res: answer{body: idle}}
	_, stdout, _ := driveTeamCmdWith(t, runLeaseCmd, d, fakeGetenv(nil), nil, "ls")
	if got, want := fieldsOf(strings.SplitN(stdout, "\n", 2)[0]), "host 12/100 load 1.00/10 mem 12% pressure normal"; got != want {
		t.Errorf("host line = %q, want %q", got, want)
	}

	unknown := idle
	unknown.Host.Pressure = 0
	d = &fakeResourcesDaemon{res: answer{body: unknown}}
	_, stdout, _ = driveTeamCmdWith(t, runLeaseCmd, d, fakeGetenv(nil), nil, "ls")
	if !strings.Contains(strings.SplitN(stdout, "\n", 2)[0], "pressure -") {
		t.Errorf("host line = %q, want pressure -", stdout)
	}

	// Not available: one line with the reason, exit 0 (it is an answer).
	d = &fakeResourcesDaemon{res: answer{body: resources.Snapshot{Reason: resources.ReasonWarmingUp, Capacity: 100}}}
	code, stdout, stderr := driveTeamCmdWith(t, runLeaseCmd, d, fakeGetenv(nil), nil, "ls")
	if code != ExitOK || stderr != "" || fieldsOf(stdout) != "host unavailable (warming_up)" {
		t.Errorf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// An older daemon has no route: one stderr line ending in the code, exit 1.
func TestLeaseLs_OldDaemon404(t *testing.T) {
	d := &fakeResourcesDaemon{} // plain 404 for everything
	code, stdout, stderr := driveTeamCmdWith(t, runLeaseCmd, d, fakeGetenv(nil), []daemonclient.Option{leadClockOpt()}, "ls")
	if code != ExitError || stdout != "" || lastToken(stderr) != "unsupported" ||
		!strings.Contains(stderr, "/api/resources") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestLeaseLs_JSON(t *testing.T) {
	want := fakeSnapshot()
	d := &fakeResourcesDaemon{res: answer{body: want}}
	code, stdout, stderr := driveTeamCmdWith(t, runLeaseCmd, d, fakeGetenv(nil), nil, "ls", "--json")
	if code != ExitOK || stderr != "" || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var got resources.Snapshot
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not a snapshot: %v", err)
	}
	if got.Host.Measured != 76 || len(got.Sessions) != 2 || got.Sessions[0].SessionID != "cc-sid-member-1" {
		t.Errorf("snapshot = %+v", got)
	}
	if strings.Contains(stdout, "\n ") || strings.Contains(strings.TrimRight(stdout, "\n"), "\n") {
		t.Errorf("--json must be compact: %q", stdout)
	}
}

func TestLeaseCmd_UsageErrorsExit2(t *testing.T) {
	d := &fakeResourcesDaemon{res: answer{body: fakeSnapshot()}}
	for _, args := range [][]string{nil, {"bogus"}, {"ls", "extra"}, {"ls", "--bogus"}, {"--json"}} {
		code, stdout, stderr := driveTeamCmdWith(t, runLeaseCmd, d, fakeGetenv(nil), nil, args...)
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "pdx lease: ") || !strings.Contains(stderr, "usage: pdx lease ls") {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	if d.hits() != 0 {
		t.Errorf("usage errors reached the daemon %d time(s)", d.hits())
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

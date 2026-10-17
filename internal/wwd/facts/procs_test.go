package facts

import (
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func mb(n uint64) uint64 { return n << 20 }

// A small host: init, a Claude Code session (pid 100, ref _abc123) with a codex
// run, a vite helper and two MCP servers under it, a wwd job tree (pid 300
// carries WWD_JOB), an orphaned playwright, and a root daemon.
func fixtureSnapshot() (ProcSnapshot, ProcContext) {
	snap := ProcSnapshot{Procs: []ProcRec{
		{PID: 1, PPID: 0, UID: 0, Name: "launchd", FootprintBytes: mb(20)},
		{PID: 100, PPID: 1, UID: 501, Name: "claude", FootprintBytes: mb(900), CPUPct: 5},
		{PID: 110, PPID: 100, UID: 501, Name: "codex", FootprintBytes: mb(300), CPUPct: 40},
		{PID: 111, PPID: 100, UID: 501, Name: "node", FootprintBytes: mb(250), CPUPct: 1},   // vite
		{PID: 112, PPID: 100, UID: 501, Name: "python3", FootprintBytes: mb(80), CPUPct: 0}, // mcp
		{PID: 113, PPID: 100, UID: 501, Name: "node", FootprintBytes: mb(70), CPUPct: 0},    // mcp
		{PID: 120, PPID: 110, UID: 501, Name: "codex-broker", FootprintBytes: mb(60)},
		{PID: 300, PPID: 1, UID: 501, Name: "pnpm", FootprintBytes: mb(50), CPUPct: 10},
		{PID: 301, PPID: 300, UID: 501, Name: "node", FootprintBytes: mb(1500), CPUPct: 88}, // inherits WWD_JOB
		{PID: 303, PPID: 301, UID: 501, Name: "node", FootprintBytes: mb(5)},                // a vite a job started: the job's, not a resident
		{PID: 302, PPID: 301, UID: 501, Name: "sleep", FootprintBytes: mb(1)},               // a system binary: the kernel hides its environment
		{PID: 400, PPID: 1, UID: 501, Name: "node", FootprintBytes: mb(200), CPUPct: 0},     // orphan playwright
		{PID: 500, PPID: 1, UID: 0, Name: "mds_stores", FootprintBytes: mb(400), CPUPct: 12},
		{PID: 600, PPID: 1, UID: 501, Name: "Safari", FootprintBytes: mb(700), CPUPct: 3},
	}}
	args := map[int][]string{
		110: {"codex", "exec"},
		111: {"node", "/x/node_modules/.bin/vite", "--port", "5174"},
		112: {"python3", "-m", "workspace-mcp"},
		113: {"node", "/x/mcp-server.js"},
		120: {"codex-broker"},
		400: {"node", "/x/playwright/cli.js", "mcp"},
		303: {"node", "/x/node_modules/.bin/vite"},
	}
	ctx := ProcContext{
		JobOf: func(pid int) (string, bool) {
			if pid == 300 || pid == 301 {
				return "j_01JC", true
			}
			return "", false
		},
		SessionOf: func(pid int) (string, bool) {
			if pid == 100 {
				return "_abc123", true
			}
			return "", false
		},
		ArgsOf: func(pid int) []string { return args[pid] },
	}
	return snap, ctx
}

func TestSelectTopIsUnionOfMemAndCPU(t *testing.T) {
	snap, _ := fixtureSnapshot()
	got := SelectTop(snap.Procs, 2, 2)
	pids := map[int]bool{}
	for _, p := range got {
		pids[p.PID] = true
	}
	// top 2 by footprint: 301 (1500), 100 (900); top 2 by cpu: 301 (88), 110 (40)
	for _, want := range []int{301, 100, 110} {
		if !pids[want] {
			t.Errorf("pid %d missing from %v", want, pids)
		}
	}
	if len(got) != 3 {
		t.Errorf("union has %d entries, want 3 (301 is in both lists once)", len(got))
	}
	// default sizes are 12 and 12
	if got := SelectTop(snap.Procs, 12, 12); len(got) != len(snap.Procs) {
		t.Errorf("fewer procs than the limit keeps them all, got %d", len(got))
	}
}

func TestAttributeOwners(t *testing.T) {
	snap, ctx := fixtureSnapshot()
	byPID := map[int]ProcRec{}
	for _, p := range snap.Procs {
		byPID[p.PID] = p
	}
	ctx.Table = byPID
	cases := []struct {
		pid  int
		want *wire.ProcOwner
	}{
		{301, &wire.ProcOwner{Kind: wire.OwnerJob, ID: "j_01JC"}},
		{300, &wire.ProcOwner{Kind: wire.OwnerJob, ID: "j_01JC"}},
		{303, &wire.ProcOwner{Kind: wire.OwnerJob, ID: "j_01JC"}},
		{302, &wire.ProcOwner{Kind: wire.OwnerJob, ID: "j_01JC"}}, // no marker of its own: a job ancestor decides
		{100, &wire.ProcOwner{Kind: wire.OwnerSession, Session: "_abc123"}},
		{110, &wire.ProcOwner{Kind: wire.OwnerCodex, Session: "_abc123"}},
		{120, &wire.ProcOwner{Kind: wire.OwnerResident, Session: "_abc123"}}, // the broker is a long-lived helper, not a run
		{111, &wire.ProcOwner{Kind: wire.OwnerResident, Session: "_abc123"}},
		{112, &wire.ProcOwner{Kind: wire.OwnerResident, Session: "_abc123"}},
		{400, &wire.ProcOwner{Kind: wire.OwnerResident, Session: "orphan"}},
		{500, &wire.ProcOwner{Kind: wire.OwnerSystem}},
		{1, &wire.ProcOwner{Kind: wire.OwnerSystem}},
		{600, nil}, // a user app nobody can attribute: null, not a guess
	}
	for _, c := range cases {
		got := Attribute(byPID[c.pid], ctx)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("pid %d (%s): got %+v want %+v", c.pid, byPID[c.pid].Name, got, c.want)
		}
	}
}

func TestBuildProcsWireShape(t *testing.T) {
	snap, ctx := fixtureSnapshot()
	procs := BuildProcs(snap, ctx, 2, 2)
	if len(procs) != 3 {
		t.Fatalf("%d procs", len(procs))
	}
	for _, p := range procs {
		if p.MemKind != "footprint" {
			t.Errorf("pid %d mem_kind %q", p.PID, p.MemKind)
		}
		if p.PID == 301 {
			if p.MemMB != 1500 || p.CPUPct != 88 || p.Name != "node" || p.Owner == nil || p.Owner.Kind != wire.OwnerJob {
				t.Errorf("301: %+v", p)
			}
		}
	}
	// the result is valid wire data
	f := wire.Facts{V: wire.Proto, Procs: procs, Derived: wire.Derived{Zone: wire.ZoneNormal}}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	if BuildProcs(ProcSnapshot{}, ProcContext{}, 12, 12) == nil {
		t.Error("no processes must be [] not null")
	}
}

func TestBuildResidents(t *testing.T) {
	snap, ctx := fixtureSnapshot()
	got := BuildResidents(snap, ctx)
	want := []wire.Resident{
		{Session: "_abc123", Kind: "codex-broker", Count: 1, MemMB: 60},
		{Session: "_abc123", Kind: "mcp", Count: 2, MemMB: 150},
		{Session: "_abc123", Kind: "vite", Count: 1, MemMB: 250},
		{Session: "orphan", Kind: "playwright", Count: 1, MemMB: 200},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if BuildResidents(ProcSnapshot{}, ProcContext{}) == nil {
		t.Error("no residents must be [] not null")
	}
}

func TestJobMarkerParse(t *testing.T) {
	env := []string{"HOME=/x", "WWD_JOB=j_01ABC", "PATH=/bin"}
	if id, ok := JobFromEnv(env); !ok || id != "j_01ABC" {
		t.Fatalf("%q %v", id, ok)
	}
	for _, bad := range [][]string{nil, {"WWD_JOB="}, {"XWWD_JOB=j_1"}, {"WWD_JOBS=j_1"}} {
		if id, ok := JobFromEnv(bad); ok {
			t.Errorf("%v accepted as %q", bad, id)
		}
	}
}

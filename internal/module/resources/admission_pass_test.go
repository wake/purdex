package resourcesmod

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// passFix is a fixture in mode lease with a published, available sample.
type passFix struct{ *useFix }

func newPassFix(t *testing.T, mode string) *passFix {
	f := &passFix{newUseFix(t)}
	f.set.set(resources.Settings{Mode: mode})
	f.setHost(resources.HostUse{Measured: 10, NCPU: 10, Load1: 1})
	return f
}

func (f *passFix) setHost(h resources.HostUse) {
	f.m.publish(&resources.Snapshot{Available: true, Capacity: resources.Capacity, Host: h, Mode: resources.ModeLease})
}

// queue creates a waiting row created one second ago whose deadline is in
// `deadline`.
func (f *passFix) queue(id string, weight int, deadline time.Duration) leaseRow {
	f.t.Helper()
	r := baseRow(id, "c-"+id)
	r.Weight = weight
	r.CreatedAt = f.nowMS() - 1000
	r.DeadlineAt = f.nowMS() + deadline.Milliseconds()
	r.LeaseUntil = f.nowMS() + 30000
	return mustCreate(f.t, f.m.store, r)
}

type decRow struct {
	recorded, full, wouldR2 int
	path                    string
	weight, ncpu            int
	sumCharge, unleased     float64
	load1                   float64
}

func (f *passFix) dec(id string) decRow {
	f.t.Helper()
	var d decRow
	err := f.m.store.db.QueryRow(`SELECT dec_recorded, dec_full, would_wait_r2, dec_path, dec_weight, dec_ncpu, dec_sum_charge, dec_unleased, dec_load1
		FROM resource_leases WHERE id = ?`, id).Scan(&d.recorded, &d.full, &d.wouldR2, &d.path, &d.weight, &d.ncpu, &d.sumCharge, &d.unleased, &d.load1)
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

func (f *passFix) state(id string) string { return f.row(id).State }

// A fitting waiter is granted by the pass, and the grant's own write carries
// the decision: the host state it saw, its path and weight.
func TestPass_GrantsAFittingWaiterAndRecordsTheDecision(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.queue("a", 35, 5*time.Minute)
	if res := f.m.admissionPass(context.Background(), ""); res.granted != 1 {
		t.Fatalf("granted %d", res.granted)
	}
	r := f.row("a")
	if r.State != "held" || r.GrantedAt == 0 || r.Overrun || r.WaitedMS != 1000 {
		t.Fatalf("row = %+v", r)
	}
	d := f.dec("a")
	if d.recorded != 1 || d.path != resources.PathWaited || d.weight != 35 || d.ncpu != 10 || d.load1 != 1 || d.full != 0 || d.wouldR2 != 0 || d.sumCharge != 0 {
		t.Errorf("decision = %+v", d)
	}
}

// The request the pass was started for is recorded as immediate.
func TestPass_FreshRequestIsImmediate(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "a")
	if d := f.dec("a"); d.path != resources.PathImmediate {
		t.Errorf("path = %q", d.path)
	}
}

// Three requests of 35: two fit (the second sees the first's weight in the
// sum, which its decision records), the third waits.
func TestPass_SecondSeesTheFirstsWeight(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.queue("a", 35, 5*time.Minute)
	f.queue("b", 35, 5*time.Minute)
	f.queue("c", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	if f.state("a") != "held" || f.state("b") != "held" || f.state("c") != "waiting" {
		t.Fatalf("states %s %s %s", f.state("a"), f.state("b"), f.state("c"))
	}
	if d := f.dec("b"); d.sumCharge != 35 {
		t.Errorf("b saw sum %v, want 35", d.sumCharge)
	}
}

// A full host keeps a fitting request waiting; when it stops being full the
// next pass grants it.
func TestPass_FullHostWaitsThenGrants(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.setHost(resources.HostUse{Measured: 95, Full: true, NCPU: 10})
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	if f.state("a") != "waiting" {
		t.Fatal("granted on a full host")
	}
	f.setHost(resources.HostUse{Measured: 60, NCPU: 10})
	f.m.admissionPass(context.Background(), "")
	if f.state("a") != "held" {
		t.Fatal("not granted once the host was not full")
	}
}

// At its deadline a request is granted whatever the state, as an overrun.
func TestPass_OverrunAtDeadline(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.setHost(resources.HostUse{Measured: 99, Full: true, NCPU: 10})
	f.queue("a", 35, 3*time.Second)
	f.m.admissionPass(context.Background(), "")
	if f.state("a") != "waiting" {
		t.Fatal("granted early")
	}
	f.advance(4 * time.Second)
	f.m.admissionPass(context.Background(), "")
	r := f.row("a")
	if r.State != "held" || !r.Overrun || r.WaitedMS != 5000 {
		t.Fatalf("row = %+v", r)
	}
	if d := f.dec("a"); d.path != resources.PathOverrun || d.full != 1 {
		t.Errorf("decision = %+v", d)
	}
}

// The sweeper's pass honours a deadline with no sample ever taken and no
// change of any kind (plan review #3).
func TestPass_OverrunWithNoSampleNoChange(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.m.latest.Store(nil) // no sample
	f.queue("a", 35, 2*time.Second)
	f.m.passIfWaiting(context.Background())
	if f.state("a") != "held" { // no sample = nothing measured, not full: it fits at once
		t.Fatal("not granted without a sample")
	}
	// A pool already full of leases: the waiter fits nothing and waits for its deadline.
	f2 := newPassFix(t, resources.ModeLease)
	f2.m.latest.Store(nil)
	f2.held("h1", 1, "", f2.nowMS()-10000)
	f2.held("h2", 2, "", f2.nowMS()-10000)
	f2.held("h3", 3, "", f2.nowMS()-10000)
	f2.queue("a", 35, 2*time.Second)
	f2.m.passIfWaiting(context.Background())
	if f2.state("a") != "waiting" {
		t.Fatal("granted into a full pool")
	}
	f2.advance(3 * time.Second)
	f2.m.passIfWaiting(context.Background())
	if r := f2.row("a"); r.State != "held" || !r.Overrun {
		t.Fatalf("not granted at its deadline: %+v", r)
	}
}

// The sampler's tick runs the pass.
func TestPass_TickRunsIt(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return goodRaw(), nil, nil
	}}
	f.queue("a", 35, 5*time.Minute)
	f.m.tick(context.Background())
	if f.state("a") != "held" {
		t.Fatal("a tick did not grant")
	}
}

// A grant that loses its CAS (the row was ended after the read) aborts the
// round; the next round, from fresh rows, grants the next waiter.
func TestPass_LostGrantCASReruns(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.queue("a", 35, 5*time.Minute)
	f.queue("b", 35, 5*time.Minute)
	once := false
	f.m.passHook = func() {
		if once {
			return
		}
		once = true
		if ok, err := f.m.store.End("a", resources.EndCancelled, f.nowMS()); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	res := f.m.admissionPass(context.Background(), "")
	if f.state("a") != "ended" || f.state("b") != "held" || res.granted != 1 {
		t.Fatalf("a=%s b=%s granted=%d", f.state("a"), f.state("b"), res.granted)
	}
}

// A grant wakes the pollers; a pass that grants nothing does not.
func TestPass_GrantWakes(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	ch := f.m.genChan()
	f.m.admissionPass(context.Background(), "")
	select {
	case <-ch:
		t.Fatal("woken with nothing granted")
	default:
	}
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	select {
	case <-ch:
	default:
		t.Fatal("a grant did not wake")
	}
}

// Mode advise grants every waiter and records whether lease mode would have
// held it: the one that does not fit is granted with would_wait, not overrun.
func TestPass_AdviseGrantsAllAndRecordsWouldWait(t *testing.T) {
	f := newPassFix(t, resources.ModeAdvise)
	f.held("h", 99, "", f.nowMS()-60000)
	f.m.store.db.Exec(`UPDATE resource_leases SET weight = 80, samples = 5, ewma = 80 WHERE id = 'h'`)
	f.queue("fits", 10, 5*time.Minute)
	f.queue("toomuch", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "toomuch")
	for _, id := range []string{"fits", "toomuch"} {
		if f.state(id) != "held" {
			t.Fatalf("%s not granted in advise", id)
		}
	}
	if r := f.row("fits"); r.WouldWait || r.Overrun {
		t.Errorf("fits: %+v", r)
	}
	r := f.row("toomuch")
	if !r.WouldWait || r.Overrun {
		t.Errorf("toomuch: %+v", r)
	}
	if d := f.dec("toomuch"); d.path != resources.PathImmediate || d.recorded != 1 {
		t.Errorf("decision = %+v", d)
	}
}

// Modes off and measure never touch the rows.
func TestPass_ModesOffAndMeasureDoNothing(t *testing.T) {
	for _, mode := range []string{resources.ModeOff, resources.ModeMeasure} {
		f := newPassFix(t, mode)
		f.queue("a", 35, 5*time.Minute)
		if res := f.m.admissionPass(context.Background(), ""); res.granted != 0 || f.state("a") != "waiting" {
			t.Errorf("%s: granted %d state %s", mode, res.granted, f.state("a"))
		}
	}
}

// A session-new waiter is granted with its baseline written by the same
// statement: the processes under its agent pid (with their start times) at the
// sampler's last reading. A pid with no readable start is left out.
func TestPass_SessionNewBaselineWrittenWithTheGrant(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	start := time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC)
	f.alive(500, start)
	f.alive(501, start.Add(time.Minute)) // an MCP server under the agent
	f.alive(502, start.Add(2*time.Minute))
	procs := []resources.Proc{{PID: 500, PPID: 1}, {PID: 501, PPID: 500}, {PID: 502, PPID: 501}, {PID: 503, PPID: 500}, {PID: 900, PPID: 1}}
	f.m.lastProcs.Store(&procs) // 503 has no start in the table; 900 is another session
	r := baseRow("s", "c-s")
	r.Scope, r.HolderPID = resources.ScopeSessionNew, 500
	r.CreatedAt, r.DeadlineAt, r.LeaseUntil = f.nowMS()-1000, f.nowMS()+300000, f.nowMS()+30000
	mustCreate(t, f.m.store, r)
	f.m.admissionPass(context.Background(), "s")
	got := f.row("s")
	if got.State != "held" {
		t.Fatalf("not granted: %+v", got)
	}
	var base []resources.BaselineEntry
	if err := json.Unmarshal([]byte(got.Baseline), &base); err != nil {
		t.Fatalf("baseline %q: %v", got.Baseline, err)
	}
	want := map[int]int64{501: start.Add(time.Minute).UnixMilli(), 502: start.Add(2 * time.Minute).UnixMilli()}
	if len(base) != 2 || base[0].StartMS != want[base[0].PID] || base[1].StartMS != want[base[1].PID] || base[0].PID == base[1].PID {
		t.Errorf("baseline = %+v, want pids 501 and 502 with their starts", base)
	}
}

// With the process table unreadable the baseline is unknown (NULL, measured
// as the whole tree: spec D-5 fails open), not an empty list, and the grant
// still happens at once; once the table can be read again the next session-new
// lease gets a real baseline.
func TestPass_SessionNewWithUnreadableTableGetsAnUnknownBaseline(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.snapErr = context.DeadlineExceeded
	procs := []resources.Proc{{PID: 501, PPID: 500}}
	f.m.lastProcs.Store(&procs)
	mk := func(id string) {
		r := baseRow(id, "c-"+id)
		r.Scope, r.HolderPID, r.Weight = resources.ScopeSessionNew, 500, 5
		r.CreatedAt, r.DeadlineAt, r.LeaseUntil = f.nowMS()-1000, f.nowMS()+300000, f.nowMS()+30000
		mustCreate(t, f.m.store, r)
	}
	mk("s1")
	f.m.admissionPass(context.Background(), "s1")
	if got := f.row("s1"); got.State != "held" || got.Baseline != "" {
		t.Errorf("unreadable: %+v", got)
	}
	f.snapErr = nil
	f.alive(501, time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC))
	mk("s2")
	f.m.admissionPass(context.Background(), "s2")
	if got := f.row("s2"); got.State != "held" || got.Baseline == "" || got.Baseline == "[]" {
		t.Errorf("readable again: %+v", got)
	}
}

// A process-scope lease needs no baseline and no process table read.
func TestPass_ProcessScopeReadsNoProcessTable(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.queue("a", 35, 5*time.Minute)
	before := f.snaps.Load()
	f.m.admissionPass(context.Background(), "a")
	if f.snaps.Load() != before || f.row("a").Baseline != "" {
		t.Errorf("snapshots read %d -> %d, baseline %q", before, f.snaps.Load(), f.row("a").Baseline)
	}
}

// The two reads the pass makes under stateMu use the state index (a handful
// of rows), not a table scan.
func TestPass_ReadsUseTheStateIndex(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	for _, q := range []string{
		`SELECT id FROM resource_leases WHERE state = 'held' ORDER BY granted_at, id`,
		`SELECT id FROM resource_leases WHERE state = 'waiting' ORDER BY created_at, id`,
	} {
		rows, err := f.m.store.db.Query(`EXPLAIN QUERY PLAN ` + q)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for rows.Next() {
			var a, b, c int
			var detail string
			if err := rows.Scan(&a, &b, &c, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "USING INDEX resource_leases_state") {
				found = true
			}
		}
		rows.Close()
		if !found {
			t.Errorf("no state-index search for %s", q)
		}
	}
}

// The sweeper loop runs the pass on its tick while a request waits.
func TestPass_SweeperLoopRunsIt(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.m.sweepEvery = 5 * time.Millisecond
	f.queue("a", 35, 5*time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	f.m.wg.Add(1)
	go f.m.runSweeper(ctx)
	defer func() { cancel(); f.m.wg.Wait() }()
	deadline := time.Now().Add(2 * time.Second)
	for f.state("a") != "held" {
		if time.Now().After(deadline) {
			t.Fatal("the sweeper loop never granted")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A session-new request that arrives after the baselines were worked out is
// not granted without one: the round leaves it, the next round gives it its
// baseline and grants it.
func TestPass_SessionNewArrivingLateGetsItsBaselineFirst(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	start := time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC)
	f.alive(501, start)
	procs := []resources.Proc{{PID: 501, PPID: 500}}
	f.m.lastProcs.Store(&procs)
	once := false
	f.m.beforeLockHook = func() {
		if once {
			return
		}
		once = true
		r := baseRow("late", "c-late")
		r.Scope, r.HolderPID = resources.ScopeSessionNew, 500
		r.CreatedAt, r.DeadlineAt, r.LeaseUntil = f.nowMS()-1000, f.nowMS()+300000, f.nowMS()+30000
		mustCreate(t, f.m.store, r)
	}
	f.m.admissionPass(context.Background(), "")
	got := f.row("late")
	if got.State != "held" || got.Baseline == "" || got.Baseline == "[]" {
		t.Fatalf("row = %+v: granted without its baseline, or not at all", got)
	}
}

// Advise: a request that lease mode would have queued does not take capacity
// from the counterfactual of the next. Held 80; waiters 30 then 10: lease mode
// keeps the 30 back and lets the 10 through.
func TestPass_AdviseCounterfactualIgnoresWhatLeaseWouldHaveQueued(t *testing.T) {
	f := newPassFix(t, resources.ModeAdvise)
	f.held("h", 99, "", f.nowMS()-60000)
	f.m.store.db.Exec(`UPDATE resource_leases SET weight = 80, samples = 5, ewma = 80 WHERE id = 'h'`)
	f.queue("w1-thirty", 30, 5*time.Minute)
	f.queue("w2-ten", 10, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	if r := f.row("w1-thirty"); !r.WouldWait {
		t.Errorf("thirty: %+v", r)
	}
	if r := f.row("w2-ten"); r.WouldWait {
		t.Errorf("ten was marked would_wait because the thirty it was not behind in lease mode took capacity: %+v", r)
	}
	// What advise did on the host is still recorded: ten saw thirty's weight.
	if d := f.dec("w2-ten"); d.sumCharge != 110 {
		t.Errorf("ten's decision sum = %v, want 110 (80 + the 30 advise let in)", d.sumCharge)
	}
}

// A late-arriving session-new request that would fit holds the queue behind
// it for one round: a process-scope request that arrived after it does not
// take the capacity first.
func TestPass_LateSessionNewIsNotOvertaken(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	once := false
	f.m.beforeLockHook = func() {
		if once {
			return
		}
		once = true
		a := baseRow("a", "c-a")
		a.Scope, a.HolderPID, a.Weight = resources.ScopeSessionNew, 500, 60
		a.CreatedAt, a.DeadlineAt, a.LeaseUntil = f.nowMS()-2000, f.nowMS()+300000, f.nowMS()+30000
		mustCreate(t, f.m.store, a)
		b := baseRow("b", "c-b")
		b.Weight = 60
		b.CreatedAt, b.DeadlineAt, b.LeaseUntil = f.nowMS()-1000, f.nowMS()+300000, f.nowMS()+30000
		mustCreate(t, f.m.store, b)
	}
	f.alive(501, time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC))
	procs := []resources.Proc{{PID: 501, PPID: 500}}
	f.m.lastProcs.Store(&procs)
	f.m.admissionPass(context.Background(), "")
	if f.state("a") != "held" || f.state("b") != "waiting" {
		t.Fatalf("a=%s b=%s: b overtook a", f.state("a"), f.state("b"))
	}
}

// A waiter whose lease ran out (nobody polls it) is the sweeper's to abandon,
// never the pass's to grant, even past its deadline.
func TestPass_NeverGrantsAnAbandonedWaiter(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	r := f.queue("gone", 35, -time.Second) // past its deadline too
	if _, err := f.m.store.db.Exec(`UPDATE resource_leases SET lease_until = ? WHERE id = ?`, f.nowMS()-1, r.ID); err != nil {
		t.Fatal(err)
	}
	f.queue("live", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	if f.state("gone") != "waiting" || f.state("live") != "held" {
		t.Fatalf("gone=%s live=%s", f.state("gone"), f.state("live"))
	}
}

// The snapshot's lease list carries what admission counts (the weight through
// the warmup) and the latest measured use.
func TestSnapshot_LeaseChargeAndUse(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.held("h", 99, "", f.nowMS()-5000) // 5 s old: inside the 20 s warmup
	f.m.setLeaseUse(map[string]resources.LeaseUsage{"h": {CPU: 12, Use: 12}})
	snap := f.m.current()
	f.m.addLeases(&snap)
	if len(snap.Leases) != 1 || snap.Leases[0].Charge != 35 || snap.Leases[0].Use != 12 {
		t.Fatalf("leases = %+v", snap.Leases)
	}
}

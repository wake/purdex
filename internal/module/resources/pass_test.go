package resourcesmod

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// The pass hands the Admitter exactly what the rows and the latest sample say.
func TestPass_BuildsAdmitInput(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(10)
	_, held := e.post(1, resources.LeaseRequest{Kind: "build"})
	if err := e.m.store.UpdateUse(held.ID, 30, 55, 28, 9, 0); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(time.Minute)

	// A published sample: the host figures and its availability go through.
	e.m.sampler = &fakeSampler{}
	e.m.tick(context.Background())
	sample := *e.m.latest.Load()

	e.adm.setMax(0)
	e.post(2, resources.LeaseRequest{Weight: 20, WaitS: 100})
	in := e.adm.last()

	if in.Host != sample.Host || !in.Available {
		t.Fatalf("host = %+v available=%v, want the latest sample's %+v", in.Host, in.Available, sample.Host)
	}
	if len(in.Leases) != 1 || in.Leases[0].ID != held.ID || in.Leases[0].Weight != 35 ||
		in.Leases[0].Measured != 30 || in.Leases[0].Samples != 9 ||
		in.Leases[0].GrantedAt.UnixMilli() != e.row(held.ID).GrantedAt {
		t.Fatalf("leases = %+v", in.Leases)
	}
	if in.LeaseUse == nil || len(in.LeaseUse) != 0 {
		t.Fatalf("lease use = %#v, want an empty map until P1-2b measures it", in.LeaseUse)
	}
	if len(in.Waiters) != 1 || in.Waiters[0].Weight != 20 ||
		in.Waiters[0].Deadline.Sub(in.Waiters[0].EnqueuedAt) != 100*time.Second {
		t.Fatalf("waiters = %+v", in.Waiters)
	}
	if !in.Now.Equal(e.clock.now()) || in.Settings.Mode != resources.ModeLease {
		t.Fatalf("now/settings = %v / %+v", in.Now, in.Settings.Mode)
	}
}

func TestPass_UnavailableBeforeFirstSample(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(0)
	e.post(1, resources.LeaseRequest{})
	if in := e.adm.last(); in.Available {
		t.Fatalf("no sample yet, yet Available = true (host %+v)", in.Host)
	}
}

func TestPass_OverrunFlagFromAdmitter(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(0)
	_, b := e.post(1, resources.LeaseRequest{WaitS: 30})
	e.clock.advance(31 * time.Second)
	e.m.pass(context.Background())
	_, r := e.get(b.ID, 0)
	if !r.Granted || !r.Overrun || r.WaitedMS != 31000 {
		t.Fatalf("response = %+v, want an overrun grant after 31 s", r)
	}
	row := e.row(b.ID)
	if !row.Overrun || row.WaitedMS != 31000 || row.State != resources.StateHeld {
		t.Fatalf("row = %+v", row)
	}
}

func TestPass_OnGrantedHook(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	var got []leaseRow
	e.m.onGranted = func(r leaseRow) { got = append(got, r) }
	e.adm.setMax(1)
	_, a := e.post(1, resources.LeaseRequest{})
	e.post(2, resources.LeaseRequest{})
	if len(got) != 1 || got[0].ID != a.ID || got[0].State != resources.StateHeld {
		t.Fatalf("hook calls = %+v, want one call with the granted row", got)
	}
	e.del(a.ID)
	if len(got) != 2 || got[1].ClientID != clientID(2) {
		t.Fatalf("hook calls = %d", len(got))
	}
}

// A Grant whose compare-and-set fails aborts the pass, which runs again once
// from fresh rows: the waiter that was ended under it is skipped, the next
// one is granted.
func TestPass_LostGrantCASReruns(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(0)
	_, a := e.post(1, resources.LeaseRequest{})
	_, b := e.post(2, resources.LeaseRequest{})

	var calls atomic.Int64
	e.adm.mu.Lock()
	e.adm.fn = func(in AdmitInput) (grant, overrun []string) {
		if calls.Add(1) == 1 {
			// An external writer ends A between the read and the grant.
			if _, err := e.m.store.End(a.ID, resources.EndCancelled, e.clock.now().UnixMilli()); err != nil {
				t.Error(err)
			}
		}
		for _, w := range in.Waiters {
			grant = append(grant, w.ID)
		}
		return grant, nil
	}
	e.adm.mu.Unlock()

	e.m.pass(context.Background())
	if calls.Load() != 2 {
		t.Fatalf("Admit ran %d times, want 2 (one re-run)", calls.Load())
	}
	if got := e.row(b.ID); got.State != resources.StateHeld {
		t.Fatalf("B = %+v, want granted by the re-run", got)
	}
	if got := e.row(a.ID); got.State != resources.StateEnded || got.EndReason != resources.EndCancelled {
		t.Fatalf("A = %+v, want left as the external writer ended it", got)
	}
}

func TestPass_LostTwiceStopsAndLogs(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(0)
	_, a := e.post(1, resources.LeaseRequest{})
	_, b := e.post(2, resources.LeaseRequest{})
	before := e.logs.Load()
	var calls atomic.Int64
	e.adm.mu.Lock()
	e.adm.fn = func(in AdmitInput) (grant, overrun []string) {
		// Each run decides on a waiter that an external writer ends first.
		victim := a.ID
		if calls.Add(1) == 2 {
			victim = b.ID
		}
		if _, err := e.m.store.End(victim, resources.EndCancelled, 1); err != nil {
			t.Error(err)
		}
		return []string{victim}, nil
	}
	e.adm.mu.Unlock()
	e.m.pass(context.Background())
	if calls.Load() != 2 {
		t.Fatalf("Admit ran %d times, want exactly 2 (the pass gives up after one re-run)", calls.Load())
	}
	if e.logs.Load() == before {
		t.Fatal("giving up must be logged")
	}
}

// --- modes ---

func TestSettings_ModeTransitions(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(1)
	_, a := e.post(1, resources.LeaseRequest{})
	_, b := e.post(2, resources.LeaseRequest{})
	_, c := e.post(3, resources.LeaseRequest{WaitS: 10})
	if !a.Granted || b.Granted || c.Granted {
		t.Fatalf("setup: %v %v %v", a.Granted, b.Granted, c.Granted)
	}

	// lease -> measure: the waiters leave as plain grants (not overruns) on
	// the next pass.
	e.set.set(resources.Settings{Mode: resources.ModeMeasure})
	e.m.pass(context.Background())
	for _, id := range []string{b.ID, c.ID} {
		if row := e.row(id); row.State != resources.StateHeld || row.Overrun || row.WouldWait {
			t.Fatalf("row %s = %+v, want a plain grant", id, row)
		}
	}

	// measure -> off: POST grants without a row.
	e.set.set(resources.Settings{Mode: resources.ModeOff})
	if code, r := e.post(4, resources.LeaseRequest{}); code != http.StatusOK || !r.Granted || r.ID != "" || r.Mode != resources.ModeOff {
		t.Fatalf("mode off: %d %+v", code, r)
	}

	// off -> lease, no restart: real waiting again.
	e.set.set(resources.Settings{Mode: resources.ModeLease})
	e.adm.setMax(0)
	if code, r := e.post(5, resources.LeaseRequest{}); code != http.StatusCreated || r.Granted || r.State != resources.StateWaiting || r.Mode != resources.ModeLease {
		t.Fatalf("back in lease: %d %+v", code, r)
	}
}

// Mode advise with waiters left over from mode lease: all start, the ones the
// formula would have kept waiting carry would_wait.
func TestSettings_LeaseToAdviseReleasesWaiters(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(1)
	e.post(1, resources.LeaseRequest{})
	_, b := e.post(2, resources.LeaseRequest{})
	e.set.set(resources.Settings{Mode: resources.ModeAdvise})
	e.m.pass(context.Background())
	if row := e.row(b.ID); row.State != resources.StateHeld || !row.WouldWait || row.Overrun {
		t.Fatalf("row = %+v", row)
	}
}

func TestSettings_ReadFailureFailsOpen(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.set.fail(context.DeadlineExceeded)
	before := e.logs.Load()
	for i := 1; i <= 3; i++ {
		code, r := e.post(i, resources.LeaseRequest{})
		if code != http.StatusOK || !r.Granted || r.State != resources.StateNone || r.Mode != resources.ModeMeasure {
			t.Fatalf("with an unreadable setting: %d %+v, want a measure-mode grant", code, r)
		}
	}
	if got := e.logs.Load() - before; got != 1 {
		t.Fatalf("%d log lines for three failing reads, want exactly one", got)
	}
	e.set.set(resources.Settings{Mode: resources.ModeLease})
	if _, r := e.post(9, resources.LeaseRequest{}); r.Mode != resources.ModeLease || !r.Granted || r.State != resources.StateHeld {
		t.Fatalf("after recovery: %+v", r)
	}
}

func TestTick_ModeOffSkipsSampler(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeOff)
	s := &fakeSampler{}
	e.m.sampler = s
	e.m.tick(context.Background())
	if s.calls.Load() != 0 {
		t.Fatalf("sampler called %d times in mode off", s.calls.Load())
	}
	snap := e.m.latest.Load()
	if snap == nil || snap.Available || snap.Reason != resources.ReasonOff || snap.Mode != resources.ModeOff || snap.Sessions == nil {
		t.Fatalf("snapshot = %+v", snap)
	}
	// Back on without a restart.
	e.set.set(resources.Settings{Mode: resources.ModeMeasure})
	e.m.tick(context.Background())
	if s.calls.Load() != 1 || !e.m.latest.Load().Available || e.m.latest.Load().Mode != resources.ModeMeasure {
		t.Fatalf("after switching on: calls=%d %+v", s.calls.Load(), e.m.latest.Load())
	}
}

func TestAPI_ModeReflectsSettings(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeAdvise)
	if got := e.snapshot().Mode; got != resources.ModeAdvise {
		t.Fatalf("mode = %q", got)
	}
	e.set.set(resources.Settings{Mode: resources.ModeLease})
	if got := e.snapshot().Mode; got != resources.ModeLease {
		t.Fatalf("mode = %q", got)
	}
	e.m.tick(context.Background())
	if got := e.m.latest.Load().Mode; got != resources.ModeLease {
		t.Fatalf("published snapshot mode = %q", got)
	}
}

// --- GET /api/resources lists ---

func TestAPI_LeasesWaitersRecent(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(1)
	_, a := e.post(1, resources.LeaseRequest{Kind: "test-full", SessionID: "sid-a", WaitS: 200})
	_, b := e.post(2, resources.LeaseRequest{Kind: "build", SessionID: "sid-b", WaitS: 200})
	_, c := e.post(3, resources.LeaseRequest{Weight: 12, WaitS: 200})
	e.del(c.ID) // cancelled while waiting
	e.clock.advance(9 * time.Second)

	s := e.snapshot()
	if len(s.Leases) != 1 || s.Leases[0].ID != a.ID || s.Leases[0].Kind != "test-full" || s.Leases[0].Weight != 35 ||
		s.Leases[0].Charge != 35 || s.Leases[0].SessionID != "sid-a" || s.Leases[0].AgeS != 9 || s.Leases[0].Overrun {
		t.Fatalf("leases = %+v (charge is the weight while in warmup)", s.Leases)
	}
	if len(s.Waiters) != 1 || s.Waiters[0].ID != b.ID || s.Waiters[0].Position != 1 || s.Waiters[0].Kind != "build" ||
		s.Waiters[0].WaitedS != 9 || s.Waiters[0].DeadlineInS != 191 {
		t.Fatalf("waiters = %+v", s.Waiters)
	}
	if len(s.Recent) != 1 || s.Recent[0].ID != c.ID || s.Recent[0].EndReason != resources.EndCancelled {
		t.Fatalf("recent = %+v", s.Recent)
	}

	// Charge follows the lease's measured use once the warmup is over.
	if err := e.m.store.UpdateUse(a.ID, 30, 40, 28, 9, 0); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(time.Minute)
	if got := e.snapshot().Leases[0].Charge; got != 30 {
		t.Fatalf("charge after warmup = %v, want the persisted ewma 30", got)
	}

	// The recent list ends after an hour and is capped.
	e.clock.advance(2 * time.Hour)
	if got := e.snapshot().Recent; len(got) != 0 {
		t.Fatalf("recent after two hours = %+v", got)
	}
}

func TestAPI_NoLeasesKeepsP0Shape(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	rec := e.do(http.MethodGet, "/api/resources", nil)
	body := rec.Body.String()
	for _, k := range []string{`"leases"`, `"waiters"`, `"recent"`} {
		if strings.Contains(body, k) {
			t.Errorf("an idle host must omit %s: %s", k, body)
		}
	}
}

// --- the Admitter seam ---

func TestAdmitter_DefaultIsTheAdditiveFormula(t *testing.T) {
	m := New()
	if _, ok := m.admit.(additiveAdmitter); !ok {
		t.Fatalf("default admitter = %T, want additiveAdmitter", m.admit)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	in := AdmitInput{
		Host:      resources.HostUse{Measured: 80},
		Available: true,
		Waiters:   []resources.Waiter{{ID: "w", Weight: 45, EnqueuedAt: now, Deadline: now.Add(time.Minute)}},
		Now:       now,
		Settings:  resources.DefaultSettings(),
	}
	// Unknown heavy work on the host (80 unleased) blocks a 45 request ...
	if grant, overrun := m.admit.Admit(in); len(grant) != 0 || len(overrun) != 0 {
		t.Fatalf("grant=%v overrun=%v, want the waiter kept waiting", grant, overrun)
	}
	// ... until its deadline, when it overruns.
	in.Now = now.Add(time.Minute)
	if grant, overrun := m.admit.Admit(in); len(grant) != 0 || len(overrun) != 1 {
		t.Fatalf("at the deadline: grant=%v overrun=%v", grant, overrun)
	}
	// An idle host grants.
	in.Now, in.Host = now, resources.HostUse{Measured: 5}
	if grant, _ := m.admit.Admit(in); len(grant) != 1 {
		t.Fatalf("idle host: grant=%v", grant)
	}
}

// The pass is formula-free: a fake Admitter alone decides who starts.
func TestAdmitter_PassFollowsTheFake(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.mu.Lock()
	e.adm.fn = func(in AdmitInput) (grant, overrun []string) {
		// Grant only the heaviest waiter, whatever the queue order.
		best := ""
		bw := -1
		for _, w := range in.Waiters {
			if w.Weight > bw {
				best, bw = w.ID, w.Weight
			}
		}
		if best == "" {
			return nil, nil
		}
		return []string{best}, nil
	}
	e.adm.mu.Unlock()
	_, a := e.post(1, resources.LeaseRequest{Weight: 10})
	if !a.Granted {
		t.Fatal("a lone waiter is the heaviest")
	}
	e.adm.mu.Lock()
	e.adm.fn = func(in AdmitInput) (grant, overrun []string) { return nil, nil }
	e.adm.mu.Unlock()
	_, b := e.post(2, resources.LeaseRequest{Weight: 90})
	if b.Granted {
		t.Fatal("the fake granted nobody")
	}
}

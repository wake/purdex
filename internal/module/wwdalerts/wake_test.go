package wwdalerts

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/wwd/wire"
)

// fakeWaker records the WakeWatcher calls; block (when set) holds a call until it is closed or the call's context ends.
type fakeWaker struct {
	mu       sync.Mutex
	calls    []string // reason|detail
	deadline []time.Duration
	block    chan struct{}
	entered  chan struct{} // receives once per call that started
	err      error
	ctxDone  int
}

func (f *fakeWaker) WakeWatcher(ctx context.Context, reason, detail string) error {
	f.mu.Lock()
	f.calls = append(f.calls, reason+"|"+detail)
	if dl, ok := ctx.Deadline(); ok {
		f.deadline = append(f.deadline, time.Until(dl))
	}
	block, entered, err := f.block, f.entered, f.err
	f.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			f.mu.Lock()
			f.ctxDone++
			f.mu.Unlock()
			return ctx.Err()
		}
	}
	return err
}

func (f *fakeWaker) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

// wakeEnv is an env whose module wakes: it is the primary, the host has been looked at once (so what comes next is new), and the one
// wake worker runs.
func wakeEnv(t *testing.T) (*env, *fakeWaker, context.CancelFunc) {
	t.Helper()
	e := newEnv(t)
	return e, startWakeFor(t, e), nil
}

func startWakeFor(t *testing.T, e *env) *fakeWaker {
	t.Helper()
	w := &fakeWaker{}
	e.m.wakeRule = func() bool { return true }
	e.m.waker = func() (watch.Waker, bool) { return w, true }
	ctx, cancel := context.WithCancel(context.Background())
	e.m.startWake(ctx)
	t.Cleanup(func() { cancel(); e.m.wg.Wait() })
	return w
}

// settle waits until the wake worker has nothing waiting and is in no call.
func (e *env) settle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !e.m.wakeQ.idle() {
		if time.Now().After(deadline) {
			t.Fatal("the wake worker did not settle")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// baseline looks at every host once with nothing standing, so the wake ledger is started and what comes next is new.
func (e *env) baseline(t *testing.T, hosts ...string) {
	t.Helper()
	e.pass(t)
	for _, h := range hosts {
		e.src.set(h)
	}
}

func alertOf(key string, kind wire.AlertKind, level wire.AlertLevel, since time.Time, detail string) wire.Alert {
	return wire.Alert{Key: key, Kind: kind, Level: level, Since: since, Last: since, Detail: detail}
}

// Every level wakes the watcher: push, digest, and a level this module does not know yet (the watch level that comes with S3-2).
func TestEveryLevelWakesTheWatcher(t *testing.T) {
	e, w, _ := wakeEnv(t)
	e.baseline(t)
	e.src.set("mlab",
		alertOf("disk_low:/", wire.AlertDiskLow, wire.AlertPush, t0, "mlab 剩 9.8 GB"),
		alertOf("thrash", wire.AlertThrash, wire.AlertDigest, t0, "換頁"),
		alertOf("pressure_watch", wire.AlertKind("pressure_watch"), wire.AlertLevel("watch"), t0, "壓力"),
	)
	e.pass(t)
	e.settle(t)
	all := strings.Join(w.got(), "\n")
	for _, want := range []string{"磁碟空間不足", "換頁過量", "pressure_watch"} {
		if !strings.Contains(all, want) && !strings.Contains(all, "另 ") {
			t.Fatalf("%q not told: %q", want, all)
		}
	}
	if !strings.Contains(all, "wwd_alert|mlab：") {
		t.Fatalf("calls = %q", all)
	}
	// the three are told one way or another: lines, or the first and a count
	told := 0
	for _, c := range w.got() {
		told++
		if i := strings.Index(c, "；另 "); i >= 0 {
			told += 2 // only "另 2 則" can follow when folded from three
		}
	}
	if told < 3 {
		t.Fatalf("told %d of 3: %q", told, all)
	}
}

// The note is "<host>：<kind name>｜<detail>"; a kind with no name shows itself.
func TestWakeLine(t *testing.T) {
	got := wakeLine("air26", alertOf("k", wire.AlertThrash, wire.AlertDigest, t0, "a\nb"))
	if got != "air26：換頁過量｜a b" {
		t.Fatalf("line = %q", got)
	}
	if got := wakeLine("h", alertOf("k", wire.AlertKind("mystery"), wire.AlertDigest, t0, "")); got != "h：mystery" {
		t.Fatalf("line = %q", got)
	}
	long := wakeLine("h", alertOf("k", wire.AlertThrash, wire.AlertDigest, t0, strings.Repeat("字", 400)))
	if err := watch.ValidateWake(wakeReason, long+"；另 99 則"); err != nil {
		t.Fatalf("a long detail makes an invalid note: %v", err)
	}
	for k := range wakeKindNames {
		if !k.Valid() {
			t.Errorf("%q is not a kind", k)
		}
	}
}

// The same alert holding over many passes wakes once; the same (host, key) again within ten minutes (a new episode) does not wake, and
// is not made up later; after ten minutes a new episode wakes.
func TestOneEpisodeWakesOnceAndTheSameKeyWaitsTenMinutes(t *testing.T) {
	e, w, _ := wakeEnv(t)
	e.baseline(t)
	a := alertOf("disk_low:/", wire.AlertDiskLow, wire.AlertPush, t0, "x")
	e.src.set("mlab", a)
	for i := 0; i < 3; i++ {
		e.pass(t)
	}
	e.settle(t)
	if n := len(w.got()); n != 1 {
		t.Fatalf("calls = %d, want 1", n)
	}
	// cleared: no wake
	e.src.set("mlab")
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 1 {
		t.Fatalf("a clear woke: %d", n)
	}
	// raised again 5 minutes after the wake (a new since): inside the ten minutes
	e.now = e.now.Add(5 * time.Minute)
	b := a
	b.Since = e.now
	e.src.set("mlab", b)
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 1 {
		t.Fatalf("a re-raise inside ten minutes woke: %d", n)
	}
	// still holding later: not made up
	e.now = e.now.Add(20 * time.Minute)
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 1 {
		t.Fatalf("the throttled episode was made up later: %d", n)
	}
	// clear and raise after the ten minutes: wakes
	e.src.set("mlab")
	e.pass(t)
	c := a
	c.Since = e.now
	e.src.set("mlab", c)
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 2 {
		t.Fatalf("a new episode after ten minutes: calls = %d, want 2", n)
	}
}

// Another key of the same host is not held back by the first one's ten minutes.
func TestTheTenMinutesAreKeptPerHostAndKey(t *testing.T) {
	e, w, _ := wakeEnv(t)
	e.src.peers = []string{"mserv"}
	e.baseline(t)
	e.src.set("mlab", alertOf("disk_low:/", wire.AlertDiskLow, wire.AlertPush, t0, "a"))
	e.pass(t)
	e.settle(t)
	e.src.set("mserv", alertOf("disk_low:/", wire.AlertDiskLow, wire.AlertPush, t0, "b"))
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 2 {
		t.Fatalf("calls = %v", w.got())
	}
}

// A silence alert is the same failure on every list that noticed it: one wake.
func TestASharedSilenceWakesOnce(t *testing.T) {
	e, w, _ := wakeEnv(t)
	e.src.peers = []string{"air26", "mserv"}
	e.baseline(t)
	s := func(h string) wire.Alert {
		return alertOf("silence:mserv", wire.AlertSilence, wire.AlertPush, t0, "mserv 失聯（"+h+"）")
	}
	e.src.set("mlab", s("mlab"))
	e.src.set("air26", s("air26"))
	e.pass(t)
	e.pass(t)
	e.settle(t)
	calls := w.got()
	if len(calls) != 1 || !strings.Contains(calls[0], "mserv：主機失聯") {
		t.Fatalf("calls = %v", calls)
	}
}

// The first deployment: what stands when the ledger is new is recorded and not woken for; a host looked at later (a peer that was
// unreadable) is seeded when it is first read; what is raised afterwards wakes.
func TestTheFirstDeploymentDoesNotWakeForStandingAlerts(t *testing.T) {
	e, w, _ := wakeEnv(t)
	e.src.peers = []string{"mserv"}
	e.src.set("mlab", alertOf("disk_low:/", wire.AlertDiskLow, wire.AlertPush, t0.Add(-48*time.Hour), "old"))
	e.src.errs = map[string]error{"mserv": errors.New("down")}
	e.src.set("mserv", alertOf("thrash", wire.AlertThrash, wire.AlertDigest, t0.Add(-time.Hour), "old2"))
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 0 {
		t.Fatalf("woke for standing alerts: %v", w.got())
	}
	if e.logCount("not woken for") != 1 {
		t.Fatalf("the seeding was not logged: %v", e.logs)
	}
	e.src.errs = nil // mserv can be read now: its standing alert is seeded, not woken for
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 0 {
		t.Fatalf("woke for a standing alert of a host first read later: %v", w.got())
	}
	e.src.set("mlab",
		alertOf("disk_low:/", wire.AlertDiskLow, wire.AlertPush, t0.Add(-48*time.Hour), "old"),
		alertOf("orphans", wire.AlertOrphans, wire.AlertDigest, t0, "new"))
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 1 {
		t.Fatalf("calls = %v, want one for the new alert", w.got())
	}
}

// A restart (a second module over the same database) does not wake again for what was woken for, a pending silence included, and wakes
// for an episode raised while it was down.
func TestARestartDoesNotWakeAgain(t *testing.T) {
	e, w, _ := wakeEnv(t)
	e.src.peers = []string{"mserv"}
	e.baseline(t)
	a := alertOf("disk_low:/", wire.AlertDiskLow, wire.AlertPush, t0, "x")
	sil := alertOf("silence:mserv", wire.AlertSilence, wire.AlertPush, t0, "mserv 失聯")
	e.src.set("mlab", a, sil)
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 2 && n != 1 { // two alerts folded into one call
		t.Fatalf("calls = %v", w.got())
	}
	before := len(w.got())

	e.now = e.now.Add(time.Minute)
	e.m = e.module(t) // the daemon restarted
	w2 := startWakeFor(t, e)
	e.pass(t)
	e.settle(t)
	if n := len(w2.got()); n != 0 {
		t.Fatalf("a restart woke again: %v (first run woke %d times)", w2.got(), before)
	}
	late := alertOf("orphans", wire.AlertOrphans, wire.AlertDigest, e.now, "late")
	e.src.set("mlab", a, sil, late)
	e.pass(t)
	e.settle(t)
	if n := len(w2.got()); n != 1 {
		t.Fatalf("an episode raised later: calls = %v", w2.got())
	}
}

// Who wakes: the primary always; a standby only when the primary is known not to answer, or answered that it cannot read alerts or push;
// nothing observed yet does not wake; an off daemon and one without a fleet role never do.
func TestWakeRule(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		role string
		peer fleet.PeerPush
		want bool
	}{
		{"primary", fleet.RolePrimary, fleet.PeerPush{}, true},
		{"standby, primary answered and ready", fleet.RoleStandby, fleet.PeerPush{Known: true, Answered: true, Ready: &yes}, false},
		{"standby, primary answered, not ready", fleet.RoleStandby, fleet.PeerPush{Known: true, Answered: true, Ready: &no}, true},
		{"standby, primary does not answer", fleet.RoleStandby, fleet.PeerPush{Known: true}, true},
		{"standby, nothing observed yet", fleet.RoleStandby, fleet.PeerPush{}, false},
		{"standby, older primary without push_ready", fleet.RoleStandby, fleet.PeerPush{Known: true, Answered: true}, false},
		{"off", fleet.RoleOff, fleet.PeerPush{Known: true}, false},
	}
	for _, c := range cases {
		role := fleet.NewFakeRole(fleet.RoleState{Role: c.role, Self: "mlab", Primary: "air26"})
		role.SetPeerPush(c.peer)
		if got := wakeRule(role)(); got != c.want {
			t.Errorf("%s: wakes=%v, want %v", c.name, got, c.want)
		}
	}
	if wakeRule(nil)() {
		t.Error("a daemon without a fleet role wakes")
	}
}

// A standby that is not to wake records nothing; when the primary turns out unable it wakes for what stands.
func TestAStandbyWakesWhenThePrimaryStopsAnswering(t *testing.T) {
	e := newEnv(t)
	w := startWakeFor(t, e)
	role := fleet.NewFakeRole(fleet.RoleState{Role: fleet.RoleStandby, Self: "mlab", Primary: "air26"})
	yes := true
	role.SetPeerPush(fleet.PeerPush{Known: true, Answered: true, Ready: &yes})
	e.m.wakeRule = wakeRule(role)
	e.baseline(t)
	e.src.set("mlab", alertOf("disk_low:/", wire.AlertDiskLow, wire.AlertPush, t0, "x"))
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 0 {
		t.Fatalf("a standby woke: %v", w.got())
	}
	role.SetPeerPush(fleet.PeerPush{Known: true})
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 1 {
		t.Fatalf("calls after the primary stopped answering = %v", w.got())
	}
}

// Episodes that come while the worker is inside a call are folded into one note and one more call: one worker, no goroutine per alert.
func TestEpisodesDuringACallAreFolded(t *testing.T) {
	e, w, _ := wakeEnv(t)
	w.block = make(chan struct{})
	w.entered = make(chan struct{}, 8)
	e.baseline(t)
	e.src.set("mlab", alertOf("k0", wire.AlertThrash, wire.AlertDigest, t0, "first"))
	e.pass(t)
	<-w.entered // the worker is inside the first call
	for i := 1; i <= 5; i++ {
		e.src.set("mlab", alertOf("k"+string(rune('0'+i)), wire.AlertOrphans, wire.AlertDigest, t0, "next"))
		e.pass(t) // does not block on the waker
	}
	close(w.block)
	e.settle(t)
	calls := w.got()
	if len(calls) != 2 {
		t.Fatalf("calls = %v, want the first and one folded", calls)
	}
	if !strings.HasSuffix(calls[1], "；另 4 則") {
		t.Fatalf("folded call = %q", calls[1])
	}
}

// A slow waker does not hold the pass loop.
func TestASlowWakerDoesNotBlockThePass(t *testing.T) {
	e, w, _ := wakeEnv(t)
	w.block = make(chan struct{})
	w.entered = make(chan struct{}, 1)
	defer close(w.block)
	e.baseline(t)
	e.src.set("mlab", alertOf("k", wire.AlertThrash, wire.AlertDigest, t0, "x"))
	done := make(chan struct{})
	go func() { e.pass(t); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the pass waited for the waker")
	}
}

// Stop cancels the call in progress and waits for the worker; the call is given the watcher's registration window.
func TestStopCancelsAndJoinsTheWake(t *testing.T) {
	e := newEnv(t)
	w := &fakeWaker{block: make(chan struct{}), entered: make(chan struct{}, 1)}
	e.m.wakeRule = func() bool { return true }
	e.m.waker = func() (watch.Waker, bool) { return w, true }
	ctx, cancel := context.WithCancel(context.Background())
	e.m.startWake(ctx)
	e.baseline(t)
	e.src.set("mlab", alertOf("k", wire.AlertThrash, wire.AlertDigest, t0, "x"))
	e.pass(t)
	<-w.entered
	w.mu.Lock()
	dl := w.deadline[0]
	w.mu.Unlock()
	if dl < watch.RegisterWithin+watch.StartGrace {
		t.Fatalf("the call's budget %v does not cover the registration window", dl)
	}
	cancel()
	joined := make(chan struct{})
	go func() { e.m.wg.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker was not joined")
	}
	w.mu.Lock()
	n := w.ctxDone
	w.mu.Unlock()
	if n != 1 {
		t.Fatalf("the call's context was not cancelled")
	}
	if e.logCount("wake the watcher") != 0 {
		t.Fatalf("a cancel at stop was logged as a failure: %v", e.logs)
	}
}

// No watch module (no waker), or a worker that was never started: the pass goes on, nothing panics, and a failed wake is only logged.
func TestNoWakerAndAFailingWakerAreHarmless(t *testing.T) {
	e := newEnv(t)
	e.m.wakeRule = func() bool { return true }
	e.m.waker = func() (watch.Waker, bool) { return nil, false }
	ctx, cancel := context.WithCancel(context.Background())
	e.m.startWake(ctx)
	t.Cleanup(func() { cancel(); e.m.wg.Wait() })
	e.baseline(t)
	e.src.set("mlab", alertOf("k", wire.AlertThrash, wire.AlertDigest, t0, "x"))
	e.pass(t)
	e.settle(t)
	if e.logCount("the watch module is not running") != 1 {
		t.Fatalf("logs = %v", e.logs)
	}

	e2 := newEnv(t)
	e2.m.wakeRule = func() bool { return true } // no worker, no waker func at all
	e2.baseline(t)
	e2.src.set("mlab", alertOf("k", wire.AlertThrash, wire.AlertDigest, t0, "x"))
	e2.pass(t)

	e3 := newEnv(t)
	w := startWakeFor(t, e3)
	w.err = errors.New("no watcher")
	e3.baseline(t)
	e3.src.set("mlab", alertOf("k", wire.AlertThrash, wire.AlertDigest, t0, "x"))
	e3.pass(t)
	e3.settle(t)
	e3.pass(t) // not retried
	e3.settle(t)
	if len(w.got()) != 1 || e3.logCount("no watcher") != 1 {
		t.Fatalf("calls %v logs %v", w.got(), e3.logs)
	}
}

// The wake does not depend on the push side: a daemon with no push module and the switch off still wakes.
func TestWakeDoesNotNeedPush(t *testing.T) {
	e, w, _ := wakeEnv(t)
	e.push = false
	e.deliv = false
	e.baseline(t)
	e.src.set("mlab", alertOf("disk_low:/", wire.AlertDiskLow, wire.AlertPush, t0, "x"))
	e.pass(t)
	e.settle(t)
	if len(w.got()) != 1 || e.ntf.count() != 0 {
		t.Fatalf("wakes %v pushes %d", w.got(), e.ntf.count())
	}
}

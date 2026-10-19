package wwdalerts

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/wwd/wire"
)

var t0 = time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)

// fakeSrc is the local wwd: its own alias, its pool, and each host's alert list.
type fakeSrc struct {
	mu      sync.Mutex
	self    string
	peers   []string
	lists   map[string][]wire.Alert // by host alias
	known   map[string]bool         // absent = known
	errs    map[string]error
	infoErr error // what Info answers with when set
}

func (f *fakeSrc) Info(context.Context) (wire.Info, error) {
	if f.infoErr != nil {
		return wire.Info{}, f.infoErr
	}
	return wire.Info{Alias: f.self}, nil
}

func (f *fakeSrc) Pool(context.Context) (wire.Pool, error) {
	var p wire.Pool
	for _, a := range f.peers {
		p.Peers = append(p.Peers, wire.Peer{Info: wire.Info{Alias: a}, Reachable: true})
	}
	return p, nil
}

func (f *fakeSrc) Alerts(_ context.Context, host string) (wire.AlertList, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if host == "" {
		host = f.self
	}
	if err := f.errs[host]; err != nil {
		return wire.AlertList{}, false, err
	}
	if k, ok := f.known[host]; ok && !k {
		return wire.AlertList{Host: host, Alerts: []wire.Alert{}}, false, nil
	}
	return wire.AlertList{Host: host, Alerts: append([]wire.Alert{}, f.lists[host]...)}, true, nil
}

func (f *fakeSrc) set(host string, as ...wire.Alert) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lists == nil {
		f.lists = map[string][]wire.Alert{}
	}
	f.lists[host] = as
}

// fakeNotifier records the pushes and keeps each Sent so a test decides when APNs "took" it.
type fakeNotifier struct {
	mu    sync.Mutex
	got   []push.AlertPush
	done  bool // what NotifyAlert answers
	sents []func()
}

func (n *fakeNotifier) NotifyAlert(p push.AlertPush) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.got = append(n.got, p)
	n.sents = append(n.sents, p.Sent)
	return n.done
}

func (n *fakeNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.got)
}

// confirm makes APNs "take" the idx-th push.
func (n *fakeNotifier) confirm(idx int) {
	n.mu.Lock()
	f := n.sents[idx]
	n.mu.Unlock()
	f()
}

type env struct {
	m     *Module
	src   *fakeSrc
	ntf   *fakeNotifier
	now   time.Time
	deliv bool
	db    string
	// the wwd_alerts.push switch as the module reads it: on (the other tests), off, or unreadable
	push    bool
	pushErr error
	logMu   sync.Mutex
	logs    []string
}

func (e *env) logf(format string, args ...any) {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	e.logs = append(e.logs, fmt.Sprintf(format, args...))
}

func (e *env) logCount(sub string) int {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	n := 0
	for _, l := range e.logs {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{src: &fakeSrc{self: "mlab"}, ntf: &fakeNotifier{done: true}, now: t0, deliv: true, push: true, db: filepath.Join(t.TempDir(), "wwdalerts.db")}
	e.m = e.module(t)
	return e
}

// module builds a module over the env's database (a second call is "the daemon restarted").
func (e *env) module(t *testing.T) *Module {
	t.Helper()
	st, err := openStore(e.db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	return &Module{
		logf:    func(f string, a ...any) { t.Logf(f, a...); e.logf(f, a...) },
		now:     func() time.Time { return e.now },
		src:     e.src,
		store:   st,
		notify:  func() (push.AlertNotifier, bool) { return e.ntf, true },
		deliver: func() bool { return e.deliv },
		pushOn:  func() (bool, error) { return e.push, e.pushErr },
	}
}

func (e *env) pass(t *testing.T) {
	t.Helper()
	e.m.pass(context.Background())
}

func diskLow(host string) wire.Alert {
	return wire.Alert{Key: "disk_low:/", Kind: wire.AlertDiskLow, Level: wire.AlertPush, Since: t0, Last: t0, Detail: host + " 剩 9.8 GB"}
}

// A pushed alert goes to the push module once per attempt, with the collapse id alert:<host>:<key> and the alert's own line as the
// title; it is not "delivered" until the push module says APNs took it (Sent).
func TestPushLevelAlertGoesThroughThePushModuleAndIsDeliveredOnSent(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"mserv"}
	e.src.set("mserv", diskLow("mserv"))
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("pushes = %d", e.ntf.count())
	}
	p := e.ntf.got[0]
	if p.CollapseID != "alert:mserv:disk_low:/" || p.Host != "mserv" || p.Title != "[wwd] mserv 剩 9.8 GB" {
		t.Fatalf("push = %+v", p)
	}
	if st, _ := e.m.store.delivery("mserv", "disk_low:/"); st.Sent != 0 {
		t.Fatalf("recorded as delivered before Sent: %+v", st)
	}
	e.ntf.confirm(0)
	st, ok := e.m.store.delivery("mserv", "disk_low:/")
	if !ok || st.Sent != e.now.UnixMilli() {
		t.Fatalf("not recorded after Sent: %+v %v", st, ok)
	}
}

// Delivered within the hour: not pushed again, whatever the passes. After the hour, an alert that still holds is told again.
func TestADeliveredAlertIsNotPushedAgainWithinTheHour(t *testing.T) {
	e := newEnv(t)
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	e.ntf.confirm(0)
	for i := 0; i < 100; i++ {
		e.now = e.now.Add(30 * time.Second)
		e.pass(t)
	}
	if e.ntf.count() != 1 {
		t.Fatalf("pushed again within the hour: %d", e.ntf.count())
	}
	e.now = t0.Add(time.Hour + time.Minute)
	e.pass(t)
	if e.ntf.count() != 2 {
		t.Fatalf("still active after an hour, pushes = %d", e.ntf.count())
	}
}

// Sent never comes (a person at the computer, no device, APNs down): the module may not queue the alert on every pass. At most one
// attempt per 5 minutes, so at most 12 in an hour.
func TestWithoutSentAtMostTwelveAttemptsAnHour(t *testing.T) {
	e := newEnv(t)
	e.src.set("mlab", diskLow("mlab"))
	for i := 0; i < 120; i++ { // a pass every 30 s for an hour, the last one at 59m30s
		e.pass(t)
		e.now = e.now.Add(30 * time.Second)
	}
	if n := e.ntf.count(); n != 12 {
		t.Fatalf("attempts in an hour = %d, want 12", n)
	}
}

// The attempt time is stored: a restart (a new module over the same database) does not make an attempt a minute after the last one.
func TestTheAttemptIntervalSurvivesARestart(t *testing.T) {
	e := newEnv(t)
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	e.now = e.now.Add(time.Minute)
	e.m = e.module(t)
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("attempts = %d, a restart must not retry within 5 minutes", e.ntf.count())
	}
	e.now = e.now.Add(5 * time.Minute)
	e.pass(t)
	if e.ntf.count() != 2 {
		t.Fatalf("attempts = %d, want a retry after 5 minutes", e.ntf.count())
	}
}

// A delivery is remembered across a restart too (the restart pass tells nothing again within the hour).
func TestADeliveryIsRememberedAcrossARestart(t *testing.T) {
	e := newEnv(t)
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	e.ntf.confirm(0)
	e.now = e.now.Add(10 * time.Minute)
	e.m = e.module(t)
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("pushes = %d", e.ntf.count())
	}
}

// An alert that cleared and holds again is a new one: it is told at once, even within the hour of the earlier delivery.
func TestAClearedAndRaisedAgainAlertIsPushedAtOnce(t *testing.T) {
	e := newEnv(t)
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	e.ntf.confirm(0)
	e.now = e.now.Add(time.Minute)
	e.src.set("mlab") // cleared
	e.pass(t)
	e.now = e.now.Add(time.Minute)
	again := diskLow("mlab")
	again.Since = e.now
	e.src.set("mlab", again)
	e.pass(t)
	if e.ntf.count() != 2 {
		t.Fatalf("pushes = %d, a new alert is told at once", e.ntf.count())
	}
}

// Only the deliverer pushes; the other daemon reads and gathers the digest, and tells nothing.
func TestOnlyTheDelivererPushes(t *testing.T) {
	e := newEnv(t)
	e.deliv = false
	d := diskLow("mlab")
	dg := wire.Alert{Key: "orphans:/tmp", Kind: wire.AlertOrphans, Level: wire.AlertDigest, Since: t0, Last: t0, Detail: "3 個孤兒行程"}
	e.src.set("mlab", d, dg)
	e.pass(t)
	if e.ntf.count() != 0 {
		t.Fatalf("a daemon that is not the deliverer pushed %d", e.ntf.count())
	}
	if n, _ := e.m.store.digestCount(); n != 1 {
		t.Fatalf("digest rows = %d", n)
	}
	e.deliv = true // it becomes the deliverer: the alert still holds and is told
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("pushes after takeover = %d", e.ntf.count())
	}
}

// A digest-level alert is gathered, never pushed, and a repeat of it is one row.
func TestADigestAlertIsGatheredNotPushed(t *testing.T) {
	e := newEnv(t)
	dg := wire.Alert{Key: "orphans:/tmp", Kind: wire.AlertOrphans, Level: wire.AlertDigest, Since: t0, Last: t0, Detail: "3 個孤兒行程"}
	e.src.set("mlab", dg)
	e.pass(t)
	e.now = e.now.Add(time.Minute)
	dg.Last = e.now
	e.src.set("mlab", dg)
	e.pass(t)
	if e.ntf.count() != 0 {
		t.Fatal("a digest alert was pushed")
	}
	if n, _ := e.m.store.digestCount(); n != 1 {
		t.Fatalf("digest rows = %d, want one row for the alert", n)
	}
}

// A host whose list cannot be read is skipped without forgetting it: the others go on, and when it answers again an alert that was
// delivered is not told a second time.
func TestAHostThatCannotBeReadKeepsItsLedger(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"mserv"}
	e.src.set("mserv", diskLow("mserv"))
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	e.ntf.confirm(0)
	e.ntf.confirm(1)
	e.src.errs = map[string]error{"mserv": errors.New("pool read failed")}
	e.now = e.now.Add(2 * time.Minute)
	e.pass(t)
	e.src.errs = nil
	e.now = e.now.Add(2 * time.Minute)
	e.pass(t)
	if e.ntf.count() != 2 {
		t.Fatalf("pushes = %d: an unreadable host must not look cleared", e.ntf.count())
	}
}

// The push module not asking done (queue full, not started): the attempt is counted all the same, so a push side that is down is not
// asked every pass, and nothing is recorded as delivered.
func TestARefusedPushIsRetriedAfterTheInterval(t *testing.T) {
	e := newEnv(t)
	e.ntf.done = false
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("attempts = %d", e.ntf.count())
	}
	e.now = e.now.Add(5 * time.Minute)
	e.pass(t)
	if e.ntf.count() != 2 {
		t.Fatalf("attempts = %d, want a retry after the interval", e.ntf.count())
	}
}

// The deliverer is the daemon holding the fleet primary role. A standby (or off) reads and tells nothing; when the primary is lost and
// the standby takes over, the alerts that still hold are told then; when it steps down again it stops.
func TestDeliveryFollowsTheFleetRole(t *testing.T) {
	e := newEnv(t)
	role := fleet.NewFakeRole(fleet.RoleState{Role: fleet.RoleStandby, Self: "mlab", Primary: "air26"})
	yes := true
	role.SetPeerPush(fleet.PeerPush{Known: true, Answered: true, Ready: &yes}) // the primary can push
	e.m.deliver = deliverRule(role)
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	if e.ntf.count() != 0 {
		t.Fatalf("a standby pushed %d", e.ntf.count())
	}
	role.Set(fleet.RoleState{Role: fleet.RolePrimary, Term: 2, Self: "mlab"}) // the primary was lost: the standby took over
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("pushes after the takeover = %d", e.ntf.count())
	}
	e.ntf.confirm(0)
	role.Set(fleet.RoleState{Role: fleet.RoleStandby, Term: 3, Self: "mlab", Primary: "air26"})
	e.now = e.now.Add(2 * time.Hour)
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("a daemon that stepped down pushed again: %d", e.ntf.count())
	}
	role.Set(fleet.RoleState{Role: fleet.RoleOff, Self: "mlab", Reason: "disabled"})
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("an off daemon pushed: %d", e.ntf.count())
	}
}

// No fleet role at all (the fleet module is not there): nothing delivers, nothing panics.
func TestNoFleetRoleMeansNoDelivery(t *testing.T) {
	if deliverRule(nil)() {
		t.Fatal("a daemon without a fleet role delivered")
	}
}

// A standby with push ready also delivers when the primary cannot: it reported no push, reported nothing (an older daemon), did not
// answer, or nothing has been asked yet. It does not when the primary reported its push ready; and an off daemon never does.
func TestAStandbyDeliversWhenThePrimaryCannotPush(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		peer fleet.PeerPush
		want bool
	}{
		{"primary can push", fleet.PeerPush{Known: true, Answered: true, Ready: &yes}, false},
		{"primary reports no push", fleet.PeerPush{Known: true, Answered: true, Ready: &no}, true},
		{"primary reports nothing (older daemon)", fleet.PeerPush{Known: true, Answered: true}, true},
		{"primary does not answer", fleet.PeerPush{Known: true}, true},
		{"not asked yet", fleet.PeerPush{}, true},
	}
	for _, c := range cases {
		role := fleet.NewFakeRole(fleet.RoleState{Role: fleet.RoleStandby, Self: "mlab", Primary: "air26"})
		role.SetPeerPush(c.peer)
		if got := deliverRule(role)(); got != c.want {
			t.Errorf("standby, %s: delivers=%v, want %v", c.name, got, c.want)
		}
	}
	off := fleet.NewFakeRole(fleet.RoleState{Role: fleet.RoleOff, Self: "mlab", Reason: "disabled"})
	off.SetPeerPush(fleet.PeerPush{Known: true})
	if deliverRule(off)() {
		t.Error("an off daemon delivered")
	}
}

// End to end through the pass: the primary (air26, no push) is the fleet primary, mlab is the standby with push ready. mlab tells the
// alert; once the primary reports its push ready, mlab stops.
func TestAStandbyWithPushPushesWhileThePrimaryHasNone(t *testing.T) {
	e := newEnv(t)
	role := fleet.NewFakeRole(fleet.RoleState{Role: fleet.RoleStandby, Self: "mlab", Primary: "air26"})
	no := false
	role.SetPeerPush(fleet.PeerPush{Known: true, Answered: true, Ready: &no})
	e.m.deliver = deliverRule(role)
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("pushes = %d", e.ntf.count())
	}
	yes := true
	role.SetPeerPush(fleet.PeerPush{Known: true, Answered: true, Ready: &yes})
	e.now = e.now.Add(10 * time.Minute)
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("the standby kept pushing after the primary could: %d", e.ntf.count())
	}
}

// Two different alerts must never share a collapse id (the phone shows one notification per id): host "a" with key "b:c" and host
// "a:b" with key "c" are not the same alert, although joined by colons they read alike.
func TestCollapseIDsOfDifferentAlertsDiffer(t *testing.T) {
	if collapseID("a", "b:c") == collapseID("a:b", "c") {
		t.Fatal("two different alerts share a collapse id")
	}
	if got := collapseID("mserv", "disk_low:/"); got != "alert:mserv:disk_low:/" {
		t.Fatalf("the plain form is lost: %q", got)
	}
}

// A clock that stepped back after an attempt or a delivery was written must not stretch the windows: a time in the future is spent.
func TestAClockThatSteppedBackDoesNotStretchTheWindows(t *testing.T) {
	e := newEnv(t)
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t) // attempt at t0
	e.now = t0.Add(-time.Hour)
	e.pass(t)
	if e.ntf.count() != 2 {
		t.Fatalf("attempts = %d: an attempt stamped in the future still silenced the alert", e.ntf.count())
	}
	e.ntf.confirm(1) // delivered at t0 - 1h
	e.now = t0.Add(-3 * time.Hour)
	e.pass(t)
	if e.ntf.count() != 3 {
		t.Fatalf("attempts = %d: a delivery stamped in the future still silenced the alert", e.ntf.count())
	}
}

// WWD-8a-2a-0: the wwd_alerts.push switch. Off (the default): the alerts are read, the digest ones are gathered, and not one push-level
// alert is handed to the push module — no attempt, no ledger row, nothing recorded as sent — so that when the switch is turned on the
// alerts that still hold are pushed by the rules, not held back as if they had been told.
func TestPushOffGathersTheDigestAndTouchesNeitherThePushModuleNorTheLedger(t *testing.T) {
	e := newEnv(t)
	e.push = false
	dg := wire.Alert{Key: "orphans:/tmp", Kind: wire.AlertOrphans, Level: wire.AlertDigest, Since: t0, Last: t0, Detail: "3 個孤兒行程"}
	e.src.set("mlab", diskLow("mlab"), dg)
	for i := 0; i < 5; i++ {
		e.pass(t)
		e.now = e.now.Add(time.Minute)
	}
	if e.ntf.count() != 0 {
		t.Fatalf("the push module was asked %d times with the switch off", e.ntf.count())
	}
	if _, ok := e.m.store.delivery("mlab", "disk_low:/"); ok {
		t.Fatal("the ledger holds a row for an alert that was not pushed")
	}
	if n, _ := e.m.store.digestCount(); n != 1 {
		t.Fatalf("digest rows = %d, the digest is gathered whatever the switch says", n)
	}
	if st := e.m.Status(); st["push"] != "off" || st["suppressed"] != 1 {
		t.Fatalf("status = %v", st)
	}
}

// Turning it on: the next pass pushes what still holds, once; turning it off and on again within the hour of a confirmed delivery
// pushes nothing more.
func TestTurningPushOnPushesWhatStillHoldsAndOnlyOnce(t *testing.T) {
	e := newEnv(t)
	e.push = false
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	e.push = true
	e.now = e.now.Add(time.Second)
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("pushes after turning on = %d", e.ntf.count())
	}
	e.ntf.confirm(0)
	e.push = false
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	e.push = true
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("pushes = %d: off and on again must not tell a delivered alert twice", e.ntf.count())
	}
	if st := e.m.Status(); st["push"] != "on" || st["suppressed"] != 0 {
		t.Fatalf("status = %v", st)
	}
}

// A switch that cannot be read counts as off (the stop is the safe side), and the problem is logged once, not every pass.
func TestAnUnreadablePushSwitchIsOffAndLoggedOnce(t *testing.T) {
	e := newEnv(t)
	e.pushErr = errors.New("host_config.db is damaged")
	e.src.set("mlab", diskLow("mlab"))
	for i := 0; i < 4; i++ {
		e.pass(t)
		e.now = e.now.Add(time.Minute)
	}
	if e.ntf.count() != 0 {
		t.Fatalf("pushed %d with the switch unreadable", e.ntf.count())
	}
	if n := e.logCount("host_config.db is damaged"); n != 1 {
		t.Fatalf("the problem was logged %d times", n)
	}
	if st := e.m.Status(); st["push"] != "off" {
		t.Fatalf("status = %v", st)
	}
}

// The state of the switch is logged when it changes, not on every pass.
func TestThePushSwitchIsLoggedWhenItChanges(t *testing.T) {
	e := newEnv(t)
	e.push = false
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	e.pass(t)
	if n := e.logCount("push is off"); n != 1 {
		t.Fatalf("off logged %d times", n)
	}
	e.push = true
	e.pass(t)
	e.pass(t)
	if n := e.logCount("push is on"); n != 1 {
		t.Fatalf("on logged %d times", n)
	}
}

// A push handed over while the switch was on, and still waiting in the push module's queue when it is turned off, is not sent: the
// push module asks StillWanted just before sending, and it follows the switch.
func TestAQueuedPushIsNotSentOnceTheSwitchIsTurnedOff(t *testing.T) {
	e := newEnv(t)
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("pushes = %d", e.ntf.count())
	}
	e.ntf.mu.Lock()
	wanted := e.ntf.got[0].StillWanted
	e.ntf.mu.Unlock()
	if wanted == nil {
		t.Fatal("the push carries no way to be asked again")
	}
	if !wanted() {
		t.Fatal("not wanted while the switch is on")
	}
	e.push = false
	if wanted() {
		t.Fatal("still wanted after the switch was turned off")
	}
	e.pushErr = errors.New("unreadable")
	e.push = true
	if wanted() {
		t.Fatal("still wanted with the switch unreadable")
	}
}

// A push taken back before anything was sent hands the attempt back: Recalled puts the ledger as it was, so turning the switch on again
// is not held back for the 5-minute interval.
func TestARecalledPushDoesNotHoldTheAlertBack(t *testing.T) {
	e := newEnv(t)
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	e.ntf.mu.Lock()
	recalled := e.ntf.got[0].Recalled
	e.ntf.mu.Unlock()
	if recalled == nil {
		t.Fatal("the push carries no way to be told it was taken back")
	}
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("pushes = %d: the attempt interval should hold it back", e.ntf.count())
	}
	recalled()
	e.pass(t)
	if e.ntf.count() != 2 {
		t.Fatalf("pushes = %d: a recalled attempt should not hold it back", e.ntf.count())
	}
}

// Status reports a deliverer only when this daemon would really send: the rule says so and its own push module is ready (#3573). A
// primary whose push is not configured is not a deliverer, though the rule names it; a standby that takes over for such a primary is.
func TestStatusDelivererNeedsAReadyNotifier(t *testing.T) {
	ready := func() (push.AlertNotifier, bool) { return &fakeNotifier{done: true}, true }
	notReady := func() (push.AlertNotifier, bool) { return nil, false }
	no := false
	for _, c := range []struct {
		name   string
		role   fleet.RoleState
		peer   fleet.PeerPush
		notify func() (push.AlertNotifier, bool)
		want   bool
	}{
		{"primary without a ready notifier", fleet.RoleState{Role: fleet.RolePrimary, Self: "air26"}, fleet.PeerPush{}, notReady, false},
		{"primary with a ready notifier", fleet.RoleState{Role: fleet.RolePrimary, Self: "air26"}, fleet.PeerPush{}, ready, true},
		{"standby, primary cannot push, own notifier ready", fleet.RoleState{Role: fleet.RoleStandby, Self: "mlab", Primary: "air26"}, fleet.PeerPush{Known: true, Answered: true, Ready: &no}, ready, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			role := fleet.NewFakeRole(c.role)
			role.SetPeerPush(c.peer)
			e.m.deliver = deliverRule(role)
			e.m.notify = c.notify
			e.pass(t)
			if got := e.m.Status()["deliverer"]; got != c.want {
				t.Fatalf("deliverer = %v, want %v", got, c.want)
			}
		})
	}
}

// A daemon that is not the deliverer says so, and counts nothing as held back by the switch.
func TestStatusSaysWhetherThisDaemonIsTheDeliverer(t *testing.T) {
	e := newEnv(t)
	e.deliv = false
	e.push = false
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	st := e.m.Status()
	if st["deliverer"] != false || st["suppressed"] != 0 {
		t.Fatalf("status = %v", st)
	}
}

// The registry keeps the push module's notifier after the module stops. One that says it is not ready is not asked.
type readinessNotifier struct {
	fakeNotifier
	ready bool
}

func (r *readinessNotifier) AlertReady() bool { return r.ready }

func TestAStoppedPushModuleIsNotAsked(t *testing.T) {
	n := &readinessNotifier{ready: true}
	if _, ok := readyNotifier(n); !ok {
		t.Fatal("a ready notifier was refused")
	}
	n.ready = false
	if _, ok := readyNotifier(n); ok {
		t.Fatal("a notifier that is not ready was asked")
	}
	if _, ok := readyNotifier(struct{}{}); ok {
		t.Fatal("something that is not a notifier was taken for one")
	}
	if _, ok := readyNotifier(&fakeNotifier{}); !ok {
		t.Fatal("a notifier with no readiness is taken as asked-able")
	}
}

// The push module stops between the readiness look and the push: NotifyAlert answers false because the module is down, nothing was queued,
// and the attempt that was written before the call is given back — it must not silence the alert for the 5 minutes of the interval.
type stoppingNotifier struct {
	mu    sync.Mutex
	ready bool
	stop  bool // the next NotifyAlert finds the module just stopped
	calls int
	// during, when set, runs inside NotifyAlert: what another goroutine does at that moment (an older push getting its Sent)
	during func()
}

func (s *stoppingNotifier) NotifyAlert(push.AlertPush) bool {
	if s.during != nil {
		s.during()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.stop {
		s.ready = false
		return false
	}
	return true
}

func (s *stoppingNotifier) AlertReady() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.ready }

func TestAnAttemptTheStoppedPushModuleNeverTookIsGivenBack(t *testing.T) {
	e := newEnv(t)
	n := &stoppingNotifier{ready: true, stop: true}
	e.m.notify = func() (push.AlertNotifier, bool) { return readyNotifier(n) }
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	if n.calls != 1 {
		t.Fatalf("calls = %d", n.calls)
	}
	// the module is back a minute later: the alert is not held back by an attempt that sent nothing
	n.mu.Lock()
	n.ready, n.stop = true, false
	n.mu.Unlock()
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	if n.calls != 2 {
		t.Fatalf("calls = %d: the failed attempt was still counted", n.calls)
	}
	if d, ok := e.m.store.delivery("mlab", "disk_low:/"); !ok || d.Attempt != e.now.UnixMilli() {
		t.Fatalf("the ledger row is %+v", d)
	}
}

// An older push of the same alert may still be in the sender when a new attempt is refused: its Sent can land between the attempt being
// written and given back. Giving the attempt back puts back the attempt time only, never over a delivery that was recorded meanwhile.
func TestGivingAnAttemptBackNeverErasesADeliveryRecordedMeanwhile(t *testing.T) {
	e := newEnv(t)
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t) // the first attempt, at t0, whose push is still in the sender (its Sent has not come)
	first := e.now
	e.now = e.now.Add(6 * time.Minute) // past the interval: a new attempt is due
	n := &stoppingNotifier{ready: true, stop: true}
	n.during = func() { _ = e.m.store.markSent("mlab", "disk_low:/", t0, e.now) } // the old push is taken by APNs just now
	e.m.notify = func() (push.AlertNotifier, bool) { return readyNotifier(n) }
	e.pass(t)
	d, ok := e.m.store.delivery("mlab", "disk_low:/")
	if !ok {
		t.Fatal("the ledger row is gone")
	}
	if d.Sent != e.now.UnixMilli() {
		t.Fatalf("the delivery recorded meanwhile was erased: %+v", d)
	}
	if d.Attempt != first.UnixMilli() {
		t.Fatalf("the attempt that queued nothing was not given back: %+v", d)
	}
}

// A queue that is full (the module is up) is an attempt that counts: the module is not asked again before the interval.
func TestAFullQueueStillSpendsTheAttempt(t *testing.T) {
	e := newEnv(t)
	n := &readinessNotifier{ready: true}
	n.done = false
	e.m.notify = func() (push.AlertNotifier, bool) { return readyNotifier(n) }
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	if n.count() != 1 {
		t.Fatalf("calls = %d, a full queue is retried only after the interval", n.count())
	}
}

// The push module is not there (no notifier in the registry yet): nothing is attempted, and nothing is counted as an attempt.
func TestNoPushModuleMeansNoAttempt(t *testing.T) {
	e := newEnv(t)
	e.m.notify = func() (push.AlertNotifier, bool) { return nil, false }
	e.src.set("mlab", diskLow("mlab"))
	e.pass(t)
	e.m.notify = func() (push.AlertNotifier, bool) { return e.ntf, true }
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("pushes = %d: the first pass must not have used up the interval", e.ntf.count())
	}
}

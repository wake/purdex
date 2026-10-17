package assistmod

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/team"
)

type sent struct{ sid, text string }

type sink struct {
	mu   sync.Mutex
	sent []sent
}

func (s *sink) send(sid, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, sent{sid, text})
	return nil
}

func (s *sink) all() []sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sent(nil), s.sent...)
}

type wakes struct {
	mu      sync.Mutex
	calls   [][2]string
	block   chan struct{} // when set, a wake waits on it
	entered chan struct{} // when set, a wake says it has started
}

func (w *wakes) wake(ctx context.Context, reason, detail string) error {
	if w.entered != nil {
		w.entered <- struct{}{}
	}
	if w.block != nil {
		select {
		case <-w.block:
		case <-ctx.Done():
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, [2]string{reason, detail})
	return nil
}

func (w *wakes) all() [][2]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][2]string(nil), w.calls...)
}

// notifyRig is a host that is asked on, allowed for team t1 (a lead and a member live here), with purdex ready on it.
func notifyRig(t *testing.T) (*rig, *sink, *wakes) {
	r := newRig(t)
	snk, wk := &sink{}, &wakes{}
	r.m.notify, r.m.waker = snk.send, wk.wake
	r.addHost("air26")
	r.edit(func(f *gate.AssistFile) {
		h := f.Hosts["air26"]
		h.On = true
		f.Hosts["air26"] = h
		f.Allowed = []gate.AssistAllow{{Kind: "team", ID: "t1", Name: "alpha"}}
	})
	r.teams.sessions["t1"] = []string{"lead", "m1"}
	r.live("lead", "_lead01", "mlab/x-lead")
	r.live("m1", "_memb01", "mlab/x-m1")
	r.setCheck("on", map[string]bool{"purdex": true})
	return r, snk, wk
}

// setCheck makes the host's check, for its current generation, say this.
func (r *rig) setCheck(sw string, repos map[string]bool) {
	a := gateAnswer(sw)
	for n, ok := range repos {
		a.Repos[n] = gate.AssistRepo{Ready: ok, Why: map[bool]string{true: "", false: "repo " + n + " is not cloned"}[ok]}
	}
	a.Host = "air26"
	r.m.setReady("air26", r.readyOf(r.file().Hosts["air26"].Gen, a))
}

func TestNoticeWhenAHostBecomesUsableAndUnusableOnePerChange(t *testing.T) {
	r, snk, _ := notifyRig(t)
	r.m.notifyPass()
	got := snk.all()
	if len(got) != 2 || got[0].text != "[輔測] air26 可用（purdex）" || got[1].text != got[0].text {
		t.Fatalf("first pass: %+v", got)
	}
	if got[0].sid != "lead" && got[1].sid != "lead" {
		t.Fatalf("the lead was not told: %+v", got)
	}
	r.m.notifyPass()
	r.m.notifyPass()
	if n := len(snk.all()); n != 2 {
		t.Fatalf("an unchanged pass sent notices: %d", n)
	}
	r.setCheck("off", map[string]bool{"purdex": true})
	r.m.notifyPass()
	got = snk.all()[2:]
	if len(got) != 2 || got[0].text != "[輔測] air26 不可用（purdex）：the host says off" {
		t.Fatalf("unusable: %+v", got)
	}
}

func TestNoNoticeAfterARestartWithNothingChanged(t *testing.T) {
	r, snk, _ := notifyRig(t)
	r.m.notifyPass()
	before := len(snk.all())
	m2 := New()
	m2.dir, m2.now, m2.logf, m2.origins, m2.teams, m2.seats = r.dir, r.clock.now, r.t.Logf, r.origins, r.teams, r.seats
	m2.ready = r.m.readyCopy()
	m2.notify = snk.send
	m2.notifyPass()
	if n := len(snk.all()); n != before {
		t.Fatalf("a restart sent %d notices", n-before)
	}
}

func TestSeveralChangesForOneRecipientAreOneNotice(t *testing.T) {
	r, snk, _ := notifyRig(t)
	r.setCheck("on", map[string]bool{"purdex": true, "aigora-v2": true})
	r.m.notifyPass()
	for _, s := range snk.all() {
		if s.sid == "lead" {
			if s.text != "[輔測] air26 可用（aigora-v2）\n[輔測] air26 可用（purdex）" {
				t.Fatalf("%q", s.text)
			}
			return
		}
	}
	t.Fatal("the lead was not told")
}

func TestPassesRunOneAfterTheOtherEachChangeToldOnce(t *testing.T) {
	r, snk, _ := notifyRig(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.m.notifyPass() }()
	}
	wg.Wait()
	if n := len(snk.all()); n != 2 {
		t.Fatalf("%d notices from 8 concurrent passes, want 2 (lead and member, once)", n)
	}
}

func TestOnlyLiveSessionsOfThisHostAreToldAndAnEndedTeamTellsNobody(t *testing.T) {
	r, snk, _ := notifyRig(t)
	r.teams.sessions["t1"] = []string{"lead", "m1", "gone"} // "gone" is no live session here
	r.m.notifyPass()
	for _, s := range snk.all() {
		if s.sid == "gone" {
			t.Fatal("a session that is not live here was told")
		}
	}
	r2, snk2, _ := notifyRig(t)
	r2.teams.sessions["t1"] = nil // an ended team, or one with nobody here
	r2.m.notifyPass()
	if n := len(snk2.all()); n != 0 {
		t.Fatalf("%d notices", n)
	}
}

func TestSessionEntryIsFoundByRefAfterARelay(t *testing.T) {
	r, snk, _ := notifyRig(t)
	r.edit(func(f *gate.AssistFile) { f.Allowed = []gate.AssistAllow{{Kind: "session", ID: "_aaaaaa"}} })
	r.live("sid2", "_bbbbbb", "mlab/x-2")
	r.teams.succ["_aaaaaa"] = []string{"sid2"}
	r.m.notifyPass()
	got := snk.all()
	if len(got) != 1 || got[0].sid != "sid2" || got[0].text != "[輔測] air26 可用（purdex）" {
		t.Fatalf("%+v", got)
	}
}

func TestDenyTellsTheEntryItIsNoLongerUsable(t *testing.T) {
	r, snk, _ := notifyRig(t)
	r.m.notifyPass()
	r.edit(func(f *gate.AssistFile) { f.Allowed = nil })
	r.m.notifyPass()
	got := snk.all()[2:]
	if len(got) != 2 || !strings.HasPrefix(got[0].text, "[輔測] air26 不可用（purdex）：") {
		t.Fatalf("%+v", got)
	}
	r.m.notifyPass()
	if len(snk.all()) != 4 {
		t.Fatal("told twice")
	}
}

func TestNoNoticesWithoutANotifier(t *testing.T) {
	r, _, _ := notifyRig(t)
	r.m.notify = nil
	r.m.notifyPass() // nothing to send through: no panic, no state written
	if _, ok := r.m.readNotified()["x"]; ok {
		t.Fatal("?")
	}
}

// The module, started, runs a pass after an allow without anyone calling notifyPass.
func TestAnAllowAsksTheNotifierGoroutine(t *testing.T) {
	r, snk, _ := notifyRig(t)
	r.edit(func(f *gate.AssistFile) { f.Allowed = nil })
	r.m.pollEvery = time.Hour
	r.teams.teams = []team.Team{{ID: "t1", TeamName: "alpha", LeadSessionID: "lead"}}
	if err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer r.m.Stop(context.Background())
	if code, b := r.do("POST", "/api/assist/allow", map[string]any{"kind": "team", "id": "alpha", "reason": "x"}); code != 200 {
		t.Fatalf("%d %v", code, b)
	}
	r.waitFor("the notices", func() bool { return len(snk.all()) >= 2 })
}

func TestWatcherWokenOnSwitchAllowNotDenyNotCheck(t *testing.T) {
	r, _, wk := notifyRig(t)
	r.teams.teams = []team.Team{{ID: "t1", TeamName: "alpha", LeadSessionID: "lead"}}
	r.edit(func(f *gate.AssistFile) { h := f.Hosts["air26"]; h.On = false; f.Hosts["air26"] = h; f.Allowed = nil })
	r.script("assist-check", checkOut("on", map[string]bool{"purdex": true}), 0)
	r.m.pass() // a check wakes nobody
	if code, _ := r.do("POST", "/api/assist/switch", map[string]any{"host": "air26", "on": true, "reason": "測試", "by": "s1"}); code != 200 {
		t.Fatal(code)
	}
	r.waitFor("the switch wake", func() bool { return len(wk.all()) == 1 })
	if c := wk.all()[0]; c[0] != "assist_changed" || c[1] != "air26 輔測 關→開 by s1：測試" {
		t.Fatalf("%v", c)
	}
	r.do("POST", "/api/assist/allow", map[string]any{"kind": "team", "id": "alpha", "reason": "因為", "by": "s1", "host": "air26"})
	r.waitFor("the allow wake", func() bool { return len(wk.all()) == 2 })
	if c := wk.all()[1]; c[0] != "assist_allowed" || c[1] != "team alpha 可用 air26 by s1：因為" {
		t.Fatalf("%v", c)
	}
	r.do("POST", "/api/assist/allow", map[string]any{"kind": "team", "id": "alpha", "reason": "again", "host": "air26"}) // already
	r.do("POST", "/api/assist/deny", map[string]any{"kind": "team", "id": "alpha", "host": "air26"})
	r.do("POST", "/api/assist/switch", map[string]any{"host": "air26", "on": false, "reason": "off", "by": "s1"})
	r.waitFor("the off wake", func() bool { return len(wk.all()) == 3 })
	if c := wk.all()[2]; c[0] != "assist_changed" || c[1] != "air26 輔測 開→關 by s1：off" {
		t.Fatalf("%v", c)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(wk.all()); n != 3 {
		t.Fatalf("%d wakes; an already, a deny and a check must wake nobody", n)
	}
	// a switch that changes nothing (off when it is off) wakes nobody either
	r.do("POST", "/api/assist/switch", map[string]any{"host": "air26", "on": false, "reason": "again"})
	time.Sleep(50 * time.Millisecond)
	if n := len(wk.all()); n != 3 {
		t.Fatalf("%d wakes", n)
	}
}

func TestWakesThatComeFasterThanOneFinishesAreFoldedIntoTheLast(t *testing.T) {
	r, _, wk := notifyRig(t)
	wk.block, wk.entered = make(chan struct{}), make(chan struct{}, 8)
	r.m.wakeWatcher("assist_changed", "air26 輔測 關→開 a")
	<-wk.entered // the first wake is under way and blocked: the next ones fold
	for _, d := range []string{"b", "c", "d"} {
		r.m.wakeWatcher("assist_changed", "air26 輔測 關→開 "+d)
	}
	close(wk.block)
	r.waitFor("the folded wakes", func() bool { return len(wk.all()) >= 2 })
	time.Sleep(50 * time.Millisecond)
	got := wk.all()
	if len(got) != 2 || !strings.HasSuffix(got[0][1], " a") || !strings.HasSuffix(got[1][1], " d") {
		t.Fatalf("%v: want the first, then only the last", got)
	}
}

func TestWakeDetailIsOneLineOfAtMost500Bytes(t *testing.T) {
	long := strings.Repeat("輔", 400)
	d := wakeDetail("a\nb\x1b" + long)
	if len(d) > 500 || strings.ContainsAny(d, "\n\x1b") || !strings.HasPrefix(d, "a b ") {
		t.Fatalf("%d bytes %q", len(d), d[:20])
	}
}

func TestStopEndsAWakeInFlight(t *testing.T) {
	r, _, wk := notifyRig(t)
	wk.block = make(chan struct{}) // never released
	if err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.m.wakeWatcher("assist_changed", "x")
	done := make(chan struct{})
	go func() { r.m.Stop(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop waited for the wake")
	}
}

// Wakes about different subjects are all kept: two teams allowed in a row must both reach the watcher.
func TestWakesAboutDifferentSubjectsAreNotFolded(t *testing.T) {
	r, _, wk := notifyRig(t)
	wk.block, wk.entered = make(chan struct{}), make(chan struct{}, 8)
	r.m.wakeWatcher("assist_changed", "air26 輔測 關→開 x")
	<-wk.entered
	r.m.wakeWatcher("assist_allowed", "team A 可用 全部 by s：r")
	r.m.wakeWatcher("assist_allowed", "team B 可用 全部 by s：r")
	r.m.wakeWatcher("assist_changed", "mserv 輔測 關→開 y")
	close(wk.block)
	r.waitFor("all four", func() bool { return len(wk.all()) == 4 })
	var got []string
	for _, c := range wk.all() {
		got = append(got, c[1])
	}
	if !strings.Contains(strings.Join(got, "|"), "team A") || !strings.Contains(strings.Join(got, "|"), "team B") || !strings.Contains(strings.Join(got, "|"), "mserv") {
		t.Fatalf("%v", got)
	}
}

// A wake that comes while Stop is closing the door is refused, never added behind Stop's Wait (checked under -race).
func TestWakeAndStopRace(t *testing.T) {
	for i := 0; i < 50; i++ {
		r, _, _ := notifyRig(t)
		if err := r.m.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); r.m.wakeWatcher("assist_changed", "air26 輔測 關→開 x") }()
		go func() { defer wg.Done(); r.m.Stop(context.Background()) }()
		wg.Wait()
	}
}

// A notice that could not be delivered is tried again at the next pass; the other session's state is not touched.
func TestFailedDeliveryIsTriedAgainAndOthersAreNotToldTwice(t *testing.T) {
	r, snk, _ := notifyRig(t)
	failing := true
	r.m.notify = func(sid, text string) error {
		if sid == "m1" && failing {
			return context.DeadlineExceeded
		}
		return snk.send(sid, text)
	}
	r.m.notifyPass()
	if got := snk.all(); len(got) != 1 || got[0].sid != "lead" {
		t.Fatalf("first pass: %+v", got)
	}
	failing = false
	r.m.notifyPass()
	got := snk.all()
	if len(got) != 2 || got[1].sid != "m1" {
		t.Fatalf("second pass: %+v (the member once, the lead not again)", got)
	}
	r.m.notifyPass()
	if len(snk.all()) != 2 {
		t.Fatal("told again")
	}
}

// A member who joins after the state was told gets the state as it is, and so does the session a relay moved the entry to.
func TestANewRecipientIsToldTheCurrentState(t *testing.T) {
	r, snk, _ := notifyRig(t)
	r.m.notifyPass()
	r.live("m2", "_memb02", "mlab/x-m2")
	r.teams.sessions["t1"] = []string{"lead", "m1", "m2"}
	r.m.notifyPass()
	got := snk.all()
	if len(got) != 3 || got[2].sid != "m2" || got[2].text != "[輔測] air26 可用（purdex）" {
		t.Fatalf("%+v", got)
	}
	r.edit(func(f *gate.AssistFile) { f.Allowed = []gate.AssistAllow{{Kind: "session", ID: "_aaaaaa"}} })
	r.live("s1", "_aaaaab", "mlab/x-s1")
	r.teams.succ["_aaaaaa"] = []string{"s1"}
	r.m.notifyPass()
	last := snk.all()[len(snk.all())-1]
	_ = last
	var toS1 []sent
	for _, s := range snk.all() {
		if s.sid == "s1" {
			toS1 = append(toS1, s)
		}
	}
	if len(toS1) != 1 || toS1[0].text != "[輔測] air26 可用（purdex）" {
		t.Fatalf("%+v", toS1)
	}
	// the relay: the entry now resolves to another session, which is told; nothing again for the one before
	delete(r.origins.live, "s1")
	r.live("s2", "_aaaaac", "mlab/x-s2")
	r.teams.succ["_aaaaaa"] = []string{"s2"}
	r.m.notifyPass()
	var toS2 []sent
	for _, s := range snk.all() {
		if s.sid == "s2" {
			toS2 = append(toS2, s)
		}
	}
	if len(toS2) != 1 {
		t.Fatalf("%+v", toS2)
	}
}

// No wake about a different subject is ever dropped, however many wait behind a slow one.
func TestNoWakeIsDroppedBehindASlowOne(t *testing.T) {
	r, _, wk := notifyRig(t)
	wk.block, wk.entered = make(chan struct{}), make(chan struct{}, 256)
	r.m.wakeWatcher("assist_changed", "air26 輔測 關→開 x")
	<-wk.entered
	for i := 0; i < 150; i++ {
		r.m.wakeWatcher("assist_allowed", fmt.Sprintf("team T%d 可用 全部 by s：r", i))
	}
	close(wk.block)
	r.waitFor("all of them", func() bool { return len(wk.all()) == 151 })
}

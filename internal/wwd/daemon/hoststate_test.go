package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

type stateRig struct {
	st    *HostState
	b     *Broker
	ch    <-chan wire.Event
	now   time.Time
	alive map[int]bool
	path  string
}

func newRig(t *testing.T) *stateRig {
	t.Helper()
	r := &stateRig{now: s0, alive: map[int]bool{}, path: filepath.Join(t.TempDir(), "wwd.db"), b: NewBroker()}
	r.reopen(t)
	return r
}

func (r *stateRig) reopen(t *testing.T) {
	t.Helper()
	if r.st != nil {
		r.st.store.Close()
	}
	s, err := store.Open(r.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r.st, err = NewHostState(HostStateOptions{
		Store: s, Host: "mlab", OwnerRuleSupported: true, Initial: wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}},
		Now: func() time.Time { return r.now }, PidAlive: func(p int) bool { return r.alive[p] }, Events: r.b,
		PidIdent: func(p int) (string, bool) { return "id", r.alive[p] },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.ch, _ = r.b.Subscribe(false)
}

func exc(name string, in time.Duration, holds ...wire.Hold) wire.Exception {
	u := s0.Add(in)
	return wire.Exception{Name: name, Until: &u, Holds: holds}
}

func TestWorkerSettingsStartFromConfigThenFromTheDatabase(t *testing.T) {
	r := newRig(t)
	if got := r.st.Worker(); !got.Enabled || got.Slots != (wire.Slots{Heavy: 1, Light: 2}) {
		t.Fatalf("config start: %+v", got)
	}
	want := wire.WorkerSettings{Enabled: false, Slots: wire.Slots{Heavy: 0, Light: 1}, OwnerRule: "hid<3m"}
	changed, err := r.st.SetWorker(want)
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	r.reopen(t)                                       // a restart keeps what was set
	if got := r.st.Worker(); !sameWorker(got, want) { // the floor is the default in both (want carries none)
		t.Fatalf("after restart: %+v", got)
	}
}

func TestWorkerEventOnlyWhenSomethingChanged(t *testing.T) {
	r := newRig(t)
	same := r.st.Worker()
	if changed, err := r.st.SetWorker(same); err != nil || changed {
		t.Fatalf("an identical PUT: changed=%v err=%v", changed, err)
	}
	none(t, r.ch)
	next := same
	next.Slots.Heavy = 0
	if changed, _ := r.st.SetWorker(next); !changed {
		t.Fatal("a real change was not reported")
	}
	e := recv(t, r.ch)
	if e.Type != wire.EventWorker {
		t.Fatalf("got %s", e.Type)
	}
	ev, err := wire.Decode[wire.WorkerEvent](e.Data)
	if err != nil || ev.Host != "mlab" || ev.Worker.Slots.Heavy != 0 {
		t.Fatalf("%+v %v", ev, err)
	}
	none(t, r.ch) // once
}

func TestWorkerSettingsAreValidated(t *testing.T) {
	r := newRig(t)
	for _, bad := range []wire.WorkerSettings{
		{Slots: wire.Slots{Heavy: -1}},
		{Slots: wire.Slots{Light: -1}},
		{OwnerRule: string(make([]byte, 300))},
	} {
		if _, err := r.st.SetWorker(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if got := r.st.Worker(); got.Slots != (wire.Slots{Heavy: 1, Light: 2}) {
		t.Errorf("a refused PUT changed the settings: %+v", got)
	}
}

func TestExceptionLifecycleAndEventsOnce(t *testing.T) {
	r := newRig(t)
	e := exc("app-dev-package", time.Hour, wire.HoldLight)
	changed, err := r.st.AddException(e)
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	ev := recv(t, r.ch)
	if ev.Type != wire.EventException {
		t.Fatal(ev.Type)
	}
	x, _ := wire.Decode[wire.ExceptionEvent](ev.Data)
	if x.Name != "app-dev-package" || !x.Active || x.Exception == nil || x.Host != "mlab" {
		t.Fatalf("%+v", x)
	}
	// the same request again changes nothing and says nothing
	if changed, _ := r.st.AddException(e); changed {
		t.Error("an identical POST was reported as a change")
	}
	none(t, r.ch)

	holds := r.st.Holds()
	if len(holds) != 1 || holds[0].Name != "app-dev-package" || !holds[0].Light {
		t.Fatalf("holds %+v", holds)
	}
	fe := r.st.Active()
	if len(fe) != 1 || fe[0].Name != "app-dev-package" || !fe[0].Since.Equal(s0) || fe[0].Until == nil {
		t.Fatalf("facts exceptions %+v", fe)
	}

	if ok, err := r.st.RemoveException("app-dev-package"); err != nil || !ok {
		t.Fatalf("remove: ok=%v err=%v", ok, err)
	}
	x, _ = wire.Decode[wire.ExceptionEvent](recv(t, r.ch).Data)
	if x.Active || x.Name != "app-dev-package" {
		t.Fatalf("%+v", x)
	}
	if ok, err := r.st.RemoveException("app-dev-package"); ok || err != nil {
		t.Errorf("removing twice: ok=%v err=%v", ok, err)
	}
	none(t, r.ch)
	if len(r.st.Active()) != 0 || len(r.st.Holds()) != 0 {
		t.Fatal("still active")
	}
}

// A removal the database could not make must not look done: the exception would
// come back after a restart (R1).
func TestRemoveExceptionReportsAStorageFailureAndKeepsTheException(t *testing.T) {
	r := newRig(t)
	if _, err := r.st.AddException(exc("keeps", time.Hour)); err != nil {
		t.Fatal(err)
	}
	recv(t, r.ch)
	r.st.store.Close() // the database goes away
	ok, err := r.st.RemoveException("keeps")
	if err == nil || ok {
		t.Fatalf("ok=%v err=%v: a failed delete must be an error", ok, err)
	}
	if len(r.st.Active()) != 1 {
		t.Error("the exception was dropped from memory though its row is still there")
	}
	none(t, r.ch) // and nothing was announced
}

func TestExceptionsEndByTimeOrByTheirProcess(t *testing.T) {
	r := newRig(t)
	if _, err := r.st.AddException(exc("timed", 10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	pid := 777
	r.alive[pid] = true
	if _, err := r.st.AddException(wire.Exception{Name: "dev-server", PID: &pid, Holds: []wire.Hold{}}); err != nil {
		t.Fatal(err)
	}
	recv(t, r.ch)
	recv(t, r.ch)
	r.st.Prune(r.now.Add(5 * time.Minute))
	if len(r.st.Active()) != 2 {
		t.Fatal("ended too early")
	}
	r.alive[pid] = false // the dev server exited
	r.st.Prune(r.now.Add(6 * time.Minute))
	x, _ := wire.Decode[wire.ExceptionEvent](recv(t, r.ch).Data)
	if x.Name != "dev-server" || x.Active {
		t.Fatalf("%+v", x)
	}
	r.now = r.now.Add(11 * time.Minute)
	r.st.Prune(r.now)
	x, _ = wire.Decode[wire.ExceptionEvent](recv(t, r.ch).Data)
	if x.Name != "timed" || x.Active {
		t.Fatalf("%+v", x)
	}
	r.st.Prune(r.now)
	none(t, r.ch) // an ended exception ends once
	if len(r.st.Active()) != 0 {
		t.Fatal("still active")
	}
}

func TestExceptionsSurviveARestartButNotTheirEnd(t *testing.T) {
	r := newRig(t)
	pid := 31
	r.alive[pid] = true
	_, _ = r.st.AddException(exc("keeps", time.Hour))
	_, _ = r.st.AddException(exc("lapses", time.Minute))
	_, _ = r.st.AddException(wire.Exception{Name: "dies", PID: &pid, Holds: []wire.Hold{}})
	r.now = r.now.Add(10 * time.Minute) // while wwd was down, one lapsed...
	r.alive[pid] = false                // ...and one process exited
	r.reopen(t)
	fe := r.st.Active()
	if len(fe) != 1 || fe[0].Name != "keeps" {
		t.Fatalf("after restart: %+v", fe)
	}
}

func TestExceptionRequestsAreValidated(t *testing.T) {
	r := newRig(t)
	past := exc("old", -time.Minute)
	pid := 5
	both := wire.Exception{Name: "x", Until: past.Until, PID: &pid}
	zero := 0
	for name, e := range map[string]wire.Exception{
		"in the past":   past,
		"no bound":      {Name: "x"},
		"both bounds":   both,
		"bad pid":       {Name: "x", PID: &zero},
		"no name":       exc("", time.Hour),
		"odd name":      exc("a b/c", time.Hour),
		"unknown hold":  exc("x", time.Hour, wire.Hold("gpu")),
		"overlong name": exc(string(make([]byte, 80)), time.Hour),
	} {
		if _, err := r.st.AddException(e); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// a pid that is not running is refused too: it would end at once
	dead := 99999
	if _, err := r.st.AddException(wire.Exception{Name: "ghost", PID: &dead}); err == nil {
		t.Error("an exception for a dead pid was accepted")
	}
	for i := 0; i < maxExceptions; i++ {
		if _, err := r.st.AddException(exc("e"+string(rune('a'+i%26))+string(rune('a'+i/26)), time.Hour)); err != nil {
			t.Fatalf("exception %d: %v", i, err)
		}
	}
	if _, err := r.st.AddException(exc("one-too-many", time.Hour)); err == nil {
		t.Error("the cap was not enforced")
	}
}

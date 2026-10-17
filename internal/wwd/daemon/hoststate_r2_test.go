package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

func TestParseOwnerRule(t *testing.T) {
	cases := []struct {
		in      string
		minutes int
		ok      bool
	}{
		{"", 0, true}, // the rule's default (5 minutes)
		{"hid<5m", 5, true},
		{"hid<1m", 1, true},
		{"hid<120m", 120, true},
		{"hid<0m", 0, false},
		{"hid<m", 0, false},
		{"hid<5", 0, false},
		{"idle<5m", 0, false},
		{" hid<5m", 0, false},
		{"hid<99999m", 0, false},
	}
	for _, c := range cases {
		m, ok := ParseOwnerRule(c.in)
		if ok != c.ok || (ok && m != c.minutes) {
			t.Errorf("%q: got %d,%v want %d,%v", c.in, m, ok, c.minutes, c.ok)
		}
	}
}

// owner_rule is not a label: on a host with the rule it must parse and then it
// drives the rule; on a host without the rule it is refused instead of saved (R2).
func TestOwnerRuleIsValidatedAgainstTheHost(t *testing.T) {
	mk := func(supported bool) *HostState {
		st, err := NewHostState(HostStateOptions{Host: "h", OwnerRuleSupported: supported, Initial: wire.WorkerSettings{Enabled: true}})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	with, without := mk(true), mk(false)
	if _, err := with.SetWorker(wire.WorkerSettings{OwnerRule: "hid<3m"}); err != nil {
		t.Errorf("a valid rule on a host that has the rule: %v", err)
	}
	if m, _ := ParseOwnerRule(with.Worker().OwnerRule); m != 3 {
		t.Errorf("rule not stored: %+v", with.Worker())
	}
	if _, err := with.SetWorker(wire.WorkerSettings{OwnerRule: "whatever"}); err == nil {
		t.Error("free text must not be saved as a rule")
	}
	if _, err := without.SetWorker(wire.WorkerSettings{OwnerRule: "hid<3m"}); err == nil {
		t.Error("a host without an owner rule accepted one")
	}
	if _, err := without.SetWorker(wire.WorkerSettings{OwnerRule: ""}); err != nil {
		t.Errorf("an empty rule is always fine: %v", err)
	}
}

// A pid exception is tied to the process it was made for: the same pid held by
// another process is not that process (R2).
func TestPIDExceptionDoesNotSurvivePidReuse(t *testing.T) {
	ident := map[int]string{900: "start-A"}
	alive := map[int]bool{900: true}
	now := s0
	path := filepath.Join(t.TempDir(), "wwd.db")
	open := func() *HostState {
		s, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		st, err := NewHostState(HostStateOptions{
			Store: s, Host: "h", Initial: wire.WorkerSettings{Enabled: true},
			Now:      func() time.Time { return now },
			PidAlive: func(p int) bool { return alive[p] },
			PidIdent: func(p int) (string, bool) { id, ok := ident[p]; return id, ok },
		})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	st := open()
	pid := 900
	if _, err := st.AddException(wire.Exception{Name: "dev-server", PID: &pid, Holds: []wire.Hold{}}); err != nil {
		t.Fatal(err)
	}
	st.Prune(now)
	if len(st.Active()) != 1 {
		t.Fatal("ended while its process still runs")
	}
	// the dev server exited and an unrelated process took the pid
	ident[900] = "start-B"
	st.Prune(now)
	if len(st.Active()) != 0 {
		t.Fatal("an unrelated process holding the pid kept the exception alive")
	}

	// the same across a restart
	ident[900] = "start-A"
	if _, err := st.AddException(wire.Exception{Name: "dev-server", PID: &pid, Holds: []wire.Hold{}}); err != nil {
		t.Fatal(err)
	}
	ident[900] = "start-C" // while wwd was down
	st2 := open()
	if len(st2.Active()) != 0 {
		t.Fatal("a reused pid brought the exception back after a restart")
	}
}

// facts shows the exceptions and the headroom they cause from one moment (R2).
func TestSnapshotIsOneConsistentView(t *testing.T) {
	st, err := NewHostState(HostStateOptions{Host: "h", Initial: wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	u := time.Now().Add(time.Hour)
	if _, err := st.AddException(wire.Exception{Name: "x", Until: &u, Holds: []wire.Hold{wire.HoldLight}}); err != nil {
		t.Fatal(err)
	}
	snap := st.Snapshot(time.Now())
	if len(snap.Exceptions) != 1 || len(snap.Holds) != 1 || !snap.Holds[0].Light || snap.Worker.Slots.Heavy != 1 {
		t.Fatalf("%+v", snap)
	}
	if snap.Exceptions[0].Name != snap.Holds[0].Name {
		t.Error("the two lists describe different exceptions")
	}
}

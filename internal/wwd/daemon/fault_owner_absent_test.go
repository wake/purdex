package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/fault"
	"github.com/wake/purdex/internal/wwd/wire"
)

// Without an owner rule ([owner] off) the owner block is nil, and a worker that yielded stays yielded on a nil block (yield.Observe: only
// a reading that says the owner is away ends a yield). So with H2 named the block says "away" while the file is absent: the hook then
// drives both ways, file there -> yield, file gone -> back.
func TestFaultOwnerFileAbsentSaysAwayWithoutAnOwnerRule(t *testing.T) {
	file := filepath.Join(t.TempDir(), "owner-active")
	level := 1
	s := faultSampler(fault.New(fault.Config{Enabled: true, OwnerActiveFile: file}, nil), &level, nil)
	s.Tick(s0)
	f, _ := s.Latest()
	if f.Owner == nil || f.Owner.Active || !strings.HasPrefix(f.Owner.Reason, "fault:") || f.Derived.Headroom.Heavy != 1 {
		t.Fatalf("no file: owner %+v derived %+v", f.Owner, f.Derived)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.Tick(s0.Add(5 * time.Second))
	if f, _ = s.Latest(); f.Owner == nil || !f.Owner.Active || f.Derived.Headroom.Heavy != 1 { // no rule: the made-up reading is shown, not counted (#3457)
		t.Fatalf("file: owner %+v derived %+v", f.Owner, f.Derived)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	s.Tick(s0.Add(10 * time.Second))
	if f, _ = s.Latest(); f.Owner == nil || f.Owner.Active || f.Derived.Headroom.Heavy != 1 {
		t.Fatalf("file gone: owner %+v derived %+v", f.Owner, f.Derived)
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
}

// With a real owner rule the block is the rule's answer, never replaced by the absent-file one.
func TestFaultOwnerFileAbsentKeepsARealOwnerBlock(t *testing.T) {
	file := filepath.Join(t.TempDir(), "owner-active")
	level := 1
	real := &wire.OwnerState{Active: false, Reason: "idle"}
	s := faultSampler(fault.New(fault.Config{Enabled: true, OwnerActiveFile: file}, nil), &level, real)
	s.Tick(s0)
	if f, _ := s.Latest(); f.Owner == nil || f.Owner.Reason != "idle" {
		t.Fatalf("owner %+v", f.Owner)
	}
}

// A host that has an owner rule and whose reading failed this time (null block) is not "away": the yield stays.
func TestFaultOwnerFileAbsentDoesNotAnswerForAFailedReading(t *testing.T) {
	file := filepath.Join(t.TempDir(), "owner-active")
	level := 1
	s := NewSampler(SamplerOptions{
		HostID: "h1", Alias: "air26", OSVersion: "26.6.2", Fault: fault.New(fault.Config{Enabled: true, OwnerActiveFile: file}, nil),
		Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true, OwnerRule: "hid<5m",
	})
	s.Tick(s0)
	if f, _ := s.Latest(); f.Owner != nil {
		t.Fatalf("owner %+v", f.Owner)
	}
}

// "No owner rule" is the rule in force (f.Worker.OwnerRule, which a PUT /v1/worker changes), not the one the daemon started with: the
// matrix PUTs owner_rule "" and then the absent-file block must come, whatever the config said (#3449).
func TestFaultOwnerFileAbsentFollowsTheRuleInForce(t *testing.T) {
	file := filepath.Join(t.TempDir(), "owner-active")
	sampler := func(configRule, ruleInForce string) *Sampler {
		st, err := NewHostState(HostStateOptions{Host: "h", OwnerRuleSupported: true, Initial: wire.WorkerSettings{Enabled: true, OwnerRule: ruleInForce}})
		if err != nil {
			t.Fatal(err)
		}
		level := 1
		return NewSampler(SamplerOptions{
			HostID: "h1", Alias: "air26", OSVersion: "26.6.2", Fault: fault.New(fault.Config{Enabled: true, OwnerActiveFile: file}, nil),
			Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true, OwnerRule: configRule, State: st,
		})
	}
	// started with a rule, the rule in force is "" (a PUT): no owner rule now, the file is absent -> away
	s := sampler("hid<5m", "")
	s.Tick(s0)
	if f, _ := s.Latest(); f.Owner == nil || f.Owner.Active || !strings.HasPrefix(f.Owner.Reason, "fault:") {
		t.Fatalf("rule in force empty, config has one: owner %+v", f.Owner)
	}
	// started without a rule, a rule in force (hid<5m): a null block is a failed reading, the yield stays
	s = sampler("", "hid<5m")
	s.Tick(s0)
	if f, _ := s.Latest(); f.Owner != nil {
		t.Fatalf("rule in force, config has none: owner %+v", f.Owner)
	}
}

// A file that cannot be told about (here: its parent is a file, ENOTDIR) is not "absent": nothing is made up, so a yield stays.
func TestFaultOwnerFileUnknownIsNotAway(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	level := 1
	s := faultSampler(fault.New(fault.Config{Enabled: true, OwnerActiveFile: filepath.Join(parent, "owner-active")}, nil), &level, nil)
	s.Tick(s0)
	if f, _ := s.Latest(); f.Owner != nil {
		t.Fatalf("owner %+v", f.Owner)
	}
}

// Another hook, or none, leaves a host without an owner rule with a null owner block.
func TestFaultOtherHooksLeaveTheOwnerNull(t *testing.T) {
	level := 1
	for name, h := range map[string]*fault.Hooks{
		"nil":  nil,
		"zone": fault.New(fault.Config{Enabled: true, Zone: "danger"}, nil),
	} {
		s := faultSampler(h, &level, nil)
		s.Tick(s0)
		if f, _ := s.Latest(); f.Owner != nil {
			t.Errorf("%s: owner %+v", name, f.Owner)
		}
	}
}

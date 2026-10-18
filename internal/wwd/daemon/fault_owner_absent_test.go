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
	if f, _ = s.Latest(); f.Owner == nil || !f.Owner.Active || f.Derived.Headroom != (wire.Slots{}) {
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

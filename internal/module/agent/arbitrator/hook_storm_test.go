package arbitrator

import (
	"testing"
	"time"
)

func TestHookStorm_UnderCap_Allows(t *testing.T) {
	g := newHookStormGuard(10*time.Millisecond, 50)
	now := time.Unix(0, 0)
	for i := 0; i < 49; i++ {
		if g.ShouldDrop("s1", now.Add(time.Duration(i)*time.Microsecond)) {
			t.Fatalf("drop at i=%d (under cap, within window)", i)
		}
	}
}

func TestHookStorm_51stObservationIn10ms_Drops(t *testing.T) {
	g := newHookStormGuard(10*time.Millisecond, 50)
	start := time.Unix(0, 0)
	// First 50 should all be allowed.
	for i := 0; i < 50; i++ {
		ts := start.Add(time.Duration(i) * 10 * time.Microsecond) // well within 10ms
		if g.ShouldDrop("s1", ts) {
			t.Fatalf("dropped at i=%d", i)
		}
	}
	// 51st must drop.
	ts51 := start.Add(500 * time.Microsecond) // still within window
	if !g.ShouldDrop("s1", ts51) {
		t.Error("51st observation within window should drop")
	}
	// 52nd also drops.
	ts52 := start.Add(600 * time.Microsecond)
	if !g.ShouldDrop("s1", ts52) {
		t.Error("52nd observation within window should drop")
	}
}

func TestHookStorm_SessionIsolated_OneBurstDoesntAffectOther(t *testing.T) {
	g := newHookStormGuard(10*time.Millisecond, 50)
	start := time.Unix(0, 0)
	for i := 0; i < 60; i++ {
		g.ShouldDrop("hot", start.Add(time.Duration(i)*time.Microsecond))
	}
	// Cold session first obs should be allowed.
	if g.ShouldDrop("cold", start.Add(time.Millisecond)) {
		t.Error("cold session first obs dropped — sessions not isolated")
	}
}

func TestHookStorm_WindowReset_AllowsNewObs(t *testing.T) {
	g := newHookStormGuard(10*time.Millisecond, 50)
	start := time.Unix(0, 0)
	// Saturate window.
	for i := 0; i < 60; i++ {
		g.ShouldDrop("s1", start.Add(time.Duration(i)*time.Microsecond))
	}
	// Advance past the window boundary.
	afterWindow := start.Add(11 * time.Millisecond)
	if g.ShouldDrop("s1", afterWindow) {
		t.Error("observation after window expiry should be allowed (window reset)")
	}
	// Post-reset counter should start fresh (49 more allowed).
	for i := 1; i < 50; i++ {
		ts := afterWindow.Add(time.Duration(i) * time.Microsecond)
		if g.ShouldDrop("s1", ts) {
			t.Fatalf("post-reset drop at i=%d", i)
		}
	}
	// 51st (cap reached) drops.
	if !g.ShouldDrop("s1", afterWindow.Add(5*time.Millisecond)) {
		t.Error("post-reset 51st observation should drop")
	}
}

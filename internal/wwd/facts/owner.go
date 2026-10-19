package facts

import (
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// OwnerConfig is the owner rule's setting (worker settings, spec §3.1).
type OwnerConfig struct {
	IdleMinutes int // HID idle below this means the owner is using the machine; 0: 5
	// UID restricts the build and simulator rule to this user's processes (wwd
	// runs as the owner, so the caller passes its own uid). 0 accepts any user.
	UID int
}

func (c OwnerConfig) idleLimit() float64 {
	m := c.IdleMinutes
	if m <= 0 {
		m = 5
	}
	return float64(m) * 60
}

// OwnerSources are the two reads the rule needs beyond the process table.
type OwnerSources struct {
	IdleSeconds func() (float64, error) // seconds since the last keyboard/mouse event
	Locked      func() (bool, error)    // the screen is locked
}

// ownerBuildProcs are the processes of an iOS/macOS build that the owner runs by
// hand (M-S2). comm is cut at 16 characters, all of these fit.
var ownerBuildProcs = map[string]bool{
	"xcodebuild": true, "XCBBuildService": true, "SWBBuildService": true,
	"swift-frontend": true, "swift-build": true,
}

// BuildOwner is the air26 "owner is active" rule (spec §3.1, M-S2). Active when
//  1. HID idle time is under the limit (reason "hid"), or
//  2. the screen is unlocked and a build of the owner's runs ("build") or a
//     simulator device is booted ("simulator").
//
// Processes of wwd's own jobs are excluded by the shared job check. The front
// app, the load average and pmset's UserIsActive are not used (M-S2 measured
// them unusable). The lock state is read only when a candidate process exists.
// An unreadable idle time is "unknown" (not active) unless rule 2 decides. An
// unreadable lock state counts as unlocked on purpose: the failure then makes
// the worker yield to the owner instead of competing with their build.
func BuildOwner(cfg OwnerConfig, src OwnerSources, snap ProcSnapshot, ctx ProcContext) *wire.OwnerState {
	out := &wire.OwnerState{}
	idle, err := src.IdleSeconds()
	idleKnown := err == nil
	if idleKnown {
		out.IdleS = int(idle)
		if idle < cfg.idleLimit() {
			out.Active, out.Reason = true, "hid"
			return out
		}
	}
	if reason := ownerWorkReason(cfg, snap, ctx); reason != "" {
		locked := false
		if src.Locked != nil {
			if l, err := src.Locked(); err == nil {
				locked = l // unreadable counts as unlocked
			}
		}
		if !locked {
			out.Active, out.Reason = true, reason
			return out
		}
	}
	if idleKnown {
		out.Reason = "idle"
	} else {
		out.Reason = "unknown"
	}
	return out
}

// OverrideOwner puts a test hook in front of the owner rule: while override answers non-nil, that block is the owner and the real rule
// (the HID idle time included) is not read at all, so who is at the keyboard of the host a test runs on cannot change its result (#3561).
// Without a rule (real nil) there is nothing to override. override nil or answering nil leaves the real rule in force.
func OverrideOwner(real func(ProcSnapshot, ProcContext) *wire.OwnerState, override func() *wire.OwnerState) func(ProcSnapshot, ProcContext) *wire.OwnerState {
	if real == nil || override == nil {
		return real
	}
	return func(s ProcSnapshot, c ProcContext) *wire.OwnerState {
		if o := override(); o != nil {
			return o
		}
		return real(s, c)
	}
}

// ownerWorkReason is "build" or "simulator" when the owner has such work
// running, "" otherwise. A build wins over a simulator.
func ownerWorkReason(cfg OwnerConfig, snap ProcSnapshot, ctx ProcContext) string {
	reason := ""
	for _, p := range snap.Procs {
		if cfg.UID != 0 && p.UID != cfg.UID {
			continue
		}
		var r string
		switch {
		case ownerBuildProcs[p.Name]:
			r = "build"
		case strings.HasPrefix(p.Name, "SimLaunchHost"):
			r = "simulator"
		default:
			continue
		}
		if _, job := jobAbove(p, ctx); job {
			continue // a wwd job's own build is not the owner's
		}
		if r == "build" {
			return "build"
		}
		reason = r
	}
	return reason
}

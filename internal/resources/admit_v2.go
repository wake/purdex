package resources

import (
	"slices"
	"time"
)

// V2Reason is why a waiter was not granted in one pass (lease v2 spec D-7
// wait_reasons).
type V2Reason string

const (
	ReasonGap         V2Reason = "gap"          // inside the pause after the last grant
	ReasonDanger      V2Reason = "danger"       // host in the danger zone
	ReasonWarningFlat V2Reason = "warning_flat" // no longer produced (P2e: flat in warning is granted); kept for rows and reports written before
	ReasonRising      V2Reason = "rising"       // a lease running and usage rising (or unknown)
	ReasonBehind      V2Reason = "behind"       // not the head of the queue
	ReasonCut         V2Reason = "cut"          // the head is held back by a baseline or disk guard
	ReasonParked      V2Reason = "parked"       // parked: keeps its place, never granted
)

// V2Waiter is one waiting request as AdmitV2 sees it.
type V2Waiter struct {
	ID        string
	CreatedAt time.Time
	// Parked waiters keep their place but are skipped.
	Parked bool
	// Cut is the caller's own hold on this waiter (a session-new request
	// without a baseline, a disk-guarded kind during a trim). When it is the
	// head, nothing behind it is considered.
	Cut bool
}

// AdmitV2 decides one admission pass (lease v2 spec D-3′): at most one grant,
// strictly the head of the queue.
//
// running is whether any row is held, of any kind. lastGrant is the newest
// granted_at of any row, held or ended (zero when nothing was ever granted).
// waiters may come in any order; they are taken by created_at, ties in the
// caller's order. The input is not modified.
//
// Parked waiters get ReasonParked and are skipped; the first other waiter is
// the head and everyone after it is ReasonBehind. A cut head stops the walk.
// Inside the gap after lastGrant the head waits (ReasonGap). Otherwise the
// spec table decides:
//
//	zone     none running   falling   flat      rising or unknown trend
//	normal   grant          grant     grant     observe (rising)
//	warning  grant          grant     observe   observe (rising)
//	danger   observe        observe   observe   observe
//	unknown  grant          grant     grant     grant        (fail open)
//
// The returned reasons name every waiter that was not granted; the granted
// one is absent. gapLeft is what remains of the gap at now, also when it did
// not decide the pass.
func AdmitV2(zone Zone, trend Trend, running bool, lastGrant time.Time, waiters []V2Waiter, now time.Time, s Settings) (grant string, gapLeft time.Duration, reasons map[string]V2Reason) {
	if !lastGrant.IsZero() {
		gapLeft = max(lastGrant.Add(s.Gap()).Sub(now), 0)
	}
	reasons = make(map[string]V2Reason, len(waiters))
	ordered := slices.Clone(waiters)
	slices.SortStableFunc(ordered, func(a, b V2Waiter) int { return a.CreatedAt.Compare(b.CreatedAt) })

	headSeen := false
	for _, w := range ordered {
		switch {
		case w.Parked:
			reasons[w.ID] = ReasonParked
		case headSeen:
			reasons[w.ID] = ReasonBehind
		default:
			headSeen = true
			if r, ok := headReason(w, zone, trend, running, gapLeft); ok {
				reasons[w.ID] = r
			} else {
				grant = w.ID
			}
		}
	}
	return grant, gapLeft, reasons
}

// headReason is why the head waits, or ok = false when it is granted.
func headReason(w V2Waiter, zone Zone, trend Trend, running bool, gapLeft time.Duration) (V2Reason, bool) {
	switch {
	case w.Cut:
		return ReasonCut, true
	case gapLeft > 0:
		return ReasonGap, true
	}
	switch zone {
	case ZoneNormal:
		if running && !(trend == TrendFalling || trend == TrendFlat) {
			return ReasonRising, true
		}
	case ZoneWarning:
		if running {
			switch trend {
			case TrendFalling, TrendFlat: // flat is let through since P2e (G1, user 2026-10-14: option c)
			default:
				return ReasonRising, true
			}
		}
	case ZoneDanger:
		return ReasonDanger, true
	}
	return "", false // none running, falling, or the unknown zone: fail open
}

// V2Record is what a grant writes about the decision (the dec_zone,
// dec_trend, dec_running and dec_gap_ms columns).
type V2Record struct {
	Zone    string
	Trend   string
	Running bool
	GapMS   int64
}

// V2Decision builds the record of a decision from the inputs AdmitV2 saw and
// the gap it reported.
func V2Decision(zone Zone, trend Trend, running bool, gapLeft time.Duration) V2Record {
	return V2Record{Zone: zone.String(), Trend: trend.String(), Running: running, GapMS: gapLeft.Milliseconds()}
}

package resources

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var v2Now = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func v2w(id string, ageS int) V2Waiter {
	return V2Waiter{ID: id, CreatedAt: v2Now.Add(-time.Duration(ageS) * time.Second)}
}

// longAgo is a last grant far enough back that no gap is left.
var longAgo = v2Now.Add(-time.Hour)

// One case per cell of the spec D-3′ table (the 13 cells, with the unknown
// zone row's trend variants folded into one).
func TestAdmitV2_Table(t *testing.T) {
	s := DefaultSettings()
	for _, c := range []struct {
		name    string
		zone    Zone
		trend   Trend
		running bool
		grant   bool
		reason  V2Reason // when observed
	}{
		{"normal, none running", ZoneNormal, TrendRising, false, true, ""},
		{"normal, running, falling", ZoneNormal, TrendFalling, true, true, ""},
		{"normal, running, flat", ZoneNormal, TrendFlat, true, true, ""},
		{"normal, running, rising", ZoneNormal, TrendRising, true, false, ReasonRising},
		{"normal, running, unknown trend counts as rising", ZoneNormal, TrendUnknown, true, false, ReasonRising},
		{"warning, none running", ZoneWarning, TrendRising, false, true, ""},
		{"warning, running, falling", ZoneWarning, TrendFalling, true, true, ""},
		{"warning, running, flat is let through (P2e)", ZoneWarning, TrendFlat, true, true, ""},
		{"warning, running, rising", ZoneWarning, TrendRising, true, false, ReasonRising},
		{"warning, running, unknown trend counts as rising", ZoneWarning, TrendUnknown, true, false, ReasonRising},
		{"danger, none running", ZoneDanger, TrendFalling, false, false, ReasonDanger},
		{"danger, running, falling", ZoneDanger, TrendFalling, true, false, ReasonDanger},
		{"danger, running, flat", ZoneDanger, TrendFlat, true, false, ReasonDanger},
		{"danger, running, rising", ZoneDanger, TrendRising, true, false, ReasonDanger},
		{"unknown zone fails open, none running", ZoneUnknown, TrendUnknown, false, true, ""},
		{"unknown zone fails open, rising", ZoneUnknown, TrendRising, true, true, ""},
	} {
		grant, gapLeft, reasons := AdmitV2(c.zone, c.trend, c.running, longAgo, []V2Waiter{v2w("a", 5)}, v2Now, s)
		assert.Zero(t, gapLeft, c.name)
		if c.grant {
			assert.Equal(t, "a", grant, c.name)
			assert.Empty(t, reasons, c.name)
			continue
		}
		assert.Empty(t, grant, c.name)
		assert.Equal(t, map[string]V2Reason{"a": c.reason}, reasons, c.name)
	}
}

// The gap runs from the last grant: 29.9 s is still inside, 30 s is out.
func TestAdmitV2_GapBoundary(t *testing.T) {
	s := DefaultSettings()
	w := []V2Waiter{v2w("a", 5)}

	grant, gapLeft, reasons := AdmitV2(ZoneNormal, TrendFlat, false, v2Now.Add(-29900*time.Millisecond), w, v2Now, s)
	assert.Empty(t, grant)
	assert.Equal(t, 100*time.Millisecond, gapLeft)
	assert.Equal(t, map[string]V2Reason{"a": ReasonGap}, reasons)

	grant, gapLeft, reasons = AdmitV2(ZoneNormal, TrendFlat, false, v2Now.Add(-30*time.Second), w, v2Now, s)
	assert.Equal(t, "a", grant)
	assert.Zero(t, gapLeft)
	assert.Empty(t, reasons)

	// A gap of 0 turns it off.
	zero := Settings{GapS: intp(0)}.Effective()
	grant, _, _ = AdmitV2(ZoneNormal, TrendFlat, false, v2Now, w, v2Now, zero)
	assert.Equal(t, "a", grant)

	// The gap is a setting.
	long := Settings{GapS: intp(120)}.Effective()
	grant, gapLeft, _ = AdmitV2(ZoneNormal, TrendFlat, false, v2Now.Add(-100*time.Second), w, v2Now, long)
	assert.Empty(t, grant)
	assert.Equal(t, 20*time.Second, gapLeft)
}

// The gap counts from the newest grant of any row, also one that has ended:
// the caller passes nothing running, only the old grant time.
func TestAdmitV2_GapCountsFromAnEndedGrant(t *testing.T) {
	s := DefaultSettings()
	grant, gapLeft, reasons := AdmitV2(ZoneNormal, TrendFlat, false, v2Now.Add(-5*time.Second),
		[]V2Waiter{v2w("a", 20)}, v2Now, s)
	assert.Empty(t, grant)
	assert.Equal(t, 25*time.Second, gapLeft)
	assert.Equal(t, ReasonGap, reasons["a"])
}

// No grant ever (zero time) is no gap.
func TestAdmitV2_NoGrantEverMeansNoGap(t *testing.T) {
	grant, gapLeft, _ := AdmitV2(ZoneNormal, TrendFlat, false, time.Time{}, []V2Waiter{v2w("a", 5)}, v2Now, DefaultSettings())
	assert.Equal(t, "a", grant)
	assert.Zero(t, gapLeft)
}

// Only the head is considered; everyone behind it is "behind", whatever the
// head's own reason, and at most one is granted.
func TestAdmitV2_HeadOnlyAndAtMostOne(t *testing.T) {
	s := DefaultSettings()
	// Passed out of order on purpose: the oldest created_at is the head.
	ws := []V2Waiter{v2w("b", 20), v2w("a", 30), v2w("c", 10)}

	grant, _, reasons := AdmitV2(ZoneNormal, TrendFlat, false, longAgo, ws, v2Now, s)
	assert.Equal(t, "a", grant, "the oldest created_at goes first")
	assert.Equal(t, map[string]V2Reason{"b": ReasonBehind, "c": ReasonBehind}, reasons)

	grant, _, reasons = AdmitV2(ZoneDanger, TrendFlat, true, longAgo, ws, v2Now, s)
	assert.Empty(t, grant)
	assert.Equal(t, map[string]V2Reason{"a": ReasonDanger, "b": ReasonBehind, "c": ReasonBehind}, reasons)

	grant, _, reasons = AdmitV2(ZoneNormal, TrendFlat, false, v2Now.Add(-time.Second), ws, v2Now, s)
	assert.Empty(t, grant)
	assert.Equal(t, map[string]V2Reason{"a": ReasonGap, "b": ReasonBehind, "c": ReasonBehind}, reasons)
}

// Equal created_at keeps the caller's order (stable), so a pass cannot flap
// between two waiters of the same instant.
func TestAdmitV2_TieKeepsCallerOrder(t *testing.T) {
	ws := []V2Waiter{v2w("x", 10), v2w("y", 10)}
	grant, _, _ := AdmitV2(ZoneNormal, TrendFlat, false, longAgo, ws, v2Now, DefaultSettings())
	assert.Equal(t, "x", grant)
}

// A parked waiter keeps its place but is skipped: the next present one is the
// head and can be granted.
func TestAdmitV2_ParkedHeadIsSkipped(t *testing.T) {
	s := DefaultSettings()
	p := v2w("a", 30)
	p.Parked = true
	ws := []V2Waiter{p, v2w("b", 20), v2w("c", 10)}

	grant, _, reasons := AdmitV2(ZoneNormal, TrendFlat, false, longAgo, ws, v2Now, s)
	assert.Equal(t, "b", grant)
	assert.Equal(t, map[string]V2Reason{"a": ReasonParked, "c": ReasonBehind}, reasons)

	// A parked waiter is never granted even when it is alone.
	grant, _, reasons = AdmitV2(ZoneNormal, TrendFlat, false, longAgo, []V2Waiter{p}, v2Now, s)
	assert.Empty(t, grant)
	assert.Equal(t, map[string]V2Reason{"a": ReasonParked}, reasons)

	// The next present head still waits for its own reason.
	grant, _, reasons = AdmitV2(ZoneDanger, TrendFlat, true, longAgo, ws, v2Now, s)
	assert.Empty(t, grant)
	assert.Equal(t, map[string]V2Reason{"a": ReasonParked, "b": ReasonDanger, "c": ReasonBehind}, reasons)

	// A parked one in the middle changes nothing for the others.
	mid := v2w("m", 15)
	mid.Parked = true
	grant, _, reasons = AdmitV2(ZoneNormal, TrendFlat, false, longAgo, []V2Waiter{v2w("a", 30), mid, v2w("c", 10)}, v2Now, s)
	assert.Equal(t, "a", grant)
	assert.Equal(t, map[string]V2Reason{"m": ReasonParked, "c": ReasonBehind}, reasons)
}

// A cut head (no baseline yet, or a disk guard) stops the walk: nobody behind
// it is considered, even a calm host.
func TestAdmitV2_CutHeadStopsTheWalk(t *testing.T) {
	s := DefaultSettings()
	cut := v2w("a", 30)
	cut.Cut = true
	ws := []V2Waiter{cut, v2w("b", 20)}

	grant, _, reasons := AdmitV2(ZoneNormal, TrendFlat, false, longAgo, ws, v2Now, s)
	assert.Empty(t, grant)
	assert.Equal(t, map[string]V2Reason{"a": ReasonCut, "b": ReasonBehind}, reasons)

	// Cut wins over the gap and over a fail-open zone.
	grant, _, reasons = AdmitV2(ZoneUnknown, TrendUnknown, false, v2Now.Add(-time.Second), ws, v2Now, s)
	assert.Empty(t, grant)
	assert.Equal(t, ReasonCut, reasons["a"])

	// Cut belongs to the head only: a cut waiter behind a granted head is
	// just behind.
	later := v2w("c", 10)
	later.Cut = true
	grant, _, reasons = AdmitV2(ZoneNormal, TrendFlat, false, longAgo, []V2Waiter{v2w("a", 30), later}, v2Now, s)
	assert.Equal(t, "a", grant)
	assert.Equal(t, map[string]V2Reason{"c": ReasonBehind}, reasons)

	// A parked cut waiter is skipped like any parked one.
	pc := v2w("p", 40)
	pc.Cut, pc.Parked = true, true
	grant, _, reasons = AdmitV2(ZoneNormal, TrendFlat, false, longAgo, []V2Waiter{pc, v2w("a", 30)}, v2Now, s)
	assert.Equal(t, "a", grant)
	assert.Equal(t, map[string]V2Reason{"p": ReasonParked}, reasons)
}

// Nobody waiting is an empty answer, and the gap is still reported.
func TestAdmitV2_NoWaiters(t *testing.T) {
	grant, gapLeft, reasons := AdmitV2(ZoneNormal, TrendFlat, true, v2Now.Add(-10*time.Second), nil, v2Now, DefaultSettings())
	assert.Empty(t, grant)
	assert.Equal(t, 20*time.Second, gapLeft)
	assert.Empty(t, reasons)
}

// The caller's slice is not reordered.
func TestAdmitV2_DoesNotMutateTheInput(t *testing.T) {
	ws := []V2Waiter{v2w("b", 10), v2w("a", 30)}
	AdmitV2(ZoneNormal, TrendFlat, false, longAgo, ws, v2Now, DefaultSettings())
	assert.Equal(t, "b", ws[0].ID)
}

func TestV2Decision(t *testing.T) {
	d := V2Decision(ZoneWarning, TrendFalling, true, 1500*time.Millisecond)
	assert.Equal(t, V2Record{Zone: "warning", Trend: "falling", Running: true, GapMS: 1500}, d)
	assert.Equal(t, V2Record{Zone: "unknown", Trend: "unknown"}, V2Decision(ZoneUnknown, TrendUnknown, false, 0))
}

package resources

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// wt builds a waiter enqueued ago before t0 whose deadline is deadlineIn from t0
// (negative: already past).
func wt(id string, weight int, ago, deadlineIn time.Duration) Waiter {
	return Waiter{ID: id, Weight: weight, EnqueuedAt: t0.Add(-ago), Deadline: t0.Add(deadlineIn)}
}

// held is a lease long past warmup, so its charge is max(measured, floor).
func held(id string, weight int, measured float64) Lease {
	return lease(id, weight, time.Hour, measured)
}

// TestAdmit_Table walks D-2 and D-3: every row names the situation it pins.
func TestAdmit_Table(t *testing.T) {
	const later = 5 * time.Minute
	for _, c := range []struct {
		name      string
		measured  int
		missing   bool // host sample unavailable
		leases    []Lease
		use       map[string]float64
		waiters   []Waiter
		wantGrant []string
		wantOver  []string
	}{
		{
			name: "empty host grants", measured: 0,
			waiters:   []Waiter{wt("a", 45, 0, later)},
			wantGrant: []string{"a"},
		},
		{
			name: "nothing waiting grants nothing", measured: 40,
			leases: []Lease{held("l", 45, 40)}, use: map[string]float64{"l": 40},
		},
		{
			// the "unknown heavy work blocks" property: 80 of measured use that no
			// lease owns leaves no room for a 45
			name: "unleased use blocks", measured: 80,
			waiters: []Waiter{wt("a", 45, 0, later)},
		},
		{
			name: "unleased use that leaves room does not block", measured: 50,
			waiters:   []Waiter{wt("a", 45, 0, later)},
			wantGrant: []string{"a"}, // 0 + 50 + 45 = 95
		},
		{
			// 45 held + 5 idle: the second test-full fits (95), a third does not
			name: "two test-full and idle 5: second fits, third waits", measured: 50,
			leases: []Lease{held("l", 45, 45)}, use: map[string]float64{"l": 45},
			waiters:   []Waiter{wt("b", 45, 2*time.Second, later), wt("c", 45, time.Second, later)},
			wantGrant: []string{"b"},
		},
		{
			name: "two test-full held and a third: waits", measured: 95,
			leases: []Lease{held("l1", 45, 45), held("l2", 45, 45)}, use: map[string]float64{"l1": 45, "l2": 45},
			waiters: []Waiter{wt("c", 45, 0, later)},
		},
		{
			name: "two test-full and idle 15: second waits", measured: 60,
			leases: []Lease{held("l", 45, 45)}, use: map[string]float64{"l": 45},
			waiters: []Waiter{wt("b", 45, 0, later)}, // 45 + 15 + 45 = 105
		},
		{
			name: "warmup charges the weight: waits", measured: 5,
			leases: []Lease{lease("l", 60, 5*time.Second, 5)}, use: map[string]float64{"l": 5},
			waiters: []Waiter{wt("a", 45, 0, later)}, // 60 + 0 + 45 = 105
		},
		{
			name: "after warmup the EWMA is charged: fits", measured: 30,
			leases: []Lease{held("l", 60, 30)}, use: map[string]float64{"l": 30},
			waiters:   []Waiter{wt("a", 45, 0, later)}, // 30 + 0 + 45 = 75
			wantGrant: []string{"a"},
		},
		{
			// floor row: measured 5 would charge 5 (5 + 75 = 80 fits); the floor
			// charges 30 (30 + 75 = 105 waits)
			name: "floor applies after warmup: waits", measured: 5,
			leases: []Lease{held("l", 60, 5)}, use: map[string]float64{"l": 5},
			waiters: []Waiter{wt("a", 75, 0, later)},
		},
		{
			name: "exactly 100 fits", measured: 55,
			leases: []Lease{held("l", 55, 55)}, use: map[string]float64{"l": 55},
			waiters:   []Waiter{wt("a", 45, 0, later)},
			wantGrant: []string{"a"},
		},
		{
			name: "101 does not fit", measured: 55,
			leases: []Lease{held("l", 55, 55)}, use: map[string]float64{"l": 55},
			waiters: []Waiter{wt("a", 46, 0, later)},
		},
		{
			// the earlier waiter fits and is granted first, so the later one
			// (which would also fit alone) now waits behind the charge
			name: "FIFO: the earlier waiter that fits goes first", measured: 50,
			leases: []Lease{held("l", 50, 50)}, use: map[string]float64{"l": 50},
			waiters:   []Waiter{wt("heavy", 45, 2*time.Second, later), wt("light", 10, time.Second, later)},
			wantGrant: []string{"heavy"},
		},
		{
			name: "a light later waiter passes a heavy one that does not fit", measured: 60,
			leases: []Lease{held("l", 60, 60)}, use: map[string]float64{"l": 60},
			waiters:   []Waiter{wt("heavy", 45, 2*time.Second, later), wt("light", 10, time.Second, later)},
			wantGrant: []string{"light"},
		},
		{
			name: "FIFO follows enqueue time, not slice order", measured: 50,
			leases: []Lease{held("l", 50, 50)}, use: map[string]float64{"l": 50},
			waiters:   []Waiter{wt("light", 10, time.Second, later), wt("heavy", 45, 2*time.Second, later)},
			wantGrant: []string{"heavy"},
		},
		{
			name: "same enqueue time keeps the given order", measured: 50,
			leases: []Lease{held("l", 50, 50)}, use: map[string]float64{"l": 50},
			waiters:   []Waiter{wt("first", 45, time.Second, later), wt("second", 45, time.Second, later)},
			wantGrant: []string{"first"},
		},
		{
			name: "grants add up within one pass", measured: 0,
			waiters:   []Waiter{wt("a", 45, 3*time.Second, later), wt("b", 45, 2*time.Second, later), wt("c", 45, time.Second, later)},
			wantGrant: []string{"a", "b"}, // 45, 90, then 135 waits
		},
		{
			name: "deadline passed: overrun grant even when full", measured: 95,
			waiters:  []Waiter{wt("a", 45, time.Hour, -time.Second)},
			wantOver: []string{"a"},
		},
		{
			name: "deadline exactly now counts as passed", measured: 95,
			waiters:  []Waiter{wt("a", 45, time.Hour, 0)},
			wantOver: []string{"a"},
		},
		{
			name: "deadline one nanosecond away is not passed", measured: 95,
			waiters: []Waiter{wt("a", 45, time.Hour, time.Nanosecond)},
		},
		{
			name: "an overrun grant counts against the waiters behind it", measured: 0,
			waiters:  []Waiter{wt("late", 45, time.Hour, -time.Second), wt("next", 60, time.Minute, later)},
			wantOver: []string{"late"}, // 45 + 60 = 105: next waits
		},
		{
			name: "a lighter waiter fits beside an overrun grant", measured: 0,
			waiters:   []Waiter{wt("late", 45, time.Hour, -time.Second), wt("next", 40, time.Minute, later)},
			wantOver:  []string{"late"},
			wantGrant: []string{"next"},
		},
		{
			name: "heavier than capacity: granted when nothing is active", measured: 80,
			waiters:   []Waiter{wt("big", 150, 0, later)},
			wantGrant: []string{"big"}, // unleased 80 does not matter
		},
		{
			name: "heavier than capacity: waits while a lease is active", measured: 20,
			leases: []Lease{held("l", 15, 15)}, use: map[string]float64{"l": 15},
			waiters: []Waiter{wt("big", 150, 0, later)},
		},
		{
			name: "heavier than capacity: not after a grant in the same pass", measured: 0,
			waiters:   []Waiter{wt("a", 45, 2*time.Second, later), wt("big", 150, time.Second, later)},
			wantGrant: []string{"a"},
		},
		{
			name: "heavier than capacity: two of them, one at a time", measured: 0,
			waiters:   []Waiter{wt("big1", 150, 2*time.Second, later), wt("big2", 120, time.Second, later)},
			wantGrant: []string{"big1"},
		},
		{
			name: "exactly capacity is not heavier than capacity", measured: 10,
			waiters: []Waiter{wt("full", 100, 0, later)}, // needs 110: waits
		},
		{
			name: "heavier than capacity at its deadline: overrun", measured: 20,
			leases: []Lease{held("l", 15, 15)}, use: map[string]float64{"l": 15},
			waiters:  []Waiter{wt("big", 150, time.Hour, -time.Second)},
			wantOver: []string{"big"},
		},
		{
			// 45 held and the lease alone measures 90: unleased is 0 (not -40),
			// so a 60 waits (45 + 60); with a negative unleased it would pass
			name: "lease use above host use: unleased is 0, not negative", measured: 50,
			leases: []Lease{held("l", 45, 45)}, use: map[string]float64{"l": 90},
			waiters: []Waiter{wt("a", 60, 0, later)},
		},
		{
			name: "a lease with no use reading counts as 0 use", measured: 50,
			leases:  []Lease{held("l", 45, 45)},
			waiters: []Waiter{wt("a", 10, 0, later)}, // 45 + 50 + 10 = 105
		},
		{
			// sample unavailable, measured 80 is not trusted: unknown work cannot block
			name: "host unavailable: a 45 fits with no charges", measured: 80, missing: true,
			waiters:   []Waiter{wt("a", 45, 0, later)},
			wantGrant: []string{"a"},
		},
		{
			name: "host unavailable: leases still queue behind each other's charges", measured: 80, missing: true,
			leases:  []Lease{held("l", 60, 60)},
			waiters: []Waiter{wt("a", 45, 0, later)}, // 60 + 0 + 45 = 105
		},
		{
			name: "host unavailable: a fitting waiter next to a charge", measured: 80, missing: true,
			leases:    []Lease{held("l", 60, 60)},
			waiters:   []Waiter{wt("a", 40, 0, later)},
			wantGrant: []string{"a"},
		},
		{
			name: "host unavailable: deadlines still overrun", measured: 80, missing: true,
			leases:   []Lease{held("l", 60, 60)},
			waiters:  []Waiter{wt("a", 45, time.Hour, -time.Second)},
			wantOver: []string{"a"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			host := HostUse{Measured: c.measured}
			grant, over := Admit(host, !c.missing, c.leases, c.use, c.waiters, t0, DefaultSettings())
			assert.Equal(t, nilIfEmpty(c.wantGrant), nilIfEmpty(grant), "grant")
			assert.Equal(t, nilIfEmpty(c.wantOver), nilIfEmpty(over), "overrun")
		})
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// Admit reads nothing but its arguments and changes none of them.
func TestAdmit_PureAndDoesNotMutate(t *testing.T) {
	leases := []Lease{held("l", 45, 45)}
	use := map[string]float64{"l": 45}
	waiters := []Waiter{wt("b", 10, time.Second, time.Hour), wt("a", 10, 2*time.Second, time.Hour)}
	s := DefaultSettings()
	g1, o1 := Admit(HostUse{Measured: 50}, true, leases, use, waiters, t0, s)
	g2, o2 := Admit(HostUse{Measured: 50}, true, leases, use, waiters, t0, s)
	assert.Equal(t, g1, g2)
	assert.Equal(t, o1, o2)
	assert.Equal(t, []string{"a", "b"}, g1)
	assert.Equal(t, "b", waiters[0].ID, "the caller's slice is not reordered")
	assert.Equal(t, map[string]float64{"l": 45}, use)
}

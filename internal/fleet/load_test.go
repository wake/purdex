package fleet

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/resources"
)

// LoadScore is Composite, with the name of the reading that holds it up (the first of cpu, mem, pressure, paging, disk on a tie).
func TestLoadScoreNamesTheDriver(t *testing.T) {
	for _, c := range []struct {
		name   string
		f      LocalFast
		disk   float64
		diskOK bool
		want   float64
		driver string
	}{
		{"cpu leads", LocalFast{CPU: 95, Mem: 40, Level: 1}, 40, true, 95, "cpu"},
		{"mem leads", LocalFast{CPU: 10, Mem: 82, Level: 1}, 40, true, 82, "mem"},
		{"level leads", LocalFast{CPU: 10, Mem: 40, Level: 2}, 40, true, 75, "pressure"},
		{"an idle host is held up by the level", LocalFast{Level: 1}, 40, true, 30, "pressure"},
		{"paging leads", LocalFast{CPU: 5, Mem: 5, Level: 1, PageRate: 60}, 40, true, 60, "paging"},
		{"disk leads", LocalFast{CPU: 5, Mem: 5}, 90, true, 90, "disk"},
		{"disk without a reading does not", LocalFast{CPU: 5}, 99, false, 5, "cpu"},
		{"a tie goes to the first", LocalFast{CPU: 50, Mem: 50}, 40, true, 50, "cpu"},
		{"nothing at all", LocalFast{}, 0, false, 0, "cpu"},
	} {
		score, driver := LoadScore(c.f, c.disk, c.diskOK)
		assert.InDelta(t, c.want, score, 0.0001, c.name)
		assert.Equal(t, c.driver, driver, c.name)
		assert.InDelta(t, score, Composite(c.f, c.disk, c.diskOK), 0.0001, c.name+": Composite is the same number")
	}
}

func TestCompositeIsTheLargestOfTheFive(t *testing.T) {
	for _, c := range []struct {
		name string
		f    LocalFast
		disk float64
		want float64
	}{
		{"idle", LocalFast{Level: 1}, 40, 30},
		{"cpu leads", LocalFast{CPU: 95, Mem: 40, Level: 1}, 40, 95},
		{"mem leads", LocalFast{CPU: 10, Mem: 82, Level: 1}, 40, 82},
		{"level 2", LocalFast{CPU: 10, Mem: 40, Level: 2}, 40, 75},
		{"level 4", LocalFast{CPU: 10, Mem: 40, Level: 4}, 40, 100},
		{"unknown level counts for nothing", LocalFast{CPU: 10, Mem: 40}, 40, 40},
		{"page rate", LocalFast{CPU: 5, Mem: 5, Level: 1, PageRate: 60}, 40, 60},
		{"page rate capped", LocalFast{Level: 1, PageRate: 4000}, 40, 100},
		{"disk 84 is ignored", LocalFast{CPU: 5, Mem: 5}, 84, 5},
		{"disk 85 counts", LocalFast{CPU: 5, Mem: 5}, 85, 85},
		{"disk 86 counts", LocalFast{CPU: 5, Mem: 5}, 86, 86},
		{"cpu over 100 is clamped", LocalFast{CPU: 130}, 10, 100},
	} {
		assert.InDelta(t, c.want, Composite(c.f, c.disk, true), 0.0001, c.name)
	}
	assert.InDelta(t, 5, Composite(LocalFast{CPU: 5}, 99, false), 0.0001, "no disk reading: it does not count")
}

// The ring's colour is the zone, not the score: a busy CPU with no memory pressure is a long neutral ring.
func TestBusyCPUAtLevelOneIsNeutral(t *testing.T) {
	var hz HostZone
	f := LocalFast{CPU: 95, Mem: 40, Level: 1}
	assert.InDelta(t, 95, Composite(f, 40, true), 0.0001)
	assert.Equal(t, resources.ZoneNormal, hz.Update(t0, f, 40, true))
}

func TestMemLatchEnterStayLeave(t *testing.T) {
	var l MemLatch
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	assert.Equal(t, resources.ZoneNormal, l.Update(at(0), 1, 0))
	assert.Equal(t, resources.ZoneNormal, l.Update(at(1), 1, 49.9), "rate under 50")
	assert.Equal(t, resources.ZoneWarning, l.Update(at(2), 1, 50), "rate at 50 enters")
	// calmer, but a rate of 45 is above the leave line of 40: stays however long it lasts
	for s := 3.0; s <= 30; s++ {
		assert.Equal(t, resources.ZoneWarning, l.Update(at(s), 1, 45), "t=%v", s)
	}
	assert.Equal(t, resources.ZoneWarning, l.Update(at(31), 1, 10), "the leave condition begins")
	assert.Equal(t, resources.ZoneWarning, l.Update(at(40.9), 1, 10), "9.9 s")
	assert.Equal(t, resources.ZoneNormal, l.Update(at(41), 1, 10), "10 s")
}

func TestMemLatchAFlickerResetsTheHold(t *testing.T) {
	var l MemLatch
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	l.Update(at(0), 2, 0)
	assert.Equal(t, resources.ZoneWarning, l.Update(at(1), 1, 0))
	assert.Equal(t, resources.ZoneWarning, l.Update(at(8), 1, 0))
	assert.Equal(t, resources.ZoneWarning, l.Update(at(9), 2, 0), "level 2 again: the 8 s are lost")
	assert.Equal(t, resources.ZoneWarning, l.Update(at(10), 1, 0))
	assert.Equal(t, resources.ZoneWarning, l.Update(at(19), 1, 0), "only 9 s since the new start")
	assert.Equal(t, resources.ZoneNormal, l.Update(at(20), 1, 0))
}

// A rate between the lines (40–50) neither enters nor allows leaving: it breaks the hold too.
func TestMemLatchARateBetweenTheLinesResetsTheHold(t *testing.T) {
	var l MemLatch
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	l.Update(at(0), 2, 0)
	l.Update(at(1), 1, 0)
	l.Update(at(8), 1, 0)
	assert.Equal(t, resources.ZoneWarning, l.Update(at(9), 1, 45))
	assert.Equal(t, resources.ZoneWarning, l.Update(at(10), 1, 0))
	assert.Equal(t, resources.ZoneWarning, l.Update(at(19), 1, 0), "9 s since the hold began again")
	assert.Equal(t, resources.ZoneNormal, l.Update(at(20), 1, 0))
}

func TestMemLatchLevelTwoNeedsTheLevelToFallToOne(t *testing.T) {
	var l MemLatch
	l.Update(t0, 2, 0)
	for s := 1; s <= 60; s++ {
		assert.Equal(t, resources.ZoneWarning, l.Update(t0.Add(time.Duration(s)*time.Second), 2, 0))
	}
}

func TestMemLatchDangerLeavesOneLevelAtATime(t *testing.T) {
	var l MemLatch
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	assert.Equal(t, resources.ZoneDanger, l.Update(at(0), 4, 0))
	assert.Equal(t, resources.ZoneDanger, l.Update(at(1), 2, 0), "below 4: the hold begins")
	assert.Equal(t, resources.ZoneDanger, l.Update(at(10), 2, 0), "9 s")
	assert.Equal(t, resources.ZoneWarning, l.Update(at(11), 2, 0), "10 s: danger -> warning, level 2 keeps it there")
	// quiet now: warning needs its own 10 s, it does not fall through to normal
	assert.Equal(t, resources.ZoneWarning, l.Update(at(12), 1, 0))
	assert.Equal(t, resources.ZoneWarning, l.Update(at(21), 1, 0))
	assert.Equal(t, resources.ZoneNormal, l.Update(at(22), 1, 0))
}

func TestMemLatchDangerHoldRestartsWhenLevelFourReturns(t *testing.T) {
	var l MemLatch
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	l.Update(at(0), 4, 0)
	l.Update(at(1), 1, 0)
	l.Update(at(9), 1, 0)
	assert.Equal(t, resources.ZoneDanger, l.Update(at(10), 4, 0))
	assert.Equal(t, resources.ZoneDanger, l.Update(at(11), 1, 0))
	assert.Equal(t, resources.ZoneDanger, l.Update(at(20), 1, 0), "9 s after the new start")
	assert.Equal(t, resources.ZoneWarning, l.Update(at(21), 1, 0))
}

func TestMemLatchEntersDangerFromNormalAtOnce(t *testing.T) {
	var l MemLatch
	assert.Equal(t, resources.ZoneDanger, l.Update(t0, 4, 0))
}

func TestMemLatchWithoutALevelRunsOnThePageRate(t *testing.T) {
	var l MemLatch
	assert.Equal(t, resources.ZoneNormal, l.Update(t0, 0, 0))
	assert.Equal(t, resources.ZoneWarning, l.Update(t0.Add(time.Second), 0, 80))
	l.Update(t0.Add(2*time.Second), 0, 0)
	assert.Equal(t, resources.ZoneNormal, l.Update(t0.Add(12*time.Second), 0, 0))
}

func TestMemLatchReset(t *testing.T) {
	var l MemLatch
	l.Update(t0, 4, 0)
	assert.Equal(t, resources.ZoneUnknown, l.Reset())
	assert.Equal(t, resources.ZoneNormal, l.Update(t0.Add(time.Second), 1, 0), "starts again from normal")
}

func TestHostZoneIsTheWorseOfMemoryAndDisk(t *testing.T) {
	var hz HostZone
	calm := LocalFast{CPU: 10, Mem: 30, Level: 1}
	assert.Equal(t, resources.ZoneNormal, hz.Update(t0, calm, 84, true))
	assert.Equal(t, resources.ZoneWarning, hz.Update(t0.Add(time.Second), calm, 86, true), "disk 86 is warning")
	assert.Equal(t, resources.ZoneWarning, hz.Update(t0.Add(2*time.Second), calm, 84, true), "the disk latch holds to 83")
	assert.Equal(t, resources.ZoneNormal, hz.Update(t0.Add(3*time.Second), calm, 80, true))
	assert.Equal(t, resources.ZoneDanger, hz.Update(t0.Add(4*time.Second), LocalFast{Level: 4}, 10, true))
	assert.Equal(t, resources.ZoneUnknown, hz.Reset())
}

func gateStatus() gate.Status {
	return gate.Status{
		Heavy: []gate.Holder{{Class: "heavy", Slot: 1, PID: 10}},
		Light: []gate.Holder{{Class: "light", Slot: 1}, {Class: "light", Slot: 2}},
		QueueHeavy: []gate.Waiter{
			{Class: "heavy", Place: 1, PID: 20, Since: t0.Add(-150 * time.Second), Argv: []string{"go", "test", "./..."}, Cwd: "/Users/u/Workspace/org/proj"},
			{Class: "heavy", Place: 2, PID: 21, Since: t0.Add(-30 * time.Second), Argv: []string{"pnpm", "build"}},
		},
		QueueLight: []gate.Waiter{{Class: "light", Place: 1, PID: 22, Since: t0.Add(-5 * time.Second), Argv: []string{"git", "status"}, Cwd: "/x/y"}},
		Settings: []gate.Setting{
			{Key: "admission.enabled", Value: "true"}, {Key: "admission.max_page_rate", Value: "auto"},
			{Key: "admission.max_pressure_level", Value: "1"}, {Key: "slots.heavy", Value: "2"}, {Key: "slots.light", Value: "8"},
		},
	}
}

func TestQueueSummary(t *testing.T) {
	q := QueueSummary(gateStatus(), false, LocalFast{Level: 1}, t0)
	assert.Equal(t, 1, q.HeavyUsed)
	assert.Equal(t, 2, q.HeavyTotal)
	assert.Equal(t, 2, q.LightUsed)
	assert.Equal(t, 8, q.LightTotal)
	assert.Equal(t, 150, q.LongestWaitS)
	assert.False(t, q.AdmissionHeld)
	require.Len(t, q.Waiters, 3)
	assert.Equal(t, QueueWaiter{Class: "heavy", Place: 1, Command: "go test ./...", Project: "proj", Since: t0.Add(-150 * time.Second)}, q.Waiters[0])
	assert.Equal(t, "", q.Waiters[1].Project)
	assert.Equal(t, "light", q.Waiters[2].Class, "heavy waiters first, then light")
}

func TestQueueSummaryAdmissionHeldFromSettings(t *testing.T) {
	st := gateStatus()
	for name, c := range map[string]struct {
		f    LocalFast
		held bool
	}{
		"level 1, quiet":      {LocalFast{Level: 1}, false},
		"level 2 over max 1":  {LocalFast{Level: 2}, true},
		"level 4":             {LocalFast{Level: 4}, true},
		"auto rate under 50":  {LocalFast{Level: 1, PageRate: 49.9}, false},
		"auto rate at 50":     {LocalFast{Level: 1, PageRate: 50}, true},
		"unknown level, calm": {LocalFast{}, false},
	} {
		assert.Equal(t, c.held, QueueSummary(st, false, c.f, t0).AdmissionHeld, name)
	}
}

func TestQueueSummaryAdmissionSettingsVary(t *testing.T) {
	st := gateStatus()
	st.Settings = []gate.Setting{
		{Key: "admission.enabled", Value: "true"}, {Key: "admission.max_page_rate", Value: "120"}, {Key: "admission.max_pressure_level", Value: "2"},
	}
	assert.False(t, QueueSummary(st, false, LocalFast{Level: 2, PageRate: 100}, t0).AdmissionHeld)
	assert.True(t, QueueSummary(st, false, LocalFast{Level: 2, PageRate: 120}, t0).AdmissionHeld)
	assert.True(t, QueueSummary(st, false, LocalFast{Level: 4}, t0).AdmissionHeld)
	st.Settings[0].Value = "false"
	assert.False(t, QueueSummary(st, false, LocalFast{Level: 4, PageRate: 900}, t0).AdmissionHeld, "admission switched off")
	st.Settings = nil
	assert.False(t, QueueSummary(st, false, LocalFast{Level: 4}, t0).AdmissionHeld, "no admission settings: not held")
}

func TestQueueSummaryCapsWaitersAndCommand(t *testing.T) {
	st := gate.Status{}
	for i := 0; i < 30; i++ {
		st.QueueHeavy = append(st.QueueHeavy, gate.Waiter{Place: i + 1, Since: t0.Add(-time.Duration(i) * time.Second), Argv: []string{string(make([]rune, 300))}})
	}
	st.QueueHeavy[0].Argv = []string{"é", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}
	q := QueueSummary(st, false, LocalFast{}, t0)
	assert.Len(t, q.Waiters, maxQueueShown)
	assert.Equal(t, 29, q.LongestWaitS, "the longest wait counts the waiters that are not shown")
	assert.LessOrEqual(t, len([]rune(q.Waiters[0].Command)), maxCommandRunes)
}

// holdersStatus is a gate with two heavy holders and one light holder, each with a token (the gate's own secret).
func holdersStatus() gate.Status {
	st := gateStatus()
	st.Heavy = []gate.Holder{
		{Class: "heavy", Slot: 1, PID: 10, PGID: 11, Argv: []string{"go", "test", "./..."}, Cwd: "/Users/u/Workspace/org/proj", Started: t0.Add(-90 * time.Second), Session: "sess-a", Token: "SECRET-1"},
		{Class: "heavy", Slot: 2, PID: 12, Argv: []string{"pnpm", "build"}, Started: t0.Add(-5 * time.Second), Token: "SECRET-2"},
	}
	st.Light = []gate.Holder{{Class: "light", Slot: 3, PID: 14, Argv: []string{"git", "status"}, Cwd: "/x/y", Started: t0.Add(-2 * time.Second), Session: "sess-b", Token: "SECRET-3"}}
	return st
}

// Holders come heavy first, then light, with the command shortened, the project, the session, when it started and the
// gate's pid (the 名額鎖 column); the waiters carry their session.
// Mutation gate: holders read from the wrong pool / the order swapped / a field dropped -> red.
func TestQueueSummaryHolders(t *testing.T) {
	st := holdersStatus()
	st.QueueHeavy[0].Session = "sess-w"
	q := QueueSummary(st, false, LocalFast{Level: 1}, t0)
	require.Len(t, q.Holders, 3)
	assert.Equal(t, QueueHolder{Class: "heavy", Slot: 1, Command: "go test ./...", Project: "proj", Session: "sess-a", Since: t0.Add(-90 * time.Second), PID: 10}, q.Holders[0])
	assert.Equal(t, "pnpm build", q.Holders[1].Command)
	assert.Equal(t, "", q.Holders[1].Project)
	assert.Equal(t, "", q.Holders[1].Session)
	assert.Equal(t, QueueHolder{Class: "light", Slot: 3, Command: "git status", Project: "y", Session: "sess-b", Since: t0.Add(-2 * time.Second), PID: 14}, q.Holders[2])
	assert.Equal(t, "sess-w", q.Waiters[0].Session)
	assert.Equal(t, 3, q.HeavyUsed+q.LightUsed, "the used counts are unchanged")
}

// At most maxQueueShown holders are listed; the used counts still count them all.
func TestQueueSummaryCapsHolders(t *testing.T) {
	st := gate.Status{Settings: []gate.Setting{{Key: "slots.heavy", Value: "30"}, {Key: "slots.light", Value: "30"}}}
	for i := 0; i < 15; i++ {
		st.Heavy = append(st.Heavy, gate.Holder{Class: "heavy", Slot: i + 1, PID: 100 + i, Argv: []string{"go", "test"}, Started: t0})
		st.Light = append(st.Light, gate.Holder{Class: "light", Slot: i + 1, PID: 200 + i, Argv: []string{"git"}, Started: t0})
	}
	q := QueueSummary(st, false, LocalFast{}, t0)
	require.Len(t, q.Holders, maxQueueShown)
	assert.Equal(t, 15, q.HeavyUsed)
	assert.Equal(t, 15, q.LightUsed)
	assert.Equal(t, "heavy", q.Holders[14].Class, "heavy first")
	assert.Equal(t, "light", q.Holders[15].Class)
	assert.Equal(t, 204, q.Holders[19].PID, "cut at the cap")
}

// The slot's token is the secret of PDX_GATE_HELD: nothing in the serialised summary may carry it, by key or by value.
// Mutation gate: a Token field on QueueHolder that is filled -> red.
func TestQueueSummaryNeverCarriesATokenOut(t *testing.T) {
	q := QueueSummary(holdersStatus(), true, LocalFast{}, t0)
	b, err := json.Marshal(q)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(b)), "token")
	assert.NotContains(t, string(b), "SECRET")
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	for _, h := range m["holders"].([]any) {
		for k := range h.(map[string]any) {
			assert.NotEqual(t, "token", strings.ToLower(k))
		}
	}
}

// The status' own admission decides when it was there (even when it says "not held"); without it the host's level and rate
// against the effective settings decide, and the reason is empty.
// Mutation gate: drop the presence check (always rebuild, or always trust the value) -> red.
func TestQueueSummaryAdmissionFromTheStatusOrRebuilt(t *testing.T) {
	calm := LocalFast{Level: 1}
	hot := LocalFast{Level: 4} // the settings of gateStatus() would hold at level 4

	st := gateStatus()
	st.Admission = gate.AdmissionState{Held: true, Reason: "memory pressure level 2"}
	q := QueueSummary(st, true, calm, t0)
	assert.True(t, q.AdmissionHeld, "the status says held; the calm host does not override it")
	assert.Equal(t, "memory pressure level 2", q.AdmissionReason)

	st.Admission = gate.AdmissionState{} // present, and "not held"
	q = QueueSummary(st, true, hot, t0)
	assert.False(t, q.AdmissionHeld, "a present admission that says not held is not rebuilt from the level")
	assert.Equal(t, "", q.AdmissionReason)

	// an old status has no admission object: the zero value must not read as "not held"
	q = QueueSummary(st, false, hot, t0)
	assert.True(t, q.AdmissionHeld, "no admission in the status: rebuilt from the level and the settings")
	assert.Equal(t, "", q.AdmissionReason, "a rebuilt answer has no reason")
}

// A class whose effective slot count is 0 is paused (the gate accepts 0 from W3c); no slots.* setting at all is not.
func TestQueueSummaryPausedFromTheEffectiveSlots(t *testing.T) {
	set := func(heavy, light string) gate.Status {
		st := gate.Status{}
		if heavy != "" {
			st.Settings = append(st.Settings, gate.Setting{Key: "slots.heavy", Value: heavy})
		}
		if light != "" {
			st.Settings = append(st.Settings, gate.Setting{Key: "slots.light", Value: light})
		}
		return st
	}
	q := QueueSummary(set("2", "0"), false, LocalFast{}, t0)
	assert.False(t, q.HeavyPaused)
	assert.True(t, q.LightPaused)
	assert.Equal(t, 0, q.LightTotal)
	q = QueueSummary(set("0", "8"), false, LocalFast{}, t0)
	assert.True(t, q.HeavyPaused)
	assert.False(t, q.LightPaused)
	q = QueueSummary(set("", ""), false, LocalFast{}, t0)
	assert.False(t, q.HeavyPaused, "no setting: not paused (an older gate)")
	assert.False(t, q.LightPaused)
	q = QueueSummary(set("x", "-1"), false, LocalFast{}, t0)
	assert.False(t, q.HeavyPaused, "an unreadable value is not a pause")
	assert.False(t, q.LightPaused)
}

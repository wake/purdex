package nex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1866 PR1b (spec 2026-10-08 §3.7): the recheck after a terminal that left
// a row running.

// recheckTiming coalesces fast and rechecks at +30, +80 and +160 ms.
var recheckTiming = projectorTiming{trailing: 5 * time.Millisecond, maxDelay: 20 * time.Millisecond,
	retryDelay: time.Hour, recheck: []time.Duration{30 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond}}

// stateOf is a delta row's state.
func stateOf(t *testing.T, d delta) string {
	t.Helper()
	var r struct {
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(d.Row, &r))
	return r.State
}

// A terminal that leaves the row running (SettleIdle comes after it and is
// silent, §1 F2) is read again until a read shows the row settled.
func TestProjector_TerminalStillRunningIsRecheckedUntilItSettles(t *testing.T) {
	e := newProjEnv(t, recheckTiming)
	e.rows.states("exc_a", "running", "running", "idle")
	e.p.markFrame("exc_a", "execution.terminal")

	first := nextDelta(t, e.sub)
	assert.Equal(t, []string{"execution.terminal"}, first.Cause)
	assert.Equal(t, "running", stateOf(t, first))
	ev, _ := nextFrame(t, e.sub)
	assert.Contains(t, ev.Value, `"cause":[]`, "a recheck has no trigger of its own")
	ev, _ = nextFrame(t, e.sub)
	var third delta
	require.NoError(t, json.Unmarshal([]byte(ev.Value), &third))
	assert.Equal(t, "idle", stateOf(t, third))

	noFrame(t, e.sub, 250*time.Millisecond) // past the last offset
	assert.Equal(t, 3, e.rows.readsOf("exc_a"), "rechecked after the row settled")
	e.p.mu.Lock()
	defer e.p.mu.Unlock()
	assert.Empty(t, e.p.rechecks)
}

func TestProjector_TerminalAlreadySettledIsNotRechecked(t *testing.T) {
	e := newProjEnv(t, recheckTiming)
	e.rows.states("exc_a", "idle")
	e.p.markFrame("exc_a", "execution.terminal")
	nextDelta(t, e.sub)
	noFrame(t, e.sub, 250*time.Millisecond)
	assert.Equal(t, 1, e.rows.readsOf("exc_a"))
}

func TestProjector_RunningWithoutATerminalIsNotRechecked(t *testing.T) {
	e := newProjEnv(t, recheckTiming)
	e.rows.states("exc_a", "running")
	e.p.markFrame("exc_a", "execution.running")
	nextDelta(t, e.sub)
	noFrame(t, e.sub, 250*time.Millisecond)
	assert.Equal(t, 1, e.rows.readsOf("exc_a"))
}

func TestProjector_RecheckGivesUpAfterTheLastOffset(t *testing.T) {
	e := newProjEnv(t, recheckTiming)
	e.rows.states("exc_a", "running")
	e.p.markFrame("exc_a", "execution.terminal")
	for i := 0; i < 4; i++ {
		assert.Equal(t, "running", stateOf(t, nextDelta(t, e.sub)))
	}
	noFrame(t, e.sub, 250*time.Millisecond)
	assert.Equal(t, 4, e.rows.readsOf("exc_a"), "one read plus three rechecks")
	e.p.mu.Lock()
	defer e.p.mu.Unlock()
	assert.Empty(t, e.p.rechecks)
}

// Any read that shows the row settled ends the recheck, including one an
// ordinary event triggered before the next recheck was due.
func TestProjector_ASettledReadCancelsThePendingRecheck(t *testing.T) {
	tm := recheckTiming
	tm.recheck = []time.Duration{150 * time.Millisecond, 300 * time.Millisecond, 450 * time.Millisecond}
	e := newProjEnv(t, tm)
	e.rows.states("exc_a", "running", "idle")
	e.p.markFrame("exc_a", "execution.terminal")
	assert.Equal(t, "running", stateOf(t, nextDelta(t, e.sub)))
	e.p.markFrame("exc_a", "tool_result")
	assert.Equal(t, "idle", stateOf(t, nextDelta(t, e.sub)))

	noFrame(t, e.sub, 300*time.Millisecond)
	assert.Equal(t, 2, e.rows.readsOf("exc_a"), "the recheck ran after a settled read")
}

func TestProjector_StopCancelsAPendingRecheck(t *testing.T) {
	tm := recheckTiming
	tm.recheck = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond}
	e := newProjEnv(t, tm)
	e.rows.states("exc_a", "running")
	e.p.markFrame("exc_a", "execution.terminal")
	nextDelta(t, e.sub)
	e.p.stop(context.Background())
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 1, e.rows.readsOf("exc_a"), "a recheck ran after stop")
}

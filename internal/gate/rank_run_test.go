package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- GQ-b: the wait loop, the record, the lines and the would-wait follow the ranks (#3034, spec §5.2) ----

// queuedTickets waits until n tickets are waiting in the heavy class and returns them in serving order.
func queuedTickets(t *testing.T, g *Gate, n int) []Waiter {
	t.Helper()
	var ws []Waiter
	eventually(t, "the waiters", func() bool {
		ws, _ = g.Queue()
		return len(ws) == n
	})
	return ws
}

// holderOf starts a command that holds the one heavy slot until release is called.
func holderOf(t *testing.T, g *Gate) (release func(), done <-chan runResult) {
	t.Helper()
	dir := t.TempDir()
	done = runAsync(g, context.Background(), Options{Argv: sh(holdScript(dir, "h")), Class: ClassHeavy})
	eventually(t, "the holder", func() bool { return exists(dir + "/started-h") })
	return func() { _ = os.WriteFile(dir+"/go-h", nil, 0o644) }, done
}

func logCommand(logFile, name string) []string { return sh("echo " + name + " >> " + logFile) }

// A front ticket is served before the older one: the commands run in the order the queue says, not the order they arrived.
// Mutation gate: the wait loop uses the plain order (TryTake without ranks) → A runs first (red).
func TestRankRun_AFrontCommandRunsFirst(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	release, holder := holderOf(t, g)
	logFile := filepath.Join(t.TempDir(), "order")
	a := runAsync(g, context.Background(), Options{Argv: logCommand(logFile, "A"), Class: ClassHeavy})
	queuedTickets(t, g, 1)
	b := runAsync(g, context.Background(), Options{Argv: logCommand(logFile, "B"), Class: ClassHeavy})
	ws := queuedTickets(t, g, 2)
	_, err := g.MoveTicket(ClassHeavy, ws[1].Ticket, RankFront, "sess-w", "merge check first", time.Time{})
	require.NoError(t, err)
	release()
	<-holder
	assert.Equal(t, 0, (<-a).code)
	assert.Equal(t, 0, (<-b).code)
	got, err := os.ReadFile(logFile)
	require.NoError(t, err)
	assert.Equal(t, "B\nA\n", string(got))
	rec := lastDecisionOf(t, g, "echo A")
	assert.Nil(t, rec["rank"], "A was never moved")
}

// A held command is not served while the slot is free, says so, and is served after it is set back; its record carries held_s and
// the reason `held`. Mutation gates: a hold served → it starts at once (red); held_s not summed → red; no `held` reason → red.
func TestRankRun_AHeldCommandWaitsAndTheRecordSaysHowLong(t *testing.T) {
	g, errs := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	release, holder := holderOf(t, g)
	dir := t.TempDir()
	w := runAsync(g, context.Background(), Options{Argv: sh(": heldcase; touch " + dir + "/ran"), Class: ClassHeavy})
	ws := queuedTickets(t, g, 1)
	_, err := g.MoveTicket(ClassHeavy, ws[0].Ticket, RankHold, "sess-w", "release check first", time.Time{})
	require.NoError(t, err)
	release()
	<-holder
	never(t, "the held command to start", 400*time.Millisecond, func() bool { return exists(dir + "/ran") })
	after, _ := g.Queue()
	require.Len(t, after, 1)
	assert.Equal(t, RankHold, after[0].Rank)
	assert.Equal(t, "sess-w", after[0].RankBy)
	_, err = g.MoveTicket(ClassHeavy, ws[0].Ticket, RankNormal, "sess-w", "", time.Time{})
	require.NoError(t, err)
	assert.Equal(t, 0, (<-w).code)
	assert.True(t, exists(dir+"/ran"))
	rec := lastDecisionOf(t, g, ": heldcase")
	assert.Contains(t, rec["wait_reasons"], "held")
	assert.Greater(t, rec["held_s"], 0.3, "held for at least the time the test watched it")
	assert.Contains(t, errs.String(), "on hold")
	assert.Contains(t, errs.String(), "release check first")
}

// An expiring hold ends by itself: the command is served once until has passed, with no one moving it.
func TestRankRun_AHoldWithAnUntilEndsByItself(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	release, holder := holderOf(t, g)
	dir := t.TempDir()
	w := runAsync(g, context.Background(), Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy})
	ws := queuedTickets(t, g, 1)
	_, err := g.MoveTicket(ClassHeavy, ws[0].Ticket, RankHold, "sess-w", "short", time.Now().Add(1500*time.Millisecond))
	require.NoError(t, err)
	release()
	<-holder
	never(t, "the held command to start", 300*time.Millisecond, func() bool { return exists(dir + "/ran") })
	assert.Equal(t, 0, (<-w).code, "after the until it is served")
}

// The would-wait hint: back and held tickets do not make a new command wait; a front or normal one does. Tickets made by hand in a
// pool nobody serves keep the state still. Mutation gate: every ticket counted (the old PeekWaiting) → red.
func TestRankRun_WouldWaitPassesBackAndHeldTickets(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	pool := g.pool(g.Config(), ClassHeavy)
	a, err := pool.Enqueue()
	require.NoError(t, err)
	defer a.Drop()
	assert.True(t, g.WouldWait(ClassHeavy).Wait, "a normal ticket in the queue makes a new command wait")
	_, err = pool.SetRank(a.Name, RankHold, "x", "y", time.Time{})
	require.NoError(t, err)
	assert.False(t, g.WouldWait(ClassHeavy).Wait, "a held ticket does not")
	_, err = pool.SetRank(a.Name, RankBack, "x", "y", time.Time{})
	require.NoError(t, err)
	assert.False(t, g.WouldWait(ClassHeavy).Wait, "a back ticket does not")
	_, err = pool.SetRank(a.Name, RankFront, "x", "y", time.Time{})
	require.NoError(t, err)
	w := g.WouldWait(ClassHeavy)
	assert.True(t, w.Wait, "a front ticket does")
	assert.Equal(t, "queue", w.Reason)
}

func lastDecisionOf(t *testing.T, g *Gate, argvContains string) map[string]any {
	t.Helper()
	for _, rec := range readDecisions(t, g) {
		if argv, _ := rec["argv"].([]any); len(argv) > 0 && strings.Contains(strings.Join(anyStrings(argv), " "), argvContains) {
			return rec
		}
	}
	t.Fatalf("no decision for %q", argvContains)
	return nil
}

func anyStrings(in []any) []string {
	var out []string
	for _, v := range in {
		out = append(out, v.(string))
	}
	return out
}

package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- GC (#3058): the rules file, the walk of the queue, the take, the would-wait, the wait loop ----

var tscRule = Conflict{Class: ClassHeavy, A: "tsc -b", B: "vitest related", Reason: "thrash 10-16", By: "sess-w"}

func entry(name string, rank Rank, cmd string) QueueEntry {
	return QueueEntry{Name: name, Rank: RankInfo{Rank: rank, At: time.Unix(0, 1)}, Argv: strings.Fields(cmd)}
}

func holderOfCmd(slot int, cmd string) Holder {
	return Holder{Class: ClassHeavy, Slot: slot, Argv: strings.Fields(cmd)}
}

// The walk, as a table. Each case lists tickets in serving order and says which are served and which blocked (by what).
// Mutation gates: the holder side of the blocked check dropped → the holder cases red; the ticket-ahead side dropped → the
// "behind a blocked one" and "no holder" cases red; a blocked ticket taking capacity → the "behind it takes the slot" case red.
func TestWalk_ServedBlockedAndWaiting(t *testing.T) {
	rules := []Conflict{tscRule}
	type want struct {
		served  []string
		blocked map[string]string // ticket -> "slot heavy-1" or "ticket <name>"
	}
	for name, c := range map[string]struct {
		q        []QueueEntry
		holders  []Holder
		capacity int
		want     want
	}{
		"a ticket that conflicts with a holder is blocked and a free one behind it takes the slot": {
			q:        []QueueEntry{entry("a", RankNormal, "tsc -b --force"), entry("b", RankNormal, "go vet ./...")},
			holders:  []Holder{holderOfCmd(1, "vitest related --run x")},
			capacity: 1,
			want:     want{served: []string{"b"}, blocked: map[string]string{"a": "slot heavy-1"}},
		},
		"a ticket behind a blocked one that conflicts with it waits even with a slot free": {
			q:        []QueueEntry{entry("a", RankNormal, "tsc -b"), entry("b", RankNormal, "vitest related x")},
			holders:  []Holder{holderOfCmd(1, "vitest related y")},
			capacity: 2,
			want:     want{blocked: map[string]string{"a": "slot heavy-1", "b": "ticket a"}},
		},
		"two tickets that conflict with each other and no holder: the first is served, the second blocked": {
			q:        []QueueEntry{entry("a", RankNormal, "tsc -b"), entry("b", RankNormal, "vitest related x")},
			capacity: 2,
			want:     want{served: []string{"a"}, blocked: map[string]string{"b": "ticket a"}},
		},
		"a front ticket that conflicts with a holder is blocked and does not hold back the normal ones": {
			q:        []QueueEntry{entry("f", RankFront, "tsc -b"), entry("n", RankNormal, "go test ./a")},
			holders:  []Holder{holderOfCmd(1, "vitest related y")},
			capacity: 1,
			want:     want{served: []string{"n"}, blocked: map[string]string{"f": "slot heavy-1"}},
		},
		"a hold ticket that conflicts with a later one does not block it": {
			q:        []QueueEntry{entry("n", RankNormal, "vitest related x"), entry("h", RankHold, "tsc -b")},
			capacity: 2,
			want:     want{served: []string{"n"}},
		},
		"a holder with no record or no argv blocks nothing": {
			q:        []QueueEntry{entry("a", RankNormal, "tsc -b")},
			holders:  []Holder{{Class: ClassHeavy, Slot: 1, NoRecord: true}, {Class: ClassHeavy, Slot: 2}},
			capacity: 1,
			want:     want{served: []string{"a"}},
		},
		"a ticket whose file carried no argv matches no pattern": {
			q:        []QueueEntry{entry("a", RankNormal, "vitest related x"), {Name: "b", Rank: RankInfo{Rank: RankNormal}}},
			capacity: 2,
			want:     want{served: []string{"a", "b"}},
		},
		"capacity used up: the rest wait for capacity, not blocked": {
			q:        []QueueEntry{entry("a", RankNormal, "go test ./a"), entry("b", RankNormal, "go test ./b")},
			capacity: 1,
			want:     want{served: []string{"a"}},
		},
		"a blocked ticket takes no capacity": {
			q:        []QueueEntry{entry("a", RankNormal, "tsc -b"), entry("b", RankNormal, "go vet ./...")},
			holders:  []Holder{holderOfCmd(1, "vitest related y")},
			capacity: 1,
			want:     want{served: []string{"b"}, blocked: map[string]string{"a": "slot heavy-1"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			res := walkQueue(ClassHeavy, c.q, c.holders, rules, c.capacity, "", nil, nil)
			var served []string
			for _, e := range c.q {
				if res.Served[e.Name] {
					served = append(served, e.Name)
				}
			}
			assert.Equal(t, c.want.served, served)
			got := map[string]string{}
			for n, b := range res.Blocked {
				got[n] = b.With
				assert.Equal(t, tscRule.Reason, b.Rule.Reason)
			}
			if len(c.want.blocked) == 0 {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, c.want.blocked, got)
			}
		})
	}
	// no rules: the walk is the plain capacity rule (the first `capacity` tickets in order are served)
	res := walkQueue(ClassHeavy, []QueueEntry{entry("a", RankNormal, "tsc -b"), entry("b", RankNormal, "vitest related"), entry("c", RankNormal, "x")},
		nil, nil, 2, "", nil, nil)
	assert.True(t, res.Served["a"] && res.Served["b"] && !res.Served["c"])
}

// ---- the rules file ----

func TestConflicts_SaveLoadAndABrokenFileIsNoRules(t *testing.T) {
	dir := t.TempDir()
	rules, problem := LoadConflicts(dir)
	assert.Empty(t, rules)
	assert.Empty(t, problem, "no file is no rules, not a problem")
	require.NoError(t, SaveConflicts(dir, []Conflict{tscRule}))
	rules, problem = LoadConflicts(dir)
	assert.Empty(t, problem)
	require.Len(t, rules, 1)
	assert.Equal(t, "tsc -b", rules[0].A)
	for name, content := range map[string]string{
		"not json":    "{nope",
		"no class":    `{"rules":[{"a":"tsc -b","b":"x"}]}`,
		"empty side":  `{"rules":[{"class":"heavy","a":" ","b":"x"}]}`,
		"other class": `{"rules":[{"class":"huge","a":"a","b":"b"}]}`,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, conflictsFile), []byte(content), 0o644))
		rules, problem = LoadConflicts(dir)
		assert.Empty(t, rules, name)
		assert.NotEmpty(t, problem, name)
	}
}

// ---- the take ----

func conflictPool(t *testing.T, size int, rules ...Conflict) *rankPool {
	t.Helper()
	rp := newRankPool(t, size)
	if len(rules) > 0 {
		require.NoError(t, SaveConflicts(rp.Dir, rules))
	}
	return rp
}

func ticketFor(t *testing.T, rp *rankPool, cmd string) *Ticket {
	t.Helper()
	rp.advance(time.Millisecond)
	tk, err := rp.EnqueueFor(WaiterInfo{PID: 1, Argv: strings.Fields(cmd)})
	require.NoError(t, err)
	t.Cleanup(tk.Drop)
	return tk
}

// A waiter whose command conflicts with a holder is not served though a slot is free, and says what it waits for; a rule removed
// while it waits lets it through on the next try (the rules are read inside the lock).
// Mutation gates: rules not read in the take → red; the Take without Blocked → red.
func TestTake_AConflictingWaiterIsBlockedAndAFreeOneIsNot(t *testing.T) {
	rp := conflictPool(t, 2, tscRule)
	hold := ticketFor(t, rp, "vitest related a.ts")
	got, err := rp.TryTakeRanked(hold)
	require.NoError(t, err)
	require.NotNil(t, got.Slot)
	defer got.Slot.Release(nil)
	hold.Drop() // as the gate does once it has the slot

	tsc := ticketFor(t, rp, "tsc -b --force")
	vet := ticketFor(t, rp, "go vet ./...")
	blocked, err := rp.TryTakeRanked(tsc)
	require.NoError(t, err)
	assert.Nil(t, blocked.Slot, "a slot is free but the rule keeps tsc -b from vitest related")
	require.NotNil(t, blocked.Blocked)
	assert.Equal(t, "slot heavy-1", blocked.Blocked.With)
	assert.Equal(t, "thrash 10-16", blocked.Blocked.Rule.Reason)
	free, err := rp.TryTakeRanked(vet)
	require.NoError(t, err)
	require.NotNil(t, free.Slot, "a command no rule names passes the blocked one")
	free.Slot.Release(nil)

	require.NoError(t, SaveConflicts(rp.Dir, nil)) // the rule is removed
	now, err := rp.TryTakeRanked(tsc)
	require.NoError(t, err)
	require.NotNil(t, now.Slot, "served on the next poll")
	now.Slot.Release(nil)
}

// A ticket whose file was taken from under it still obeys the rules (by the argv it keeps in memory).
// Mutation gate: the walk skipped for a ticket that is not listed → it takes a slot beside the conflicting holder (red).
func TestTake_AMissingTicketStillObeysTheRules(t *testing.T) {
	rp := conflictPool(t, 2, tscRule)
	holder := ticketFor(t, rp, "vitest related a.ts")
	got, err := rp.TryTakeRanked(holder)
	require.NoError(t, err)
	require.NotNil(t, got.Slot)
	defer got.Slot.Release(nil)
	holder.Drop()
	w := ticketFor(t, rp, "tsc -b")
	require.NoError(t, os.Remove(filepath.Join(rp.queueDir(), w.Name)))
	b, err := rp.TryTakeRanked(w)
	require.NoError(t, err)
	assert.Nil(t, b.Slot)
	require.NotNil(t, b.Blocked)
	assert.Equal(t, "slot heavy-1", b.Blocked.With)
}

// A rule broken on disk is no rule: the waiter is served and the problem is carried.
func TestTake_ABrokenRulesFileStrandsNoOne(t *testing.T) {
	rp := conflictPool(t, 2)
	require.NoError(t, os.WriteFile(filepath.Join(rp.Dir, conflictsFile), []byte("{nope"), 0o644))
	a := ticketFor(t, rp, "tsc -b")
	got, err := rp.TryTakeRanked(a)
	require.NoError(t, err)
	require.NotNil(t, got.Slot)
	assert.NotEmpty(t, got.ConflictsProblem)
	got.Slot.Release(nil)
}

// A late claim is running already: it ignores the rules, and once taken it blocks a conflicting waiter like any holder.
func TestTakeLate_IgnoresRulesAndThenBlocksAConflictingWaiter(t *testing.T) {
	rp := conflictPool(t, 2, tscRule)
	first := ticketFor(t, rp, "vitest related a.ts")
	got, err := rp.TryTakeRanked(first)
	require.NoError(t, err)
	require.NotNil(t, got.Slot)
	defer got.Slot.Release(nil)
	first.Drop() // as the gate does once it has the slot
	late, err := rp.TakeLate(WaiterInfo{PID: 2, Argv: strings.Fields("tsc -b")})
	require.NoError(t, err)
	require.NotNil(t, late, "a running command is not stopped by a rule, though its twin is held")
	defer late.Release(nil)
	w := ticketFor(t, rp, "tsc -b --force")
	b, err := rp.TryTakeRanked(w)
	require.NoError(t, err)
	assert.Nil(t, b.Slot)
	require.NotNil(t, b.Blocked)
}

// The outcome of a late claim against a conflicting take follows who took the admit lock first: both orders, deterministically.
func TestTakeLate_AgainstAConflictingTakeFollowsTheLockOrder(t *testing.T) {
	// the late claim first (slot 1) → the waiter is blocked
	rp := conflictPool(t, 2, tscRule)
	late, err := rp.TakeLate(WaiterInfo{PID: 2, Argv: strings.Fields("tsc -b")})
	require.NoError(t, err)
	require.NotNil(t, late)
	assert.Equal(t, 1, late.K)
	w := ticketFor(t, rp, "vitest related x")
	got, err := rp.TryTakeRanked(w)
	require.NoError(t, err)
	assert.Nil(t, got.Slot, "the waiter is blocked by the late holder")
	late.Release(nil)

	// the take first (slot 1) → the late claim takes slot 2 and both hold
	rp2 := conflictPool(t, 2, tscRule)
	w2 := ticketFor(t, rp2, "vitest related x")
	got2, err := rp2.TryTakeRanked(w2)
	require.NoError(t, err)
	require.NotNil(t, got2.Slot)
	assert.Equal(t, 1, got2.Slot.K)
	late2, err := rp2.TakeLate(WaiterInfo{PID: 2, Argv: strings.Fields("tsc -b")})
	require.NoError(t, err)
	require.NotNil(t, late2)
	assert.Equal(t, 2, late2.K, "the late claim ignores the rule: both run")
	late2.Release(nil)
	got2.Slot.Release(nil)
}

// Two gates whose commands conflict, two slots free, taking at once: exactly one is served (the second sees the first's argv).
// Mutation gate: the take without the argv in the record → both are served (red). Run under -race.
func TestTake_TwoConflictingGatesAtOnceServeExactlyOne(t *testing.T) {
	for i := 0; i < 30; i++ {
		rp := conflictPool(t, 2, tscRule)
		a := ticketFor(t, rp, "tsc -b")
		b := ticketFor(t, rp, "vitest related x")
		var wg sync.WaitGroup
		slots := make([]*Slot, 2)
		for j, tk := range []*Ticket{a, b} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := rp.TryTakeRanked(tk)
				assert.NoError(t, err)
				if got.Slot != nil {
					tk.Drop() // the gate leaves the queue once it holds the slot: the other must then see the record, not the ticket
				}
				slots[j] = got.Slot
			}()
		}
		wg.Wait()
		n := 0
		for _, s := range slots {
			if s != nil {
				n++
				s.Release(nil)
			}
		}
		assert.Equal(t, 1, n, "round %d", i)
	}
}

// Two waiters that read the old (empty) rules, then a rule is added, then both try: the rule holds (read inside the lock).
func TestTake_ARuleAddedBetweenTriesHoldsForTheNextTake(t *testing.T) {
	rp := conflictPool(t, 2)
	a := ticketFor(t, rp, "tsc -b")
	b := ticketFor(t, rp, "vitest related x")
	first, err := rp.TryTakeRanked(a)
	require.NoError(t, err)
	require.NotNil(t, first.Slot, "no rule yet")
	defer first.Slot.Release(nil)
	require.NoError(t, SaveConflicts(rp.Dir, []Conflict{tscRule}))
	second, err := rp.TryTakeRanked(b)
	require.NoError(t, err)
	assert.Nil(t, second.Slot, "the rule added since is honoured by this take")
}

// A pool shrunk with an over-size holder: the holder still blocks a conflicting waiter and is not counted twice against capacity.
func TestTake_AnOverSizeHolderBlocksAndIsCountedOnce(t *testing.T) {
	rp := conflictPool(t, 2, tscRule)
	late, err := rp.TakeLate(WaiterInfo{PID: 2, Argv: strings.Fields("vitest related x")}) // slot 1
	require.NoError(t, err)
	late2, err := rp.TakeLate(WaiterInfo{PID: 3, Argv: strings.Fields("go test ./a")}) // slot 2
	require.NoError(t, err)
	over, err := rp.TakeLate(WaiterInfo{PID: 4, Argv: strings.Fields("go test ./b")}) // slot 3: over the size of 2
	require.NoError(t, err)
	require.NotNil(t, over)
	assert.True(t, over.Over(rp.Size))
	defer func() { late.Release(nil); late2.Release(nil); over.Release(nil) }()
	late2.Release(nil) // one slot of the pool is free now, but an over-size holder uses it up
	tsc := ticketFor(t, rp, "tsc -b")
	vet := ticketFor(t, rp, "go vet ./...")
	b, err := rp.TryTakeRanked(tsc)
	require.NoError(t, err)
	assert.Nil(t, b.Slot)
	require.NotNil(t, b.Blocked, "blocked by the holder of slot 1")
	c, err := rp.TryTakeRanked(vet)
	require.NoError(t, err)
	assert.Nil(t, c.Slot, "the one free slot is used up by the over-size holder: waits for capacity")
	assert.Nil(t, c.Blocked)
}

// ---- would-wait ----

// A command that conflicts with a holder or a front/normal ticket would wait; with a back or held ticket it would not.
// Mutation gate: back counted → red.
func TestWouldWaitFor_ConflictWithAHolderAFrontTicketAndNotABackOne(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 4 })
	require.NoError(t, SaveConflicts(g.Config().Dir, []Conflict{tscRule}))
	pool := g.pool(g.Config(), ClassHeavy)
	assert.False(t, g.WouldWaitFor(ClassHeavy, strings.Fields("tsc -b")).Wait, "nothing holds the class")

	hold, err := pool.TakeLate(WaiterInfo{PID: 1, Argv: strings.Fields("vitest related a.ts")})
	require.NoError(t, err)
	defer hold.Release(nil)
	w := g.WouldWaitFor(ClassHeavy, strings.Fields("tsc -b --force"))
	assert.True(t, w.Wait)
	assert.Equal(t, "conflict", w.Reason)
	assert.Contains(t, w.Note, "tsc -b × vitest related")
	assert.False(t, g.WouldWaitFor(ClassHeavy, strings.Fields("go vet ./...")).Wait, "a command no rule names")
	assert.False(t, g.WouldWaitFor(ClassHeavy, nil).Wait)
	hold.Release(nil)

	other, err := pool.EnqueueFor(WaiterInfo{PID: 2, Argv: strings.Fields("vitest related b.ts")})
	require.NoError(t, err)
	defer other.Drop()
	assert.True(t, g.WouldWaitFor(ClassHeavy, strings.Fields("tsc -b")).Wait, "a normal ticket ahead that conflicts")
	_, err = pool.SetRank(other.Name, RankBack, "x", "y", time.Time{})
	require.NoError(t, err)
	assert.False(t, g.WouldWaitFor(ClassHeavy, strings.Fields("tsc -b")).Wait, "a back ticket never makes a new command wait")
	_, err = pool.SetRank(other.Name, RankHold, "x", "y", time.Time{})
	require.NoError(t, err)
	assert.False(t, g.WouldWaitFor(ClassHeavy, strings.Fields("tsc -b")).Wait, "nor a held one")
	_, err = pool.SetRank(other.Name, RankFront, "x", "y", time.Time{})
	require.NoError(t, err)
	assert.True(t, g.WouldWaitFor(ClassHeavy, strings.Fields("tsc -b")).Wait, "a front ticket does")
}

// ---- the wait loop ----

// toolScript makes an executable that holds until its go file exists: `tool-a` and `tool-b` are two commands a rule can name.
func toolScript(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\ntouch " + dir + "/started-" + name + "\nwhile [ ! -e " + dir + "/go-" + name + " ]; do sleep 0.02; done\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	return path
}

// Two commands a rule keeps apart, two slots: the second waits though a slot is free, says what for, and its record carries the rule
// and the time blocked; it starts when the first ends. Mutation gates: the line missing → red; conflict_s not summed → red; the
// reason `conflict` missing → red.
func TestConflictRun_TheSecondWaitsForTheFirstAndTheRecordSaysSo(t *testing.T) {
	g, errs := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 2 })
	require.NoError(t, SaveConflicts(g.Config().Dir, []Conflict{{Class: ClassHeavy, A: "tool-a", B: "tool-b", Reason: "they thrash", By: "sess-w"}}))
	dir := t.TempDir()
	a := toolScript(t, dir, "tool-a")
	b := toolScript(t, dir, "tool-b")
	ra := runAsync(g, context.Background(), Options{Argv: []string{a}, Class: ClassHeavy})
	eventually(t, "tool-a to start", func() bool { return exists(dir + "/started-tool-a") })
	rb := runAsync(g, context.Background(), Options{Argv: []string{b}, Class: ClassHeavy})
	eventually(t, "the conflict line", func() bool { return strings.Contains(errs.String(), "waits for slot heavy-1") })
	assert.Contains(t, errs.String(), "tool-a × tool-b (they thrash)")
	never(t, "tool-b to start while tool-a runs", 300*time.Millisecond, func() bool { return exists(dir + "/started-tool-b") })
	require.NoError(t, os.WriteFile(dir+"/go-tool-a", nil, 0o644))
	assert.Equal(t, 0, (<-ra).code)
	eventually(t, "tool-b to start", func() bool { return exists(dir + "/started-tool-b") })
	require.NoError(t, os.WriteFile(dir+"/go-tool-b", nil, 0o644))
	assert.Equal(t, 0, (<-rb).code)
	rec := lastDecisionOf(t, g, "tool-b")
	assert.Contains(t, rec["wait_reasons"], "conflict")
	assert.Equal(t, "tool-a × tool-b", rec["conflict"])
	assert.Greater(t, rec["conflict_s"], 0.2)
}

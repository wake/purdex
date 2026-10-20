package entry

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

func peer(alias string, reachable, workerOn bool, labels ...string) wire.Peer {
	return wire.Peer{
		Info:      wire.Info{HostID: alias + "-id", Alias: alias, Labels: labels, Worker: wire.InfoWorker{Enabled: workerOn}},
		Reachable: reachable,
		Facts:     &wire.Facts{}, // its facts have arrived: its labels are all in (a peer with no facts yet is still settling)
	}
}

func queued(labels ...string) Result {
	return Result{Action: ActionRewrite, Task: "vitest-root", Queued: true, Labels: labels}
}

// An entry that turns a command into a queued task has to be sure a host can take it: pdx test would end at once with exit 5 when no host holds
// the task's labels, and a command that fails where it would have run is worse than no entry (#S2-b0).
func TestGateOnPool_NoHostHoldsTheLabelsIsNone(t *testing.T) {
	pool := wire.Pool{Peers: []wire.Peer{
		peer("mlab", true, true, "os:darwin", "host:mlab"),
		peer("air26", true, true, "os:darwin", "repo:purdex"),
	}}
	r := GateOnPool(queued("repo:aigora-v2"), pool, nil)
	assert.Equal(t, ActionNone, r.Action)
	assert.Contains(t, r.Reason, "no reachable host with a worker on holds repo:aigora-v2")
	assert.NotContains(t, r.Reason, "\n", "the reason is one line")
	assert.Empty(t, r.Task, "a command that is not rewritten names no task")
}

func TestGateOnPool_AHostThatCanTakeItKeepsTheRewrite(t *testing.T) {
	pool := wire.Pool{Peers: []wire.Peer{
		peer("mlab", true, true, "os:darwin"),
		peer("air26", true, true, "os:darwin", "repo:purdex", "tool:node24"),
	}}
	in := queued("repo:purdex", "tool:node24")
	assert.Equal(t, in, GateOnPool(in, pool, nil))
}

func TestGateOnPool_AHostThatHoldsThemButCannotTakeItNowDoesNotCount(t *testing.T) {
	labels := []string{"repo:purdex", "os:darwin"}
	for name, p := range map[string]wire.Peer{
		"unreachable (only its last known labels)": peer("air26", false, true, labels...),
		"worker off": peer("air26", true, false, labels...),
	} {
		r := GateOnPool(queued(labels...), wire.Pool{Peers: []wire.Peer{p}}, nil)
		assert.Equal(t, ActionNone, r.Action, name)
		assert.Contains(t, r.Reason, "no reachable host with a worker on holds", name)
	}
	// one that cannot and one that can: the one that can decides
	pool := wire.Pool{Peers: []wire.Peer{peer("air26", false, true, labels...), peer("mlab", true, true, labels...)}}
	assert.Equal(t, ActionRewrite, GateOnPool(queued(labels...), pool, nil).Action)
}

// An owner at the keyboard makes a host yield for a while: that is waiting, not "cannot take it".
func TestGateOnPool_AnOwnerYieldingHostStillCounts(t *testing.T) {
	p := peer("air26", true, true, "repo:purdex")
	p.Facts = &wire.Facts{Owner: &wire.OwnerState{Active: true}, Worker: wire.FactsWorker{OwnerRule: "x"}}
	assert.Equal(t, ActionRewrite, GateOnPool(queued("repo:purdex"), wire.Pool{Peers: []wire.Peer{p}}, nil).Action)
}

func TestGateOnPool_APoolThatCannotBeReadIsNone(t *testing.T) {
	r := GateOnPool(queued("repo:purdex"), wire.Pool{}, errors.New("dial unix /x/wwd.sock: connect: no such file\nsecond line"))
	assert.Equal(t, ActionNone, r.Action)
	assert.Contains(t, r.Reason, "wwd pool cannot be read")
	assert.NotContains(t, r.Reason, "\n")
}

func TestGateOnPool_OnlyQueuedRewritesAreJudged(t *testing.T) {
	broken := errors.New("no pool")
	local := Result{Action: ActionRewrite, Task: "go-pkg", Args: []string{"./x/"}}
	assert.Equal(t, local, GateOnPool(local, wire.Pool{}, broken), "a local-lane task does not need a host")
	for _, a := range []Result{{Action: ActionAdhoc}, {Action: ActionAdvise}, {Action: ActionDeny}, {Action: ActionNone, Reason: "r"}} {
		assert.Equal(t, a, GateOnPool(a, wire.Pool{}, broken), string(a.Action))
	}
}

// A task with no labels can be taken by any worker, but not when no worker is on or reachable (#3696 R1): it is judged all the same.
func TestGateOnPool_AQueuedRewriteWithNoLabelsNeedsAWorker(t *testing.T) {
	in := Result{Action: ActionRewrite, Task: "t", Queued: true}
	assert.Equal(t, in, GateOnPool(in, wire.Pool{Peers: []wire.Peer{peer("a", true, true)}}, nil))
	for name, pool := range map[string]wire.Pool{
		"no host":     {},
		"worker off":  {Peers: []wire.Peer{peer("a", true, false)}},
		"unreachable": {Peers: []wire.Peer{peer("a", false, true)}},
	} {
		r := GateOnPool(in, pool, nil)
		assert.Equal(t, ActionNone, r.Action, name)
		assert.Contains(t, r.Reason, "no reachable host with a worker on", name)
		assert.NotContains(t, r.Reason, "holds", name+": there is no label to name")
	}
}

// A host that answers but has not sent its first facts record announces only its os and arch labels (repo:, tool:, earned task: labels
// follow with the facts): it may hold the labels, so the answer is not "no" (#3696 R2; pdx test treats it the same way and queues as usual).
func TestGateOnPool_AHostStillSettlingMayHoldTheLabels(t *testing.T) {
	settling := peer("air26", true, true, "os:darwin", "arch:arm64")
	settling.Facts = nil
	in := queued("repo:purdex", "tool:node24")
	assert.Equal(t, in, GateOnPool(in, wire.Pool{Peers: []wire.Peer{peer("mlab", true, true, "os:darwin"), settling}}, nil))

	// ... a settling host whose worker is known to be off is not one to wait for: the worker switch is in its info, no facts needed (#3696 critic)
	off := peer("air26", true, false, "os:darwin", "arch:arm64")
	off.Facts = nil
	assert.Equal(t, ActionNone, GateOnPool(in, wire.Pool{Peers: []wire.Peer{off}}, nil).Action)

	// ... and one that is not answering holds nothing we can wait for, with or without facts
	down := peer("air26", false, true, "os:darwin")
	down.Facts = nil
	assert.Equal(t, ActionNone, GateOnPool(in, wire.Pool{Peers: []wire.Peer{down}}, nil).Action)

	// and once every host that answers has its facts, the answer is a plain no
	assert.Equal(t, ActionNone, GateOnPool(in, wire.Pool{Peers: []wire.Peer{peer("mlab", true, true, "os:darwin"), peer("air26", true, true, "os:darwin")}}, nil).Action)
}

func TestGateOnPool_ANeverReachedPeerHoldsNothing(t *testing.T) {
	pool := wire.Pool{Peers: []wire.Peer{{Reachable: false}}}
	assert.Equal(t, ActionNone, GateOnPool(queued("repo:purdex"), pool, nil).Action)
}

// Match gives a queued rewrite the task's labels, for the pool check; a local one needs none.
func TestMatch_AQueuedRewriteCarriesTheTasksLabels(t *testing.T) {
	root, err := filepath.Abs("../../..")
	require.NoError(t, err)
	got := Match(filepath.Join(root, "spa"), []string{"pnpm", "vitest", "run", "--maxWorkers=3"}) // spa-full, a release task: through the queue
	require.Equal(t, ActionRewrite, got.Action)
	require.Equal(t, "spa-full", got.Task)
	assert.True(t, got.Queued)
	assert.Equal(t, []string{"os:darwin", "tool:node24", "tool:pnpm10"}, got.Labels)

	local := Match(root, []string{"go", "test", "./internal/wwd/entry/"}) // go-pkg: the local fast lane
	require.Equal(t, ActionRewrite, local.Action)
	assert.False(t, local.Queued)
	assert.Empty(t, local.Labels, "a local task is not judged against the pool")
}

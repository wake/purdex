package wwdalerts

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/wake/purdex/internal/fleet"
)

// #3610: both fleet daemons cannot read the pool. Each can still read its own host's wwd, and the other has said (twice running) that it
// cannot push either; then each pushes the alerts of its own host and nobody else's, until the pool can be read again (spec §13.3: at
// least once, a duplicate is preferred to a failure not told).

func boolp(b bool) *bool { return &b }

// node is one fleet daemon: its env (its own wwd, ledger and push module) and what it knows of the other.
type node struct {
	e *env
}

func newNode(t *testing.T, self string) *node {
	e := newEnv(t)
	e.src.self = self
	n := &node{e: e}
	return n
}

// peer sets what this node's role engine reports about the other daemon's push.
func (n *node) peer(p fleet.PeerPush) { n.e.m.peer = func() fleet.PeerPush { return p } }

func notReady() fleet.PeerPush {
	return fleet.PeerPush{Known: true, Answered: true, Ready: boolp(false)}
}
func ready() fleet.PeerPush { return fleet.PeerPush{Known: true, Answered: true, Ready: boolp(true)} }

func (n *node) passes(t *testing.T, k int) {
	for i := 0; i < k; i++ {
		n.e.now = n.e.now.Add(30 * time.Second)
		n.e.pass(t)
	}
}

func TestBothDaemonsWithoutThePoolEachPushTheirOwnHost(t *testing.T) {
	a, b := newNode(t, "mlab"), newNode(t, "air26")
	for _, n := range []*node{a, b} {
		n.e.src.poolErr = errors.New("pool unreadable")
		n.e.src.peers = []string{"mlab", "air26"}
		n.peer(notReady())
	}
	a.e.deliv, b.e.deliv = true, true // primary; the standby's rule says "the primary cannot push"
	a.e.src.set("mlab", diskLow("mlab"))
	b.e.src.set("air26", diskLow("air26"))

	a.passes(t, 1)
	b.passes(t, 1)
	assert.Equal(t, 0, a.e.ntf.count(), "one pass of the other not being ready is not enough")
	assert.Equal(t, 0, b.e.ntf.count())

	a.passes(t, 1)
	b.passes(t, 1)
	if assert.Equal(t, 1, a.e.ntf.count(), "the primary tells its own host's alert") {
		assert.Equal(t, "mlab", a.e.ntf.got[0].Host)
	}
	if assert.Equal(t, 1, b.e.ntf.count(), "the standby tells its own host's alert too") {
		assert.Equal(t, "air26", b.e.ntf.got[0].Host)
	}
	assert.Equal(t, true, a.e.m.Status()["deliverer"], "it really sends now")

	a.passes(t, 3) // the same alert is not queued on every pass
	assert.Equal(t, 1, a.e.ntf.count())
}

func TestTheOtherDaemonBeingReadyMeansNoFallback(t *testing.T) {
	a := newNode(t, "mlab")
	a.e.src.poolErr = errors.New("pool unreadable")
	a.e.src.set("mlab", diskLow("mlab"))
	a.peer(ready()) // the standby can push: it takes over, as before
	a.passes(t, 4)
	assert.Equal(t, 0, a.e.ntf.count())
	assert.Equal(t, false, a.e.m.Status()["deliverer"])
}

func TestAnUnreachableOtherDaemonCountsAsNotAbleToPush(t *testing.T) {
	a := newNode(t, "mlab")
	a.e.src.poolErr = errors.New("pool unreadable")
	a.e.src.set("mlab", diskLow("mlab"))
	a.peer(fleet.PeerPush{Known: true, Answered: false})
	a.passes(t, 2)
	assert.Equal(t, 1, a.e.ntf.count())
}

func TestUnknownOrUnanswerableIsNotConfirmed(t *testing.T) {
	for name, p := range map[string]fleet.PeerPush{
		"nothing known yet":              {},
		"an older daemon, no push_ready": {Known: true, Answered: true},
	} {
		a := newNode(t, "mlab")
		a.e.src.poolErr = errors.New("pool unreadable")
		a.e.src.set("mlab", diskLow("mlab"))
		a.peer(p)
		a.passes(t, 4)
		assert.Equal(t, 0, a.e.ntf.count(), name)
	}
}

func TestWithoutItsOwnWWDNothingIsPushed(t *testing.T) {
	a := newNode(t, "mlab")
	a.e.src.poolErr = errors.New("pool unreadable")
	a.e.src.errs = map[string]error{"mlab": errors.New("socket refused")}
	a.e.src.set("mlab", diskLow("mlab"))
	a.peer(notReady())
	a.passes(t, 4)
	assert.Equal(t, 0, a.e.ntf.count())
	assert.Equal(t, false, a.e.m.Status()["deliverer"], "with no list of its own there is nothing it could send, and it does not say it does")
}

// The pool coming back ends the fallback at once and the streak starts again; a duplicate is at most one (the other host's alert, which
// this node's ledger had never seen), the alert it already told is not told again.
func TestTheFallbackEndsWithThePoolAndRepeatsAtMostOnce(t *testing.T) {
	a := newNode(t, "mlab")
	a.e.src.poolErr = errors.New("pool unreadable")
	a.e.src.peers = []string{"air26"}
	a.e.src.set("mlab", diskLow("mlab"))
	a.e.src.set("air26", diskLow("air26"))
	a.peer(notReady())
	a.passes(t, 2)
	assert.Equal(t, 1, a.e.ntf.count())
	a.e.ntf.confirm(0)

	a.e.src.poolErr = nil
	a.peer(ready())
	a.passes(t, 3)
	// the whole pool is read again: this daemon is the deliverer as usual; air26's alert (never in its ledger) is told once
	assert.Equal(t, 2, a.e.ntf.count())
	assert.Equal(t, "air26", a.e.ntf.got[1].Host)

	// the pool fails once more: one pass of "not ready" does not start a fallback again
	a.e.src.poolErr = errors.New("pool unreadable")
	a.peer(notReady())
	a.e.ntf.confirm(1)
	a.passes(t, 1)
	assert.Equal(t, 2, a.e.ntf.count())
}

// R2: a failure only a peer's list held (and was told) when the pool was whole, and which this host's own wwd starts to hold when the pool
// can no longer be read, is the same failure going on: the peer's list is unread, not cleared, so the fallback does not tell it again.
func TestASharedFailureTheUnreadPeerStillHoldsIsNotToldAgainInTheFallback(t *testing.T) {
	a := newNode(t, "mlab")
	a.e.src.peers = []string{"air26"}
	a.e.src.set("air26", silenceOf("mserv", t0))
	a.passes(t, 1)
	assert.Equal(t, 1, a.e.ntf.count())
	a.e.ntf.confirm(0)

	// the pool becomes unreadable (air26's list with it); this host's own wwd now notices the same loss of mserv
	a.e.src.poolErr = errors.New("pool unreadable")
	a.e.src.set("mlab", silenceOf("mserv", t0.Add(2*time.Minute)))
	a.peer(notReady())
	a.passes(t, 4)
	assert.Equal(t, 1, a.e.ntf.count(), "the failure that was told is not told again as a new one")
}

// The other side of it: an unread list's old hold is not a reason to keep quiet for ever. When a list that is read clears the failure and
// it comes back (mserv recovers and goes down again while air26's list cannot be read), that is a new failure and it is told: a duplicate is
// preferred to a failure not told.
func TestAStaleHoldOfAnUnreadListDoesNotHideANewFailure(t *testing.T) {
	a := newNode(t, "mlab")
	a.e.src.peers = []string{"air26"}
	a.e.src.set("air26", silenceOf("mserv", t0))
	a.passes(t, 1)
	assert.Equal(t, 1, a.e.ntf.count())
	a.e.ntf.confirm(0)

	a.e.src.poolErr = errors.New("pool unreadable")
	a.e.src.set("mlab", silenceOf("mserv", t0.Add(time.Minute)))
	a.peer(notReady())
	a.passes(t, 2)
	assert.Equal(t, 1, a.e.ntf.count(), "still the failure that was told")

	a.e.src.set("mlab") // mserv answers again: this wwd's list clears the failure
	a.passes(t, 1)
	a.e.now = a.e.now.Add(10 * time.Minute)
	a.e.src.set("mlab", silenceOf("mserv", a.e.now)) // and it goes down again: a new failure
	a.passes(t, 2)
	assert.Equal(t, 2, a.e.ntf.count(), "told again as the new failure it is, not hidden by air26's old hold")
}

package wwdalerts

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// silenceOf is silence:<peer> as the wwd that lost the peer raises it, at its own time.
func silenceOf(peer string, since time.Time) wire.Alert {
	return wire.Alert{Key: "silence:" + peer, Kind: wire.AlertSilence, Level: wire.AlertPush, Since: since, Last: since, Detail: peer + " 的 wwd 已 3 分鐘連不上"}
}

// Two wwds lose the same peer, at different moments: the deliverer pushes the failure once, whichever list it reads first, and reading it
// again does not push it again.
func TestSilenceNoticedByTwoWWDsIsPushedOnce(t *testing.T) {
	for _, mlabFirst := range []bool{true, false} {
		e := newEnv(t)
		e.src.peers = []string{"air26"}
		early, late := t0, t0.Add(40*time.Second)
		if !mlabFirst {
			early, late = late, early
		}
		e.src.set("mlab", silenceOf("mserv", early))
		e.src.set("air26", silenceOf("mserv", late))
		e.pass(t)
		e.pass(t)
		e.now = e.now.Add(time.Minute)
		e.pass(t)
		assert.Equal(t, 1, e.ntf.count(), "mlabFirst=%v: one failure, one push", mlabFirst)
		assert.Contains(t, e.ntf.got[0].Title, "mserv")
		assert.Equal(t, "mserv", e.ntf.got[0].Host, "the push is about the lost peer, not the wwd that noticed")
	}
}

// The failure goes on while one wwd's list still holds it, whatever the other lists do or whether they can be read.
func TestSilenceGoingOnIsNotToldAgain(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26", silenceOf("mserv", t0.Add(20*time.Second)))
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count())
	e.ntf.confirm(0)
	e.src.errs = map[string]error{"air26": errors.New("unreachable")}
	for i := 0; i < 3; i++ {
		e.now = e.now.Add(10 * time.Minute)
		e.pass(t)
	}
	assert.Equal(t, 1, e.ntf.count(), "mlab's list holds the same failure the whole time")
}

// The first wwd to notice stops noticing (it restarted, say) while the failure goes on and another wwd, which raised it before the push,
// still holds it: the same failure, not told again.
func TestSilenceTheFirstObserverLeavingIsNotANewFailure(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.now = t0.Add(time.Minute)
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26", silenceOf("mserv", t0.Add(20*time.Second)))
	e.pass(t)
	e.ntf.confirm(0)
	e.src.set("mlab")
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count(), "air26 raised it before the push and still holds it")
}

// Cleared on every list and raised again, even inside the hour, is a new failure: told at once.
func TestSilenceClearedAndBackIsANewFailure(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26", silenceOf("mserv", t0))
	e.pass(t)
	e.ntf.confirm(0)
	e.src.set("mlab")
	e.src.set("air26")
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	e.src.set("mlab", silenceOf("mserv", e.now))
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 2, e.ntf.count())
}

// silenceKind is silenceOf with the words of a kind of silence (the peer's wwd, or its host).
func silenceKind(peer string, since time.Time, detail string) wire.Alert {
	a := silenceOf(peer, since)
	a.Detail = detail
	return a
}

// A silence that is the same failure but changes kind — the wwd of a host that was up stops answering, then the host itself goes — is told
// again, in the new words: "the host is gone" is news the person needs. The same kind again, within the hour, is not told twice.
func TestSilenceChangingKindIsToldAgainAndTheSameKindIsNot(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	wwdDown := wire.SilenceDetailWwdDown("mserv")
	hostDown := wire.SilenceDetailHostDown("mserv")

	e.src.set("mlab", silenceKind("mserv", t0, wwdDown))
	e.pass(t)
	require.Equal(t, 1, e.ntf.count())
	assert.Contains(t, e.ntf.got[0].Title, "wwd 沒有回應")
	e.ntf.confirm(0)

	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count(), "the same kind, still going on: not told again")

	e.src.set("mlab", silenceKind("mserv", t0, hostDown)) // the same key and Since, other words
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	require.Equal(t, 2, e.ntf.count(), "the host itself went: told again")
	assert.Contains(t, e.ntf.got[1].Title, "主機離線")
	e.ntf.confirm(1)

	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 2, e.ntf.count(), "and not again for the same kind")

	e.src.set("mlab", silenceKind("mserv", t0, wwdDown)) // the host came back, its wwd did not
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 3, e.ntf.count(), "a change back is a change too")
}

// Two wwds that see the same failure differently (one reaches the host's SSH port, the other does not) are one failure with one kind: the
// host-down words win, and the push says the same whichever list is read first.
func TestSilenceTwoWWDsDisagreeingOnTheKindMakeOneAlertInTheWorseWords(t *testing.T) {
	for _, mlabFirst := range []bool{true, false} {
		e := newEnv(t)
		e.src.peers = []string{"air26"}
		up := silenceKind("mserv", t0, wire.SilenceDetailWwdDown("mserv"))
		gone := silenceKind("mserv", t0, wire.SilenceDetailHostDown("mserv"))
		if mlabFirst {
			e.src.set("mlab", up)
			e.src.set("air26", gone)
		} else {
			e.src.set("mlab", gone)
			e.src.set("air26", up)
		}
		e.pass(t)
		e.pass(t)
		assert.Equal(t, 1, e.ntf.count(), "mlabFirst=%v: one failure, one push", mlabFirst)
		assert.Contains(t, e.ntf.got[0].Title, "主機離線", "mlabFirst=%v", mlabFirst)
	}
}

// The wwds' clocks are not the deliverer's: a new failure is told whatever Since the wwds put on it, a long time before the deliverer's last
// push included (attack review: no cross-host clock comparison).
func TestSilenceANewFailureIsToldWhateverTheWWDsClocksSay(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	behind := t0.Add(-3 * time.Hour) // a wwd whose clock is far behind
	e.src.set("mlab", silenceOf("mserv", behind))
	e.src.set("air26")
	e.pass(t)
	e.ntf.confirm(0)
	e.src.set("mlab")
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	e.src.set("mlab", silenceOf("mserv", behind.Add(2*time.Minute))) // still before the first push in the deliverer's time
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 2, e.ntf.count())
}

// A restart is not the end of a failure the ledger knows: the same alert, raised before the last push, is not told again. One that cleared
// and was raised again while the daemon was down — raised after that push — is a new failure and is told.
func TestSilenceAcrossARestart(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26")
	e.pass(t)
	e.ntf.confirm(0)

	e.m = e.module(t) // the daemon restarted: same ledger, no memory
	e.now = e.now.Add(5 * time.Minute)
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count(), "the same failure after a restart")

	e.m = e.module(t)
	e.src.set("mlab", silenceOf("mserv", e.now.Add(2*time.Minute))) // cleared and raised again while the daemon was down
	e.now = e.now.Add(5 * time.Minute)
	e.pass(t)
	assert.Equal(t, 2, e.ntf.count(), "raised after the last push: a new failure")
}

// The first look after a restart may miss the list (the wwd is not ready yet): the one after it is still the first look at that failure.
func TestSilenceARestartWhoseFirstReadFailsStillJoinsTheLedger(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26")
	e.pass(t)
	e.ntf.confirm(0)

	e.m = e.module(t)
	e.src.errs = map[string]error{"mlab": errors.New("not ready"), "air26": errors.New("not ready")}
	e.now = e.now.Add(time.Minute)
	e.pass(t) // nothing could be read
	e.src.errs = nil
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count(), "the same failure, seen for the first time on the second pass")
}

// A host that left the pool takes what it held with it: back in the pool and holding the key again is a new observer, and the failure it
// shows is told.
func TestSilenceAHostThatLeftAndCameBackIsANewObserver(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab")
	e.src.set("air26", silenceOf("mserv", t0))
	e.pass(t)
	e.ntf.confirm(0)

	e.src.peers = nil // air26 left the pool: mserv recovered and failed again meanwhile, nobody holds it
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	e.src.peers = []string{"air26"}
	e.src.set("air26", silenceOf("mserv", e.now))
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 2, e.ntf.count())
}

// A list that cannot be read must not hide a new failure, whatever it held: one unreadable list does not freeze the failures the others
// show (attack review).
func TestSilenceAnUnreadableListDoesNotHideANewFailure(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26")
	e.pass(t)
	e.ntf.confirm(0)
	e.src.errs = map[string]error{"air26": errors.New("unreachable")}
	e.src.set("mlab")
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	e.src.set("mlab", silenceOf("mserv", e.now))
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 2, e.ntf.count(), "mserv came back and failed again inside the hour")
}

// The attack review's interleaving: the list that held the old failure was unreadable, and in the pass where it comes back empty another
// list already shows the new one. The new failure is told in that pass, not suppressed by the old one's row.
func TestSilenceANewFailureIsToldInThePassTheOldHolderComesBack(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26", silenceOf("mserv", t0))
	e.pass(t)
	e.ntf.confirm(0)

	e.src.errs = map[string]error{"air26": errors.New("unreachable")}
	e.src.set("mlab")
	e.now = e.now.Add(time.Minute)
	e.pass(t)

	e.src.errs = nil
	e.src.set("air26") // the old holder is back and empty
	e.src.set("mlab", silenceOf("mserv", e.now.Add(10*time.Minute)))
	e.now = e.now.Add(10 * time.Minute)
	e.pass(t)
	assert.Equal(t, 2, e.ntf.count(), "the new failure is told at once")
}

// Another wwd noticing the same peer afresh after the first one's alert is gone is told again: a duplicate is preferred to a failure not
// told (spec §13.3 at-least-once).
func TestSilenceAHandOverToALaterObserverIsToldAgain(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26")
	e.pass(t)
	e.ntf.confirm(0)
	e.src.set("mlab")
	e.src.set("air26", silenceOf("mserv", t0.Add(time.Minute)))
	e.now = e.now.Add(2 * time.Minute)
	e.pass(t)
	assert.Equal(t, 2, e.ntf.count())
}

// The partner's alert is about the wwd's own host and stays per host: two hosts' partner_down are two failures.
func TestPartnerDownStaysPerHost(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	pd := func(since time.Time) wire.Alert {
		return wire.Alert{Key: "partner_down", Kind: wire.AlertSilence, Level: wire.AlertPush, Since: since, Last: since, Detail: "這台的 Purdex daemon 已 3 分鐘沒有來讀告警"}
	}
	e.src.set("mlab", pd(t0))
	e.src.set("air26", pd(t0))
	e.pass(t)
	assert.Equal(t, 2, e.ntf.count())
}

package wwdalerts

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/wake/purdex/internal/wwd/wire"
)

// silenceOf is silence:<peer> as the wwd that lost the peer raises it, at its own time.
func silenceOf(peer string, since time.Time) wire.Alert {
	return wire.Alert{Key: "silence:" + peer, Kind: wire.AlertSilence, Level: wire.AlertPush, Since: since, Last: since, Detail: peer + " 的 wwd 已 3 分鐘連不上"}
}

// Two wwds lose the same peer, at different moments: the deliverer pushes the failure once, whichever list it reads first, and again
// reading does not push it again.
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

// A failure only one wwd notices is pushed too, and the ledger row of a shared failure stays while any list still holds it.
func TestSilenceStaysOneFailureWhileAnyListHoldsIt(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26")
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count())
	// mlab's wwd stops noticing (a blip) and air26's takes over: still the same failure
	e.src.set("mlab")
	e.src.set("air26", silenceOf("mserv", t0.Add(time.Minute)))
	e.now = e.now.Add(2 * time.Minute)
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count(), "a hand-over between observers is not a new failure")
}

// A list that cannot be read may still hold the failure, going by what it held when it was last read: the ledger row is kept, so the
// failure that comes back is not told twice. Once every list that held it has been read and none holds it, the row goes and a new
// failure is told.
func TestSilenceLedgerKeepsWhatAnUnreadableListHeld(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26", silenceOf("mserv", t0))
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count())
	e.ntf.confirm(0) // APNs took it: the ledger holds it for the hour

	e.src.set("mlab")
	e.src.errs = map[string]error{"air26": errors.New("unreachable")}
	e.pass(t)
	e.src.set("mlab", silenceOf("mserv", t0.Add(10*time.Minute)))
	e.now = e.now.Add(10 * time.Minute)
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count(), "air26's list could not be read and held the failure last time: not a new one")

	e.src.errs = nil
	e.src.set("mlab")
	e.src.set("air26")
	e.now = e.now.Add(time.Minute)
	e.pass(t) // every list read, none holds it: forgotten
	e.src.set("mlab", silenceOf("mserv", e.now))
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 2, e.ntf.count(), "cleared everywhere and back: a new failure")
}

// A list that could not be read and did not hold the failure must not hold the ledger: a failure that cleared and came back within the hour
// is a new failure (the attack review: one unreadable list must not freeze every shared row).
func TestSilenceAnUnrelatedUnreadableListDoesNotFreezeTheLedger(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26")
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count())
	e.ntf.confirm(0)

	e.src.errs = map[string]error{"air26": errors.New("unreachable")} // air26's list never held it
	e.src.set("mlab")
	e.now = e.now.Add(time.Minute)
	e.pass(t) // cleared on every list that could hold it
	e.src.set("mlab", silenceOf("mserv", e.now))
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 2, e.ntf.count(), "mserv came back and failed again inside the hour: a new failure, whatever air26's list is doing")
}

// A list never read since this daemon started could hold anything: nothing is forgotten while it stays unread.
func TestSilenceANeverReadListKeepsTheLedger(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.errs = map[string]error{"air26": errors.New("unreachable")}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count())
	e.ntf.confirm(0)
	e.src.set("mlab")
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	e.src.set("mlab", silenceOf("mserv", e.now))
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count(), "air26 has never been read: the first failure may still be going on there")
}

// A pass that could not read the pool saw only the local list: the peers' lists may still hold the failure, so the ledger row stays (R1).
func TestSilenceLedgerStaysWhenThePoolCouldNotBeRead(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	e.src.set("mlab", silenceOf("mserv", t0))
	e.src.set("air26", silenceOf("mserv", t0))
	e.pass(t)
	assert.Equal(t, 1, e.ntf.count())
	e.ntf.confirm(0)

	e.src.poolErr = errors.New("wwd busy")
	e.src.set("mlab") // the local list no longer holds it, the peer's still does
	e.now = e.now.Add(time.Minute)
	e.pass(t) // only the local list was seen: nothing is forgotten
	e.src.poolErr = nil
	e.now = e.now.Add(time.Minute)
	e.pass(t) // the pool is back and air26's list still holds the same failure
	assert.Equal(t, 1, e.ntf.count(), "a pool read that failed must not make the same failure new")
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

package conversation

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/convmodel/ccnorm"
)

// Message stages (docs/plans/2026-10-19-message-stages-step2-plan.md §1.1): the in-memory ledger that follows each
// client_msg_id from received to read.

var t0 = time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC)

func ms(t time.Time) int64 { return t.UnixMilli() }

func stageOf(t *testing.T, l *stageLedger, sid, id string) stageJSON {
	t.Helper()
	all, _ := l.snapshot(sid, t0)
	for _, s := range all {
		if s.ClientMsgID == id {
			return s
		}
	}
	t.Fatalf("no stage for %q", id)
	return stageJSON{}
}

func uq(text string, at time.Time) ccnorm.Queued {
	return ccnorm.Queued{Text: text, At: ms(at), Source: ccnorm.QueuedUser}
}

func item(id, text string, at time.Time) stageItem { return stageItem{ID: id, Text: text, At: ms(at)} }

func TestStages_FillTransitionsToRead(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "hello", t0)
	assert.Equal(t, "received", stageOf(t, &l, "s", "a").Stage)
	l.set("s", "a", "filled", "", "fill", t0.Add(100*time.Millisecond))
	l.set("s", "a", "entered", "", "fill", t0.Add(200*time.Millisecond))
	got := stageOf(t, &l, "s", "a")
	assert.Equal(t, "entered", got.Stage)
	assert.Equal(t, "fill", got.Mode)
	// an idle session: the user item shows up a moment later, no queue row at all
	l.observe("s", nil, []stageItem{item("u1", "hello", t0.Add(400*time.Millisecond))}, t0.Add(500*time.Millisecond))
	assert.Equal(t, "read", stageOf(t, &l, "s", "a").Stage)
}

func TestStages_KeysModeKeepsTextAndReads(t *testing.T) {
	var l stageLedger
	l.begin("s", "k", "typed blind", t0)
	l.set("s", "k", "entered", "", "keys", t0.Add(time.Second))
	l.observe("s", nil, []stageItem{item("u1", "typed blind", t0.Add(1500*time.Millisecond))}, t0.Add(2*time.Second))
	assert.Equal(t, "read", stageOf(t, &l, "s", "k").Stage)
}

// A busy session: entered -> queued by the header's row, then read minutes later by an item of source queued. The 30 s
// echo window of an idle session must not apply once the id reached the queue.
func TestStages_BusyQueuedThenReadFiveMinutesLater(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "later", t0)
	l.set("s", "a", "entered", "", "fill", t0.Add(time.Second))
	l.observe("s", []ccnorm.Queued{uq("later", t0.Add(1100*time.Millisecond))}, nil, t0.Add(2*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
	// still queued after a while: nothing changes
	l.observe("s", []ccnorm.Queued{uq("later", t0.Add(1100*time.Millisecond))}, nil, t0.Add(2*time.Minute))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
	// the row left the queue (remove + queued_command): an item of source queued, five minutes after the Enter
	l.observe("s", nil, []stageItem{item("u9", "later", t0.Add(5*time.Minute))}, t0.Add(5*time.Minute+time.Second))
	assert.Equal(t, "read", stageOf(t, &l, "s", "a").Stage)
}

// Send now leaves the queue as dequeue rows and real user items (MS2-0 row 1): the same ledger path.
func TestStages_SendNowShapeReads(t *testing.T) {
	var l stageLedger
	for i, id := range []string{"a", "b"} {
		l.begin("s", id, "msg-"+id, t0)
		l.set("s", id, "entered", "", "fill", t0.Add(time.Duration(i+1)*time.Second))
	}
	q := []ccnorm.Queued{uq("msg-a", t0.Add(1100*time.Millisecond)), uq("msg-b", t0.Add(2100*time.Millisecond))}
	l.observe("s", q, nil, t0.Add(3*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
	assert.Equal(t, "queued", stageOf(t, &l, "s", "b").Stage)
	l.observe("s", nil, []stageItem{item("u1", "msg-a", t0.Add(10*time.Second)), item("u2", "msg-b", t0.Add(10*time.Second))}, t0.Add(11*time.Second))
	assert.Equal(t, "read", stageOf(t, &l, "s", "a").Stage)
	assert.Equal(t, "read", stageOf(t, &l, "s", "b").Stage)
}

func TestStages_FailedCarriesReasonAndUnknownAdvancesByMatching(t *testing.T) {
	var l stageLedger
	l.begin("s", "f", "x", t0)
	l.set("s", "f", "failed", "waiting", "", t0.Add(time.Second))
	got := stageOf(t, &l, "s", "f")
	assert.Equal(t, "failed", got.Stage)
	assert.Equal(t, "waiting", got.Reason)

	l.begin("s", "u", "maybe", t0)
	l.set("s", "u", "unknown", "mod_timeout", "fill", t0.Add(time.Second))
	assert.Equal(t, "unknown", stageOf(t, &l, "s", "u").Stage)
	l.observe("s", []ccnorm.Queued{uq("maybe", t0.Add(2*time.Second))}, nil, t0.Add(3*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "u").Stage, "an unknown id may still be claimed by the queue")

	l.begin("s", "u2", "maybe two", t0)
	l.set("s", "u2", "unknown", "x", "keys", t0.Add(time.Second))
	l.observe("s", nil, []stageItem{item("i", "maybe two", t0.Add(2*time.Second))}, t0.Add(3*time.Second))
	assert.Equal(t, "read", stageOf(t, &l, "s", "u2").Stage)
}

func TestStages_NeverMovesBack(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "x", t0)
	l.set("s", "a", "entered", "", "fill", t0)
	l.set("s", "a", "filled", "", "fill", t0.Add(time.Second)) // late report of an earlier step
	assert.Equal(t, "entered", stageOf(t, &l, "s", "a").Stage)
	l.set("s", "a", "read", "", "", t0.Add(2*time.Second))
	for _, back := range []string{"received", "filled", "entered", "queued", "unknown", "failed"} {
		l.set("s", "a", back, "r", "", t0.Add(3*time.Second))
		assert.Equal(t, "read", stageOf(t, &l, "s", "a").Stage, back)
	}
}

// The same id coming back as received after a refusal that sent nothing (a lock-wait busy never reaches the other
// ledgers) is a new attempt, not a step back of a sent message.
func TestStages_ReceivedAfterFailedStartsAgain(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "x", t0)
	l.set("s", "a", "failed", "busy", "", t0.Add(time.Second))
	l.begin("s", "a", "x", t0.Add(2*time.Second))
	assert.Equal(t, "received", stageOf(t, &l, "s", "a").Stage)
	l.set("s", "a", "entered", "", "fill", t0.Add(3*time.Second))
	l.begin("s", "a", "x", t0.Add(4*time.Second)) // a second begin of a live id changes nothing
	assert.Equal(t, "entered", stageOf(t, &l, "s", "a").Stage)
}

// Busy retries mint a new id each time: the old id stays failed/busy, the retry goes on and reads. The ledger keeps one
// row per id; the clients fold them (plan L1).
func TestStages_BusyRetryIdsAreIndependent(t *testing.T) {
	var l stageLedger
	l.begin("s", "id0", "msg", t0)
	l.set("s", "id0", "failed", "busy", "", t0.Add(time.Second))
	l.begin("s", "id1", "msg", t0.Add(2*time.Second))
	l.set("s", "id1", "entered", "", "fill", t0.Add(3*time.Second))
	l.observe("s", nil, []stageItem{item("u", "msg", t0.Add(3500*time.Millisecond))}, t0.Add(4*time.Second))
	assert.Equal(t, "failed", stageOf(t, &l, "s", "id0").Stage)
	assert.Equal(t, "read", stageOf(t, &l, "s", "id1").Stage)
}

func TestStages_RecallTakesBackEveryQueuedIdOnly(t *testing.T) {
	var l stageLedger
	for _, id := range []string{"a", "b"} {
		l.begin("s", id, "m-"+id, t0)
		l.set("s", id, "entered", "", "fill", t0.Add(time.Second))
	}
	l.observe("s", []ccnorm.Queued{uq("m-a", t0.Add(1100*time.Millisecond)), uq("m-b", t0.Add(1200*time.Millisecond))}, nil, t0.Add(2*time.Second))
	l.begin("s", "c", "m-c", t0.Add(3*time.Second))
	l.set("s", "c", "entered", "", "fill", t0.Add(3*time.Second)) // not queued yet
	l.recallAll("s", t0.Add(4*time.Second))
	assert.Equal(t, "recalled", stageOf(t, &l, "s", "a").Stage)
	assert.Equal(t, "recalled", stageOf(t, &l, "s", "b").Stage)
	assert.Equal(t, "entered", stageOf(t, &l, "s", "c").Stage)
	// the request id of the recall itself is only an idempotency key: no row for it
	all, _ := l.snapshot("s", t0.Add(5*time.Second))
	assert.Len(t, all, 3)
}

// The same text twice: the old id is read first, the new one stays queued (queue order, not time distance).
func TestStages_SameTextTwiceReadsInQueueOrder(t *testing.T) {
	var l stageLedger
	l.begin("s", "old", "same", t0)
	l.set("s", "old", "entered", "", "fill", t0.Add(time.Second))
	l.begin("s", "new", "same", t0.Add(10*time.Second))
	l.set("s", "new", "entered", "", "fill", t0.Add(11*time.Second))
	both := []ccnorm.Queued{uq("same", t0.Add(1100*time.Millisecond)), uq("same", t0.Add(11100*time.Millisecond))}
	l.observe("s", both, nil, t0.Add(12*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "old").Stage)
	assert.Equal(t, "queued", stageOf(t, &l, "s", "new").Stage)
	// the first row leaves the queue; its item is stamped much closer to the NEW id's Enter than to the old one's
	l.observe("s", both[1:], []stageItem{item("u1", "same", t0.Add(10500*time.Millisecond))}, t0.Add(13*time.Second))
	assert.Equal(t, "read", stageOf(t, &l, "s", "old").Stage)
	assert.Equal(t, "queued", stageOf(t, &l, "s", "new").Stage)
}

// An item is never given to two ids.
func TestStages_OneItemOneId(t *testing.T) {
	var l stageLedger
	for _, id := range []string{"a", "b"} {
		l.begin("s", id, "dup", t0)
		l.set("s", id, "entered", "", "fill", t0.Add(time.Second))
	}
	l.observe("s", nil, []stageItem{item("u1", "dup", t0.Add(1500*time.Millisecond))}, t0.Add(2*time.Second))
	reads := 0
	for _, id := range []string{"a", "b"} {
		if stageOf(t, &l, "s", id).Stage == "read" {
			reads++
		}
	}
	assert.Equal(t, 1, reads)
}

func TestStages_TextIsDroppedAtTerminalAndCapped(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "x", t0)
	assert.Equal(t, len("x"), l.textBytes)
	l.set("s", "a", "failed", "r", "", t0)
	assert.Zero(t, l.textBytes, "a failed id keeps no text")

	l.begin("s", "big", strings.Repeat("a", maxStageText+1), t0)
	assert.Zero(t, l.textBytes, "a text over the per-text cap is not kept")
	assert.Equal(t, "received", stageOf(t, &l, "s", "big").Stage, "but the id is still followed")

	// the total cap drops the oldest in-flight texts first
	var l2 stageLedger
	per := maxStageText
	n := maxStageTotal/per + 2
	for i := 0; i < n; i++ {
		l2.begin("s", "id"+string(rune('a'+i)), strings.Repeat("b", per), t0.Add(time.Duration(i)*time.Second))
	}
	assert.LessOrEqual(t, l2.textBytes, maxStageTotal)
	assert.Empty(t, l2.sessions["s"].rows["ida"].text, "the oldest text went first")
	assert.NotEmpty(t, l2.sessions["s"].rows["id"+string(rune('a'+n-1))].text)
}

func TestStages_TextDroppedAfterThirtyMinutes(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "slow", t0)
	l.set("s", "a", "entered", "", "fill", t0)
	l.observe("s", nil, nil, t0.Add(31*time.Minute))
	assert.Zero(t, l.textBytes)
	assert.Equal(t, "entered", stageOf(t, &l, "s", "a").Stage, "the stage stays, only the text goes")
}

func TestStages_TerminalRowsExpireAfterTenMinutes(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "x", t0)
	l.set("s", "a", "failed", "r", "", t0)
	all, _ := l.snapshot("s", t0.Add(9*time.Minute))
	assert.Len(t, all, 1)
	all, _ = l.snapshot("s", t0.Add(11*time.Minute))
	assert.Empty(t, all)
}

// Frames carry only what changed since the connection's seq; a quiet ledger has nothing to send.
func TestStages_SinceReturnsOnlyNewChanges(t *testing.T) {
	var l stageLedger
	_, base := l.snapshot("s", t0)
	got, seq := l.since("s", base, t0)
	assert.Empty(t, got)
	assert.Equal(t, base, seq)

	l.begin("s", "a", "x", t0)
	l.set("s", "a", "entered", "", "fill", t0)
	l.begin("s", "b", "y", t0)
	l.set("s", "b", "failed", "busy", "", t0)
	got, seq = l.since("s", base, t0)
	require.Len(t, got, 2, "three changes of two ids in one tick are one frame, one stage per id")
	assert.Equal(t, "entered", got[0].Stage)
	assert.Equal(t, "failed", got[1].Stage)

	got, _ = l.since("s", seq, t0)
	assert.Empty(t, got, "nothing new since the last frame")

	// a no-op write (same stage again) is not a change
	l.set("s", "a", "entered", "", "fill", t0)
	got, _ = l.since("s", seq, t0)
	assert.Empty(t, got)
}

func TestStages_SnapshotBaselineDoesNotReplayOlderDeltas(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "x", t0)
	l.set("s", "a", "entered", "", "fill", t0)
	all, seq := l.snapshot("s", t0)
	require.Len(t, all, 1)
	got, _ := l.since("s", seq, t0)
	assert.Empty(t, got)
}

func TestStages_SessionsAreSeparate(t *testing.T) {
	var l stageLedger
	l.begin("s1", "a", "x", t0)
	all, _ := l.snapshot("s2", t0)
	assert.Empty(t, all)
}

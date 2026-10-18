package conversation

import (
	"fmt"
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
	l.observe("s", nil, false, []stageItem{item("u1", "hello", t0.Add(400*time.Millisecond))}, t0.Add(500*time.Millisecond))
	assert.Equal(t, "read", stageOf(t, &l, "s", "a").Stage)
}

func TestStages_KeysModeKeepsTextAndReads(t *testing.T) {
	var l stageLedger
	l.begin("s", "k", "typed blind", t0)
	l.set("s", "k", "entered", "", "keys", t0.Add(time.Second))
	l.observe("s", nil, false, []stageItem{item("u1", "typed blind", t0.Add(1500*time.Millisecond))}, t0.Add(2*time.Second))
	assert.Equal(t, "read", stageOf(t, &l, "s", "k").Stage)
}

// A busy session: entered -> queued by the header's row, then read minutes later by an item of source queued. The 30 s
// echo window of an idle session must not apply once the id reached the queue.
func TestStages_BusyQueuedThenReadFiveMinutesLater(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "later", t0)
	l.set("s", "a", "entered", "", "fill", t0.Add(time.Second))
	l.observe("s", []ccnorm.Queued{uq("later", t0.Add(1100*time.Millisecond))}, false, nil, t0.Add(2*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
	// still queued after a while: nothing changes
	l.observe("s", []ccnorm.Queued{uq("later", t0.Add(1100*time.Millisecond))}, false, nil, t0.Add(2*time.Minute))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
	// the row left the queue (remove + queued_command): an item of source queued, five minutes after the Enter
	l.observe("s", nil, false, []stageItem{item("u9", "later", t0.Add(5*time.Minute))}, t0.Add(5*time.Minute+time.Second))
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
	l.observe("s", q, false, nil, t0.Add(3*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
	assert.Equal(t, "queued", stageOf(t, &l, "s", "b").Stage)
	l.observe("s", nil, false, []stageItem{item("u1", "msg-a", t0.Add(10*time.Second)), item("u2", "msg-b", t0.Add(10*time.Second))}, t0.Add(11*time.Second))
	assert.Equal(t, "read", stageOf(t, &l, "s", "a").Stage)
	assert.Equal(t, "read", stageOf(t, &l, "s", "b").Stage)
}

// A queue row without a timestamp must not let an older item with the same text read the id (codex R1 on #3409).
func TestStages_OlderSameTextItemDoesNotReadAQueuedId(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "again", t0)
	l.set("s", "a", "entered", "", "fill", t0.Add(time.Second))
	l.observe("s", []ccnorm.Queued{{Text: "again", Source: ccnorm.QueuedUser}}, false, nil, t0.Add(2*time.Second))
	require.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
	old := item("u0", "again", t0.Add(-time.Hour)) // typed an hour ago
	l.observe("s", nil, false, []stageItem{old}, t0.Add(3*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
	l.observe("s", nil, false, []stageItem{old, item("u1", "again", t0.Add(2500*time.Millisecond))}, t0.Add(4*time.Second))
	assert.Equal(t, "read", stageOf(t, &l, "s", "a").Stage)
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
	l.observe("s", []ccnorm.Queued{uq("maybe", t0.Add(2*time.Second))}, false, nil, t0.Add(3*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "u").Stage, "an unknown id may still be claimed by the queue")

	l.begin("s", "u2", "maybe two", t0)
	l.set("s", "u2", "unknown", "x", "keys", t0.Add(time.Second))
	l.observe("s", nil, false, []stageItem{item("i", "maybe two", t0.Add(2*time.Second))}, t0.Add(3*time.Second))
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
	l.observe("s", nil, false, []stageItem{item("u", "msg", t0.Add(3500*time.Millisecond))}, t0.Add(4*time.Second))
	assert.Equal(t, "failed", stageOf(t, &l, "s", "id0").Stage)
	assert.Equal(t, "read", stageOf(t, &l, "s", "id1").Stage)
}

func TestStages_RecallTakesBackEveryQueuedIdOnly(t *testing.T) {
	var l stageLedger
	for _, id := range []string{"a", "b"} {
		l.begin("s", id, "m-"+id, t0)
		l.set("s", id, "entered", "", "fill", t0.Add(time.Second))
	}
	l.observe("s", []ccnorm.Queued{uq("m-a", t0.Add(1100*time.Millisecond)), uq("m-b", t0.Add(1200*time.Millisecond))}, false, nil, t0.Add(2*time.Second))
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
	l.observe("s", both, false, nil, t0.Add(12*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "old").Stage)
	assert.Equal(t, "queued", stageOf(t, &l, "s", "new").Stage)
	// the first row leaves the queue; its item is stamped much closer to the NEW id's Enter than to the old one's
	l.observe("s", both[1:], false, []stageItem{item("u1", "same", t0.Add(10500*time.Millisecond))}, t0.Add(13*time.Second))
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
	l.observe("s", nil, false, []stageItem{item("u1", "dup", t0.Add(1500*time.Millisecond))}, t0.Add(2*time.Second))
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
	l.observe("s", nil, false, nil, t0.Add(31*time.Minute))
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

// A later frame carries only the id that changed after the connection's seq, not everything the ledger holds.
func TestStages_SinceSecondFrameHoldsOnlyTheNewChange(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "x", t0)
	_, seq := l.since("s", 0, t0)
	l.begin("s", "b", "y", t0)
	got, _ := l.since("s", seq, t0)
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].ClientMsgID)
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

// The feed's queue tracker keeps at most ccnorm.MaxQueueEntries rows: when it may have dropped older ones, a row that is not
// there proves nothing, and a later item with the same text must not read the id (codex attack on #3409).
func TestStages_ALossyQueueNeverJudgesAnIdToHaveLeft(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "same", t0)
	l.set("s", "a", "entered", "", "fill", t0.Add(time.Second))
	q := []ccnorm.Queued{uq("same", t0.Add(1100*time.Millisecond))}
	l.observe("s", q, false, nil, t0.Add(2*time.Second))
	require.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
	l.observe("s", nil, true, []stageItem{item("u1", "same", t0.Add(3*time.Second))}, t0.Add(4*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage, "the row may only have been pushed out of the tracker")
	l.observe("s", nil, false, []stageItem{item("u1", "same", t0.Add(3*time.Second))}, t0.Add(5*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage, "and it stays unjudged until the queue is seen empty (see the sticky test)")
}

func TestStages_UsedItemsAreForgottenWhenNoIdCouldStillMatchThem(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "x", t0)
	l.set("s", "a", "entered", "", "fill", t0)
	l.observe("s", nil, false, []stageItem{item("u1", "x", t0.Add(time.Second))}, t0.Add(2*time.Second))
	require.Equal(t, "read", stageOf(t, &l, "s", "a").Stage)
	assert.Len(t, l.sessions["s"].used, 1)
	l.observe("s", nil, false, nil, t0.Add(3*time.Second)) // nothing is in flight any more: nothing can still match it
	assert.Empty(t, l.sessions["s"].used)
	l.begin("s", "c", "z", t0.Add(4*time.Second))
	l.set("s", "c", "entered", "", "fill", t0.Add(4*time.Second))
	l.observe("s", nil, false, []stageItem{item("u2", "nope", t0.Add(5*time.Second))}, t0.Add(5*time.Second))
	l.sessions["s"].used["u1"] = ms(t0.Add(time.Second))
	l.begin("s", "b", "y", t0.Add(time.Minute)) // an id in flight later: u1 is far older than anything it could match
	l.set("s", "b", "entered", "", "fill", t0.Add(time.Minute))
	l.observe("s", nil, false, nil, t0.Add(time.Minute+time.Second))
	assert.Empty(t, l.sessions["s"].used)
}

func TestStages_IdsPerSessionAreBounded(t *testing.T) {
	var l stageLedger
	l.begin("s", "oldest-live", "x", t0)
	l.begin("s", "first-done", "x", t0.Add(time.Millisecond))
	l.set("s", "first-done", "failed", "r", "", t0)
	for i := 0; i < maxStageRows-1; i++ {
		l.begin("s", "id"+string(rune('A'+i%26))+string(rune('a'+i/26)), "y", t0.Add(time.Duration(i+1)*time.Millisecond))
	}
	assert.LessOrEqual(t, len(l.sessions["s"].rows), maxStageRows)
	_, has := l.sessions["s"].rows["first-done"]
	assert.False(t, has, "the finished id goes first")
	_, live := l.sessions["s"].rows["oldest-live"]
	assert.True(t, live, "an id still in flight is kept while a finished one can go")
}

// A queued id keeps no second copy of its text: the one counted copy is what the caps and the TTL act on.
func TestStages_QueuedIdsRetainNoTextBeyondTheCounted(t *testing.T) {
	var l stageLedger
	big := strings.Repeat("q", maxStageText)
	var q []ccnorm.Queued
	for i := 0; i < 5; i++ {
		id := "id" + string(rune('a'+i))
		text := big + string(rune('a'+i))
		text = text[1:] // keep each text distinct but exactly at the cap
		l.begin("s", id, text, t0)
		l.set("s", id, "entered", "", "fill", t0)
		q = append(q, uq(text, t0))
	}
	l.observe("s", q, false, nil, t0.Add(time.Second))
	assert.Equal(t, 5*maxStageText, l.textBytes)
	l.observe("s", q, false, nil, t0.Add(31*time.Minute))
	assert.Zero(t, l.textBytes, "after the TTL nothing of the texts is left, claims included")
	for _, r := range l.sessions["s"].rows {
		assert.Empty(t, r.text)
	}
}

// A queued id is read by an item stamped no more than stageItemQueueSkew before its queue row: a queued_command attachment
// carries the time the person sent the text, which is before the enqueue record that makes the row (#3452).
func TestStages_QueuedIdIsReadOnlyByItemsWithinTheSkewOfTheRow(t *testing.T) {
	for _, c := range []struct {
		name  string
		delta time.Duration
		want  string
	}{
		{"past the skew before the row", -stageItemQueueSkew - time.Millisecond, "queued"},
		{"at the skew before the row", -stageItemQueueSkew, "read"},
		{"at the row", 0, "read"},
		{"after the row", time.Millisecond, "read"},
	} {
		var l stageLedger
		row := t0.Add(5 * time.Second)
		l.begin("s", "a", "dup", t0)
		l.set("s", "a", "entered", "", "fill", t0.Add(500*time.Millisecond))
		l.observe("s", []ccnorm.Queued{uq("dup", row)}, false, nil, t0.Add(6*time.Second))
		require.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage, c.name)
		l.observe("s", nil, false, []stageItem{item("u1", "dup", row.Add(c.delta))}, t0.Add(8*time.Second))
		assert.Equal(t, c.want, stageOf(t, &l, "s", "a").Stage, c.name)
	}
}

// The timestamps measured on alpha.754 (#3452): the attachment 23:39:41.028, the enqueue record 23:39:41.261. The message was
// absorbed mid-turn, so it exists only as the attachment, which is older than its row.
func TestStages_AbsorbedMidTurnAttachmentOlderThanItsRowReads(t *testing.T) {
	var l stageLedger
	enter := t0.Add(41 * time.Second) // the seconds of the measured :41.028 / :41.261
	row := enter.Add(261 * time.Millisecond)
	l.begin("s", "a", "queued message D", enter)
	l.set("s", "a", "entered", "", "fill", enter.Add(100*time.Millisecond))
	l.observe("s", []ccnorm.Queued{uq("queued message D", row)}, false, nil, enter.Add(time.Second))
	require.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
	l.observe("s", nil, false, []stageItem{item("u1", "queued message D", enter.Add(28*time.Millisecond))}, enter.Add(5*time.Second))
	assert.Equal(t, "read", stageOf(t, &l, "s", "a").Stage)
}

// Two same-text messages queued next to each other, both items inside the skew of both rows: the oldest item goes to the
// oldest id and each item to one id only, whether they leave in one look or in two.
func TestStages_AdjacentSameTextQueuedIdsDoNotSwapInsideTheSkew(t *testing.T) {
	for _, together := range []bool{true, false} {
		var l stageLedger
		l.begin("s", "a", "dup", t0)
		l.set("s", "a", "entered", "", "fill", t0.Add(100*time.Millisecond))
		l.begin("s", "b", "dup", t0.Add(300*time.Millisecond))
		l.set("s", "b", "entered", "", "fill", t0.Add(400*time.Millisecond))
		ra, rb := t0.Add(1000*time.Millisecond), t0.Add(1200*time.Millisecond)
		l.observe("s", []ccnorm.Queued{uq("dup", ra), uq("dup", rb)}, false, nil, t0.Add(2*time.Second))
		require.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
		require.Equal(t, "queued", stageOf(t, &l, "s", "b").Stage)
		ua, ub := item("u1", "dup", ra.Add(-230*time.Millisecond)), item("u2", "dup", rb.Add(-230*time.Millisecond))
		if together {
			l.observe("s", nil, false, []stageItem{ua, ub}, t0.Add(3*time.Second))
			assert.Equal(t, "read", stageOf(t, &l, "s", "a").Stage)
			assert.Equal(t, "read", stageOf(t, &l, "s", "b").Stage)
			continue
		}
		// a leaves first; its item alone must not read b too
		l.observe("s", []ccnorm.Queued{uq("dup", rb)}, false, []stageItem{ua}, t0.Add(3*time.Second))
		assert.Equal(t, "read", stageOf(t, &l, "s", "a").Stage)
		assert.Equal(t, "queued", stageOf(t, &l, "s", "b").Stage, "one item reads one id")
		l.observe("s", nil, false, []stageItem{ua, ub}, t0.Add(4*time.Second))
		assert.Equal(t, "read", stageOf(t, &l, "s", "b").Stage)
	}
}

// Once the tracker overflowed, the header shrinking back under the limit does not make a missing row proof of anything.
func TestStages_LossyStaysUntilTheQueueIsSeenEmpty(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "same", t0)
	l.set("s", "a", "entered", "", "fill", t0.Add(time.Second))
	row := []ccnorm.Queued{uq("same", t0.Add(1100*time.Millisecond))}
	l.observe("s", row, false, nil, t0.Add(2*time.Second))
	require.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage)
	l.observe("s", row, true, nil, t0.Add(3*time.Second)) // 50 rows: the oldest may have been pushed out
	other := []ccnorm.Queued{uq("other", t0.Add(2*time.Second))}
	l.observe("s", other, false, []stageItem{item("u1", "same", t0.Add(4*time.Second))}, t0.Add(5*time.Second)) // shrunk to 49 and our row is gone
	assert.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage, "the overflow is remembered")
	l.observe("s", nil, false, []stageItem{item("u1", "same", t0.Add(4*time.Second))}, t0.Add(6*time.Second))
	assert.Equal(t, "queued", stageOf(t, &l, "s", "a").Stage, "an empty header proves nothing while an id is still queued")
	l.recallAll("s", t0.Add(7*time.Second)) // the person took the queue back: nothing is queued, the tracker is whole again
	l.observe("s", nil, false, nil, t0.Add(8*time.Second))
	assert.False(t, l.sessions["s"].lossy)
}

func TestStages_UsedItemsAreBoundedByCountWhateverTheTraffic(t *testing.T) {
	var l stageLedger
	l.begin("s", "pin", "pinned", t0) // one old id in flight keeps every window open
	l.set("s", "pin", "entered", "", "fill", t0)
	n := maxStageUsed + 50
	for i := 0; i < n; i++ {
		id := "m" + string(rune('A'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('0'+i/676))
		at := t0.Add(time.Duration(i+1) * time.Millisecond)
		l.begin("s", id, "x", at)
		l.set("s", id, "entered", "", "fill", at)
		l.observe("s", nil, false, []stageItem{item("it-"+id, "x", at)}, at)
	}
	assert.LessOrEqual(t, len(l.sessions["s"].used), maxStageUsed)
}

// An overflow seen long ago no longer blocks judging: ids that old have lost their text, and new ones are not at risk.
func TestStages_LossyExpiresAfterTheTextTTL(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "x", t0)
	l.set("s", "a", "entered", "", "fill", t0)
	l.observe("s", []ccnorm.Queued{uq("x", t0)}, false, nil, t0.Add(time.Second))
	l.observe("s", []ccnorm.Queued{uq("y", t0)}, true, nil, t0.Add(2*time.Second))
	require.True(t, l.sessions["s"].lossy)
	l.observe("s", []ccnorm.Queued{uq("y", t0)}, false, nil, t0.Add(stageTextTTL-time.Minute))
	assert.True(t, l.sessions["s"].lossy, "still inside the window")
	l.observe("s", []ccnorm.Queued{uq("y", t0)}, false, nil, t0.Add(stageTextTTL+time.Minute))
	assert.False(t, l.sessions["s"].lossy)
}

// Trimming the used map by count must not let a trimmed item be given out a second time.
func TestStages_ATrimmedUsedItemIsNeverMatchedAgain(t *testing.T) {
	var l stageLedger
	l.begin("s", "pin", "pinned", t0)
	l.set("s", "pin", "entered", "", "fill", t0)
	first := item("it-first", "x", t0.Add(time.Millisecond))
	l.begin("s", "first", "x", first.at())
	l.set("s", "first", "entered", "", "fill", first.at())
	l.observe("s", nil, false, []stageItem{first}, first.at())
	require.Equal(t, "read", stageOf(t, &l, "s", "first").Stage)
	for i := 0; i < maxStageUsed+5; i++ {
		id := "f" + string(rune('A'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('0'+i/676))
		at := t0.Add(time.Duration(i+10) * time.Millisecond)
		l.begin("s", id, "x", at)
		l.set("s", id, "entered", "", "fill", at)
		l.observe("s", nil, false, []stageItem{item("it-"+id, "x", at)}, at)
	}
	_, still := l.sessions["s"].used["it-first"]
	require.False(t, still, "the oldest marker was trimmed")
	late := t0.Add(2 * time.Millisecond) // inside the idle window of the used item
	l.begin("s", "late", "x", late)
	l.set("s", "late", "entered", "", "fill", late)
	l.observe("s", nil, false, []stageItem{first}, late) // the transcript offers the already used item again
	assert.Equal(t, "entered", stageOf(t, &l, "s", "late").Stage)
}

func (i stageItem) at() time.Time { return time.UnixMilli(i.At) }

// A trim that cuts inside one millisecond must not shut out the unused items of that millisecond (codex re-review on #3409).
func TestStages_TrimInsideOneMillisecondKeepsTheFloorBelowIt(t *testing.T) {
	s := &sessionStages{used: map[string]int64{}}
	for i := 0; i < maxStageUsed+2; i++ {
		s.used[fmt.Sprintf("a%d", i)] = 100 // one busy millisecond
	}
	s.used["old"] = 50
	s.trimUsed()
	assert.Less(t, s.usedFloor, int64(100), "items of that millisecond that were never used must stay matchable")
	assert.Len(t, s.used, maxStageUsed+2, "and every marker of that millisecond stays, so a used item is not handed out twice")
	_, oldKept := s.used["old"]
	assert.False(t, oldKept, "the strictly older marker is what went")

	one := &sessionStages{used: map[string]int64{}}
	for i := 0; i < 3*maxStageUsed; i++ {
		one.used[fmt.Sprintf("c%d", i)] = 70 // every marker in the same millisecond
	}
	one.trimUsed()
	assert.LessOrEqual(t, len(one.used), maxStageUsed, "the hard bound holds even when the whole map is one millisecond")
	assert.Equal(t, int64(70), one.usedFloor)

	s2 := &sessionStages{used: map[string]int64{}}
	for i := 0; i < maxStageUsed+2; i++ {
		s2.used[fmt.Sprintf("b%d", i)] = int64(i + 1)
	}
	s2.trimUsed()
	assert.Equal(t, int64(2), s2.usedFloor, "a cut between milliseconds raises the floor to the last trimmed one")
}

// The request that holds the locks owns the id's stages: one that gave up earlier cannot overwrite them (codex R1 on #3420).
func TestStages_ALaterClaimOwnsTheWrites(t *testing.T) {
	var l stageLedger
	first := l.begin("s", "a", "x", t0)
	second := l.claim("s", "a", "x", t0.Add(time.Second))
	require.NotEqual(t, first, second)
	l.setAttempt("s", "a", second, "entered", "", "fill", t0.Add(2*time.Second))
	l.setAttempt("s", "a", first, "failed", "busy", "", t0.Add(3*time.Second)) // the request that timed out at the locks, late
	assert.Equal(t, "entered", stageOf(t, &l, "s", "a").Stage)
	// and the earlier request's failure still stands when nobody claimed the id after it
	l.begin("s", "b", "y", t0)
	l.setAttempt("s", "b", 1, "failed", "busy", "", t0)
	assert.Equal(t, "failed", stageOf(t, &l, "s", "b").Stage)
	// a claim also revives an id an earlier request marked failed
	third := l.claim("s", "b", "y", t0.Add(time.Second))
	l.setAttempt("s", "b", third, "entered", "", "fill", t0.Add(2*time.Second))
	assert.Equal(t, "entered", stageOf(t, &l, "s", "b").Stage)
}

// A session the ledger swept and then heard from again keeps counting upwards, so a long-lived connection with an older
// cursor still gets the new stages (review of #3409 by olga).
func TestStages_SeqSurvivesASweptSession(t *testing.T) {
	var l stageLedger
	for i := 0; i < 5; i++ { // push the cursor up
		l.begin("s", fmt.Sprintf("old%d", i), "x", t0)
		l.set("s", fmt.Sprintf("old%d", i), "failed", "r", "", t0)
	}
	_, cursor := l.snapshot("s", t0)
	require.Greater(t, cursor, uint64(3))
	l.snapshot("s", t0.Add(time.Hour)) // everything expired: the session is gone
	_, gone := l.sessions["s"]
	require.False(t, gone)
	l.begin("s", "new", "y", t0.Add(time.Hour))
	got, seq := l.since("s", cursor, t0.Add(time.Hour))
	require.Len(t, got, 1, "the connection with the older cursor still hears the new id")
	assert.Equal(t, "new", got[0].ClientMsgID)
	assert.Greater(t, seq, cursor)
	// and a connection never moves its cursor back when nothing changed
	_, same := l.since("s", seq+10, t0.Add(time.Hour))
	assert.Equal(t, seq+10, same)
}

// The connections of a session share one look at each transcript revision (codex attack on #3421).
func TestStages_SeenAlreadyUntilTheRevisionOrTheSessionMoves(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "x", t0)
	assert.False(t, l.seenAlready("s", "e:1"), "nothing observed yet")
	l.markSeen("s", "e:1", l.sessionSeq("s"))
	assert.True(t, l.seenAlready("s", "e:1"))
	assert.False(t, l.seenAlready("s", "e:2"), "a new transcript revision is worth a look")
	l.begin("s", "b", "y", t0) // the session's ledger moved
	assert.False(t, l.seenAlready("s", "e:1"))
	assert.False(t, l.seenAlready("other", "e:1"))
}

// A change that lands while an observer works is not covered by its look (codex critic on #3421).
func TestStages_AnIdThatEntersDuringALookIsStillLookedAt(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "x", t0)
	seq := l.sessionSeq("s") // the observer starts here
	l.begin("s", "late", "y", t0)
	l.set("s", "late", "entered", "", "fill", t0)
	l.markSeen("s", "e:1", seq) // and finishes without having seen "late"
	assert.False(t, l.seenAlready("s", "e:1"))
}

// Another session's traffic does not spoil this session's shared look.
func TestStages_AnotherSessionsChangesDoNotInvalidateTheLook(t *testing.T) {
	var l stageLedger
	l.begin("s", "a", "x", t0)
	l.markSeen("s", "e:1", l.sessionSeq("s"))
	l.begin("other", "z", "z", t0)
	l.set("other", "z", "entered", "", "fill", t0)
	assert.True(t, l.seenAlready("s", "e:1"))
}

func TestStages_WindowFromIsTheEarliestOpenWindow(t *testing.T) {
	var l stageLedger
	_, ok := l.windowFrom("s")
	assert.False(t, ok)
	l.begin("s", "a", "x", t0.Add(10*time.Second))
	l.begin("s", "b", "y", t0.Add(5*time.Second))
	l.begin("s", "done", "z", t0)
	l.set("s", "done", "failed", "r", "", t0)
	from, ok := l.windowFrom("s")
	require.True(t, ok)
	assert.Equal(t, ms(t0.Add(5*time.Second))-stageItemBefore.Milliseconds(), from, "finished ids do not hold the window open")
}

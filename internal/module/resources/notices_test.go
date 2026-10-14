package resourcesmod

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/resources"
)

// fakeNotices is the prompt queue as the outbox sees it: it records every try and answers from a script.
type fakeNotices struct {
	mu     sync.Mutex
	calls  []noticeCall
	answer func(n int, c noticeCall) (promptq.Result, error)
}

type noticeCall struct{ session, key, text string }

func (f *fakeNotices) SubmitNotice(_ context.Context, session, key, text string) (promptq.Result, error) {
	f.mu.Lock()
	c := noticeCall{session, key, text}
	f.calls = append(f.calls, c)
	n := len(f.calls)
	answer := f.answer
	f.mu.Unlock()
	if answer == nil {
		return promptq.Result{Status: promptq.Accepted}, nil
	}
	return answer(n, c)
}

func (f *fakeNotices) tries() []noticeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]noticeCall(nil), f.calls...)
}

func (f *posFix) outbox(answer func(n int, c noticeCall) (promptq.Result, error)) *fakeNotices {
	n := &fakeNotices{answer: answer}
	f.m.noticeQ = n
	return n
}

// notices runs one pass of the outbox and waits for the tries it started.
func (f *posFix) notices() {
	f.t.Helper()
	f.m.noticePass(context.Background())
	f.m.wg.Wait()
}

// endWithSlot ends a position as the sweeper would, with a notice slot of the kind armed at endedAt (a test of the boot
// replay needs slots of a known age).
func (f *posFix) endWithSlot(posID string, endedAt int64, kind string) {
	f.t.Helper()
	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET state = 'ended', ended_at = ?, end_reason = 'no_answer' WHERE position_id = ? AND state = 'waiting'`, endedAt, posID)
	require.NoError(f.t, err)
	_, err = f.m.store.db.Exec(`UPDATE resource_positions SET state = 'ended', end_reason = 'no_answer', ended_at = ?, window_until = 0,
		notice_kind = ?, notice_due_at = ?, notice_sent_at = 0, notice_tries = 0, notice_result = '' WHERE id = ?`, endedAt, kind, endedAt, posID)
	require.NoError(f.t, err)
}

func status(s string) func(int, noticeCall) (promptq.Result, error) {
	return func(int, noticeCall) (promptq.Result, error) { return promptq.Result{Status: s}, nil }
}

// An accepted offer notice marks the slot sent, delivers the offer (D1) and starts the 3-minute window now.
func TestNotices_OfferSentWhenIdle(t *testing.T) {
	f := newPosFix(t)
	n := f.outbox(nil)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.advance(4 * time.Second)
	f.notices()
	tries := n.tries()
	require.Len(t, tries, 1)
	assert.Equal(t, "sid-agent", tries[0].session)
	assert.Contains(t, tries[0].text, "輪到你了：`go build`")
	assert.Contains(t, tries[0].text, "pdx lease cancel "+posID)
	p := f.position(posID)
	assert.Equal(t, f.nowMS(), p.NoticeSentAt)
	assert.Equal(t, promptq.Accepted, p.NoticeResult)
	assert.Equal(t, f.nowMS(), p.DeliveredAt)
	assert.Equal(t, f.nowMS()+window.Milliseconds(), p.WindowUntil)
	f.notices()
	assert.Len(t, n.tries(), 1, "a sent slot is not sent again")
}

// The window counts from delivery: the mod answers busy for 90 s, then accepts; the window ends 3:00 after that, not 3:00
// after the offer. (M: start the window at the offer.)
func TestNotices_WindowCountsFromDelivery(t *testing.T) {
	f := newPosFix(t)
	busy := true
	n := f.outbox(func(int, noticeCall) (promptq.Result, error) {
		if busy {
			return promptq.Result{Status: promptq.Busy}, nil
		}
		return promptq.Result{Status: promptq.Accepted}, nil
	})
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	offeredAt := f.position(posID).OfferedAt
	for i := 0; i < 45; i++ { // 90 s of busy, a try every 2 s
		f.notices()
		f.advance(noticeRetryBusy)
	}
	busy = false
	f.notices()
	p := f.position(posID)
	assert.Equal(t, f.nowMS(), p.DeliveredAt)
	assert.Equal(t, f.nowMS()+window.Milliseconds(), p.WindowUntil)
	assert.Greater(t, p.WindowUntil, offeredAt+window.Milliseconds(), "later than a window from the offer")
	assert.Greater(t, len(n.tries()), 40)
}

// A notice the session never took in 3 minutes: the offer passes (P15), the slot is cleared, and the next waiter's offer
// goes out.
func TestNotices_UndeliveredOfferPasses(t *testing.T) {
	f := newPosFix(t)
	n := f.outbox(status(promptq.Busy))
	posA, posB := f.offeredWithAWaiter()
	for i := 0; i < 90; i++ { // 3 minutes
		f.notices()
		f.advance(noticeRetryBusy)
	}
	f.sweep()
	a := f.position(posA)
	require.Equal(t, resources.PosQueued, a.State, "the offer passed")
	assert.Equal(t, "", a.NoticeKind, "the slot is cleared")
	f.setZone("normal", "flat")
	f.pass()
	require.Equal(t, resources.PosOffered, f.position(posB).State)
	n.mu.Lock()
	n.answer = nil
	n.mu.Unlock()
	f.notices()
	last := n.tries()[len(n.tries())-1]
	assert.Contains(t, last.text, "go test", "the next waiter's offer goes out")
}

// busy and timeout were never sent: the same key is tried again after 2 s, not sooner. (M: give up on busy.)
func TestNotices_BusyAndTimeoutRetryWithTheSameKey(t *testing.T) {
	f := newPosFix(t)
	n := f.outbox(func(c int, _ noticeCall) (promptq.Result, error) {
		switch c {
		case 1:
			return promptq.Result{Status: promptq.Busy}, nil
		case 2:
			return promptq.Result{Status: promptq.Timeout}, nil
		}
		return promptq.Result{Status: promptq.Accepted}, nil
	})
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.notices()
	f.notices() // too soon: nothing is tried
	require.Len(t, n.tries(), 1)
	f.advance(noticeRetryBusy)
	f.notices()
	f.advance(noticeRetryBusy)
	f.notices()
	tries := n.tries()
	require.Len(t, tries, 3)
	assert.Equal(t, tries[0].key, tries[1].key)
	assert.Equal(t, tries[0].key, tries[2].key)
	assert.Equal(t, promptq.Accepted, f.position(posID).NoticeResult)
}

// A queue that refuses (16 jobs wait for the session) is tried again after 5 s.
func TestNotices_ErrBusyRetries(t *testing.T) {
	f := newPosFix(t)
	n := f.outbox(func(c int, _ noticeCall) (promptq.Result, error) {
		if c == 1 {
			return promptq.Result{}, promptq.ErrBusy
		}
		return promptq.Result{Status: promptq.Accepted}, nil
	})
	f.queuedThenOffered(newCID(), pidA, "go build")
	f.notices()
	f.advance(noticeRetryBusy)
	f.notices()
	require.Len(t, n.tries(), 1, "not before 5 s")
	f.advance(noticeRetryOther - noticeRetryBusy)
	f.notices()
	assert.Len(t, n.tries(), 2)
}

// unknown (handed out, no result) is not a delivery: the window does not start, the slot is tried again with a fresh key
// while it is current, and the window starts only at the later accepted. (M: treat unknown as delivered.)
func TestNotices_UnknownIsNotADelivery(t *testing.T) {
	f := newPosFix(t)
	n := f.outbox(func(c int, _ noticeCall) (promptq.Result, error) {
		if c == 1 {
			return promptq.Result{Status: promptq.Unknown, Reason: "no_result"}, nil
		}
		return promptq.Result{Status: promptq.Accepted}, nil
	})
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.notices()
	p := f.position(posID)
	assert.Equal(t, int64(0), p.DeliveredAt)
	assert.Equal(t, 1, p.NoticeTries)
	assert.Equal(t, promptq.Unknown, p.NoticeResult)
	f.advance(noticeRetryBusy)
	f.notices()
	tries := n.tries()
	require.Len(t, tries, 2)
	assert.NotEqual(t, tries[0].key, tries[1].key, "a fresh key")
	p = f.position(posID)
	assert.Equal(t, f.nowMS(), p.DeliveredAt, "the window starts at the later accepted")
}

// A /clear (session_changed) ends the slot for good; any other drop is tried again after 5 s.
func TestNotices_DroppedSessionChangedGivesUpOtherDropsRetry(t *testing.T) {
	f := newPosFix(t)
	n := f.outbox(func(c int, _ noticeCall) (promptq.Result, error) {
		if c == 1 {
			return promptq.Result{Status: promptq.Dropped, Reason: "blocked by a hook"}, nil
		}
		return promptq.Result{Status: promptq.Dropped, Reason: "session_changed"}, nil
	})
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.notices()
	assert.Equal(t, "dropped:blocked by a hook", f.position(posID).NoticeResult)
	f.advance(noticeRetryOther)
	f.notices()
	assert.Equal(t, noticeSessionChanged, f.position(posID).NoticeResult)
	f.advance(time.Minute)
	f.notices()
	assert.Len(t, n.tries(), 2, "nothing more is tried for it")
}

// The slot is checked again before each try: an offer whose position started a command meanwhile is cleared, never sent
// (a stale offer would have the session re-run what was passed on). (M: drop the current check.)
func TestNotices_StaleOfferIsDropped(t *testing.T) {
	f := newPosFix(t)
	n := f.outbox(nil)
	posID, rowID := f.queuedThenOffered(newCID(), pidA, "go build")
	// the session re-ran the command meanwhile and it started: the position is running
	again := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, resources.StateHeld, again.State, "the offered position's own command starts at once")
	require.Equal(t, resources.PosRunning, f.position(posID).State)
	_ = rowID
	f.notices()
	assert.Empty(t, n.tries())
	assert.Equal(t, noticeStale, f.position(posID).NoticeResult)
}

// The cancelled notice is sent only for a position that had something pending (Q7); one that had nothing leaves no slot.
func TestNotices_CancelOnlyWhenSomethingIsPending(t *testing.T) {
	f := newPosFix(t)
	n := f.outbox(nil)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.deliver(posID, f.now())
	f.advance(window)
	f.sweep() // the window ran out: no_answer, a named row was pending
	p := f.position(posID)
	require.Equal(t, resources.PosEnded, p.State)
	require.Equal(t, "cancelled", p.NoticeKind)
	f.notices()
	tries := n.tries()
	require.Len(t, tries, 1)
	assert.Equal(t, resources.CancelledNotice, tries[0].text)

	// an idle position nobody was waiting behind ends with no slot at all
	g := newPosFix(t)
	m := g.outbox(nil)
	idle := g.idlePosition()
	g.advance(window)
	g.sweep()
	assert.Equal(t, resources.PosEnded, g.position(idle).State)
	assert.Equal(t, "", g.position(idle).NoticeKind)
	g.notices()
	assert.Empty(t, m.tries())
}

// A restart finds the slots in the database: an undelivered offer and a cancelled slot armed 2 minutes before are both
// sent after it; one armed 11 minutes before is not. (M: replay offers only.)
func TestNotices_BootResendsPendingSlots(t *testing.T) {
	f := newPosFix(t)
	n := f.outbox(nil)
	posA, posB := f.offeredWithAWaiter() // A offered, B queued behind it
	// B's session: a cancelled slot armed 2 minutes ago (it ended), and a stale one 11 minutes old
	f.endWithSlot(posB, f.nowMS()-2*60*1000, "cancelled")
	f.m.notices = noticeState{} // the restart forgets what the outbox remembered
	f.notices()
	got := map[string]bool{}
	for _, c := range n.tries() {
		got[c.text] = true
	}
	assert.True(t, got[resources.CancelledNotice], "the cancelled slot armed 2 minutes before is sent")
	foundOffer := false
	for _, c := range n.tries() {
		foundOffer = foundOffer || strings.Contains(c.text, "輪到你了")
	}
	assert.True(t, foundOffer, "the undelivered offer is sent")
	_ = posA

	h := newPosFix(t)
	m := h.outbox(nil)
	_, posD := h.offeredWithAWaiter()
	h.endWithSlot(posD, h.nowMS()-11*60*1000, "cancelled")
	h.notices()
	for _, c := range m.tries() {
		assert.NotEqual(t, resources.CancelledNotice, c.text, "armed 11 minutes before: too old to be worth sending")
	}
	assert.Equal(t, noticeStale, h.position(posD).NoticeResult)
}

// No mod to take it: the slot stays pending and the mod channel is tried again every 5 s (the fallback of P2d-2 comes on
// top of this).
func TestNotices_NoModKeepsTrying(t *testing.T) {
	f := newPosFix(t)
	n := f.outbox(status(promptq.NoMod))
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.notices()
	assert.Equal(t, promptq.NoMod, f.position(posID).NoticeResult)
	f.advance(noticeRetryOther)
	f.notices()
	f.advance(noticeRetryOther)
	f.notices()
	assert.Len(t, n.tries(), 3)
	assert.Equal(t, int64(0), f.position(posID).DeliveredAt)
}

// The outbox never holds stateMu while it waits for the queue: a try that blocks does not block the module.
func TestNotices_ATryDoesNotHoldTheModuleLock(t *testing.T) {
	f := newPosFix(t)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	f.outbox(func(int, noticeCall) (promptq.Result, error) {
		entered <- struct{}{}
		<-release
		return promptq.Result{Status: promptq.Accepted}, nil
	})
	f.queuedThenOffered(newCID(), pidA, "go build")
	f.m.noticePass(context.Background())
	<-entered
	locked := make(chan struct{})
	go func() {
		f.m.stateMu.Lock()
		f.m.stateMu.Unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(2 * time.Second):
		t.Fatal("stateMu is held while a notice try waits for the queue")
	}
	close(release)
	f.m.wg.Wait()
}

// An error from the queue (not busy) is logged and tried again, never a panic or a settled slot.
func TestNotices_AnErrorRetries(t *testing.T) {
	f := newPosFix(t)
	n := f.outbox(func(c int, _ noticeCall) (promptq.Result, error) {
		if c == 1 {
			return promptq.Result{}, errors.New("queue down")
		}
		return promptq.Result{Status: promptq.Accepted}, nil
	})
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.notices()
	f.advance(noticeRetryOther)
	f.notices()
	assert.Len(t, n.tries(), 2)
	assert.Equal(t, promptq.Accepted, f.position(posID).NoticeResult)
}

// An offer slot is current only while its position is still offered, for the same offer, and not yet delivered: each
// condition alone makes the text empty (a stale offer would have the session re-run what was passed on).
func TestNotices_OfferTextNeedsACurrentOffer(t *testing.T) {
	f := newPosFix(t)
	f.outbox(nil)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	p := f.position(posID)
	p.NoticeKind, p.NoticeDueAt = "offer", p.OfferedAt
	require.NotEmpty(t, f.m.noticeText(p, f.nowMS()), "the current offer says something")

	delivered := p
	delivered.DeliveredAt = p.OfferedAt + 1
	assert.Empty(t, f.m.noticeText(delivered, f.nowMS()), "already delivered")

	newer := p
	newer.NoticeDueAt = p.OfferedAt - 1
	assert.Empty(t, f.m.noticeText(newer, f.nowMS()), "a slot of an older offer")

	moved := p
	moved.State = resources.PosRunning
	assert.Empty(t, f.m.noticeText(moved, f.nowMS()), "no longer offered")
}

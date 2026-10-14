package resourcesmod

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

func (f *posFix) setNoticeResult(posID, result string) {
	f.t.Helper()
	_, err := f.m.store.db.Exec(`UPDATE resource_positions SET notice_result = ? WHERE id = ?`, result, posID)
	require.NoError(f.t, err)
}

// An offer whose notice could only be told to the person (user_alert) lets go at
// 60 s, not 3 minutes (P2f-2): 59.999 s holds, 60 s passes.
// (M: apply 60 s to every undelivered offer; leave 3 minutes for user_alert.)
func TestSweep_UserAlertOfferPassesAt60s(t *testing.T) {
	f := newPosFix(t)
	posA, posB := f.offeredWithAWaiter()
	f.setNoticeResult(posA, noticeUserAlert)
	f.advance(resources.NoticeFallbackPass - time.Millisecond)
	f.sweep()
	assert.Equal(t, resources.PosOffered, f.position(posA).State, "59.999 s")
	f.advance(time.Millisecond)
	f.sweep()
	a := f.position(posA)
	assert.Equal(t, resources.PosQueued, a.State, "60 s: the offer passes, the position keeps its place")
	assert.Equal(t, 1, a.OfferPasses)
	f.pass()
	assert.Equal(t, resources.PosOffered, f.position(posB).State)
}

// The 60 s count from the moment the fallback told the person, not from the offer: a fallback that happens 100 s
// after the offer must not be passed on the next sweep, before the alert has had its minute (codex R2).
// (M: count from offered_at.)
func TestSweep_UserAlertMinuteCountsFromTheFallback(t *testing.T) {
	f := newPosFix(t)
	posA, _ := f.offeredWithAWaiter()
	f.advance(100 * time.Second)
	f.setNoticeResult(posA, noticeUserAlert)
	_, err := f.m.store.db.Exec(`UPDATE resource_positions SET notice_sent_at = ? WHERE id = ?`, f.nowMS(), posA)
	require.NoError(t, err)
	f.sweep()
	assert.Equal(t, resources.PosOffered, f.position(posA).State, "just alerted: holds")
	f.advance(resources.NoticeFallbackPass - time.Millisecond)
	f.sweep()
	assert.Equal(t, resources.PosOffered, f.position(posA).State, "59.999 s after the alert")
	f.advance(time.Millisecond)
	f.sweep()
	assert.Equal(t, resources.PosQueued, f.position(posA).State, "60 s after the alert")
}

// Any other result (or none) still waits the 3 minutes.
func TestSweep_UndeliveredOfferStillWaits3Min(t *testing.T) {
	f := newPosFix(t)
	posA, _ := f.offeredWithAWaiter()
	f.setNoticeResult(posA, "unknown")
	f.advance(resources.NoticeFallbackPass)
	f.sweep()
	assert.Equal(t, resources.PosOffered, f.position(posA).State, "60 s is not enough without user_alert")
	f.advance(window - resources.NoticeFallbackPass - time.Millisecond)
	f.sweep()
	assert.Equal(t, resources.PosOffered, f.position(posA).State, "2:59.999")
	f.advance(time.Millisecond)
	f.sweep()
	assert.Equal(t, resources.PosQueued, f.position(posA).State)
}

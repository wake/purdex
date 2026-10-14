package resourcesmod

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/resources"
)

// fakeLead is the team module's seam as the outbox sees it.
type fakeLead struct {
	mu      sync.Mutex
	member  bool
	reached func(n int) bool // whether try n got to the lead
	during  func()           // runs while the lead is being asked (the slot changes under the try)
	calls   []string
}

func (f *fakeLead) NoticeLeadOf(sessionID, text string) (bool, bool) {
	if f.during != nil {
		f.during()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sessionID+"|"+text)
	return f.member, f.member && (f.reached == nil || f.reached(len(f.calls)))
}

func (f *fakeLead) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// fallbackFix is a position fixture whose queue has no mod, with the lead seam and an alert catcher.
func fallbackFix(t *testing.T, lead *fakeLead) (*posFix, *fakeNotices, *[]core.HostEvent) {
	f := newPosFix(t)
	n := f.outbox(status(promptq.NoMod))
	var alerts []core.HostEvent
	var mu sync.Mutex
	f.m.alertOut = func(ev core.HostEvent) { mu.Lock(); alerts = append(alerts, ev); mu.Unlock() }
	if lead != nil {
		f.m.leadNotice = lead
	}
	return f, n, &alerts
}

// A member's offer that no mod takes goes to its lead, which counts as delivered: the window starts (D1) and the mod
// channel is not tried again.
func TestFallback_MemberGetsItFromItsLead(t *testing.T) {
	lead := &fakeLead{member: true}
	f, n, alerts := fallbackFix(t, lead)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.notices()
	p := f.position(posID)
	assert.Equal(t, noticeMember, p.NoticeResult)
	assert.Equal(t, f.nowMS(), p.DeliveredAt, "a lead that was told is a delivery")
	require.Len(t, lead.sent(), 1)
	assert.Contains(t, lead.sent()[0], "輪到你了：`go build`")
	assert.Contains(t, lead.sent()[0], "資源租約")
	f.advance(noticeRetryOther)
	f.notices()
	assert.Len(t, n.tries(), 1, "settled: the mod channel is not asked again")
	assert.Empty(t, *alerts, "a member is never a user alert")
}

// The lead cannot be reached right now: the slot stays pending, the mod channel and the lead are tried again, and the
// one that works settles it.
func TestFallback_LeadLookupMissingIsRetried(t *testing.T) {
	lead := &fakeLead{member: true, reached: func(n int) bool { return n >= 2 }}
	f, n, alerts := fallbackFix(t, lead)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.notices()
	p := f.position(posID)
	assert.Equal(t, promptq.NoMod, p.NoticeResult)
	assert.Zero(t, p.DeliveredAt)
	f.advance(noticeRetryOther)
	f.notices()
	p = f.position(posID)
	assert.Equal(t, noticeMember, p.NoticeResult)
	assert.Equal(t, f.nowMS(), p.DeliveredAt)
	assert.Len(t, n.tries(), 2)
	assert.Len(t, lead.sent(), 2)
	assert.Empty(t, *alerts)
}

// The lead was sent the notice and the delivery could not be recorded: the next try finishes the record and does not send a
// second copy to the lead.
func TestFallback_ASentLeadIsNotSentAgainWhenTheRecordFailed(t *testing.T) {
	lead := &fakeLead{member: true}
	f, _, _ := fallbackFix(t, lead)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	_, err := f.m.store.db.Exec(`CREATE TRIGGER no_delivery BEFORE UPDATE OF delivered_at ON resource_positions
		BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	require.NoError(t, err)
	f.notices()
	assert.Len(t, lead.sent(), 1)
	assert.Zero(t, f.position(posID).DeliveredAt)
	_, err = f.m.store.db.Exec(`DROP TRIGGER no_delivery`)
	require.NoError(t, err)
	f.advance(noticeRetryOther)
	f.notices()
	assert.Len(t, lead.sent(), 1, "not sent again")
	p := f.position(posID)
	assert.Equal(t, noticeMember, p.NoticeResult)
	assert.Equal(t, f.nowMS(), p.DeliveredAt)
}

// A slot that was replaced before the claim sends nothing to the lead.
func TestFallback_AReplacedSlotIsNotSentToTheLead(t *testing.T) {
	lead := &fakeLead{member: true}
	f, _, alerts := fallbackFix(t, lead)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	old := f.position(posID)
	_, err := f.m.store.db.Exec(`UPDATE resource_positions SET notice_due_at = notice_due_at + 1 WHERE id = ?`, posID)
	require.NoError(t, err)
	d := f.m.noticeFallback(old, "text", f.nowMS(), func(string, int64) bool { return true })
	assert.Zero(t, d)
	assert.Empty(t, lead.sent())
	assert.Empty(t, *alerts)
}

// A session that is no team member has no lead to tell: a user alert, once; never a send to itself.
func TestFallback_SoloSessionIsAUserAlertNeverASelfSend(t *testing.T) {
	lead := &fakeLead{member: false}
	f, _, alerts := fallbackFix(t, lead)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.notices()
	f.advance(noticeRetryOther)
	f.notices()
	f.advance(noticeRetryOther)
	f.notices()
	assert.Len(t, *alerts, 1, "once per slot")
	assert.Len(t, lead.sent(), 1, "asked once; no member, no further tries")
	p := f.position(posID)
	assert.Equal(t, noticeUserAlert, p.NoticeResult)
	assert.Zero(t, p.DeliveredAt, "a user alert is not a delivery")
}

// The alert names the session and carries the text; the position is marked from its own column.
func TestFallback_EventCarriesTheTextAndMarksThePosition(t *testing.T) {
	f, _, alerts := fallbackFix(t, &fakeLead{})
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.notices()
	require.Len(t, *alerts, 1)
	ev := (*alerts)[0]
	assert.Equal(t, resources.AlertEventType, ev.Type)
	var a resources.Alert
	require.NoError(t, json.Unmarshal([]byte(ev.Value), &a))
	assert.Equal(t, resources.AlertRuleLeaseNotice, a.Rule)
	assert.Equal(t, posID, a.Lease.ID)
	assert.Equal(t, f.position(posID).SessionID, a.Lease.SessionID)
	assert.Contains(t, a.Text, "輪到你了：`go build`")
	assert.Zero(t, a.ClosedAt)
	assert.Equal(t, noticeUserAlert, f.position(posID).NoticeResult)
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(ev.Value), &raw))
	host, _ := raw["host"].(map[string]any)
	for _, k := range []string{"zone", "mem", "pressure"} {
		assert.Contains(t, host, k, "the App contract's host fields")
	}
}

// A slot that was cleared while the lead was being asked is not alerted about: the event goes with the write that holds.
func TestFallback_ASlotClearedUnderTheTryIsNoAlert(t *testing.T) {
	lead := &fakeLead{}
	f, _, alerts := fallbackFix(t, lead)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	lead.during = func() {
		// a newer slot replaced it: the same position, armed again
		_, err := f.m.store.db.Exec(`UPDATE resource_positions SET notice_due_at = notice_due_at + 1 WHERE id = ?`, posID)
		require.NoError(t, err)
	}
	f.notices()
	assert.Empty(t, *alerts)
	assert.NotEqual(t, noticeUserAlert, f.position(posID).NoticeResult, "the replacement is not marked by the old try")
}

// With no team module there is no fallback: the slot keeps trying the mod channel, as before P2d-2.
func TestFallback_NoTeamModuleKeepsTrying(t *testing.T) {
	f, n, alerts := fallbackFix(t, nil)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.notices()
	f.advance(noticeRetryOther)
	f.notices()
	assert.Len(t, n.tries(), 2)
	assert.Equal(t, promptq.NoMod, f.position(posID).NoticeResult)
	assert.Empty(t, *alerts)
}

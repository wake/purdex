package resourcesmod

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// release DELETEs a lease by id, with notice=1 when asked, and returns the answer.
func (f *posFix) release(id string, notice bool) resources.LeaseResponse {
	f.t.Helper()
	path := "/api/resources/leases/" + id
	if notice {
		path += "?notice=1"
	}
	rec := f.do(http.MethodDelete, path, nil)
	require.Equal(f.t, http.StatusOK, rec.Code, rec.Body.String())
	return decodeLease(f.t, rec)
}

// run starts one command of session A in the position (the host is calm unless
// the zone was changed) and returns its row id.
func (f *posFix) run(pid int, command string) string {
	f.t.Helper()
	got := decodeLease(f.t, f.postAs(newCID(), pid, resources.CallQueue, command))
	require.Equal(f.t, resources.StateHeld, got.State, command)
	return got.ID
}

// waiterB queues session B behind the host (danger) and returns its position id.
func (f *posFix) waiterB() string {
	f.t.Helper()
	f.setZone("danger", "flat")
	got := decodeLease(f.t, f.postAs(newCID(), pidB, resources.CallQueue, "go test"))
	require.Equal(f.t, resources.StateQueued, got.State)
	return got.PositionID
}

// Three short commands with a waiter: the second is announced as the last
// before the third, the third's release releases the position, and the waiter
// is offered in the same pass. (M: do not apply rule 3.)
func TestLimits_ThreeShortCommandsWithAWaiter(t *testing.T) {
	f := newPosFix(t)
	c1 := f.run(pidA, "c1")
	posA := f.row(c1).PositionID
	r1 := f.release(c1, true)
	assert.Empty(t, r1.Notice, "nobody waits yet")
	assert.Equal(t, resources.PosIdle, f.position(posA).State)

	posB := f.waiterB()
	c2 := f.run(pidA, "c2") // continued, in any zone
	r2 := f.release(c2, true)
	assert.Equal(t, resources.FinalNotice(resources.NoticeThreeCommands), r2.Notice)
	pa := f.position(posA)
	assert.Equal(t, resources.PosIdle, pa.State)
	assert.True(t, pa.Warned)
	assert.Equal(t, 1, pa.Extensions)
	assert.Equal(t, resources.FinalNotice(resources.NoticeThreeCommands), f.row(c2).Notice, "stored on the row")

	c3 := f.run(pidA, "c3")
	f.setZone("normal", "flat") // the host is calm when the position is released
	r3 := f.release(c3, true)
	assert.Empty(t, r3.Notice)
	pa = f.position(posA)
	assert.Equal(t, resources.PosEnded, pa.State)
	assert.Equal(t, resources.EndReleasedAfterLimit, pa.EndReason)
	assert.Equal(t, resources.PosOffered, f.position(posB).State, "the waiter is offered in the same pass")
}

// Nobody waiting: five commands over two hours, no limit and no notice.
// (M: apply the limits without a waiter.)
func TestLimits_NobodyWaitingNoLimit(t *testing.T) {
	f := newPosFix(t)
	var posA string
	for i := 0; i < 5; i++ {
		id := f.run(pidA, "cmd-"+string(rune('a'+i)))
		posA = f.row(id).PositionID
		f.advance(25 * time.Minute)
		got := f.release(id, true)
		assert.Empty(t, got.Notice)
		assert.Equal(t, resources.PosIdle, f.position(posA).State)
		assert.False(t, f.position(posA).Warned)
	}
}

// A command that runs past 30 minutes while someone waits: its release carries
// the overtime notice.
func TestLimits_OvertimeNoticeOnTheReleaseAnswer(t *testing.T) {
	f := newPosFix(t)
	c1 := f.run(pidA, "c1")
	f.waiterB()
	f.advance(31 * time.Minute)
	got := f.release(c1, true)
	assert.Equal(t, resources.FinalNotice(resources.NoticeOvertime), got.Notice)
	assert.True(t, f.position(f.row(c1).PositionID).Warned)
}

// The waiter arrives while the third command runs: its release warns, the
// fourth command's release releases (V21). (M: release at k >= 3 without
// looking at warned.)
func TestLimits_WaiterDuringTheThirdGetsOneExtension(t *testing.T) {
	f := newPosFix(t)
	c1 := f.run(pidA, "c1")
	posA := f.row(c1).PositionID
	f.release(c1, true)
	c2 := f.run(pidA, "c2")
	f.release(c2, true)
	c3 := f.run(pidA, "c3")
	f.waiterB()
	r3 := f.release(c3, true)
	assert.Equal(t, resources.FinalNotice(resources.NoticeThreeCommands), r3.Notice, "an extension, not a release")
	assert.Equal(t, resources.PosIdle, f.position(posA).State)
	c4 := f.run(pidA, "c4")
	f.setZone("normal", "flat")
	f.release(c4, true)
	assert.Equal(t, resources.PosEnded, f.position(posA).State)
	assert.Equal(t, 4, f.position(posA).CommandsEnded)
}

// The warning is only about a waiter: if it leaves, the notice is void.
func TestLimits_NoticeVoidWhenTheWaiterLeaves(t *testing.T) {
	f := newPosFix(t)
	c1 := f.run(pidA, "c1")
	posA := f.row(c1).PositionID
	f.release(c1, true)
	posB := f.waiterB()
	c2 := f.run(pidA, "c2")
	f.release(c2, true)
	require.True(t, f.position(posA).Warned)
	// B gives up.
	rows, err := f.m.store.RowsOf(posB)
	require.NoError(t, err)
	f.release(rows[0].ID, false)
	c3 := f.run(pidA, "c3")
	got := f.release(c3, true)
	assert.Empty(t, got.Notice)
	assert.Equal(t, resources.PosIdle, f.position(posA).State, "nobody waits: no limit")
	assert.False(t, f.position(posA).Warned, "an earlier notice is void")
}

// A limit that would warn needs a release that shows the notice (the mod's throw
// path sends none): the end is an ordinary one, and the next release warns.
// (M: apply rule 3 to every client release.)
func TestLimits_RuleThreeNeedsANoticeRelease(t *testing.T) {
	f := newPosFix(t)
	c1 := f.run(pidA, "c1")
	posA := f.row(c1).PositionID
	f.waiterB()
	f.release(c1, true)
	c2 := f.run(pidA, "c2")
	got := f.release(c2, false) // k=2 would warn, but nobody can read it
	assert.Empty(t, got.Notice)
	assert.False(t, f.position(posA).Warned)
	assert.Equal(t, resources.PosIdle, f.position(posA).State)
	c3 := f.run(pidA, "c3")
	got = f.release(c3, true)
	assert.Equal(t, resources.FinalNotice(resources.NoticeThreeCommands), got.Notice)
	assert.True(t, f.position(posA).Warned)
}

// The sweeper's ends never warn: holder_gone / expired with a waiter at k=2 is an
// ordinary end, and the next release that shows a notice warns.
// (M: let the sweeper warn.)
func TestLimits_SweeperEndNeverWarns(t *testing.T) {
	f := newPosFix(t)
	c1 := f.run(pidA, "c1")
	posA := f.row(c1).PositionID
	f.waiterB()
	f.release(c1, true)
	c2 := f.run(pidA, "c2")
	f.advance(f.m.settings().MaxHold() + time.Second) // c2 ran past max_hold: the sweeper ends it as expired
	f.m.sweepOnce(context.Background())
	r := f.row(c2)
	require.Equal(t, resources.StateEnded, r.State)
	assert.Equal(t, resources.EndExpired, r.EndReason)
	assert.Empty(t, r.Notice)
	pa := f.position(posA)
	assert.Equal(t, resources.PosIdle, pa.State)
	assert.False(t, pa.Warned)
	assert.Equal(t, 2, pa.CommandsEnded)
	c3 := f.run(pidA, "c3")
	assert.Equal(t, resources.FinalNotice(resources.NoticeOvertime), f.release(c3, true).Notice, "max_hold passed, so it is also over 30 minutes: overtime wins")
}

// Two commands running at once: the first to end only counts; the last decides.
func TestLimits_LastOfTwoConcurrentCommandsDecides(t *testing.T) {
	f := newPosFix(t)
	c1 := f.run(pidA, "c1")
	posA := f.row(c1).PositionID
	c2 := f.run(pidA, "c2") // a second command of the running position
	require.Equal(t, 2, f.position(posA).CommandsStarted)
	f.release(c1, true)
	pa := f.position(posA)
	assert.Equal(t, resources.PosRunning, pa.State, "c2 still runs")
	assert.Equal(t, 1, pa.CommandsEnded)
	f.release(c2, true)
	pa = f.position(posA)
	assert.Equal(t, resources.PosIdle, pa.State)
	assert.Equal(t, 2, pa.CommandsEnded)
}

// A wait-mode row that is still polled counts as waiting (Q5); one whose lease
// ran out does not.
func TestLimits_WaitModeRowsCountAsWaiting(t *testing.T) {
	f := newPosFix(t)
	c1 := f.run(pidA, "c1")
	posA := f.row(c1).PositionID
	f.setZone("danger", "flat")
	w := decodeLease(t, f.postAs(newCID(), 2000, resources.CallWait, "w"))
	f.advance(31 * time.Minute) // past 30 min; the wait row's lease (30 s) has run out
	got := f.release(c1, true)
	assert.Empty(t, got.Notice, "a wait row nobody polls is not waiting")
	c2 := f.run(pidA, "c2")
	require.NoError(t, f.m.store.RenewLease(w.ID, f.nowMS()+30_000)) // polled again
	f.advance(31 * time.Minute)
	require.NoError(t, f.m.store.RenewLease(w.ID, f.nowMS()+30_000))
	got = f.release(c2, true)
	assert.Equal(t, resources.FinalNotice(resources.NoticeOvertime), got.Notice)
	assert.True(t, f.position(posA).Warned)
}

// At release, a waiting ask row of the position moves to a new queued position
// of the same session behind the waiter; queue rows end released_after_limit.
// (M: end the ask rows instead.)
func TestLimits_ReleaseRequeuesAskRowsAtTheBack(t *testing.T) {
	f := newPosFix(t)
	c1 := f.run(pidA, "c1")
	posA := f.row(c1).PositionID
	_, err := f.m.store.db.Exec(`UPDATE resource_positions SET warned = 1 WHERE id = ?`, posA)
	require.NoError(t, err)
	posB := f.waiterB()
	// While c1 runs the session asks for a background command (cut by the table
	// in danger) and a foreground one.
	ask := decodeLease(t, f.postAs(newCID(), pidA, resources.CallAsk, "bg"))
	require.Equal(t, resources.StateWaiting, ask.State)
	fg := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "fg"))
	require.Equal(t, resources.StateQueued, fg.State)

	f.advance(time.Second)
	f.release(c1, true) // warned: this is the announced last command
	old := f.position(posA)
	assert.Equal(t, resources.PosEnded, old.State)
	assert.Equal(t, resources.EndReleasedAfterLimit, old.EndReason)
	assert.Equal(t, resources.EndReleasedAfterLimit, f.row(fg.ID).EndReason)
	a := f.row(ask.ID)
	assert.Equal(t, resources.StateWaiting, a.State, "the ask row waits on")
	assert.NotEqual(t, posA, a.PositionID)
	np := f.position(a.PositionID)
	assert.Equal(t, resources.PosQueued, np.State)
	assert.Equal(t, posA, np.RequeuedFrom)
	assert.Equal(t, "sid-agent", np.SessionID)
	// Behind the waiter: B's position is first.
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+ask.ID+"?wait=0", nil))
	assert.Equal(t, resources.StateWaiting, got.State)
	assert.Equal(t, 2, got.Position)
	assert.Equal(t, ask.ID, got.ID)
	_ = posB
}

// The re-queued position is offered after the waiter, and its ask row is named.
func TestLimits_RequeuedAskIsAskedAtItsTurn(t *testing.T) {
	f := newPosFix(t)
	c1 := f.run(pidA, "c1")
	posA := f.row(c1).PositionID
	_, err := f.m.store.db.Exec(`UPDATE resource_positions SET warned = 1 WHERE id = ?`, posA)
	require.NoError(t, err)
	posB := f.waiterB()
	f.advance(time.Second)
	ask := decodeLease(t, f.postAs(newCID(), pidA, resources.CallAsk, "bg"))
	f.setZone("normal", "flat")
	f.release(c1, true)
	assert.Equal(t, resources.PosOffered, f.position(posB).State, "the waiter first")
	newPos := f.row(ask.ID).PositionID
	assert.Equal(t, resources.PosQueued, f.position(newPos).State)
	// B's session re-runs, then its command ends; the re-queued position is next.
	rerun := decodeLease(t, f.postAs(newCID(), pidB, resources.CallQueue, "go test"))
	require.Equal(t, resources.StateHeld, rerun.State)
	f.release(rerun.ID, true)
	f.advance(time.Second)
	_, err = f.m.store.db.Exec(`UPDATE resource_positions SET state = 'ended', end_reason = 'done', ended_at = 1 WHERE id = ?`, posB)
	require.NoError(t, err)
	f.pass()
	np := f.position(newPos)
	assert.Equal(t, resources.PosOffered, np.State)
	assert.NotZero(t, f.row(ask.ID).OfferedAt, "named")
}

// A held row with no position (as a plain lease, or left by the deploy) ends
// exactly as before: by DELETE by id and by client id, and by the sweeper.
// (M: make endCommand fail when no position is found.)
func TestCommandEnd_LegacyHeldRowEndsAsToday(t *testing.T) {
	f := newPosFix(t)
	for i, byClient := range []bool{false, true} {
		cid := newCID()
		got := decodeLease(t, f.postAs(cid, 2000, resources.CallWait, "legacy"))
		require.Equal(t, resources.StateHeld, got.State, "wait mode, no position")
		f.advance(time.Duration(i+1) * time.Minute)
		path := "/api/resources/leases/" + got.ID
		if byClient {
			path = "/api/resources/leases?client_id=" + cid
		}
		rec := f.do(http.MethodDelete, path, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		ended := decodeLease(t, rec)
		assert.Equal(t, resources.StateEnded, ended.State)
		assert.Equal(t, resources.EndReleased, ended.EndReason)
		assert.Empty(t, ended.Notice)
	}
	// The sweeper: expired.
	got := decodeLease(t, f.postAs(newCID(), 2000, resources.CallWait, "legacy"))
	f.advance(f.m.settings().MaxHold() + time.Second)
	f.m.sweepOnce(context.Background())
	r := f.row(got.ID)
	assert.Equal(t, resources.EndExpired, r.EndReason)
	// vanished goes through the same end with its guard.
	f.setZone("normal", "flat")
	v := decodeLease(t, f.postAs(newCID(), 2000, resources.CallWait, "legacy"))
	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET scope = 'session-new', empty_samples = 100 WHERE id = ?`, v.ID)
	require.NoError(t, err)
	f.m.stateMu.Lock()
	won, _, err := f.m.endCommand(f.row(v.ID), resources.EndVanished, f.m.now(), false, ` AND scope = 'session-new' AND empty_samples >= ?`, emptySamplesToVanish)
	f.m.stateMu.Unlock()
	require.NoError(t, err)
	assert.True(t, won)
	assert.Equal(t, resources.EndVanished, f.row(v.ID).EndReason)
}

// A held row whose position is ended or gone ends as a plain lease does.
func TestCommandEnd_RowOfAMissingOrEndedPositionEndsAsToday(t *testing.T) {
	f := newPosFix(t)
	a := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "c1"))
	require.Equal(t, resources.StateHeld, a.State)
	_, err := f.m.store.db.Exec(`UPDATE resource_positions SET state = 'ended', end_reason = 'done', ended_at = 1 WHERE id = ?`, a.PositionID)
	require.NoError(t, err)
	got := f.release(a.ID, true)
	assert.Equal(t, resources.StateEnded, got.State)
	assert.Equal(t, resources.EndReleased, got.EndReason)

	b := decodeLease(t, f.postAs(newCID(), pidB, resources.CallQueue, "c1"))
	require.Equal(t, resources.StateHeld, b.State)
	_, err = f.m.store.db.Exec(`DELETE FROM resource_positions WHERE id = ?`, b.PositionID)
	require.NoError(t, err)
	got = f.release(b.ID, true)
	assert.Equal(t, resources.StateEnded, got.State)
}

// DELETE by client id and by id each carry the notice.
func TestRelease_AnswerCarriesTheNotice(t *testing.T) {
	f := newPosFix(t)
	c1 := f.run(pidA, "c1")
	posA := f.row(c1).PositionID
	f.waiterB()
	f.advance(31 * time.Minute)
	_, err := f.m.store.db.Exec(`UPDATE resource_positions SET warned = 0 WHERE id = ?`, posA)
	require.NoError(t, err)
	got := f.release(c1, true)
	assert.Equal(t, resources.FinalNotice(resources.NoticeOvertime), got.Notice)

	cid := newCID()
	c2 := decodeLease(t, f.postAs(cid, pidA, resources.CallQueue, "c2"))
	require.Equal(t, resources.StateHeld, c2.State)
	_, err = f.m.store.db.Exec(`UPDATE resource_positions SET warned = 0, first_started_at = ? WHERE id = ?`, f.nowMS()-31*60_000, posA)
	require.NoError(t, err)
	rec := f.do(http.MethodDelete, "/api/resources/leases?client_id="+cid+"&notice=1", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, resources.FinalNotice(resources.NoticeOvertime), decodeLease(t, rec).Notice)
}

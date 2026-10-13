package resourcesmod

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// posFix is the attribution fixture with a second agent: pdx 1000 / 900 / 800 is
// session sid-agent (A), pdx 1010 / 910 / 810 is session sid-b (B). The gap is off.
type posFix struct{ *attrFix }

func newPosFix(t *testing.T) *posFix {
	f := &posFix{newAttrFix(t)}
	f.m.roots = &fakeRoots{roots: []resources.Root{
		{SessionID: "sid-agent", PID: 800, ProcStart: attrStartText},
		{SessionID: "sid-b", PID: 810, ProcStart: attrStartText},
	}}
	for pid, parent := range map[int]int{1010: 910, 910: 810, 810: 1} {
		f.table.ppid[pid] = parent
		f.table.start[pid] = attrStart
	}
	f.set.set(resources.Settings{Mode: resources.ModeLease, GapS: intp(0)})
	f.setZone("normal", "flat")
	// The holders are alive for the sweeper.
	for _, pid := range []int{1000, 1010, 2000, 800, 810} {
		f.view.procs[pid] = attrStart
	}
	return f
}

const (
	pidA = 1000
	pidB = 1010
)

// postAs POSTs a heavy request of the session behind pid.
func (f *posFix) postAs(cid string, pid int, mode, command string) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, "/api/resources/leases", resources.LeaseRequest{
		ClientID: cid, Kind: "build", HolderPID: pid, HolderStart: attrStartText, Mode: mode, Command: command, Cwd: "/work"})
}

func newCID() string { return uuid.NewString() }

func (f *posFix) position(id string) positionRow {
	f.t.Helper()
	p, ok, err := f.m.store.PositionByID(id)
	require.NoError(f.t, err)
	require.True(f.t, ok, id)
	return p
}

func (f *posFix) livePositions() []positionRow {
	f.t.Helper()
	ps, err := f.m.store.LivePositions()
	require.NoError(f.t, err)
	return ps
}

func (f *posFix) decPath(id string) string { return f.row(id).DecPath }

// ---- T7: the create path ----

// The host is free: the command runs at once, the position is running, and the
// offer was made in this request's own pass, so its path is immediate (V13).
// (M: record `offer` for a start made in the same pass.)
func TestPosition_FreeHostRunsAtOnce(t *testing.T) {
	f := newPosFix(t)
	rec := f.postAs(newCID(), pidA, resources.CallQueue, "go build ./...")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	got := decodeLease(t, rec)
	assert.Equal(t, resources.StateHeld, got.State)
	assert.NotEmpty(t, got.PositionID)
	p := f.position(got.PositionID)
	assert.Equal(t, resources.PosRunning, p.State)
	assert.Equal(t, p.OfferedAt, p.FirstStartedAt)
	assert.Equal(t, 1, p.CommandsStarted)
	assert.Equal(t, resources.PathImmediate, f.decPath(got.ID))
	assert.Equal(t, "sid-agent", p.SessionID)
	assert.Equal(t, 800, p.AgentPID, "the agent's identity is recorded")
	assert.Equal(t, attrStartText, p.AgentStart)
}

// The host is busy: the answer is queued, at once, with its place. Nothing polls
// and the call never waits. (M: route queue mode to the wait path.)
func TestPosition_BusyHostQueuesAtOnce(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	began := time.Now()
	rec := f.postAs(newCID(), pidA, resources.CallQueue, "go build ./...")
	assert.Less(t, time.Since(began), 10*time.Second, "answered at once: a wait would last minutes")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	got := decodeLease(t, rec)
	assert.Equal(t, resources.StateQueued, got.State)
	assert.Equal(t, 1, got.Position)
	assert.Equal(t, resources.QueuedBehindQueue, got.QueuedReason)
	assert.NotEmpty(t, got.PositionID)
	r := f.row(got.ID)
	assert.Equal(t, resources.StateWaiting, r.State, "the row waits; the client does not")
	assert.Zero(t, r.LeaseUntil, "a queue row is nobody's to poll")
	assert.Equal(t, resources.PosQueued, f.position(got.PositionID).State)
}

// Two commands of one session share a position; the same command and cwd again
// is the first row (adopted, same place).
// (M: in CreateAttached attach the row to the id of the attempted insert.)
func TestPosition_OnePerSessionTwoCommands(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	b := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go test"))
	require.NotEmpty(t, a.PositionID)
	assert.Equal(t, a.PositionID, b.PositionID)
	assert.NotEqual(t, a.ID, b.ID)
	assert.Len(t, f.livePositions(), 1)
	_, ok, err := f.m.store.PositionByID(a.PositionID)
	require.NoError(t, err)
	assert.True(t, ok)

	again := f.postAs(newCID(), pidA, resources.CallQueue, "go build")
	assert.Equal(t, http.StatusOK, again.Code)
	got := decodeLease(t, again)
	assert.Equal(t, a.ID, got.ID, "the first row is adopted")
	rows, err := f.m.store.RowsOf(a.PositionID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}

// Wait mode, and clients that name no mode, make no position; their place in the
// queue is among the positions'.
func TestPosition_WaitModeAndOldClientsMakeNoPosition(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, 1, a.Position)
	for i, mode := range []string{"", resources.CallWait} {
		f.advance(time.Second)
		got := decodeLease(t, f.postAs(newCID(), pidB, mode, "x"))
		assert.Equal(t, resources.StateWaiting, got.State, "mode %q", mode)
		assert.Empty(t, got.PositionID)
		assert.Equal(t, 2+i, got.Position, "behind the position that came first")
		assert.Equal(t, "", f.row(got.ID).PositionID)
	}
	assert.Len(t, f.livePositions(), 1)
}

// A caller that is not under an agent has no session, so queue mode is wait mode.
func TestPosition_QueueWithoutASessionIsWait(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	got := decodeLease(t, f.postAs(newCID(), 2000, resources.CallQueue, "x"))
	assert.Equal(t, resources.StateWaiting, got.State)
	assert.Empty(t, got.PositionID)
	assert.Empty(t, f.livePositions())
}

// Advise is granted at once as before; positions exist in lease mode only.
func TestPosition_AdviseMakesNoPositions(t *testing.T) {
	f := newPosFix(t)
	f.set.set(resources.Settings{Mode: resources.ModeAdvise, GapS: intp(0)})
	got := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	assert.Equal(t, resources.StateHeld, got.State)
	assert.Empty(t, got.PositionID)
	assert.Empty(t, f.livePositions())
	r := f.row(got.ID)
	assert.Equal(t, resources.CallQueue, r.Mode, "advise still records what the queue would show")
	assert.Equal(t, "go build", r.Command)
}

func TestPosition_ConcurrentCreatesOneLivePosition(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := f.postAs(newCID(), pidA, resources.CallQueue, "cmd-"+string(rune('a'+i)))
			assert.Less(t, rec.Code, 300, rec.Body.String())
		}(i)
	}
	wg.Wait()
	ps := f.livePositions()
	require.Len(t, ps, 1)
	rows, err := f.m.store.RowsOf(ps[0].ID)
	require.NoError(t, err)
	assert.Len(t, rows, 8)
}

// ---- T8: the pass with positions ----

// session A queued behind danger; the zone then turns normal: the next pass
// offers the position. Returns its id and the command id.
func (f *posFix) queuedThenOffered(cid string, pid int, command string) (posID, rowID string) {
	f.t.Helper()
	f.setZone("danger", "flat")
	got := decodeLease(f.t, f.postAs(cid, pid, resources.CallQueue, command))
	require.Equal(f.t, resources.StateQueued, got.State)
	f.setZone("normal", "flat")
	f.pass()
	require.Equal(f.t, resources.PosOffered, f.position(got.PositionID).State)
	return got.PositionID, got.ID
}

// An offer is a grant decision: the gap starts at the offer, and the request
// behind waits for it. (M: leave lastOfferMS out of lastGrant.)
func TestPosition_OfferIsAGrantDecisionWithTheGap(t *testing.T) {
	f := newPosFix(t)
	f.set.set(resources.Settings{Mode: resources.ModeLease, GapS: intp(30)})
	f.setZone("danger", "flat")
	decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.advance(time.Second)
	w := decodeLease(t, f.postAs(newCID(), 2000, resources.CallWait, "other")) // a wait-mode row behind it
	f.setZone("normal", "flat")
	f.pass()
	offered := f.livePositions()[0]
	require.Equal(t, resources.PosOffered, offered.State)
	assert.Equal(t, f.nowMS(), offered.OfferedAt)
	f.advance(10 * time.Second)
	require.NoError(t, f.m.store.RenewLease(w.ID, f.nowMS()+30_000)) // its client is polling
	f.pass()
	assert.Equal(t, resources.StateWaiting, f.state(w.ID))
	r, _, _ := f.m.reasons.current(w.ID)
	assert.Equal(t, resources.ReasonGap, r)
	f.advance(21 * time.Second)
	require.NoError(t, f.m.store.RenewLease(w.ID, f.nowMS()+30_000))
	f.pass()
	assert.Equal(t, resources.StateHeld, f.state(w.ID), "granted once 30 s have passed since the offer")
}

// Using the offer does not start another gap: the next head follows 30 s after the
// offer, not 30 s after the first command. (M: let the offer path set lastGrantMS.)
func TestPosition_FirstCommandDoesNotRestartTheGap(t *testing.T) {
	f := newPosFix(t)
	f.set.set(resources.Settings{Mode: resources.ModeLease, GapS: intp(30)})
	f.setZone("danger", "flat")
	decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.advance(time.Second)
	b := decodeLease(t, f.postAs(newCID(), pidB, resources.CallQueue, "go test"))
	f.setZone("normal", "flat")
	f.pass() // A is offered at t0
	t0 := f.nowMS()
	f.advance(20 * time.Second)
	rerun := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	assert.Equal(t, resources.StateHeld, rerun.State)
	assert.Equal(t, resources.PathOffer, f.decPath(rerun.ID))
	f.advance(9 * time.Second) // t0 + 29 s
	f.pass()
	assert.Equal(t, resources.PosQueued, f.position(b.PositionID).State, "the gap since the offer is not over")
	f.advance(2 * time.Second) // t0 + 31 s, but only 11 s after the first command
	f.pass()
	assert.Equal(t, resources.PosOffered, f.position(b.PositionID).State, "30 s after the offer, not after the first command")
	assert.Equal(t, t0+31_000, f.position(b.PositionID).OfferedAt)
}

// The gap is counted from the newest offer at boot, not from its first command.
func TestBoot_GapCountsFromTheLastOfferNotItsFirstCommand(t *testing.T) {
	clock := newFakeClock()
	var offeredAt int64
	m := bootModule(t, idleSampler(), clock, func(s *leaseStore, now int64) {
		offeredAt = now - 10_000
		require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000001", SessionID: "s1", State: resources.PosRunning, CreatedAt: now - 20_000, OfferedAt: offeredAt}))
		h := baseRow("h", "c-h")
		h.PositionID, h.Mode = "pos_00000001", resources.CallQueue
		mustCreate(t, s, h)
		_, err := s.db.Exec(`UPDATE resource_leases SET state = 'held', granted_at = ?, dec_path = 'offer' WHERE id = 'h'`, now-5_000)
		require.NoError(t, err)
	})
	require.NoError(t, m.Start(context.Background()))
	assert.Equal(t, offeredAt, m.lastOfferMS)
	assert.Less(t, m.lastGrantMS, offeredAt, "the offer-path grant is not a grant")
}

// The reservation is the point of the offer: the first command is not judged by
// the table again. (M: send it through AdmitV2.)
func TestPosition_OfferedFirstCommandSkipsTheTable(t *testing.T) {
	f := newPosFix(t)
	posID, rowID := f.queuedThenOffered(newCID(), pidA, "go build")
	f.setZone("danger", "rising")
	got := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	assert.Equal(t, rowID, got.ID)
	assert.Equal(t, resources.StateHeld, got.State)
	assert.Equal(t, resources.PathOffer, f.decPath(got.ID))
	assert.Equal(t, resources.PosRunning, f.position(posID).State)
}

// The window after a command: the next command of the session runs at once
// whatever the zone says.
func TestPosition_IdleNextCommandRunsAtOnce(t *testing.T) {
	f := newPosFix(t)
	first := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, resources.StateHeld, first.State)
	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET state = 'ended', ended_at = 1, end_reason = 'released' WHERE id = ?`, first.ID)
	require.NoError(t, err)
	_, err = f.m.store.db.Exec(`UPDATE resource_positions SET state = 'idle', window_until = ? WHERE id = ?`, f.nowMS()+180_000, first.PositionID)
	require.NoError(t, err)
	f.setZone("danger", "rising")
	got := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go test"))
	assert.Equal(t, resources.StateHeld, got.State)
	assert.Equal(t, resources.PathContinued, f.decPath(got.ID))
	assert.Equal(t, resources.PosRunning, f.position(first.PositionID).State)
}

// A background command of the session posted while its position is offered or
// idle runs at once as well; no question is armed. (M: grant only queue-mode
// fresh rows in the reservation step.)
func TestPosition_AskPostedWhileOfferedOrIdleRunsAtOnce(t *testing.T) {
	f := newPosFix(t)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.setZone("danger", "rising")
	got := decodeLease(t, f.postAs(newCID(), pidA, resources.CallAsk, "go test"))
	assert.Equal(t, resources.StateHeld, got.State)
	assert.Equal(t, resources.PathOffer, f.decPath(got.ID))
	assert.Zero(t, f.row(got.ID).OfferedAt)

	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET state = 'ended', ended_at = 1, end_reason = 'released' WHERE state = 'held'`)
	require.NoError(t, err)
	_, err = f.m.store.db.Exec(`UPDATE resource_positions SET state = 'idle', window_until = ? WHERE id = ?`, f.nowMS()+180_000, posID)
	require.NoError(t, err)
	got = decodeLease(t, f.postAs(newCID(), pidA, resources.CallAsk, "go lint"))
	assert.Equal(t, resources.StateHeld, got.State)
	assert.Equal(t, resources.PathContinued, f.decPath(got.ID))
}

// A request cut by a running disk trim waits at an offered position, and is
// named at once so that its session is told (R7). (M: skip the naming step.)
func TestPosition_UngrantedRowAtAnOfferedPositionIsNamed(t *testing.T) {
	f := newPosFix(t)
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	f.m.disk.mu.Lock()
	f.m.disk.trimming = true
	f.m.disk.mu.Unlock()
	got := decodeLease(t, f.postAs(newCID(), pidA, resources.CallAsk, "go test"))
	assert.Equal(t, resources.StateWaiting, got.State, "cut by the trim")
	r := f.row(got.ID)
	assert.NotZero(t, r.OfferedAt, "named")
	assert.Equal(t, "offer", f.position(posID).NoticeKind)
}

// An offered position holds the host for its session: another head sees a running
// lease. (M: running = len(held) > 0.)
func TestPosition_ReservedCountsAsRunning(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.advance(time.Second)
	b := decodeLease(t, f.postAs(newCID(), pidB, resources.CallQueue, "go test"))
	f.setZone("normal", "flat")
	f.pass() // A offered
	f.setZone("normal", "rising")
	f.pass()
	assert.Equal(t, resources.PosQueued, f.position(b.PositionID).State)
	r, _, _ := f.m.reasons.current(posKey(b.PositionID))
	assert.Equal(t, resources.ReasonRising, r, "the offered position counts as running")
	f.setZone("normal", "flat")
	f.pass()
	assert.Equal(t, resources.PosOffered, f.position(b.PositionID).State, "both reserved in normal and flat")
}

// A second command of a running position: the table decides, whether or not
// someone is queued; refused, it waits for the session's own command.
// (M: require an empty queue, the revision-1 rule.)
func TestPosition_SecondCommandWhileRunning(t *testing.T) {
	f := newPosFix(t)
	a1 := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, resources.StateHeld, a1.State)
	f.setZone("danger", "flat")
	decodeLease(t, f.postAs(newCID(), pidB, resources.CallQueue, "go test")) // someone queued
	f.setZone("normal", "flat")
	a2 := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go vet"))
	assert.Equal(t, resources.StateHeld, a2.State, "the table grants, with someone queued")
	assert.Equal(t, resources.PathImmediate, f.decPath(a2.ID))
	assert.Equal(t, 2, f.position(a1.PositionID).CommandsStarted)

	f.setZone("danger", "flat")
	a3 := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go lint"))
	assert.Equal(t, resources.StateQueued, a3.State)
	assert.Equal(t, resources.QueuedOwnRunning, a3.QueuedReason)
	assert.Zero(t, a3.Position, "it waits for its own session's command, not in the queue")
	a4 := decodeLease(t, f.postAs(newCID(), pidA, resources.CallAsk, "go fmt"))
	assert.Equal(t, resources.StateWaiting, a4.State)
	assert.NotEmpty(t, a4.PositionID)
}

// With someone queued, the position's own limit applies to a second command;
// with nobody queued there is none (V18). (M: drop the guard.)
func TestPosition_SecondCommandCountsTowardTheLimit(t *testing.T) {
	f := newPosFix(t)
	a1 := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.setZone("danger", "flat")
	decodeLease(t, f.postAs(newCID(), pidB, resources.CallQueue, "go test"))
	f.setZone("normal", "flat")

	set := func(sqlSet string) {
		_, err := f.m.store.db.Exec(`UPDATE resource_positions SET ` + sqlSet + ` WHERE id = '` + a1.PositionID + `'`)
		require.NoError(t, err)
	}
	// An earlier pass would have offered B (the table grants): put it back in the queue.
	requeueB := func() {
		_, err := f.m.store.db.Exec(`UPDATE resource_positions SET state = 'queued', offered_at = 0, notice_kind = '' WHERE session_id = 'sid-b'`)
		require.NoError(t, err)
		_, err = f.m.store.db.Exec(`UPDATE resource_leases SET offered_at = 0 WHERE session_id = 'sid-b' AND state = 'waiting'`)
		require.NoError(t, err)
	}
	set("commands_started = 3")
	got := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "c1"))
	assert.Equal(t, resources.QueuedOwnRunning, got.QueuedReason, "three commands started and someone waits")
	requeueB()
	set("commands_started = 1, warned = 1")
	got = decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "c2"))
	assert.Equal(t, resources.QueuedOwnRunning, got.QueuedReason, "warned: the announced last command")

	// Nobody queued: no limit.
	_, err := f.m.store.db.Exec(`UPDATE resource_positions SET state = 'ended', end_reason = 'abandoned', ended_at = 1 WHERE session_id = 'sid-b'`)
	require.NoError(t, err)
	_, err = f.m.store.db.Exec(`UPDATE resource_leases SET state = 'ended', ended_at = 1, end_reason = 'abandoned' WHERE session_id = 'sid-b'`)
	require.NoError(t, err)
	set("commands_started = 5, warned = 0")
	got = decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "c3"))
	assert.Equal(t, resources.StateHeld, got.State, "nobody queued: unlimited")
}

// Strict FIFO across wait-mode rows and positions, by created_at.
func TestPosition_StrictFIFOAcrossPositionsAndRows(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	w1 := decodeLease(t, f.postAs(newCID(), 2000, resources.CallWait, "w1"))
	f.advance(time.Second)
	a := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.advance(time.Second)
	w2 := decodeLease(t, f.postAs(newCID(), 2000, resources.CallWait, "w2"))
	f.setZone("normal", "flat")
	f.pass()
	assert.Equal(t, resources.StateHeld, f.state(w1.ID))
	assert.Equal(t, resources.PosQueued, f.position(a.PositionID).State)
	f.pass()
	assert.Equal(t, resources.PosOffered, f.position(a.PositionID).State)
	assert.Equal(t, resources.StateWaiting, f.state(w2.ID))
	f.pass()
	assert.Equal(t, resources.StateHeld, f.state(w2.ID))
}

// Nobody polls a queue-mode row, so lease_until never abandons it.
// (M: drop the exclusion in sweepLeases.)
func TestPosition_QueueRowsAreNeverAbandonedByLease(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	got := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.advance(2 * time.Minute)
	f.m.sweepOnce(context.Background())
	f.m.sweepOnce(context.Background())
	assert.Equal(t, resources.StateWaiting, f.state(got.ID))
	assert.Equal(t, resources.PosQueued, f.position(got.PositionID).State)
}

// An ask row is polled by its background process; not polled, it is abandoned and
// the queued position with no rows left ends with it (P14).
func TestPosition_AskRowIsPolledAndAbandoned(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	got := decodeLease(t, f.postAs(newCID(), pidA, resources.CallAsk, "go build"))
	require.Equal(t, resources.StateWaiting, got.State)
	f.advance(31 * time.Second)
	f.m.sweepOnce(context.Background())
	r := f.row(got.ID)
	assert.Equal(t, resources.StateEnded, r.State)
	assert.Equal(t, resources.EndAbandoned, r.EndReason)
	p := f.position(got.PositionID)
	assert.Equal(t, resources.PosEnded, p.State)
	assert.Equal(t, resources.EndAbandoned, p.EndReason)
}

// The wait reasons of a queued position are kept under its key and written on the
// position at the offer.
func TestPosition_ReasonsKeyedByPosition(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	got := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.advance(5 * time.Second)
	f.pass()
	r, _, _ := f.m.reasons.current(posKey(got.PositionID))
	assert.Equal(t, resources.ReasonDanger, r)
	f.setZone("normal", "flat")
	f.pass()
	p := f.position(got.PositionID)
	require.Equal(t, resources.PosOffered, p.State)
	assert.Contains(t, p.WaitReasons, "danger")
	assert.Equal(t, "normal", p.OfferZone)
}

// ---- T9: the answers ----

// The place counts positions and wait-mode rows together, by created_at; a row
// of a position that is not queued has no place.
func TestLeaseResponse_PlaceCountsPositionsAndRows(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	w := decodeLease(t, f.postAs(newCID(), 2000, resources.CallWait, "w"))
	f.advance(time.Second)
	a := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "a1"))
	f.advance(time.Second)
	b := decodeLease(t, f.postAs(newCID(), pidB, resources.CallAsk, "b1"))
	assert.Equal(t, 1, w.Position)
	assert.Equal(t, 2, a.Position)
	assert.Equal(t, 3, b.Position)
	assert.Equal(t, resources.StateWaiting, b.State, "an ask row is held by its own long poll, not queued")
	a2 := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "a2"))
	assert.Equal(t, 2, a2.Position, "the second command shares the position's place")
}

// One pass has one table admission: a second command that took it leaves the
// queue head for the next pass, even with no gap (v2: at most one grant per pass).
// (M: ignore that the second command used the admission.)
func TestPosition_SecondCommandAndTheQueueHeadAreNotBothAdmittedInOnePass(t *testing.T) {
	f := newPosFix(t)
	a1 := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, resources.StateHeld, a1.State)
	f.setZone("danger", "flat")
	b := decodeLease(t, f.postAs(newCID(), pidB, resources.CallQueue, "go test"))
	f.setZone("normal", "flat")
	a2 := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go vet"))
	assert.Equal(t, resources.StateHeld, a2.State)
	assert.Equal(t, resources.PosQueued, f.position(b.PositionID).State, "the head waits for the next pass")
	f.pass()
	assert.Equal(t, resources.PosOffered, f.position(b.PositionID).State)
}

// On the same millisecond a position comes before a wait-mode row, in the place
// the answer reports and in the order the pass admits them.
func TestPosition_SameMillisecondPositionsComeFirst(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	w := decodeLease(t, f.postAs(newCID(), 2000, resources.CallWait, "w")) // posted first, same millisecond
	a := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "a"))
	assert.Equal(t, 1, a.Position)
	assert.Equal(t, 2, f.queuePlaceOf(w.ID))
	f.setZone("normal", "flat")
	f.pass()
	assert.Equal(t, resources.PosOffered, f.position(a.PositionID).State, "the pass admits the position first, as the place says")
	assert.Equal(t, resources.StateWaiting, f.state(w.ID))
}

func (f *posFix) queuePlaceOf(id string) int { return f.m.queuePlace(f.row(id)) }

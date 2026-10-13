package resourcesmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// call POSTs a route as the session behind pid.
func (f *posFix) call(path string, pid int) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, path, resources.CallerRequest{HolderPID: pid})
}

func decodePosition(t *testing.T, rec *httptest.ResponseRecorder) resources.PositionResponse {
	t.Helper()
	var r resources.PositionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r), rec.Body.String())
	return r
}

// An ask row waits at a queued position behind danger; the zone clears and the
// position is offered. Returns the ask row and its position.
func (f *posFix) askOffered() (rowID, posID string) {
	f.t.Helper()
	f.setZone("danger", "flat")
	a := decodeLease(f.t, f.postAs(newCID(), pidA, resources.CallAsk, "go build"))
	require.Equal(f.t, resources.StateWaiting, a.State)
	f.setZone("normal", "flat")
	f.pass()
	require.Equal(f.t, resources.PosOffered, f.position(a.PositionID).State)
	return a.ID, a.PositionID
}

// The session says yes: the command starts at once, and the background process's
// own poll gets held with the position id.
func TestConfirm_GrantsTheAskedCommand(t *testing.T) {
	f := newPosFix(t)
	rowID, posID := f.askOffered()
	polled := make(chan struct{}, 1)
	f.m.pollHook = func() {
		select {
		case polled <- struct{}{}:
		default:
		}
	}
	got := make(chan resources.LeaseResponse, 1)
	go func() {
		got <- decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+rowID+"?wait=20", nil))
	}()
	<-polled // the long poll is waiting
	rec := f.call("/api/resources/leases/"+rowID+"/confirm", pidA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, resources.StateHeld, decodeLease(t, rec).State)
	select {
	case resp := <-got:
		assert.Equal(t, resources.StateHeld, resp.State)
		assert.Equal(t, posID, resp.PositionID, "the ask client runs only a held answer that carries a position id (R8)")
	case <-time.After(10 * time.Second):
		t.Fatal("the long poll did not return")
	}
	assert.Equal(t, resources.PosRunning, f.position(posID).State)
	assert.NotZero(t, f.row(rowID).ConfirmedAt)
}

func TestConfirm_QueuedIs409(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.postAs(newCID(), pidA, resources.CallAsk, "go build"))
	rec := f.call("/api/resources/leases/"+a.ID+"/confirm", pidA)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, resources.ErrNotYourTurn, apiCode(rec))
	assert.Zero(t, f.row(a.ID).ConfirmedAt)
	assert.Equal(t, resources.StateWaiting, f.state(a.ID))
}

func TestConfirm_OnlyAnAskRow(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	q := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	rec := f.call("/api/resources/leases/"+q.ID+"/confirm", pidA)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// Cancelling a request ends it cancelled_by_session; the ask client's poll sees it.
func TestCancel_RequestEndsCancelledBySession(t *testing.T) {
	f := newPosFix(t)
	rowID, _ := f.askOffered()
	rec := f.call("/api/resources/leases/"+rowID+"/cancel", pidA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+rowID+"?wait=0", nil))
	assert.Equal(t, resources.StateEnded, got.State)
	assert.Equal(t, resources.EndCancelledBySession, got.EndReason)
}

// The last request of a queued position takes the position with it; another
// request keeps it.
func TestCancel_LastRequestEndsTheQueuedPosition(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "c1"))
	b := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "c2"))
	f.call("/api/resources/leases/"+a.ID+"/cancel", pidA)
	assert.Equal(t, resources.PosQueued, f.position(a.PositionID).State, "another request keeps the position")
	f.call("/api/resources/leases/"+b.ID+"/cancel", pidA)
	p := f.position(a.PositionID)
	assert.Equal(t, resources.PosEnded, p.State)
	assert.Equal(t, resources.EndCancelledBySession, p.EndReason)
}

func TestCancel_RunningIs409(t *testing.T) {
	f := newPosFix(t)
	run := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, resources.StateHeld, run.State)
	rec := f.call("/api/resources/leases/"+run.ID+"/cancel", pidA)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, resources.ErrRunning, apiCode(rec))
	assert.Equal(t, resources.StateHeld, f.state(run.ID))
	rec = f.call("/api/resources/positions/"+run.PositionID+"/cancel", pidA)
	assert.Equal(t, http.StatusConflict, rec.Code, "a running position is not cancelled (Q10)")
	assert.Equal(t, resources.PosRunning, f.position(run.PositionID).State)
}

// Cancelling the head's position lets the next in.
func TestCancel_PositionLetsTheNextIn(t *testing.T) {
	f := newPosFix(t)
	posA, posB := f.offeredWithAWaiter()
	rec := f.call("/api/resources/positions/"+posA+"/cancel", pidA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	ans := decodePosition(t, rec)
	assert.Equal(t, resources.PosEnded, ans.State)
	assert.Equal(t, resources.EndCancelledBySession, ans.EndReason)
	f.setZone("normal", "flat")
	f.pass()
	assert.Equal(t, resources.PosOffered, f.position(posB).State)
}

func TestDone_IdleEndsAtOnce(t *testing.T) {
	f := newPosFix(t)
	posID := f.idlePosition()
	rec := f.call("/api/resources/positions/done", pidA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, resources.PosEnded, decodePosition(t, rec).State)
	p := f.position(posID)
	assert.Equal(t, resources.EndDone, p.EndReason)
}

func TestDone_RunningEndsAfterTheCommandWithoutAWindow(t *testing.T) {
	f := newPosFix(t)
	run := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	rec := f.call("/api/resources/positions/done", pidA)
	require.Equal(t, http.StatusOK, rec.Code)
	ans := decodePosition(t, rec)
	assert.Equal(t, resources.PosRunning, ans.State)
	assert.True(t, ans.DonePending)
	f.release(run.ID, true)
	p := f.position(run.PositionID)
	assert.Equal(t, resources.PosEnded, p.State, "no window after the command")
	assert.Equal(t, resources.EndDone, p.EndReason)
}

func TestDone_NothingToReleaseIsNone(t *testing.T) {
	f := newPosFix(t)
	rec := f.call("/api/resources/positions/done", pidA)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, resources.StateNone, decodePosition(t, rec).State)
}

// ---- the own-session guard on every route (#17) ----

type routeCase struct {
	name string
	// target makes a fixture and returns the path of the route aimed at A's target.
	target func(f *posFix) string
	// unchanged says the target is as the fixture left it.
	unchanged func(f *posFix) bool
}

func guardCases() []routeCase {
	return []routeCase{
		{"confirm", func(f *posFix) string {
			id, _ := f.askOffered()
			f.guardRow = id
			return "/api/resources/leases/" + id + "/confirm"
		}, func(f *posFix) bool {
			return f.row(f.guardRow).ConfirmedAt == 0 && f.state(f.guardRow) == resources.StateWaiting
		}},
		{"cancel request", func(f *posFix) string {
			f.setZone("danger", "flat")
			q := decodeLease(f.t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
			f.guardRow = q.ID
			return "/api/resources/leases/" + q.ID + "/cancel"
		}, func(f *posFix) bool { return f.state(f.guardRow) == resources.StateWaiting }},
		{"cancel position", func(f *posFix) string {
			f.setZone("danger", "flat")
			q := decodeLease(f.t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
			f.guardPos = q.PositionID
			return "/api/resources/positions/" + q.PositionID + "/cancel"
		}, func(f *posFix) bool { return f.position(f.guardPos).State == resources.PosQueued }},
		{"done", func(f *posFix) string {
			f.guardPos = f.idlePosition()
			return "/api/resources/positions/done"
		}, func(f *posFix) bool { return f.position(f.guardPos).State == resources.PosIdle }},
	}
}

// Another session's request or position is a 403 and stays as it was; for done,
// which acts on the caller's own position, another session changes nothing of A's.
// (M: drop the check from each route in turn.)
func TestOwnSession_AnotherSessionIs403(t *testing.T) {
	for _, c := range guardCases() {
		t.Run(c.name, func(t *testing.T) {
			f := newPosFix(t)
			path := c.target(f)
			rec := f.call(path, pidB)
			if c.name == "done" {
				assert.Equal(t, http.StatusOK, rec.Code, "B has no position of its own: nothing to release")
			} else {
				assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
				assert.Equal(t, resources.ErrNotYourSession, apiCode(rec))
			}
			assert.True(t, c.unchanged(f), "A's target is untouched")
		})
	}
}

// A caller that is not under an agent has no session: 403, nothing changes.
func TestOwnSession_NoSessionIs403(t *testing.T) {
	for _, c := range guardCases() {
		t.Run(c.name, func(t *testing.T) {
			f := newPosFix(t)
			path := c.target(f)
			rec := f.call(path, 2000)
			assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			assert.Equal(t, resources.ErrNoSession, apiCode(rec))
			assert.True(t, c.unchanged(f))
		})
	}
}

func TestRoutes_BodyIsChecked(t *testing.T) {
	f := newPosFix(t)
	rec := f.do(http.MethodPost, "/api/resources/positions/done", map[string]any{"holder_pid": 0})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = f.do(http.MethodPost, "/api/resources/positions/done", map[string]any{"holder_pid": 1000, "extra": 1})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "unknown fields are refused")
	rec = f.call("/api/resources/leases/nope/cancel", pidA)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = f.call("/api/resources/positions/pos_nope/cancel", pidA)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, resources.ErrNoPosition, apiCode(rec))
}

// A reservation grant ends the pass like any grant: the pollers are woken and no
// second round is needed (the row that started the position is not a second
// command). (M: treat the started row as a second command.)
func TestPass_ReservationGrantWakesAndNeedsNoSecondRound(t *testing.T) {
	f := newPosFix(t)
	rowID, posID := f.askOffered()
	passes := 0
	f.m.passHook = func() { passes++ }
	woken := f.m.genChan()
	require.NoError(t, f.m.store.ConfirmRow(rowID, f.nowMS()))
	f.m.admissionPass(context.Background(), rowID)
	assert.Equal(t, resources.StateHeld, f.state(rowID))
	assert.Equal(t, resources.PosRunning, f.position(posID).State)
	assert.Equal(t, 1, f.position(posID).CommandsStarted)
	assert.Equal(t, 1, passes, "one round: nothing was lost")
	select {
	case <-woken:
	default:
		t.Fatal("the pollers were not woken by the grant")
	}
}

// A pass starts the request between the moment the route looked at it and the
// moment it holds the lock: the answer is the running 409, not an error and not a
// silent 200. (M: decide on the row read before the lock.)
func TestCancel_RequestStartedMeanwhileIs409(t *testing.T) {
	f := newPosFix(t)
	rowID, _ := f.askOffered()
	f.m.routeHook = func() {
		require.NoError(t, f.m.store.ConfirmRow(rowID, f.nowMS()))
		f.m.admissionPass(context.Background(), rowID) // the confirmed command starts
	}
	rec := f.call("/api/resources/leases/"+rowID+"/cancel", pidA)
	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, resources.ErrRunning, apiCode(rec))
	assert.Equal(t, resources.StateHeld, f.state(rowID))
}

// Same for a position: it started between the read and the lock, so cancel is the
// running 409, and done leaves it to end with its command.
func TestCancel_PositionStartedMeanwhile(t *testing.T) {
	for _, route := range []string{"cancel", "done"} {
		t.Run(route, func(t *testing.T) {
			f := newPosFix(t)
			rowID, posID := f.askOffered()
			f.m.routeHook = func() {
				f.m.routeHook = nil
				require.NoError(t, f.m.store.ConfirmRow(rowID, f.nowMS()))
				f.m.admissionPass(context.Background(), rowID)
			}
			path := "/api/resources/positions/" + posID + "/cancel"
			if route == "done" {
				path = "/api/resources/positions/done"
			}
			rec := f.call(path, pidA)
			if route == "cancel" {
				assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			} else {
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				assert.True(t, decodePosition(t, rec).DonePending, "the running position ends with its command")
				assert.True(t, f.position(posID).DoneRequested)
			}
			assert.Equal(t, resources.PosRunning, f.position(posID).State)
		})
	}
}

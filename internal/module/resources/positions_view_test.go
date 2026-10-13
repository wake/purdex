package resourcesmod

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

func (f *posFix) snapshot() resources.Snapshot {
	f.t.Helper()
	rec := f.do(http.MethodGet, "/api/resources", nil)
	require.Equal(f.t, http.StatusOK, rec.Code)
	var s resources.Snapshot
	require.NoError(f.t, json.Unmarshal(rec.Body.Bytes(), &s))
	return s
}

// The snapshot lists the positions with what pdx lease ls shows: who, the state,
// the place, the window left, the counters and each request with its command and
// directory. Waiters keep only the wait-mode requests. (M: leave positions out.)
func TestSnapshot_ListsPositionsWithTheirCommands(t *testing.T) {
	f := newPosFix(t)
	run := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build ./..."))
	require.Equal(t, resources.StateHeld, run.State)
	f.setZone("danger", "flat")
	f.advance(time.Second)
	q := decodeLease(t, f.postAs(newCID(), pidB, resources.CallQueue, "go test ./..."))
	f.advance(time.Second)
	ask := decodeLease(t, f.postAs(newCID(), pidA, resources.CallAsk, "make lint"))
	f.advance(time.Second)
	w := decodeLease(t, f.postAs(newCID(), 2000, resources.CallWait, "script"))

	s := f.snapshot()
	require.Len(t, s.Positions, 2)
	a, b := s.Positions[0], s.Positions[1]
	assert.Equal(t, run.PositionID, a.ID)
	assert.Equal(t, "sid-agent", a.SessionID)
	assert.Equal(t, "_"+refDigits(a.Ref), a.Ref)
	assert.Equal(t, resources.PosRunning, a.State)
	assert.Equal(t, 1, a.Commands)
	assert.NotZero(t, a.FirstStartedAt)
	assert.Zero(t, a.Place, "a running position is not in the queue")
	require.Len(t, a.Requests, 2)
	assert.Equal(t, "go build ./...", a.Requests[0].Command)
	assert.Equal(t, "/work", a.Requests[0].Cwd)
	assert.Equal(t, "held", a.Requests[0].State)
	assert.Equal(t, resources.CallQueue, a.Requests[0].Mode)
	assert.Equal(t, "make lint", a.Requests[1].Command)
	assert.Equal(t, resources.CallAsk, a.Requests[1].Mode)
	assert.Equal(t, "waiting", a.Requests[1].State, "an ask row not asked yet")
	assert.Equal(t, ask.ID, a.Requests[1].ID)

	assert.Equal(t, q.PositionID, b.ID)
	assert.Equal(t, resources.PosQueued, b.State)
	assert.Equal(t, 1, b.Place)
	require.Len(t, b.Requests, 1)
	assert.Equal(t, "queued", b.Requests[0].State)

	require.Len(t, s.Waiters, 1, "only the wait-mode request")
	assert.Equal(t, w.ID, s.Waiters[0].ID)
	assert.Equal(t, 2, s.Waiters[0].Position, "behind the queued position")

	raw := f.do(http.MethodGet, "/api/resources", nil).Body.String()
	assert.NotContains(t, raw, `"env"`)

	// Once it is asked (named in a notice), the ask row shows as asked.
	f.name(ask.ID)
	s = f.snapshot()
	assert.Equal(t, "asked", s.Positions[0].Requests[1].State)
}

func refDigits(ref string) string {
	if len(ref) > 0 && ref[0] == '_' {
		return ref[1:]
	}
	return ref
}

// A position between two commands holds the host with nothing held and nothing
// waiting: the tick still sends the state. (M: count only held and waiting.)
func TestEvent_SentWhileOnlyAnIdlePositionLives(t *testing.T) {
	f := newEventFix(t, 20*time.Millisecond)
	f.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return goodRaw(), nil, nil
	}}
	for i := 0; i < 3; i++ {
		f.m.tick(context.Background())
	}
	require.Empty(t, f.got(150*time.Millisecond), "idle: silent")
	require.NoError(t, f.m.store.insertPosition(positionRow{ID: "pos_00000001", SessionID: "s1", State: resources.PosIdle, CreatedAt: f.nowMS(), AgentPID: 0}))
	f.m.tick(context.Background())
	evs := f.got(300 * time.Millisecond)
	require.NotEmpty(t, evs, "an idle position is activity")
	var snap resources.Snapshot
	require.NoError(t, json.Unmarshal([]byte(evs[len(evs)-1].Value), &snap))
	require.Len(t, snap.Positions, 1)
	assert.Equal(t, resources.PosIdle, snap.Positions[0].State)
}

// ---- the report ----

func TestBuildReport_PositionFigures(t *testing.T) {
	rows := []resources.ReportPosition{
		{EndReason: resources.EndDone, CommandsStarted: 1, CreatedAt: 1000, OfferedAt: 2000, DeliveredAt: 2500, OfferWaitMS: 500, IdleWaitMS: 1000, NoticeResult: "accepted"},
		{EndReason: resources.EndReleasedAfterLimit, CommandsStarted: 3, Extensions: 1, CreatedAt: 1000, OfferedAt: 5000, DeliveredAt: 7000, OfferWaitMS: 1500, NoticeResult: "accepted"},
		{EndReason: resources.EndNoAnswer, CommandsStarted: 0, CreatedAt: 1000, OfferedAt: 3000, DeliveredAt: 3100, OfferPasses: 2, NoticeResult: "unknown"},
		{EndReason: resources.EndAbandoned, CommandsStarted: 5, RequeuedFrom: "pos_x", CreatedAt: 1000},
		{EndReason: resources.EndDone, CommandsStarted: 2, CreatedAt: 1000, OfferedAt: 1500},
	}
	got := resources.BuildPositionReport(rows)
	assert.Equal(t, 5, got.Total)
	assert.Equal(t, []resources.ReportReasonCount{{Reason: "abandoned", Count: 1}, {Reason: "done", Count: 2}, {Reason: "no_answer", Count: 1}, {Reason: "released_after_limit", Count: 1}}, got.ByEndReason)
	assert.Equal(t, resources.ReportCommandCounts{None: 1, One: 1, Two: 1, Three: 1, FourPlus: 1}, got.Commands)
	assert.Equal(t, 1, got.Requeued)
	assert.Equal(t, 1, got.Extensions)
	assert.Equal(t, resources.ReportDist{N: 2, Total: 2000, P50: 500, P90: 1500, Max: 1500}, got.OfferWaitMS)
	assert.Equal(t, resources.ReportDist{N: 1, Total: 1000, P50: 1000, P90: 1000, Max: 1000}, got.IdleWaitMS)
	// offered_at - created_at: 1000, 4000, 2000, 500
	assert.Equal(t, resources.ReportWait{P50: 1000, P90: 4000, Max: 4000}, got.QueuedWaitMS)
	assert.Equal(t, 4, got.Offers.Offered)
	assert.Equal(t, 3, got.Offers.Delivered)
	assert.Equal(t, 2, got.Offers.Passed)
	// delivered - offered: 500, 2000, 100
	assert.Equal(t, resources.ReportDist{N: 3, Total: 2600, P50: 500, P90: 2000, Max: 2000}, got.Offers.DeliveryDelayMS)
	assert.Equal(t, 1, got.Offers.LapsedAfterDelivery)
	assert.Equal(t, []resources.ReportReasonCount{{Reason: "accepted", Count: 2}, {Reason: "unknown", Count: 1}}, got.Offers.NoticeResults)

	empty := resources.BuildPositionReport(nil)
	assert.Zero(t, empty.Total)
	assert.NotNil(t, empty.ByEndReason, "lists are empty, not null")
	b, err := json.Marshal(empty)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "null")
}

func TestBuildReport_OfferAndContinuedPaths(t *testing.T) {
	leases := []resources.ReportLease{
		{Kind: "build", GrantedAt: 5, Recorded: true, Path: resources.PathOffer},
		{Kind: "build", GrantedAt: 6, Recorded: true, Path: resources.PathContinued},
		{Kind: "build", GrantedAt: 7, Recorded: true, Path: resources.PathContinued},
		{Kind: "build", GrantedAt: 8, Recorded: true, Path: resources.PathImmediate},
	}
	r := resources.BuildReport(0, 100, leases, nil)
	assert.Equal(t, 1, r.Paths.Offer)
	assert.Equal(t, 2, r.Paths.Continued)
	assert.Equal(t, 1, r.Paths.Immediate)
}

// The report endpoint carries the position figures from the stored rows.
func TestReport_EndpointHasThePositions(t *testing.T) {
	f := newPosFix(t)
	run := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.release(run.ID, true)
	rec := f.do(http.MethodGet, "/api/resources/report?since=1h", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var rep resources.Report
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rep))
	assert.Equal(t, 1, rep.Positions.Total)
	assert.Equal(t, 1, rep.Positions.Commands.One)
	assert.Equal(t, 1, rep.Paths.Immediate)
}

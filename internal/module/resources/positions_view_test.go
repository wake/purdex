package resourcesmod

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
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

// countingRoots is the root source with the batch address lookup of the peers
// module's resolver, counting the registry reads.
type countingRoots struct {
	*fakeRoots
	reads atomic.Int64
}

func (c *countingRoots) ResolveOriginsBySession(ids []string) (map[string]team.Origin, error) {
	c.reads.Add(1)
	out := map[string]team.Origin{}
	for _, id := range ids {
		out[id] = team.Origin{SessionID: id, Address: "mlab/" + id}
	}
	return out, nil
}

// Twenty positions cost one registry read per snapshot at most, and none while the
// addresses are fresh. (M: resolve each position on its own.)
func TestSnapshot_AddressesAreResolvedOnceForManyPositions(t *testing.T) {
	f := newPosFix(t)
	cr := &countingRoots{fakeRoots: f.m.roots.(*fakeRoots)}
	f.m.roots = cr
	f.setZone("danger", "flat")
	for i := 0; i < 20; i++ {
		sid := "sid-" + string(rune('a'+i))
		_, _, _, err := f.m.store.CreateAttached(positionRow{ID: newPositionID(), SessionID: sid, CreatedAt: f.nowMS() + int64(i)},
			leaseRow{ID: "q" + sid, ClientID: "c" + sid, Weight: 35, SessionID: sid, HolderPID: 7, Scope: resources.ScopeProcess,
				CreatedAt: f.nowMS() + int64(i), Mode: resources.CallQueue, Command: "go test", Cwd: "/w"})
		require.NoError(t, err)
	}
	s := f.snapshot()
	require.Len(t, s.Positions, 20)
	assert.Equal(t, "mlab/sid-a", s.Positions[0].Address)
	assert.Equal(t, int64(1), cr.reads.Load(), "one registry read for twenty positions")
	f.snapshot()
	assert.Equal(t, int64(1), cr.reads.Load(), "fresh addresses are not read again")
	f.advance(addressTTL + time.Second)
	f.snapshot()
	assert.Equal(t, int64(2), cr.reads.Load(), "after the TTL, once more")
}

// The held rows, the queue and the positions of a snapshot are one view: a
// transition that lands between the reads is in all of it or none. (M: read the
// positions outside the transaction.)
func TestSnapshot_LeasesAndPositionsAreOneView(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	s, err := openLeaseStore(path)
	require.NoError(t, err)
	defer s.Close()
	other, err := openLeaseStore(path) // the writer, on its own connection
	require.NoError(t, err)
	defer other.Close()
	_, _, _, err = s.CreateAttached(txPos("pos_00000001", "s1", 10), txRow("a", "s1", "", resources.CallQueue, "go build", "/w", 10))
	require.NoError(t, err)
	_, err = s.OfferPosition("pos_00000001", 20, "", "", "")
	require.NoError(t, err)
	ok, err := s.GrantInPosition("pos_00000001", resources.PosOffered, "a", 30, false, resources.Decision{}, resources.V2Record{}, "", "")
	require.NoError(t, err)
	require.True(t, ok)

	s.afterFirstStmt = func() error { // between the two reads the command ends and the position goes idle
		_, e := other.EndCommand("a", 40, commandEnd{Reason: resources.EndReleased, Outcome: outcomeIdle, PositionID: "pos_00000001", WindowUntil: 99_000})
		return e
	}
	active, _, _, positions, err := s.ListingWithPositions(10, 0)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Len(t, positions, 1)
	assert.Equal(t, resources.PosRunning, positions[0].State, "the position as it was when the held row was read")
	// The next view sees the change, whole.
	s.afterFirstStmt = nil
	active, _, _, positions, err = s.ListingWithPositions(10, 0)
	require.NoError(t, err)
	assert.Empty(t, active)
	assert.Equal(t, resources.PosIdle, positions[0].State)
}

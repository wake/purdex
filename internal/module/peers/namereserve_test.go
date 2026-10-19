package peers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/team"
)

// The reservations of a member's name (name pool v2 spec §5.2): a spawn gets its name when the op is accepted, long before the
// session exists, and no other naming takes it meanwhile. They are in memory, under the pool lock; they lapse after ten
// minutes; at boot they are rebuilt from the team module's open ops.

var tNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// reserveEnv is rulesEnv (personas hiro lead:true, anna, mia) with the clock fixed and a live lead A named purdex-54-hiro.
func reserveEnv(t *testing.T) *poolEnv {
	t.Helper()
	e := rulesEnv(t)
	e.m.now = func() time.Time { return tNow }
	e.start(poolSidA, tEarly)
	e.assign(poolSidA, "purdex-54-hiro", store.PeerNameSourcePool) // the lead
	return e
}

type fakeReader struct {
	list []ipeers.Reservation
	err  error
	n    int
}

func (f *fakeReader) OpenReservations(context.Context) ([]ipeers.Reservation, error) {
	f.n++
	return f.list, f.err
}

func TestReservations_AMemberGetsAFreeNameByRule2AndNoOneElseTakesItMeanwhile(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	require.NoError(t, e.meta.NameHistory().RecordPair(ctx, "hiro", "mia", 5))
	code, err := e.m.ReserveMember(ctx, poolSidA, "op-1", "tm-1", tNow)
	require.NoError(t, err)
	assert.Equal(t, "mia", code, "paired with the lead before, though anna is first in the pool")

	// the reserved name is held: the next member and an ordinary new session take the others
	code2, err := e.m.ReserveMember(ctx, poolSidA, "op-2", "tm-2", tNow)
	require.NoError(t, err)
	assert.Equal(t, "anna", code2)
	// mia and anna are reserved and hiro is A's: an ordinary new session finds the pool used up and takes its ref suffix
	e.start(poolSidB, tLate)
	assert.Equal(t, "purdex-54-"+ipeers.RefID(poolSidB)[1:3], e.nameOf(poolSidB))
}

func TestReservations_ASecondReserveOfOneOpGivesTheSameName(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	a, err := e.m.ReserveMember(ctx, poolSidA, "op-1", "tm-1", tNow)
	require.NoError(t, err)
	b, err := e.m.ReserveMember(ctx, poolSidA, "op-1", "tm-1", tNow)
	require.NoError(t, err)
	assert.Equal(t, a, b)
	c, err := e.m.ReserveMember(ctx, poolSidA, "op-2", "tm-2", tNow)
	require.NoError(t, err)
	assert.NotEqual(t, a, c, "a retry of op-1 did not take a second name")
}

func TestReservations_NothingFreeIsAnEmptyNameNotAnError(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	holdRemoteNow(e, tEarly, "anna", "mia") // hiro is held by the lead A
	code, err := e.m.ReserveMember(ctx, poolSidA, "op-1", "tm-1", tNow)
	require.NoError(t, err)
	assert.Equal(t, "", code)
}

func TestReservations_ReleasingFreesTheNameAndIsIdempotent(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	holdRemoteNow(e, tEarly, "anna") // free: mia only (hiro is the lead's)
	code, err := e.m.ReserveMember(ctx, poolSidA, "op-1", "tm-1", tNow)
	require.NoError(t, err)
	require.Equal(t, "mia", code)
	again, err := e.m.ReserveMember(ctx, poolSidA, "op-2", "tm-2", tNow)
	require.NoError(t, err)
	assert.Equal(t, "", again, "mia is reserved")

	e.m.ReleaseReservation("op-1")
	e.m.ReleaseReservation("op-1")
	e.m.ReleaseReservation("unknown")
	again, err = e.m.ReserveMember(ctx, poolSidA, "op-2", "tm-2", tNow)
	require.NoError(t, err)
	assert.Equal(t, "mia", again)
}

func TestReservations_LapseAfterTenMinutes(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	e.start(poolSidB, tLate)
	e.assign(poolSidB, "purdex-54-anna", store.PeerNameSourcePool) // anna is a live session's, so only mia is left
	_, err := e.m.ReserveMember(ctx, poolSidA, "op-1", "tm-1", tNow)
	require.NoError(t, err)
	e.m.now = func() time.Time { return tNow.Add(reservationTTL - time.Second) }
	got, _ := e.m.ReserveMember(ctx, poolSidA, "op-2", "tm-2", e.m.now())
	assert.Equal(t, "", got, "still held a second before the end")
	e.m.now = func() time.Time { return tNow.Add(reservationTTL + time.Second) }
	got, _ = e.m.ReserveMember(ctx, poolSidA, "op-2", "tm-2", e.m.now())
	assert.Equal(t, "mia", got, "a missed release costs nothing")
}

func TestReservations_ReserveExactIsRefusedWhenTheNameIsHeldHere(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	require.NoError(t, e.m.ReserveExact(ctx, "mia", "op-1", "tm-1", tNow))
	assert.ErrorIs(t, e.m.ReserveExact(ctx, "mia", "op-2", "tm-2", tNow), ipeers.ErrNameTaken, "held by a reservation")
	assert.NoError(t, e.m.ReserveExact(ctx, "mia", "op-1", "tm-1", tNow), "the same op again is a success")
	assert.ErrorIs(t, e.m.ReserveExact(ctx, "hiro", "op-3", "tm-3", tNow), ipeers.ErrNameTaken, "held by the live lead A")
	assert.ErrorIs(t, e.m.ReserveExact(ctx, "zed", "op-4", "tm-4", tNow), ipeers.ErrNameUnknown, "not a pickable persona code")
}

func TestReservations_TheyAreRebuiltFromTheTeamModuleAtFirstUse(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	rd := &fakeReader{list: []ipeers.Reservation{{OpID: "op-1", Code: "mia", TmuxName: "tm-1", CreatedAt: tNow}}}
	c := newTestCoreWithHosts(t, "host-mlab", "mlab", nil)
	c.Registry.Register(team.SpawnReservationsKey, rd)
	e.m.core = c
	holdRemoteNow(e, tEarly, "anna")

	got, err := e.m.ReserveMember(ctx, poolSidA, "op-2", "tm-2", tNow)
	require.NoError(t, err)
	assert.Equal(t, "", got, "mia is held by the op the team module still has open")
	_, _ = e.m.ReserveMember(ctx, poolSidA, "op-3", "tm-3", tNow)
	assert.Equal(t, 1, rd.n, "read once")

	e.m.ReleaseReservation("op-1")
	got, _ = e.m.ReserveMember(ctx, poolSidA, "op-2", "tm-2", tNow)
	assert.Equal(t, "mia", got)
}

func TestReservations_AnUnreadableTeamStoreIsRetriedAndANewerOpIsKept(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	rd := &fakeReader{err: errors.New("team.db down")}
	c := newTestCoreWithHosts(t, "host-mlab", "mlab", nil)
	c.Registry.Register(team.SpawnReservationsKey, rd)
	e.m.core = c
	holdRemoteNow(e, tEarly, "anna")

	_, err := e.m.ReserveMember(ctx, poolSidA, "op-1", "tm-1", tNow)
	require.NoError(t, err, "the reservation itself does not wait for the rebuild")
	assert.True(t, e.logged("team.db down"))
	rd.err = nil
	rd.list = []ipeers.Reservation{{OpID: "op-1", Code: "mia", TmuxName: "tm-1", CreatedAt: tNow}}
	got, _ := e.m.ReserveMember(ctx, poolSidA, "op-2", "tm-2", tNow)
	assert.Equal(t, "", got, "the next use rebuilt it and op-1 holds mia")
	assert.Equal(t, 2, rd.n)
}

func TestReservations_AnExpiredOpIsNotRebuilt(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	rd := &fakeReader{list: []ipeers.Reservation{{OpID: "op-old", Code: "mia", TmuxName: "tm-0", CreatedAt: tNow.Add(-2 * reservationTTL)}}}
	c := newTestCoreWithHosts(t, "host-mlab", "mlab", nil)
	c.Registry.Register(team.SpawnReservationsKey, rd)
	e.m.core = c
	holdRemoteNow(e, tEarly, "anna")
	got, err := e.m.ReserveMember(ctx, poolSidA, "op-1", "tm-1", tNow)
	require.NoError(t, err)
	assert.Equal(t, "mia", got)
}

// The naming path (NP-0 result, b′): the first naming pass of a spawned session finds its reservation by the tmux session name.
func TestReservations_ASpawnedSessionTakesItsReservedNameAtItsFirstNaming(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	require.NoError(t, e.meta.NameHistory().RecordPair(ctx, "hiro", "mia", 5))
	code, err := e.m.ReserveMember(ctx, poolSidA, "op-1", "tm-1", tNow)
	require.NoError(t, err)
	require.Equal(t, "mia", code)

	e.start(poolSidB, tLate)
	c := cand(poolSidB, "purdex-54")
	c.tmuxName = "tm-1"
	got := e.m.resolveNames(ctx, []nameCandidate{c})[poolSidB]
	assert.Equal(t, "purdex-54-mia", got, "the reserved name, though rule 3 would take anna")

	e.start(poolSidC, tLate)
	other := cand(poolSidC, "purdex-54")
	other.tmuxName = "tm-other"
	assert.Equal(t, "purdex-54-anna", e.m.resolveNames(ctx, []nameCandidate{other})[poolSidC], "a session without a reservation: rule 3, not the reserved name")
}

func TestReservations_TheTmuxNameReachesTheCandidates(t *testing.T) {
	entries := []ipeers.Entry{{PID: 1, SessionID: poolSidA, Name: "x"}, {PID: 2, SessionID: poolSidB, Name: "y"}}
	got := entryNameCandidates(entries, nil, nil, map[string]string{poolSidA: "tm-1"})
	require.Len(t, got, 2)
	assert.Equal(t, "tm-1", got[0].tmuxName)
	assert.Equal(t, "", got[1].tmuxName)
}

// holdRemoteNow is holdRemote with the snapshot recorded at the test's fixed clock: the remote names expire by m.now.
func holdRemoteNow(e *poolEnv, start time.Time, names ...string) {
	var rows []ipeers.PeerRecord
	for _, n := range names {
		rows = append(rows, snapRow("remote-"+n, start.UTC().Format(ipeers.ProcStartLayout)))
	}
	e.m.remoteNames.record("host-remote", rows, ipeers.AddressVersionV5, e.m.now())
}

// codex R2 on A1: a rebuild must not give one name to two ops.
func TestReservations_ARebuiltOpDoesNotTakeANameAnotherOpHolds(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	rd := &fakeReader{err: errors.New("team.db down")}
	c := newTestCoreWithHosts(t, "host-mlab", "mlab", nil)
	c.Registry.Register(team.SpawnReservationsKey, rd)
	e.m.core = c
	require.NoError(t, e.meta.NameHistory().RecordPair(ctx, "hiro", "mia", 5))

	got, err := e.m.ReserveMember(ctx, poolSidA, "op-new", "tm-new", tNow)
	require.NoError(t, err)
	require.Equal(t, "mia", got)
	rd.err = nil
	rd.list = []ipeers.Reservation{
		{OpID: "op-old", Code: "mia", TmuxName: "tm-old", CreatedAt: tNow.Add(-time.Minute)},
		{OpID: "op-dup", Code: "anna", TmuxName: "tm-d1", CreatedAt: tNow},
		{OpID: "op-dup2", Code: "anna", TmuxName: "tm-d2", CreatedAt: tNow},
	}
	_, _ = e.m.ReserveMember(ctx, poolSidA, "op-x", "tm-x", tNow)
	_, ok := e.m.reservations.get("op-old", tNow)
	assert.False(t, ok, "mia stays with op-new")
	n := 0
	for _, id := range []string{"op-dup", "op-dup2"} {
		if _, ok := e.m.reservations.get(id, tNow); ok {
			n++
		}
	}
	assert.Equal(t, 1, n, "a reader that lists anna twice keeps one of them")
}

func TestReservations_TwoOpsUnderOneTmuxNameGiveTheOlderOne(t *testing.T) {
	e := reserveEnv(t)
	for i := 0; i < 20; i++ { // map order must not matter
		e.m.reservations.put(reservation{opID: "op-b", code: "anna", tmuxName: "tm", at: tNow.Add(-time.Minute)})
		e.m.reservations.put(reservation{opID: "op-a", code: "mia", tmuxName: "tm", at: tNow})
		r, ok := e.m.reservations.byTmux("tm", tNow)
		require.True(t, ok)
		assert.Equal(t, "op-b", r.opID)
	}
}

func TestReservations_AFutureStartDoesNotStretchTheTenMinutes(t *testing.T) {
	e := reserveEnv(t)
	ctx := context.Background()
	e.start(poolSidB, tLate)
	e.assign(poolSidB, "purdex-54-anna", store.PeerNameSourcePool) // anna is a live session's, so only mia is left
	_, err := e.m.ReserveMember(ctx, poolSidA, "op-1", "tm-1", tNow.Add(3*time.Hour))
	require.NoError(t, err)
	e.m.now = func() time.Time { return tNow.Add(reservationTTL + time.Second) }
	got, _ := e.m.ReserveMember(ctx, poolSidA, "op-2", "tm-2", e.m.now())
	assert.Equal(t, "mia", got, "op-1 lapsed ten minutes after it was made, not after its claimed start")
}

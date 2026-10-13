package resourcesmod

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// createRow inserts a waiting row, for fixtures.
func createRow(s *leaseStore, r leaseRow) error {
	_, _, err := s.Create(r)
	return err
}

func memStore(t *testing.T) *leaseStore {
	t.Helper()
	s, err := openLeaseStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

// txRow makes a waiting row of a position.
func txRow(id, session, positionID, mode, command, cwd string, createdAt int64) leaseRow {
	return leaseRow{ID: id, ClientID: "client-" + id, Weight: 35, SessionID: session, HolderPID: 7,
		Scope: resources.ScopeProcess, CreatedAt: createdAt, PositionID: positionID, Mode: mode, Command: command, Cwd: cwd}
}

func txPos(id, session string, createdAt int64) positionRow {
	return positionRow{ID: id, SessionID: session, CreatedAt: createdAt, AgentPID: 99, AgentStart: "Thu Oct  9 00:00:00 2026"}
}

func mustPos(t *testing.T, s *leaseStore, id string) positionRow {
	t.Helper()
	p, ok, err := s.PositionByID(id)
	require.NoError(t, err)
	require.True(t, ok, id)
	return p
}

func mustRow(t *testing.T, s *leaseStore, id string) leaseRow {
	t.Helper()
	r, ok, err := s.Get(id)
	require.NoError(t, err)
	require.True(t, ok, id)
	return r
}

// setState forces a position state by direct SQL, for building a fixture.
func setState(t *testing.T, s *leaseStore, id, sqlSet string) {
	t.Helper()
	_, err := s.db.Exec(`UPDATE resource_positions SET ` + sqlSet + ` WHERE id = '` + id + `'`)
	require.NoError(t, err)
}

func holdRow(t *testing.T, s *leaseStore, id string) {
	t.Helper()
	_, err := s.db.Exec(`UPDATE resource_leases SET state = 'held', granted_at = 50 WHERE id = ?`, id)
	require.NoError(t, err)
}

// ---- P1 ----

func TestPositions_CreateAttachedReturnsTheLiveOne(t *testing.T) {
	s := memStore(t)
	row1, pos1, created, err := s.CreateAttached(txPos("pos_00000001", "s1", 10), txRow("a", "s1", "", resources.CallQueue, "go test", "/w", 10))
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "pos_00000001", pos1.ID)
	assert.Equal(t, "pos_00000001", row1.PositionID)

	// A second create for the session attaches to the first position, and the
	// attempted new id is not used.
	row2, pos2, created, err := s.CreateAttached(txPos("pos_00000002", "s1", 11), txRow("b", "s1", "", resources.CallQueue, "go build", "/w", 11))
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "pos_00000001", pos2.ID)
	assert.Equal(t, "pos_00000001", row2.PositionID)
	_, ok, err := s.PositionByID("pos_00000002")
	require.NoError(t, err)
	assert.False(t, ok)

	// After it ends, the next create makes a new one.
	setState(t, s, "pos_00000001", "state = 'ended', end_reason = 'done', ended_at = 20")
	_, pos3, _, err := s.CreateAttached(txPos("pos_00000003", "s1", 30), txRow("c", "s1", "", resources.CallQueue, "x", "/w", 30))
	require.NoError(t, err)
	assert.Equal(t, "pos_00000003", pos3.ID)
}

func TestPositions_CreateAttachedReplayLeavesNoEmptyPosition(t *testing.T) {
	s := memStore(t)
	_, _, _, err := s.CreateAttached(txPos("pos_00000001", "s1", 10), txRow("a", "s1", "", resources.CallQueue, "x", "/w", 10))
	require.NoError(t, err)
	// The same client id again, for a session that has no position yet.
	dup := txRow("a2", "s2", "", resources.CallQueue, "x", "/w", 11)
	dup.ClientID = "client-a"
	row, _, created, err := s.CreateAttached(txPos("pos_00000002", "s2", 11), dup)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, "a", row.ID, "the existing row is returned")
	_, ok, err := s.PositionByID("pos_00000002")
	require.NoError(t, err)
	assert.False(t, ok, "a replay does not leave an empty queued position behind")
}

// ---- P2 .. P5 ----

func queuedFixture(t *testing.T, s *leaseStore) {
	t.Helper()
	_, _, _, err := s.CreateAttached(txPos("pos_00000001", "s1", 10), txRow("a", "s1", "", resources.CallQueue, "go test", "/w", 10))
	require.NoError(t, err)
}

func TestPositions_OfferCASOnce(t *testing.T) {
	s := memStore(t)
	queuedFixture(t, s)
	ok, err := s.OfferPosition("pos_00000001", 1000, "normal", "flat", `{"gap":5}`)
	require.NoError(t, err)
	assert.True(t, ok)
	p := mustPos(t, s, "pos_00000001")
	assert.Equal(t, resources.PosOffered, p.State)
	assert.Equal(t, int64(1000), p.OfferedAt)
	assert.Equal(t, "normal", p.OfferZone)
	assert.Equal(t, `{"gap":5}`, p.WaitReasons)
	assert.Equal(t, "offer", p.NoticeKind)
	assert.Equal(t, int64(1000), p.NoticeDueAt)
	assert.Equal(t, int64(1000), mustRow(t, s, "a").OfferedAt, "the row is named")
	// A second offer loses the compare-and-set and changes nothing.
	ok, err = s.OfferPosition("pos_00000001", 2000, "danger", "rising", "")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, int64(1000), mustPos(t, s, "pos_00000001").OfferedAt)
}

func TestPositions_StartKeepsTheFirstStart(t *testing.T) {
	s := memStore(t)
	queuedFixture(t, s)
	_, err := s.OfferPosition("pos_00000001", 1000, "", "", "")
	require.NoError(t, err)
	ok, err := s.GrantInPosition("pos_00000001", resources.PosOffered, "a", 1100, false, resources.Decision{Path: resources.PathOffer}, resources.V2Record{}, "", "")
	require.NoError(t, err)
	require.True(t, ok)
	p := mustPos(t, s, "pos_00000001")
	assert.Equal(t, resources.PosRunning, p.State)
	assert.Equal(t, int64(1100), p.FirstStartedAt)
	assert.Equal(t, 1, p.CommandsStarted)
	r := mustRow(t, s, "a")
	assert.Equal(t, "held", r.State)

	// A second command of the running position counts and keeps the first start.
	require.NoError(t, createRow(s, txRow("b", "s1", "pos_00000001", resources.CallQueue, "x", "/w", 1200)))
	ok, err = s.GrantInPosition("pos_00000001", resources.PosRunning, "b", 1300, false, resources.Decision{Path: resources.PathImmediate}, resources.V2Record{}, "", "")
	require.NoError(t, err)
	require.True(t, ok)
	p = mustPos(t, s, "pos_00000001")
	assert.Equal(t, int64(1100), p.FirstStartedAt)
	assert.Equal(t, 2, p.CommandsStarted)

	// Idle → running (P4) with a window cleared.
	setState(t, s, "pos_00000001", "state = 'idle', window_until = 5000")
	require.NoError(t, createRow(s, txRow("c", "s1", "pos_00000001", resources.CallQueue, "y", "/w", 1400)))
	ok, err = s.GrantInPosition("pos_00000001", resources.PosIdle, "c", 1500, false, resources.Decision{Path: resources.PathContinued}, resources.V2Record{}, "", "")
	require.NoError(t, err)
	require.True(t, ok)
	p = mustPos(t, s, "pos_00000001")
	assert.Equal(t, resources.PosRunning, p.State)
	assert.Zero(t, p.WindowUntil)
	assert.Equal(t, int64(1100), p.FirstStartedAt)
}

func TestPositions_GrantLosesWhenTheRowOrThePositionMoved(t *testing.T) {
	s := memStore(t)
	queuedFixture(t, s)
	// Not offered yet: a start from offered loses, and nothing changes.
	ok, err := s.GrantInPosition("pos_00000001", resources.PosOffered, "a", 1100, false, resources.Decision{}, resources.V2Record{}, "", "")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, "waiting", mustRow(t, s, "a").State)
	// The row ended meanwhile: the position is not started either.
	_, err = s.OfferPosition("pos_00000001", 1000, "", "", "")
	require.NoError(t, err)
	_, err = s.End("a", resources.EndCancelled, 1050)
	require.NoError(t, err)
	ok, err = s.GrantInPosition("pos_00000001", resources.PosOffered, "a", 1100, false, resources.Decision{}, resources.V2Record{}, "", "")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, resources.PosOffered, mustPos(t, s, "pos_00000001").State, "the position start rolled back with the lost row")
}

// ---- P6 .. P8 ----

func runningFixture(t *testing.T, s *leaseStore) {
	t.Helper()
	queuedFixture(t, s)
	_, err := s.OfferPosition("pos_00000001", 1000, "", "", "")
	require.NoError(t, err)
	ok, err := s.GrantInPosition("pos_00000001", resources.PosOffered, "a", 1100, false, resources.Decision{}, resources.V2Record{}, "", "")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestPositions_EndCommandIdleNamesTheWaitingRows(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	require.NoError(t, createRow(s, txRow("b", "s1", "pos_00000001", resources.CallQueue, "next", "/w", 1200)))
	res, err := s.EndCommand("a", 2000, commandEnd{Reason: resources.EndReleased, Outcome: outcomeIdle, PositionID: "pos_00000001",
		WindowUntil: 2000 + 180_000, Warned: true, NewWarning: true, Notice: "last one"})
	require.NoError(t, err)
	assert.True(t, res.Ended)
	p := mustPos(t, s, "pos_00000001")
	assert.Equal(t, resources.PosIdle, p.State)
	assert.Equal(t, 1, p.CommandsEnded)
	assert.Equal(t, int64(182_000), p.WindowUntil)
	assert.True(t, p.Warned)
	assert.Equal(t, 1, p.Extensions)
	assert.Equal(t, "idle", p.NoticeKind)
	assert.Equal(t, "last one", mustRow(t, s, "a").Notice)
	assert.Equal(t, int64(2000), mustRow(t, s, "b").OfferedAt, "the waiting row is named")

	// A second EndCommand of the same row loses.
	res, err = s.EndCommand("a", 2100, commandEnd{Reason: resources.EndReleased, Outcome: outcomeIdle, PositionID: "pos_00000001"})
	require.NoError(t, err)
	assert.False(t, res.Ended)
}

func TestPositions_EndCommandCountOnlyWhenAnotherRowRemains(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	_, err := s.EndCommand("a", 2000, commandEnd{Reason: resources.EndReleased, Outcome: outcomeCount, PositionID: "pos_00000001"})
	require.NoError(t, err)
	p := mustPos(t, s, "pos_00000001")
	assert.Equal(t, resources.PosRunning, p.State, "still running")
	assert.Equal(t, 1, p.CommandsEnded)
}

func TestPositions_ReleaseMovesAskRowsToANewQueuedPosition(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	require.NoError(t, createRow(s, txRow("q", "s1", "pos_00000001", resources.CallQueue, "fg", "/w", 1200)))
	require.NoError(t, createRow(s, txRow("k", "s1", "pos_00000001", resources.CallAsk, "bg", "/w", 1300)))
	_, err := s.db.Exec(`UPDATE resource_leases SET offered_at = 1500, confirmed_at = 1600 WHERE id = 'k'`)
	require.NoError(t, err)

	res, err := s.EndCommand("a", 2000, commandEnd{Reason: resources.EndReleased, Outcome: outcomeRelease, PositionID: "pos_00000001", NewPositionID: "pos_00000009"})
	require.NoError(t, err)
	require.True(t, res.Ended)
	assert.Equal(t, "pos_00000009", res.NewPositionID)
	old := mustPos(t, s, "pos_00000001")
	assert.Equal(t, resources.PosEnded, old.State)
	assert.Equal(t, resources.EndReleasedAfterLimit, old.EndReason)
	q := mustRow(t, s, "q")
	assert.Equal(t, "ended", q.State)
	assert.Equal(t, resources.EndReleasedAfterLimit, q.EndReason)
	k := mustRow(t, s, "k")
	assert.Equal(t, "waiting", k.State)
	assert.Equal(t, "pos_00000009", k.PositionID)
	assert.Zero(t, k.OfferedAt)
	assert.Zero(t, k.ConfirmedAt)
	n := mustPos(t, s, "pos_00000009")
	assert.Equal(t, resources.PosQueued, n.State)
	assert.Equal(t, "s1", n.SessionID)
	assert.Equal(t, 99, n.AgentPID, "the agent is copied")
	assert.Equal(t, "pos_00000001", n.RequeuedFrom)
	assert.Equal(t, int64(2000), n.CreatedAt)
}

func TestPositions_ReleaseWithoutAskRowsMakesNoNewPosition(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	res, err := s.EndCommand("a", 2000, commandEnd{Reason: resources.EndReleased, Outcome: outcomeRelease, PositionID: "pos_00000001", NewPositionID: "pos_00000009"})
	require.NoError(t, err)
	assert.True(t, res.Ended)
	assert.Empty(t, res.NewPositionID)
	live, err := s.LivePositions()
	require.NoError(t, err)
	assert.Empty(t, live)
}

func TestPositions_EndCommandEndPositionEndsItsWaitingRows(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	require.NoError(t, createRow(s, txRow("q", "s1", "pos_00000001", resources.CallQueue, "fg", "/w", 1200)))
	_, err := s.EndCommand("a", 2000, commandEnd{Reason: resources.EndReleased, Outcome: outcomeEnd, PositionID: "pos_00000001", PosReason: resources.EndDone})
	require.NoError(t, err)
	assert.Equal(t, resources.EndDone, mustPos(t, s, "pos_00000001").EndReason)
	assert.Equal(t, resources.EndDone, mustRow(t, s, "q").EndReason)
}

func TestPositions_EndCommandOfARowWithNoPositionIsAPlainEnd(t *testing.T) {
	s := memStore(t)
	require.NoError(t, createRow(s, txRow("p", "s1", "", "", "x", "/w", 10)))
	holdRow(t, s, "p")
	res, err := s.EndCommand("p", 100, commandEnd{Reason: resources.EndReleased})
	require.NoError(t, err)
	assert.True(t, res.Ended)
	assert.Equal(t, "ended", mustRow(t, s, "p").State)
}

// ---- P9 .. P16 ----

// ---- adopt, boot, prune ----

// ---- boot reconcile ----

func fileStore(t *testing.T) (*leaseStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resources.db")
	s, err := openLeaseStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s, path
}

// ---- crash boundaries ----

// dumpTables is every row of both tables, as text, for comparing before and
// after an aborted operation.
func dumpTables(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	for _, table := range []string{"resource_positions", "resource_leases"} {
		rows, err := db.Query("SELECT * FROM " + table + " ORDER BY id")
		require.NoError(t, err)
		cols, err := rows.Columns()
		require.NoError(t, err)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			fmt.Fprintf(&b, "%s %v\n", table, vals)
		}
		require.NoError(t, rows.Close())
	}
	return b.String()
}

var errCrash = errors.New("simulated crash between the statements")

// An operation that writes both tables fails between its two statements: after
// the file database is closed and reopened, both tables read as before it.
func TestTx_FailureBetweenStatementsLeavesNeither(t *testing.T) {
	type op struct {
		name  string
		setup func(t *testing.T, s *leaseStore)
		do    func(s *leaseStore) error
	}
	askRelease := func(t *testing.T, s *leaseStore) {
		runningFixture(t, s)
		require.NoError(t, createRow(s, txRow("k", "s1", "pos_00000001", resources.CallAsk, "bg", "/w", 1300)))
	}
	ops := []op{
		{"CreateAttached", func(t *testing.T, s *leaseStore) {}, func(s *leaseStore) error {
			_, _, _, err := s.CreateAttached(txPos("pos_00000001", "s1", 10), txRow("a", "s1", "", resources.CallQueue, "x", "/w", 10))
			return err
		}},
		{"OfferPosition", func(t *testing.T, s *leaseStore) { queuedFixture(t, s) }, func(s *leaseStore) error {
			_, err := s.OfferPosition("pos_00000001", 1000, "normal", "flat", "")
			return err
		}},
		{"GrantInPosition", func(t *testing.T, s *leaseStore) {
			queuedFixture(t, s)
			_, err := s.OfferPosition("pos_00000001", 1000, "", "", "")
			require.NoError(t, err)
		}, func(s *leaseStore) error {
			_, err := s.GrantInPosition("pos_00000001", resources.PosOffered, "a", 1100, false, resources.Decision{}, resources.V2Record{}, "", "")
			return err
		}},
		{"EndCommand idle", func(t *testing.T, s *leaseStore) {
			runningFixture(t, s)
			require.NoError(t, createRow(s, txRow("b", "s1", "pos_00000001", resources.CallQueue, "x", "/w", 1200)))
		}, func(s *leaseStore) error {
			_, err := s.EndCommand("a", 2000, commandEnd{Reason: resources.EndReleased, Outcome: outcomeIdle, PositionID: "pos_00000001", WindowUntil: 9000})
			return err
		}},
		{"EndCommand release", askRelease, func(s *leaseStore) error {
			_, err := s.EndCommand("a", 2000, commandEnd{Reason: resources.EndReleased, Outcome: outcomeRelease, PositionID: "pos_00000001", NewPositionID: "pos_00000009"})
			return err
		}},
		{"EndCommand end", func(t *testing.T, s *leaseStore) { runningFixture(t, s) }, func(s *leaseStore) error {
			_, err := s.EndCommand("a", 2000, commandEnd{Reason: resources.EndReleased, Outcome: outcomeEnd, PositionID: "pos_00000001", PosReason: resources.EndDone})
			return err
		}},
	}
	for _, o := range ops {
		t.Run(o.name, func(t *testing.T) {
			s, path := fileStore(t)
			o.setup(t, s)
			before := dumpTables(t, s.db)

			s.afterFirstStmt = func() error { return errCrash }
			require.ErrorIs(t, o.do(s), errCrash)
			require.NoError(t, s.Close())
			s2, err := openLeaseStore(path)
			require.NoError(t, err)
			defer s2.Close()
			assert.Equal(t, before, dumpTables(t, s2.db), "an operation that failed between its statements changed nothing")
		})
	}
}

// The seam is real: without it the same operations do change the tables (a
// guard against a test that passes because nothing ever runs).
func TestTx_TheOperationsDoChangeThingsWithoutTheFailure(t *testing.T) {
	s, _ := fileStore(t)
	queuedFixture(t, s)
	before := dumpTables(t, s.db)
	_, err := s.OfferPosition("pos_00000001", 1000, "normal", "flat", "")
	require.NoError(t, err)
	assert.NotEqual(t, before, dumpTables(t, s.db))
}

// A pair that does not belong together changes nothing.
func TestPositions_MismatchedRowAndPositionChangeNothing(t *testing.T) {
	s := memStore(t)
	_, _, _, err := s.CreateAttached(txPos("pos_00000001", "s1", 10), txRow("a", "s1", "", resources.CallQueue, "x", "/w", 10))
	require.NoError(t, err)
	_, _, _, err = s.CreateAttached(txPos("pos_00000002", "s2", 11), txRow("b", "s2", "", resources.CallQueue, "y", "/w", 11))
	require.NoError(t, err)
	_, err = s.OfferPosition("pos_00000001", 1000, "", "", "")
	require.NoError(t, err)
	before := dumpTables(t, s.db)

	// Start position 1 with the row of position 2.
	ok, err := s.GrantInPosition("pos_00000001", resources.PosOffered, "b", 1100, false, resources.Decision{}, resources.V2Record{}, "", "")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, before, dumpTables(t, s.db))

	// End the held row of position 2 as a command of position 1.
	holdRow(t, s, "b")
	setState(t, s, "pos_00000001", "state = 'running'")
	before = dumpTables(t, s.db)
	res, err := s.EndCommand("b", 2000, commandEnd{Reason: resources.EndReleased, Outcome: outcomeCount, PositionID: "pos_00000001"})
	require.NoError(t, err)
	assert.False(t, res.Ended)
	assert.Equal(t, before, dumpTables(t, s.db))
}

// A count outcome on a position that is not running loses, and the row stays held.
func TestPositions_CountOutcomeNeedsARunningPosition(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	setState(t, s, "pos_00000001", "state = 'idle'")
	res, err := s.EndCommand("a", 2000, commandEnd{Reason: resources.EndReleased, Outcome: outcomeCount, PositionID: "pos_00000001"})
	require.NoError(t, err)
	assert.False(t, res.Ended)
	assert.Equal(t, "held", mustRow(t, s, "a").State)
}

// Only a held row of the position ends a command of it.
func TestPositions_AWaitingRowCannotEndACommand(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	require.NoError(t, createRow(s, txRow("w", "s1", "pos_00000001", resources.CallQueue, "next", "/w", 1200)))
	before := dumpTables(t, s.db)
	res, err := s.EndCommand("w", 2000, commandEnd{Reason: resources.EndReleased, Outcome: outcomeIdle, PositionID: "pos_00000001", WindowUntil: 9000})
	require.NoError(t, err)
	assert.False(t, res.Ended)
	assert.Equal(t, before, dumpTables(t, s.db), "the running position was not moved by a waiting row")
}

// A row with a live position is not ended alone (R5): the position must hear about it.
func TestPositions_APositionedHeldRowCannotEndWithoutItsPosition(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	before := dumpTables(t, s.db)
	res, err := s.EndCommand("a", 2000, commandEnd{Reason: resources.EndReleased})
	require.NoError(t, err)
	assert.False(t, res.Ended)
	assert.Equal(t, before, dumpTables(t, s.db))
	// Once the position has ended (or is gone), the plain end is allowed.
	setState(t, s, "pos_00000001", "state = 'ended', end_reason = 'done', ended_at = 5")
	res, err = s.EndCommand("a", 2100, commandEnd{Reason: resources.EndReleased})
	require.NoError(t, err)
	assert.True(t, res.Ended)
}

func TestPositions_EndOnlyFromTheNamedStates(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	ok, err := s.EndPositionWithRows("pos_00000001", 3000, endPosition{Reason: resources.EndCancelledBySession, From: []string{resources.PosQueued, resources.PosOffered, resources.PosIdle}})
	require.NoError(t, err)
	assert.False(t, ok, "a running position is not ended by a cancel")
	assert.Equal(t, resources.PosRunning, mustPos(t, s, "pos_00000001").State)
}

func TestPositions_WindowLapseLosesToAStart(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	setState(t, s, "pos_00000001", "state = 'idle', window_until = 5000")
	_, err := s.db.Exec(`UPDATE resource_leases SET state = 'ended', end_reason = 'released', ended_at = 4000 WHERE id = 'a'`)
	require.NoError(t, err)
	// The sweeper read window_until=5000; a command starts at once (clearing it).
	require.NoError(t, createRow(s, txRow("b", "s1", "pos_00000001", resources.CallQueue, "x", "/w", 4500)))
	ok, err := s.GrantInPosition("pos_00000001", resources.PosIdle, "b", 4600, false, resources.Decision{}, resources.V2Record{}, "", "")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = s.EndPositionWithRows("pos_00000001", 5100, endPosition{Reason: resources.EndIdleTimeout, From: []string{resources.PosIdle}, WindowGuard: 5000})
	require.NoError(t, err)
	assert.False(t, ok, "the racing start wins")
	assert.Equal(t, resources.PosRunning, mustPos(t, s, "pos_00000001").State)
}

// A new window (a command ran and ended again) is not the one the sweeper read.
func TestPositions_WindowLapseLosesToANewWindow(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	setState(t, s, "pos_00000001", "state = 'idle', window_until = 9000")
	ok, err := s.EndPositionWithRows("pos_00000001", 5100, endPosition{Reason: resources.EndIdleTimeout, From: []string{resources.PosIdle}, WindowGuard: 5000})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, resources.PosIdle, mustPos(t, s, "pos_00000001").State)
}

func TestPositions_IdleTimeoutEndsAskedRowsAsNoAnswer(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	setState(t, s, "pos_00000001", "state = 'idle', window_until = 5000")
	_, err := s.db.Exec(`UPDATE resource_leases SET state = 'ended', end_reason = 'released', ended_at = 4000 WHERE id = 'a'`)
	require.NoError(t, err)
	require.NoError(t, createRow(s, txRow("asked", "s1", "pos_00000001", resources.CallAsk, "bg", "/w", 4100)))
	require.NoError(t, createRow(s, txRow("plain", "s1", "pos_00000001", resources.CallQueue, "fg", "/w", 4200)))
	_, err = s.db.Exec(`UPDATE resource_leases SET offered_at = 4300 WHERE id = 'asked'`)
	require.NoError(t, err)
	ok, err := s.EndPositionWithRows("pos_00000001", 5100, endPosition{Reason: resources.EndIdleTimeout, AskedReason: resources.EndNoAnswer,
		From: []string{resources.PosIdle}, WindowGuard: 5000, Notice: "cancelled"})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, resources.EndNoAnswer, mustRow(t, s, "asked").EndReason)
	assert.Equal(t, resources.EndIdleTimeout, mustRow(t, s, "plain").EndReason)
	p := mustPos(t, s, "pos_00000001")
	assert.Equal(t, resources.EndIdleTimeout, p.EndReason)
	assert.Equal(t, "cancelled", p.NoticeKind)
}

func TestPositions_EndRowMaybePositionEndsAnEmptyQueuedPosition(t *testing.T) {
	s := memStore(t)
	queuedFixture(t, s)
	require.NoError(t, createRow(s, txRow("b", "s1", "pos_00000001", resources.CallQueue, "y", "/w", 11)))
	ok, err := s.EndRowMaybePosition("a", resources.EndCancelledBySession, 100, "")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, resources.PosQueued, mustPos(t, s, "pos_00000001").State, "another waiting row keeps it")
	ok, err = s.EndRowMaybePosition("b", resources.EndCancelledBySession, 200, "")
	require.NoError(t, err)
	require.True(t, ok)
	p := mustPos(t, s, "pos_00000001")
	assert.Equal(t, resources.PosEnded, p.State)
	assert.Equal(t, resources.EndCancelledBySession, p.EndReason)
	ok, err = s.EndRowMaybePosition("b", resources.EndCancelledBySession, 300, "")
	require.NoError(t, err)
	assert.False(t, ok, "an ended row cannot end again")
}

func TestPositions_PassOfferOnlyTheSameUndeliveredOffer(t *testing.T) {
	s := memStore(t)
	queuedFixture(t, s)
	_, err := s.OfferPosition("pos_00000001", 1000, "", "", "")
	require.NoError(t, err)
	ok, err := s.PassOffer("pos_00000001", 999, 5000)
	require.NoError(t, err)
	assert.False(t, ok, "another offer")
	ok, err = s.PassOffer("pos_00000001", 1000, 5000)
	require.NoError(t, err)
	require.True(t, ok)
	p := mustPos(t, s, "pos_00000001")
	assert.Equal(t, resources.PosQueued, p.State)
	assert.Equal(t, int64(5000), p.PassedAt)
	assert.Equal(t, 1, p.OfferPasses)
	assert.Empty(t, p.NoticeKind)
	assert.Zero(t, mustRow(t, s, "a").OfferedAt)
	// A delivered offer is never passed.
	_, err = s.OfferPosition("pos_00000001", 6000, "", "", "")
	require.NoError(t, err)
	ok, err = s.MarkDelivered("pos_00000001", 6000, 6100)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = s.PassOffer("pos_00000001", 6000, 9000)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestPositions_MarkDeliveredOnlyForTheSameOffer(t *testing.T) {
	s := memStore(t)
	queuedFixture(t, s)
	_, err := s.OfferPosition("pos_00000001", 1000, "", "", "")
	require.NoError(t, err)
	ok, err := s.MarkDelivered("pos_00000001", 900, 1100)
	require.NoError(t, err)
	assert.False(t, ok, "a delivery for an older offer changes nothing")
	assert.Zero(t, mustPos(t, s, "pos_00000001").DeliveredAt)
	ok, err = s.MarkDelivered("pos_00000001", 1000, 1100)
	require.NoError(t, err)
	require.True(t, ok)
	p := mustPos(t, s, "pos_00000001")
	assert.Equal(t, int64(1100), p.DeliveredAt)
	assert.Equal(t, int64(1100+180_000), p.WindowUntil)
	ok, err = s.MarkDelivered("pos_00000001", 1000, 1200)
	require.NoError(t, err)
	assert.False(t, ok, "delivered once")
}

func TestPositions_NameRowsOnlyUnnamedRowsOfAnOfferedOrIdlePosition(t *testing.T) {
	s := memStore(t)
	runningFixture(t, s)
	require.NoError(t, createRow(s, txRow("b", "s1", "pos_00000001", resources.CallQueue, "x", "/w", 1200)))
	n, err := s.NameRows("pos_00000001", 1300, "offer")
	require.NoError(t, err)
	assert.Zero(t, n, "a running position names nothing")
	setState(t, s, "pos_00000001", "state = 'idle', window_until = 9000")
	n, err = s.NameRows("pos_00000001", 1400, "idle")
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.Equal(t, int64(1400), mustRow(t, s, "b").OfferedAt)
	assert.Equal(t, "idle", mustPos(t, s, "pos_00000001").NoticeKind)
	n, err = s.NameRows("pos_00000001", 1500, "idle")
	require.NoError(t, err)
	assert.Zero(t, n, "already named")
}

func TestPositions_MarkDoneOnlyWhileRunning(t *testing.T) {
	s := memStore(t)
	queuedFixture(t, s)
	ok, err := s.MarkDone("pos_00000001")
	require.NoError(t, err)
	assert.False(t, ok)
	setState(t, s, "pos_00000001", "state = 'running'")
	ok, err = s.MarkDone("pos_00000001")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, mustPos(t, s, "pos_00000001").DoneRequested)
}

func TestPositions_SmallWrites(t *testing.T) {
	s := memStore(t)
	queuedFixture(t, s)
	require.NoError(t, s.SetNoticeResult("pos_00000001", "accepted", 500))
	require.NoError(t, s.SetNoticeResult("pos_00000001", "unknown", 400))
	require.NoError(t, s.AddWindowWait("pos_00000001", 10, 0))
	require.NoError(t, s.AddWindowWait("pos_00000001", 0, 25))
	p := mustPos(t, s, "pos_00000001")
	assert.Equal(t, "unknown", p.NoticeResult)
	assert.Equal(t, int64(500), p.NoticeSentAt, "sent_at only moves forward")
	assert.Equal(t, int64(10), p.OfferWaitMS)
	assert.Equal(t, int64(25), p.IdleWaitMS)
}

func TestStore_AdoptQueuedOnlySameCommandCwdAndWaiting(t *testing.T) {
	s := memStore(t)
	_, _, _, err := s.CreateAttached(txPos("pos_00000001", "s1", 10), txRow("a", "s1", "", resources.CallQueue, "go test", "/w", 10))
	require.NoError(t, err)
	old := mustRow(t, s, "a")
	n := leaseRow{ClientID: "client-new", HolderPID: 8, HolderStart: "x", ToolUseID: "tu", Scope: resources.ScopeProcess}

	for name, mut := range map[string]func(*leaseRow){
		"another cwd":     func(r *leaseRow) { r.Cwd = "/other" },
		"another command": func(r *leaseRow) { r.Command = "go build" },
		"another client":  func(r *leaseRow) { r.ClientID = "client-zzz" },
		"another place":   func(r *leaseRow) { r.PositionID = "pos_other" },
	} {
		probe := old
		mut(&probe)
		ok, err := s.AdoptQueued(probe, n, 9000)
		require.NoError(t, err)
		assert.False(t, ok, name)
	}
	// An ask row is never adopted, whatever the caller says.
	require.NoError(t, createRow(s, txRow("k", "s1", "pos_00000001", resources.CallAsk, "go test", "/w", 12)))
	ok, err := s.AdoptQueued(mustRow(t, s, "k"), n, 9000)
	require.NoError(t, err)
	assert.False(t, ok, "an ask row")

	ok, err = s.AdoptQueued(old, n, 9000)
	require.NoError(t, err)
	require.True(t, ok)
	got := mustRow(t, s, "a")
	assert.Equal(t, "client-new", got.ClientID)
	assert.Equal(t, 8, got.HolderPID)
	assert.Equal(t, int64(10), got.CreatedAt, "the place stays")
	assert.Equal(t, int64(9000), got.LeaseUntil)

	// A held row is never adopted.
	holdRow(t, s, "a")
	held := mustRow(t, s, "a")
	n.ClientID = "client-newer"
	ok, err = s.AdoptQueued(held, n, 9000)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestStore_EndParkedAtBoot(t *testing.T) {
	s := memStore(t)
	for _, id := range []string{"parked", "plain"} {
		require.NoError(t, createRow(s, txRow(id, "s", "", "", "x", "/w", 10)))
	}
	_, err := s.db.Exec(`UPDATE resource_leases SET parked_until = 99999 WHERE id = 'parked'`)
	require.NoError(t, err)
	n, err := s.EndParkedAtBoot(500)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.Equal(t, resources.EndAbandoned, mustRow(t, s, "parked").EndReason)
	assert.Equal(t, "waiting", mustRow(t, s, "plain").State)
}

func TestPrune_EndedPositionsOlderThanRetention(t *testing.T) {
	s := memStore(t)
	const day = int64(24 * 3600 * 1000)
	now := 100 * day
	for id, endedAt := range map[string]int64{"pos_00000001": now - 15*day, "pos_00000002": now - 13*day} {
		require.NoError(t, s.insertPosition(positionRow{ID: id, SessionID: "s-" + id, State: resources.PosQueued, CreatedAt: 1}))
		setState(t, s, id, fmt.Sprintf("state = 'ended', ended_at = %d", endedAt))
	}
	require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000003", SessionID: "s3", State: resources.PosQueued, CreatedAt: 1}))
	_, err := s.Prune(now - 14*day)
	require.NoError(t, err)
	_, ok, _ := s.PositionByID("pos_00000001")
	assert.False(t, ok, "15 days goes")
	_, ok, _ = s.PositionByID("pos_00000002")
	assert.True(t, ok, "13 days stays")
	_, ok, _ = s.PositionByID("pos_00000003")
	assert.True(t, ok, "a live one stays")
}

func TestReconcile_RepairsEachInconsistentPair(t *testing.T) {
	t.Run("a: running with no held row becomes idle", func(t *testing.T) {
		s, path := fileStore(t)
		require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000001", SessionID: "s1", State: resources.PosQueued, CreatedAt: 1}))
		setState(t, s, "pos_00000001", "state = 'running', commands_started = 2, commands_ended = 1")
		require.NoError(t, s.Close())
		s, err := openLeaseStore(path)
		require.NoError(t, err)
		defer s.Close()
		repairs, err := s.ReconcilePositions(10_000)
		require.NoError(t, err)
		assert.Len(t, repairs, 1)
		p := mustPos(t, s, "pos_00000001")
		assert.Equal(t, resources.PosIdle, p.State)
		assert.Equal(t, int64(10_000+180_000), p.WindowUntil)
		assert.Equal(t, 2, p.CommandsEnded)
	})
	t.Run("a: running with no held row and done requested ends done", func(t *testing.T) {
		s := memStore(t)
		require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000001", SessionID: "s1", State: resources.PosQueued, CreatedAt: 1}))
		setState(t, s, "pos_00000001", "state = 'running', done_requested = 1")
		_, err := s.ReconcilePositions(10_000)
		require.NoError(t, err)
		p := mustPos(t, s, "pos_00000001")
		assert.Equal(t, resources.PosEnded, p.State)
		assert.Equal(t, resources.EndDone, p.EndReason)
	})
	t.Run("b: a held row makes its position running", func(t *testing.T) {
		s := memStore(t)
		require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000001", SessionID: "s1", State: resources.PosQueued, CreatedAt: 1}))
		setState(t, s, "pos_00000001", "state = 'idle', window_until = 5")
		require.NoError(t, createRow(s, txRow("h", "s1", "pos_00000001", resources.CallQueue, "x", "/w", 2)))
		holdRow(t, s, "h")
		repairs, err := s.ReconcilePositions(10_000)
		require.NoError(t, err)
		assert.Len(t, repairs, 1)
		p := mustPos(t, s, "pos_00000001")
		assert.Equal(t, resources.PosRunning, p.State)
		assert.Zero(t, p.WindowUntil)
	})
	t.Run("c: waiting rows of an ended position end with its reason", func(t *testing.T) {
		s := memStore(t)
		require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000001", SessionID: "s1", State: resources.PosQueued, CreatedAt: 1}))
		setState(t, s, "pos_00000001", "state = 'ended', end_reason = 'cancelled_by_session', ended_at = 5")
		require.NoError(t, createRow(s, txRow("w", "s1", "pos_00000001", resources.CallQueue, "x", "/w", 2)))
		_, err := s.ReconcilePositions(10_000)
		require.NoError(t, err)
		r := mustRow(t, s, "w")
		assert.Equal(t, "ended", r.State)
		assert.Equal(t, resources.EndCancelledBySession, r.EndReason)
	})
	t.Run("c: waiting rows of a missing position end abandoned; held rows are left", func(t *testing.T) {
		s := memStore(t)
		require.NoError(t, createRow(s, txRow("w", "s1", "pos_gone", resources.CallQueue, "x", "/w", 2)))
		require.NoError(t, createRow(s, txRow("h", "s1", "pos_gone", resources.CallQueue, "x", "/w", 3)))
		holdRow(t, s, "h")
		_, err := s.ReconcilePositions(10_000)
		require.NoError(t, err)
		w := mustRow(t, s, "w")
		assert.Equal(t, "ended", w.State)
		assert.Equal(t, resources.EndAbandoned, w.EndReason)
		assert.Equal(t, "held", mustRow(t, s, "h").State, "a held row of a missing position keeps running (R5)")
	})
	t.Run("d: queued with no waiting row ends abandoned", func(t *testing.T) {
		s := memStore(t)
		require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000001", SessionID: "s1", State: resources.PosQueued, CreatedAt: 1}))
		repairs, err := s.ReconcilePositions(10_000)
		require.NoError(t, err)
		assert.Len(t, repairs, 1)
		p := mustPos(t, s, "pos_00000001")
		assert.Equal(t, resources.PosEnded, p.State)
		assert.Equal(t, resources.EndAbandoned, p.EndReason)
	})
	t.Run("a consistent database repairs nothing", func(t *testing.T) {
		s := memStore(t)
		queuedFixture(t, s)
		repairs, err := s.ReconcilePositions(10_000)
		require.NoError(t, err)
		assert.Empty(t, repairs)
		assert.Equal(t, resources.PosQueued, mustPos(t, s, "pos_00000001").State)
	})
}

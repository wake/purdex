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

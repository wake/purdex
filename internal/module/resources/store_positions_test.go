package resourcesmod

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/resources"
)

// amendmentColumns are what amendment A adds to resource_leases.
var amendmentColumns = []string{"position_id", "mode", "command", "cwd", "queued_reason", "offered_at", "confirmed_at", "notice"}

// A database from before amendment A (the P2a schema) opens, gets the new
// columns and the positions table, and keeps its rows readable as wait-mode.
func TestStore_MigratesAPreAmendmentDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	s, err := openLeaseStore(path)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	// Turn it back into the P2a schema.
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = old.Exec(`DROP TABLE resource_positions; DROP INDEX IF EXISTS resource_leases_position;`)
	require.NoError(t, err)
	for _, c := range amendmentColumns {
		_, err = old.Exec("ALTER TABLE resource_leases DROP COLUMN " + c)
		require.NoError(t, err, c)
	}
	_, err = old.Exec(`INSERT INTO resource_leases (id, client_id, state, weight, holder_pid, scope, created_at, deadline_at, lease_until, granted_at, dec_path)
		VALUES ('old', 'c-old', 'held', 35, 7, 'process', 1000, 2000, 3000, 1500, 'immediate')`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	s, err = openLeaseStore(path)
	require.NoError(t, err)
	defer s.Close()
	have, err := columnSet(s.db, "resource_leases")
	require.NoError(t, err)
	for _, c := range amendmentColumns {
		assert.True(t, have[c], c)
	}
	posCols, err := columnSet(s.db, "resource_positions")
	require.NoError(t, err)
	assert.True(t, posCols["session_id"] && posCols["window_until"] && posCols["notice_kind"])
	r, ok, err := s.Get("old")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "", r.PositionID)
	assert.Equal(t, "", r.Mode, "an old row reads as wait mode")
	live, err := s.LivePositions()
	require.NoError(t, err)
	assert.Empty(t, live)
}

func TestStore_UniqueLivePositionPerSession(t *testing.T) {
	s, err := openLeaseStore(":memory:")
	require.NoError(t, err)
	defer s.Close()
	require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000001", SessionID: "s1", State: resources.PosQueued, CreatedAt: 1}))
	err = s.insertPosition(positionRow{ID: "pos_00000002", SessionID: "s1", State: resources.PosQueued, CreatedAt: 2})
	require.Error(t, err, "two live positions for one session conflict")
	// Another session is fine.
	require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000003", SessionID: "s2", State: resources.PosQueued, CreatedAt: 3}))
	// After the first ends, the session gets a new one.
	_, err = s.db.Exec(`UPDATE resource_positions SET state='ended' WHERE id='pos_00000001'`)
	require.NoError(t, err)
	require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000004", SessionID: "s1", State: resources.PosQueued, CreatedAt: 4}))

	got, ok, err := s.LivePositionOf("s1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "pos_00000004", got.ID)
	live, err := s.LivePositions()
	require.NoError(t, err)
	require.Len(t, live, 2)
	assert.Equal(t, "pos_00000003", live[0].ID, "oldest first")
	byID, ok, err := s.PositionByID("pos_00000003")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "s2", byID.SessionID)
	_, ok, err = s.PositionByID("pos_nope")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestStore_RowsOfAPosition(t *testing.T) {
	s, err := openLeaseStore(":memory:")
	require.NoError(t, err)
	defer s.Close()
	for i, id := range []string{"a", "b", "c"} {
		r := leaseRow{ID: id, ClientID: "client-" + id, Weight: 35, HolderPID: 7, Scope: resources.ScopeProcess,
			CreatedAt: int64(10 - i), Mode: resources.CallQueue, Command: "go test", Cwd: "/w"}
		if id != "c" {
			r.PositionID = "pos_00000001"
		}
		_, _, err := s.Create(r)
		require.NoError(t, err)
	}
	rows, err := s.RowsOf("pos_00000001")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "b", rows[0].ID, "oldest first")
	assert.Equal(t, resources.CallQueue, rows[0].Mode)
	assert.Equal(t, "go test", rows[0].Command)
	assert.Equal(t, "/w", rows[0].Cwd)
}

func TestStore_MaxGrantedAtSkipsOfferPath(t *testing.T) {
	s, err := openLeaseStore(":memory:")
	require.NoError(t, err)
	defer s.Close()
	for id, path := range map[string]string{"a": "immediate", "b": "offer"} {
		_, _, err := s.Create(leaseRow{ID: id, ClientID: "c-" + id, Weight: 1, HolderPID: 7, Scope: resources.ScopeProcess, CreatedAt: 1})
		require.NoError(t, err)
		_, err = s.db.Exec(`UPDATE resource_leases SET state='held', granted_at=?, dec_path=? WHERE id=?`,
			map[string]int64{"a": 100, "b": 900}[id], path, id)
		require.NoError(t, err)
	}
	got, err := s.MaxGrantedAt()
	require.NoError(t, err)
	assert.Equal(t, int64(100), got, "an offer-path grant is the reservation being used, not a new grant")
}

func TestStore_MaxOfferedAt(t *testing.T) {
	s, err := openLeaseStore(":memory:")
	require.NoError(t, err)
	defer s.Close()
	got, err := s.MaxOfferedAt()
	require.NoError(t, err)
	assert.Equal(t, int64(0), got)
	require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000001", SessionID: "s1", State: resources.PosOffered, CreatedAt: 1, OfferedAt: 700}))
	require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000002", SessionID: "s2", State: resources.PosQueued, CreatedAt: 2, OfferedAt: 300}))
	got, err = s.MaxOfferedAt()
	require.NoError(t, err)
	assert.Equal(t, int64(700), got)
}

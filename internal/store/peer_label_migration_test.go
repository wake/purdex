// internal/store/peer_label_migration_test.go
package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyPeerLabelsDDL is the peer_labels definition that shipped before Peer
// Address v3 D5. The UNIQUE on label is the whole point of these tests: it is
// what CREATE TABLE IF NOT EXISTS cannot remove from a DB that already exists.
const legacyPeerLabelsDDL = `
	CREATE TABLE peer_labels (
		session_id TEXT PRIMARY KEY,
		label      TEXT UNIQUE,
		rev        INTEGER NOT NULL,
		set_at     INTEGER NOT NULL
	)`

// peerLabelsDDL returns the CREATE TABLE text sqlite has on file for
// peer_labels, which is what the migration keys off.
func peerLabelsDDL(t *testing.T, db *sql.DB) string {
	t.Helper()
	var ddl string
	require.NoError(t, db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'peer_labels'`).Scan(&ddl))
	return ddl
}

// seedLegacyMetaDB writes a meta.db carrying the pre-v3 peer_labels table
// with rows in it, then closes it — exactly the state of a machine that has
// run an older daemon.
func seedLegacyMetaDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(legacyPeerLabelsDDL)
	require.NoError(t, err)
	_, err = db.Exec(`
		CREATE TABLE peer_label_seq (
			id  INTEGER PRIMARY KEY CHECK (id = 1),
			rev INTEGER NOT NULL
		)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO peer_label_seq (id, rev) VALUES (1, 2)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO peer_labels (session_id, label, rev, set_at) VALUES
		('sid-a', 'purdex-tester', 1, 1000),
		('sid-b', NULL,            2, 2000)`)
	require.NoError(t, err)
}

// TestMigrateMetaDB_DropsLegacyPeerLabelsUnique is the deployment regression
// test: on a DB that already has the UNIQUE, opening the store must rebuild
// the table so that two sessions can hold one label (D5). Before the
// migration existed, the second claim failed with a UNIQUE constraint error
// and the route answered 503 instead of 200 + warning.
func TestMigrateMetaDB_DropsLegacyPeerLabelsUnique(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")
	seedLegacyMetaDB(t, path)

	ms, err := OpenMeta(path)
	require.NoError(t, err)
	defer ms.Close()

	assert.NotContains(t, peerLabelsDDL(t, ms.db), "UNIQUE",
		"the rebuilt table must not carry the UNIQUE any more")

	// (a) every row survived the rebuild, values intact.
	ls := ms.PeerLabels()
	rows, err := ls.Snapshot()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, PeerLabel{SessionID: "sid-a", Label: "purdex-tester", Rev: 1, SetAt: time.UnixMilli(1000)}, rows[0])
	assert.Equal(t, PeerLabel{SessionID: "sid-b", Label: "", Rev: 2, SetAt: time.UnixMilli(2000)}, rows[1])

	// The host-wide revision counter is untouched by the rebuild.
	var seq int64
	require.NoError(t, ms.db.QueryRow(`SELECT rev FROM peer_label_seq WHERE id = 1`).Scan(&seq))
	assert.Equal(t, int64(2), seq)

	// (b) two different sessions may now hold the same label.
	_, err = ls.Claim("sid-b", "purdex-tester", time.UnixMilli(3000))
	require.NoError(t, err, "a duplicate label must no longer hit a UNIQUE constraint")
	rows, err = ls.Snapshot()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, r := range rows {
		assert.Equal(t, "purdex-tester", r.Label)
	}

	// session_id is still the primary key: re-claiming updates in place.
	_, err = ls.Claim("sid-b", "purdex-tester-2", time.UnixMilli(4000))
	require.NoError(t, err)
	rows, err = ls.Snapshot()
	require.NoError(t, err)
	require.Len(t, rows, 2, "ON CONFLICT(session_id) still upserts rather than inserting a duplicate row")
	assert.Equal(t, "purdex-tester-2", rows[1].Label)

	// Reopening a DB that was migrated once must not rebuild it again. Its
	// recorded DDL reads `CREATE TABLE "peer_labels"` (the rename quotes the
	// name and drops the IF NOT EXISTS), which is a different string from a
	// freshly created table's — so this is its own idempotence case, not a
	// repeat of the fresh-DB one below.
	migratedDDL := peerLabelsDDL(t, ms.db)
	require.NoError(t, ms.Close())
	reopened, err := OpenMeta(path)
	require.NoError(t, err)
	defer reopened.Close()
	assert.Equal(t, migratedDDL, peerLabelsDDL(t, reopened.db), "a migrated DB is rebuilt once, not every open")
	again, err := reopened.PeerLabels().Snapshot()
	require.NoError(t, err)
	assert.Equal(t, rows, again)
}

// TestMigrateMetaDB_PeerLabelsRebuildIsIdempotent pins the "leave it alone"
// half: a DB already on the new schema must come out unchanged, however many
// times the migration runs.
func TestMigrateMetaDB_PeerLabelsRebuildIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")

	ms, err := OpenMeta(path)
	require.NoError(t, err)
	_, err = ms.PeerLabels().Claim("sid-a", "purdex-tester", time.UnixMilli(1000))
	require.NoError(t, err)
	wantDDL := peerLabelsDDL(t, ms.db)
	wantRows, err := ms.PeerLabels().Snapshot()
	require.NoError(t, err)
	require.NoError(t, ms.Close())

	for i := 1; i <= 2; i++ {
		ms, err := OpenMeta(path)
		require.NoError(t, err)
		assert.Equal(t, wantDDL, peerLabelsDDL(t, ms.db), "reopen %d rewrote the schema", i)
		gotRows, err := ms.PeerLabels().Snapshot()
		require.NoError(t, err)
		assert.Equal(t, wantRows, gotRows, "reopen %d disturbed the rows", i)
		require.NoError(t, ms.Close())
	}
}

// TestMigrateMetaDB_PeerLabelsFreshDB covers the CREATE TABLE IF NOT EXISTS
// path: a brand new DB has nothing to rebuild and must still be usable.
func TestMigrateMetaDB_PeerLabelsFreshDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")

	ms, err := OpenMeta(path)
	require.NoError(t, err)
	defer ms.Close()

	assert.NotContains(t, peerLabelsDDL(t, ms.db), "UNIQUE")

	ls := ms.PeerLabels()
	_, err = ls.Claim("sid-a", "purdex-tester", time.UnixMilli(1000))
	require.NoError(t, err)
	_, err = ls.Claim("sid-b", "purdex-tester", time.UnixMilli(2000))
	require.NoError(t, err, "a fresh DB never had the UNIQUE to begin with")
	rows, err := ls.Snapshot()
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}

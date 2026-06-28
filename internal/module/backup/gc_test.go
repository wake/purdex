package backup

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

const day = int64(86400)

// insertSnap inserts a snapshot row directly (bypassing AppendSnapshot/GC) for
// deterministic GC fixtures.
func insertSnap(t *testing.T, s *BackupStore, store string, parent *int64, createdAt int64, manifest []ManifestEntry) int64 {
	t.Helper()
	mj, _ := json.Marshal(manifest)
	var p interface{}
	if parent != nil {
		p = *parent
	}
	res, err := s.db.Exec(
		`INSERT INTO backup_snapshots (store_id, parent_id, created_at, manifest) VALUES (?, ?, ?, ?)`,
		store, p, createdAt, string(mj),
	)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// insertBlob inserts a blob row directly with an explicit created_at.
func insertBlob(t *testing.T, s *BackupStore, content []byte, createdAt int64) string {
	t.Helper()
	h := sha256hex(content)
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO backup_blobs (hash, content, size, created_at) VALUES (?, ?, ?, ?)`,
		h, content, len(content), createdAt,
	)
	require.NoError(t, err)
	return h
}

func snapExists(t *testing.T, s *BackupStore, id int64) bool {
	t.Helper()
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM backup_snapshots WHERE id=?`, id).Scan(&one)
	return err == nil
}

func blobExists(t *testing.T, s *BackupStore, hash string) bool {
	t.Helper()
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM backup_blobs WHERE hash=?`, hash).Scan(&one)
	return err == nil
}

// Fixture 1: >100 snapshots ALL within 90 days → none deleted (the 90-day arm
// keeps them; count alone must NOT trigger deletion).
func TestGC_AllRecentNoDeletion(t *testing.T) {
	s := openTestBackupStore(t)
	now := int64(1_000_000_000)
	for i := 0; i < 150; i++ {
		insertSnap(t, s, storeID, nil, now-1000, nil)
	}
	require.NoError(t, s.GC(now))
	require.Equal(t, 150, snapCount(t, s, storeID))
}

// Fixture 2: >100 with some aged past 90 days → only rows in neither the
// latest-100 nor the 90-day window (and not an ancestor) are deleted.
func TestGC_AgedSurplusDeleted(t *testing.T) {
	s := openTestBackupStore(t)
	now := int64(1_000_000_000)
	var agedIDs []int64
	// ids 1..50: aged past 90 days.
	for i := 0; i < 50; i++ {
		agedIDs = append(agedIDs, insertSnap(t, s, storeID, nil, now-100*day, nil))
	}
	// ids 51..150: recent.
	for i := 0; i < 100; i++ {
		insertSnap(t, s, storeID, nil, now-1*day, nil)
	}
	require.NoError(t, s.GC(now))
	require.Equal(t, 100, snapCount(t, s, storeID))
	for _, id := range agedIDs {
		require.False(t, snapExists(t, s, id), "aged surplus %d should be deleted", id)
	}
}

// Fixture 3: ancestor closure — a survivor's aged ancestor (past both windows
// and out of the latest-100) is still retained; an aged non-ancestor is deleted.
func TestGC_AncestorClosure(t *testing.T) {
	s := openTestBackupStore(t)
	now := int64(1_000_000_000)

	d := insertSnap(t, s, storeID, nil, now-100*day, nil) // id 1: aged orphan → delete
	a := insertSnap(t, s, storeID, nil, now-100*day, nil) // id 2: aged ancestor → keep via closure
	b := insertSnap(t, s, storeID, i64(a), now-100*day, nil)
	// 100 recent fillers (ids 4..103) push d/a/b out of the latest-100.
	for i := 0; i < 100; i++ {
		insertSnap(t, s, storeID, nil, now-1*day, nil)
	}
	survivor := insertSnap(t, s, storeID, i64(b), now-1*day, nil) // recent, chains to b→a

	require.NoError(t, s.GC(now))

	require.False(t, snapExists(t, s, d), "aged non-ancestor should be deleted")
	require.True(t, snapExists(t, s, a), "aged ancestor should be retained via closure")
	require.True(t, snapExists(t, s, b), "aged ancestor should be retained via closure")
	require.True(t, snapExists(t, s, survivor))
}

// Fixture 4: blob grace — unreferenced + past grace → deleted; referenced
// (incl. shared) or within grace → retained.
func TestGC_BlobGrace(t *testing.T) {
	s := openTestBackupStore(t)
	now := int64(1_000_000_000)

	h1 := insertBlob(t, s, []byte("referenced-one"), now-2*3600) // referenced, past grace
	h2 := insertBlob(t, s, []byte("shared-blob"), now-2*3600)    // shared across 2 snaps
	h3 := insertBlob(t, s, []byte("orphan-old"), now-2*3600)     // unreferenced, past grace → delete
	h4 := insertBlob(t, s, []byte("orphan-new"), now-60)         // unreferenced, within grace → keep

	insertSnap(t, s, storeID, nil, now-1*day, []ManifestEntry{
		{Path: "a.txt", Kind: "file", Hash: h1, Size: 14},
		{Path: "b.txt", Kind: "file", Hash: h2, Size: 11},
	})
	insertSnap(t, s, storeID, nil, now-1*day, []ManifestEntry{
		{Path: "c.txt", Kind: "file", Hash: h2, Size: 11},
	})

	require.NoError(t, s.GC(now))

	require.True(t, blobExists(t, s, h1), "referenced blob kept despite past grace")
	require.True(t, blobExists(t, s, h2), "shared blob kept")
	require.False(t, blobExists(t, s, h3), "unreferenced past-grace blob deleted")
	require.True(t, blobExists(t, s, h4), "unreferenced within-grace blob kept")
}

// GC also runs inside the append transaction: an unreferenced past-grace blob is
// reaped by a normal AppendSnapshot.
func TestGC_RunsInAppendTx(t *testing.T) {
	s := openTestBackupStore(t)
	now := int64(1_000_000_000)
	s.now = func() int64 { return now }

	orphan := insertBlob(t, s, []byte("orphan-old"), now-2*3600) // past grace, unreferenced

	_, err := s.AppendSnapshot(AppendRequest{
		StoreID: storeID, Trigger: "auto",
		Manifest: []ManifestEntry{fileEntry(t, s, "a.txt", []byte("hello"))},
	})
	require.NoError(t, err)
	require.False(t, blobExists(t, s, orphan), "append-tx GC should reap the orphan blob")
}

package backup

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func i64(v int64) *int64 { return &v }

// fileEntry stores content as a blob and returns a matching file manifest entry.
func fileEntry(t *testing.T, s *BackupStore, path string, content []byte) ManifestEntry {
	t.Helper()
	h := sha256hex(content)
	require.NoError(t, s.PutBlob(h, content))
	return ManifestEntry{Path: path, Kind: "file", Hash: h, Size: int64(len(content))}
}

func snapCount(t *testing.T, s *BackupStore, storeID string) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM backup_snapshots WHERE store_id=?`, storeID).Scan(&n))
	return n
}

const storeID = "inapp:buffer"

func TestAppendSnapshot_BasicAndFork(t *testing.T) {
	s := openTestBackupStore(t)

	r1, err := s.AppendSnapshot(AppendRequest{
		StoreID: storeID, Device: "devA", Trigger: "auto",
		Manifest: []ManifestEntry{fileEntry(t, s, "a.txt", []byte("hello"))},
	})
	require.NoError(t, err)
	require.True(t, r1.Written)
	require.False(t, r1.IsFork) // parent nil, no prior head
	require.Greater(t, r1.SnapshotID, int64(0))
	require.Equal(t, r1.SnapshotID, r1.CurrentHeadID)

	// parent == head, differing content → not a fork.
	r2, err := s.AppendSnapshot(AppendRequest{
		StoreID: storeID, Device: "devA", ParentID: i64(r1.SnapshotID), Trigger: "auto",
		Manifest: []ManifestEntry{fileEntry(t, s, "a.txt", []byte("world!!"))},
	})
	require.NoError(t, err)
	require.True(t, r2.Written)
	require.False(t, r2.IsFork)

	// stale parent (r1, head now r2), differing content → fork.
	r3, err := s.AppendSnapshot(AppendRequest{
		StoreID: storeID, Device: "devB", ParentID: i64(r1.SnapshotID), Trigger: "manual",
		Manifest: []ManifestEntry{fileEntry(t, s, "a.txt", []byte("forked content"))},
	})
	require.NoError(t, err)
	require.True(t, r3.Written)
	require.True(t, r3.IsFork)

	require.Equal(t, 3, snapCount(t, s, storeID))

	// parent_id chain preserved, nothing overwritten.
	var parent int64
	require.NoError(t, s.db.QueryRow(`SELECT parent_id FROM backup_snapshots WHERE id=?`, r3.SnapshotID).Scan(&parent))
	require.Equal(t, r1.SnapshotID, parent)
}

func TestAppendSnapshot_NoOpSuppression(t *testing.T) {
	s := openTestBackupStore(t)
	entry := fileEntry(t, s, "a.txt", []byte("hello"))

	r1, err := s.AppendSnapshot(AppendRequest{StoreID: storeID, Trigger: "auto", Manifest: []ManifestEntry{entry}})
	require.NoError(t, err)
	require.True(t, r1.Written)

	// trigger:auto, identical content, parent == head → no-op.
	r2, err := s.AppendSnapshot(AppendRequest{StoreID: storeID, ParentID: i64(r1.SnapshotID), Trigger: "auto", Manifest: []ManifestEntry{entry}})
	require.NoError(t, err)
	require.False(t, r2.Written)
	require.Equal(t, r1.SnapshotID, r2.SnapshotID)
	require.Equal(t, r1.SnapshotID, r2.CurrentHeadID)

	// trigger:pre-restore, identical content, stale parent (nil, cross-device) → no-op.
	r3, err := s.AppendSnapshot(AppendRequest{StoreID: storeID, ParentID: nil, Trigger: "pre-restore", Manifest: []ManifestEntry{entry}})
	require.NoError(t, err)
	require.False(t, r3.Written)
	require.Equal(t, r1.SnapshotID, r3.SnapshotID)

	require.Equal(t, 1, snapCount(t, s, storeID))
}

func TestAppendSnapshot_MissingBlob(t *testing.T) {
	s := openTestBackupStore(t)
	// File entry whose blob was never uploaded.
	entry := ManifestEntry{Path: "a.txt", Kind: "file", Hash: sha256hex([]byte("nope")), Size: 4}

	_, err := s.AppendSnapshot(AppendRequest{StoreID: storeID, Trigger: "auto", Manifest: []ManifestEntry{entry}})
	require.ErrorIs(t, err, ErrBlobMissing)
	require.Equal(t, 0, snapCount(t, s, storeID))
}

func TestAppendSnapshot_ValidationFailures(t *testing.T) {
	s := openTestBackupStore(t)

	// Unsorted manifest.
	_, err := s.AppendSnapshot(AppendRequest{StoreID: storeID, Manifest: []ManifestEntry{
		{Path: "b", Kind: "dir"}, {Path: "a", Kind: "dir"},
	}})
	require.ErrorIs(t, err, ErrInvalidRequest)

	// Nonexistent parent.
	_, err = s.AppendSnapshot(AppendRequest{StoreID: storeID, ParentID: i64(9999), Manifest: []ManifestEntry{{Path: "a", Kind: "dir"}}})
	require.ErrorIs(t, err, ErrInvalidRequest)

	// Cross-store parent.
	other, err := s.AppendSnapshot(AppendRequest{StoreID: "other:store", Manifest: []ManifestEntry{{Path: "a", Kind: "dir"}}})
	require.NoError(t, err)
	_, err = s.AppendSnapshot(AppendRequest{StoreID: storeID, ParentID: i64(other.SnapshotID), Manifest: []ManifestEntry{{Path: "a", Kind: "dir"}}})
	require.ErrorIs(t, err, ErrInvalidRequest)

	// Size mismatch.
	content := []byte("hello")
	h := sha256hex(content)
	require.NoError(t, s.PutBlob(h, content))
	_, err = s.AppendSnapshot(AppendRequest{StoreID: storeID, Manifest: []ManifestEntry{
		{Path: "a.txt", Kind: "file", Hash: h, Size: 999},
	}})
	require.ErrorIs(t, err, ErrInvalidRequest)

	require.Equal(t, 0, snapCount(t, s, storeID))
}

func TestAppendSnapshot_Serialisation(t *testing.T) {
	s := openTempBackupStore(t) // file-backed, MaxOpenConns(2)

	// Seed S1 (head=1) WITHOUT the barrier installed.
	seed, err := s.AppendSnapshot(AppendRequest{StoreID: storeID, Trigger: "auto", Manifest: []ManifestEntry{{Path: "d1", Kind: "dir"}}})
	require.NoError(t, err)

	// Barrier: only the first AppendSnapshot after install blocks after reading
	// head and before inserting.
	var once sync.Once
	reached := make(chan struct{})
	release := make(chan struct{})
	s.afterReadHead = func() {
		once.Do(func() {
			close(reached)
			<-release
		})
	}

	type out struct {
		r   AppendResult
		err error
	}
	done1 := make(chan out, 1)
	go func() {
		r, e := s.AppendSnapshot(AppendRequest{StoreID: storeID, ParentID: i64(seed.SnapshotID), Trigger: "auto", Manifest: []ManifestEntry{{Path: "d2", Kind: "dir"}}})
		done1 <- out{r, e}
	}()

	<-reached // writer-1 holds the RESERVED lock, blocked before insert.

	done2 := make(chan out, 1)
	go func() {
		r, e := s.AppendSnapshot(AppendRequest{StoreID: storeID, ParentID: i64(seed.SnapshotID), Trigger: "auto", Manifest: []ManifestEntry{{Path: "d3", Kind: "dir"}}})
		done2 <- out{r, e}
	}()

	// Give writer-2 time to reach BEGIN IMMEDIATE and block on the write lock,
	// then release writer-1 (well within busy_timeout).
	time.Sleep(50 * time.Millisecond)
	close(release)

	o1 := <-done1
	o2 := <-done2
	require.NoError(t, o1.err)
	require.NoError(t, o2.err) // no SQLITE_BUSY surfaced

	// writer-1 saw head==seed → not a fork; writer-2 blocked, then observed the
	// advanced head (writer-1's row) → fork. They must not both be is_fork=false.
	require.False(t, o1.r.IsFork)
	require.True(t, o2.r.IsFork)
	require.NotEqual(t, o1.r.SnapshotID, o2.r.SnapshotID)
	require.Equal(t, 3, snapCount(t, s, storeID))
}

func TestHandleSnapshot(t *testing.T) {
	m, mux := newTestModuleMux(t)

	post := func(body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/backup/snapshot", bytes.NewReader(b))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	content := []byte("hello")
	h := sha256hex(content)
	require.NoError(t, m.store.PutBlob(h, content))

	// Valid → 200.
	rec := post(map[string]any{
		"storeId": storeID, "device": "devA", "trigger": "auto",
		"manifest": []ManifestEntry{{Path: "a.txt", Kind: "file", Hash: h, Size: int64(len(content))}},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		SnapshotID    int64 `json:"snapshotId"`
		IsFork        bool  `json:"isFork"`
		CurrentHeadID int64 `json:"currentHeadId"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Greater(t, resp.SnapshotID, int64(0))
	require.Equal(t, resp.SnapshotID, resp.CurrentHeadID)

	// Missing blob → 409.
	rec = post(map[string]any{
		"storeId": storeID, "trigger": "auto",
		"manifest": []ManifestEntry{{Path: "b.txt", Kind: "file", Hash: sha256hex([]byte("absent")), Size: 6}},
	})
	require.Equal(t, http.StatusConflict, rec.Code)

	// Invalid (unsorted) → 400.
	rec = post(map[string]any{
		"storeId":  storeID,
		"manifest": []ManifestEntry{{Path: "z", Kind: "dir"}, {Path: "a", Kind: "dir"}},
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Over item cap → 413.
	big := make([]ManifestEntry, manifestItemCap+1)
	rec = post(map[string]any{"storeId": storeID, "manifest": big})
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

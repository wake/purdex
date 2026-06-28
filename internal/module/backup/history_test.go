package backup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestHistoryAndGetSnapshot(t *testing.T) {
	m, mux := newTestModuleMux(t)
	s := m.store

	// S1: 2 files (sizes 3 + 5) + 1 dir.
	s1, err := s.AppendSnapshot(AppendRequest{
		StoreID: storeID, Device: "devA", Trigger: "auto",
		Manifest: []ManifestEntry{
			fileEntry(t, s, "b.txt", []byte("bbb")),
			{Path: "d", Kind: "dir"},
			fileEntry(t, s, "d/a.txt", []byte("aaaaa")),
		},
	})
	require.NoError(t, err)

	// S2: 1 empty dir only.
	s2, err := s.AppendSnapshot(AppendRequest{
		StoreID: storeID, Device: "devB", ParentID: i64(s1.SnapshotID), Trigger: "manual",
		Manifest: []ManifestEntry{{Path: "x", Kind: "dir"}},
	})
	require.NoError(t, err)

	// history newest-first, summarised, no blob bytes.
	req := httptest.NewRequest("GET", "/api/backup/history?storeId="+storeID, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "manifest")

	var hist []struct {
		ID        int64  `json:"id"`
		Device    string `json:"device"`
		IsFork    bool   `json:"isFork"`
		Trigger   string `json:"trigger"`
		FileCount int    `json:"fileCount"`
		DirCount  int    `json:"dirCount"`
		TotalSize int64  `json:"totalSize"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&hist))
	require.Len(t, hist, 2)
	require.Equal(t, s2.SnapshotID, hist[0].ID) // newest first
	require.Equal(t, s1.SnapshotID, hist[1].ID)
	require.Equal(t, 0, hist[0].FileCount)
	require.Equal(t, 1, hist[0].DirCount)
	require.Equal(t, int64(0), hist[0].TotalSize)
	require.Equal(t, 2, hist[1].FileCount)
	require.Equal(t, 1, hist[1].DirCount)
	require.Equal(t, int64(8), hist[1].TotalSize)

	// get one snapshot: full manifest incl. dir entries.
	req = httptest.NewRequest("GET", "/api/backup/snapshot/"+itoa(s1.SnapshotID), nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var detail struct {
		ID       int64           `json:"id"`
		StoreID  string          `json:"storeId"`
		Manifest []ManifestEntry `json:"manifest"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&detail))
	require.Equal(t, s1.SnapshotID, detail.ID)
	require.Equal(t, storeID, detail.StoreID)
	require.Len(t, detail.Manifest, 3)
	var hasDir bool
	for _, e := range detail.Manifest {
		if e.Path == "d" && e.Kind == "dir" {
			hasDir = true
		}
	}
	require.True(t, hasDir, "dir entry must round-trip")

	// nonexistent → 404.
	req = httptest.NewRequest("GET", "/api/backup/snapshot/999999", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

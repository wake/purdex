package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// newTestModuleMux builds a BackupModule backed by an in-memory store plus a
// mux with its routes registered (so PathValue routing is exercised).
func newTestModuleMux(t *testing.T) (*BackupModule, *http.ServeMux) {
	t.Helper()
	m := &BackupModule{store: openTestBackupStore(t)}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	return m, mux
}

func TestPutBlob_Store(t *testing.T) {
	s := openTestBackupStore(t)
	content := []byte("hello world")
	h := sha256hex(content)

	// Mismatched hash is rejected, nothing stored.
	err := s.PutBlob("0000000000000000000000000000000000000000000000000000000000000000", content)
	require.ErrorIs(t, err, ErrHashMismatch)
	miss, err := s.MissingBlobs([]string{h})
	require.NoError(t, err)
	require.Equal(t, []string{h}, miss)

	// Correct hash stores once.
	require.NoError(t, s.PutBlob(h, content))
	// Re-put same hash: idempotent, no duplicate row, size correct.
	require.NoError(t, s.PutBlob(h, content))

	var count, size int64
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*), MAX(size) FROM backup_blobs WHERE hash=?`, h).Scan(&count, &size))
	require.Equal(t, int64(1), count)
	require.Equal(t, int64(len(content)), size)

	// GetBlob is byte-identical.
	got, found, err := s.GetBlob(h)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, content, got)

	// Absent blob.
	_, found, err = s.GetBlob(sha256hex([]byte("absent")))
	require.NoError(t, err)
	require.False(t, found)
}

func TestMissingBlobs_Subset(t *testing.T) {
	s := openTestBackupStore(t)
	a := []byte("a")
	b := []byte("b")
	ha, hb := sha256hex(a), sha256hex(b)
	hc := sha256hex([]byte("c"))
	require.NoError(t, s.PutBlob(ha, a))
	require.NoError(t, s.PutBlob(hb, b))

	miss, err := s.MissingBlobs([]string{ha, hb, hc})
	require.NoError(t, err)
	require.Equal(t, []string{hc}, miss)
}

func TestHandlePutBlob(t *testing.T) {
	_, mux := newTestModuleMux(t)
	content := []byte("payload bytes")
	h := sha256hex(content)

	// Non-hex hash → 400.
	req := httptest.NewRequest("PUT", "/api/backup/blob/NOTHEX", bytes.NewReader(content))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Body hash mismatch → 400, nothing stored.
	wrong := sha256hex([]byte("different"))
	req = httptest.NewRequest("PUT", "/api/backup/blob/"+wrong, bytes.NewReader(content))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	req = httptest.NewRequest("GET", "/api/backup/blob/"+wrong, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// Matching hash → 204, stored.
	req = httptest.NewRequest("PUT", "/api/backup/blob/"+h, bytes.NewReader(content))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	// Re-PUT → 204, no duplicate.
	req = httptest.NewRequest("PUT", "/api/backup/blob/"+h, bytes.NewReader(content))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	// GET → exact bytes.
	req = httptest.NewRequest("GET", "/api/backup/blob/"+h, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, content, rec.Body.Bytes())
}

func TestHandlePutBlob_TooLarge(t *testing.T) {
	_, mux := newTestModuleMux(t)
	big := bytes.Repeat([]byte("x"), blobSizeCap+1)
	h := sha256hex(big)

	req := httptest.NewRequest("PUT", "/api/backup/blob/"+h, bytes.NewReader(big))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	// Nothing stored.
	req = httptest.NewRequest("GET", "/api/backup/blob/"+h, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleMissing(t *testing.T) {
	m, mux := newTestModuleMux(t)
	a := []byte("a")
	ha := sha256hex(a)
	hb := sha256hex([]byte("b"))
	require.NoError(t, m.store.PutBlob(ha, a))

	body, _ := json.Marshal(map[string][]string{"hashes": {ha, hb}})
	req := httptest.NewRequest("POST", "/api/backup/missing", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Missing []string `json:"missing"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, []string{hb}, resp.Missing)
}

func TestHandleMissing_OverCap(t *testing.T) {
	_, mux := newTestModuleMux(t)
	hashes := make([]string, missingHashesCap+1)
	for i := range hashes {
		hashes[i] = strings.Repeat("a", 64)
	}
	body, _ := json.Marshal(map[string][]string{"hashes": hashes})
	req := httptest.NewRequest("POST", "/api/backup/missing", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

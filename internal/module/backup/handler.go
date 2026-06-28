package backup

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const (
	// blobSizeCap is the maximum raw blob upload size (> the 25 MB single-file
	// In-App cap). Uploads read cap+1 and return 413 on overflow — never a
	// silently truncated accept.
	blobSizeCap = 30 << 20 // 30 MB
	// missingHashesCap caps the number of hashes in a negotiation request.
	missingHashesCap = 10000
	// manifestItemCap caps the number of entries in a snapshot manifest.
	manifestItemCap = 50000
	// jsonBodyCap bounds JSON request bodies (negotiation / snapshot).
	jsonBodyCap = 64 << 20 // 64 MB
)

// handlePutBlob stores one blob: PUT /api/backup/blob/{hash}.
func (m *BackupModule) handlePutBlob(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !isHex64(hash) {
		http.Error(w, "hash must be 64-char lowercase hex", http.StatusBadRequest)
		return
	}

	// Read cap+1 so an over-cap body is detected rather than silently truncated.
	body, err := io.ReadAll(io.LimitReader(r.Body, blobSizeCap+1))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if len(body) > blobSizeCap {
		http.Error(w, "blob too large", http.StatusRequestEntityTooLarge)
		return
	}

	if err := m.store.PutBlob(hash, body); err != nil {
		if errors.Is(err, ErrHashMismatch) {
			http.Error(w, "content hash mismatch", http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetBlob returns one blob's raw bytes: GET /api/backup/blob/{hash}.
func (m *BackupModule) handleGetBlob(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !isHex64(hash) {
		http.Error(w, "hash must be 64-char lowercase hex", http.StatusBadRequest)
		return
	}

	content, found, err := m.store.GetBlob(hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "blob not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(content)
}

// handleSnapshot appends a snapshot: POST /api/backup/snapshot.
func (m *BackupModule) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StoreID  string          `json:"storeId"`
		Device   string          `json:"device"`
		ParentID *int64          `json:"parentId"`
		Trigger  string          `json:"trigger"`
		Manifest []ManifestEntry `json:"manifest"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, jsonBodyCap)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.StoreID == "" {
		http.Error(w, "storeId required", http.StatusBadRequest)
		return
	}
	// Manifest item-count cap is a handler-level input guard (413), checked
	// before the store call (§4.2 (h)).
	if len(req.Manifest) > manifestItemCap {
		http.Error(w, "manifest too large", http.StatusRequestEntityTooLarge)
		return
	}

	result, err := m.store.AppendSnapshot(AppendRequest{
		StoreID:  req.StoreID,
		Device:   req.Device,
		ParentID: req.ParentID,
		Trigger:  req.Trigger,
		Manifest: req.Manifest,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrBlobMissing):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, ErrInvalidRequest):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"snapshotId":    result.SnapshotID,
		"isFork":        result.IsFork,
		"currentHeadId": result.CurrentHeadID,
	})
}

// handleMissing returns the subset of requested hashes the daemon lacks:
// POST /api/backup/missing.
func (m *BackupModule) handleMissing(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hashes []string `json:"hashes"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, jsonBodyCap)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if len(req.Hashes) > missingHashesCap {
		http.Error(w, "too many hashes", http.StatusRequestEntityTooLarge)
		return
	}

	missing, err := m.store.MissingBlobs(req.Hashes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]string{"missing": missing})
}

package devicestate

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// putBodyCap bounds PUT request bodies. Reads cap+1 so an over-cap body is
// rejected with 413 rather than silently truncated.
const putBodyCap = 5 << 20 // 5 MB

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[devicestate] encode response: %v", err)
	}
}

// handlePut stores one device's state: PUT /api/device-state/{clientId}.
func (m *Module) handlePut(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("clientId")
	if err := validateClientID(clientID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, putBodyCap+1))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if len(body) > putBodyCap {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}

	var req struct {
		DeviceName string          `json:"deviceName"`
		AppVersion string          `json:"appVersion"`
		CapturedAt int64           `json:"capturedAt"`
		Payload    json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	name, err := validateDeviceName(req.DeviceName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateAppVersion(req.AppVersion); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateCapturedAt(req.CapturedAt); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	wsCount, tabCount, err := parsePayload(req.Payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	stored, err := m.store.Upsert(Record{
		ClientID:       clientID,
		DeviceName:     name,
		AppVersion:     req.AppVersion,
		CapturedAt:     req.CapturedAt,
		WorkspaceCount: wsCount,
		TabCount:       tabCount,
		Payload:        req.Payload,
	})
	if err != nil {
		log.Printf("[devicestate] upsert %s: %v", clientID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"stored": stored})
}

// handleList returns all device summaries: GET /api/device-state.
func (m *Module) handleList(w http.ResponseWriter, _ *http.Request) {
	records, err := m.store.List()
	if err != nil {
		log.Printf("[devicestate] list: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if records == nil {
		records = []Record{}
	}
	writeJSON(w, records)
}

// handleGet returns one device's full record: GET /api/device-state/{clientId}.
func (m *Module) handleGet(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("clientId")
	if err := validateClientID(clientID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rec, found, err := m.store.Get(clientID)
	if err != nil {
		log.Printf("[devicestate] get %s: %v", clientID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "device state not found", http.StatusNotFound)
		return
	}
	writeJSON(w, rec)
}

// handleDelete removes one device's record: DELETE /api/device-state/{clientId}.
func (m *Module) handleDelete(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("clientId")
	if err := validateClientID(clientID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := m.store.Delete(clientID); err != nil {
		log.Printf("[devicestate] delete %s: %v", clientID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

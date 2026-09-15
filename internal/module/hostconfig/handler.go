package hostconfig

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
)

// bodyCap bounds request bodies. Reads cap+1 so an over-cap body is 413.
const bodyCap = 1 << 20

type collection struct {
	Items    json.RawMessage `json:"items"`
	Revision int64           `json:"revision"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[hostconfig] encode response: %v", err)
	}
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, bodyCap+1))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return nil, false
	}
	if len(body) > bodyCap {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	return body, true
}

// entryItems returns the stored JSON, or the empty value for a never-written key.
func entryItems(e Entry, empty string) json.RawMessage {
	if e.Value == nil {
		return json.RawMessage(empty)
	}
	return e.Value
}

func emptyFor(key string) string {
	if key == KeyResumeTemplates {
		return `{}`
	}
	return `[]`
}

// handleGet returns all collections: GET /api/hostconfig.
func (m *Module) handleGet(w http.ResponseWriter, _ *http.Request) {
	out := map[string]collection{}
	for field, key := range map[string]string{"projects": KeyProjects, "commands": KeyCommands, "resumeTemplates": KeyResumeTemplates} {
		e, err := m.store.Get(key)
		if err != nil {
			log.Printf("[hostconfig] get %s: %v", key, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out[field] = collection{Items: entryItems(e, emptyFor(key)), Revision: e.Revision}
	}
	writeJSON(w, http.StatusOK, out)
}

// putHandler replaces one collection guarded by baseRevision.
func (m *Module) putHandler(key string, normalize func([]byte) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := readBody(w, r)
		if !ok {
			return
		}
		if err := rejectDuplicateKeys(body); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		var req struct {
			Items        json.RawMessage `json:"items"`
			BaseRevision *int64          `json:"baseRevision"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.Items == nil || req.BaseRevision == nil || *req.BaseRevision < 0 {
			http.Error(w, "items and baseRevision (>= 0) are required", http.StatusBadRequest)
			return
		}
		// Normalize inside the store's CAS so a stale revision yields 409 with
		// the server copy even when the payload is invalid.
		var marshalErr error
		entry, stored, err := m.store.Put(key, *req.BaseRevision, func() (json.RawMessage, error) {
			normalized, err := normalize(req.Items)
			if err != nil {
				return nil, err
			}
			value, err := json.Marshal(normalized)
			if err != nil {
				marshalErr = err
				return nil, err
			}
			return value, nil
		})
		var ve *ValidationError
		if err != nil && marshalErr == nil && errors.As(err, &ve) {
			http.Error(w, ve.Error(), http.StatusBadRequest)
			return
		}
		if err != nil {
			log.Printf("[hostconfig] put %s: %v", key, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		status := http.StatusOK
		if !stored {
			status = http.StatusConflict
		}
		writeJSON(w, status, collection{Items: entryItems(entry, emptyFor(key)), Revision: entry.Revision})
	}
}

// handleCheckPath classifies a project path: POST /api/hostconfig/check-path.
func (m *Module) handleCheckPath(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	if err := rejectDuplicateKeys(body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	res, err := checkPath(req.Path, m.home)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

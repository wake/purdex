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

// collection is one collection as the GET, a PUT and a 409 answer it. The
// markers (#1889) are omitempty, so a clean collection reads as before:
// Invalid says the stored value is not this collection at all (Items is its
// empty value), Dropped how many rows were left out and why.
type collection struct {
	Items    any      `json:"items"`
	Revision int64    `json:"revision"`
	Invalid  bool     `json:"invalid,omitempty"`
	Dropped  *Dropped `json:"dropped,omitempty"`
}

// writeJSON marshals v before writing the header, so a value that does not
// marshal is a 500 rather than the status with an empty body. The body ends
// in a newline, as json.Encoder writes it.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("[hostconfig] encode response: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(body, '\n')); err != nil {
		log.Printf("[hostconfig] write response: %v", err)
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

// collectionOf is a stored entry as op ("get" or "put") answers it: a
// never-written key its empty value; a stored one only what a PUT would
// accept, row by row, with the markers (#1889). Something left out is
// logged once, naming no more of a row than its reason does.
func collectionOf(op, key string, e Entry) collection {
	if e.Value == nil {
		return collection{Items: json.RawMessage(emptyFor(key)), Revision: e.Revision}
	}
	r := readers[key](e.Value)
	c := collection{Items: r.items, Revision: e.Revision, Invalid: r.invalid != nil, Dropped: r.dropped}
	switch {
	case r.invalid != nil:
		log.Printf("[hostconfig] %s %s: invalid: %s", op, key, clipReason(r.invalid.Error()))
	case r.dropped != nil:
		log.Printf("[hostconfig] %s %s: dropped %d (%s)", op, key, r.dropped.Count, r.dropped.Reasons[0])
	}
	return c
}

func emptyFor(key string) string {
	switch key {
	case KeyResumeTemplates:
		return `{}`
	case KeyRelay:
		return relaySwitchesJSON
	case KeyTeam:
		return teamSettingsJSON
	case KeyResources:
		return resourcesDefaultJSON
	case KeyRelayQuota:
		return relayQuotaJSON
	}
	return `[]`
}

// handleGet returns all collections: GET /api/hostconfig.
func (m *Module) handleGet(w http.ResponseWriter, _ *http.Request) {
	out := map[string]collection{}
	for field, key := range map[string]string{
		"projects":        KeyProjects,
		"commands":        KeyCommands,
		"resumeTemplates": KeyResumeTemplates,
		"quickReplies":    KeyQuickReplies,
		"relay":           KeyRelay,
		"team":            KeyTeam,
		"resources":       KeyResources,
		"relayQuota":      KeyRelayQuota,
	} {
		e, err := m.store.Get(key)
		if err != nil {
			log.Printf("[hostconfig] get %s: %v", key, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out[field] = collectionOf("get", key, e)
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
		// A stored answer is already normalised; a 409's current may be a
		// row edited by hand, and is read like the GET reads it.
		writeJSON(w, status, collectionOf("put", key, entry))
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

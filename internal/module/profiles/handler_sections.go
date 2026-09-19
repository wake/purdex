package profiles

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
)

// putBodyCap bounds a section PUT body. The spec caps the *payload* at
// PayloadCap, so the body is allowed that plus room for its envelope — a legal
// 5 MiB payload must not be refused for the few hundred bytes around it. The
// payload itself is then held to PayloadCap by validatePayload.
const putBodyCap = PayloadCap + 64<<10

// profileEventType is the host-event type of a section change.
const profileEventType = "profile"

// profileEvent is the value of a "profile" host event (spec §4.6,
// Notification). The key names are a wire contract with the client (P2b), hence
// the explicit tags. It never carries the payload: a client fetches the section
// rather than trusting one off the wire.
type profileEvent struct {
	ProfileID      string `json:"profileId"`
	Section        string `json:"section"`
	Rev            int64  `json:"rev"`
	Hash           string `json:"hash"`
	WriterClientID string `json:"writerClientId"`
	Deleted        bool   `json:"deleted,omitempty"`
}

// conflictBody is the 409 of a stale write whose content differs. Against a
// section that is absent — never existed, or a tombstone — there is no SOT
// side to offer: Rev is 0 and Hash/Payload are omitted altogether, which the
// client reads as "deleted under you" (§4.6.3).
type conflictBody struct {
	Reason  string          `json:"reason"`
	Rev     int64           `json:"rev"`
	Hash    string          `json:"hash,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// schemaBody is the 409 of a write refused by the shape gate (§4.5).
type schemaBody struct {
	Reason      string `json:"reason"`
	Fingerprint string `json:"fingerprint"`
	Ordinal     int    `json:"ordinal"`
}

// announce broadcasts one applied section write. Callers must only call it for
// a write that changed a row (PutResult.Changed): a spurious event would make
// every client re-fetch a section it already has.
func (m *Module) announce(ev profileEvent) {
	if m.broadcast == nil {
		return
	}
	value, err := json.Marshal(ev)
	if err != nil {
		log.Printf("[profiles] encode event: %v", err)
		return
	}
	m.broadcast(profileEventType, string(value))
}

// pathSection returns the validated {id} and {section} path values, or writes 400.
func pathSection(w http.ResponseWriter, r *http.Request) (profileID, section string, ok bool) {
	profileID, ok = pathProfileID(w, r)
	if !ok {
		return "", "", false
	}
	section = r.PathValue("section")
	if err := validateSection(section); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", "", false
	}
	return profileID, section, true
}

// writeStoreError answers a PutSection / DeleteSection error.
func writeStoreError(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, ErrProfileNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrSectionContended):
		// Nothing was written and nothing is wrong with the request: the
		// section is being hammered by other writers. Tell the client when to
		// come back rather than calling it a server fault.
		w.Header().Set("Retry-After", "1")
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	default:
		internalError(w, what, err)
	}
}

// writeConflict answers 409 `reason:"conflict"` with the SOT side, if any.
func writeConflict(w http.ResponseWriter, res PutResult) {
	body := conflictBody{Reason: "conflict", Rev: res.Rev}
	if res.Current != nil {
		body.Hash = res.Current.Hash
		body.Payload = res.Current.Payload
	}
	writeJSONStatus(w, http.StatusConflict, body)
}

// handleGetProfile returns every live section with its payload, keyed by
// section name: GET /api/profiles/{id} (used by pull).
//
// The index and the sections are separate reads. A section deleted in between
// is simply left out, and one rewritten in between is returned as rewritten;
// the map is a set of individually consistent sections, not a snapshot — which
// is all the protocol asks for (cross-section writes are not atomic, §4.6.3).
func (m *Module) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathProfileID(w, r)
	if !ok {
		return
	}
	if err := m.store.requireProfile(id); err != nil {
		writeStoreError(w, "get profile "+id, err)
		return
	}
	index, err := m.store.ListSections(id)
	if err != nil {
		internalError(w, "list sections of "+id, err)
		return
	}
	sections := make(map[string]Section, len(index))
	for _, meta := range index {
		sec, found, err := m.store.GetSection(id, meta.Section)
		if err != nil {
			internalError(w, "get section "+id+"/"+meta.Section, err)
			return
		}
		if found {
			sections[meta.Section] = sec
		}
	}
	writeJSON(w, map[string]any{"sections": sections})
}

// handleGetSection returns one live section:
// GET /api/profiles/{id}/sections/{section}. A tombstone is a 404.
func (m *Module) handleGetSection(w http.ResponseWriter, r *http.Request) {
	id, section, ok := pathSection(w, r)
	if !ok {
		return
	}
	sec, found, err := m.store.GetSection(id, section)
	if err != nil {
		internalError(w, "get section "+id+"/"+section, err)
		return
	}
	if !found {
		http.Error(w, "section not found", http.StatusNotFound)
		return
	}
	writeJSON(w, sec)
}

// handlePutSection is the compare-and-set of spec §4.6:
// PUT /api/profiles/{id}/sections/{section}.
func (m *Module) handlePutSection(w http.ResponseWriter, r *http.Request) {
	id, section, ok := pathSection(w, r)
	if !ok {
		return
	}
	body, ok := readBody(w, r, putBodyCap)
	if !ok {
		return
	}

	// BaseRev and Ordinal are pointers so that leaving one out is a 400 rather
	// than a silent 0 — and baseRev 0 means "create", the one value a client
	// must never send by accident.
	var req struct {
		ClientID    string          `json:"clientId"`
		BaseRev     *int64          `json:"baseRev"`
		Hash        string          `json:"hash"`
		Fingerprint string          `json:"fingerprint"`
		Ordinal     *int64          `json:"ordinal"`
		Payload     json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.BaseRev == nil {
		http.Error(w, "baseRev is required", http.StatusBadRequest)
		return
	}
	if req.Ordinal == nil {
		http.Error(w, "ordinal is required", http.StatusBadRequest)
		return
	}
	for _, err := range []error{
		validateClientID(req.ClientID),
		validateBaseRev(*req.BaseRev),
		validateHash(req.Hash),
		validateFingerprint(req.Fingerprint),
		validateOrdinal(*req.Ordinal),
	} {
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := validatePayload(req.Payload); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrPayloadTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}

	res, err := m.store.PutSection(id, Section{
		Section:     section,
		Hash:        req.Hash,
		Fingerprint: req.Fingerprint,
		Ordinal:     int(*req.Ordinal),
		Payload:     req.Payload,
		Writer:      req.ClientID,
	}, *req.BaseRev)
	if err != nil {
		writeStoreError(w, "put section "+id+"/"+section, err)
		return
	}

	switch res.Outcome {
	case PutApplied:
		if res.Changed {
			m.announce(profileEvent{
				ProfileID: id, Section: section, Rev: res.Rev,
				Hash: req.Hash, WriterClientID: req.ClientID,
			})
		}
		writeJSON(w, map[string]any{"rev": res.Rev, "applied": true})
	case PutConverged:
		// Nothing was written, so nothing is announced.
		writeJSON(w, map[string]any{"rev": res.Rev, "applied": false})
	case PutConflict:
		writeConflict(w, res)
	case PutSchema:
		writeJSONStatus(w, http.StatusConflict, schemaBody{
			Reason: "schema", Fingerprint: res.CurrentFingerprint, Ordinal: res.CurrentOrdinal,
		})
	default:
		internalError(w, "put section "+id+"/"+section, errors.New("unknown put outcome"))
	}
}

// handleDeleteSection is the compare-and-set delete of spec §4.6.3:
// DELETE /api/profiles/{id}/sections/{section}?baseRev=N&clientId=c_….
//
// Deleting a section that is already gone is a 200 (two clients removing the
// same workspace must not deadlock each other) — but only the delete that
// wrote the tombstone is announced.
func (m *Module) handleDeleteSection(w http.ResponseWriter, r *http.Request) {
	id, section, ok := pathSection(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	clientID := query.Get("clientId")
	if err := validateClientID(clientID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	baseRev, err := strconv.ParseInt(query.Get("baseRev"), 10, 64)
	if err != nil {
		http.Error(w, "baseRev must be an integer", http.StatusBadRequest)
		return
	}
	if err := validateBaseRev(baseRev); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	res, err := m.store.DeleteSection(id, section, clientID, baseRev)
	if err != nil {
		writeStoreError(w, "delete section "+id+"/"+section, err)
		return
	}
	switch res.Outcome {
	case PutApplied:
		if res.Changed {
			m.announce(profileEvent{
				ProfileID: id, Section: section, Rev: res.Rev,
				WriterClientID: clientID, Deleted: true,
			})
		}
		writeJSON(w, map[string]int64{"rev": res.Rev})
	case PutConflict:
		writeConflict(w, res)
	default:
		internalError(w, "delete section "+id+"/"+section, errors.New("unknown delete outcome"))
	}
}

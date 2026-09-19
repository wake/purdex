package profiles

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
)

// smallBodyCap bounds the bodies of the routes that carry a name or an
// attachment — a few hundred bytes at most. Section PUTs have their own cap
// (putBodyCap, handler_sections.go).
const smallBodyCap = 64 << 10

// RegisterRoutes wires up the ten /api/profiles endpoints of spec §4.8.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/profiles", m.handleList)
	mux.HandleFunc("POST /api/profiles", m.handleCreate)
	mux.HandleFunc("PATCH /api/profiles/{id}", m.handleRename)
	mux.HandleFunc("DELETE /api/profiles/{id}", m.handleDeleteProfile)
	mux.HandleFunc("GET /api/profiles/{id}", m.handleGetProfile)
	mux.HandleFunc("GET /api/profiles/{id}/sections/{section}", m.handleGetSection)
	mux.HandleFunc("PUT /api/profiles/{id}/sections/{section}", m.handlePutSection)
	mux.HandleFunc("DELETE /api/profiles/{id}/sections/{section}", m.handleDeleteSection)
	mux.HandleFunc("PUT /api/profiles/{id}/attachment", m.handlePutAttachment)
	mux.HandleFunc("DELETE /api/profiles/{id}/attachment", m.handleDeleteAttachment)
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[profiles] encode response: %v", err)
	}
}

// internalError logs err and answers 500 without echoing it to the client.
func internalError(w http.ResponseWriter, what string, err error) {
	log.Printf("[profiles] %s: %v", what, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// readBody reads at most limit bytes of the request body. It reads limit+1 so
// an over-cap body is answered with 413 rather than silently truncated. On
// failure it has already written the response and returns false.
func readBody(w http.ResponseWriter, r *http.Request, limit int) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(limit)+1))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return nil, false
	}
	if len(body) > limit {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	return body, true
}

// pathProfileID returns the validated {id} path value, or writes 400.
func pathProfileID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if err := validateProfileID(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	return id, true
}

// readName reads a `{name}` body and returns the trimmed, validated name.
func readName(w http.ResponseWriter, r *http.Request) (string, bool) {
	body, ok := readBody(w, r, smallBodyCap)
	if !ok {
		return "", false
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return "", false
	}
	name, err := validateName(req.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	return name, true
}

// profileEntry is one element of the GET /api/profiles list: the profile, the
// index of its live sections (no payloads) and the clients attached to it.
type profileEntry struct {
	Profile
	Sections    []SectionMeta `json:"sections"`
	Attachments []Attachment  `json:"attachments"`
}

// handleList returns every profile: GET /api/profiles.
func (m *Module) handleList(w http.ResponseWriter, _ *http.Request) {
	profiles, err := m.store.ListProfiles()
	if err != nil {
		internalError(w, "list profiles", err)
		return
	}
	entries := make([]profileEntry, 0, len(profiles))
	for _, p := range profiles {
		sections, err := m.store.ListSections(p.ID)
		if err != nil {
			internalError(w, "list sections of "+p.ID, err)
			return
		}
		attachments, err := m.store.ListAttachments(p.ID)
		if err != nil {
			internalError(w, "list attachments of "+p.ID, err)
			return
		}
		entries = append(entries, profileEntry{Profile: p, Sections: sections, Attachments: attachments})
	}
	writeJSON(w, map[string]any{"profiles": entries})
}

// handleCreate creates a profile: POST /api/profiles with `{name}`. The
// response is the whole profile, of which the client needs `id`.
func (m *Module) handleCreate(w http.ResponseWriter, r *http.Request) {
	name, ok := readName(w, r)
	if !ok {
		return
	}
	p, err := m.store.CreateProfile(name)
	if err != nil {
		internalError(w, "create profile", err)
		return
	}
	writeJSON(w, p)
}

// handleRename renames a profile: PATCH /api/profiles/{id} with `{name}`.
func (m *Module) handleRename(w http.ResponseWriter, r *http.Request) {
	id, ok := pathProfileID(w, r)
	if !ok {
		return
	}
	name, ok := readName(w, r)
	if !ok {
		return
	}
	found, err := m.store.RenameProfile(id, name)
	if err != nil {
		internalError(w, "rename profile "+id, err)
		return
	}
	if !found {
		http.Error(w, ErrProfileNotFound.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"id": id, "name": name})
}

// handleDeleteProfile removes a profile: DELETE /api/profiles/{id}. While any
// client is attached it answers 409 with the attachments (spec decision 16).
func (m *Module) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathProfileID(w, r)
	if !ok {
		return
	}
	err := m.store.DeleteProfile(id)
	switch {
	case err == nil:
		writeJSON(w, map[string]bool{"deleted": true})
	case errors.Is(err, ErrProfileNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrProfileAttached):
		// Read after the refusal, so the list describes who is in the way now;
		// it may already be empty if they detached in between.
		attachments, listErr := m.store.ListAttachments(id)
		if listErr != nil {
			internalError(w, "list attachments of "+id, listErr)
			return
		}
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"reason":      "attached",
			"attachments": attachments,
		})
	default:
		internalError(w, "delete profile "+id, err)
	}
}

// handlePutAttachment makes {id} the master profile of the body's client:
// PUT /api/profiles/{id}/attachment with `{clientId, deviceName}`.
func (m *Module) handlePutAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathProfileID(w, r)
	if !ok {
		return
	}
	body, ok := readBody(w, r, smallBodyCap)
	if !ok {
		return
	}
	var req struct {
		ClientID   string `json:"clientId"`
		DeviceName string `json:"deviceName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if err := validateClientID(req.ClientID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	deviceName, err := validateDeviceName(req.DeviceName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = m.store.PutAttachment(Attachment{ClientID: req.ClientID, ProfileID: id, DeviceName: deviceName})
	switch {
	case err == nil:
		writeJSON(w, map[string]bool{"attached": true})
	case errors.Is(err, ErrProfileNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		internalError(w, "put attachment "+req.ClientID, err)
	}
}

// handleDeleteAttachment detaches a client from {id}:
// DELETE /api/profiles/{id}/attachment?clientId=c_…. The client id travels in
// the query string because DELETE bodies do not survive every proxy. An
// attachment to some other profile is left alone (`detached:false`).
func (m *Module) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathProfileID(w, r)
	if !ok {
		return
	}
	clientID := r.URL.Query().Get("clientId")
	if err := validateClientID(clientID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := m.store.requireProfile(id); err != nil {
		if errors.Is(err, ErrProfileNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		internalError(w, "get profile "+id, err)
		return
	}
	detached, err := m.store.DeleteAttachment(id, clientID)
	if err != nil {
		internalError(w, "delete attachment "+clientID, err)
		return
	}
	writeJSON(w, map[string]bool{"detached": detached})
}

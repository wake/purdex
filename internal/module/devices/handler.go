package devices

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/devices"
)

// The management routes (spec §3.4): admin token only, except PUT /api/devices/self (the device's own). "Admin" here is
// "no device principal on the request": a device token never manages devices. (The scope middleware of QP-1b refuses these
// routes to a device before they get here; this check is the second wall.)

const (
	maxBody       = 8 << 10
	defaultWithin = 900 // seconds
	minWithin     = 60
	maxWithin     = 1200
	maxLabelRunes = 64
)

var uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type rowView struct {
	ID          string `json:"id"`
	PairingID   string `json:"pairing_id"`
	ProfileID   string `json:"profile_id"`
	Label       string `json:"label"`
	CreatedAt   int64  `json:"created_at"`
	CreatedBy   string `json:"created_by"`
	UseBy       int64  `json:"use_by"`
	FirstUsedAt int64  `json:"first_used_at"`
	LastUsedAt  int64  `json:"last_used_at"`
	RevokedAt   int64  `json:"revoked_at"`
}

func viewOf(r Row) rowView {
	return rowView{ID: r.ID, PairingID: r.PairingID, ProfileID: r.ProfileID, Label: r.Label, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy,
		UseBy: r.UseBy, FirstUsedAt: r.FirstUsedAt, LastUsedAt: r.LastUsedAt, RevokedAt: r.RevokedAt}
}

type mintRequest struct {
	PairingID  string `json:"pairing_id"`
	ProfileID  string `json:"profile_id"`
	Label      string `json:"label"`
	UseWithinS *int   `json:"use_within_s"`
	Client     struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
	} `json:"client"`
}

type mintResponse struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	PairingID string `json:"pairing_id"`
	ProfileID string `json:"profile_id"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"created_at"`
	UseBy     int64  `json:"use_by"`
}

// printable: 1..maxLabelRunes runes, no control, format or non-space separator characters (it is shown on screens and in
// lists, and written by a person or a phone).
func printable(s string) bool {
	n := utf8.RuneCountInString(s)
	if n < 1 || n > maxLabelRunes || !utf8.ValidString(s) || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || (unicode.IsSpace(r) && r != ' ') {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]string{"error": code, "detail": detail})
}

// adminOnly answers 403 and false when the request carries a device principal.
func adminOnly(w http.ResponseWriter, r *http.Request) bool {
	if _, isDevice := devices.PrincipalFrom(r.Context()); isDevice {
		writeError(w, http.StatusForbidden, "admin_only", "device management needs the admin token")
		return false
	}
	return true
}

func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "")
		} else {
			writeError(w, http.StatusBadRequest, "bad_body", "")
		}
		return false
	}
	if !utf8.Valid(body) {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is not valid UTF-8")
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is not the expected JSON")
		return false
	}
	return true
}

// handleMint is POST /api/devices.
func (m *Module) handleMint(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	var req mintRequest
	if !readBody(w, r, &req) {
		return
	}
	bad := func(detail string) { writeError(w, http.StatusBadRequest, "bad_request", detail) }
	switch {
	case !uuidRe.MatchString(req.PairingID):
		bad("pairing_id must be a UUID")
		return
	case req.ProfileID != "" && !printable(req.ProfileID):
		bad("profile_id must be 1-64 printable characters")
		return
	case !printable(req.Label):
		bad("label must be 1-64 printable characters")
		return
	case strings.TrimSpace(req.Client.Kind) != "app" || !printable(req.Client.Label):
		bad(`client must be {"kind":"app","label":…}`)
		return
	}
	within := defaultWithin
	if req.UseWithinS != nil {
		within = *req.UseWithinS
	}
	if within < minWithin || within > maxWithin {
		bad("use_within_s must be 60-1200")
		return
	}
	st := m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the device store is not open")
		return
	}
	row, token, err := st.Mint(MintRequest{PairingID: strings.ToLower(req.PairingID), ProfileID: req.ProfileID, Label: req.Label, CreatedBy: req.Client.Label, UseWithin: time.Duration(within) * time.Second})
	if err != nil {
		log.Printf("[devices] mint: %v", err) // never the token
		writeError(w, http.StatusInternalServerError, "store_failed", "")
		return
	}
	log.Printf("[devices] minted %s for pairing %s by %q from %s", row.ID, row.PairingID, row.CreatedBy, r.RemoteAddr)
	writeJSON(w, http.StatusCreated, mintResponse{ID: row.ID, Token: token, PairingID: row.PairingID, ProfileID: row.ProfileID, Label: row.Label, CreatedAt: row.CreatedAt, UseBy: row.UseBy})
}

// handleList is GET /api/devices: rows without a token or hash.
func (m *Module) handleList(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	st := m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the device store is not open")
		return
	}
	rows, err := st.List()
	if err != nil {
		log.Printf("[devices] list: %v", err)
		writeError(w, http.StatusInternalServerError, "store_failed", "")
		return
	}
	out := make([]rowView, 0, len(rows))
	for _, r := range rows {
		out = append(out, viewOf(r))
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

// handleRevokeID is DELETE /api/devices/{id}: idempotent, 204 whether or not the id was live.
func (m *Module) handleRevokeID(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	st := m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the device store is not open")
		return
	}
	id := r.PathValue("id")
	if devices.ValidID(id) {
		changed, err := st.RevokeID(id)
		if err != nil {
			log.Printf("[devices] revoke %s: %v", id, err)
			writeError(w, http.StatusInternalServerError, "store_failed", "")
			return
		}
		if changed {
			log.Printf("[devices] revoked %s from %s", id, r.RemoteAddr)
		}
		// The hook runs whether or not this call changed the row: a connection of an already revoked device (one that was
		// opened before an earlier, failed close) is closed by a second revoke.
		m.revoked([]string{id})
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRevokePairing is DELETE /api/devices?pairing_id=<uuid>.
func (m *Module) handleRevokePairing(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	pairing := r.URL.Query().Get("pairing_id")
	if !uuidRe.MatchString(pairing) {
		writeError(w, http.StatusBadRequest, "bad_request", "pairing_id must be a UUID")
		return
	}
	st := m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the device store is not open")
		return
	}
	ids, err := st.RevokePairing(strings.ToLower(pairing))
	if err != nil {
		log.Printf("[devices] revoke pairing %s: %v", pairing, err)
		writeError(w, http.StatusInternalServerError, "store_failed", "")
		return
	}
	if len(ids) > 0 {
		log.Printf("[devices] revoked %d device(s) of pairing %s from %s", len(ids), pairing, r.RemoteAddr)
	}
	m.revoked(ids)
	w.WriteHeader(http.StatusNoContent)
}

// handleSelf is PUT /api/devices/self {label}: a device renames itself, and only itself.
func (m *Module) handleSelf(w http.ResponseWriter, r *http.Request) {
	p, isDevice := devices.PrincipalFrom(r.Context())
	if !isDevice {
		writeError(w, http.StatusForbidden, "device_only", "only a device token can rename itself")
		return
	}
	var req struct {
		Label string `json:"label"`
	}
	if !readBody(w, r, &req) {
		return
	}
	if !printable(req.Label) {
		writeError(w, http.StatusBadRequest, "bad_request", "label must be 1-64 printable characters")
		return
	}
	st := m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the device store is not open")
		return
	}
	if err := st.SetLabel(p.ID, req.Label); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "")
			return
		}
		log.Printf("[devices] label %s: %v", p.ID, err)
		writeError(w, http.StatusInternalServerError, "store_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": p.ID, "label": req.Label})
}

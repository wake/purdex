package hosttransfer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"time"

	"github.com/wake/purdex/internal/devices"
)

// Pairing entries on the relay (QR pairing spec §4): separate from Share-hosts transfers — own create route, own validation,
// own claim. The claim carries no bearer (the phone has no credential yet): the code is the credential, the source must be on
// the tailnet or loopback, and a failure limiter of its own, per source, bounds guessing.

const (
	pairingMinTTL     = 60 * time.Second
	pairingMaxTTL     = 600 * time.Second
	claimBodyCap      = 1 << 10
	pairingRoutesBase = "/api/host-transfer/pairings"
)

// ClaimRoute is the exact path the daemon's chain exempts from TokenAuth and the device scope (cmd/pdx/http_chain.go).
const ClaimRoute = pairingRoutesBase + "/claim"

func (m *Module) registerPairingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+pairingRoutesBase, m.handlePairingCreate)
	mux.HandleFunc("POST "+ClaimRoute, m.handleClaim)
	mux.HandleFunc("GET "+pairingRoutesBase+"/{code}", m.handlePairingStatus)
	mux.HandleFunc("DELETE "+pairingRoutesBase+"/{code}", m.handlePairingDelete)
}

var (
	profileIDRe = regexp.MustCompile(`^p_[0-9a-f]{12}$`)
	uuidRe      = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type pairProfile struct {
	HostDaemonID string `json:"hostDaemonId"`
	ProfileID    string `json:"profileId"`
	Name         string `json:"name"`
}

// pairRow is exactly the row of spec §4.1; any other key is an error.
type pairRow struct {
	V         int             `json:"v"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	IP        string          `json:"ip"`
	Port      int             `json:"port"`
	DaemonID  string          `json:"daemonId"`
	Look      json.RawMessage `json:"look"`
	Token     string          `json:"token"`
	DeviceID  string          `json:"deviceId"`
	PairingID string          `json:"pairingId"`
	Profile   *pairProfile    `json:"profile"`
}

// validatePairRow checks one row against spec §4.1 and returns its pairing id and profile for the cross-row check.
func validatePairRow(raw json.RawMessage) (pairingID string, profile pairProfile, ok bool) {
	var r pairRow
	if err := decodeStrict(raw, &r); err != nil {
		return "", pairProfile{}, false
	}
	look := bytes.TrimSpace(r.Look)
	if r.V != 1 || r.Kind != "pair" || r.Name == "" || r.DaemonID == "" || r.Port < 1 || r.Port > 65535 ||
		len(look) == 0 || look[0] != '{' || r.Profile == nil {
		return "", pairProfile{}, false
	}
	if _, err := netip.ParseAddr(r.IP); err != nil {
		return "", pairProfile{}, false
	}
	if !devices.IsDeviceToken(r.Token) || !devices.ValidID(r.DeviceID) || !uuidRe.MatchString(r.PairingID) {
		return "", pairProfile{}, false
	}
	p := *r.Profile
	if p.HostDaemonID == "" || p.Name == "" || !profileIDRe.MatchString(p.ProfileID) {
		return "", pairProfile{}, false
	}
	return r.PairingID, p, true
}

// handlePairingCreate parks `{rows, expires_in_s?}` as a pairing entry (admin).
func (m *Module) handlePairingCreate(w http.ResponseWriter, r *http.Request) {
	if !m.authorize(w, r) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, createBodyCap))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeReason(w, http.StatusRequestEntityTooLarge, "too_large")
			return
		}
		writeReason(w, http.StatusBadRequest, "bad_payload")
		return
	}
	var req struct {
		Rows      []json.RawMessage `json:"rows"`
		ExpiresIn *int              `json:"expires_in_s"`
	}
	if err := decodeStrict(body, &req); err != nil || len(req.Rows) == 0 || len(req.Rows) > maxRows {
		writeReason(w, http.StatusBadRequest, "bad_payload")
		return
	}
	ttl := pairingMaxTTL
	if req.ExpiresIn != nil {
		// Compared as integers first: multiplying a huge value by time.Second would wrap into the accepted range.
		if secs := *req.ExpiresIn; secs < int(pairingMinTTL/time.Second) || secs > int(pairingMaxTTL/time.Second) {
			writeReason(w, http.StatusBadRequest, "bad_expiry")
			return
		}
		ttl = time.Duration(*req.ExpiresIn) * time.Second
	}
	var firstID string
	var firstProfile pairProfile
	for i, row := range req.Rows {
		id, p, ok := validatePairRow(row)
		if !ok {
			writeReason(w, http.StatusBadRequest, "bad_payload")
			return
		}
		if i == 0 {
			firstID, firstProfile = id, p
		} else if id != firstID || p != firstProfile { // one pairing, one profile across the rows
			writeReason(w, http.StatusBadRequest, "bad_payload")
			return
		}
	}
	payload, err := json.Marshal(req.Rows)
	if err != nil {
		writeReason(w, http.StatusBadRequest, "bad_payload")
		return
	}
	code, expiresAt, err := m.store.CreatePairing(payload, ttl)
	switch {
	case errors.Is(err, ErrCapacity):
		writeReason(w, http.StatusTooManyRequests, "capacity")
	case err != nil:
		writeReason(w, http.StatusServiceUnavailable, "unavailable")
	default:
		writeJSONStatus(w, http.StatusOK, struct {
			Code      string `json:"code"`
			ExpiresAt int64  `json:"expiresAt"`
		}{code, expiresAt.UnixMilli()})
	}
}

// sourceAllowed: the claim's own source rule, whatever the daemon's allow list says — tailnet or loopback only.
var claimNets = func() []netip.Prefix {
	var out []netip.Prefix
	for _, c := range []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48", "127.0.0.0/8", "::1/128"} {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}()

// sourceOf returns the request's source address (no port, IPv4-mapped IPv6 unmapped) and whether it may claim.
func sourceOf(r *http.Request) (string, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "", false
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return "", false
	}
	a = a.Unmap().WithZone("")
	for _, p := range claimNets {
		if p.Contains(a) {
			return a.String(), true
		}
	}
	return a.String(), false
}

// handleClaim trades `{code}` for the pairing rows, once. No bearer.
func (m *Module) handleClaim(w http.ResponseWriter, r *http.Request) {
	source, ok := sourceOf(r)
	if !ok {
		writeReason(w, http.StatusForbidden, "forbidden_source")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, claimBodyCap))
	if err != nil {
		writeReason(w, http.StatusBadRequest, "bad_request")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeStrict(body, &req); err != nil || req.Code == "" {
		writeReason(w, http.StatusBadRequest, "bad_request")
		return
	}
	rows, retryAfter, err := m.store.Claim(source, req.Code)
	switch {
	case errors.Is(err, ErrRateLimited):
		w.Header().Set("Retry-After", strconv.Itoa(ceilSeconds(retryAfter)))
		writeReason(w, http.StatusTooManyRequests, "rate_limited")
	case errors.Is(err, ErrStopped):
		writeReason(w, http.StatusServiceUnavailable, "unavailable")
	case err != nil:
		writeReason(w, http.StatusNotFound, "invalid_code")
	default:
		writeJSONStatus(w, http.StatusOK, struct {
			Rows json.RawMessage `json:"rows"`
		}{rows})
	}
}

// handlePairingStatus: `{claimed, claimedAt?, expiresAt}` (admin).
func (m *Module) handlePairingStatus(w http.ResponseWriter, r *http.Request) {
	if !m.authorize(w, r) {
		return
	}
	st, ok := m.store.Status(r.PathValue("code"))
	if !ok {
		writeReason(w, http.StatusNotFound, "not_found")
		return
	}
	out := struct {
		Claimed   bool  `json:"claimed"`
		ClaimedAt int64 `json:"claimedAt,omitempty"`
		ExpiresAt int64 `json:"expiresAt"`
	}{Claimed: st.Claimed, ExpiresAt: st.ExpiresAt.UnixMilli()}
	if st.Claimed {
		out.ClaimedAt = st.ClaimedAt.UnixMilli()
	}
	writeJSONStatus(w, http.StatusOK, out)
}

// handlePairingDelete: 204 removed an unclaimed entry, 409 claimed (nothing removed), 404 none (admin).
func (m *Module) handlePairingDelete(w http.ResponseWriter, r *http.Request) {
	if !m.authorize(w, r) {
		return
	}
	switch err := m.store.DeletePairing(r.PathValue("code")); {
	case err == nil:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrClaimed):
		writeReason(w, http.StatusConflict, "claimed")
	default:
		writeReason(w, http.StatusNotFound, "not_found")
	}
}

// internal/module/peers/hosts.go
package peers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
)

// apiError is a handler error carrying the HTTP status it should produce.
// UpdateConfig's mutate closures return one of these so the handler can
// translate a commit-time re-check failure into the right status code
// without inspecting error strings.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

// hostRow is the never-secret view of a config.PeerHost served by every
// hosts route except the POST 201 body: alias/url/host_id plus booleans,
// never Token or InboundToken themselves.
type hostRow struct {
	Alias           string `json:"alias"`
	URL             string `json:"url"`
	HostID          string `json:"host_id"`
	Verified        bool   `json:"verified"`
	HasToken        bool   `json:"has_token"`
	HasInboundToken bool   `json:"has_inbound_token"`
	AllowBypass     bool   `json:"allow_bypass"`
	// Rotation state (spec §6.1): pending = inbound_token_prev is set;
	// last_inbound_auth = "" | "current" | "prev" — which token the peer
	// most recently presented in this epoch (in memory, rotation.go).
	RotationPending bool   `json:"rotation_pending"`
	LastInboundAuth string `json:"last_inbound_auth"`
}

func toHostRow(h config.PeerHost) hostRow {
	return hostRow{
		Alias:           h.Alias,
		URL:             h.URL,
		HostID:          h.HostID,
		Verified:        h.HostID != "",
		HasToken:        h.Token != "",
		HasInboundToken: h.InboundToken != "",
		AllowBypass:     h.AllowBypass,
	}
}

// addHostRequest is POST /api/peers/hosts' body. Token is optional: when
// empty, the host is added unverified (host_id stays "" until a later PUT
// supplies and verifies a token).
type addHostRequest struct {
	Alias string `json:"alias"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

// addHostResponse is POST /api/peers/hosts' 201 body — the only response
// that ever carries a live token value (inbound_token, the token the OTHER
// side must present to reach this host going forward).
type addHostResponse struct {
	Alias        string `json:"alias"`
	URL          string `json:"url"`
	HostID       string `json:"host_id"`
	InboundToken string `json:"inbound_token"`
	Verified     bool   `json:"verified"`
}

// putHostRequest is PUT /api/peers/hosts/{alias}'s body. All three fields
// are optional and independent: Token (when non-empty) verifies and stores
// an outbound token; AllowBypass (when non-nil) sets AllowBypass; Alias
// (when non-empty) renames the entry (spec §4.2) — validated with
// config.ValidateAlias and for case-insensitive uniqueness exactly as an
// operator-typed name at POST is, because the value the page sends here is
// the peer's own self-reported alias (v4 spec §7.2: a learned alias must
// clear the same bar).
type putHostRequest struct {
	Token       string `json:"token"`
	AllowBypass *bool  `json:"allow_bypass"`
	Alias       string `json:"alias"`
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// writeAPIError translates an UpdateConfig mutate error into a response:
// an *apiError carries its own status, anything else is a 500.
func writeAPIError(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeJSONError(w, ae.status, ae.msg)
		return
	}
	writeJSONError(w, http.StatusInternalServerError, err.Error())
}

// requireAdmin writes 403 and returns false when the request's principal
// (set by PeerAuth) is not PrincipalAdmin. The hosts routes are already
// admin-only at the middleware chain level (HostRoutePolicy refuses every
// host principal on this subtree) — this is a defense-in-depth check for
// when a handler is exercised directly (tests, or a future chain bug).
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	p, ok := middleware.PrincipalFrom(r.Context())
	if !ok || p.Kind != middleware.PrincipalAdmin {
		writeJSONError(w, http.StatusForbidden, ipeers.ErrForbidden)
		return false
	}
	return true
}

// normalizeHostURL parses raw as an absolute http(s) URL suitable for
// storing as a peer host's URL, and returns the normalized form: any
// trailing "/" stripped, so a stored URL concatenated with "/api/peers" in
// fetchRemote never produces a double slash (a URL submitted with or
// without a trailing slash both persist identically). Userinfo, a query
// string, and a fragment are rejected outright — none has any meaning for
// a peer host URL, and accepting them risks smuggling unexpected behavior
// into a value that gets stored and later dialed. On any rejection the
// returned error's message is fit to report to the caller as the body of
// a 400, prefixed "invalid url: ".
func normalizeHostURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid url: scheme must be http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid url: missing host")
	}
	if u.User != nil {
		return "", fmt.Errorf("invalid url: must not contain userinfo")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("invalid url: must not contain a query string")
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("invalid url: must not contain a fragment")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// tokenEqualsAdmin reports whether token (a caller-supplied outbound
// token) equals the local admin token, in constant time. An empty admin
// token never matches (there is nothing to collide with).
func tokenEqualsAdmin(token, adminToken string) bool {
	if token == "" || adminToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) == 1
}

// mintInboundToken returns a fresh peer token guaranteed not to equal
// adminToken, re-minting on the practically-impossible collision (the
// check is cheap, so it costs nothing to make it exact rather than
// probabilistic).
func mintInboundToken(adminToken string) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		tok, err := config.NewPeerToken()
		if err != nil {
			return "", fmt.Errorf("mint inbound token: %w", err)
		}
		if !tokenEqualsAdmin(tok, adminToken) {
			return tok, nil
		}
	}
	return "", fmt.Errorf("mint inbound token: repeated collision with admin token")
}

// validHostID reports whether s is safe to persist or trust as a peer's
// self-reported host_id: non-empty, at most 128 bytes, and every rune
// printable and not a space. A peer's host_id is attacker-controlled — it
// flows into config (POST/PUT persist it), into fan-out rows, and
// otherwise unbounded into this host's own error messages — so a control
// character (e.g. an ANSI escape), whitespace, or an oversized value must
// never be accepted, let alone stored or trusted.
func validHostID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// sanitizeLearnedAlias returns alias when it is safe to adopt as a local name
// for a peer, or "" when it is not. Same posture as validHostID: a peer's
// self-report is data, never a decision. config.ValidateAlias is the single
// rule — one safe URL path segment, bounded, no control characters, not a
// reserved dot name, and distinct from localAlias — so a learned alias can
// never be accepted on terms an operator-supplied one would be refused on.
func sanitizeLearnedAlias(alias, localAlias string) string {
	if config.ValidateAlias(alias, localAlias) != nil {
		return ""
	}
	return alias
}

// verifyHost calls the fetch seam against url with the given outbound
// token, applying the verify predicate: err == nil && env.OK &&
// validHostID(env.HostID). On success errMsg is ""; otherwise errMsg names
// why (transport error, peer reported not-ok, or an invalid host_id), fit
// to report to the caller as the body of a 502.
func (m *Module) verifyHost(ctx context.Context, targetURL, token string) (env ipeers.Envelope, errMsg string) {
	vctx, cancel := context.WithTimeout(ctx, remoteFetchTimeout)
	defer cancel()

	env, err := m.fetch(vctx, m.client, targetURL, token)
	if err != nil {
		return env, boundRemote(err.Error(), token)
	}
	if !env.OK {
		if env.Error != "" {
			return env, "peer: " + boundRemote(env.Error, token)
		}
		return env, "peer reported ok=false"
	}
	if !validHostID(env.HostID) {
		return env, "peer returned an invalid host_id"
	}
	// A host_id that passes validHostID's shape check can still carry (or
	// embed) the very outbound token we just sent as this request's Bearer
	// — validHostID only checks length/printable/no-whitespace, not
	// content. Learning that value would persist it into config and serve
	// it back out of every hostRow.host_id forever, so it is refused here
	// exactly as any other shape violation is: same message, since it is
	// just as invalid for us (#1152).
	if redactSecret(env.HostID, token) != env.HostID {
		return env, "peer returned an invalid host_id"
	}
	return env, ""
}

// handleListHosts serves GET /api/peers/hosts: every configured host as a
// never-secret hostRow, in config order.
func (m *Module) handleListHosts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}

	m.core.CfgMu.RLock()
	hosts := append([]config.PeerHost(nil), m.core.Cfg.Peers.Hosts...)
	m.core.CfgMu.RUnlock()

	rows := make([]hostRow, len(hosts))
	for i, h := range hosts {
		rows[i] = m.hostRowFor(h)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"hosts": rows})
}

// handleAddHost serves POST /api/peers/hosts. The sequence: validate an
// explicit alias and the url, refuse an outbound token equal to the admin
// token, verify outside any lock when a token was supplied, SETTLE the
// alias (spec §7.2 — an omitted one falls back to the alias the peer
// publishes for itself, which is why the verify has to come first), check
// the settled alias for validity and for uniqueness (fast path), mint an
// inbound token, then commit inside UpdateConfig — which re-checks alias
// uniqueness and, when verified, that the learned host_id doesn't collide
// with the local one. Any failure persists nothing.
//
// Every alias check below runs on the settled alias, not on req.Alias: a
// learned alias must clear exactly the bar an operator-supplied one does.
func (m *Module) handleAddHost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}

	var req addHostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}

	m.core.CfgMu.RLock()
	localAlias := m.core.Cfg.PeerAlias()
	adminToken := m.core.Cfg.Token
	m.core.CfgMu.RUnlock()

	// An explicit alias is rejected here, before the verify below dials
	// anyone: a request this host will refuse anyway must not cost a
	// network round trip to a peer. The settled alias is validated again
	// after the verify, which is what covers the learned path.
	if req.Alias != "" {
		if err := config.ValidateAlias(req.Alias, localAlias); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	normalizedURL, err := normalizeHostURL(req.URL)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if tokenEqualsAdmin(req.Token, adminToken) {
		writeJSONError(w, http.StatusBadRequest, "token equals admin token")
		return
	}

	var learnedHostID, publishedAlias string
	verified := false
	if req.Token != "" {
		env, errMsg := m.verifyHost(r.Context(), normalizedURL, req.Token)
		if errMsg != "" {
			writeJSONError(w, http.StatusBadGateway, errMsg)
			return
		}
		learnedHostID = env.HostID
		publishedAlias = env.Alias
		verified = true
	}

	// Settle the alias. With no outbound token there was no verify and so
	// no envelope, which leaves publishedAlias "" — that path still
	// requires an explicit alias. sanitizeLearnedAlias yields "" for
	// anything unsafe, and the refusal below deliberately does NOT quote
	// the peer's value back: it is attacker-controlled and this message
	// lands in an operator's terminal.
	alias := req.Alias
	if alias == "" {
		alias = sanitizeLearnedAlias(publishedAlias, localAlias)
	}
	if alias == "" {
		writeJSONError(w, http.StatusBadRequest,
			"no alias given and the peer published none; pass one explicitly")
		return
	}
	if err := config.ValidateAlias(alias, localAlias); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Recomputed against the settled alias, never req.Alias: a learned one
	// has to clear the same uniqueness bar. config.ValidateAlias cannot do
	// this — it compares only against the local alias, never against the
	// other configured hosts. Not auto-suffixed either: "air26-2" would be
	// unportable in a new way, which is the problem this phase exists to
	// remove. The operator picks.
	m.core.CfgMu.RLock()
	aliasTaken := m.core.Cfg.Peers.FindPeerHostByAlias(alias) != -1
	m.core.CfgMu.RUnlock()
	if aliasTaken {
		writeJSONError(w, http.StatusConflict, fmt.Sprintf(
			"alias %q is already used by another host; pass an explicit alias for this one", alias))
		return
	}

	inboundToken, err := mintInboundToken(adminToken)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	newHost := config.PeerHost{
		Alias:        alias,
		URL:          normalizedURL,
		HostID:       learnedHostID,
		Token:        req.Token,
		InboundToken: inboundToken,
	}

	err = m.core.UpdateConfig(func(cfg *config.Config) error {
		if cfg.Peers.FindPeerHostByAlias(alias) != -1 {
			return &apiError{http.StatusConflict, "alias changed concurrently"}
		}
		// Re-checked against the LIVE local alias, not the pre-lock
		// snapshot: PUT /api/peers/settings {alias} can land while the
		// verify above is on the wire (self-alias spec S-1, codex F2), and
		// the rename closure in handlePutHost already re-validates the same
		// way. Shape errors were refused before the dial, so the only thing
		// this can catch is the local-alias collision — a conflict.
		if err := config.ValidateAlias(alias, cfg.PeerAlias()); err != nil {
			return &apiError{http.StatusConflict, err.Error()}
		}
		if verified && learnedHostID == cfg.HostID {
			return &apiError{http.StatusBadRequest, "cannot pair a host with itself"}
		}
		cfg.Peers.Hosts = append(cfg.Peers.Hosts, newHost)
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(addHostResponse{
		Alias:        alias,
		URL:          normalizedURL,
		HostID:       learnedHostID,
		InboundToken: inboundToken,
		Verified:     verified,
	})
}

// handlePutHost serves PUT /api/peers/hosts/{alias}: an optional Token
// verifies (outside any lock) and stores an outbound token plus the
// learned host_id, an optional AllowBypass sets that flag, and an optional
// Alias renames the entry (spec §4.2), validated and uniqueness-checked
// before the verify and again under the lock; the alias write is the last
// one in the closure. Any subset — including none — may be present. The
// commit re-checks under the lock that the entry is still present, is
// still the SAME entry the verify OR the rename ran against (URL and
// InboundToken both match the pre-lock snapshot — InboundToken is unique
// per entry and minted fresh at POST, so this also catches a
// delete+re-create at the same alias/url as a different entry, and the
// re-check applies whether the request is verifying, renaming, or both),
// its HostID is still empty or equal to the newly learned one, and the
// learned host_id isn't the local one.
func (m *Module) handlePutHost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	alias := r.PathValue("alias")

	m.core.CfgMu.RLock()
	idx := m.core.Cfg.Peers.FindPeerHostByAlias(alias)
	var existingURL, existingInboundToken string
	if idx != -1 {
		existingURL = m.core.Cfg.Peers.Hosts[idx].URL
		existingInboundToken = m.core.Cfg.Peers.Hosts[idx].InboundToken
	}
	adminToken := m.core.Cfg.Token
	localAlias := m.core.Cfg.PeerAlias()
	m.core.CfgMu.RUnlock()

	if m.putHostAfterSnapshot != nil {
		m.putHostAfterSnapshot()
	}

	if idx == -1 {
		writeJSONError(w, http.StatusNotFound, "unknown alias")
		return
	}

	var req putHostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}

	if tokenEqualsAdmin(req.Token, adminToken) {
		writeJSONError(w, http.StatusBadRequest, "token equals admin token")
		return
	}

	// A rename is validated before the verify below dials anyone, for the
	// same reason handleAddHost validates an explicit alias first: a
	// request this host will refuse anyway must not cost a network round
	// trip. Uniqueness excludes the entry itself, so a case-only change
	// ("air" → "Air") is not a collision. Both checks run again under the
	// lock at commit.
	renaming := req.Alias != ""
	if renaming {
		if err := config.ValidateAlias(req.Alias, localAlias); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		m.core.CfgMu.RLock()
		cur := m.core.Cfg.Peers.FindPeerHostByAlias(alias)
		other := m.core.Cfg.Peers.FindPeerHostByAlias(req.Alias)
		m.core.CfgMu.RUnlock()
		// cur and other come from the same snapshot, so "other == cur" means
		// the entry itself (a case-only rename), whatever index it now sits
		// at. Comparing against the idx taken under the earlier lock would
		// mis-report a self-match as a collision after a concurrent delete
		// shifted the slice. cur == -1 (deleted meanwhile) falls through to
		// the closure, which answers 404.
		if other != -1 && other != cur {
			writeJSONError(w, http.StatusConflict, fmt.Sprintf(
				"alias %q is already used by another host", req.Alias))
			return
		}
	}

	var learnedHostID string
	verifying := req.Token != ""
	if verifying {
		env, errMsg := m.verifyHost(r.Context(), existingURL, req.Token)
		if errMsg != "" {
			writeJSONError(w, http.StatusBadGateway, errMsg)
			return
		}
		learnedHostID = env.HostID
	}

	var row hostRow
	err := m.core.UpdateConfig(func(cfg *config.Config) error {
		i := cfg.Peers.FindPeerHostByAlias(alias)
		if i == -1 {
			return &apiError{http.StatusNotFound, "unknown alias"}
		}
		h := &cfg.Peers.Hosts[i]
		if verifying || renaming {
			// InboundToken is unique per entry and minted fresh at POST, so
			// comparing it (alongside URL) catches an entry that was
			// deleted and re-created — even at the SAME url — while this
			// request was in flight: it is a different entry wearing the
			// same alias, and neither a verify's result nor a rename must
			// land on it.
			if h.URL != existingURL || h.InboundToken != existingInboundToken {
				return &apiError{http.StatusConflict, "entry changed concurrently"}
			}
		}
		if verifying {
			if h.HostID != "" && h.HostID != learnedHostID {
				return &apiError{http.StatusConflict, "host_id mismatch"}
			}
			if learnedHostID == cfg.HostID {
				return &apiError{http.StatusBadRequest, "cannot pair a host with itself"}
			}
		}
		if renaming {
			if err := config.ValidateAlias(req.Alias, cfg.PeerAlias()); err != nil {
				return &apiError{http.StatusBadRequest, err.Error()}
			}
			if other := cfg.Peers.FindPeerHostByAlias(req.Alias); other != -1 && other != i {
				return &apiError{http.StatusConflict, fmt.Sprintf(
					"alias %q is already used by another host", req.Alias)}
			}
		}
		if verifying {
			h.Token = req.Token
			h.HostID = learnedHostID
		}
		if req.AllowBypass != nil {
			h.AllowBypass = *req.AllowBypass
		}
		if renaming {
			// The record is keyed by the STORED alias; the path's spelling
			// only matched case-insensitively.
			oldAlias := h.Alias
			h.Alias = req.Alias
			m.renameInboundAuth(oldAlias, req.Alias)
		}
		// Captured here, inside the mutate closure, so the response
		// always reflects exactly what THIS request committed — never a
		// value a concurrent request wrote in between commit and a
		// separate post-commit read.
		row = m.hostRowFor(*h)
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}

	_ = json.NewEncoder(w).Encode(row)
}

// handleDeleteHost serves DELETE /api/peers/hosts/{alias}.
func (m *Module) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	alias := r.PathValue("alias")

	err := m.core.UpdateConfig(func(cfg *config.Config) error {
		i := cfg.Peers.FindPeerHostByAlias(alias)
		if i == -1 {
			return &apiError{http.StatusNotFound, "unknown alias"}
		}
		stored := cfg.Peers.Hosts[i].Alias // the record's key, not the path's spelling
		cfg.Peers.Hosts = append(cfg.Peers.Hosts[:i], cfg.Peers.Hosts[i+1:]...)
		m.resetInboundAuth(stored)
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

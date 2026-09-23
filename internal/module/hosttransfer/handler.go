package hosttransfer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	createBodyCap = 64 << 10 // spec §6.2: ≤ 64 KiB
	redeemBodyCap = 1 << 10  // `{"code": "..."}`
	maxRows       = 32       // spec §6.2: ≤ 32 hosts
)

// RegisterRoutes wires the two endpoints of spec §6.2. Both sit behind the
// general chain's TokenAuth (cmd/pdx/http_chain.go), which is open when no
// admin token is configured — so each handler also fails closed on its own:
// no token → 403 no_token, before the body, the store or the failure count.
//
// Nothing here logs: a body, a code or a payload never reaches the log.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/host-transfer", m.handleCreate)
	mux.HandleFunc("POST /api/host-transfer/redeem", m.handleRedeem)
}

// writeJSONStatus answers v as JSON. Every response of this module carries
// Cache-Control: no-store — a code or a list of tokens must not be cached.
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	// An encode error is a dead connection; there is nothing to report, and
	// its text must not carry the payload into a log.
	_ = json.NewEncoder(w).Encode(v)
}

func writeReason(w http.ResponseWriter, status int, reason string) {
	writeJSONStatus(w, status, struct {
		Reason string `json:"reason"`
	}{reason})
}

// decodeStrict decodes exactly one JSON value from body into v, rejecting
// unknown fields and anything after the value.
func decodeStrict(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}

// handleCreate parks `{"hosts": [...]}` under a new code. The rows are
// opaque to the daemon: it only checks that each is a JSON object.
func (m *Module) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !m.hasToken() {
		writeReason(w, http.StatusForbidden, "no_token")
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
		Hosts []json.RawMessage `json:"hosts"`
	}
	if err := decodeStrict(body, &req); err != nil || len(req.Hosts) == 0 || len(req.Hosts) > maxRows {
		writeReason(w, http.StatusBadRequest, "bad_payload")
		return
	}
	for _, row := range req.Hosts {
		if t := bytes.TrimSpace(row); len(t) == 0 || t[0] != '{' {
			writeReason(w, http.StatusBadRequest, "bad_payload")
			return
		}
	}
	payload, err := json.Marshal(req.Hosts)
	if err != nil {
		writeReason(w, http.StatusBadRequest, "bad_payload")
		return
	}

	code, expiresAt, err := m.store.Create(payload)
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

// handleRedeem trades `{"code": "..."}` for the parked rows, once. A body
// that is not a well-formed request answers bad_request and is not counted
// as a failure: it is not a guess.
func (m *Module) handleRedeem(w http.ResponseWriter, r *http.Request) {
	if !m.hasToken() {
		writeReason(w, http.StatusForbidden, "no_token")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, redeemBodyCap))
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

	payload, retryAfter, err := m.store.Redeem(req.Code)
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
			Hosts json.RawMessage `json:"hosts"`
		}{payload})
	}
}

// ceilSeconds rounds d up to whole seconds, at least 1.
func ceilSeconds(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	if s < 1 {
		return 1
	}
	return s
}

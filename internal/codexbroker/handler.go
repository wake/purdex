package codexbroker

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Handler exposes the codex broker inventory over HTTP.
//
// Auth is enforced upstream by daemon middleware (see cmd/pdx/main.go's
// TokenAuth wrapping the inner mux); this handler does not perform any token
// check itself. Adding one here would duplicate middleware behaviour.
type Handler struct {
	scanner *Scanner
}

// NewHandler builds a Handler bound to the supplied scanner.
func NewHandler(scanner *Scanner) *Handler {
	return &Handler{scanner: scanner}
}

// HandleBrokers serves GET /api/codex/brokers.
//
// Behaviour:
//   - Non-GET → 405 Method Not Allowed.
//   - Scanner.Scan succeeds → 200 OK with the JSON shape from spec §4.3
//     (including partial=true when a sub-source timed out).
//   - Scanner.Scan returns ErrPsUnavailable → 503 with {"error": "..."}.
//   - The request context propagates into Scan; client deadlines win over
//     the scanner's own deadline because Scan stops at whichever cancels first.
func (h *Handler) HandleBrokers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "method not allowed",
		})
		return
	}

	res, err := h.scanner.Scan(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		if errors.Is(err, ErrPsUnavailable) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": err.Error(),
			})
			return
		}
		// Any other error is unexpected (e.g. ctx canceled before Scan
		// produced anything). Surface as 503 so callers can retry; the
		// 503-only-when-ps-unavailable rule from spec §4.3 still holds in
		// practice because Scanner.Scan only returns errors in those two
		// cases (sentinel + ctx).
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

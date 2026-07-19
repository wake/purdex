package execution

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// This file exposes the M0 manual-reclaim HTTP surface (spec §5.4, codex #2). It
// is the human-triggered counterpart to the automatic startup sweep: an operator
// POSTs to unwedge a dispatch stuck at claimed/running (e.g. its daemon crashed
// and was restarted). Automatic lease/heartbeat is M1 — M0 guarantees only "there
// is a way to manually reclaim + one automatic sweep on start".

// ReclaimRequest is the manual-reclaim POST body. An empty/absent ExecutionID
// sweeps every stuck execution; a set ExecutionID reclaims just that one.
type ReclaimRequest struct {
	ExecutionID string `json:"execution_id"`
}

// ReclaimResponse reports what a manual reclaim did. Reconciled is the count swept
// in all-mode; Found reports whether a single-id reclaim matched a live row.
type ReclaimResponse struct {
	Reconciled  int    `json:"reconciled"`
	Found       bool   `json:"found"`
	ExecutionID string `json:"execution_id,omitempty"`
}

// ReclaimHandler returns the POST /api/dispatch/reclaim handler. An empty body
// sweeps all stuck executions; {"execution_id":...} reclaims one. Reconcile is
// idempotent, so repeated POSTs are safe. Method routing (POST) and auth are
// enforced by the mux + global TokenAuth middleware.
func (r *Reconciler) ReclaimHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body ReclaimRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		ctx := req.Context()

		if body.ExecutionID != "" {
			found, err := r.ReclaimByID(ctx, body.ExecutionID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, ReclaimResponse{Found: found, ExecutionID: body.ExecutionID})
			return
		}

		n, err := r.sweep(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, ReclaimResponse{Reconciled: n})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

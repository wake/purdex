package core

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

// handleExecstat serves GET /api/debug/execstat: every child process the daemon starts, by call-site tag — the total, the
// forks of the last minute / 5 minutes / hour, and the summed wall time (host-probe dedupe plan D0). It reaches the mux only
// through the daemon's general chain, whose TokenAuth knows the admin token alone, and the device scope refuses a paired phone
// every pattern it does not list (this one is not listed); like the profiler, it is read and never written.
func handleExecstat(w http.ResponseWriter, _ *http.Request) {
	tags := execstat.Tags()
	var total, l1, l5, l60 int64
	for _, t := range tags {
		total += t.Total
		l1 += t.Last1
		l5 += t.Last5
		l60 += t.Last60
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"at":   time.Now().UTC().Format(time.RFC3339),
		"tags": tags,
		"sum":  map[string]int64{"total": total, "last_1m": l1, "last_5m": l5, "last_60m": l60},
	})
}

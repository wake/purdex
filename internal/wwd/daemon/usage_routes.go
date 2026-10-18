package daemon

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// UsageStore reads the usage of finished jobs (store.Store).
type UsageStore interface {
	LoadUsage(since time.Time, after string, limit int) ([]wire.JobUsage, bool, error)
}

// The reads of GET /v1/usage: the default period, and the page size (the default, and the most a caller may ask for).
const (
	usageDefaultSince = 24 * time.Hour
	usageDefaultLimit = 200
	usageMaxLimit     = 1000
)

// usageRoutes registers the usage read on the local socket only: the rows are for the host's own analysis, the TCP surface (peers)
// never serves them. It is not forwarded to another host either; each host answers for itself.
func (d *Daemon) usageRoutes(mux *http.ServeMux) {
	if d.opts.Usage == nil {
		return
	}
	mux.HandleFunc("GET /v1/usage", d.handleUsage)
}

func (d *Daemon) handleUsage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if h := q.Get("host"); h != "" && h != d.opts.Alias {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "usage is read from the host itself: ask that host's socket"})
		return
	}
	now := d.now()
	since := now.Add(-usageDefaultSince)
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "since: " + err.Error()})
			return
		}
		since = t
	}
	since = maxTime(since, now.Add(-store.UsageRetention)) // nothing older is kept
	after := q.Get("after")                                // a job id: the page starts after that job (with since = when it ended)
	limit := usageDefaultLimit
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > usageMaxLimit {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a whole number from 1 to " + strconv.Itoa(usageMaxLimit)})
			return
		}
		limit = n
	}
	rows, more, err := d.opts.Usage.LoadUsage(since, after, limit)
	var skipped *store.SkippedRowsError
	if err != nil && !errors.As(err, &skipped) { // a row that does not decode is left out; any other failure is an error
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := wire.UsageResponse{Host: d.opts.Alias, Since: since, Records: rows, Truncated: more}
	if resp.Records == nil {
		resp.Records = []wire.JobUsage{}
	}
	if more && len(rows) > 0 {
		// The next page starts after the last row: its end time and its job id, so jobs that ended in the same millisecond are
		// neither repeated nor skipped, however small the page.
		next, last := rows[len(rows)-1].EndedAt, rows[len(rows)-1].JobID
		resp.NextSince, resp.NextAfter = &next, &last
	} else {
		resp.Truncated = false
	}
	writeJSON(w, http.StatusOK, resp)
}

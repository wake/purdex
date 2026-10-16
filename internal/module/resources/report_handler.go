package resourcesmod

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// defaultReportSince is the period of a report asked with no since.
const defaultReportSince = 24 * time.Hour

// ReportMinutes reads the minute rows in [since, until), as the report reads them. Read only.
func (s *leaseStore) ReportMinutes(since, until int64) ([]resources.ReportMinute, error) {
	// heavy_held is the retired lease's column (written 0 from L2 on, kept in the rows before); it is read apart from the gate's
	// heavy_running, whose figures it must not be mixed into.
	mrows, err := s.db.Query(`SELECT at, load1, mem, full_ticks, full_starts, full_longest_s, heavy_running,
			light_running, gate_waiting, heavy_held, sampled_s, normal_s, warning_s, danger_s
		FROM host_minutes WHERE at >= ? AND at < ? ORDER BY at`, since, until)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	var out []resources.ReportMinute
	for mrows.Next() {
		var m resources.ReportMinute
		if err := mrows.Scan(&m.At, &m.Load1, &m.Mem, &m.FullTicks, &m.FullStarts, &m.FullLongestS, &m.HeavyRunning,
			&m.LightRunning, &m.GateWaiting, &m.HeavyHeld, &m.SampledS, &m.NormalS, &m.WarningS, &m.DangerS); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, mrows.Err()
}

// parseReportSince reads ?since=: a Go duration, or whole days as 7d. At most
// resources.MaxReportSince hours; empty is a day.
func parseReportSince(q string) (time.Duration, string) {
	if q == "" {
		return defaultReportSince, ""
	}
	var d time.Duration
	if days, ok := strings.CutSuffix(q, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, "since must be a positive duration such as 24h, 90m or 7d"
		}
		if n > resources.MaxReportSince/24 { // before the multiplication, which would overflow
			return 0, "since is at most 14 days (the rows are kept that long)"
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(q); err != nil || d <= 0 {
			return 0, "since must be a positive duration such as 24h, 90m or 7d"
		}
	}
	if d > resources.MaxReportSince*time.Hour {
		return 0, "since is at most 14 days (the rows are kept that long)"
	}
	return d, ""
}

// handleReport is GET /api/resources/report?since=<duration>: the monitoring
// report, worked out from the stored host minutes (it never samples).
func (m *Module) handleReport(w http.ResponseWriter, r *http.Request) {
	since, msg := parseReportSince(r.URL.Query().Get("since"))
	if msg != "" {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, msg)
		return
	}
	if m.store == nil {
		writeErr(w, http.StatusServiceUnavailable, resources.ErrNotReady, "resources.db is not open")
		return
	}
	now := m.now()
	from, until := now.Add(-since).UnixMilli(), now.UnixMilli()+1
	minutes, err := m.store.ReportMinutes(from, until)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read the report rows: "+err.Error())
		return
	}
	report := resources.BuildReport(from, now.UnixMilli(), minutes)
	writeJSON(w, http.StatusOK, report)
}

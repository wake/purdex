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

// ReportRows reads the lease rows created in [since, until) and the minute
// rows in the same period, as the report reads them. Read only.
func (s *leaseStore) ReportRows(since, until int64) ([]resources.ReportLease, []resources.ReportMinute, error) {
	lrows, err := s.db.Query(`SELECT kind, weight, created_at, COALESCE(granted_at, 0), dec_recorded, dec_path,
			COALESCE(waited_ms, 0), would_wait_r2, COALESCE(peak_use, 0), COALESCE(mean_use, 0), samples
		FROM resource_leases WHERE created_at >= ? AND created_at < ? ORDER BY created_at, id`, since, until)
	if err != nil {
		return nil, nil, err
	}
	defer lrows.Close()
	var leases []resources.ReportLease
	for lrows.Next() {
		var l resources.ReportLease
		var recorded, r2 int
		if err := lrows.Scan(&l.Kind, &l.Weight, &l.CreatedAt, &l.GrantedAt, &recorded, &l.Path, &l.WaitedMS, &r2, &l.PeakUse, &l.MeanUse, &l.Samples); err != nil {
			return nil, nil, err
		}
		l.Recorded, l.WouldWaitR2 = recorded != 0, r2 != 0
		leases = append(leases, l)
	}
	if err := lrows.Err(); err != nil {
		return nil, nil, err
	}
	minutes, err := s.reportMinutes(since, until)
	return leases, minutes, err
}

func (s *leaseStore) reportMinutes(since, until int64) ([]resources.ReportMinute, error) {
	mrows, err := s.db.Query(`SELECT at, load1, mem, full_ticks, full_starts, full_longest_s, heavy_held
		FROM host_minutes WHERE at >= ? AND at < ? ORDER BY at`, since, until)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	var out []resources.ReportMinute
	for mrows.Next() {
		var m resources.ReportMinute
		if err := mrows.Scan(&m.At, &m.Load1, &m.Mem, &m.FullTicks, &m.FullStarts, &m.FullLongestS, &m.HeavyHeld); err != nil {
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
// report of spec D-8.3, worked out from the stored rows (it never samples).
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
	leases, minutes, err := m.store.ReportRows(from, until)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read the report rows: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resources.BuildReport(from, now.UnixMilli(), leases, minutes))
}

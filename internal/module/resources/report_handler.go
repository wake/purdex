package resourcesmod

import (
	"encoding/json"
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
			COALESCE(waited_ms, 0), would_wait_r2, COALESCE(peak_use, 0), COALESCE(mean_use, 0), samples,
			parks, dec_zone, dec_trend, wait_reasons, v2_wait_ms, v2_wait_open
		FROM resource_leases WHERE created_at >= ? AND created_at < ? ORDER BY created_at, id`, since, until)
	if err != nil {
		return nil, nil, err
	}
	defer lrows.Close()
	var leases []resources.ReportLease
	for lrows.Next() {
		var l resources.ReportLease
		var recorded, r2, open int
		var reasons string
		if err := lrows.Scan(&l.Kind, &l.Weight, &l.CreatedAt, &l.GrantedAt, &recorded, &l.Path, &l.WaitedMS, &r2, &l.PeakUse, &l.MeanUse, &l.Samples,
			&l.Parks, &l.Zone, &l.Trend, &reasons, &l.V2WaitMS, &open); err != nil {
			return nil, nil, err
		}
		l.Recorded, l.WouldWaitR2, l.V2Open = recorded != 0, r2 != 0, open != 0
		if reasons != "" {
			_ = json.Unmarshal([]byte(reasons), &l.Reasons) // a malformed text reads as no reasons
		}
		leases = append(leases, l)
	}
	if err := lrows.Err(); err != nil {
		return nil, nil, err
	}
	minutes, err := s.reportMinutes(since, until)
	return leases, minutes, err
}

// ReportNested sums the nested answers of the leases created in [since, until).
func (s *leaseStore) ReportNested(since, until int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COALESCE(SUM(nested_runs), 0) FROM resource_leases WHERE created_at >= ? AND created_at < ?`,
		since, until).Scan(&n)
	return n, err
}

// ReportPositions reads the positions created in [since, until).
func (s *leaseStore) ReportPositions(since, until int64) ([]resources.ReportPosition, error) {
	rows, err := s.db.Query(`SELECT end_reason, commands_started, requeued_from, extensions, created_at, offered_at,
			delivered_at, offer_passes, offer_wait_ms, idle_wait_ms, notice_result
		FROM resource_positions WHERE created_at >= ? AND created_at < ? ORDER BY created_at, id`, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []resources.ReportPosition
	for rows.Next() {
		var p resources.ReportPosition
		if err := rows.Scan(&p.EndReason, &p.CommandsStarted, &p.RequeuedFrom, &p.Extensions, &p.CreatedAt, &p.OfferedAt,
			&p.DeliveredAt, &p.OfferPasses, &p.OfferWaitMS, &p.IdleWaitMS, &p.NoticeResult); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *leaseStore) reportMinutes(since, until int64) ([]resources.ReportMinute, error) {
	// heavy_held is the lease's column (written 0 from L2 on); heavy_running is the gate's: a minute of either kind counts, so the
	// report does not lose the minutes recorded after L2 while it is being moved to the gate's columns.
	mrows, err := s.db.Query(`SELECT at, load1, mem, full_ticks, full_starts, full_longest_s, max(heavy_held, heavy_running),
			sampled_s, normal_s, warning_s, danger_s
		FROM host_minutes WHERE at >= ? AND at < ? ORDER BY at`, since, until)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	var out []resources.ReportMinute
	for mrows.Next() {
		var m resources.ReportMinute
		if err := mrows.Scan(&m.At, &m.Load1, &m.Mem, &m.FullTicks, &m.FullStarts, &m.FullLongestS, &m.HeavyHeld,
			&m.SampledS, &m.NormalS, &m.WarningS, &m.DangerS); err != nil {
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
	positions, err := m.store.ReportPositions(from, until)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read the position rows: "+err.Error())
		return
	}
	report := resources.BuildReport(from, now.UnixMilli(), leases, minutes)
	report.Positions = resources.BuildPositionReport(positions)
	if report.Positions.Nested, err = m.store.ReportNested(from, until); err != nil {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read the nested counts: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

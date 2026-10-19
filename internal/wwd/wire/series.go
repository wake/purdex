package wire

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"time"
)

// A series is a host's history cut down to what a chart draws (GET /v1/facts/series; the Purdex daemon's GET /api/pool/facts/series
// gives the same points). A whole facts record is about 7 KB; a point here is about 200 bytes. The host that has the data cuts, so a
// day of 1 m points does not cross the tailnet as 10 MB of records.

// SeriesMaxPoints bounds a series answer: wwd keeps its per-minute rollups for 14 days (20160 points), and a little more.
const SeriesMaxPoints = 21600

// SeriesPoint is one moment of a host: the readings a chart draws, taken from one facts record.
type SeriesPoint struct {
	At             time.Time `json:"at"`
	Zone           Zone      `json:"zone"`
	CPUIdlePct     float64   `json:"cpu_idle_pct"`
	MemAvailableMB int       `json:"mem_available_mb"`
	Pressure       int       `json:"pressure"` // 0 when unknown
	PageOutRate    *float64  `json:"page_out_rate"`
	DiskMaxUsedPct *float64  `json:"disk_max_used_pct"` // null when the record names no disk
	Headroom       Slots     `json:"headroom"`
}

// FactsSeriesResponse is the answer: the step that was served, the points oldest first, and Truncated when the oldest ones were left
// out to stay within the limit asked for.
type FactsSeriesResponse struct {
	Step      string        `json:"step"`
	Points    []SeriesPoint `json:"points"`
	Truncated bool          `json:"truncated"`
}

func (r FactsSeriesResponse) Validate() error {
	if r.Step != Step1m && r.Step != Step5s {
		return fmt.Errorf("series: invalid step %q", r.Step)
	}
	if len(r.Points) > SeriesMaxPoints {
		return fmt.Errorf("series: %d points is over the limit of %d", len(r.Points), SeriesMaxPoints)
	}
	return nil
}

// FactsSeriesQuery is GET /v1/facts/series: the history query and the most points to answer (the latest are kept).
type FactsSeriesQuery struct {
	FactsHistoryQuery
	Limit int
}

// ParseFactsSeriesQuery parses since and step as the history does, and limit (1 to SeriesMaxPoints; absent or empty is the maximum).
func ParseFactsSeriesQuery(v url.Values) (FactsSeriesQuery, error) {
	h, err := ParseFactsHistoryQuery(v)
	if err != nil {
		return FactsSeriesQuery{}, err
	}
	q := FactsSeriesQuery{FactsHistoryQuery: h, Limit: SeriesMaxPoints}
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > SeriesMaxPoints {
			return FactsSeriesQuery{}, fmt.Errorf("series: limit must be 1 to %d", SeriesMaxPoints)
		}
		q.Limit = n
	}
	return q, nil
}

func (q FactsSeriesQuery) Values() url.Values {
	v := q.FactsHistoryQuery.Values()
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	return v
}

// SeriesOf projects a history onto points and keeps the latest limit of them. The cut is by time, not by position: wwd answers oldest
// first, but a record out of order must not decide which points are lost.
func SeriesOf(h FactsHistoryResponse, limit int) FactsSeriesResponse {
	recs := h.Records
	if !slices.IsSortedFunc(recs, func(a, b Facts) int { return a.At.Compare(b.At) }) {
		recs = slices.Clone(recs)
		slices.SortStableFunc(recs, func(a, b Facts) int { return a.At.Compare(b.At) })
	}
	out := FactsSeriesResponse{Step: h.Step, Points: make([]SeriesPoint, 0, min(len(recs), limit))}
	if len(recs) > limit {
		recs, out.Truncated = recs[len(recs)-limit:], true
	}
	for _, f := range recs {
		p := SeriesPoint{
			At: f.At, Zone: f.Derived.Zone, CPUIdlePct: f.CPU.IdlePct, MemAvailableMB: f.Mem.AvailableMB, Pressure: f.Mem.Pressure,
			PageOutRate: f.Mem.PageOutRate, Headroom: f.Derived.Headroom,
		}
		for i, d := range f.Disk {
			if i == 0 || d.UsedPct > *p.DiskMaxUsedPct {
				u := d.UsedPct
				p.DiskMaxUsedPct = &u
			}
		}
		out.Points = append(out.Points, p)
	}
	return out
}

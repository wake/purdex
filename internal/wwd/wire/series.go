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

	// What a record already carries, cut down for a chart (spec §2.3). Each is null when the record does not say.
	Load1          *float64     `json:"load1"`
	Load5          *float64     `json:"load5"`
	SwapUsedMB     *int         `json:"swap_used_mb"`
	CompressorMB   *int         `json:"compressor_mb"`
	SwapOutRate    *float64     `json:"swap_out_rate"`
	DecompressRate *float64     `json:"decompress_rate"`
	WorkerRunning  *Slots       `json:"worker_running"`
	QueueQueued    *int         `json:"queue_queued"`
	Disks          []SeriesDisk `json:"disks"`
}

// SeriesDisk is one volume of a point: used_pct for the line, free_gb for the disk_min_gb threshold line.
type SeriesDisk struct {
	Mount   string  `json:"mount"`
	UsedPct float64 `json:"used_pct"`
	FreeGB  float64 `json:"free_gb"`
}

// copyPtr gives a point its own copy, so it never points into the record it was cut from.
func copyPtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
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
	for i, p := range r.Points { // what a full history refuses record by record (Facts.Validate), so does a series point by point
		if !p.Zone.Valid() {
			return fmt.Errorf("series: points[%d]: invalid zone %q", i, p.Zone)
		}
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
		load1, load5, running := f.CPU.Load1, f.CPU.Load5, f.Worker.Running
		p.Load1, p.Load5, p.WorkerRunning = &load1, &load5, &running
		p.SwapUsedMB, p.CompressorMB = copyPtr(f.Mem.SwapUsedMB), copyPtr(f.Mem.CompressorMB)
		p.SwapOutRate, p.DecompressRate = copyPtr(f.Mem.SwapOutRate), copyPtr(f.Mem.DecompressRate)
		if f.Queue != nil {
			p.QueueQueued = copyPtr(&f.Queue.Queued)
		}
		for _, d := range f.Disk {
			p.Disks = append(p.Disks, SeriesDisk{Mount: d.Mount, UsedPct: d.UsedPct, FreeGB: d.FreeGB})
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

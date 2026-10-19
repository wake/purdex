package poolmod

import (
	"context"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// A series is the history of a host cut down to what a chart draws (GET /api/pool/facts/series, capability pool.series.v1). The full
// history is a whole facts record per point, about 7 KB: an hour at 5 s is about 5 MB and a day at 1 m about 10 MB. A point here is
// about 200 bytes.
const (
	// seriesMaxPoints bounds the answer: more than this and the latest points are kept and "truncated" says so. 24 h at 1 m is 1440
	// and 2 h at 5 s is 1440, so a chart of either fits whole.
	seriesMaxPoints = 2000
	// seriesMaxSince is how far back a series may ask: wwd keeps its per-minute rollups for 14 days (store.MinuteRetention).
	seriesMaxSince = 14 * 24 * time.Hour
	// seriesDefaultSince is the window when the request names none.
	seriesDefaultSince = time.Hour
	// seriesFineWindow is the longest window that defaults to the 5 s step; longer ones default to 1 m.
	seriesFineWindow = 2 * time.Hour
)

// SeriesPoint and SeriesResponse are wire's: wwd cuts the series itself (GET /v1/facts/series) and this daemon cuts it from a full
// history only when the wwd it asks does not have that route.
type (
	SeriesPoint    = wire.SeriesPoint
	SeriesResponse = wire.FactsSeriesResponse
)

// seriesOf projects a history onto points, and keeps the latest seriesMaxPoints of them.
func seriesOf(h wire.FactsHistoryResponse) SeriesResponse { return wire.SeriesOf(h, seriesMaxPoints) }

// parseSince reads a since that is an RFC 3339 time or a duration back from now ("1h", "24h", "90m"). The default is cut to the minute
// so that readers asking within the same minute share one key.
func (m *Module) parseSince(s string) (time.Time, bool) {
	if s == "" {
		return m.now().Add(-seriesDefaultSince).UTC().Truncate(time.Minute), true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		if age := m.now().Sub(t); age < 0 || age > seriesMaxSince {
			return time.Time{}, false
		}
		return t, true
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > seriesMaxSince {
		return time.Time{}, false
	}
	return m.now().Add(-d).UTC().Truncate(time.Minute), true
}

func (m *Module) handleSeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, ok := m.parseSince(q.Get("since"))
	if !ok {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "since must be RFC 3339 or a duration up to 336h (14 days), like 1h or 24h")
		return
	}
	step := q.Get("step")
	if step == "" {
		step = wire.Step1m
		if m.now().Sub(since) <= seriesFineWindow {
			step = wire.Step5s
		}
	}
	if step != wire.Step1m && step != wire.Step5s {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "step must be 1m or 5s")
		return
	}
	host := q.Get("host")
	key := "series|" + host + "|" + since.Format(time.RFC3339) + "|" + step
	m.serve(w, r, key, func(ctx context.Context) (any, *apiError) {
		hq := wire.FactsHistoryQuery{Since: since, Step: step, Host: host}
		// the host that has the data cuts the series (a day of 1 m points is 10 MB of records, more than a peer's answer may be);
		// a wwd without that route (an older release, here or on the peer) answers 404, and then the full history is fetched and cut here
		sr, err := m.client().FactsSeries(ctx, wire.FactsSeriesQuery{FactsHistoryQuery: hq, Limit: seriesMaxPoints})
		if err == nil {
			// the 2000-point contract of this route is held here too, whatever a wwd of another release answered to the limit
			if over := len(sr.Points) - seriesMaxPoints; over > 0 {
				sr.Points, sr.Truncated = sr.Points[over:], true
			}
			return sr, nil
		}
		if !client.RouteMissing(err) {
			return nil, historyError(err)
		}
		h, err := m.client().FactsHistory(ctx, hq)
		if err != nil {
			return nil, historyError(err)
		}
		return seriesOf(h), nil
	})
}

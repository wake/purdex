package daemon

import (
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

var ut = time.Date(2026, 10, 18, 14, 0, 0, 0, time.UTC)

func fl(v float64) *float64 { return &v }
func in(v int) *int         { return &v }

func usageRow(id string, end time.Time) wire.JobUsage {
	no := false
	return wire.JobUsage{
		JobID: id, Host: "air26", Repo: "purdex", Task: "go-test", Slot: wire.SlotHeavy, Class: wire.ClassDev,
		Source: wire.UsageQueue, Result: wire.StateSucceeded, StartedAt: end.Add(-time.Minute), EndedAt: end,
		CPUSeconds: fl(120), CPUSource: wire.CPUFromRusage, PeakCores: fl(3), PeakCoresMeasure: wire.MeasureSampled,
		MemPeakMB: in(900), MemPeakMeasure: wire.MeasureSampled, JobsPeak: 1, ZoneAtStart: wire.ZoneNormal, ZoneWorst: wire.ZoneNormal,
		OwnerAtStart: &no, OwnerSeen: &no,
	}
}

type fakeUsage struct {
	rows []wire.JobUsage
	err  error
}

func (f *fakeUsage) LoadUsage(since time.Time, after string, limit int) ([]wire.JobUsage, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	var out []wire.JobUsage
	for _, r := range f.rows { // the rows are given in (ended_at, job_id) order
		switch {
		case after == "" && r.EndedAt.Before(since):
		case after != "" && (r.EndedAt.Before(since) || (r.EndedAt.Equal(since) && r.JobID <= after)):
		default:
			out = append(out, r)
		}
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

func usageDaemon(rows ...wire.JobUsage) *Daemon {
	return New(Options{HostID: "id", Alias: "air26", Version: "v0", Token: testToken, Usage: &fakeUsage{rows: rows},
		Now: func() time.Time { return ut.Add(24 * time.Hour) }})
}

func TestUsageRouteAnswersOnTheSocketOnly(t *testing.T) {
	d := usageDaemon(usageRow("j1", ut.Add(2*time.Hour)))
	w := do(d.SocketHandler(), "GET", "/v1/usage", "")
	r, err := wire.Decode[wire.UsageResponse](w.Body.Bytes())
	if w.Code != 200 || err != nil || r.Host != "air26" || len(r.Records) != 1 || r.Truncated {
		t.Fatalf("%d %s (%v)", w.Code, w.Body, err)
	}
	// TCP does not have it, token or not
	for _, auth := range []string{"", "Bearer " + testToken} {
		if w := do(d.TCPHandler(), "GET", "/v1/usage", auth); w.Code == 200 {
			t.Errorf("TCP answered usage (auth %q): %d", auth, w.Code)
		}
	}
}

func TestUsageRouteDefaultsAndLimits(t *testing.T) {
	var rows []wire.JobUsage
	for i := 0; i < 5; i++ {
		rows = append(rows, usageRow(fmt.Sprintf("j%d", i), ut.Add(time.Duration(i+1)*time.Hour)))
	}
	d := usageDaemon(rows...)
	// the default period is the last day: since = now - 24 h = ut, every row is in it
	r, _ := wire.Decode[wire.UsageResponse](do(d.SocketHandler(), "GET", "/v1/usage", "").Body.Bytes())
	if !r.Since.Equal(ut) || len(r.Records) != 5 {
		t.Fatalf("since %v, %d rows", r.Since, len(r.Records))
	}
	q := url.Values{"since": {ut.Format(time.RFC3339)}, "limit": {"2"}}
	r, err := wire.Decode[wire.UsageResponse](do(d.SocketHandler(), "GET", "/v1/usage?"+q.Encode(), "").Body.Bytes())
	if err != nil || len(r.Records) != 2 || !r.Truncated || r.NextSince == nil || !r.NextSince.Equal(rows[1].EndedAt) || r.NextAfter == nil || *r.NextAfter != "j1" {
		t.Fatalf("%+v %v", r, err)
	}
	// the next page starts right after the last row: nothing twice, nothing skipped
	q.Set("since", r.NextSince.Format(time.RFC3339Nano))
	q.Set("after", *r.NextAfter)
	r2, _ := wire.Decode[wire.UsageResponse](do(d.SocketHandler(), "GET", "/v1/usage?"+q.Encode(), "").Body.Bytes())
	if len(r2.Records) != 2 || r2.Records[0].JobID != "j2" || r2.Records[1].JobID != "j3" || !r2.Truncated {
		t.Fatalf("%+v", r2)
	}
}

// Jobs that ended at the same moment are paged through one at a time, however small the page.
func TestUsageRoutePagesThroughTies(t *testing.T) {
	d := usageDaemon(usageRow("j_a", ut.Add(time.Hour)), usageRow("j_b", ut.Add(time.Hour)), usageRow("j_c", ut.Add(time.Hour)))
	var seen []string
	q := url.Values{"since": {ut.Format(time.RFC3339)}, "limit": {"1"}}
	for page := 0; page < 6; page++ {
		r, err := wire.Decode[wire.UsageResponse](do(d.SocketHandler(), "GET", "/v1/usage?"+q.Encode(), "").Body.Bytes())
		if err != nil || len(r.Records) != 1 {
			t.Fatalf("page %d: %+v %v", page, r, err)
		}
		seen = append(seen, r.Records[0].JobID)
		if !r.Truncated {
			break
		}
		q.Set("since", r.NextSince.Format(time.RFC3339Nano))
		q.Set("after", *r.NextAfter)
	}
	if fmt.Sprint(seen) != "[j_a j_b j_c]" {
		t.Fatalf("%v", seen)
	}
}

func TestUsageRouteRefusesWhatItCannotAnswer(t *testing.T) {
	d := usageDaemon(usageRow("j1", ut.Add(2*time.Hour)))
	for name, path := range map[string]string{
		"a bad since":          "/v1/usage?since=yesterday",
		"a zero limit":         "/v1/usage?limit=0",
		"a negative limit":     "/v1/usage?limit=-3",
		"a limit too large":    "/v1/usage?limit=100000",
		"a limit not a number": "/v1/usage?limit=many",
		"another host":         "/v1/usage?host=mlab",
	} {
		if w := do(d.SocketHandler(), "GET", path, ""); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// the host's own alias is fine
	if w := do(d.SocketHandler(), "GET", "/v1/usage?host=air26", ""); w.Code != 200 {
		t.Errorf("own alias: %d", w.Code)
	}
}

func TestUsageRouteSinceNeverReachesBackFartherThanTheRetention(t *testing.T) {
	d := usageDaemon()
	r, err := wire.Decode[wire.UsageResponse](do(d.SocketHandler(), "GET", "/v1/usage?since=1970-01-01T00:00:00Z", "").Body.Bytes())
	if err != nil || !r.Since.Equal(ut.Add(24*time.Hour).Add(-store.UsageRetention)) {
		t.Fatalf("%v %v", r.Since, err)
	}
}

func TestUsageRouteAnEmptyAnswerIsAnEmptyList(t *testing.T) {
	w := do(usageDaemon().SocketHandler(), "GET", "/v1/usage", "")
	if w.Code != 200 || !contains(w.Body.String(), `"records":[]`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestUsageRouteStoreFailureIsA500AndNothingIsInvented(t *testing.T) {
	d := New(Options{HostID: "id", Alias: "air26", Version: "v0", Token: testToken, Usage: &fakeUsage{err: errors.New("disk")}})
	if w := do(d.SocketHandler(), "GET", "/v1/usage", ""); w.Code != 500 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestUsageRouteIsNotRegisteredWithoutAStore(t *testing.T) {
	d := New(Options{HostID: "id", Alias: "air26", Version: "v0", Token: testToken})
	if w := do(d.SocketHandler(), "GET", "/v1/usage", ""); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
}

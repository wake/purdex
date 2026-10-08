package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

var updateReportGolden = flag.Bool("update-report", false, "rewrite the golden report text")

// The daemon's fixture day (its golden JSON) is the input; the text is the
// golden, byte for byte, in a fixed zone.
func TestReport_TextOfTheFixtureDayExactly(t *testing.T) {
	b, err := os.ReadFile("../../internal/module/resources/testdata/report-day.json")
	if err != nil {
		t.Fatal(err)
	}
	var rep resources.Report
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	formatReport(&out, rep, time.UTC)
	if *updateReportGolden {
		if err := os.WriteFile("testdata/report-day.txt", out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/report-day.txt")
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != string(want) {
		t.Errorf("text differs:\n--- got\n%s\n--- want\n%s", out.String(), want)
	}
}

// An empty report says so instead of printing zeros as if they were data.
func TestReport_EmptyText(t *testing.T) {
	var out bytes.Buffer
	formatReport(&out, resources.Report{Since: 0, Until: 3600000, Requests: resources.ReportRequests{ByKind: []resources.ReportKindCount{}}, Kinds: []resources.ReportKind{}}, time.UTC)
	if !strings.Contains(out.String(), "no minute rows in the period") || strings.Contains(out.String(), "KIND") {
		t.Errorf("empty report:\n%s", out.String())
	}
}

type fakeReportAPI struct {
	since string
	body  any
	code  int
}

func (f *fakeReportAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/health" {
		_, _ = w.Write([]byte(`{"ok":true,"boot_id":"b1"}`))
		return
	}
	f.since = r.URL.Query().Get("since")
	write(w, answer{status: f.code, body: f.body})
}

func driveLeaseReport(t *testing.T, d *fakeReportAPI, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runLeaseCmd(context.Background(), append([]string{"report"}, append(args, "--config", cfg)...), fakeGetenv(nil), &stdout, &stderr, leadClockOpt(), leadNoKeepAlive())
	return code, stdout.String(), stderr.String()
}

// --json is the daemon's body, one line; the shape is the Report type's.
func TestReport_JSONAndSinceAreSent(t *testing.T) {
	rep := resources.Report{Since: 1, Until: 2, Requests: resources.ReportRequests{Total: 3, ByKind: []resources.ReportKindCount{{Kind: "build", Count: 3}}}, Kinds: []resources.ReportKind{}}
	d := &fakeReportAPI{body: rep}
	code, stdout, _ := driveLeaseReport(t, d, "--since", "7d", "--json")
	var back resources.Report
	if code != ExitOK || d.since != "7d" || strings.Count(stdout, "\n") != 1 || json.Unmarshal([]byte(stdout), &back) != nil || back.Requests.Total != 3 {
		t.Fatalf("code=%d since=%q stdout=%q", code, d.since, stdout)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(stdout), &raw)
	for _, k := range []string{"since", "until", "coverage", "not_recorded", "requests", "paths", "wait_ms", "would_wait_r2", "full", "heavy", "kinds"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("field %q missing from the JSON", k)
		}
	}
	// Default period is a day; a refusal is an error line.
	d = &fakeReportAPI{body: rep}
	driveLeaseReport(t, d)
	if d.since != "24h" {
		t.Errorf("default since = %q", d.since)
	}
	d = &fakeReportAPI{code: 400, body: resources.APIError{Error: resources.ErrBadRequest, Detail: "since is at most 14 days"}}
	if code, _, stderr := driveLeaseReport(t, d, "--since", "30d"); code == ExitOK || !strings.Contains(stderr, "since is at most 14 days") {
		t.Errorf("refusal: code=%d stderr=%q", code, stderr)
	}
	if code, _, _ := driveLeaseReport(t, &fakeReportAPI{}, "stray"); code != ExitUsage {
		t.Errorf("stray argument: %d", code)
	}
}

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

// The daemon's fixture day (its golden JSON) is the input; the text is the golden, byte for byte, in a fixed zone.
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
	formatReport(&out, resources.Report{Since: 0, Until: 3600000}, time.UTC)
	if !strings.Contains(out.String(), "no minute rows in the period") || strings.Contains(out.String(), "heavy") {
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

func driveHostReport(t *testing.T, d *fakeReportAPI, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runHostCmd(context.Background(), append([]string{"report"}, append(args, "--config", cfg)...), &stdout, &stderr, leadClockOpt(), leadNoKeepAlive())
	return code, stdout.String(), stderr.String()
}

// --json is the daemon's body, one line; the shape is the Report type's.
func TestReport_JSONAndSinceAreSent(t *testing.T) {
	rep := resources.Report{Since: 1, Until: 2, Heavy: resources.ReportHeavy{Minutes: 3}}
	d := &fakeReportAPI{body: rep}
	code, stdout, _ := driveHostReport(t, d, "--since", "7d", "--json")
	var back resources.Report
	if code != ExitOK || d.since != "7d" || strings.Count(stdout, "\n") != 1 || json.Unmarshal([]byte(stdout), &back) != nil || back.Heavy.Minutes != 3 {
		t.Fatalf("code=%d since=%q stdout=%q", code, d.since, stdout)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(stdout), &raw)
	for _, k := range []string{"since", "until", "coverage", "full", "heavy", "light", "gate", "zones"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("field %q missing from the JSON", k)
		}
	}
	// The lease's figures are gone from the wire.
	for _, k := range []string{"requests", "paths", "wait_ms", "kinds", "positions", "shadow"} {
		if _, ok := raw[k]; ok {
			t.Errorf("field %q is still in the JSON", k)
		}
	}
	// Default period is a day; a refusal is an error line.
	d = &fakeReportAPI{body: rep}
	driveHostReport(t, d)
	if d.since != "24h" {
		t.Errorf("default since = %q", d.since)
	}
	d = &fakeReportAPI{code: 400, body: resources.APIError{Error: resources.ErrBadRequest, Detail: "since is at most 14 days"}}
	if code, _, stderr := driveHostReport(t, d, "--since", "30d"); code == ExitOK || !strings.Contains(stderr, "since is at most 14 days") {
		t.Errorf("refusal: code=%d stderr=%q", code, stderr)
	}
	if code, _, _ := driveHostReport(t, &fakeReportAPI{}, "stray"); code != ExitUsage {
		t.Errorf("stray argument: %d", code)
	}
}

// `pdx lease report` points at the new verb and asks the daemon nothing.
func TestLeaseReport_PointsAtHostReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runLeaseCmd(context.Background(), []string{"report"}, fakeGetenv(nil), &stdout, &stderr)
	if code != ExitUsage || stdout.Len() != 0 || !strings.Contains(stderr.String(), "pdx host report") {
		t.Errorf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// pdx host with no or an unknown verb is a usage error.
func TestHost_UnknownVerb(t *testing.T) {
	for _, args := range [][]string{nil, {"ls"}} {
		var stdout, stderr bytes.Buffer
		if code := runHostCmd(context.Background(), args, &stdout, &stderr); code != ExitUsage || !strings.Contains(stderr.String(), "pdx host report") {
			t.Errorf("%v: code=%d stderr=%q", args, code, stderr.String())
		}
	}
}

// A reason name cannot pass for the output's own structure.
func TestReport_ReasonNamesAreQuotedWhenNotPlain(t *testing.T) {
	for in, want := range map[string]string{"": "(none)", "gap": "gap", "a:b": "a:b", "x 0), y 9 (": `"x 0), y 9 ("`, "a\x1b[31mb": `"a\x1b[31mb"`} {
		if got := reportReasonName(in); got != want {
			t.Errorf("%q -> %s, want %s", in, got, want)
		}
	}
}

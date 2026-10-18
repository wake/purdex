package jobscmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func fp(v float64) *float64 { return &v }
func ip(v int) *int         { return &v }

func usageRec(id string, end time.Time) wire.JobUsage {
	yes, no := true, false
	return wire.JobUsage{
		JobID: id, Host: "air26", Repo: "purdex", Task: "go-test", Slot: wire.SlotHeavy, Class: wire.ClassDev,
		Source: wire.UsageFastLane, Result: wire.StateSucceeded, StartedAt: end.Add(-90 * time.Second), EndedAt: end,
		CPUSeconds: fp(270), CPUSource: wire.CPUSampled, PeakCores: fp(5.5), PeakCoresMeasure: wire.MeasureSampled,
		MemPeakMB: ip(2100), MemPeakMeasure: wire.MeasureSampled, JobsAtStart: 1, JobsPeak: 3, ZoneAtStart: wire.ZoneNormal,
		ZoneWorst: wire.ZoneWarning, OwnerAtStart: &no, OwnerSeen: &yes,
	}
}

func TestWWDUsagePrintsATableThatSaysWhatIsSampled(t *testing.T) {
	u := usageRec("j_01JC", t0.Add(-time.Hour))
	exact := usageRec("j_exact", t0.Add(-30*time.Minute))
	exact.Source, exact.CPUSource, exact.PeakCoresMeasure, exact.MemPeakMeasure = wire.UsageQueue, wire.CPUFromUnit, wire.MeasureExact, wire.MeasureExact
	exact.OwnerAtStart, exact.OwnerSeen = nil, nil
	exact.Partial = false
	u.Partial = true
	f := &fakeAPI{usage: wire.UsageResponse{Host: "air26", Since: t0.Add(-24 * time.Hour), Records: []wire.JobUsage{u, exact}}}
	code, out, errOut := runWWD(f, "usage")
	if code != 0 || errOut != "" {
		t.Fatalf("%d %q", code, errOut)
	}
	for _, want := range []string{"j_01JC", "go-test", "fast_lane", "heavy", "270.0~", "5.5~", "2100~", "partial", "normal>warning", "away>seen"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// the exact row has no mark on its figures, and an owner it cannot know shows as not applicable
	line := lineWith(out, "j_exact")
	if strings.Contains(line, "~") || !strings.Contains(line, "n/a") || strings.Contains(line, "partial") {
		t.Errorf("the exact row: %q", line)
	}
	if !strings.Contains(out, "~ = sampled") {
		t.Errorf("no legend:\n%s", out)
	}
	if !f.usageQ.Since.IsZero() || f.usageQ.Limit != 0 {
		t.Errorf("no option, no query: %+v", f.usageQ)
	}
}

func lineWith(out, s string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

func TestWWDUsageOptions(t *testing.T) {
	f := &fakeAPI{usage: wire.UsageResponse{Host: "air26", Since: t0}}
	// a duration back from now, in days too
	for in, back := range map[string]time.Duration{"6h": 6 * time.Hour, "7d": 7 * 24 * time.Hour, "90m": 90 * time.Minute} {
		if code, _, _ := runWWD(f, "usage", "--since", in, "--limit", "50"); code != 0 || !f.usageQ.Since.Equal(t0.Add(-back)) || f.usageQ.Limit != 50 {
			t.Errorf("--since %s: %d %+v", in, code, f.usageQ)
		}
	}
	if code, _, _ := runWWD(f, "usage", "--since", "2026-10-01T00:00:00+08:00"); code != 0 || !f.usageQ.Since.Equal(time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("RFC 3339: %+v", f.usageQ)
	}
	for _, args := range [][]string{{"usage", "--since", "yesterday"}, {"usage", "--since", "-5h"}, {"usage", "--limit", "0"}, {"usage", "--limit", "x"}, {"usage", "extra"}, {"usage", "--bogus"}} {
		if code, _, errOut := runWWD(f, args...); code != ExitUsage || errOut == "" {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
}

func TestWWDUsageJSONIsTheWholeAnswer(t *testing.T) {
	next, after := t0, "j1"
	f := &fakeAPI{usage: wire.UsageResponse{Host: "air26", Since: t0, Records: []wire.JobUsage{usageRec("j1", t0)}, Truncated: true, NextSince: &next, NextAfter: &after}}
	code, out, _ := runWWD(f, "usage", "--json")
	var got wire.UsageResponse
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || !got.Truncated || got.NextSince == nil || len(got.Records) != 1 {
		t.Fatalf("%d %s", code, out)
	}
}

func TestWWDUsageTellsWhenThereIsMoreAndWhenThereIsNothing(t *testing.T) {
	next, after := t0.Add(-time.Minute), "j1"
	f := &fakeAPI{usage: wire.UsageResponse{Host: "air26", Since: t0, Records: []wire.JobUsage{usageRec("j1", t0)}, Truncated: true, NextSince: &next, NextAfter: &after}}
	_, out, _ := runWWD(f, "usage")
	if !strings.Contains(out, "more") || !strings.Contains(out, next.Format(time.RFC3339Nano)) || !strings.Contains(out, "--after j1") {
		t.Errorf("the next page is named:\n%s", out)
	}
	f = &fakeAPI{usage: wire.UsageResponse{Host: "air26", Since: t0, Records: []wire.JobUsage{}}}
	if _, out, _ := runWWD(f, "usage"); !strings.Contains(out, "no finished jobs") {
		t.Errorf("an empty answer says so:\n%s", out)
	}
}

// A figure nobody measured is a dash, not a 0 that looks measured.
func TestWWDUsageShowsWhatWasNotMeasuredAsADash(t *testing.T) {
	u := usageRec("j_short", t0.Add(-time.Minute))
	u.CPUSeconds, u.CPUSource = nil, wire.CPUNone
	u.PeakCores, u.PeakCoresMeasure = nil, wire.MeasureNone
	u.MemPeakMB, u.MemPeakMeasure = nil, wire.MeasureNone
	f := &fakeAPI{usage: wire.UsageResponse{Host: "air26", Since: t0, Records: []wire.JobUsage{u}}}
	code, out, _ := runWWD(f, "usage")
	line := lineWith(out, "j_short")
	if code != 0 || strings.Contains(line, "0.0") || strings.Count(line, " - ") < 3 {
		t.Fatalf("%d %q", code, line)
	}
}

func TestWWDUsageAfterNeedsTheExactTimeOfTheLastAnswer(t *testing.T) {
	f := &fakeAPI{usage: wire.UsageResponse{Host: "air26", Since: t0}}
	since := "2026-10-18T05:00:00.25Z"
	if code, _, _ := runWWD(f, "usage", "--since", since, "--after", "j9"); code != 0 || f.usageQ.After != "j9" || !f.usageQ.Since.Equal(time.Date(2026, 10, 18, 5, 0, 0, 250_000_000, time.UTC)) {
		t.Fatalf("%d %+v", code, f.usageQ)
	}
	for _, args := range [][]string{{"usage", "--after", "j9"}, {"usage", "--since", "6h", "--after", "j9"}} {
		if code, _, errOut := runWWD(f, args...); code != ExitUsage || errOut == "" {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
}

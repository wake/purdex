package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/wake/purdex/internal/resources"
)

func v2Report() resources.Report {
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC).UnixMilli()
	return resources.Report{
		Since: at, Until: at + 3600000,
		Coverage:     resources.ReportCoverage{From: at, To: at + 3540000, Minutes: 60},
		Requests:     resources.ReportRequests{Total: 3, ByKind: []resources.ReportKindCount{{Kind: "build", Count: 3}}},
		Kinds:        []resources.ReportKind{},
		Zones:        resources.ReportZones{Minutes: 50, SampledS: 3000, Normal: 0.9, Warning: 0.08, Danger: 0.02},
		Parks:        2,
		WaitByReason: []resources.ReportReasonWait{{Reason: "danger", N: 1, P50: 90000, P90: 90000, Max: 90000}, {Reason: "gap", N: 2, P50: 30000, P90: 50000, Max: 50000}},
		Longest: []resources.ReportWaitRow{{Kind: "build", CreatedAt: at, WaitedMS: 90000, Dominant: "danger",
			Reasons: map[string]int64{"danger": 70000, "gap": 20000}, Zone: "warning", Trend: "falling"}},
		Shadow: resources.ReportShadow{N: 4, Open: 1, WaitMS: resources.ReportWait{P50: 30000, P90: 90000, Max: 90000},
			Longest: []resources.ReportShadowRow{{Kind: "build", CreatedAt: at, WaitMS: 90000, Open: true, Dominant: "danger",
				Reasons: map[string]int64{"danger": 90000}}}},
	}
}

func TestReportText_V2Sections(t *testing.T) {
	var out bytes.Buffer
	formatReport(&out, v2Report(), time.UTC)
	got := out.String()
	for _, want := range []string{
		"zones     normal 90.0%  warning 8.0%  danger 2.0%  (50 of 60 minutes counted)",
		"parks     2",
		"wait by   danger  n 1  p50 90.0s  p90 90.0s  max 90.0s",
		"wait by   gap  n 2  p50 30.0s  p90 50.0s  max 50.0s",
		"longest   #1 10-13 12:00  build  90.0s  danger 70.0s, gap 20.0s  (zone warning, trend falling at the grant)",
		"shadow    v2 would have made 4 requests wait: p50 30.0s  p90 90.0s  max 90.0s; 1 are lower bounds (the request ended first)",
		"#1 10-13 12:00  build  90.0s (at least)  danger 90.0s",
	} {
		assert.Contains(t, got, want)
	}
}

// Without zone seconds or shadow verdicts the report says so and prints no
// shadow section; a reason name cannot pass for the report's own structure.
func TestReportText_V2EmptyAndQuoted(t *testing.T) {
	var out bytes.Buffer
	rep := v2Report()
	rep.Zones, rep.Shadow = resources.ReportZones{}, resources.ReportShadow{}
	rep.Longest = []resources.ReportWaitRow{{Kind: "build", WaitedMS: 1000, Reasons: map[string]int64{"gap\n999 sneaky": 1000}}}
	formatReport(&out, rep, time.UTC)
	got := out.String()
	assert.Contains(t, got, "zones     no minute with zone seconds yet")
	assert.NotContains(t, got, "shadow ")
	assert.Contains(t, got, `"gap\n999 sneaky" 1.0s`)
	assert.Equal(t, 1, strings.Count(got, "longest   #"))
	assert.Contains(t, got, "(zone (none), trend (none) at the grant)")
}

// A grant by a position is a grant: the old rule's denominator counts it and the
// two paths are shown. (M: leave them out of the sum.)
func TestFormatReport_PositionPathsCountAsGrants(t *testing.T) {
	var buf bytes.Buffer
	r := resources.Report{WouldWaitR2: 2}
	r.Paths = resources.ReportPaths{Immediate: 1, Offer: 2, Continued: 1}
	formatReport(&buf, r, time.UTC)
	out := buf.String()
	if !strings.Contains(out, "would have held back 2 of 4 grants") {
		t.Errorf("the denominator misses the position paths:\n%s", out)
	}
	if !strings.Contains(out, "first command after an offer 2, continued in an idle window 1") {
		t.Errorf("the position paths are not shown:\n%s", out)
	}
}

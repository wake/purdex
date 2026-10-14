package main

// `pdx lease report` (spec D-8.3, R10): whether the admission rule suits the
// host, from what the daemon stored. It reads the daemon's report; it never
// samples anything itself.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/resources"
)

// runLeaseReport implements `pdx lease report [--since 24h] [--json]`.
func runLeaseReport(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx lease report", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	since := fs.String("since", "24h", "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseTeamFlags(fs, args)
	if err == nil && len(pos) != 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx lease: %v\n%s\n", err, leaseUsage)
		return ExitUsage
	}
	client, ok := leaseClient("lease", *cfgPath, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	var raw json.RawMessage
	if _, err := client.Do(ctx, http.MethodGet, resources.ReportPath+"?since="+url.QueryEscape(*since), nil, &raw); err != nil {
		return teamReportErr("lease", err, stderr)
	}
	var rep resources.Report
	var line bytes.Buffer
	if json.Unmarshal(raw, &rep) != nil || json.Compact(&line, raw) != nil {
		fmt.Fprintln(stderr, "pdx lease: daemon 的回應不是 report invalid_response")
		return ExitError
	}
	if *asJSON {
		fmt.Fprintln(stdout, line.String())
		return ExitOK
	}
	formatReport(stdout, rep, time.Local)
	return ExitOK
}

func reportTime(ms int64, loc *time.Location) string {
	return time.UnixMilli(ms).In(loc).Format("01-02 15:04")
}

// safeKind is what a kind name looks like when settings validation let it in.
var safeKind = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// reportKindName is a kind as the report prints it: "(weight)" for a request
// that named a weight, a validated name as it is, anything else quoted so that
// its commas and brackets cannot pass for the report's own structure.
func reportKindName(k string) string {
	switch {
	case k == "":
		return "(weight)"
	case safeKind.MatchString(k):
		return k
	}
	return strconv.QuoteToASCII(k)
}

func seconds(ms int64) string { return fmt.Sprintf("%.1fs", float64(ms)/1000) }

// formatReport prints the report as plain lines, a section a line or a table.
func formatReport(w io.Writer, r resources.Report, loc *time.Location) {
	fmt.Fprintf(w, "period    %s → %s\n", reportTime(r.Since, loc), reportTime(r.Until, loc))
	if r.Coverage.Minutes == 0 {
		fmt.Fprintf(w, "timeline  no minute rows in the period\n")
	} else {
		fmt.Fprintf(w, "timeline  %d minute rows, %s → %s\n", r.Coverage.Minutes, reportTime(r.Coverage.From, loc), reportTime(r.Coverage.To, loc))
	}
	parts := make([]string, 0, len(r.Requests.ByKind))
	for _, k := range r.Requests.ByKind {
		parts = append(parts, fmt.Sprintf("%s %d", reportKindName(k.Kind), k.Count))
	}
	by := ""
	if len(parts) > 0 {
		by = "  (" + strings.Join(parts, ", ") + ")"
	}
	fmt.Fprintf(w, "requests  %d%s\n", r.Requests.Total, by)
	fmt.Fprintf(w, "let in    at once %d, after waiting %d, over the deadline %d; never granted %d\n",
		r.Paths.Immediate, r.Paths.Waited, r.Paths.Overrun, r.Requests.NotGranted)
	if r.Paths.Offer+r.Paths.Continued > 0 {
		fmt.Fprintf(w, "          by a position: first command after an offer %d, continued in an idle window %d\n", r.Paths.Offer, r.Paths.Continued)
	}
	if r.NotRecorded > 0 {
		fmt.Fprintf(w, "          %d granted before the decision record existed are in no figure below\n", r.NotRecorded)
	}
	fmt.Fprintf(w, "wait      p50 %s  p90 %s  max %s\n", seconds(r.WaitMS.P50), seconds(r.WaitMS.P90), seconds(r.WaitMS.Max))
	fmt.Fprintf(w, "old rule  would have held back %d of %d grants\n", r.WouldWaitR2, r.Paths.Immediate+r.Paths.Waited+r.Paths.Overrun+r.Paths.Offer+r.Paths.Continued)
	fmt.Fprintf(w, "full      %.1f%% of the time, %d runs, longest %ds\n", r.Full.Share*100, r.Full.Runs, r.Full.LongestS)
	fmt.Fprintf(w, "2+ heavy  %d minutes, highest load1 %.2f, highest memory %.1f%%\n", r.Heavy.Minutes, r.Heavy.MaxLoad1, r.Heavy.MaxMem)
	formatReportV2(w, r, loc)
	formatReportPositions(w, r.Positions)
	if len(r.Kinds) > 0 {
		fmt.Fprintln(w)
		rows := [][]string{{"KIND", "N", "WEIGHT", "PEAK", "MEAN"}}
		for _, k := range r.Kinds {
			rows = append(rows, []string{reportKindName(k.Kind), fmt.Sprint(k.N), fmt.Sprint(k.Weight),
				fmt.Sprintf("%.1f%%", k.PeakMax), fmt.Sprintf("%.1f%%", k.MeanAvg)})
		}
		_ = alignRows(w, rows, 2)
	}
}

// safeReason is what a wait reason looks like (the daemon's own names); anything
// else is quoted, like a kind.
var safeReason = regexp.MustCompile(`^[a-z][a-z_:]{0,47}$`)

func reportReasonName(r string) string {
	switch {
	case r == "":
		return "(none)"
	case safeReason.MatchString(r):
		return r
	}
	return strconv.QuoteToASCII(r)
}

// reasonsText prints a wait_reasons map as "gap 30.0s, danger 12.0s", longest first.
func reasonsText(m map[string]int64) string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		if m[names[i]] != m[names[j]] {
			return m[names[i]] > m[names[j]]
		}
		return names[i] < names[j]
	})
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, reportReasonName(k)+" "+seconds(m[k]))
	}
	return strings.Join(parts, ", ")
}

// formatReportV2 prints the lease v2 sections: zone shares, parks, waits by
// reason, the longest waits and the advise shadow.
func formatReportV2(w io.Writer, r resources.Report, loc *time.Location) {
	if r.Zones.Minutes == 0 {
		fmt.Fprintf(w, "zones     no minute with zone seconds yet\n")
	} else {
		fmt.Fprintf(w, "zones     normal %.1f%%  warning %.1f%%  danger %.1f%%  (%d of %d minutes counted)\n",
			r.Zones.Normal*100, r.Zones.Warning*100, r.Zones.Danger*100, r.Zones.Minutes, r.Coverage.Minutes)
	}
	fmt.Fprintf(w, "parks     %d\n", r.Parks)
	for _, g := range r.WaitByReason {
		fmt.Fprintf(w, "wait by   %s  n %d  p50 %s  p90 %s  max %s\n", reportReasonName(g.Reason), g.N, seconds(g.P50), seconds(g.P90), seconds(g.Max))
	}
	for i, l := range r.Longest {
		why := reasonsText(l.Reasons)
		if why == "" {
			why = "no reasons recorded"
		}
		fmt.Fprintf(w, "longest   #%d %s  %s  %s  %s  (zone %s, trend %s at the grant)\n", i+1, reportTime(l.CreatedAt, loc), reportKindName(l.Kind),
			seconds(l.WaitedMS), why, reportReasonName(l.Zone), reportReasonName(l.Trend))
	}
	if r.Shadow.N == 0 {
		return
	}
	fmt.Fprintf(w, "shadow    v2 would have made %d requests wait: p50 %s  p90 %s  max %s; %d are lower bounds (the request ended first)\n",
		r.Shadow.N, seconds(r.Shadow.WaitMS.P50), seconds(r.Shadow.WaitMS.P90), seconds(r.Shadow.WaitMS.Max), r.Shadow.Open)
	for i, l := range r.Shadow.Longest {
		bound := ""
		if l.Open {
			bound = " (at least)"
		}
		fmt.Fprintf(w, "          #%d %s  %s  %s%s  %s\n", i+1, reportTime(l.CreatedAt, loc), reportKindName(l.Kind), seconds(l.WaitMS), bound, reasonsText(l.Reasons))
	}
}

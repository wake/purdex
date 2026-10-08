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
	if r.NotRecorded > 0 {
		fmt.Fprintf(w, "          %d granted before the decision record existed are in no figure below\n", r.NotRecorded)
	}
	fmt.Fprintf(w, "wait      p50 %s  p90 %s  max %s\n", seconds(r.WaitMS.P50), seconds(r.WaitMS.P90), seconds(r.WaitMS.Max))
	fmt.Fprintf(w, "old rule  would have held back %d of %d grants\n", r.WouldWaitR2, r.Paths.Immediate+r.Paths.Waited+r.Paths.Overrun)
	fmt.Fprintf(w, "full      %.1f%% of the time, %d runs, longest %ds\n", r.Full.Share*100, r.Full.Runs, r.Full.LongestS)
	fmt.Fprintf(w, "2+ heavy  %d minutes, highest load1 %.2f, highest memory %.1f%%\n", r.Heavy.Minutes, r.Heavy.MaxLoad1, r.Heavy.MaxMem)
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

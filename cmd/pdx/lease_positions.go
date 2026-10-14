package main

// The POSITIONS section of `pdx lease ls` and the position lines of the report
// (lease v2 amendment A, Task 21).

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wake/purdex/internal/resources"
)

var (
	positionStateText = map[string]string{
		resources.PosQueued: "排隊中", resources.PosOffered: "輪到了", resources.PosRunning: "執行中", resources.PosIdle: "空窗",
	}
	requestStateText = map[string]string{
		"held": "執行中", "queued": "排隊中", "waiting": "等待中", "asked": "已詢問",
	}
)

// leftText is the time left of a window as 2m14s, or 45s.
func leftText(ms int64) string {
	s := max((ms+999)/1000, 0)
	if s >= 60 {
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%ds", s)
}

// printPositions writes the POSITIONS section: one row per position, its
// requests indented under it. A queued position is "#N", a position that holds
// the host (offered, running, idle) is 占用.
func printPositions(w io.Writer, snap resources.Snapshot, now time.Time) error {
	if len(snap.Positions) == 0 {
		return nil
	}
	cell := func(s string) string {
		if s = sanitizeCell(s); s == "" {
			return "-"
		}
		return s
	}
	rows := [][]string{{"POSITIONS", "SESSION", "STATE", "LEFT", "K", "T", "WARNED"}}
	for _, p := range snap.Positions {
		place := "占用"
		if p.State == resources.PosQueued {
			place = fmt.Sprintf("#%d", p.Place)
		}
		who := p.Address
		if who == "" {
			who = shortSession(p.SessionID)
		} else if p.Ref != "" {
			who += " (" + p.Ref + ")"
		}
		state, ok := positionStateText[p.State]
		if !ok {
			state = fmt.Sprintf("%q", p.State)
		}
		left, k, t, warned := "-", "-", "-", "-"
		switch {
		case p.State == resources.PosOffered && !p.Delivered:
			state += "（通知中）"
		case p.State == resources.PosOffered || p.State == resources.PosIdle:
			if p.WindowUntil > 0 {
				left = leftText(p.WindowUntil - now.UnixMilli())
			}
		}
		if p.State == resources.PosRunning || p.State == resources.PosIdle {
			k, t = fmt.Sprint(p.Commands), shortSeconds(p.SpanS)
			warned = "-"
			if p.Warned {
				warned = "已預告"
			}
		}
		if p.NoticeResult == "user_alert" {
			state += "（通知沒送到）"
		}
		rows = append(rows, []string{place, cell(who), state, left, k, t, warned})
	}
	var table bytes.Buffer
	if err := alignRows(&table, rows, 2); err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(table.String(), "\n"), "\n")
	fmt.Fprintln(w)
	for i, line := range lines {
		fmt.Fprintln(w, line)
		if i == 0 {
			continue
		}
		for _, r := range snap.Positions[i-1].Requests {
			rs, ok := requestStateText[r.State]
			if !ok {
				rs = fmt.Sprintf("%q", r.State)
			}
			fmt.Fprintf(w, "  %s  %s  %s  %s\n", cell(r.Mode), rs, cell(r.Command), cell(r.Cwd))
		}
	}
	return nil
}

// formatReportPositions prints the amendment A lines of the report: what the
// positions did in the period. Nothing when there were none.
func formatReportPositions(w io.Writer, p resources.ReportPositions) {
	if p.Total == 0 {
		return
	}
	reasons := make([]string, 0, len(p.ByEndReason))
	for _, r := range p.ByEndReason {
		reasons = append(reasons, fmt.Sprintf("%s %d", reportReasonName(r.Reason), r.Count))
	}
	fmt.Fprintf(w, "positions %d  (%s)\n", p.Total, strings.Join(reasons, ", "))
	fmt.Fprintf(w, "          commands per position: none %d, one %d, two %d, three %d, four or more %d; re-queued %d; extensions %d\n",
		p.Commands.None, p.Commands.One, p.Commands.Two, p.Commands.Three, p.Commands.FourPlus, p.Requeued, p.Extensions)
	fmt.Fprintf(w, "          queued wait  p50 %s  p90 %s  max %s\n", seconds(p.QueuedWaitMS.P50), seconds(p.QueuedWaitMS.P90), seconds(p.QueuedWaitMS.Max))
	fmt.Fprintf(w, "          held for others  offered window n %d total %s p50 %s p90 %s max %s; idle window n %d total %s p50 %s p90 %s max %s\n",
		p.OfferWaitMS.N, seconds(p.OfferWaitMS.Total), seconds(p.OfferWaitMS.P50), seconds(p.OfferWaitMS.P90), seconds(p.OfferWaitMS.Max),
		p.IdleWaitMS.N, seconds(p.IdleWaitMS.Total), seconds(p.IdleWaitMS.P50), seconds(p.IdleWaitMS.P90), seconds(p.IdleWaitMS.Max))
	o := p.Offers
	fmt.Fprintf(w, "          offers  %d, delivered %d, passed undelivered %d, lapsed after delivery %d; delivery delay p50 %s max %s\n",
		o.Offered, o.Delivered, o.Passed, o.LapsedAfterDelivery, seconds(o.DeliveryDelayMS.P50), seconds(o.DeliveryDelayMS.Max))
	if len(o.NoticeResults) > 0 {
		parts := make([]string, 0, len(o.NoticeResults))
		for _, r := range o.NoticeResults {
			parts = append(parts, fmt.Sprintf("%s %d", reportReasonName(r.Reason), r.Count))
		}
		fmt.Fprintf(w, "          notices  %s\n", strings.Join(parts, ", "))
	}
}

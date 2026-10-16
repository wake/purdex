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

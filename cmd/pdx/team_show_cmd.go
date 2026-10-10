package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
)

// teamResourcesTimeout bounds the table's one resources request, retries
// included. A var only so tests can shorten it.
var teamResourcesTimeout = 5 * time.Second

// teamTaskSubjectRunes is the longest task subject the team table shows, the
// ellipsis included.
const teamTaskSubjectRunes = 30

// teamHostShares is each session's share of the host (D-1 units, host
// percent) from one GET /api/resources, keyed by session id. It is
// best-effort: any failure, an unavailable sample or a daemon without the
// route gives nil, and the table shows "-". The team table must never break,
// or print an error line, because of this call.
func teamHostShares(ctx context.Context, client *daemonclient.Client) map[string]resources.SessionUse {
	ctx, cancel := context.WithTimeout(ctx, teamResourcesTimeout)
	defer cancel()
	_, snap, err := getResources(ctx, client)
	if err != nil || !snap.Available {
		return nil
	}
	out := make(map[string]resources.SessionUse, len(snap.Sessions))
	for _, u := range snap.Sessions {
		// One row per session is the contract; if a daemon lists a twin, the
		// busier row is the session's load.
		if prev, ok := out[u.SessionID]; !ok || u.Use > prev.Use {
			out[u.SessionID] = u
		}
	}
	return out
}

// runTeamCmd implements `pdx team [--json]` (spec §7.3, U20 (e)): --json is
// the daemon's view as is; the table's MODEL and EFFORT are what each
// member actually runs (its statusline reading), "-" until its first one.
// CPU and MEM are the member's share of the host in whole percents (host
// resource lease, review #14), joined by session id from /api/resources.
func runTeamCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx team", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseTeamFlags(fs, args)
	if err == nil && len(pos) != 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx team: %v\n%s\n", err, teamUsage)
		return ExitUsage
	}
	client, inbox, ok := teamSetup("team", *cfgPath, getenv, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	var raw json.RawMessage
	if _, err := client.Do(ctx, http.MethodGet, "/api/team?origin_inbox="+url.QueryEscape(inbox), nil, &raw); err != nil {
		return teamReportErr("team", err, stderr)
	}
	var v team.TeamView
	var line bytes.Buffer
	if json.Unmarshal(raw, &v) != nil || json.Compact(&line, raw) != nil {
		fmt.Fprintln(stderr, "pdx team: daemon 的回應不是 team view invalid_response")
		return ExitError
	}
	if *asJSON {
		fmt.Fprintln(stdout, line.String())
		return ExitOK
	}
	// The resources call is optional, so it gets a client of its own whose
	// stderr is discarded: the shared client would print its "daemon restarting"
	// line for a failure the table already hides.
	var shares map[string]resources.SessionUse
	if quiet, _, ok := teamSetup("team", *cfgPath, getenv, io.Discard, clientOpts); ok {
		shares = teamHostShares(ctx, quiet)
	}
	// D-N9: the team's name, when it has one, on a line of its own above the
	// table; sanitised like a table cell, since it is printed into a terminal.
	if line := teamLine(v.Team); line != "" {
		fmt.Fprintln(stdout, line)
	}
	// The member limit is the user's (set in Purdex.app): active members over it, read-only.
	active := 0
	for _, m := range v.Members {
		if m.State == team.MemberActive {
			active++
		}
	}
	if v.InUse != nil { // starting spawns hold a place too
		active = *v.InUse
	}
	fmt.Fprintf(stdout, "members %d/%d\n", active, v.Team.Grant.MaxMembers)
	// #2062: the lead's own automatic-relay quota and its member pool, read-only (the user sets them in Purdex.app).
	if line := quotaLine(v.LeadRelayQuota); line != "" {
		fmt.Fprintln(stdout, line)
	}
	rows := [][]string{strings.Split("ADDRESS\tHOST\tREF\tTITLE\tSTATE\tCTX\tCPU\tMEM\tMODEL\tEFFORT\tTASK\tLAST\tCWD\tTMUX", "\t")}
	for _, m := range v.Members {
		pct, model, effort := "", "", ""
		if c := m.Context; c != nil {
			if c.UsedPercentage != nil {
				pct = fmt.Sprintf("%.0f%%", *c.UsedPercentage)
			}
			model, effort = c.ModelID, c.Effort
		}
		cpu, mem := "", ""
		// CPU / MEM are this host's numbers: a member on another host never takes them (session ids are not unique across hosts).
		if u, ok := shares[m.SessionID]; ok && m.HostAlias == "" && (m.HostID == "" || v.Team.HostID == "" || m.HostID == v.Team.HostID) {
			cpu, mem = fmt.Sprintf("%.0f%%", u.CPU), fmt.Sprintf("%.0f%%", u.Mem)
		}
		// TASK: "<id> <status> <subject>", the subject cut to 30 display columns; LAST:
		// how long ago. Both "-" for a member with no task, or a daemon that
		// predates the fields.
		task, last := "", ""
		if mt := m.Task; mt != nil {
			task = sanitizeCell(mt.ID) + " " + sanitizeCell(string(mt.Status)) + " " + cutWidth(sanitizeCell(mt.Subject), teamTaskSubjectRunes)
		}
		if m.LastAt != 0 {
			last = taskAge(m.LastAt)
		}
		// An open relay ask of the member (member relay ask §3.6): the minutes left of its five, rounded up.
		if left := time.UnixMilli(m.RelayAskUntil).Sub(taskNow()); m.RelayAskUntil != 0 && left > 0 {
			task = strings.TrimSpace(task + fmt.Sprintf(" 接力申請 (剩 %d 分)", int((left+time.Minute-1)/time.Minute)))
		}
		if m.ContextUnavailable { // a remote member whose host did not answer (cross-host team spec §8)
			pct = "(主機無回應)"
		}
		cells := []string{m.Address, m.HostAlias, m.Ref, m.Title, string(m.State), pct, cpu, mem, model, effort, task, last, m.Cwd, m.TmuxSession}
		for i, c := range cells {
			if cells[i] = sanitizeCell(c); c == "" {
				cells[i] = "-"
			}
		}
		rows = append(rows, cells)
	}
	if err := alignRows(stdout, rows, 2); err != nil {
		fmt.Fprintf(stderr, "pdx team: %v\n", err)
		return ExitError
	}
	return ExitOK
}

// quotaLine is `pdx team`'s relay-quota line: the lead's own self_left (automatic relays left while unattended mode is
// on, with the quota rule) and its member pool; "" from a daemon that does not send it. Numbers only: nothing in it is
// text from elsewhere.
func quotaLine(q *team.RelayQuota) string {
	if q == nil {
		return ""
	}
	return fmt.Sprintf("relay quota: 自己 %d 次 · member 池 %d 次", q.SelfLeft, q.MemberPoolLeft)
}

// teamLine is the first line of `pdx team` (name D-N9, label D-L8): the name,
// then the label in full-width brackets when it differs from the name; "" when
// the team has neither. Both are sanitised: they are printed into a terminal.
func teamLine(t team.Team) string {
	name, label := sanitizeCell(t.TeamName), sanitizeCell(t.TeamLabel)
	switch {
	case name == "" && label == "":
		return ""
	case label == "" || label == name:
		return "team: " + name
	case name == "":
		return "team: ［" + label + "］"
	}
	return "team: " + name + " ［" + label + "］"
}

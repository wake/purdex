package main

// `pdx task ls` and `pdx task show`: the read side of the task commands.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wake/purdex/internal/team"
)

// taskNow is the clock of the age columns (a test seam).
var taskNow = time.Now

// taskSubjectCells is the longest subject the ls table shows, the ellipsis
// included.
const taskSubjectCells = 40

// taskAge is how long ago a millisecond timestamp was: 45s, 12m, 3h, 2d; "-"
// for none, 0s for a stamp from the future.
func taskAge(ms int64) string {
	if ms == 0 {
		return "-"
	}
	d := taskNow().Sub(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d/time.Second)))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// taskCell makes a value safe for one terminal cell; "-" when empty.
func taskCell(s string) string {
	if s = sanitizeCell(s); s == "" {
		return "-"
	}
	return s
}

// taskOwnerCell is the owner's ref, with its state when it is not active.
func taskOwnerCell(o team.TaskOwner) string {
	if o.State != "" && o.State != "active" {
		return taskCell(o.Ref) + " (" + sanitizeCell(o.State) + ")"
	}
	return taskCell(o.Ref)
}

// taskStatusCell shows a pending task that waits on another as
// "pending (blocked)"; a started one is just its status.
func taskStatusCell(t team.Task) string {
	if t.Status == team.TaskPending && t.Blocked {
		return "pending (blocked)"
	}
	return taskCell(string(t.Status))
}

// taskLastAt is the newest of the last turn and the last report.
func taskLastAt(t team.Task) int64 {
	var at int64
	if t.LastTurn != nil {
		at = t.LastTurn.At
	}
	if t.LastReport != nil && t.LastReport.At > at {
		at = t.LastReport.At
	}
	return at
}

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// ls implements `pdx task ls [--member <ref>] [--all] [--json]`.
func (c taskCall) ls(args []string) int {
	fs := flag.NewFlagSet("pdx task ls", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	member := fs.String("member", "", "")
	all := fs.Bool("all", false, "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseTeamFlags(fs, args)
	if err == nil && len(pos) != 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	if err != nil {
		return taskUsageErr(c.stderr, err.Error())
	}
	client, inbox, ok := teamSetup("task", *cfgPath, c.getenv, c.stderr, c.clientOpts)
	if !ok {
		return ExitError
	}
	q := url.Values{"origin_inbox": {inbox}}
	if *member != "" {
		q.Set("member", *member)
	}
	if *all {
		q.Set("all", "1")
	}
	var raw json.RawMessage
	if _, err := client.Do(c.ctx, http.MethodGet, "/api/team/tasks?"+q.Encode(), nil, &raw); err != nil {
		return teamReportErr("task", err, c.stderr)
	}
	var list team.TaskList
	var line bytes.Buffer
	if json.Unmarshal(raw, &list) != nil || json.Compact(&line, raw) != nil {
		fmt.Fprintln(c.stderr, "pdx task: daemon 的回應不是 task list invalid_response")
		return ExitError
	}
	if *asJSON {
		fmt.Fprintln(c.stdout, line.String())
		return ExitOK
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tOWNER\tSUBJECT\tLAST")
	for _, t := range list.Tasks {
		fmt.Fprintln(tw, strings.Join([]string{taskCell(t.ID), taskStatusCell(t), taskOwnerCell(t.Owner),
			cutRunes(taskCell(t.Subject), taskSubjectCells), taskAge(taskLastAt(t))}, "\t"))
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintf(c.stderr, "pdx task: %v\n", err)
		return ExitError
	}
	return ExitOK
}

// mine implements `pdx task mine [--all] [--json | --seed]`: a member's own
// tasks (a lead is refused not_member, exit 13). --seed prints the relay
// notice's lines (team.TaskSeedText), nothing when the member has no open task.
func (c taskCall) mine(args []string) int {
	fs := flag.NewFlagSet("pdx task mine", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	all := fs.Bool("all", false, "")
	asJSON := fs.Bool("json", false, "")
	seed := fs.Bool("seed", false, "")
	pos, err := parseTeamFlags(fs, args)
	switch {
	case err == nil && len(pos) != 0:
		err = fmt.Errorf("unexpected argument %q", pos[0])
	case err == nil && *asJSON && *seed:
		err = fmt.Errorf("--json 與 --seed 只能擇一")
	}
	if err != nil {
		return taskUsageErr(c.stderr, err.Error())
	}
	client, inbox, ok := teamSetup("task", *cfgPath, c.getenv, c.stderr, c.clientOpts)
	if !ok {
		return ExitError
	}
	q := url.Values{"origin_inbox": {inbox}, "mine": {"1"}}
	if *all {
		q.Set("all", "1")
	}
	var raw json.RawMessage
	if _, err := client.Do(c.ctx, http.MethodGet, "/api/team/tasks?"+q.Encode(), nil, &raw); err != nil {
		return teamReportErr("task", err, c.stderr)
	}
	var list team.TaskList
	var line bytes.Buffer
	if json.Unmarshal(raw, &list) != nil || json.Compact(&line, raw) != nil {
		fmt.Fprintln(c.stderr, "pdx task: daemon 的回應不是 task list invalid_response")
		return ExitError
	}
	switch {
	case *asJSON:
		fmt.Fprintln(c.stdout, line.String())
	case *seed:
		// The member's own words are in it (subjects): cleaned like the table's cells.
		clean := make([]team.Task, len(list.Tasks))
		for i, t := range list.Tasks {
			t.ID, t.Subject = taskCell(t.ID), taskCell(t.Subject)
			clean[i] = t
		}
		if text := team.TaskSeedText(clean); text != "" {
			fmt.Fprintln(c.stdout, text)
		}
	default:
		tw := tabwriter.NewWriter(c.stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATUS\tSUBJECT\tLAST")
		for _, t := range list.Tasks {
			fmt.Fprintln(tw, strings.Join([]string{taskCell(t.ID), taskStatusCell(t),
				cutRunes(taskCell(t.Subject), taskSubjectCells), taskAge(taskLastAt(t))}, "\t"))
		}
		if err := tw.Flush(); err != nil {
			fmt.Fprintf(c.stderr, "pdx task: %v\n", err)
			return ExitError
		}
	}
	return ExitOK
}

// show implements `pdx task show <id> [--json | --message]`. --message
// prints the down message the task would send (so a failed send can be
// repeated by hand), from the same composer as add and reassign.
func (c taskCall) show(args []string) int {
	fs := flag.NewFlagSet("pdx task show", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	asJSON := fs.Bool("json", false, "")
	asMessage := fs.Bool("message", false, "")
	id, ok := c.oneID(fs, args)
	if !ok {
		return ExitUsage
	}
	if *asJSON && *asMessage {
		return taskUsageErr(c.stderr, "--json 與 --message 只能擇一")
	}
	client, inbox, ok := teamSetup("task", *cfgPath, c.getenv, c.stderr, c.clientOpts)
	if !ok {
		return ExitError
	}
	var raw json.RawMessage
	if _, err := client.Do(c.ctx, http.MethodGet, taskPath(id)+"?"+url.Values{"origin_inbox": {inbox}}.Encode(), nil, &raw); err != nil {
		return teamReportErr("task", err, c.stderr)
	}
	var d team.TaskDetail
	var line bytes.Buffer
	if json.Unmarshal(raw, &d) != nil || d.Task.ID == "" || json.Compact(&line, raw) != nil {
		fmt.Fprintln(c.stderr, "pdx task: daemon 的回應不是 task invalid_response")
		return ExitError
	}
	switch {
	case *asJSON:
		fmt.Fprintln(c.stdout, line.String())
	case *asMessage:
		fmt.Fprintln(c.stdout, team.TaskDownMessage(d.Task))
	default:
		printTaskDetail(c.stdout, d)
	}
	return ExitOK
}

// printTaskDetail is `pdx task show`'s human output: only the sections the
// task has. Every daemon-supplied text goes through sanitizeCell.
func printTaskDetail(w io.Writer, d team.TaskDetail) {
	t := d.Task
	field := func(label, value string) { fmt.Fprintf(w, "  %-14s%s\n", label+":", value) }
	fmt.Fprintf(w, "%s  %s\n", taskCell(t.ID), taskCell(t.Subject))
	owner := taskCell(t.Owner.Ref)
	if t.Owner.State != "" {
		owner += " (" + sanitizeCell(t.Owner.State) + ")"
	}
	if t.Owner.Address != "" {
		owner += "  " + sanitizeCell(t.Owner.Address)
	}
	field("owner", owner)
	field("status", taskStatusCell(t))
	if strings.TrimSpace(t.Description) != "" {
		for _, l := range strings.Split(strings.TrimRight(t.Description, " \t\r\n"), "\n") {
			fmt.Fprintf(w, "  %s\n", sanitizeCell(strings.TrimRight(l, "\r")))
		}
	}
	if len(t.DoneWhen) > 0 {
		fmt.Fprintln(w, "  done when:")
		for _, l := range t.DoneWhen {
			fmt.Fprintf(w, "    - %s\n", sanitizeCell(l))
		}
	}
	list := func(label string, items []string) {
		if len(items) > 0 {
			cells := make([]string, len(items))
			for i, s := range items {
				cells[i] = sanitizeCell(s)
			}
			field(label, strings.Join(cells, ", "))
		}
	}
	list("blocked by", t.BlockedBy)
	list("blocks", t.Blocks)
	if t.Metadata.Branch != "" {
		field("branch", sanitizeCell(t.Metadata.Branch))
	}
	if len(t.Metadata.PRs) > 0 {
		prs := make([]string, len(t.Metadata.PRs))
		for i, n := range t.Metadata.PRs {
			prs[i] = fmt.Sprintf("#%d", n)
		}
		field("prs", strings.Join(prs, ", "))
	}
	list("shas", t.Metadata.SHAs)
	if r := t.LastReport; r != nil {
		field("last report", fmt.Sprintf("%s %s (%s ago)", sanitizeCell(r.Kind), sanitizeCell(r.Summary), taskAge(r.At)))
	}
	if r := t.LastTurn; r != nil {
		field("last turn", fmt.Sprintf("%s (%s ago)", sanitizeCell(r.Summary), taskAge(r.At)))
	}
	if len(d.Reports) > 0 {
		fmt.Fprintln(w, "  reports:")
		for _, r := range d.Reports {
			fmt.Fprintf(w, "    %s %s %s\n", taskAge(r.CreatedAt), sanitizeCell(string(r.Kind)), sanitizeCell(r.Summary))
		}
	}
}

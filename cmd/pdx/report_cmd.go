package main

// `pdx report`: a member's reports to its lead (plan "PR T-1d", spec D-3).
// Runs inside the member's Claude Code session, which the daemon attributes by
// its inbox. `pdx report <kind>` stores the report, then sends the lead the
// up message (team.ReportUpMessage) from the member's inbox. API errors print
// `pdx report: <detail> <code>`, the code last; exit codes are spec §14.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

const reportUsage = "usage: pdx report <ack|progress|question|ready|merged|blocked|done> [--task <id>] --summary <s> [--needs lead|user] [--pr <n>] [--reviews <stage>=<job>]… [--sha <sha>] [--file <md> | --text <t>] [--id <rid>] [--json] [--config <path>]\n" +
	"       pdx report ls [--task <id>] [--since <dur>] [--json]\n" +
	"       pdx report show <rid> [--json | --message]\n" +
	"       (needs: question, blocked · pr: ready, merged · reviews: ready · sha: merged; only a member, only its own tasks)"

// reportNewID mints a report id (a test seam); reportNow is the clock of
// `ls --since`.
var (
	reportNewID = uuid.NewString
	reportNow   = time.Now
)

func runReport(args []string) {
	os.Exit(runReportCmd(context.Background(), args, os.Getenv, os.Stdout, os.Stderr))
}

// reportUsageErr is a grammar or field refusal: one line and the usage, exit
// 2, before the config is read or the daemon asked.
func reportUsageErr(stderr io.Writer, msg string) int {
	fmt.Fprintf(stderr, "pdx report: %s\n%s\n", msg, reportUsage)
	return ExitUsage
}

type reportCall struct {
	ctx        context.Context
	getenv     func(string) string
	stdout     io.Writer
	stderr     io.Writer
	clientOpts []daemonclient.Option
}

// runReportCmd implements `pdx report …`. Flags and positionals may come in
// any order (parseTeamFlags).
func runReportCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	c := reportCall{ctx, getenv, stdout, stderr, clientOpts}
	if len(args) == 0 {
		return reportUsageErr(stderr, "需要 kind 或子命令")
	}
	switch sub, rest := args[0], args[1:]; {
	case sub == "ls":
		return c.ls(rest)
	case sub == "show":
		return c.show(rest)
	case team.ValidReportKind(team.ReportKind(sub)):
		return c.post(team.ReportKind(sub), rest)
	default:
		return reportUsageErr(stderr, fmt.Sprintf("不認識的 kind 或子命令 %q", sub))
	}
}

// post implements `pdx report <kind>`: the field checks, the report, then the
// up message. The POST carries the report id, so a replay of it is safe
// (Idempotent) and `--id` retries a report whose send failed.
func (c reportCall) post(kind team.ReportKind, args []string) int {
	fs := flag.NewFlagSet("pdx report "+string(kind), flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	task := fs.String("task", "", "")
	summary := fs.String("summary", "", "")
	needs := fs.String("needs", "", "")
	pr := fs.Int("pr", 0, "")
	sha := fs.String("sha", "", "")
	file := fs.String("file", "", "")
	text := fs.String("text", "", "")
	rid := fs.String("id", "", "")
	asJSON := fs.Bool("json", false, "")
	var reviews listFlag
	fs.Var(&reviews, "reviews", "")
	pos, err := parseTeamFlags(fs, args)
	if err != nil {
		return reportUsageErr(c.stderr, err.Error())
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	body := *text
	switch {
	case len(pos) != 0:
		return reportUsageErr(c.stderr, fmt.Sprintf("unexpected argument %q", pos[0]))
	case set["file"] && set["text"]:
		return reportUsageErr(c.stderr, "--file 與 --text 只能擇一")
	case set["task"] && !validTaskID(*task):
		return reportUsageErr(c.stderr, badTaskID("--task", *task))
	}
	if set["id"] {
		if err := team.ValidReportID(*rid); err != nil {
			return reportUsageErr(c.stderr, "--id: "+err.Error())
		}
	}
	if set["file"] {
		if body, err = readBriefFile(*file); err != nil {
			return reportUsageErr(c.stderr, "--file: "+err.Error())
		}
	}
	req := team.ReportRequest{ID: *rid, Task: *task, Kind: kind, Summary: *summary, Needs: *needs, PR: *pr,
		Reviews: reviews, SHA: *sha, Body: body}
	if err := team.ValidateReport(req); err != nil {
		return reportUsageErr(c.stderr, err.Error())
	}
	// The whole up message, composed with the longest id there is, must fit
	// the peers limit: a report the lead cannot be told about is not stored.
	worst := team.ReportUpMessage(team.Report{Kind: kind, Task: team.TaskWorstCaseID, Summary: req.Summary, Needs: req.Needs,
		PR: req.PR, Reviews: req.Reviews, SHA: req.SHA, Body: req.Body})
	if len(worst) > taskMessageMaxBytes {
		return reportUsageErr(c.stderr, fmt.Sprintf("訊息太長：給 lead 的訊息有 %d bytes，上限 %d bytes", len(worst), taskMessageMaxBytes))
	}
	if err := ipeers.ValidateText(worst); err != nil {
		return reportUsageErr(c.stderr, "訊息不合格："+err.Error())
	}
	if req.ID == "" {
		req.ID = reportNewID()
	}

	client, inbox, ok := teamSetup("report", *cfgPath, c.getenv, c.stderr, c.clientOpts)
	if !ok {
		return ExitError
	}
	var raw json.RawMessage
	if _, err := client.Do(c.ctx, http.MethodPost, "/api/team/reports", team.CreateReportRequest{OriginInbox: inbox, ReportRequest: req}, &raw, daemonclient.Idempotent()); err != nil {
		return teamReportErr("report", err, c.stderr)
	}
	var resp team.ReportResponse
	var line bytes.Buffer
	if json.Unmarshal(raw, &resp) != nil || resp.Report.ID == "" || json.Compact(&line, raw) != nil {
		fmt.Fprintln(c.stderr, "pdx report: daemon 的回應不是 report invalid_response")
		return ExitError
	}
	return c.notify(client, inbox, *cfgPath, resp, line.String(), *asJSON)
}

// notify sends the lead the up message and prints the result. The report is
// already stored, so a failed send is exit 1 with the answer's JSON on stdout
// and the manual command on stderr, never a rollback; `--id <rid>` repeats the
// whole command and sends again.
func (c reportCall) notify(client *daemonclient.Client, inbox, cfgPath string, resp team.ReportResponse, compact string, asJSON bool) int {
	r := resp.Report
	detail, code := sendReportMessage(c.ctx, client, inbox, resp)
	if code != "" {
		fmt.Fprintln(c.stdout, compact)
		id, reason := sanitizeCell(r.ID), sanitizeCell(detail)
		cmd := reportResendCommand(resp.Lead.Address, r.ID, cfgPath)
		if resp.Lead.Address == "" {
			fmt.Fprintf(c.stderr, "pdx report: 回報 %s 已存下，但 lead 的地址不明、訊息沒送出（%s）；請先用 pdx team 查到 lead 的地址，再執行下面的命令（把 <ADDRESS> 自行替換成它）：%s %s\n",
				id, reason, cmd, sanitizeCell(code))
			return ExitError
		}
		fmt.Fprintf(c.stderr, "pdx report: 回報 %s 已存下，但給 lead 的訊息沒送出（%s）；請手動送：%s %s\n", id, reason, cmd, sanitizeCell(code))
		return ExitError
	}
	if asJSON {
		fmt.Fprintln(c.stdout, compact)
	} else {
		fmt.Fprintf(c.stdout, "reported %s %s (%s) → %s: %s\n", sanitizeCell(string(r.Kind)), sanitizeCell(r.Task),
			sanitizeCell(string(resp.Task.Status)), sanitizeCell(resp.Lead.Ref), sanitizeCell(r.Summary))
	}
	return ExitOK
}

// sendReportMessage sends the up message through POST /api/peers/send, from
// the member's inbox. One request, never replayed (client.Once): a send that
// may have arrived must not arrive twice. code "" is success.
func sendReportMessage(ctx context.Context, client *daemonclient.Client, inbox string, resp team.ReportResponse) (detail, code string) {
	if resp.Lead.Address == "" {
		return "daemon 沒有回 lead 的地址", "no_address"
	}
	sctx, cancel := context.WithTimeout(ctx, briefTimeout)
	defer cancel()
	req := ipeers.SendRequest{To: resp.Lead.Address, Text: team.ReportUpMessage(resp.Report), OriginInbox: inbox}
	if _, err := client.Once(sctx, http.MethodPost, "/api/peers/send", req, nil); err != nil {
		return briefErr(err)
	}
	return "", ""
}

// reportResendCommand is the command that sends a report's up message by
// hand, quoted like taskResendCommand's.
func reportResendCommand(address, rid, cfgPath string) string {
	if address == "" {
		address = "<ADDRESS>"
	}
	cfg := ""
	if cfgPath != "" {
		cfg = "--config " + shellQuote(sanitizeCell(cfgPath)) + " "
	}
	return "pdx msg send " + cfg + shellQuote(sanitizeCell(address)) +
		" \"$(pdx report show " + cfg + shellQuote(sanitizeCell(rid)) + " --message)\""
}

// fetch asks GET /api/team/reports and decodes the list; ok=false: a line
// was written.
func (c reportCall) fetch(cfgPath string, q url.Values) (list team.ReportList, compact string, ok bool) {
	client, inbox, ok := teamSetup("report", cfgPath, c.getenv, c.stderr, c.clientOpts)
	if !ok {
		return list, "", false
	}
	q.Set("origin_inbox", inbox)
	var raw json.RawMessage
	if _, err := client.Do(c.ctx, http.MethodGet, "/api/team/reports?"+q.Encode(), nil, &raw); err != nil {
		teamReportErr("report", err, c.stderr)
		return list, "", false
	}
	var line bytes.Buffer
	if json.Unmarshal(raw, &list) != nil || list.Reports == nil || json.Compact(&line, raw) != nil {
		fmt.Fprintln(c.stderr, "pdx report: daemon 的回應不是 report 清單 invalid_response")
		return list, "", false
	}
	return list, line.String(), true
}

// ls implements `pdx report ls [--task <id>] [--since <dur>] [--json]`.
func (c reportCall) ls(args []string) int {
	fs := flag.NewFlagSet("pdx report ls", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	task := fs.String("task", "", "")
	since := fs.Duration("since", 0, "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseTeamFlags(fs, args)
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	switch {
	case err != nil:
		return reportUsageErr(c.stderr, err.Error())
	case len(pos) != 0:
		return reportUsageErr(c.stderr, fmt.Sprintf("unexpected argument %q", pos[0]))
	case set["task"] && !validTaskID(*task):
		return reportUsageErr(c.stderr, badTaskID("--task", *task))
	case set["since"] && *since <= 0:
		return reportUsageErr(c.stderr, "--since: 需要正的時間長度（例如 30m、2h）")
	}
	q := url.Values{}
	if set["task"] {
		q.Set("task", *task)
	}
	if set["since"] {
		q.Set("since", strconv.FormatInt(reportNow().Add(-*since).UnixMilli(), 10))
	}
	list, compact, ok := c.fetch(*cfgPath, q)
	if !ok {
		return ExitError
	}
	if *asJSON {
		fmt.Fprintln(c.stdout, compact)
		return ExitOK
	}
	rows := [][]string{{"ID", "KIND", "TASK", "MEMBER", "AGE", "SUMMARY"}}
	for _, r := range list.Reports {
		rows = append(rows, []string{taskCell(r.ID), taskCell(string(r.Kind)), taskCell(r.Task), taskCell(r.Member.Ref),
			taskAge(r.CreatedAt), taskCell(cutWidth(sanitizeCell(r.Summary), 60))})
	}
	if err := alignRows(c.stdout, rows, 2); err != nil {
		fmt.Fprintf(c.stderr, "pdx report: %v\n", err)
		return ExitError
	}
	return ExitOK
}

// show implements `pdx report show <rid> [--json | --message]`. The daemon has
// no route for one report, so it reads the list (newest 200) and picks the id.
func (c reportCall) show(args []string) int {
	fs := flag.NewFlagSet("pdx report show", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	asJSON := fs.Bool("json", false, "")
	asMessage := fs.Bool("message", false, "")
	pos, err := parseTeamFlags(fs, args)
	switch {
	case err != nil:
		return reportUsageErr(c.stderr, err.Error())
	case len(pos) != 1:
		return reportUsageErr(c.stderr, "需要剛好一個 <rid>")
	case team.ValidReportID(pos[0]) != nil:
		return reportUsageErr(c.stderr, "<rid>: "+team.ValidReportID(pos[0]).Error())
	case *asJSON && *asMessage:
		return reportUsageErr(c.stderr, "--json 與 --message 只能擇一")
	}
	list, _, ok := c.fetch(*cfgPath, url.Values{})
	if !ok {
		return ExitError
	}
	for _, r := range list.Reports {
		if r.ID != pos[0] {
			continue
		}
		switch {
		case *asJSON:
			b, _ := json.Marshal(r)
			fmt.Fprintln(c.stdout, string(b))
		case *asMessage:
			fmt.Fprintln(c.stdout, team.ReportUpMessage(r))
		default:
			for _, l := range strings.Split(team.ReportUpMessage(r), "\n") {
				fmt.Fprintln(c.stdout, sanitizeCell(strings.TrimRight(l, "\r")))
			}
		}
		return ExitOK
	}
	fmt.Fprintf(c.stderr, "pdx report: 找不到回報 %s（只看得到最新 200 筆） report_not_found\n", sanitizeCell(pos[0]))
	return ExitError
}

package main

// `pdx task`: the lead's task commands (plan "PR T-1c", spec D-1/D-2/D-5).
// Each runs inside the lead's Claude Code session, which the daemon
// attributes by its inbox. `add` and `reassign` also send the owner the down
// message (team.TaskDownMessage) from the lead's inbox. API errors print
// `pdx task: <detail> <code>`, the code last; exit codes are spec §14.

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
	"regexp"
	"strings"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

const taskUsage = "usage: pdx task add --to <ref> --subject <s> [--brief-file <f> | --brief <text>] [--done-when <line>]… [--blocked-by <id>]… [--json] [--config <path>]\n" +
	"       pdx task ls [--member <ref>] [--all] [--json]\n" +
	"       pdx task show <id> [--json | --message]\n" +
	"       pdx task start|done|delete <id> [--json]\n" +
	"       pdx task reassign <id> --to <ref> [--json]\n" +
	"       (<ref> is _xxxxxx, or an address from pdx team; only a lead, only members of its own team)"

// taskMessageMaxBytes is the longest down message the CLI sends. A var only
// so a test can lower it: the field limits keep a valid task far below the
// peers text limit.
var taskMessageMaxBytes = ipeers.MaxTextBytes

// taskIDShape is the light check of a --blocked-by id: <6 hex>-<positive
// int>. The daemon is the authority on whether it names a task.
var taskIDShape = regexp.MustCompile(`^[0-9a-f]{6}-[1-9][0-9]*$`)

func runTask(args []string) {
	os.Exit(runTaskCmd(context.Background(), args, os.Getenv, os.Stdout, os.Stderr))
}

// listFlag is a flag that may repeat (--done-when, --blocked-by).
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// taskUsageErr is a grammar or field refusal: one line and the usage, exit 2,
// before the config is read or the daemon asked.
func taskUsageErr(stderr io.Writer, msg string) int {
	fmt.Fprintf(stderr, "pdx task: %s\n%s\n", msg, taskUsage)
	return ExitUsage
}

// taskCall is what every subcommand shares.
type taskCall struct {
	ctx        context.Context
	getenv     func(string) string
	stdout     io.Writer
	stderr     io.Writer
	clientOpts []daemonclient.Option
}

// runTaskCmd implements `pdx task <sub> …`. Flags and the id may come in any
// order (parseTeamFlags).
func runTaskCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	c := taskCall{ctx, getenv, stdout, stderr, clientOpts}
	if len(args) == 0 {
		return taskUsageErr(stderr, "需要子命令")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return c.add(rest)
	case "reassign":
		return c.reassign(rest)
	case "start":
		return c.setStatus(sub, team.TaskInProgress, rest)
	case "done":
		return c.setStatus(sub, team.TaskCompleted, rest)
	case "delete":
		return c.setStatus(sub, team.TaskDeleted, rest)
	case "ls":
		return c.ls(rest)
	case "show":
		return c.show(rest)
	}
	return taskUsageErr(stderr, fmt.Sprintf("不認識的子命令 %q", sub))
}

// oneID parses a subcommand that takes exactly one <id> and the flags fs
// declares; ok=false: a usage line was written.
func (c taskCall) oneID(fs *flag.FlagSet, args []string) (id string, ok bool) {
	pos, err := parseTeamFlags(fs, args)
	switch {
	case err != nil:
		taskUsageErr(c.stderr, err.Error())
	case len(pos) != 1 || strings.TrimSpace(pos[0]) == "":
		taskUsageErr(c.stderr, "需要剛好一個 <id>")
	default:
		return pos[0], true
	}
	return "", false
}

func taskPath(id string) string { return "/api/team/tasks/" + url.PathEscape(id) }

// decodeTask parses a Task answer and keeps its compact JSON. ok=false: the
// daemon's answer was not a task (a line was written).
func decodeTask(raw json.RawMessage, stderr io.Writer) (t team.Task, compact string, ok bool) {
	var line bytes.Buffer
	if json.Unmarshal(raw, &t) != nil || t.ID == "" || json.Compact(&line, raw) != nil {
		fmt.Fprintln(stderr, "pdx task: daemon 的回應不是 task invalid_response")
		return t, "", false
	}
	return t, line.String(), true
}

// add implements `pdx task add`: the field checks, the task, then the down
// message. The POST is not Idempotent: a replayed create would make a second
// task (the recorded decision, PR #2020).
func (c taskCall) add(args []string) int {
	fs := flag.NewFlagSet("pdx task add", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	to := fs.String("to", "", "")
	subject := fs.String("subject", "", "")
	brief := fs.String("brief", "", "")
	briefFile := fs.String("brief-file", "", "")
	asJSON := fs.Bool("json", false, "")
	var doneWhen, blockedBy listFlag
	fs.Var(&doneWhen, "done-when", "")
	fs.Var(&blockedBy, "blocked-by", "")
	pos, err := parseTeamFlags(fs, args)
	if err != nil {
		return taskUsageErr(c.stderr, err.Error())
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	description := *brief
	switch {
	case len(pos) != 0:
		return taskUsageErr(c.stderr, fmt.Sprintf("unexpected argument %q", pos[0]))
	case strings.TrimSpace(*to) == "":
		return taskUsageErr(c.stderr, "--to: 需要 member 的 ref（_xxxxxx）或地址")
	case set["brief"] && set["brief-file"]:
		return taskUsageErr(c.stderr, "--brief 與 --brief-file 只能擇一")
	}
	if err := team.ValidTaskSubject(*subject); err != nil {
		return taskUsageErr(c.stderr, "--subject: "+err.Error())
	}
	if err := team.ValidDoneWhen(doneWhen); err != nil {
		return taskUsageErr(c.stderr, "--done-when: "+err.Error())
	}
	for _, id := range blockedBy {
		if !taskIDShape.MatchString(id) {
			return taskUsageErr(c.stderr, fmt.Sprintf("--blocked-by %q 不是任務 id（<6 位小寫十六進位>-<序號>，例如 8f2c0f-3）", id))
		}
	}
	if set["brief-file"] {
		if description, err = readBriefFile(*briefFile); err != nil {
			return taskUsageErr(c.stderr, "--brief-file: "+err.Error())
		}
	}
	if err := team.ValidTaskDescription(description); err != nil {
		return taskUsageErr(c.stderr, "--brief: "+err.Error())
	}
	// The whole message, composed with the longest id there is, must fit the
	// peers limit: a task the owner cannot be told about is not stored.
	worst := team.TaskDownMessage(team.Task{ID: team.TaskWorstCaseID, Subject: *subject, Description: description, DoneWhen: doneWhen})
	if len(worst) > taskMessageMaxBytes {
		return taskUsageErr(c.stderr, fmt.Sprintf("訊息太長：給 member 的訊息有 %d bytes，上限 %d bytes", len(worst), taskMessageMaxBytes))
	}
	if err := ipeers.ValidateText(worst); err != nil {
		return taskUsageErr(c.stderr, "訊息不合格："+err.Error())
	}

	client, inbox, ok := teamSetup("task", *cfgPath, c.getenv, c.stderr, c.clientOpts)
	if !ok {
		return ExitError
	}
	req := team.CreateTaskRequest{OriginInbox: inbox, To: *to, Subject: *subject, Description: description,
		DoneWhen: doneWhen, BlockedBy: blockedBy}
	var raw json.RawMessage
	if _, err := client.Do(c.ctx, http.MethodPost, "/api/team/tasks", req, &raw); err != nil {
		return teamReportErr("task", err, c.stderr)
	}
	task, compact, ok := decodeTask(raw, c.stderr)
	if !ok {
		return ExitError
	}
	return c.notify(client, inbox, task, compact, "created", *asJSON)
}

// reassign implements `pdx task reassign <id> --to <ref>`: the daemon moves
// the task (back to pending), then the NEW owner gets the down message.
func (c taskCall) reassign(args []string) int {
	fs := flag.NewFlagSet("pdx task reassign", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	to := fs.String("to", "", "")
	asJSON := fs.Bool("json", false, "")
	id, ok := c.oneID(fs, args)
	if !ok {
		return ExitUsage
	}
	if strings.TrimSpace(*to) == "" {
		return taskUsageErr(c.stderr, "--to: 需要 member 的 ref（_xxxxxx）或地址")
	}
	client, inbox, ok := teamSetup("task", *cfgPath, c.getenv, c.stderr, c.clientOpts)
	if !ok {
		return ExitError
	}
	var raw json.RawMessage
	req := team.ReassignTaskRequest{OriginInbox: inbox, To: *to}
	if _, err := client.Do(c.ctx, http.MethodPost, taskPath(id)+"/reassign", req, &raw); err != nil {
		return teamReportErr("task", err, c.stderr)
	}
	task, compact, ok := decodeTask(raw, c.stderr)
	if !ok {
		return ExitError
	}
	return c.notify(client, inbox, task, compact, "reassigned", *asJSON)
}

// setStatus implements start, done and delete. A replay would answer
// bad_task_transition, so the POST is not Idempotent.
func (c taskCall) setStatus(verb string, to team.TaskStatus, args []string) int {
	fs := flag.NewFlagSet("pdx task "+verb, flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	asJSON := fs.Bool("json", false, "")
	id, ok := c.oneID(fs, args)
	if !ok {
		return ExitUsage
	}
	client, inbox, ok := teamSetup("task", *cfgPath, c.getenv, c.stderr, c.clientOpts)
	if !ok {
		return ExitError
	}
	var raw json.RawMessage
	req := team.TaskStatusRequest{OriginInbox: inbox, Status: to}
	if _, err := client.Do(c.ctx, http.MethodPost, taskPath(id)+"/status", req, &raw); err != nil {
		return teamReportErr("task", err, c.stderr)
	}
	task, compact, ok := decodeTask(raw, c.stderr)
	if !ok {
		return ExitError
	}
	if *asJSON {
		fmt.Fprintln(c.stdout, compact)
	} else {
		fmt.Fprintf(c.stdout, "%s %s\n", sanitizeCell(task.ID), sanitizeCell(string(task.Status)))
	}
	return ExitOK
}

// notify sends task's owner the down message and prints the result. The task
// is already stored, so a failed send is exit 1 with the task's JSON on
// stdout and the manual command on stderr, never a rollback.
func (c taskCall) notify(client *daemonclient.Client, inbox string, task team.Task, compact, verb string, asJSON bool) int {
	detail, code := sendTaskMessage(c.ctx, client, inbox, task)
	if code != "" {
		fmt.Fprintln(c.stdout, compact)
		addr := sanitizeCell(task.Owner.Address)
		if addr == "" {
			addr = "<owner address>"
		}
		fmt.Fprintf(c.stderr, "pdx task: 任務 %s 已存下，但給 owner 的訊息沒送出（%s）；請手動送：pdx msg send %s \"$(pdx task show %s --message)\" %s\n",
			sanitizeCell(task.ID), sanitizeCell(detail), addr, sanitizeCell(task.ID), sanitizeCell(code))
		return ExitError
	}
	if asJSON {
		fmt.Fprintln(c.stdout, compact)
	} else {
		fmt.Fprintf(c.stdout, "%s %s (%s) → %s: %s\n", verb, sanitizeCell(task.ID), sanitizeCell(string(task.Status)),
			sanitizeCell(task.Owner.Ref), sanitizeCell(task.Subject))
	}
	return ExitOK
}

// sendTaskMessage sends the down message through POST /api/peers/send, from
// the lead's inbox so the member's replies go to the lead. One request, never
// replayed (client.Once): a send that may have arrived must not arrive twice.
// code "" is success; otherwise detail and code are briefErr's.
func sendTaskMessage(ctx context.Context, client *daemonclient.Client, inbox string, task team.Task) (detail, code string) {
	if task.Owner.Address == "" {
		return "daemon 沒有回這個 owner 的地址", "no_address"
	}
	sctx, cancel := context.WithTimeout(ctx, briefTimeout)
	defer cancel()
	req := ipeers.SendRequest{To: task.Owner.Address, Text: team.TaskDownMessage(task), OriginInbox: inbox}
	if _, err := client.Once(sctx, http.MethodPost, "/api/peers/send", req, nil); err != nil {
		return briefErr(err)
	}
	return "", ""
}

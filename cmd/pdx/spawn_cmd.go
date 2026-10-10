package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wake/purdex/cmd/pdx/daemonclient"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// spawnNewID mints the spawn op id, the idempotency key of every POST of
// one spawn (a test seam).
var spawnNewID = uuid.NewString

// spawnSettleBound caps the whole wait for one spawn, below the Bash tool's
// 10-minute limit (as `pdx lead request --wait` 9m), so an op the daemon
// keeps answering running cannot hold the lead forever. A var only so tests
// can shorten it.
var spawnSettleBound = 9 * time.Minute

// briefMaxBytes is the longest brief whose message (the first line with the
// longest lead address and team id, "\n", the brief) still fits the peers
// text limit, so a brief that passes it is never refused for its size.
var briefMaxBytes = ipeers.MaxTextBytes - len("\n") - len(fmt.Sprintf(team.MemberBriefPrefixFmt,
	strings.Repeat("a", maxLeadAddressBytes), strings.Repeat("0", teamIDBytes)))

// briefReadTimeout bounds reading --brief-file, whose open blocks on a FIFO
// nobody writes to. A var only so tests can shorten it.
var briefReadTimeout = 10 * time.Second

// briefTimeout bounds the brief's one POST (as `pdx msg send`). A var only
// so tests can shorten it.
var briefTimeout = msgSendTimeout

// spawnArgs is a parsed, validated `pdx spawn`.
type spawnArgs struct {
	cfgPath, cwd, title, model, effort string
	host                               string // a paired member host's alias ("" = this host)
	brief                              string
	hasBrief                           bool
	taskSubject                        string   // T-2: "" = no task
	doneWhen                           []string // T-2
}

// parseSpawnArgs checks the grammar, U20 (a)'s model and effort included,
// before anything is read or asked. ok=false: a usage line was written (exit 2).
func parseSpawnArgs(args []string, stderr io.Writer) (spawnArgs, bool) {
	fs := flag.NewFlagSet("pdx spawn", flag.ContinueOnError)
	var a spawnArgs
	var briefFile string
	fs.StringVar(&a.cfgPath, "config", "", "")
	fs.StringVar(&a.cwd, "cwd", "", "")
	fs.StringVar(&a.host, "host", "", "")
	fs.StringVar(&a.title, "title", "", "")
	fs.StringVar(&a.model, "model", "", "")
	fs.StringVar(&a.effort, "effort", "", "")
	fs.StringVar(&a.brief, "brief", "", "")
	fs.StringVar(&briefFile, "brief-file", "", "")
	fs.StringVar(&a.taskSubject, "task-subject", "", "")
	var doneWhen listFlag
	fs.Var(&doneWhen, "done-when", "")
	reject := func(msg string) (spawnArgs, bool) {
		fmt.Fprintf(stderr, "pdx spawn: %s\n%s\n", msg, spawnUsage)
		return a, false
	}
	pos, err := parseTeamFlags(fs, args)
	if err != nil {
		return reject(err.Error())
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	switch {
	case len(pos) != 0:
		return reject(fmt.Sprintf("unexpected argument %q", pos[0]))
	case set["host"] && (a.host == "" || strings.ContainsAny(a.host, " \t\r\n/")):
		return reject("--host 必須是已配對主機的別名")
	case set["host"] && (!set["cwd"] || !filepath.IsAbs(a.cwd)):
		return reject("--host 需要 --cwd，且必須是那台主機上的絕對路徑")
	case set["model"] && !team.ValidModel(a.model):
		return reject(fmt.Sprintf("--model %q 不是模型名稱（別名如 sonnet、opus，或完整名稱，可加 [1m]）", a.model))
	case set["effort"] && !team.ValidEffort(a.effort):
		return reject(fmt.Sprintf("--effort %q 必須是 %s 之一", a.effort, strings.Join(team.Efforts, "、")))
	case set["title"] && ipeers.ValidateTitle(a.title) != nil:
		return reject("--title: " + ipeers.ValidateTitle(a.title).Error())
	case set["brief"] && set["brief-file"]:
		return reject("--brief 與 --brief-file 只能擇一")
	case len(doneWhen) > 0 && !set["task-subject"]:
		return reject("--done-when 需要 --task-subject")
	}
	if set["task-subject"] {
		if err := team.ValidTaskSubject(a.taskSubject); err != nil {
			return reject("--task-subject: " + err.Error())
		}
		if err := team.ValidDoneWhen(doneWhen); err != nil {
			return reject("--done-when: " + err.Error())
		}
		a.doneWhen = doneWhen
	}
	a.hasBrief = set["brief"] || set["brief-file"]
	if set["brief-file"] {
		if a.brief, err = readBriefFile(briefFile); err != nil {
			return reject("--brief-file: " + err.Error())
		}
	}
	if a.hasBrief {
		// Checked now so a brief the daemon would refuse fails before a
		// member opens, not after.
		if strings.TrimSpace(a.brief) == "" {
			return reject("brief 不能為空")
		}
		if len(a.brief) > briefMaxBytes || ipeers.ValidateText(a.brief) != nil {
			return reject(fmt.Sprintf("brief 必須是不超過 %d bytes 的 UTF-8 文字（peers 訊息上限 %d bytes，扣掉首行）", briefMaxBytes, ipeers.MaxTextBytes))
		}
	}
	if set["task-subject"] {
		// The brief is the task's description, and the whole message (prefix,
		// task header, brief, done-when, report line) must fit the peers limit.
		if err := team.ValidTaskDescription(a.brief); err != nil {
			return reject("brief 當任務描述不合格：" + err.Error())
		}
		worst := fmt.Sprintf(team.MemberBriefPrefixFmt, strings.Repeat("a", maxLeadAddressBytes), strings.Repeat("0", teamIDBytes)) + "\n" +
			team.TaskDownMessage(team.Task{ID: team.TaskWorstCaseID, Subject: a.taskSubject, Description: a.brief, DoneWhen: a.doneWhen})
		if len(worst) > ipeers.MaxTextBytes || ipeers.ValidateText(worst) != nil {
			return reject(fmt.Sprintf("給 member 的任務訊息太長或不合格（上限 %d bytes）", ipeers.MaxTextBytes))
		}
	}
	return a, true
}

// readBriefFile reads at most one byte past briefMaxBytes of path, so a huge
// file, /dev/zero or a FIFO whose writer never closes costs one bounded read
// (the caller refuses anything longer). An open or read that has not ended
// within briefReadTimeout (a FIFO nobody writes to) is an error; its
// goroutine is left behind, and the process exits soon after.
func readBriefFile(path string) (string, error) {
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		f, err := os.Open(path)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, int64(briefMaxBytes)+1))
		done <- result{b, err}
	}()
	select {
	case r := <-done:
		return string(r.b), r.err
	case <-time.After(briefReadTimeout):
		return "", fmt.Errorf("%s 內沒有讀完（沒有寫入端的 FIFO？）", briefReadTimeout)
	}
}

// runSpawnCmd implements `pdx spawn` (spec §7.2): one op id, POSTed again
// while the op runs (the daemon joins it), then the member on stdout and
// the brief from the lead's inbox.
func runSpawnCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	a, ok := parseSpawnArgs(args, stderr)
	if !ok {
		return ExitUsage
	}
	client, inbox, ok := teamSetup("spawn", a.cfgPath, getenv, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	cwd, err := filepath.Abs(a.cwd) // "" is the working directory (coordinator decision 6); a --host cwd is absolute already
	if err != nil {
		fmt.Fprintf(stderr, "pdx spawn: %v\n", err)
		return ExitError
	}
	if a.model == "" {
		fmt.Fprintln(stderr, team.ReminderNoModel) // U20 (c): the spawn goes on
	}
	req := team.SpawnRequest{ID: spawnNewID(), OriginInbox: inbox, Cwd: cwd, Title: a.title, Model: a.model, Effort: a.effort, Host: a.host}
	if a.taskSubject != "" {
		req.Task = &team.SpawnTask{Subject: a.taskSubject, Description: a.brief, DoneWhen: a.doneWhen}
	}
	op, code := spawnSettle(ctx, client, req, stderr)
	if code != ExitOK {
		return code
	}
	switch {
	case op.State == team.SpawnFailed && op.Reason == team.SpawnReasonStartTimeout:
		fmt.Fprintf(stderr, "pdx spawn: %s %s\n", spawnStartTimeoutHint, team.SpawnReasonStartTimeout)
		return ExitMemberFailed
	case op.State == team.SpawnFailed && remoteSpawnReasonExit[op.Reason] != 0:
		// what the member host (or the 10 minute void, or an unpairing) said, for a spawn --host
		fmt.Fprintf(stderr, "pdx spawn: spawn %s 失敗 %s\n", sanitizeCell(op.ID), sanitizeCell(op.Reason))
		return remoteSpawnReasonExit[op.Reason]
	case op.State == team.SpawnFailed:
		fmt.Fprintf(stderr, "pdx spawn: spawn %s 失敗 %s\n", sanitizeCell(op.ID), sanitizeCell(op.Reason))
		return ExitError
	case op.State != team.SpawnDone || op.Member == nil:
		fmt.Fprintf(stderr, "pdx spawn: daemon 回了無法辨識的 spawn（state %q） invalid_response\n", sanitizeCell(string(op.State)))
		return ExitError
	}
	m := op.Member
	out, _ := json.Marshal(spawnOutput{Ref: m.Ref, Address: m.Address, TmuxSession: m.TmuxSession,
		SessionID: m.SessionID, HostID: m.HostID, SpawnOp: op.ID, TaskID: op.TaskID}) // strings only: cannot fail
	fmt.Fprintln(stdout, string(out))
	if a.taskSubject != "" {
		if op.TaskID == "" {
			fmt.Fprintf(stderr, "pdx spawn: member 已開啟，但 daemon 沒有回任務 id（舊版 daemon？）；請用 pdx task ls 確認 invalid_response\n")
			return ExitError
		}
		// The task replaces the plain brief: one message, the task header first.
		task := team.Task{ID: op.TaskID, Subject: a.taskSubject, Description: a.brief, DoneWhen: a.doneWhen}
		return sendBriefText(ctx, client, inbox, op, team.TaskDownMessage(task), stderr)
	}
	if !a.hasBrief {
		return ExitOK
	}
	return sendBrief(ctx, client, inbox, op, a.brief, stderr)
}

// spawnSettle POSTs req until its op leaves running: the same id each time,
// so the daemon joins the op, across a daemon restart too (Idempotent).
// Three consecutive attempts with no answer at all are exit 20 (spec §9.1).
// Past spawnSettleBound it stops waiting: exit 1, spawn_wait_timeout, the op
// named on stderr, because the op may still finish on the daemon.
func spawnSettle(ctx context.Context, client *daemonclient.Client, req team.SpawnRequest, stderr io.Writer) (team.SpawnOp, int) {
	// A cancellation, not a deadline: the client bounds each attempt
	// (ErrNoAnswer, counted below) only under a ctx without a deadline.
	bounded, cancel := context.WithCancel(ctx)
	defer cancel()
	defer time.AfterFunc(spawnSettleBound, cancel).Stop()
	hung := 0
	for {
		var op team.SpawnOp
		_, err := client.Do(bounded, http.MethodPost, "/api/team/spawns", req, &op, daemonclient.Idempotent())
		switch {
		case err == nil && op.State != team.SpawnRunning:
			return op, ExitOK
		case ctx.Err() == nil && bounded.Err() != nil:
			fmt.Fprintf(stderr, "pdx spawn: spawn %s 在期限內沒有結束，daemon 上可能仍在進行；先用 pdx team 確認，不要直接重開 %s\n",
				req.ID, spawnWaitTimeout)
			return op, ExitError
		case err == nil: // running: the same body again joins the op
			hung = 0
		case ctx.Err() == nil && (errors.Is(err, daemonclient.ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded)):
			if hung++; hung >= teamMaxHungPolls {
				fmt.Fprintln(stderr, "pdx spawn: daemon 沒有回應 daemon_unavailable")
				return op, ExitUnavailable
			}
		default:
			return op, teamReportErr("spawn", err, stderr)
		}
	}
}

// spawnOutput is what a done spawn prints on stdout, one JSON line (§7.2 step 6).
type spawnOutput struct {
	TaskID      string `json:"task_id,omitempty"` // T-2
	Ref         string `json:"ref"`
	Address     string `json:"address"`
	TmuxSession string `json:"tmux_session"`
	SessionID   string `json:"session_id"`
	HostID      string `json:"host_id"`
	SpawnOp     string `json:"spawn_op"`
}

// sendBrief sends the brief to the new member through POST
// /api/peers/send, from the lead's inbox so the member's replies go to the
// lead, after the one-line prefix (spec §7.2). One request, never replayed:
// a send that may have arrived must not arrive twice. A failure is exit 1
// with the member already on stdout (coordinator decision 14).
func sendBrief(ctx context.Context, client *daemonclient.Client, inbox string, op team.SpawnOp, brief string, stderr io.Writer) int {
	return sendBriefText(ctx, client, inbox, op, brief, stderr)
}

// sendBriefText is sendBrief for a text already composed (the task header
// and all, T-2).
func sendBriefText(ctx context.Context, client *daemonclient.Client, inbox string, op team.SpawnOp, brief string, stderr io.Writer) int {
	text := fmt.Sprintf(team.MemberBriefPrefixFmt, op.LeadAddress, op.TeamID) + "\n" + brief
	sctx, cancel := context.WithTimeout(ctx, briefTimeout)
	defer cancel()
	req := ipeers.SendRequest{To: op.Member.Address, Text: text, OriginInbox: inbox}
	if _, err := client.Once(sctx, http.MethodPost, "/api/peers/send", req, nil); err != nil {
		detail, code := briefErr(err)
		fmt.Fprintf(stderr, "pdx spawn: member 已開啟，但 brief 沒送出（%s）；請用 pdx msg send %s 手動送 %s\n",
			sanitizeCell(detail), sanitizeCell(op.Member.Address), sanitizeCell(code))
		return ExitError
	}
	return ExitOK
}

// briefErr is a failed brief's detail and code, the code last on stderr as
// for every API error: the daemon's own code when it sent one, else the
// CLI's — unsupported (plain 404), no_answer (the bound ran out),
// invalid_response (an answer with no code), daemon_unavailable (no answer).
func briefErr(err error) (detail, code string) {
	var se *daemonclient.StatusError
	switch {
	case errors.As(err, &se) && se.API.Error != "":
		return se.API.Detail, se.API.Error
	case se != nil:
		return se.Error(), "invalid_response"
	case errors.Is(err, daemonclient.ErrUnsupported):
		return "這個 daemon 沒有 /api/peers/send", daemonclient.ErrUnsupported.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("daemon %s 內沒有回應", briefTimeout), daemonclient.ErrNoAnswer.Error()
	default:
		return err.Error(), daemonclient.ErrUnavailable.Error()
	}
}

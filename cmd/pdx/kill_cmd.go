package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// runKillCmd implements `pdx kill <ref>` (spec §7.3): the target goes as
// typed, the daemon matches it among the caller's members only. A replay is
// safe (a killed member answers 200 again), so the POST is Idempotent.
func runKillCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx kill", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	handoff := fs.Bool("handoff", false, "")
	pos, err := parseTeamFlags(fs, args)
	if err == nil && (len(pos) != 1 || strings.TrimSpace(pos[0]) == "") {
		err = errors.New("需要剛好一個 <ref>（_xxxxxx 或 <host>/<name>）")
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx kill: %v\n%s\n", err, killUsage)
		return ExitUsage
	}
	if *handoff { // the long poll of the op needs the relay's attempt bound
		clientOpts = append([]daemonclient.Option{daemonclient.WithAttemptTimeout(relayAttemptTimeout)}, clientOpts...)
	}
	client, inbox, ok := teamSetup("kill", *cfgPath, getenv, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	if *handoff {
		return runKillHandoff(ctx, client, inbox, pos[0], stdout, stderr)
	}
	var m team.Member
	if _, err := client.Do(ctx, http.MethodPost, "/api/team/kill", team.KillRequest{OriginInbox: inbox, Target: pos[0]}, &m, daemonclient.Idempotent()); err != nil {
		return teamReportErr("kill", err, stderr)
	}
	out, err := json.Marshal(m)
	if err != nil {
		fmt.Fprintf(stderr, "pdx kill: %v\n", err)
		return ExitError
	}
	fmt.Fprintln(stdout, string(out))
	return ExitOK
}

// killHandoffCloseWait is how long the command waits, after the handoff is written, for the member to be closed; a var so tests can
// shorten it.
var killHandoffCloseWait = 20 * time.Second

// runKillHandoff is `pdx kill <ref> --handoff` (RT-3c): the daemon opens a handoff op (201) and the command follows it like
// `pdx relay <ref> --wait`. The op ending cancelled / half means the file is written and the kill follows; the command then waits for
// the member to show closed. Exit 0 closed; 14 the member did not respond; 13 refused; 1 written but not closed (kill again).
func runKillHandoff(ctx context.Context, client *daemonclient.Client, inbox, target string, stdout, stderr io.Writer) int {
	var res team.RelayCreateResponse
	// not Idempotent: a replay would ask for a second op while the first is open
	if _, err := client.Do(ctx, http.MethodPost, "/api/team/kill", team.KillRequest{OriginInbox: inbox, Target: target, Handoff: true}, &res); err != nil {
		return teamReportErr("kill", err, stderr)
	}
	fmt.Fprintf(stderr, "交接已送出（%s）\n", res.Op.ID)
	finish := func(op team.RelayOp) int {
		if op.State != team.RelayCancelled || op.Reason != team.RelayReasonHalf {
			return finishRelayOp(op, stdout, stderr)
		}
		if relayPrintJSON(stdout, stderr, op) != ExitOK {
			return ExitError
		}
		if waitMemberClosed(ctx, client, inbox, op.Ref) {
			return ExitOK
		}
		fmt.Fprintf(stderr, "pdx kill: 交接檔已寫好（%s），但 member 沒有關閉；請再下一次 pdx kill\n", op.HandoffPath)
		return ExitError
	}
	return followRelayOp(ctx, client, res.Op, relayMaxWait, stdout, stderr, finish)
}

// waitMemberClosed polls the team until the member with ref shows killed or gone, at most killHandoffCloseWait.
func waitMemberClosed(ctx context.Context, client *daemonclient.Client, inbox, ref string) bool {
	deadline := time.Now().Add(killHandoffCloseWait)
	for {
		var v team.TeamView
		if _, err := client.Do(ctx, http.MethodGet, "/api/team?origin_inbox="+url.QueryEscape(inbox), nil, &v); err == nil {
			for _, m := range v.Members {
				if m.Ref == ref && (m.State == team.MemberKilled || m.State == team.MemberGone) {
					return true
				}
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

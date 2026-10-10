package main

// `pdx spawn`, `pdx kill` and `pdx team`: the lead's commands (lead-team-
// relay spec §7.2, §7.3, U20; plan v3 P4-7). Each runs inside the lead's
// Claude Code session, which the daemon attributes by its inbox. API errors
// print `pdx <cmd>: <detail> <code>`, the code last; exit codes are spec §14.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

const spawnUsage = "usage: pdx spawn [--host <alias> --cwd <dir on that host>] [--cwd <dir>] [--title <t>] [--model <m>] [--effort <e>] [--brief-file <f> | --brief <text>] [--task-subject <s> [--done-when <line>]…] [--config <path>]\n" +
	"       (--cwd defaults to this directory; --host runs the member on a paired host, where --cwd is a required absolute path\n" +
	"        under the roots that host granted; --effort is low, medium, high, xhigh or max;\n" +
	"        without --model the member runs this host's default model, which is not fixed;\n" +
	"        --task-subject makes the brief that member's first task, and --done-when needs it)"

const killUsage = "usage: pdx kill <ref> [--config <path>]\n" +
	"       (<ref> is _xxxxxx, or an address from pdx team; only a member of your own team)"

const teamUsage = "usage: pdx team [--json] [--config <path>]"

const (
	// teamAttemptTimeout bounds one request: a spawn POST waits up to
	// team.SpawnPollWaitS daemon-side, plus room (as lead's polls).
	teamAttemptTimeout = 35 * time.Second
	// teamMaxHungPolls: consecutive spawn POSTs with no answer at all before exit 20 (spec §9.1).
	teamMaxHungPolls = 3
	// maxLeadAddressBytes is the longest lead address the brief's first line
	// carries: "<alias>/<name or ref>" (the origin resolver's address), the
	// alias at most 64 bytes (config aliasPattern
	// ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$), the name at most 64
	// (ipeers.RoutableName ^[a-z0-9][a-z0-9-]{1,63}$; a ref is 7). An alias
	// outside that rule (a hand-edited config) can only make the daemon refuse
	// the send, which is reported as a failed brief.
	maxLeadAddressBytes = 64 + 1 + 64
	// teamIDBytes: the team id is the approving request's id, which the
	// daemon stores as a canonical UUID.
	teamIDBytes = 36
	// spawnStartTimeoutHint is the stderr hint of member_start_timeout: a
	// member that never registers ran on a host without the Purdex hooks.
	spawnStartTimeoutHint = "member 沒有在 20 秒內啟動（這台主機需要 Purdex hooks：pdx setup --agent cc）"
	// spawnWaitTimeout is the CLI's own code, not a daemon wire code (so it
	// is not in internal/team): `pdx spawn` stopped waiting after
	// spawnSettleBound while the op may still run, exit 1. Only the daemon's
	// failed{member_start_timeout} — the session killed, its place freed —
	// is exit 14 (critic ruling on PR P4-7).
	spawnWaitTimeout = "spawn_wait_timeout"
)

// remoteSpawnReasonExit is the exit code of a spawn --host that failed with a reason only a member host (or the lead host's
// own clean-up) gives: the member host did not answer, or its refusal of the command (cross-host spec §6.2, §3.3).
var remoteSpawnReasonExit = map[string]int{
	"remote_unreachable":    ExitMemberFailed,
	team.ErrCwdOutsideGrant: ExitRefused,
	"host_not_allowed":      ExitRefused,
	"capacity_exceeded":     ExitRefused,
}

// teamRefusalCodes are the team-rule refusals these commands can meet: exit
// 13 (spec §14). relay_open is a kill of a member mid-relay (P4-6 review).
// Any other API code is exit 1.
var teamRefusalCodes = map[string]bool{
	team.ErrNotLead:         true,
	team.ErrTeamFull:        true,
	team.ErrCwdOutsideGrant: true,
	team.ErrNotYourMember:   true,
	team.ErrRelayOpen:       true,
	// Adopt (spec D-U24-2; the same codes close a request at the click as its close_reason).
	team.ErrAdoptSelf:            true,
	team.ErrAdoptTargetIsLead:    true,
	team.ErrAdoptAlreadyMember:   true,
	team.ErrAdoptTargetNotFound:  true,
	team.ErrAdoptTargetAmbiguous: true,
	team.ErrRemoteUnsupported:    true,
	"host_not_allowed":           true, // the member host has not allowed this host (rule 7)
	team.ErrRequestOpen:          true,
	// The task routes' refusals (plan T-1c).
	team.ErrNotMember:         true,
	team.ErrTaskNotFound:      true,
	team.ErrNotTaskOwner:      true,
	team.ErrBadTaskTransition: true,
	team.ErrBlockedByUnknown:  true,
	team.ErrBlockedByCycle:    true,
	team.ErrOwnerNotActive:    true,
}

func runSpawn(args []string) {
	os.Exit(runSpawnCmd(context.Background(), args, os.Getenv, os.Stdout, os.Stderr))
}

// parseTeamFlags parses fs over args with flags and positionals in any
// order (`pdx kill <ref> --config c` and `pdx kill --config c <ref>`) and
// returns the positionals.
func parseTeamFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// teamSetup reads the caller's inbox, then the config, and builds the
// restart-aware client. ok=false means a line was written (exit 1).
func teamSetup(cmd, cfgPath string, getenv func(string) string, stderr io.Writer, clientOpts []daemonclient.Option) (*daemonclient.Client, string, bool) {
	inbox := getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if inbox == "" {
		fmt.Fprintf(stderr, "pdx %s: CLAUDE_CODE_MESSAGING_SOCKET is unset — run inside a Claude Code session\n", cmd)
		return nil, "", false
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx %s: %v\n", cmd, err)
		return nil, "", false
	}
	base := fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)
	opts := append([]daemonclient.Option{daemonclient.WithStderr(stderr), daemonclient.WithAttemptTimeout(teamAttemptTimeout)}, clientOpts...)
	return daemonclient.New(base, cfg.Token, opts...), inbox, true
}

// teamReportErr maps a client error to one stderr line and an exit code.
func teamReportErr(cmd string, err error, stderr io.Writer) int {
	var se *daemonclient.StatusError
	switch {
	case errors.Is(err, daemonclient.ErrUnavailable):
		fmt.Fprintf(stderr, "pdx %s: 等了 30 秒 daemon 仍沒有回應 daemon_unavailable\n", cmd)
		return ExitUnavailable
	case errors.Is(err, daemonclient.ErrUnsupported):
		fmt.Fprintf(stderr, "pdx %s: 這個 daemon 沒有這個 /api/team 路由，請先更新 daemon unsupported\n", cmd)
		return ExitUnsupported
	case errors.As(err, &se) && se.API.Error != "":
		detail := se.API.Detail
		if se.API.Op != nil { // relay_open: the relay to wait for (pdx relay op <id>)
			detail += "（relay op " + se.API.Op.ID + "）"
		}
		fmt.Fprintln(stderr, strings.TrimSpace("pdx "+cmd+": "+sanitizeCell(detail)), sanitizeCell(se.API.Error))
		if teamRefusalCodes[se.API.Error] {
			return ExitRefused
		}
		return ExitError
	default:
		fmt.Fprintf(stderr, "pdx %s: %s\n", cmd, sanitizeCell(err.Error()))
		return ExitError
	}
}

func runKill(args []string) {
	os.Exit(runKillCmd(context.Background(), args, os.Getenv, os.Stdout, os.Stderr))
}

func runTeam(args []string) {
	os.Exit(runTeamCmd(context.Background(), args, os.Getenv, os.Stdout, os.Stderr))
}

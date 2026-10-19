package main

// `pdx jobs` and `pdx wwd` (wwd spec §8.1): look at jobs and control the local wwd. The work is in internal/wwd/jobscmd.

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/jobscmd"
)

func runJobs(args []string) { os.Exit(wwdCommand(args, jobscmd.Jobs)) }

func runWWD(args []string) { os.Exit(wwdCommand(args, jobscmd.WWD)) }

func wwdCommand(args []string, f func(context.Context, []string, jobscmd.Env) int) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	return f(ctx, args, jobscmd.Env{
		API: client.New(client.DefaultSocket()), Session: submitterSession(), Stdout: os.Stdout, Stderr: os.Stderr,
	})
}

// submitterSession is the session `pdx test` and `pdx jobs` name as the caller's: PDX_SESSION_ID when a script names one on purpose, else
// the gate's own reading of the Claude session (CLAUDE_CODE_SESSION_ID, else CLAUDE_SESSION_ID). Nothing sets PDX_SESSION_ID, so reading
// only it left every job's submitter.session empty (#3477). The value becomes the job's identity for good and the completion notice finds
// the conversation by it, so one that does not look like a session id counts as not set: each source is checked on its own, so an invalid one falls to the next and none valid is empty.
func submitterSession() string {
	for _, name := range []string{"PDX_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "CLAUDE_SESSION_ID"} {
		if v := os.Getenv(name); facts.ValidSessionID(v) {
			return v
		}
	}
	return ""
}

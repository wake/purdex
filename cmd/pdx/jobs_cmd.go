package main

// `pdx jobs` and `pdx wwd` (wwd spec §8.1): look at jobs and control the local wwd. The work is in internal/wwd/jobscmd.

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/jobscmd"
)

func runJobs(args []string) { os.Exit(wwdCommand(args, jobscmd.Jobs)) }

func runWWD(args []string) { os.Exit(wwdCommand(args, jobscmd.WWD)) }

func wwdCommand(args []string, f func(context.Context, []string, jobscmd.Env) int) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	return f(ctx, args, jobscmd.Env{
		API: client.New(client.DefaultSocket()), Session: os.Getenv("PDX_SESSION_ID"), Stdout: os.Stdout, Stderr: os.Stderr,
	})
}

package main

// `pdx test` (wwd spec §8.1): run a task declared in .wwd/tasks.toml. The work is in internal/wwd/testcmd; this only connects
// it to the process: the wwd socket, the gate for the local fast lane, the environment and the interrupt signal.

import (
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/fastlane"
	"github.com/wake/purdex/internal/wwd/localrec"
	"github.com/wake/purdex/internal/wwd/testcmd"
)

func runTest(args []string) {
	os.Exit(testMain(args, os.Stdout, os.Stderr))
}

// testMain is runTest with its streams, for tests; it returns the exit code.
func testMain(args []string, stdout, stderr io.Writer) int {
	a, err := testcmd.ParseArgs(args)
	if err != nil {
		if err.Error() == "help" {
			io.WriteString(stdout, testcmd.Usage)
			return 0
		}
		io.WriteString(stderr, "pdx test: "+err.Error()+"\n\n"+testcmd.Usage)
		return testcmd.ExitUsage
	}
	dir, err := os.Getwd()
	if err != nil {
		io.WriteString(stderr, "pdx test: "+err.Error()+"\n")
		return testcmd.ExitFailed
	}
	// An interrupt ends the wait, not the job: the job outlives its caller (spec D5).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	api := client.New(client.DefaultSocket())
	o := testcmd.Options{
		Args: a, Dir: dir, API: api, Env: os.Environ(),
		Session: submitterSession(), Stdout: stdout, Stderr: stderr,
		Recorder: localrec.Recorder{API: api}, // a wwd without the local-runs route runs the command unrecorded
	}
	if home, err := os.UserHomeDir(); err == nil {
		o.LogDir = filepath.Join(home, ".local", "share", "wwd", "local-runs")
	}
	// The local fast lane's slot is wwd's own, taken when the run is registered (spec §5.2): the command is run here with no gate slot.
	o.Gate = fastlane.Direct{}
	return testcmd.Run(ctx, o)
}

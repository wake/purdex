package testcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/fastlane"
	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/wire"
)

// outputLines is how much of the end of a run's log a JSON report of an undeclared command carries.
const outputLines = 200

// runFast runs req on the fast lane (waiting here for a held lock), keeps its files, and prints the report; took is false only when
// the lane does not allow the task. label names the run in the report and in the name of its directory; note is added to the
// report when not empty. live is for a caller that has to see the command's output (an undeclared command, whose caller has no other
// way to read it): the output goes to this process's stdout and stderr as it is written and the report follows on stderr, so
// stdout stays the command's own; with --json nothing is streamed and the one JSON object on stdout carries the end of the output.
func runFast(ctx context.Context, o Options, task *manifest.Task, req fastlane.Request, label, note string, live bool) (code int, took bool) {
	stream := live && !o.Args.JSON
	if stream {
		req.TeeStdout, req.TeeStderr = o.Stdout, o.Stderr
	}
	// The run's files stay after the command: the log path is printed, and wwd's sweep removes old ones.
	out := ""
	if o.LogDir != "" {
		// one directory per run, created atomically: two runs of one task in one second never share files
		if err := os.MkdirAll(o.LogDir, 0o700); err != nil {
			fmt.Fprintf(o.Stderr, "pdx test: %v\n", err)
			return ExitFailed, true
		}
		d, err := os.MkdirTemp(o.LogDir, fmt.Sprintf("%s-%s-", time.Now().Format("20060102-150405"), label))
		if err != nil {
			fmt.Fprintf(o.Stderr, "pdx test: %v\n", err)
			return ExitFailed, true
		}
		out = d
	}
	req.OutDir = out
	res, err := runWhenFree(ctx, o, task, req)
	var argErr *fastlane.ArgError
	switch {
	case errors.Is(err, fastlane.ErrNotLocal):
		return 0, false
	case errors.Is(err, errWaitedOut):
		r := Report{Task: label, State: wire.StateExpired, Reason: "queue_timeout", Local: true}
		Print(o.Stdout, r, o.Args.JSON)
		return ExitCode(r), true
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ExitCancelled, true
	case errors.As(err, &argErr):
		fmt.Fprintf(o.Stderr, "pdx test: %v\n", err)
		return ExitUsage, true
	case err != nil:
		fmt.Fprintf(o.Stderr, "pdx test: %v\n", err)
		return ExitFailed, true
	}
	var output []string
	if live && o.Args.JSON {
		output = tailLines(res.Tail, res.TailCut, outputLines)
	}
	if out == "" {
		defer res.Cleanup() // no place to keep the files: they go with the command
		res.LogPath = ""
	}
	if res.Err != nil {
		fmt.Fprintf(o.Stderr, "pdx test: %v\n", res.Err)
	}
	r := Report{ID: res.JobID, Task: label, State: res.State, Reason: res.Reason, Local: true, LogPath: res.LogPath, Summary: &res.Summary}
	if res.State == wire.StateSucceeded || res.State == wire.StateFailed && res.Reason == "" || res.Reason == "timeout" {
		e := res.Exit
		r.Exit = &e
	}
	if res.JobID == "" {
		r.Notes = append(r.Notes, "this run was not recorded: wwd has no fast-lane entry yet")
	}
	if res.TreeErr != nil {
		r.Notes = append(r.Notes, fmt.Sprintf("the working tree could not be recorded when the run went on (the record has no tree): %v", res.TreeErr))
	}
	if note != "" {
		r.Notes = append(r.Notes, note)
	}
	r.Output = output
	if stream {
		Print(o.Stderr, r, false) // the command's output was stdout's; the end of the run is told apart from it
	} else {
		Print(o.Stdout, r, o.Args.JSON)
	}
	return ExitCode(r), true
}

// tailLines is the last n lines of b, the end of the output; when cut is set b starts inside a line and that first, partial line is
// left out. nil when there is nothing.
func tailLines(b []byte, cut bool, n int) []string {
	if len(b) == 0 {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if cut && len(lines) > 1 {
		lines = lines[1:]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

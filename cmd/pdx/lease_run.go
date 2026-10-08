package main

// `pdx lease run` (host-resource-lease plan Task 1.8, P1-3b): take room from
// the pool, run the command as a child, give the room back. The `pdx` process
// is the lease's holder (scope process), so the lease's tree is exactly the
// command; if pdx dies the sweeper ends the lease as holder_gone.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
)

// leaseSignals gives run the signals to pass on to the child, and a function
// to stop listening; a seam so tests can send signals without sending any.
var leaseSignals = func() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return ch, func() { signal.Stop(ch) }
}

// leaseStdinIsTTY says whether stdin is a terminal (a seam for tests): then the
// child stays in pdx's own process group, which is the terminal's foreground
// group. A child in a group of its own would be stopped by SIGTTIN the moment it
// read the terminal.
var leaseStdinIsTTY = func() bool {
	_, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	return err == nil
}

// leaseWaitNotice is how long a wait must last to be worth a line.
const leaseWaitNotice = time.Second

// splitRunArgs separates run's own flags from the command: everything after
// the first "--" is the command.
func splitRunArgs(args []string) (flags, command []string, ok bool) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:], len(args[i+1:]) > 0
		}
	}
	return args, nil, false
}

// runLeaseRun implements `pdx lease run (--kind K | --weight N) [--wait 5m]
// -- <cmd…>`. The exit code is the child's (128+n when a signal ended it); 12
// when the wait was interrupted; 126 and 127 when the command cannot be run.
func runLeaseRun(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	flagArgs, command, hasCmd := splitRunArgs(args)
	fs := flag.NewFlagSet("pdx lease run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	kind := fs.String("kind", "", "")
	weight := fs.Int("weight", 0, "")
	wait := fs.Duration("wait", leaseDefaultWait, "")
	clientID := fs.String("client-id", "", "")
	pos, err := parseTeamFlags(fs, flagArgs)
	var o acquireOpts
	msg := ""
	switch {
	case err != nil:
		msg = err.Error()
	case len(pos) != 0:
		msg = fmt.Sprintf("unexpected argument %q (the command goes after --)", pos[0])
	case !hasCmd:
		msg = "需要 -- 之後的指令"
	default:
		o, msg = checkLeaseSize(*kind, *weight, *wait, *clientID)
	}
	if msg != "" {
		fmt.Fprintf(stderr, "pdx lease: %s\n%s\n", msg, leaseUsage)
		return ExitUsage
	}

	// Listening starts before the wait: a signal that comes at any point up to
	// the child's start is held here, not lost, and the child gets it at once.
	sigs, stopSigs := leaseSignals()
	defer stopSigs()
	client, ok := leaseClientT("lease", *cfgPath, stderr, leasePollAttempt, clientOpts)
	var out acquireOutcome
	if ok {
		o.holderPID = os.Getpid() // the command is this process's child: the lease's tree is exactly it
		out = leaseAcquire(ctx, client, o, stderr)
	} else {
		out = acquireOutcome{failOpen: "config_unreadable"}
	}
	switch {
	case out.cancelled:
		return ExitCancelled
	case out.failOpen != "":
		fmt.Fprintf(stderr, "pdx lease: daemon 連不上或無法使用（%s），直接執行\n", out.failOpen)
	default:
		r := out.resp
		if r.Overrun {
			fmt.Fprintf(stderr, "pdx lease: 等滿 %s，超量放行（已記錄）\n", waitedText(r.WaitedMS))
		} else if time.Duration(r.WaitedMS)*time.Millisecond >= leaseWaitNotice {
			fmt.Fprintf(stderr, "pdx lease: 等了 %s主機資源（負載 %d/100）\n", waitedText(r.WaitedMS), r.Host.Measured)
		}
		if r.ID != "" {
			// Also after a signal: the deferred release runs on every way out.
			defer leaseReleaseByID(client, r.ID, stderr)
		}
	}
	if ctx.Err() != nil {
		// Interrupted between the grant and the start: the command does not run
		// (the deferred release gives the room back).
		return ExitCancelled
	}
	return runChild(command, sigs, leaseStdinIsTTY(), stderr)
}

// waitedText is a wait in whole seconds, or minutes from one minute on.
func waitedText(ms int64) string {
	s := ms / 1000
	if s >= 60 {
		return fmt.Sprintf("%d 分鐘", s/60)
	}
	return fmt.Sprintf("%d 秒", s)
}

// runChild runs the command with this process's stdio (in its own process
// group unless stdin is a terminal), passes SIGINT, SIGTERM and SIGHUP on to the group, and returns the
// exit code the way a shell would.
func runChild(command []string, sigs <-chan os.Signal, tty bool, stderr io.Writer) int {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if !tty {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "pdx lease: 無法執行 %s：%v\n", command[0], err)
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return 127
		}
		return 126
	}
	// The signal goes to the child's group when it leads one, else to the child.
	target := cmd.Process.Pid
	if !tty {
		target = -target
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigs:
				if s, ok := sig.(syscall.Signal); ok {
					// With a terminal the child shares pdx's foreground group,
					// which the terminal already signals for Ctrl-C and a
					// hangup: forwarding those again would run the child's
					// handlers twice. SIGTERM, which no terminal sends, is
					// still passed on.
					if tty && (s == syscall.SIGINT || s == syscall.SIGHUP) {
						continue
					}
					_ = syscall.Kill(target, s)
				}
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	fmt.Fprintf(stderr, "pdx lease: %v\n", err)
	return 1
}

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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/resources"
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
func runLeaseRun(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	flagArgs, command, hasCmd := splitRunArgs(args)
	fs := flag.NewFlagSet("pdx lease run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	kind := fs.String("kind", "", "")
	weight := fs.Int("weight", 0, "")
	var mf modeFlags
	mf.register(fs)
	cmdText := fs.String("command", "", "")
	clientID := fs.String("client-id", "", "")
	pos, err := parseTeamFlags(fs, normalizeWaitArgs(flagArgs))
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
		var mode string
		if mode, msg = mf.mode(getenv); msg == "" {
			o, msg = checkLeaseSize(*kind, *weight, mf.wait.d, *clientID)
			o.mode = mode
		}
	}
	if msg != "" {
		fmt.Fprintf(stderr, "pdx lease: %s\n%s\n", msg, leaseUsage)
		return ExitUsage
	}
	// What the queue shows: the command as typed (or the mod's own text, for a
	// wrapped background command) and where it runs.
	shown := *cmdText
	if shown == "" {
		shown = strings.Join(command, " ")
	}
	o.command = cutBytes(shown, leaseCommandMax)
	o.cwd, _ = os.Getwd()

	// Listening starts before the wait: a signal that comes at any point up to
	// the child's start is held here, not lost, and the child gets it at once.
	sigs, stopSigs := leaseSignals()
	defer stopSigs()
	client, ok := leaseClientT("lease", *cfgPath, stderr, leasePollAttempt, clientOpts)
	var out acquireOutcome
	held := ""
	if ok {
		o.holderPID = os.Getpid() // the command is this process's child: the lease's tree is exactly it
		out = leaseAcquire(ctx, client, o, stderr)
	} else {
		out = acquireOutcome{failOpen: "config_unreadable"}
	}
	switch {
	case out.cancelled:
		fmt.Fprintln(stderr, "pdx lease: 等待中被中斷，指令沒有執行")
		return ExitCancelled
	case out.askCancelled:
		fmt.Fprintln(stderr, "pdx lease: 已取消，指令沒有執行")
		return ExitQueued
	case out.queued:
		// Not an error and not run: the place is kept and the session is told
		// when it is its turn. Nothing is released or cancelled.
		fmt.Fprintf(stderr, "pdx lease: %s\n", queuedText(out.resp, scriptStepOf(ctx, o, getenv)))
		return ExitQueued
	case out.failOpen != "":
		fmt.Fprintf(stderr, "pdx lease: daemon 連不上或無法使用（%s），直接執行\n", out.failOpen)
	default:
		r := out.resp
		if time.Duration(r.WaitedMS)*time.Millisecond >= leaseWaitNotice {
			fmt.Fprintf(stderr, "pdx lease: 等了 %s主機資源（負載 %d/100）\n", waitedText(r.WaitedMS), r.Host.Measured)
		}
		if r.Warning != "" {
			fmt.Fprintf(stderr, "pdx lease: 警告：%s\n", r.Warning)
		}
		if r.ID != "" {
			// Every way out releases; only a release after the child has
			// exited asks for the final notice.
			held = r.ID
			defer func() {
				if held != "" {
					leaseReleaseByID(client, held, stderr)
				}
			}()
		}
	}
	if ctx.Err() != nil {
		// Interrupted between the grant and the start: the command does not run
		// (the deferred release gives the room back).
		fmt.Fprintln(stderr, "pdx lease: 取得租約後被中斷，指令沒有執行")
		return ExitCancelled
	}
	code := runChild(command, sigs, leaseStdinIsTTY(), stderr)
	if held != "" {
		id := held
		held = ""
		if notice := leaseReleaseWithNotice(client, id, stderr); notice != "" {
			fmt.Fprintf(stderr, "pdx lease: %s\n", notice)
		}
	}
	return code
}

// leaseParentOf is the parent of a process (a seam for tests).
var leaseParentOf = func(ctx context.Context, pid int) (int, error) {
	snap, err := iagent.SnapshotProcesses(ctx)
	if err != nil {
		return 0, err
	}
	return snap.PPID(pid)
}

// agentPIDOf is the pid of the agent that started this shell: Claude Code puts
// its inbox socket, named after its pid, in CLAUDE_CODE_MESSAGING_SOCKET. 0 when
// there is none.
func agentPIDOf(getenv func(string) string) int {
	name := strings.TrimSuffix(filepath.Base(getenv("CLAUDE_CODE_MESSAGING_SOCKET")), ".sock")
	pid, err := strconv.Atoi(name)
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// scriptStepOf says what a queued answer must add when the command is a step
// of a script (T-script). A pdx typed as a command has the agent as its parent
// (the shell was replaced by it) or as its parent's parent (the shell the agent
// opened for the command); anything deeper is a step of a script that shell
// runs. Empty when it is not a script step or the agent cannot be found.
func scriptStepOf(ctx context.Context, o acquireOpts, getenv func(string) string) string {
	agent := agentPIDOf(getenv)
	if agent == 0 {
		return ""
	}
	parent := leaseParentPID()
	if parent == agent {
		return ""
	}
	if up, err := leaseParentOf(ctx, parent); err != nil || up == agent {
		return ""
	}
	if o.kind != "" {
		return "--kind " + o.kind
	}
	return "--weight " + strconv.Itoa(o.weight)
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

// queuedText is what a call that was queued (or replaced) tells whoever runs it
// (lease v2 amendment A, T-queued, T-queued-own, T-script): that the command
// did not run, the place, that the session will be told when it is its turn,
// and that the answer is to wait, not to get around the queue. script is the
// size flag of the step when it runs inside a script ("" otherwise). The mod
// says the same.
func queuedText(r resources.LeaseResponse, script string) string {
	if r.State == resources.StateReplaced {
		return "主機資源忙，這個指令沒有執行：這個呼叫已被同一個 session 較新的呼叫取代（它接手了排隊的位置），請等那一個的結果，不要重跑。"
	}
	head := fmt.Sprintf("主機資源忙，這個指令還沒執行：排在第 %d 位。", r.Position)
	if r.QueuedReason == resources.QueuedOwnRunning {
		head = "主機資源忙，這個指令還沒執行：等你自己的上一個指令跑完。"
	}
	text := head + "輪到你時我會通知你，先做別的事；不要改成背景執行或加 & 繞過排隊。"
	if script != "" {
		text += "這支腳本做到一半被排隊，前面的步驟已經執行；之後請用 `pdx lease run " + script + " -- <整支腳本>` 整支重跑。"
	}
	return text
}

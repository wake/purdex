package fastlane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/wwd/run"
)

// directGrace is how long a command that is being stopped has between SIGTERM and SIGKILL.
const directGrace = 5 * time.Second

// Direct is the Gater of the fast lane now that wwd, not the gate, holds its slot (spec §5.2): the slot was taken when the run was
// registered, so there is nothing to wait for here. It runs the command in its own process group, prints the admitted mark before the
// start like the gate does, stops the group when ctx ends (SIGTERM, then SIGKILL after the grace) and clears what the command left
// running when it ends. It reads gate.Options for the command only: Class and WaitMax mean nothing to it.
type Direct struct {
	// Procs finds the processes that still carry the run's WWD_JOB mark once its group is cleared (a server that left the group with
	// setsid); nil reads the process table of this host (run.DefaultProcSource, none off macOS).
	Procs run.ProcSource
}

var _ Gater = Direct{}

// isTTY says whether stdin is a terminal the command would share (a seam for the tests).
var isTTY = stdinIsTTY

// stdinIsTTY is isTTY as the gate asks it.
func stdinIsTTY() bool {
	_, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	return err == nil
}

func (d Direct) Run(ctx context.Context, o gate.Options) (int, error) {
	if len(o.Argv) == 0 {
		return 127, errors.New("fastlane: no command")
	}
	real := o.Real
	if real == "" {
		p, err := exec.LookPath(o.Argv[0])
		if err != nil {
			return 127, err
		}
		real = p
	}
	stderr := o.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stdout := o.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	env := o.Env
	nonce := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, gate.AdmittedMarkEnv+"="); ok {
			nonce = v
		}
	}
	if nonce != "" { // the command must not learn the nonce
		kept := make([]string, 0, len(env))
		for _, kv := range env {
			if !strings.HasPrefix(kv, gate.AdmittedMarkEnv+"=") {
				kept = append(kept, kv)
			}
		}
		env = kept
		_, _ = io.WriteString(stderr, gate.AdmittedMark(nonce)) // before the start: nothing the command prints can come first
	}
	// Stdin is the caller's own when none is given, like the gate: a runner that reads a prompt must not see an end of file. At a
	// terminal the command stays in the caller's process group (a group of its own would be stopped by SIGTTIN at its first read);
	// what it leaves behind is then found by the job mark below.
	var stdin io.Reader = o.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	cmd := &exec.Cmd{
		Path: real, Args: append([]string{real}, o.Argv[1:]...), Env: env, Dir: o.Dir,
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
		WaitDelay: 300 * time.Millisecond, // output that goes through a pipe the command's leftovers keep open is not waited for
	}
	grouped := !(o.Stdin == nil && isTTY())
	if grouped {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	} else if markOf(env) == "" {
		// no group to clear and no job to name the leftovers by: a mark of its own, so they are still found at the end
		cmd.Env = append(append([]string(nil), env...), run.MarkEnv+"="+newCleanupMark())
		env = cmd.Env
	}
	if err := cmd.Start(); err != nil {
		if nonce != "" {
			_, _ = io.WriteString(stderr, gate.NotStartedMark(nonce))
		}
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return 127, err
		}
		return 126, err
	}
	pid := cmd.Process.Pid
	target := pid // the signals go to the group when the command has one, else to the command alone
	if grouped {
		target = -pid
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-ctx.Done():
		_ = syscall.Kill(target, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(directGrace):
			_ = syscall.Kill(target, syscall.SIGKILL)
			<-done
		}
	}
	if grouped {
		reap(pid)
	}
	// What left the group (a server that called setsid) still carries the job mark: ended like the worker's jobs' leftovers are.
	if id := markOf(env); id != "" {
		src := d.Procs
		if src == nil {
			src = run.DefaultProcSource()
		}
		left, err := run.SweepMarked(src, id, directGrace)
		switch {
		case err != nil: // the table could not be read (or read again): what is left is not known, and it is not said to be cleared
			fmt.Fprintf(stderr, "pdx test: 無法確認這個任務是否留下行程（%v）%s\n", err, pidsNote(left))
		case len(left) > 0:
			fmt.Fprintf(stderr, "pdx test: 這個任務留下 %d 個脫離 process group 的行程，已清掉 (pid %v)\n", len(left), left)
		}
	}
	return exitStatus(cmd), nil
}

func pidsNote(pids []int) string {
	if len(pids) == 0 {
		return ""
	}
	return fmt.Sprintf("，已找到但未確認清掉：pid %v", pids)
}

// newCleanupMark is the mark of a run that has none (it is not recorded) and shares the caller's process group: its leftovers are
// found by this alone. It has the shape of a job id, which is all the facts' reader accepts as a mark (facts.JobFromEnv); no such job
// exists, and the usage tracker forgets an entry that never ends.
func newCleanupMark() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "j_unrecorded_" + hex.EncodeToString(b)
}

// markOf is the job id the command was marked with (WWD_JOB), "" when it has none.
func markOf(env []string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, run.MarkEnv+"="); ok {
			return v
		}
	}
	return ""
}

// reap clears what is left in the command's process group: SIGTERM, a short grace, SIGKILL for the rest.
func reap(pgid int) {
	if !groupAlive(pgid) {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	for deadline := time.Now().Add(directGrace); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !groupAlive(pgid) {
			return
		}
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

func groupAlive(pgid int) bool { return syscall.Kill(-pgid, 0) == nil }

func exitStatus(cmd *exec.Cmd) int {
	if cmd.ProcessState == nil {
		return 1
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return cmd.ProcessState.ExitCode()
}

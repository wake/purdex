package fastlane

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/gate"
)

// directGrace is how long a command that is being stopped has between SIGTERM and SIGKILL.
const directGrace = 5 * time.Second

// Direct is the Gater of the fast lane now that wwd, not the gate, holds its slot (spec §5.2): the slot was taken when the run was
// registered, so there is nothing to wait for here. It runs the command in its own process group, prints the admitted mark before the
// start like the gate does, stops the group when ctx ends (SIGTERM, then SIGKILL after the grace) and clears what the command left
// running when it ends. It reads gate.Options for the command only: Class and WaitMax mean nothing to it.
type Direct struct{}

var _ Gater = Direct{}

func (Direct) Run(ctx context.Context, o gate.Options) (int, error) {
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
	cmd := &exec.Cmd{
		Path: real, Args: append([]string{real}, o.Argv[1:]...), Env: env, Dir: o.Dir,
		Stdin: o.Stdin, Stdout: stdout, Stderr: stderr,
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
		WaitDelay:   300 * time.Millisecond, // output that goes through a pipe the command's leftovers keep open is not waited for
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
	pgid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-ctx.Done():
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(directGrace):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-done
		}
	}
	reap(pgid)
	return exitStatus(cmd), nil
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

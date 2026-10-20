package fastlane

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/wwd/run"
)

func TestDirectRunsTheCommandAndReportsItsExit(t *testing.T) {
	var out, errb bytes.Buffer
	code, err := Direct{}.Run(context.Background(), gate.Options{Argv: []string{"sh", "-c", "echo hi; echo oops >&2; exit 3"}, Stdout: &out, Stderr: &errb})
	if err != nil || code != 3 {
		t.Fatalf("code %d, err %v", code, err)
	}
	if out.String() != "hi\n" || errb.String() != "oops\n" {
		t.Fatalf("out %q err %q", out.String(), errb.String())
	}
}

func TestDirectPrintsTheAdmittedMarkFirstAndHidesTheNonceFromTheCommand(t *testing.T) {
	var out, errb bytes.Buffer
	env := []string{"PATH=/usr/bin:/bin", gate.AdmittedMarkEnv + "=n0nce"}
	code, err := Direct{}.Run(context.Background(), gate.Options{Argv: []string{"sh", "-c", "echo seen=${" + gate.AdmittedMarkEnv + "-none}; echo late >&2"}, Env: env, Stdout: &out, Stderr: &errb})
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
	if !strings.HasPrefix(errb.String(), gate.AdmittedMark("n0nce")+"late") {
		t.Fatalf("stderr %q: the mark must come before the command's output", errb.String())
	}
	if out.String() != "seen=none\n" {
		t.Fatalf("the command saw the nonce: %q", out.String())
	}
}

func TestDirectAMissingCommandIs127(t *testing.T) {
	var errb bytes.Buffer
	code, err := Direct{}.Run(context.Background(), gate.Options{Argv: []string{"definitely-not-a-command-xyz"}, Stderr: &errb})
	if err == nil || code != 127 {
		t.Fatalf("code %d, err %v", code, err)
	}
}

func TestDirectAnUnexecutableCommandIs126WithTheNotStartedMark(t *testing.T) {
	var errb bytes.Buffer
	code, err := Direct{}.Run(context.Background(), gate.Options{Argv: []string{"hosts"}, Real: "/etc/hosts", Env: []string{gate.AdmittedMarkEnv + "=x"}, Stderr: &errb})
	if err == nil || code != 126 {
		t.Fatalf("code %d, err %v", code, err)
	}
	if want := gate.AdmittedMark("x") + gate.NotStartedMark("x"); errb.String() != want {
		t.Fatalf("stderr %q, want %q", errb.String(), want)
	}
}

func TestDirectStopsTheWholeGroupWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	code, err := Direct{}.Run(ctx, gate.Options{Argv: []string{"sh", "-c", "sleep 30 & sleep 30"}, Stdin: strings.NewReader("")})
	if err != nil {
		t.Fatal(err)
	}
	if code != 128+int(syscall.SIGTERM) {
		t.Fatalf("code %d, want 143", code)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("took %s to stop", time.Since(start))
	}
}

func TestDirectGivesTheCommandTheStdinItIsGiven(t *testing.T) {
	var out bytes.Buffer
	code, err := Direct{}.Run(context.Background(), gate.Options{Argv: []string{"cat"}, Stdin: strings.NewReader("typed\n"), Stdout: &out})
	if err != nil || code != 0 || out.String() != "typed\n" {
		t.Fatalf("code %d, err %v, out %q", code, err, out.String())
	}
}

// fakeProcs lists the processes of a job from a fixed set of pids that are still alive.
type fakeProcs struct{ pids []int }

func (f fakeProcs) JobProcesses(_, _ string) ([]int, error) {
	var alive []int
	for _, p := range f.pids {
		if syscall.Kill(p, 0) == nil {
			alive = append(alive, p)
		}
	}
	return alive, nil
}

// A process that left the command's group (a server after setsid) is found by the job mark and ended before Run returns.
func TestDirectEndsWhatLeftTheGroupButKeptTheJobMark(t *testing.T) {
	// the "escaped" process: its own group, started by the test, not by the command
	esc := exec.Command("sleep", "30")
	esc.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := esc.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { _ = esc.Wait(); close(reaped) }()
	t.Cleanup(func() { _ = esc.Process.Kill() })

	var errb bytes.Buffer
	env := []string{"PATH=/usr/bin:/bin", run.MarkEnv + "=j_mark"}
	code, err := Direct{Procs: fakeProcs{pids: []int{esc.Process.Pid}}}.Run(context.Background(),
		gate.Options{Argv: []string{"true"}, Env: env, Stdin: strings.NewReader(""), Stderr: &errb})
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
	select {
	case <-reaped:
	case <-time.After(3 * time.Second):
		t.Fatal("the process that left the group is still running")
	}
	if !strings.Contains(errb.String(), "脫離 process group") {
		t.Fatalf("the caller is not told: %q", errb.String())
	}
}

func TestDirectWithoutAMarkSweepsNothing(t *testing.T) {
	called := false
	code, err := Direct{Procs: probeProcs(func() { called = true })}.Run(context.Background(), gate.Options{Argv: []string{"true"}, Stdin: strings.NewReader("")})
	if err != nil || code != 0 || called {
		t.Fatalf("code %d, err %v, the table was read: %v", code, err, called)
	}
}

type probeProcs func()

func (p probeProcs) JobProcesses(_, _ string) ([]int, error) { p(); return nil, nil }

func TestDirectClearsWhatTheCommandLeftRunning(t *testing.T) {
	var out bytes.Buffer
	// the command prints the pid of a background sleep and exits at once
	code, err := Direct{}.Run(context.Background(), gate.Options{Argv: []string{"sh", "-c", "sleep 30 & echo $!"}, Stdout: &out, Stdin: strings.NewReader("")})
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
	var pid int
	for _, c := range strings.TrimSpace(out.String()) {
		pid = pid*10 + int(c-'0')
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("the background process %d is still running", pid)
	}
}

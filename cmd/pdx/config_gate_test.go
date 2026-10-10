package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
)

// #2562: a daemon is identified to the other writers of its config by the config file's run lock, taken inside the lock a writer
// also takes, so `pdx token generate` cannot miss it nor slip in while it starts.

func gateCfg(t *testing.T) (path, dataDir string) {
	t.Helper()
	dir := t.TempDir()
	dataDir = filepath.Join(dir, "data")
	path = filepath.Join(dir, "config.toml")
	body := fmt.Sprintf("data_dir = %q\ntoken = %q\nport = 7860\n", dataDir, "purdex_0123456789012345678901234567890123456789")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path, dataDir
}

// A daemon found through the config FILE: no pid file in the data_dir the file names (the daemon was started with another
// data_dir, or the file was edited since) — the old check missed it. Mutation gate: do not ask the run lock -> red.
func TestTokenGenerate_RefusesADaemonHoldingTheConfigsRunLock(t *testing.T) {
	path, _ := gateCfg(t) // no pdx.pid anywhere
	_, run, err := openConfigGate(path, t.Logf)
	require.NoError(t, err)
	require.NotNil(t, run)
	defer run.Close()
	before, _ := os.ReadFile(path)

	var out, errw strings.Builder
	require.NotEqual(t, 0, tokenGenerate(path, false, true, &out, &errw))
	after, _ := os.ReadFile(path)
	require.Equal(t, string(before), string(after), "the file was changed")
	require.Empty(t, out.String())
	require.Contains(t, errw.String(), "--force")

	out.Reset()
	errw.Reset()
	require.Equal(t, 0, tokenGenerate(path, true, true, &out, &errw))
	require.Contains(t, errw.String(), "re-pair")
}

// The same file reached by another name is the same daemon.
func TestTokenGenerate_FindsTheDaemonThroughASymlinkToTheConfig(t *testing.T) {
	path, _ := gateCfg(t)
	_, run, err := openConfigGate(path, t.Logf)
	require.NoError(t, err)
	defer run.Close()
	link := filepath.Join(t.TempDir(), "alias.toml")
	require.NoError(t, os.Symlink(path, link))
	var out, errw strings.Builder
	require.NotEqual(t, 0, tokenGenerate(link, false, true, &out, &errw))
}

// A run file nobody holds (the daemon exited) is not a running daemon.
func TestTokenGenerate_ALeftoverRunFileIsNotADaemon(t *testing.T) {
	path, _ := gateCfg(t)
	_, run, err := openConfigGate(path, t.Logf)
	require.NoError(t, err)
	run.Close() // the daemon is gone; "<path>.run" stays
	_, statErr := os.Stat(path + ".run")
	require.NoError(t, statErr)
	var out, errw strings.Builder
	require.Equal(t, 0, tokenGenerate(path, false, true, &out, &errw))
}

// The start-up gate waits for a writer that is mid-write and then reads what it wrote: a daemon cannot start from the old
// token while `token generate` is changing it. Mutation gate: read the config outside the lock -> red.
func TestOpenConfigGate_WaitsForAWriterAndReadsWhatItWrote(t *testing.T) {
	path, _ := gateCfg(t)
	inside, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = config.WithLock(path, func(locked string) error {
			close(inside)
			<-release
			return config.SetTokenHeld(locked, "pdx_newnewnewnewnewnewnewnewnewnewnewnew")
		})
	}()
	<-inside
	got := make(chan config.Config, 1)
	go func() {
		cfg, run, err := openConfigGate(path, t.Logf)
		if err == nil && run != nil {
			defer run.Close()
		}
		got <- cfg
	}()
	select {
	case <-got:
		t.Fatal("the daemon read the config while a writer held the lock")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	cfg := <-got
	require.Equal(t, "pdx_newnewnewnewnewnewnewnewnewnewnewnew", cfg.Token)
}

// If the gate cannot be entered the daemon still starts, from the file, without a run lock.
func TestOpenConfigGate_StartsWithoutTheGateWhenTheLockStaysHeld(t *testing.T) {
	path, _ := gateCfg(t)
	old := config.LockWait
	config.LockWait = 100 * time.Millisecond
	defer func() { config.LockWait = old }()
	done, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = config.WithLock(path, func(string) error { close(done); <-release; return nil })
	}()
	<-done
	defer close(release)
	var logs []string
	cfg, run, err := openConfigGate(path, func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	require.NoError(t, err)
	require.Nil(t, run)
	require.Equal(t, 7860, cfg.Port)
	require.NotEmpty(t, logs)
}

// ---- the hand-off across a restart ----

// The helper "new image": adopts the fd named in PDX_RUNLOCK_FD, says so, and holds it until its stdin closes.
func TestHelperRunLockChild(t *testing.T) {
	if os.Getenv("PDX_TEST_RUNLOCK_CHILD") != "1" {
		t.Skip("helper process")
	}
	adopted := false
	_, run, err := openConfigGate(os.Getenv("PDX_TEST_CFG"), func(f string, a ...any) {
		adopted = adopted || strings.Contains(fmt.Sprintf(f, a...), "adopted from the previous image")
	})
	if err != nil || run == nil {
		fmt.Println("no run lock", err)
		os.Exit(3)
	}
	if adopted {
		fmt.Println("adopted")
	} else {
		fmt.Println("took a new one")
	}
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	os.Exit(0)
}

// The restart hands the run lock across the exec: the lock is held without a gap — by the old image until the new one has it, and by
// the new one afterwards — and it is gone when the new image exits. Mutation gate: do not hand over the run lock -> red.
func TestReexec_HandsTheRunLockToTheNewImage(t *testing.T) {
	path, _ := gateCfg(t)
	cfg, run, err := openConfigGate(path, t.Logf)
	require.NoError(t, err)
	require.NotNil(t, run)
	_ = cfg
	locked, _ := filepath.EvalSymlinks(path)

	// Nothing may ever see the lock free while the image changes: probe it all the way through.
	stop := make(chan struct{})
	gaps := make(chan int, 1)
	go func() {
		n := 0
		for {
			select {
			case <-stop:
				gaps <- n
				return
			default:
			}
			if held, err := config.RunLockHeld(locked); err == nil && !held {
				n++
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// What reexec does, step by step: the env entry names the fd, close-on-exec is cleared right before the exec, the new image
	// starts. (reexec itself holds syscall.ForkLock around execFn, which os/exec needs, so the child is started here; the plan's
	// own clearing and restoring are covered below.)
	env := append(os.Environ(), runLockEnvEntry(run), "PDX_TEST_RUNLOCK_CHILD=1", "PDX_TEST_CFG="+path)
	require.NoError(t, clearCloseOnExec(run))
	child := exec.Command(os.Args[0], "-test.run=TestHelperRunLockChild")
	child.Env = env
	stdin, err := child.StdinPipe()
	require.NoError(t, err)
	out, err := child.StdoutPipe()
	require.NoError(t, err)
	stdout := bufio.NewReader(out)
	require.NoError(t, child.Start())
	syscall.CloseOnExec(int(run.Fd()))

	line, err := stdout.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "adopted", strings.TrimSpace(line))
	run.Close() // the old image is gone
	held, err := config.RunLockHeld(locked)
	require.NoError(t, err)
	require.True(t, held, "the new image does not hold the run lock the old one handed over")

	close(stop)
	require.Zero(t, <-gaps, "the run lock was free for a moment during the hand-over")

	_ = stdin.Close()
	require.NoError(t, child.Wait())
	held, err = config.RunLockHeld(locked)
	require.NoError(t, err)
	require.False(t, held, "the run lock outlived the daemon")
}

func TestReexec_RunLockIsCloseOnExecAgainWhenTheExecFails(t *testing.T) {
	path, _ := gateCfg(t)
	_, run, err := openConfigGate(path, t.Logf)
	require.NoError(t, err)
	defer run.Close()
	plan := &reexecPlan{path: "/x", runLock: run}
	var during uintptr
	reexec(plan, func(string, []string, []string) error {
		during = getFD(t, int(run.Fd()))
		return errors.New("x")
	}, func(string, ...any) {}, func(int) {})
	require.Zero(t, during, "the fd would not survive exec")
	require.NotZero(t, getFD(t, int(run.Fd()))&1, "the fd stayed inheritable after a failed exec")
}

func TestCaptureReexecPlan_DropsRunLockFD(t *testing.T) {
	p, err := captureReexecPlan(func() (string, error) { return "/opt/pdx", nil }, []string{"pdx"}, []string{"A=1", "PDX_RUNLOCK_FD=9", "B=2"})
	require.NoError(t, err)
	require.Equal(t, []string{"A=1", "B=2"}, p.env)
}

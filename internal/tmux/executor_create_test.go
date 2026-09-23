package tmux_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

// The create path's tmux calls take a context (#1293): has-session is a read
// bounded by the caller, new-session a mutation bounded by its own cap. Both
// kill the hung client when the context ends and report an error wrapping
// ctx.Err().

// installAnsweringTmux puts a fake tmux on PATH that exits with code.
func installAnsweringTmux(t *testing.T, code int) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRealExecutorHasSessionContext_DeadlineKillsHungRead(t *testing.T) {
	_, pidFile := installSleepingTmux(t)
	ctx, cancel := context.WithTimeout(context.Background(), readDeadline)
	defer cancel()
	start := time.Now()
	exists, err := (&tmux.RealExecutor{}).HasSessionContext(ctx, "dev")
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want error wrapping context.DeadlineExceeded, got %v", err)
	}
	if exists {
		t.Fatal("a read that timed out reported the session as existing")
	}
	if elapsed > readBound {
		t.Fatalf("hung has-session returned after %v, want within %v", elapsed, readBound)
	}
	assertChildReaped(t, pidFile)
}

func TestRealExecutorHasSessionContext_Answers(t *testing.T) {
	installAnsweringTmux(t, 0)
	exists, err := (&tmux.RealExecutor{}).HasSessionContext(context.Background(), "dev")
	if err != nil || !exists {
		t.Fatalf("exit 0: got (%v, %v), want (true, nil)", exists, err)
	}
	installAnsweringTmux(t, 1)
	exists, err = (&tmux.RealExecutor{}).HasSessionContext(context.Background(), "dev")
	if err != nil || exists {
		t.Fatalf("exit 1: got (%v, %v), want (false, nil)", exists, err)
	}
}

func TestRealExecutorNewSessionContext_DeadlineKillsHungCreate(t *testing.T) {
	_, pidFile := installSleepingTmux(t)
	ctx, cancel := context.WithTimeout(context.Background(), readDeadline)
	defer cancel()
	start := time.Now()
	err := (&tmux.RealExecutor{}).NewSessionContext(ctx, "dev", "/tmp")
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want error wrapping context.DeadlineExceeded, got %v", err)
	}
	if elapsed > readBound {
		t.Fatalf("hung new-session returned after %v, want within %v", elapsed, readBound)
	}
	assertChildReaped(t, pidFile)
}

func TestRealExecutorNewSessionContext_TmuxFailureIsNotACtxError(t *testing.T) {
	installAnsweringTmux(t, 1)
	err := (&tmux.RealExecutor{}).NewSessionContext(context.Background(), "dev", "/tmp")
	if err == nil {
		t.Fatal("want the failing new-session reported")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("a tmux failure on a live context was reported as a ctx error: %v", err)
	}
	installAnsweringTmux(t, 0)
	if err := (&tmux.RealExecutor{}).NewSessionContext(context.Background(), "dev", "/tmp"); err != nil {
		t.Fatalf("exit 0: %v", err)
	}
}

// The fake's create hook sees HasSessionContext / NewSessionContext with the
// caller's context, so a test can model a hung has-session or new-session.
func TestFakeExecutor_CreateHookHonoursContext(t *testing.T) {
	f := tmux.NewFakeExecutor()
	release := make(chan struct{})
	var ops []tmux.ReadOp
	block := tmux.BlockReadsUntil(release, nil)
	f.SetCreateHook(func(ctx context.Context, op tmux.ReadOp, target string) error {
		ops = append(ops, op)
		return block(ctx, op, target)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := f.HasSessionContext(ctx, "dev"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HasSessionContext: want DeadlineExceeded, got %v", err)
	}
	if err := f.NewSessionContext(ctx, "dev", "/tmp"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("NewSessionContext: want DeadlineExceeded, got %v", err)
	}
	if f.HasSession("dev") {
		t.Fatal("a new-session that failed on its context created the session")
	}
	if len(ops) != 1 || ops[0] != tmux.OpHasSession {
		// the second call never reached the hook: its ctx had already ended
		t.Fatalf("hook ops = %v, want [has-session]", ops)
	}

	close(release)
	if err := f.NewSessionContext(context.Background(), "dev", "/tmp"); err != nil {
		t.Fatalf("after release: %v", err)
	}
	if ok, err := f.HasSessionContext(context.Background(), "dev"); err != nil || !ok {
		t.Fatalf("after release: (%v, %v)", ok, err)
	}
	if ops[len(ops)-2] != tmux.OpNewSession {
		t.Fatalf("hook ops = %v, want new-session before the last has-session", ops)
	}
}

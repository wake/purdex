package dev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunBuild_Success(t *testing.T) {
	m := &DevModule{
		repoRoot: t.TempDir(),
		buildCmd: func() error { return nil },
		building: true,
	}
	// Create out/ dir so .build-info.json removal doesn't fail
	os.MkdirAll(filepath.Join(m.repoRoot, "out"), 0755)

	m.runBuild()

	if m.building {
		t.Error("building: want false after successful build")
	}
	if m.buildError != "" {
		t.Errorf("buildError: want empty, got %q", m.buildError)
	}
}

func TestRunBuild_Failure(t *testing.T) {
	m := &DevModule{
		repoRoot: t.TempDir(),
		buildCmd: func() error { return fmt.Errorf("build timed out after 5 minutes") },
		building: true,
	}
	os.MkdirAll(filepath.Join(m.repoRoot, "out"), 0755)

	m.runBuild()

	if m.building {
		t.Error("building: want false after failed build")
	}
	if m.buildError == "" {
		t.Error("buildError: want non-empty after failed build")
	}
	if m.buildError != "build timed out after 5 minutes" {
		t.Errorf("buildError: want timeout message, got %q", m.buildError)
	}
}

func TestStop_CancelsBuild(t *testing.T) {
	buildStarted := make(chan struct{})
	m := &DevModule{
		repoRoot: t.TempDir(),
		building: true,
	}
	os.MkdirAll(filepath.Join(m.repoRoot, "out"), 0755)

	// Init lifecycle (creates stopCtx)
	m.Init(nil)

	// Mock buildCmd that blocks until stopCtx is cancelled
	m.buildCmd = func() error {
		close(buildStarted)
		<-m.stopCtx.Done()
		return m.stopCtx.Err()
	}

	// Run build in goroutine
	done := make(chan struct{})
	go func() {
		m.runBuild()
		close(done)
	}()

	// Wait for build to start
	<-buildStarted

	// Stop should cancel the build
	m.Stop(context.Background())

	// Build goroutine should finish quickly
	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("build goroutine did not finish after Stop()")
	}

	if m.building {
		t.Error("building: want false after Stop()")
	}
}

func TestRunBuild_ClearsErrorOnSuccess(t *testing.T) {
	m := &DevModule{
		repoRoot:   t.TempDir(),
		buildCmd:   func() error { return nil },
		building:   true,
		buildError: "previous error",
	}
	os.MkdirAll(filepath.Join(m.repoRoot, "out"), 0755)

	m.runBuild()

	if m.buildError != "" {
		t.Errorf("buildError: want empty after success, got %q", m.buildError)
	}
}

func TestDefaultBuild_RunsInstallGenerateAndBuild(t *testing.T) {
	repoRoot := t.TempDir()
	m := &DevModule{
		repoRoot: repoRoot,
		stopCtx:  context.Background(),
	}

	var calls [][]string
	m.execCmd = func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
		if dir != repoRoot {
			t.Fatalf("dir: want %s, got %s", repoRoot, dir)
		}
		calls = append(calls, append([]string{name}, args...))
		return nil, nil
	}

	if err := m.defaultBuild(); err != nil {
		t.Fatalf("defaultBuild: %v", err)
	}

	want := [][]string{
		{"pnpm", "install", "--frozen-lockfile"},
		{"node", "spa/scripts/generate-icon-data.mjs"},
		{"pnpm", "exec", "electron-vite", "build"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands:\nwant %#v\ngot  %#v", want, calls)
	}
}

func TestDefaultBuild_ReturnsStepOutputOnFailure(t *testing.T) {
	m := &DevModule{
		repoRoot: t.TempDir(),
		stopCtx:  context.Background(),
	}

	m.execCmd = func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
		if name == "pnpm" && len(args) > 0 && args[0] == "install" {
			return []byte("ERR_PNPM_OUTDATED_LOCKFILE"), fmt.Errorf("exit status 1")
		}
		return nil, nil
	}

	err := m.defaultBuild()
	if err == nil {
		t.Fatal("defaultBuild: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "dependency install failed") {
		t.Fatalf("error: want dependency install context, got %q", msg)
	}
	if !strings.Contains(msg, "ERR_PNPM_OUTDATED_LOCKFILE") {
		t.Fatalf("error: want command output, got %q", msg)
	}
}

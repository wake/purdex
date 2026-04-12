package dev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
		buildCmd:    func() error { return nil },
		building:   true,
		buildError: "previous error",
	}
	os.MkdirAll(filepath.Join(m.repoRoot, "out"), 0755)

	m.runBuild()

	if m.buildError != "" {
		t.Errorf("buildError: want empty after success, got %q", m.buildError)
	}
}

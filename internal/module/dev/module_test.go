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
		buildCmd: func(*BuildSession) error { return nil },
		building: true,
	}
	os.MkdirAll(filepath.Join(m.repoRoot, "out"), 0755)

	m.runBuild()

	if m.building {
		t.Error("building: want false after successful build")
	}
	if m.buildError != "" {
		t.Errorf("buildError: want empty, got %q", m.buildError)
	}
	if m.buildSession == nil {
		t.Fatal("buildSession: want non-nil after build, got nil")
	}
	// Session should be closed; final event is "done"
	if n := len(m.buildSession.events); n == 0 {
		t.Fatal("session events: want >=1, got 0")
	}
	last := m.buildSession.events[len(m.buildSession.events)-1]
	if last.Type != BuildEventDone {
		t.Errorf("last event: want done, got %+v", last)
	}
}

func TestRunBuild_Failure(t *testing.T) {
	m := &DevModule{
		repoRoot: t.TempDir(),
		buildCmd: func(*BuildSession) error { return fmt.Errorf("build timed out after 5 minutes") },
		building: true,
	}
	os.MkdirAll(filepath.Join(m.repoRoot, "out"), 0755)

	m.runBuild()

	if m.building {
		t.Error("building: want false after failed build")
	}
	if m.buildError != "build timed out after 5 minutes" {
		t.Errorf("buildError: want timeout message, got %q", m.buildError)
	}
	if m.buildSession == nil {
		t.Fatal("buildSession: want non-nil after build, got nil")
	}
	last := m.buildSession.events[len(m.buildSession.events)-1]
	if last.Type != BuildEventError {
		t.Errorf("last event: want error, got %+v", last)
	}
	if last.Error != "build timed out after 5 minutes" {
		t.Errorf("last event error text: want timeout, got %q", last.Error)
	}
}

func TestStop_CancelsBuild(t *testing.T) {
	buildStarted := make(chan struct{})
	m := &DevModule{
		repoRoot: t.TempDir(),
		building: true,
	}
	os.MkdirAll(filepath.Join(m.repoRoot, "out"), 0755)

	m.Init(nil)

	m.buildCmd = func(*BuildSession) error {
		close(buildStarted)
		<-m.stopCtx.Done()
		return m.stopCtx.Err()
	}

	done := make(chan struct{})
	go func() {
		m.runBuild()
		close(done)
	}()

	<-buildStarted
	m.Stop(context.Background())

	select {
	case <-done:
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
		buildCmd:   func(*BuildSession) error { return nil },
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
	m.runStep = func(ctx context.Context, session *BuildSession, phase, dir, name string, args ...string) error {
		if dir != repoRoot {
			t.Fatalf("dir: want %s, got %s", repoRoot, dir)
		}
		calls = append(calls, append([]string{phase, name}, args...))
		return nil
	}

	session := newBuildSession()
	if err := m.defaultBuild(session); err != nil {
		t.Fatalf("defaultBuild: %v", err)
	}

	want := [][]string{
		{"dependency install", "pnpm", "install", "--frozen-lockfile"},
		{"icon generation", "node", "spa/scripts/generate-icon-data.mjs"},
		{"renderer/main build", "pnpm", "exec", "electron-vite", "build"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands:\nwant %#v\ngot  %#v", want, calls)
	}
}

func TestDefaultBuild_WrapsStepErrorWithLabel(t *testing.T) {
	m := &DevModule{
		repoRoot: t.TempDir(),
		stopCtx:  context.Background(),
	}

	m.runStep = func(ctx context.Context, session *BuildSession, phase, dir, name string, args ...string) error {
		if name == "pnpm" && len(args) > 0 && args[0] == "install" {
			return fmt.Errorf("exit status 1")
		}
		return nil
	}

	session := newBuildSession()
	err := m.defaultBuild(session)
	if err == nil {
		t.Fatal("defaultBuild: want error, got nil")
	}
	if !strings.Contains(err.Error(), "dependency install failed") {
		t.Fatalf("error: want dependency install context, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("error: want wrapped exit status, got %q", err.Error())
	}
}

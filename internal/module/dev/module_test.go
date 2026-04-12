package dev

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
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

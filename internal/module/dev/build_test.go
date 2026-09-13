package dev

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeThrowawayModule creates a module that builds at ./cmd/pdx and
// returns its root. Shared by build and download tests.
func writeThrowawayModule(t *testing.T, mainSrc string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "pdx", "main.go"), []byte(mainSrc), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuildBinary_HostTarget(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	m := &DevModule{repoRoot: dir}
	out := filepath.Join(dir, "out-host")
	var lines []string
	err := m.buildBinary(context.Background(), buildTarget{}, "abc", "1.2.3", out, func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatalf("buildBinary: %v\n%s", err, strings.Join(lines, "\n"))
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output missing: %v", err)
	}
}

func TestBuildBinary_CrossTargetProducesForeignArch(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	m := &DevModule{repoRoot: dir}
	out := filepath.Join(dir, "out-cross")
	err := m.buildBinary(context.Background(), buildTarget{GOOS: "linux", GOARCH: "amd64"}, "abc", "1.2.3", out, func(string) {})
	if err != nil {
		t.Fatalf("cross build: %v", err)
	}
	head := make([]byte, 20)
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Read(head); err != nil {
		t.Fatal(err)
	}
	// ELF magic + e_machine 0x3E (x86-64) at offset 18 (little endian).
	if string(head[:4]) != "\x7fELF" || head[18] != 0x3e || head[19] != 0x00 {
		t.Fatalf("expected linux/amd64 ELF, got header % x", head)
	}
}

// Both pipes are drained by two goroutines; the sink contract says calls
// are serialised. A real compile only ever writes stderr, so this test puts
// a fake `go` on PATH that streams to stdout AND stderr concurrently and
// appends to an unlocked slice from the sink. Under -race, removing the
// mutex in buildBinary must make this test fail.
func TestBuildBinary_SinkIsSerialised(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fakebin")
	if err := os.MkdirAll(fakeBin, 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"( i=0; while [ $i -lt 300 ]; do echo out$i; i=$((i+1)); done ) &\n" +
		"( i=0; while [ $i -lt 300 ]; do echo err$i 1>&2; i=$((i+1)); done ) &\n" +
		"wait\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "go"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := &DevModule{repoRoot: dir}
	var lines []string // plain slice: safe only if sink is serialised
	if err := m.buildBinary(context.Background(), buildTarget{}, "abc", "1.2.3", filepath.Join(dir, "out"), func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("fake go: %v", err)
	}
	if len(lines) != 600 {
		t.Fatalf("got %d lines, want 600", len(lines))
	}
}

// On ctx cancel, exec.CommandContext's default Cancel kills only the direct
// `go` child; a `compile`/`link` grandchild that inherited the stdout/stderr
// pipes keeps them open until it exits on its own, so the scanner goroutines
// in buildBinary (and therefore daemonRebuildMu) would block until then.
// This fakes that shape: `go` backgrounds a long-lived `sleep 60` that
// inherits stdout, then exits (via `wait`, itself killed by cancellation);
// the orphaned sleep keeps the pipe's write end open.
//
// Cancellation is triggered only after the fake script has confirmed (via a
// "started" line through the sink) that the orphan is already forked, rather
// than after a fixed sleep: a fixed ~200ms delay was observed to race with
// process/script startup on a loaded machine (first exec in a fresh test
// binary can itself take >100ms), letting the cancellation land before the
// orphan existed and producing a false pass for the wrong reason.
func TestBuildBinary_CancelDoesNotHangOnOrphanedChild(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fakebin")
	if err := os.MkdirAll(fakeBin, 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"sleep 60 &\n" +
		"echo started\n" +
		"wait\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "go"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := &DevModule{repoRoot: dir}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	var once sync.Once
	sink := func(line string) {
		if line == "started" {
			once.Do(func() { close(started) })
		}
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.buildBinary(ctx, buildTarget{}, "abc", "1.2.3", filepath.Join(dir, "out"), sink)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("fake go never reported the orphaned child as started")
	}
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected an error from a canceled build with an orphaned child")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("buildBinary did not return within 15s of cancellation — orphaned child held the pipe open (WaitDelay missing?)")
	}
}

func TestBuildBinary_CompileErrorIsReported(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){ undefined() }\n")
	m := &DevModule{repoRoot: dir}
	var lines []string
	err := m.buildBinary(context.Background(), buildTarget{}, "abc", "1.2.3", filepath.Join(dir, "out"), func(l string) { lines = append(lines, l) })
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "undefined") {
		t.Fatalf("compiler output not streamed to sink: %v", lines)
	}
}

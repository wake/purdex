package dev

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

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

// Killing only the direct `go` child is not enough: compile/link grandchildren
// keep burning CPU after the client has gone away. buildBinary must put the
// build in its own process group and kill the whole group on cancel. The fake
// `go` records its orphan's pid via $PDX_TEST_CHILD_PID_FILE so the test can
// assert the orphan is really dead (kill -0 → ESRCH) once buildBinary returns.
func TestBuildBinary_CancelKillsOrphanedChild(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fakebin")
	if err := os.MkdirAll(fakeBin, 0755); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "child.pid")
	script := "#!/bin/sh\n" +
		"sleep 60 &\n" +
		"echo $! > \"$PDX_TEST_CHILD_PID_FILE\"\n" +
		"echo started\n" +
		"wait\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "go"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PDX_TEST_CHILD_PID_FILE", pidFile)

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
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child pid file: %v", err)
	}
	childPid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || childPid <= 0 {
		t.Fatalf("bad child pid %q: %v", data, err)
	}
	// Whatever happens, never leave a stray sleep behind.
	defer syscall.Kill(childPid, syscall.SIGKILL)

	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected an error from a canceled build")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("buildBinary did not return within 15s of cancellation")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(childPid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphaned child %d still alive after buildBinary returned (kill -0 err=%v); process group not killed on cancel", childPid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A writer that never emits a newline (a runaway tool, a binary blob on
// stderr) must not let lineWriter grow without bound: the pending partial
// line is capped at maxPendingLine and flushed, truncated, to the sink.
func TestLineWriter_BoundsPendingLine(t *testing.T) {
	var got []string
	w := &lineWriter{sink: func(l string) { got = append(got, l) }}
	chunk := bytes.Repeat([]byte{'x'}, 64<<10) // 64 KiB per Write
	const total = 3 << 20                      // 3 MiB, no newline anywhere
	for written := 0; written < total; written += len(chunk) {
		n, err := w.Write(chunk)
		if err != nil || n != len(chunk) {
			t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, len(chunk))
		}
		if len(w.buf) > maxPendingLine {
			t.Fatalf("pending buffer grew to %d bytes, cap is %d", len(w.buf), maxPendingLine)
		}
	}
	if len(got) < 2 {
		t.Fatalf("sink got %d chunks, want at least 2 for 3 MiB of unterminated input", len(got))
	}
	flushed := 0
	for i, l := range got {
		if !strings.HasSuffix(l, truncatedLineSuffix) {
			t.Fatalf("chunk %d lacks the truncation marker: %q...", i, l[:20])
		}
		if len(l) > maxPendingLine+len(truncatedLineSuffix) {
			t.Fatalf("chunk %d is %d bytes, exceeds cap", i, len(l))
		}
		flushed += len(l) - len(truncatedLineSuffix)
	}
	// Nothing is lost: every byte is either flushed or still pending.
	if flushed+len(w.buf) != total {
		t.Fatalf("flushed %d + pending %d != %d written", flushed, len(w.buf), total)
	}
	// The scanner must still work normally afterwards.
	got = nil
	w.Write([]byte("tail\nnext\n"))
	if len(got) != 2 || !strings.HasSuffix(got[0], "tail") || got[1] != "next" {
		t.Fatalf("normal lines after truncation: %q", got)
	}
}

// Lines fed one byte at a time must still come out intact and exactly once;
// with a tracked scan offset this is also O(n) rather than O(n^2).
func TestLineWriter_ScanOffsetDoesNotRescan(t *testing.T) {
	var got []string
	w := &lineWriter{sink: func(l string) { got = append(got, l) }}
	for i := 0; i < 1000; i++ {
		w.Write([]byte("a"))
	}
	w.Write([]byte("\r\nb\n"))
	if len(got) != 2 || got[0] != strings.Repeat("a", 1000) || got[1] != "b" {
		t.Fatalf("got %d lines: %v", len(got), got)
	}
}

func TestRebuildLdflags_InjectBuildinfoHashAndVersion(t *testing.T) {
	got := rebuildLdflags("abc1234", "1.2.3")
	for _, want := range []string{
		"-X github.com/wake/purdex/internal/buildinfo.Hash=abc1234",
		"-X github.com/wake/purdex/internal/buildinfo.Version=1.2.3",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ldflags %q missing %q", got, want)
		}
	}
}

// An empty hash or version must never be baked in as "": buildinfo.Hash=""
// would make /api/dev/daemon/check report Available forever (latest hash is
// never empty, so it never equals "").
func TestRebuildLdflags_EmptyIdentityBecomesUnknown(t *testing.T) {
	got := rebuildLdflags("", "")
	for _, want := range []string{
		"-X github.com/wake/purdex/internal/buildinfo.Hash=unknown",
		"-X github.com/wake/purdex/internal/buildinfo.Version=unknown",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ldflags %q missing %q", got, want)
		}
	}
}

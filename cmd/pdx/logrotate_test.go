package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// #2163: size rotation of the daemon's log. The tests stand a spare descriptor in for stdout/stderr: dup2 repoints it
// exactly as it repoints fd 1 and 2.

// logFixture opens dir/pdx.log O_APPEND and a spare descriptor x on it (the "stdout"). The caller writes through x.
func logFixture(t *testing.T, maxBytes int64) (r *logRotator, x int, dir string) {
	t.Helper()
	dir = t.TempDir()
	path := filepath.Join(dir, "pdx.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	x, err = syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = syscall.Close(x) })
	r = newLogRotator(path, nil)
	r.w.fd = x
	r.logf = func(format string, a ...any) { _, _ = r.w.Write([]byte(fmt.Sprintf(format, a...) + "\n")) } // what log.Printf does
	r.maxBytes, r.keep, r.fds = maxBytes, 5, []int{x}
	writerOf[x] = r.w
	return r, x, dir
}

// writeFD writes as the daemon does: through the rotation-aware writer of the rotator that owns x.
var writerOf = map[int]*rotWriter{}

func writeFD(t *testing.T, x int, s string) {
	t.Helper()
	if _, err := writerOf[x].Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
}

func readGz(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLogRotator_BelowTheLimitNothingHappens(t *testing.T) {
	r, x, dir := logFixture(t, 1000)
	writeFD(t, x, "short\n")
	if r.check() {
		t.Fatal("rotated below the limit")
	}
	if _, err := os.Stat(filepath.Join(dir, "pdx.log.1.gz")); err == nil {
		t.Fatal("a generation appeared")
	}
}

// The rotation: the old content ends up compressed whole, the descriptor points at the NEW file (not the renamed one),
// the rotation line is the new file's first, and later writes land in the new file. Mutation gate: no dup2 → the
// later write lands in the compressed generation's source → red.
func TestLogRotator_RotatesAndRepointsTheDescriptor(t *testing.T) {
	r, x, dir := logFixture(t, 20)
	writeFD(t, x, "old line 1\nold line 2\nold line 3\n")
	if !r.check() {
		t.Fatal("did not rotate at the limit")
	}
	r.compressWG.Wait()
	writeFD(t, x, "after rotation\n")
	if got := readGz(t, filepath.Join(dir, "pdx.log.1.gz")); got != "old line 1\nold line 2\nold line 3\n" {
		t.Fatalf("generation 1 = %q, want the old content whole", got)
	}
	cur, err := os.ReadFile(filepath.Join(dir, "pdx.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(cur), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "log rotation: pdx.log reached") || lines[1] != "after rotation" {
		t.Fatalf("new pdx.log = %q, want the rotation line, then the later write", cur)
	}
	if _, err := os.Stat(filepath.Join(dir, "pdx.log.rotating")); err == nil {
		t.Error("the temporary file was left behind")
	}
	// dup2 gives the descriptor a clean close-on-exec flag: it survives the in-place exec restart like any stdio
	if fl, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(x), syscall.F_GETFD, 0); e != 0 || fl&syscall.FD_CLOEXEC != 0 {
		t.Errorf("fd flags = %d (%v), want no FD_CLOEXEC", fl, e)
	}
}

// No line is lost or doubled while a writer runs through the rotation. Mutation gate: close the old fd before dup2 →
// lines vanish → red.
func TestLogRotator_LosesNoLineWhileAWriterRuns(t *testing.T) {
	r, _, dir := logFixture(t, 1)
	const n = 20000
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := r.w.Write([]byte(fmt.Sprintf("line %06d\n", i))); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	rotations := 0
	for rotations < 3 {
		fi, _ := os.Stat(r.path)
		if fi != nil && fi.Size() > 20000 && r.check() {
			rotations++
			r.compressWG.Wait()
		}
		select {
		default:
		}
		if _, err := os.Stat(r.path); err != nil {
			t.Fatal(err)
		}
		if done := writerDone(&wg); done {
			break
		}
	}
	wg.Wait()
	r.compressWG.Wait()
	var all bytes.Buffer
	for i := r.keep; i >= 1; i-- {
		if b, err := os.ReadFile(r.gz(i)); err == nil {
			zr, err := gzip.NewReader(bytes.NewReader(b))
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(zr)
			all.Write(data)
		}
	}
	cur, _ := os.ReadFile(filepath.Join(dir, "pdx.log"))
	all.Write(cur)
	seen := map[string]int{}
	for _, l := range strings.Split(all.String(), "\n") {
		if strings.HasPrefix(l, "line ") {
			seen[l]++
		}
	}
	if len(seen) != n {
		t.Fatalf("%d distinct lines survived (%d rotations), want %d", len(seen), rotations, n)
	}
	for l, c := range seen {
		if c != 1 {
			t.Fatalf("%q appears %d times", l, c)
		}
	}
}

func writerDone(wg *sync.WaitGroup) bool {
	ch := make(chan struct{})
	go func() { wg.Wait(); close(ch) }()
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// keep N: the oldest falls off, the newest is .1. Mutation gate: no shift → older generations are overwritten → red.
func TestLogRotator_KeepsOnlyTheLastGenerations(t *testing.T) {
	r, x, dir := logFixture(t, 5)
	for i := 1; i <= 8; i++ {
		writeFD(t, x, fmt.Sprintf("generation-%d-padding\n", i))
		if !r.check() {
			t.Fatalf("rotation %d did not happen", i)
		}
		r.compressWG.Wait()
	}
	for n := 1; n <= 5; n++ {
		want := fmt.Sprintf("generation-%d-padding\n", 9-n)
		// each generation also holds the rotation line the previous rotation wrote at its top
		if got := readGz(t, filepath.Join(dir, fmt.Sprintf("pdx.log.%d.gz", n))); !strings.Contains(got, want) {
			t.Errorf(".%d.gz = %q, want it to hold %q", n, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pdx.log.6.gz")); err == nil {
		t.Error("a sixth generation was kept")
	}
}

// Only the daemon's own log: a descriptor on some other file (a terminal, another launcher) is never rotated away.
func TestLogRotator_LeavesAForeignStdoutAlone(t *testing.T) {
	r, _, dir := logFixture(t, 1)
	other, err := os.CreateTemp(t.TempDir(), "other")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	r.fds = []int{int(other.Fd())}
	if err := os.WriteFile(filepath.Join(dir, "pdx.log"), bytes.Repeat([]byte("x"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if r.check() {
		t.Fatal("rotated a log that is not the descriptor's file")
	}
}

// A crash between the rename and the compression leaves pdx.log.rotating: the next start compresses it.
func TestLogRotator_StartFinishesAnInterruptedCompression(t *testing.T) {
	r, _, dir := logFixture(t, 1<<30)
	if err := os.WriteFile(filepath.Join(dir, "pdx.log.rotating"), []byte("left behind\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := r.start()
	defer stop()
	r.compressWG.Wait()
	if got := readGz(t, filepath.Join(dir, "pdx.log.1.gz")); got != "left behind\n" {
		t.Fatalf("generation 1 = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "pdx.log.rotating")); err == nil {
		t.Error("the temporary file is still there")
	}
}

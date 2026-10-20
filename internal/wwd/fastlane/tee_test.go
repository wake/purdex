package fastlane

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type brokenWriter struct{ n int }

func (b *brokenWriter) Write(p []byte) (int, error) { b.n++; return 0, errors.New("pipe closed") }

func TestTheCommandsOutputGoesToTheTeeAsWellAsTheLog(t *testing.T) {
	g := &fakeGate{run: func(_ context.Context, o Options) int {
		o.Stdout.Write([]byte("to stdout\n"))
		o.Stderr.Write([]byte("to stderr\n"))
		return 0
	}}
	var so, se bytes.Buffer
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: g, TeeStdout: &so, TeeStderr: &se})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if so.String() != "to stdout\n" || se.String() != "to stderr\n" {
		t.Fatalf("tee got stdout %q stderr %q (the gate's mark must not reach it)", so.String(), se.String())
	}
	log, _ := os.ReadFile(res.LogPath)
	if !strings.Contains(string(log), "to stdout") || !strings.Contains(string(log), "to stderr") {
		t.Fatalf("the log lost what the tee got: %q", log)
	}
}

func TestTailCutOnlyWhenTheTailStartsInsideALine(t *testing.T) {
	for name, tc := range map[string]struct {
		line string // one line of output, with its newline
		rows int
		cut  bool
	}{
		"short":                        {"0123456789\n", 10, false},
		"longer, tail inside a line":   {"0123456789\n", 40000, true},      // 11 bytes: 65536 is not a multiple
		"longer, tail at a line start": {"012345678901234\n", 8000, false}, // 16 bytes: the cut falls on a line boundary
		"exactly the tail":             {"012345678901234\n", 4096, false}, // 64 KiB of output, all of it kept
	} {
		g := &fakeGate{run: func(_ context.Context, o Options) int {
			for i := 0; i < tc.rows; i++ {
				o.Stdout.Write([]byte(tc.line))
			}
			return 0
		}}
		res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: g})
		if err != nil {
			t.Fatal(err)
		}
		if res.TailCut != tc.cut || len(res.Tail) == 0 || len(res.Tail) > 64<<10 {
			t.Errorf("%s: cut %v (want %v), tail %d bytes", name, res.TailCut, tc.cut, len(res.Tail))
		}
		res.Cleanup()
	}
}

// stuck is a live reader that takes the first chunk and never returns (an open pipe nobody reads).
type stuck struct{ release chan struct{} }

func (s *stuck) Write(p []byte) (int, error) { <-s.release; return len(p), nil }

func TestATeeThatStallsNeitherHoldsTheCommandNorLosesTheLog(t *testing.T) {
	const lines = 5000 // far more chunks than the buffer of a live reader
	g := &fakeGate{run: func(_ context.Context, o Options) int {
		for i := 0; i < lines; i++ {
			o.Stdout.Write([]byte("row\n"))
		}
		return 0
	}}
	st := &stuck{release: make(chan struct{})}
	defer close(st.release)
	done := make(chan Result, 1)
	go func() {
		res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: g, TeeStdout: st})
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	select {
	case res := <-done:
		defer res.Cleanup()
		log, _ := os.ReadFile(res.LogPath)
		if got := strings.Count(string(log), "row\n"); got != lines {
			t.Fatalf("the log has %d of %d rows", got, lines)
		}
		if len(res.Tail) == 0 {
			t.Fatal("the end of the output was not returned")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a stalled live reader held the command")
	}
}

func TestATeeThatFailsDoesNotStopTheCommandOrLoseTheLog(t *testing.T) {
	g := &fakeGate{run: func(_ context.Context, o Options) int {
		o.Stdout.Write([]byte("kept\n"))
		return 0
	}}
	bw := &brokenWriter{}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: g, TeeStdout: bw, TeeStderr: bw})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	log, _ := os.ReadFile(res.LogPath)
	if bw.n == 0 || res.Err != nil || !strings.Contains(string(log), "kept") {
		t.Fatalf("tee writes %d, result error %v, log %q", bw.n, res.Err, log)
	}
}

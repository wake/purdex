package fastlane

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/gate"
)

type brokenWriter struct{ n int }

func (b *brokenWriter) Write(p []byte) (int, error) { b.n++; return 0, errors.New("pipe closed") }

func TestTheCommandsOutputGoesToTheTeeAsWellAsTheLog(t *testing.T) {
	g := &fakeGate{run: func(_ context.Context, o gate.Options) int {
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

func TestATeeThatFailsDoesNotStopTheCommandOrLoseTheLog(t *testing.T) {
	g := &fakeGate{run: func(_ context.Context, o gate.Options) int {
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

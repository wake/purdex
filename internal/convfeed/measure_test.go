package convfeed

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMeasure_RealTranscript times the follower on a real transcript. It is skipped unless
// PDX_CONVFEED_TRANSCRIPT names a file; the file is only read (a copy takes the appended row):
//
//	PDX_CONVFEED_TRANSCRIPT=/path/to/session.jsonl go test ./internal/convfeed -run TestMeasure_RealTranscript -v -count=1
//
// It logs and asserts nothing: a wall-clock bound would be flaky, and the numbers go in the PR (an entry's mutex is
// held while it feeds, spec §8.2 / feedback_measure_work_inside_lock).
func TestMeasure_RealTranscript(t *testing.T) {
	path := os.Getenv("PDX_CONVFEED_TRANSCRIPT")
	if path == "" {
		t.Skip("set PDX_CONVFEED_TRANSCRIPT to time the follower on a real transcript")
	}
	dir := t.TempDir()
	cp := filepath.Join(dir, "copy.jsonl")
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(cp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	in.Close()
	out.Close()

	f, err := os.OpenFile(cp, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	size, _ := osFile{f}.Size()
	e := NewEntry(sidA)
	s := Source{File: osFile{f}, Identity: "copy", Live: true}

	t0 := time.Now()
	if _, err := e.Refresh(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	fromZero := time.Since(t0)

	if _, err := f.Write(append(userRow("u-measure", 1e6, "one appended row"), '\n')); err != nil {
		t.Fatal(err)
	}
	t1 := time.Now()
	if _, err := e.Refresh(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	oneRow := time.Since(t1)

	t2 := time.Now()
	ch := e.ChangesSince(e.Revision() - 1)
	changes := time.Since(t2)

	t3 := time.Now()
	w := e.Window(200, -1, everything)
	window := time.Since(t3)
	t4 := time.Now()
	b, _ := json.Marshal(w.Turns)
	encode := time.Since(t4)

	t.Logf("file %.1f MB, %d turns, %d changes after the appended row", float64(size)/(1<<20), w.TotalTurns, len(ch))
	t.Logf("refresh from zero: %v | one appended row: %v | ChangesSince: %v | Window(200): %v | encode %d turns -> %.1f KB: %v",
		fromZero.Round(time.Millisecond), oneRow.Round(time.Microsecond), changes.Round(time.Microsecond),
		window.Round(time.Millisecond), len(w.Turns), float64(len(b))/1024, encode.Round(time.Millisecond))
}

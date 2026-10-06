package conversations

import (
	"path/filepath"
	"strings"
	"testing"
)

// readTail opens path and reads the tail of its first size bytes (the whole
// file when size < 0).
func readTail(t *testing.T, path string, size int64) (Tail, int64) {
	t.Helper()
	f, e, err := OpenTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if size < 0 {
		size = e.Size
	}
	tail, n, err := ReadTail(f, size)
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}
	return tail, n
}

func aiTitle(s string) obj {
	return obj{"type": "ai-title", "aiTitle": s, "sessionId": sidA}
}

func customTitle(s string) obj {
	return obj{"type": "custom-title", "customTitle": s, "sessionId": sidA}
}

func TestReadTail_LatestTitlesAndEntrypoint(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, p, lines(t,
		aiTitle("old ai"),
		with(userText("hi"), obj{"entrypoint": "cli"}),
		customTitle("  My name  "),
		aiTitle("new ai"),
		obj{"type": "assistant", "entrypoint": "sdk-cli"},
		obj{"type": "summary"},
	))

	tail, n := readTail(t, p, -1)
	want := Tail{LastEntrypoint: "sdk-cli", CustomTitle: "My name", AITitle: "new ai"}
	if tail != want {
		t.Errorf("tail = %+v, want %+v", tail, want)
	}
	if size := fileSize(t, p); n != size {
		t.Errorf("n = %d, want %d", n, size)
	}
}

func TestReadTail_FirstLineCountsWhenTheWindowStartsAtZero(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, p, lines(t, aiTitle("only"), userText("hi")))
	if tail, _ := readTail(t, p, -1); tail.AITitle != "only" {
		t.Errorf("AITitle = %q, want %q", tail.AITitle, "only")
	}
}

func TestReadTail_EmptyTitleDoesNotReplace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, p, lines(t,
		aiTitle("kept"), customTitle("named"),
		aiTitle(""), aiTitle("   "), customTitle(""), customTitle(" \t "),
	))
	want := Tail{CustomTitle: "named", AITitle: "kept"}
	if tail, _ := readTail(t, p, -1); tail != want {
		t.Errorf("tail = %+v, want %+v", tail, want)
	}
}

func TestReadTail_TitleBeforeTheWindowIsMissed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, p,
		lines(t, aiTitle("early"), customTitle("early name"), obj{"type": "system", "entrypoint": "cli"}),
		padding(t, TailWindow+1000),
	)
	tail, n := readTail(t, p, -1)
	if tail != (Tail{}) {
		t.Errorf("tail = %+v, want empty", tail)
	}
	// The byte before the window is read too, to tell a cut line from a
	// whole one.
	if n != TailWindow+1 {
		t.Errorf("n = %d, want %d", n, TailWindow+1)
	}
}

func TestReadTail_KeepsALineStartingExactlyAtTheWindowStart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	before := join(lines(t, aiTitle("before")), padding(t, 4096))
	edge := line(t, aiTitle("on the edge"))
	writeFile(t, p, before, edge, padding(t, TailWindow-len(edge)))
	if start := fileSize(t, p) - TailWindow; start != int64(len(before)) {
		t.Fatalf("fixture: window starts at %d, want %d", start, len(before))
	}

	tail, n := readTail(t, p, -1)
	if tail.AITitle != "on the edge" {
		t.Errorf("AITitle = %q, want %q", tail.AITitle, "on the edge")
	}
	if n != TailWindow+1 {
		t.Errorf("n = %d, want %d", n, TailWindow+1)
	}
}

func TestReadTail_EmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, p)
	tail, n := readTail(t, p, -1)
	if tail != (Tail{}) || n != 0 {
		t.Errorf("ReadTail(empty) = %+v, %d; want zero, 0", tail, n)
	}
}

func TestReadTail_SkipsThePieceAcrossTheWindowStart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	before := join(lines(t, aiTitle("before")), padding(t, 4096))
	// A valid line (JSON allows leading whitespace) that starts before the
	// window; the window starts inside its spaces, so the piece the window
	// sees would parse on its own.
	straddling := line(t, aiTitle("straddling"))
	straddling = append([]byte(strings.Repeat(" ", 100)), straddling...)
	cutAt := len(before) + 50
	after := padding(t, cutAt+TailWindow-len(before)-len(straddling))
	writeFile(t, p, before, straddling, after)
	if start := fileSize(t, p) - TailWindow; start != int64(cutAt) {
		t.Fatalf("fixture: window starts at %d, want %d", start, cutAt)
	}

	if tail, _ := readTail(t, p, -1); tail != (Tail{}) {
		t.Errorf("tail = %+v, want empty", tail)
	}
}

func TestReadTail_FinalPieceWithoutNewline(t *testing.T) {
	t.Run("valid JSON counts", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "t.jsonl")
		last := strings.TrimSuffix(string(line(t, aiTitle("last"))), "\n")
		writeFile(t, p, lines(t, aiTitle("earlier")), []byte(last))
		if tail, _ := readTail(t, p, -1); tail.AITitle != "last" {
			t.Errorf("AITitle = %q, want %q", tail.AITitle, "last")
		}
	})
	t.Run("a partial line is skipped", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "t.jsonl")
		writeFile(t, p, lines(t, aiTitle("earlier")), []byte(`{"type":"ai-title","aiTitle":"half`))
		if tail, _ := readTail(t, p, -1); tail.AITitle != "earlier" {
			t.Errorf("AITitle = %q, want %q", tail.AITitle, "earlier")
		}
	})
}

func TestReadTail_ReadsOnlyTheGivenSize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, p, lines(t, aiTitle("before")))
	size := fileSize(t, p)
	appendFile(t, p, lines(t, aiTitle("after")))

	tail, n := readTail(t, p, size)
	if tail.AITitle != "before" || n != size {
		t.Errorf("ReadTail(size %d) = %+v, %d; want AITitle %q, %d", size, tail, n, "before", size)
	}
}

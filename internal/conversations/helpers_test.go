package conversations

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Session ids used as transcript names in the fixtures.
const (
	sidA = "0a1b2c3d-0000-4000-8000-00000000000a"
	sidB = "0a1b2c3d-0000-4000-8000-00000000000b"
	sidC = "0a1b2c3d-0000-4000-8000-00000000000c"
)

// obj is one JSON object of a fixture line.
type obj = map[string]any

// line encodes v as one '\n'-terminated JSONL line, keeping '<' and '>'
// literal as Claude Code writes them.
func line(t testing.TB, v any) []byte {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("encode fixture line: %v", err)
	}
	return b.Bytes()
}

// lines encodes each value as one line.
func lines(t testing.TB, vs ...any) []byte {
	t.Helper()
	var b []byte
	for _, v := range vs {
		b = append(b, line(t, v)...)
	}
	return b
}

func join(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// userText is a user line whose message.content is a string.
func userText(text string) obj {
	return obj{"type": "user", "message": obj{"role": "user", "content": text}}
}

// userBlocks is a user line whose message.content is an array of blocks.
func userBlocks(blocks ...obj) obj {
	return obj{"type": "user", "message": obj{"role": "user", "content": blocks}}
}

// with returns o with extra's fields added.
func with(o obj, extra obj) obj {
	out := obj{}
	for k, v := range o {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// padding returns filler lines (assistant lines with no cwd, entrypoint,
// title or prompt) totalling exactly n bytes.
func padding(t testing.TB, n int) []byte {
	t.Helper()
	const (
		prefix   = `{"type":"assistant","pad":"`
		suffix   = "\"}\n"
		overhead = len(prefix) + len(suffix)
		lineSize = 1024
	)
	if n < overhead {
		t.Fatalf("padding(%d): at least %d bytes", n, overhead)
	}
	var b bytes.Buffer
	b.Grow(n)
	for n > 0 {
		size := lineSize
		switch {
		case n <= lineSize:
			size = n
		case n-lineSize < overhead:
			size = n / 2 // the rest would be too short for a line of its own
		}
		b.WriteString(prefix)
		b.Write(bytes.Repeat([]byte("x"), size-overhead))
		b.WriteString(suffix)
		n -= size
	}
	return b.Bytes()
}

// writeFile creates path (and its parent dirs) with the given parts.
func writeFile(t testing.TB, path string, parts ...[]byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, join(parts...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// appendFile appends the given parts to path.
func appendFile(t testing.TB, path string, parts ...[]byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(join(parts...)); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// fileSize is path's size by os.Stat.
func fileSize(t testing.TB, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

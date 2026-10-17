// Package summary turns a finished task's report into the wire summary (spec §5): the counts, the failed tests with where and
// why, and the last lines of the log. An agent reads the summary first and the full log only when it needs to.
package summary

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	// PlainTailLines is how much of the log a plain summary keeps (spec §5); the other kinds keep a short tail for context.
	PlainTailLines  = 40
	shortTailLines  = 10
	maxLineBytes    = 500
	maxFailures     = 50
	maxMessageBytes = 2000
)

// Input is what a finished job left behind.
type Input struct {
	Stdout []byte // the command's standard output (go-json reads it)
	Log    []byte // stdout and stderr as they came, for the tail
	Report []byte // the {report} file (vitest-json)
	Junit  []byte // the {junit} file (junit)
	// Root is the workspace directory; absolute file names under it are shortened to be relative.
	Root string
}

// Parse builds the summary for the task's summary kind. It always returns a usable summary: when the report cannot be read the
// counts are zero, the tail is there, and the error says why.
func Parse(kind string, in Input) (wire.Summary, error) {
	var (
		s   wire.Summary
		err error
	)
	tail := shortTailLines
	switch kind {
	case "go-json":
		s, err = parseGoJSON(in)
	case "vitest-json":
		s, err = parseVitest(in)
	case "junit":
		s, err = parseJunit(in)
	case "plain":
		tail = PlainTailLines
	default:
		err = fmt.Errorf("unknown summary kind %q", kind)
		tail = PlainTailLines
	}
	s.Tail = lastLines(in.Log, tail)
	if s.Failures == nil {
		s.Failures = []wire.Failure{}
	}
	return s, err
}

func lastLines(b []byte, n int) []string {
	b = bytes.TrimRight(b, "\r\n")
	if len(b) == 0 {
		return []string{}
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		lines[i] = clip(l, maxLineBytes)
	}
	return lines
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 { // do not cut inside a UTF-8 sequence
		cut--
	}
	return s[:cut] + "…"
}

// rel shortens an absolute path under root to a relative one.
func rel(root, p string) string {
	if root == "" || !filepath.IsAbs(p) {
		return p
	}
	if r, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return p
}

// addFailure records a failure up to the cap; the counts stay exact.
func addFailure(s *wire.Summary, f wire.Failure) {
	if len(s.Failures) >= maxFailures {
		return
	}
	f.Message = clip(strings.TrimSpace(f.Message), maxMessageBytes)
	s.Failures = append(s.Failures, f)
}

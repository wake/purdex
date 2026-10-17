// Package summary turns a finished task's report into the wire summary (spec §5): the counts, the failed tests with where and
// why, and the last lines of the log. An agent reads the summary first and the full log only when it needs to.
//
// The reports come from the commands a job ran, so every parser is bounded: an input over maxInput is refused, depth and
// per-test text are capped, and nothing recurses on the report's own structure.
package summary

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	// PlainTailLines is how much of the log a plain summary keeps (spec §5); the other kinds keep a short tail for context.
	PlainTailLines  = 40
	shortTailLines  = 10
	maxLineBytes    = 500
	maxFailures     = 50
	maxMessageBytes = 2000
	// maxInput is the largest report or output a parser reads.
	maxInput = 32 << 20
	// outside stands for a path the report names outside the workspace.
	outside = "<outside-workspace>"
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

// Parse builds the summary for the task's summary kind. It always returns a usable summary: when the report cannot be read
// the counts are zero, the tail is there, and the error says why.
func Parse(kind string, in Input) (wire.Summary, error) {
	var (
		s   wire.Summary
		err error
	)
	tail := shortTailLines
	switch kind {
	case "go-json":
		s, err = guard(in.Stdout, kind, in, parseGoJSON)
	case "vitest-json":
		s, err = guard(in.Report, kind, in, parseVitest)
	case "junit":
		s, err = guard(in.Junit, kind, in, parseJunit)
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

func guard(src []byte, kind string, in Input, parse func(Input) (wire.Summary, error)) (wire.Summary, error) {
	if len(src) > maxInput {
		return wire.Summary{}, fmt.Errorf("%s: the report is %d bytes, over the %d limit", kind, len(src), maxInput)
	}
	return parse(in)
}

// lastLines is the last n lines of b, found from the end so a huge log is not copied or split.
func lastLines(b []byte, n int) []string {
	b = bytes.TrimRight(b, "\r\n")
	if len(b) == 0 {
		return []string{}
	}
	start := len(b)
	for i := 0; i < n; i++ {
		j := bytes.LastIndexByte(b[:start], '\n')
		if j < 0 {
			start = 0
			break
		}
		start = j
	}
	if b[start] == '\n' {
		start++
	}
	lines := strings.Split(string(b[start:]), "\n")
	for i, l := range lines {
		lines[i] = clip(strings.TrimRight(l, "\r"), maxLineBytes)
	}
	return lines
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) { // do not cut inside a UTF-8 sequence
		cut--
	}
	return s[:cut] + "…"
}

// rel shortens a path under root to a relative one. A path outside the workspace (or an absolute path with no workspace to
// compare to) is not repeated: the report is the job's own output and must not teach readers the host's directory layout.
func rel(root, p string) string {
	if p == "" {
		return p
	}
	if filepath.IsAbs(p) {
		if root != "" {
			if r, err := filepath.Rel(root, p); err == nil && r != ".." && !strings.HasPrefix(r, "../") {
				return filepath.ToSlash(r)
			}
		}
		return outside + "/" + path.Base(filepath.ToSlash(p))
	}
	clean := path.Clean(filepath.ToSlash(p))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return outside + "/" + path.Base(clean)
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

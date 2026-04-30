package codexbroker

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// RawProcess is one row of `ps -eo pid=,ppid=,lstart=,rss=,command=` output.
type RawProcess struct {
	PID            int
	PPID           int
	Lstart         time.Time // zero iff LstartParseErr != "".
	LstartParseErr string    // non-empty when ps lstart was unrecognised.
	RSSKB          int64
	Cmdline        string
}

// ProcessLister enumerates all visible processes. Filtering to brokers is
// done in scan_process.go so the lister stays generic.
type ProcessLister interface {
	List(ctx context.Context) ([]RawProcess, error)
}

// PsLister invokes /bin/ps on the host using the wide format that disables
// argv truncation. Used in production.
//
// We use `ps -eo pid=,ppid=,lstart=,rss=,command=` to elide headers and
// `-ww` (or `-eww` on Linux) to disable argv truncation; selection of
// the wide flag is platform-specific so PsLister adapts.
type PsLister struct {
	// Argv overrides the default ps invocation. If nil, sensible defaults
	// for the current platform are used.
	Argv []string
}

// NewPsLister returns a production lister with platform-default argv.
func NewPsLister() *PsLister {
	return &PsLister{Argv: defaultPsArgv()}
}

// defaultPsArgv returns the ps invocation that produces the expected fields
// without argv truncation. We use the BSD/macOS-compatible form that also
// works on GNU ps (Linux): `ps -eww -o pid=,ppid=,lstart=,rss=,command=`.
func defaultPsArgv() []string {
	return []string{"ps", "-eww", "-o", "pid=,ppid=,lstart=,rss=,command="}
}

// List runs ps and parses its output.
func (l *PsLister) List(ctx context.Context) ([]RawProcess, error) {
	argv := l.Argv
	if len(argv) == 0 {
		argv = defaultPsArgv()
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return ParsePsOutput(string(out))
}

// ParsePsOutput parses the stdout of `ps -o pid=,ppid=,lstart=,rss=,command=`.
// The columns are space-separated; lstart is itself five tokens
// (`Wkd Mon Day HH:MM:SS Year` on macOS) or two tokens (`YYYY-MM-DD HH:MM:SS`
// on Linux). The parser supports both common shapes.
func ParsePsOutput(s string) ([]RawProcess, error) {
	scanner := bufio.NewScanner(strings.NewReader(s))
	// Allow large lines (long argv).
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	var rows []RawProcess
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		row, err := parsePsLine(line)
		if err != nil {
			// Whole-line malformation that's not even pid+ppid: skip
			// silently. Unit tests should not produce such lines and
			// production ps should not either.
			continue
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan ps: %w", err)
	}
	return rows, nil
}

// parsePsLine parses one row.
//
// Layout (tokens, space-separated):
//
//	pid ppid <lstart-tokens> rss <command...>
//
// `<lstart-tokens>` is 5 tokens on macOS and 2 tokens on Linux; we detect by
// trying both layouts and accepting whichever produces a valid time. RSS is
// the next integer after the lstart tokens; command is everything after.
//
// On lstart parse failure we still emit a RawProcess with LstartParseErr
// populated and a best-effort estimate of where rss/command start.
func parsePsLine(line string) (RawProcess, error) {
	// Use Fields to collapse internal whitespace runs (ps pads lstart).
	fields := strings.Fields(line)
	if len(fields) < 7 {
		return RawProcess{}, fmt.Errorf("too few fields: %q", line)
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return RawProcess{}, fmt.Errorf("pid: %w", err)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return RawProcess{}, fmt.Errorf("ppid: %w", err)
	}

	// Try macOS lstart: 5 tokens at indices 2..6, rss at 7, cmd from 8.
	// Try Linux lstart: 2 tokens at 2..3, rss at 4, cmd from 5.
	var (
		lstart  time.Time
		parseOK bool
		rssIdx  int
		cmdIdx  int
	)

	if len(fields) >= 9 {
		macStr := strings.Join(fields[2:7], " ")
		if t, err := parseLstart(macStr); err == nil {
			lstart = t
			parseOK = true
			rssIdx = 7
			cmdIdx = 8
		}
	}
	if !parseOK && len(fields) >= 6 {
		linuxStr := strings.Join(fields[2:4], " ")
		if t, err := parseLstart(linuxStr); err == nil {
			lstart = t
			parseOK = true
			rssIdx = 4
			cmdIdx = 5
		}
	}

	row := RawProcess{PID: pid, PPID: ppid}
	if !parseOK {
		row.LstartParseErr = "unrecognised lstart format"
		// Best-effort: assume macOS layout for rss/cmd boundaries so the
		// caller can still inspect cmdline.
		if len(fields) >= 9 {
			rssIdx, cmdIdx = 7, 8
		} else {
			rssIdx, cmdIdx = 4, 5
		}
	} else {
		row.Lstart = lstart
	}

	if rssIdx < len(fields) {
		if rss, err := strconv.ParseInt(fields[rssIdx], 10, 64); err == nil {
			row.RSSKB = rss
		}
	}
	if cmdIdx < len(fields) {
		row.Cmdline = strings.Join(fields[cmdIdx:], " ")
	}
	return row, nil
}

// parseLstart accepts either the macOS form `Thu May  1 13:14:15 2026`
// (note: day may be 1- or 2-padded so we match by Fields() collapse) or the
// Linux form `2026-05-01 13:14:15`.
func parseLstart(s string) (time.Time, error) {
	// macOS: collapsed runs, must look like `Wkd Mon D HH:MM:SS YYYY`.
	if t, err := time.Parse("Mon Jan 2 15:04:05 2006", s); err == nil {
		return t, nil
	}
	// Linux: `YYYY-MM-DD HH:MM:SS`.
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("lstart not recognised: %q", s)
}

// FakeProcessLister is a test double backed by a static row list. Optionally
// applies a delay before returning so timeout paths can be exercised.
type FakeProcessLister struct {
	rows  []RawProcess
	err   error
	delay time.Duration
}

// NewFakeProcessLister returns a fake lister returning the supplied rows.
func NewFakeProcessLister(rows []RawProcess) *FakeProcessLister {
	return &FakeProcessLister{rows: rows}
}

// NewFakeProcessListerFromText parses the supplied ps output text once and
// returns a fake lister that replays it.
func NewFakeProcessListerFromText(s string) *FakeProcessLister {
	rows, _ := ParsePsOutput(s)
	return &FakeProcessLister{rows: rows}
}

// WithError configures the fake lister to return err on every call.
func (f *FakeProcessLister) WithError(err error) *FakeProcessLister {
	f.err = err
	return f
}

// WithDelay configures a synthetic delay before List returns.
func (f *FakeProcessLister) WithDelay(d time.Duration) *FakeProcessLister {
	f.delay = d
	return f
}

// List satisfies ProcessLister.
func (f *FakeProcessLister) List(ctx context.Context) ([]RawProcess, error) {
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	out := make([]RawProcess, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

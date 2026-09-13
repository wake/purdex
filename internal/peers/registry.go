// Package peers reads Claude Code's own session registry
// (~/.claude/sessions/<pid>.json) and decides which entries are live.
package peers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/agent"
)

// registryFile is the on-disk wire shape of a Claude Code session registry
// file. Unknown fields are ignored; a field of the wrong JSON type makes the
// whole file a decode error.
type registryFile struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	ProcStart  string `json:"procStart"`
	Version    string `json:"version"`
	Tmux       string `json:"tmux"`
	Inbox      string `json:"messagingSocketPath"`
	Name       string `json:"name"`
	NameSource string `json:"nameSource"`
	Status     string `json:"status"`
}

// Entry is one live Claude Code session found in the registry.
type Entry struct {
	PID        int
	SessionID  string
	Name       string
	NameSource string
	Cwd        string
	Tmux       string // "<session>:@<win>.%<pane>" or ""
	Inbox      string // messagingSocketPath
	ProcStart  string // raw registry string, e.g. "Sun Sep 13 15:22:36 2026"
	Version    string
	Status     string // "idle" | "busy" | ""
}

// TmuxSessionName returns the text before the first ':' in Tmux, or "" if
// there is no ':'.
func (e Entry) TmuxSessionName() string {
	idx := strings.Index(e.Tmux, ":")
	if idx < 0 {
		return ""
	}
	return e.Tmux[:idx]
}

// TmuxPaneID returns the "%…" text after the last '.' in Tmux, or "" if
// absent.
func (e Entry) TmuxPaneID() string {
	idx := strings.LastIndex(e.Tmux, ".")
	if idx < 0 {
		return ""
	}
	return e.Tmux[idx+1:]
}

// Liveness holds the (injectable) primitives used to decide whether a
// registry entry refers to a still-running process. Tests substitute fakes
// so no test needs to fork ps.
type Liveness struct {
	Stat      func(path string) error
	PidAlive  func(pid int) bool
	StartTime func(pid int) (time.Time, error)
}

// DefaultLiveness returns the real, OS-backed Liveness.
func DefaultLiveness() Liveness {
	return Liveness{
		Stat: func(path string) error {
			_, err := os.Stat(path)
			return err
		},
		PidAlive: func(pid int) bool {
			return syscall.Kill(pid, 0) == nil
		},
		StartTime: func(pid int) (time.Time, error) {
			info, err := agent.ReadProcessInfo(pid)
			if err != nil {
				return time.Time{}, err
			}
			return info.StartTime, nil
		},
	}
}

// ProcStartLayout is Claude Code's registry format (UTC ctime).
const ProcStartLayout = "Mon Jan _2 15:04:05 2006"

// ParseProcStart parses a registry procStart string as UTC.
func ParseProcStart(s string) (time.Time, error) {
	return time.ParseInLocation(ProcStartLayout, s, time.UTC)
}

// registryFilenamePattern matches "<pid>.json" registry filenames.
var registryFilenamePattern = regexp.MustCompile(`^([0-9]+)\.json$`)

// ReadRegistry parses every "<pid>.json" in dir and returns the live ones.
// skipped counts every file considered and rejected (name mismatch, decode
// error, missing required field, bad procStart, dead). A dir that does not
// exist is treated as an empty registry (nil entries, 0 skipped, nil err) —
// there being no Claude Code registry yet is not an error condition. err is
// non-nil for any other listing failure (e.g. dir is a regular file).
func ReadRegistry(dir string, live Liveness) (entries []Entry, skipped int, err error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}

	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		m := registryFilenamePattern.FindStringSubmatch(name)
		if m == nil {
			continue // never matched the name pattern; not counted
		}
		expectedPID, atoiErr := strconv.Atoi(m[1])
		if atoiErr != nil {
			// Overflow guard: the regexp only matches digits, but an
			// implausibly long digit string would overflow int.
			skipped++
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			skipped++
			continue
		}

		var wire registryFile
		if err := json.Unmarshal(data, &wire); err != nil {
			skipped++
			continue
		}

		if wire.PID == 0 || wire.SessionID == "" || wire.ProcStart == "" || wire.Inbox == "" {
			skipped++
			continue
		}
		if wire.PID != expectedPID {
			skipped++
			continue
		}

		procStart, err := ParseProcStart(wire.ProcStart)
		if err != nil {
			skipped++
			continue
		}

		if !isLive(wire.PID, wire.Inbox, procStart, live) {
			skipped++
			continue
		}

		entries = append(entries, Entry{
			PID:        wire.PID,
			SessionID:  wire.SessionID,
			Name:       wire.Name,
			NameSource: wire.NameSource,
			Cwd:        wire.Cwd,
			Tmux:       wire.Tmux,
			Inbox:      wire.Inbox,
			ProcStart:  wire.ProcStart,
			Version:    wire.Version,
			Status:     wire.Status,
		})
	}

	return entries, skipped, nil
}

// isLive applies the liveness rule: live iff Stat(inbox)==nil &&
// PidAlive(pid) && StartTime(pid) returns no error && the two instants are
// equal (to the second).
func isLive(pid int, inbox string, procStart time.Time, live Liveness) bool {
	if live.Stat(inbox) != nil {
		return false
	}
	if !live.PidAlive(pid) {
		return false
	}
	startTime, err := live.StartTime(pid)
	if err != nil {
		return false
	}
	return startTime.Truncate(time.Second).Equal(procStart.Truncate(time.Second))
}

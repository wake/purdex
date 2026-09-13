// Package peers reads Claude Code's own session registry
// (~/.claude/sessions/<pid>.json) and decides which entries are live.
package peers

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/agent"
)

// maxRegistryFileBytes is the size cap enforced on every "<pid>.json"
// registry candidate: files at or under this size are read in full, and
// files that would exceed it (including a symlink target reached through a
// TOCTOU race, which the read-side cap still catches) are skipped rather
// than read into memory. Claude Code's registry files are well under 1 KiB
// in practice; 64 KiB leaves generous headroom while bounding one hostile or
// corrupt file's cost to a fixed amount of work.
const maxRegistryFileBytes = 64 * 1024

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
	IsProxy    bool   // set from Liveness.Info's Argv (D9); false when Info is nil
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
	StartTime func(pid int) (time.Time, error)         // kept for P1 fakes; unused per-entry once Info is set
	Info      func(pid int) (agent.ProcessInfo, error) // optional; when non-nil, replaces StartTime and also supplies Argv/ExePath for proxy classification (D9)
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
		Info: agent.ReadProcessInfo,
	}
}

// IsProxyProcess reports whether info describes a Purdex peer-proxy helper
// process (D9): its executable (by ExePath, or falling back to argv[0] when
// ExePath is unavailable/renamed) is named "pdx", AND its argv contains the
// literal element "peer-proxy". Known limitation (D9): a process that
// happens to satisfy both conditions without actually being a pdx
// peer-proxy helper (e.g. a maliciously renamed binary) would be
// misclassified; this is accepted as a low-value spoof to defend against.
func IsProxyProcess(info agent.ProcessInfo) bool {
	if len(info.Argv) == 0 {
		return false
	}

	isPdxExe := filepath.Base(info.ExePath) == "pdx"
	isPdxArgv0 := filepath.Base(info.Argv[0]) == "pdx"
	if !isPdxExe && !isPdxArgv0 {
		return false
	}

	for _, a := range info.Argv {
		if a == "peer-proxy" {
			return true
		}
	}
	return false
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

		data, ok := ReadRegistryCandidate(filepath.Join(dir, name))
		if !ok {
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

		// D9 proxy classification. When Info is set, it replaces StartTime
		// (one call per entry, not two) and also supplies Argv/ExePath so
		// IsProxyProcess can classify the entry. Fail closed: a process
		// that cannot be classified (Info errors, or returns an empty
		// Argv — e.g. a permission-denied /proc read, or the process
		// having already exited) must never become a deliverable cc row,
		// so it is skipped exactly like a dead entry rather than defaulting
		// to IsProxy=false.
		entryLive := live
		isProxy := false
		if live.Info != nil {
			info, infoErr := live.Info(wire.PID)
			if infoErr != nil || len(info.Argv) == 0 {
				skipped++
				log.Printf("peers: registry: pid %d: could not classify process (argv unavailable: %v); skipping", wire.PID, infoErr)
				continue
			}
			isProxy = IsProxyProcess(info)
			entryLive.StartTime = func(int) (time.Time, error) { return info.StartTime, nil }
		}

		if !isLive(wire.PID, wire.Inbox, procStart, entryLive) {
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
			IsProxy:    isProxy,
		})
	}

	return entries, skipped, nil
}

// ReadRegistryCandidate reads one registry file (a "<pid>.json" or key
// file) defensively — the shared contract for every registry read: it
// never follows a symlink (O_NOFOLLOW — a candidate that IS a symlink is
// rejected outright, not resolved), never blocks on a non-regular file (a
// FIFO, in particular, blocks forever on a plain read with no writer — the
// fstat check below rejects it before any read is attempted), and never
// reads more than maxRegistryFileBytes+1 bytes regardless of what the file
// claims its size is (the read-side cap, not just the fstat size, is what
// actually bounds memory use against a TOCTOU race or a growing file). ok is
// false for any of these cases; the caller counts it as skipped.
func ReadRegistryCandidate(path string) (data []byte, ok bool) {
	// O_NONBLOCK matters only for a FIFO: without it, opening one for
	// reading blocks until a writer opens the other end — before the fstat
	// check below ever runs. With it, the open returns immediately (POSIX
	// guarantees a non-blocking read-only open of a FIFO succeeds even with
	// no writer present) and the fstat check rejects it as non-regular. It
	// is a no-op for the regular-file case this function exists to serve.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, false
	}
	if !info.Mode().IsRegular() {
		return nil, false
	}
	if info.Size() > maxRegistryFileBytes {
		return nil, false
	}

	data, err = io.ReadAll(io.LimitReader(f, maxRegistryFileBytes+1))
	if err != nil {
		return nil, false
	}
	if len(data) > maxRegistryFileBytes {
		return nil, false
	}
	return data, true
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

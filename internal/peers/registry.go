// Package peers reads Claude Code's own session registry
// (~/.claude/sessions/<pid>.json) and decides which entries are live.
package peers

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	// Stat probes the entry's inbox socket. Per spec §3.3: fs.ErrNotExist
	// (ENOENT) is "confirmed dead" — the socket is gone; any other error
	// (permission denied, a transient stat failure, …) is "unknown", not
	// "dead" — it does not prove the session is gone.
	Stat      func(path string) error
	PidAlive  func(pid int) bool
	StartTime func(pid int) (time.Time, error)         // kept for P1 fakes; unused per-entry once Info is set
	Info      func(pid int) (agent.ProcessInfo, error) // optional; when non-nil, replaces StartTime and also supplies Argv/ExePath for proxy classification (D9)
}

// killProbe is kill(2) — a package-level seam so DefaultLiveness's EPERM
// rule is testable without a real process to probe.
var killProbe = syscall.Kill

// DefaultLiveness returns the real, OS-backed Liveness.
func DefaultLiveness() Liveness {
	return Liveness{
		Stat: func(path string) error {
			_, err := os.Stat(path)
			return err
		},
		PidAlive: func(pid int) bool {
			// EPERM means the pid exists but is owned by another user
			// (kill(2) checked permission before checking that the
			// signal — here 0 — would be delivered): the process is
			// alive, just not ours to signal (spec §3.3).
			err := killProbe(pid, 0)
			return err == nil || errors.Is(err, syscall.EPERM)
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

// MaxRegistryPID bounds a filename pid before it is ever probed (spec
// §3.3): a filename digit string that overflows int, or decodes to a pid
// above this, cannot name a real process, so it is classified unknown with
// Alive unconditionally false — never passed to PidAlive.
const MaxRegistryPID = 1<<31 - 1

// warnedUnclassifiablePids dedupes the "could not classify process" log
// line (in ReadRegistry, below) to at most once per pid for the lifetime of
// the process. ReadRegistry is otherwise a pure, stateless function of its
// (dir, live) arguments — this package-level map is the one deliberate
// exception, so a persistently unclassifiable process (e.g. a
// permission-denied /proc read that never resolves) does not log a fresh
// line on every /api/peers poll.
var warnedUnclassifiablePids sync.Map

// warnUnclassifiableOnce logs pid's Info failure the first time it is seen
// and is a silent no-op on every subsequent call for the same pid.
func warnUnclassifiableOnce(pid int, err error) {
	if _, already := warnedUnclassifiablePids.LoadOrStore(pid, struct{}{}); already {
		return
	}
	log.Printf("peers: registry: pid %d: could not classify process (argv unavailable: %v); skipping", pid, err)
}

// UnknownFile is a registry candidate the daemon rejected but could not
// prove dead (spec §3.3 "unknown"). Alive is kill(pid,0) on the FILENAME
// pid — the file's own claimed pid (if any) is not trusted for this, since
// an unknown file is by definition one whose contents could not be
// verified against that filename.
type UnknownFile struct {
	Path   string
	PID    int
	Alive  bool
	Reason string
}

// Diagnosis classifies every registry candidate that ReadRegistryDiag did
// not accept as a live entry (spec §3.3).
type Diagnosis struct {
	Dead    int           // confirmed dead: proven not to be a live session
	Unknown []UnknownFile // could not be classified either way; an Alive one blocks label claims and marks the inventory partial
}

// BlockingUnknown returns the paths of Unknown files whose (filename) pid
// is alive — the ones later tasks must treat as "might still be a live
// session" and refuse to claim over.
func (d Diagnosis) BlockingUnknown() []string {
	var out []string
	for _, u := range d.Unknown {
		if u.Alive {
			out = append(out, u.Path)
		}
	}
	return out
}

// unknown records one Unknown classification for path/pid with reason,
// deciding Alive from the filename pid (see UnknownFile).
func (d *Diagnosis) unknown(path string, pid int, alive bool, reason string) {
	d.Unknown = append(d.Unknown, UnknownFile{Path: path, PID: pid, Alive: alive, Reason: reason})
}

// ReadRegistry parses every "<pid>.json" in dir and returns the live ones.
// skipped counts every file considered and rejected — it is
// diag.Dead+len(diag.Unknown) from ReadRegistryDiag, collapsed to a single
// count for callers that do not need the confirmed-dead/unknown split. A
// dir that does not exist is treated as an empty registry (nil entries, 0
// skipped, nil err) — there being no Claude Code registry yet is not an
// error condition. err is non-nil for any other listing failure (e.g. dir
// is a regular file).
func ReadRegistry(dir string, live Liveness) (entries []Entry, skipped int, err error) {
	entries, diag, err := ReadRegistryDiag(dir, live)
	return entries, diag.Dead + len(diag.Unknown), err
}

// ReadRegistryDiag parses every "<pid>.json" in dir, returning the live
// entries and a Diagnosis of everything else (spec §3.3 "Registry
// diagnosis"). A rejected candidate is classified:
//
//   - confirmed dead: the inbox socket is gone (Stat ⇒ ENOENT); the pid is
//     not alive; or a start time was successfully read and differs from
//     procStart. All three positively prove the registry entry no longer
//     names a live session.
//   - unknown: everything else that is not live — the file is unreadable
//     or undecodable, fails schema, its pid does not match the filename,
//     its procStart does not parse, Stat failed with something other than
//     ENOENT, the process could not be classified (D9 fail-closed), or its
//     start time could not be read. None of these prove the session is
//     gone, so Alive is decided on the FILENAME pid (see UnknownFile) and,
//     when true, the file blocks label claims (BlockingUnknown).
//
// A dir that does not exist is treated as an empty registry (nil entries,
// zero-value Diagnosis, nil err). err is non-nil for any other listing
// failure (e.g. dir is a regular file).
func ReadRegistryDiag(dir string, live Liveness) (entries []Entry, diag Diagnosis, err error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, Diagnosis{}, nil
		}
		return nil, Diagnosis{}, err
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
		path := filepath.Join(dir, name)

		expectedPID, atoiErr := strconv.Atoi(m[1])
		if atoiErr != nil || expectedPID <= 0 || expectedPID > MaxRegistryPID {
			// Overflow guard / range guard: the regexp only matches
			// digits, but an implausibly long or large digit string
			// cannot name a real pid — never probed, so Alive is
			// unconditionally false (spec §3.3).
			diag.unknown(path, 0, false, "pid out of range")
			continue
		}
		// unknown is the common-case recorder for the rest of this
		// iteration: Alive is always decided on expectedPID (the
		// FILENAME pid), never on any pid read from the file's contents.
		unknown := func(reason string) {
			diag.unknown(path, expectedPID, live.PidAlive(expectedPID), reason)
		}

		data, ok := ReadRegistryCandidate(path)
		if !ok {
			unknown("unreadable")
			continue
		}

		var wire registryFile
		if err := json.Unmarshal(data, &wire); err != nil {
			unknown("undecodable")
			continue
		}

		if wire.PID == 0 || wire.SessionID == "" || wire.ProcStart == "" || wire.Inbox == "" {
			unknown("missing required field")
			continue
		}
		if wire.PID != expectedPID {
			unknown("pid does not match filename")
			continue
		}

		procStart, err := ParseProcStart(wire.ProcStart)
		if err != nil {
			unknown("procStart unparsable")
			continue
		}

		// Cheap liveness pre-check, before the (potentially expensive — up
		// to four `ps` forks on darwin) Info call below: a dead pid, or one
		// whose inbox socket is already gone, must never trigger Info. Dead
		// entries are the common case (every <pid>.json left behind by an
		// exited Claude Code session survives until the next cleanup), and
		// must stay a zero-fork, silent skip exactly as before D9. A
		// non-ENOENT stat error, though, proves nothing — unknown, not dead.
		if statErr := live.Stat(wire.Inbox); statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				diag.Dead++
			} else {
				unknown("inbox stat: " + statErr.Error())
			}
			continue
		}
		if !live.PidAlive(wire.PID) {
			diag.Dead++
			continue
		}

		// D9 proxy classification. When Info is set, it replaces StartTime
		// (one call per entry, not two) and also supplies Argv/ExePath so
		// IsProxyProcess can classify the entry. Fail closed: a process
		// that cannot be classified (Info errors, or returns an empty
		// Argv — e.g. a permission-denied /proc read, or a race where the
		// process exited between the PidAlive check above and here) must
		// never become a deliverable cc row, so it is unknown (not proven
		// dead — the pid IS alive, just unclassifiable) exactly like
		// before, but only warns once per pid (warnUnclassifiableOnce), so
		// a persistently unclassifiable process does not spam the log on
		// every poll.
		entryLive := live
		isProxy := false
		if live.Info != nil {
			info, infoErr := live.Info(wire.PID)
			if infoErr != nil || len(info.Argv) == 0 {
				warnUnclassifiableOnce(wire.PID, infoErr)
				unknown("unclassifiable process")
				continue
			}
			isProxy = IsProxyProcess(info)
			entryLive.StartTime = func(int) (time.Time, error) { return info.StartTime, nil }
		}

		// isLive's rule, inlined (spec §3.3): a start time that fails to
		// read is unknown (the pid is alive; nothing proves it dead); a
		// start time that reads but disagrees with procStart is confirmed
		// dead (this IS proof — the running process is not the one this
		// registry entry describes).
		startTime, err := entryLive.StartTime(wire.PID)
		if err != nil {
			unknown("start time unreadable")
			continue
		}
		if !startTime.Truncate(time.Second).Equal(procStart.Truncate(time.Second)) {
			diag.Dead++
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

	return entries, diag, nil
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

// isLive's rule (live iff Stat(inbox)==nil && PidAlive(pid) &&
// StartTime(pid) returns no error && the two instants are equal to the
// second) is now inlined in ReadRegistryDiag, split across the dead/unknown
// classes each check maps to (spec §3.3).

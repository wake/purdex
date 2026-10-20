package facts

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// sessionRescan is how often the session directory is rescanned even when its
// modification time did not move.
const sessionRescan = 30 * time.Second

// DefaultSessionsDir is ~/.claude/sessions.
func DefaultSessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "sessions")
}

// procClockTick is Linux's USER_HZ: the unit of the times in /proc/<pid>/stat.
const procClockTick = 100

// ProcStat is the part of /proc/<pid>/stat the reader uses.
type ProcStat struct {
	Comm         string
	PPID         int
	UTime, STime uint64 // clock ticks
	Start        uint64 // clock ticks since boot (field 22)
	RSSPages     uint64
}

// ParseProcStat reads one /proc/<pid>/stat line. The command name sits in
// parentheses and may hold spaces and parentheses itself, so the fields are
// counted from the last ')'.
func ParseProcStat(line string) (ProcStat, bool) {
	open := strings.IndexByte(line, '(')
	end := strings.LastIndexByte(line, ')')
	if open < 0 || end < open {
		return ProcStat{}, false
	}
	f := strings.Fields(line[end+1:])
	if len(f) < 22 { // state is f[0]; rss (field 24) is f[21]
		return ProcStat{}, false
	}
	num := func(i int) (uint64, bool) {
		v, err := strconv.ParseUint(f[i], 10, 64)
		return v, err == nil
	}
	ppid, ok1 := num(1)
	ut, ok2 := num(11)
	st, ok3 := num(12)
	start, ok4 := num(19)
	rss, err := strconv.ParseInt(f[21], 10, 64)
	if !ok1 || !ok2 || !ok3 || !ok4 || err != nil {
		return ProcStat{}, false
	}
	if rss < 0 {
		rss = 0
	}
	return ProcStat{Comm: line[open+1 : end], PPID: int(ppid), UTime: ut, STime: st, Start: start, RSSPages: uint64(rss)}, true
}

// ParseNULList splits a /proc/<pid>/cmdline or environ (NUL-terminated strings).
func ParseNULList(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(bytes.TrimSuffix(b, []byte{0})), "\x00")
}

// ParseBtime is the boot time (unix seconds) from /proc/stat.
func ParseBtime(stat string) (int64, bool) {
	for _, l := range strings.Split(stat, "\n") {
		if v, ok := strings.CutPrefix(l, "btime "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// ProcfsReader reads the process table from a /proc tree (root, "/proc" for the
// real one): stat for the figures, cmdline and environ on demand. Nothing
// forks. One value belongs to one goroutine.
//
// A pid names a process only while it lives. The uid comes from its status file, read between two reads of its stat; a row whose start time
// differs between them is dropped. Per-pid reads made later (cmdline, environ)
// are dropped when the pid's start time no longer matches the table row.
type ProcfsReader struct {
	root, sessDir string
	btime         int64
	pageSize      uint64

	prev     map[int]procfsPrev
	starts   map[int]uint64
	args     map[procfsKey]procfsArgs
	kinds    map[procfsKey]string
	sessions map[int]sessionFile
	sessAt   time.Time
	sessMod  time.Time
}

type procfsPrev struct {
	start, ticks uint64
	at           time.Time
}

// procfsArgs is a process's argv and the job marker found in its environment.
type procfsArgs struct {
	argv    []string
	job     string
	origin  string // the origin tag of the job mark (WWD_ORIGIN); "" when the mark has none
	session string // the ref of the session in the environment (SessionEnvKey); "" when none
}

type procfsKey struct {
	pid   int
	start uint64
}

// NewProcfsReader reads under root; sessionsDir is ~/.claude/sessions ("" turns
// session attribution off).
func NewProcfsReader(root, sessionsDir string) *ProcfsReader {
	return &ProcfsReader{
		root: root, sessDir: sessionsDir, pageSize: uint64(os.Getpagesize()),
		prev: map[int]procfsPrev{}, starts: map[int]uint64{},
		args: map[procfsKey]procfsArgs{}, kinds: map[procfsKey]string{},
	}
}

func (r *ProcfsReader) read(pid int, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(r.root, strconv.Itoa(pid), name))
}

func (r *ProcfsReader) stat(pid int) (ProcStat, bool) {
	b, err := r.read(pid, "stat")
	if err != nil {
		return ProcStat{}, false
	}
	return ParseProcStat(string(b))
}

// uidOf is the effective uid from the pid's status file. (The owner of the
// pid's directory is not it: the kernel shows a non-dumpable process, root's or
// not, under root's ownership.)
func (r *ProcfsReader) uidOf(pid int) (int, bool) {
	b, err := r.read(pid, "status")
	if err != nil {
		return 0, false
	}
	return ParseStatusUID(string(b))
}

// ParseStatusUID is the effective uid, the second number of the "Uid:" line of
// /proc/<pid>/status (real, effective, saved, filesystem).
func ParseStatusUID(status string) (int, bool) {
	for _, l := range strings.Split(status, "\n") {
		if v, ok := strings.CutPrefix(l, "Uid:"); ok {
			f := strings.Fields(v)
			if len(f) < 2 {
				return 0, false
			}
			n, err := strconv.ParseUint(f[1], 10, 32)
			return int(n), err == nil
		}
	}
	return 0, false
}

// Snapshot reads the table and each process's CPU share since the previous
// snapshot (0 for a process seen the first time).
func (r *ProcfsReader) Snapshot(now time.Time) (ProcSnapshot, ProcContext, error) {
	ents, err := os.ReadDir(r.root)
	if err != nil {
		return ProcSnapshot{}, ProcContext{}, fmt.Errorf("read %s: %w", r.root, err)
	}
	if r.btime == 0 {
		if b, err := os.ReadFile(filepath.Join(r.root, "stat")); err == nil {
			r.btime, _ = ParseBtime(string(b))
		}
	}
	r.refreshSessions(now)
	cur := make(map[int]procfsPrev, len(ents))
	r.starts = make(map[int]uint64, len(ents))
	snap := ProcSnapshot{Procs: make([]ProcRec, 0, len(ents))}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 || !e.IsDir() {
			continue
		}
		snap.Total++ // counted in this pass, before any read that can fail
		a, ok := r.stat(pid)
		if !ok {
			continue // exited between the listing and the read
		}
		uid, ok := r.uidOf(pid)
		if !ok {
			continue
		}
		if b, ok := r.stat(pid); !ok || b.Start != a.Start {
			continue // the pid changed hands while its uid was read
		}
		ticks := a.UTime + a.STime
		p := ProcRec{
			PID: pid, PPID: a.PPID, UID: uid, Name: a.Comm,
			FootprintBytes: a.RSSPages * r.pageSize, MemKind: "rss", Start: r.startTime(a.Start),
		}
		if old, ok := r.prev[pid]; ok && old.start == a.Start && now.After(old.at) && ticks >= old.ticks {
			secs := float64(ticks-old.ticks) / procClockTick
			p.CPUPct = 100 * secs / now.Sub(old.at).Seconds()
		}
		cur[pid] = procfsPrev{start: a.Start, ticks: ticks, at: now}
		r.starts[pid] = a.Start
		snap.Procs = append(snap.Procs, p)
	}
	r.prev = cur
	r.dropStale()
	return snap, ProcContext{JobOf: r.jobOf, OriginOf: r.originOf, SessionOf: r.sessionOf, EnvSessionOf: r.envSessionOf, ArgsOf: r.argsOf, KindOf: r.kindOf, Fresh: r.fresh}, nil
}

// BootTime is when the host booted, from the btime line of /proc/stat (read once, then kept).
func (r *ProcfsReader) BootTime() (time.Time, bool) {
	if r.btime == 0 {
		if b, err := os.ReadFile(filepath.Join(r.root, "stat")); err == nil {
			r.btime, _ = ParseBtime(string(b))
		}
	}
	if r.btime == 0 {
		return time.Time{}, false
	}
	return time.Unix(r.btime, 0), true
}

// startTime is the wall-clock time of a start in clock ticks since boot; zero while the boot time is not known.
func (r *ProcfsReader) startTime(ticks uint64) time.Time {
	if r.btime == 0 {
		return time.Time{}
	}
	return time.Unix(r.btime, 0).Add(time.Duration(ticks) * (time.Second / procClockTick))
}

// fresh lists the processes as they are now from their stat files alone (pid, parent, name, start): no uid, no figures, no argv, and none
// of the reader's state touched. A process that exits while it is read is left out.
func (r *ProcfsReader) fresh() ([]ProcRec, error) {
	ents, err := os.ReadDir(r.root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", r.root, err)
	}
	out := make([]ProcRec, 0, len(ents))
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 || !e.IsDir() {
			continue
		}
		if s, ok := r.stat(pid); ok {
			out = append(out, ProcRec{PID: pid, PPID: s.PPID, Name: s.Comm, Start: r.startTime(s.Start)})
		}
	}
	return out, nil
}

// stillSame reports whether pid still names the process that began at start.
func (r *ProcfsReader) stillSame(pid int, start uint64) bool {
	s, ok := r.stat(pid)
	return ok && s.Start == start
}

// loadArgs reads argv and the job marker of pid once per process. A read whose
// pid changed hands meanwhile is dropped and not cached. An environment the
// reader may not read (another user's process) leaves the marker empty.
func (r *ProcfsReader) loadArgs(pid int) procfsArgs {
	start, known := r.starts[pid]
	if !known {
		return procfsArgs{}
	}
	key := procfsKey{pid, start}
	if a, ok := r.args[key]; ok {
		return a
	}
	var a procfsArgs
	if b, err := r.read(pid, "cmdline"); err == nil {
		a.argv = ParseNULList(b)
	}
	if b, err := r.read(pid, "environ"); err == nil {
		env := ParseNULList(b)
		a.origin, a.job, _ = MarkFromEnv(env) // the environment itself is never kept
		if id, ok := SessionIDFromEnv(env); ok {
			a.session = sessionRef(id) // only the ref is kept
		}
	}
	if !r.stillSame(pid, start) {
		return procfsArgs{}
	}
	r.args[key] = a
	return a
}

func (r *ProcfsReader) argsOf(pid int) []string { return r.loadArgs(pid).argv }

func (r *ProcfsReader) jobOf(pid int) (string, bool) {
	j := r.loadArgs(pid).job
	return j, j != ""
}

func (r *ProcfsReader) originOf(pid int) (string, bool) {
	o := r.loadArgs(pid).origin
	return o, o != ""
}

func (r *ProcfsReader) envSessionOf(pid int) (string, bool) {
	s := r.loadArgs(pid).session
	return s, s != ""
}

func (r *ProcfsReader) kindOf(p ProcRec) string {
	start, known := r.starts[p.PID]
	if !known {
		return ""
	}
	key := procfsKey{p.PID, start}
	if k, ok := r.kinds[key]; ok {
		return k
	}
	k := residentKind(p, r.loadArgs(p.PID).argv)
	if _, cached := r.args[key]; cached {
		r.kinds[key] = k
	}
	return k
}

// sessionOf: a session file names its process by pid and start time (to the
// second); the boot time is read in whole seconds too, so one second either way
// is the same start.
func (r *ProcfsReader) sessionOf(pid int) (string, bool) {
	s, ok := r.sessions[pid]
	start, known := r.starts[pid]
	if !ok || !known || r.btime == 0 {
		return "", false
	}
	sec := r.btime + int64(start/procClockTick)
	for _, d := range []int64{0, -1, 1} {
		if s.matchesStart(sec + d) {
			return sessionRef(s.sessionID), true
		}
	}
	return "", false
}

// refreshSessions rescans the session directory when its modification time
// moved or, as a backstop, every sessionRescan.
func (r *ProcfsReader) refreshSessions(now time.Time) {
	if r.sessDir == "" {
		return
	}
	var mod time.Time
	if st, err := os.Stat(r.sessDir); err == nil {
		mod = st.ModTime()
	}
	if r.sessions != nil && mod.Equal(r.sessMod) && now.Sub(r.sessAt) < sessionRescan {
		return
	}
	r.sessions, r.sessAt, r.sessMod = scanSessions(r.sessDir), now, mod
}

func (r *ProcfsReader) dropStale() {
	for k := range r.args {
		if s, ok := r.starts[k.pid]; !ok || s != k.start {
			delete(r.args, k)
			delete(r.kinds, k)
		}
	}
}

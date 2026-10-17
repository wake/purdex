//go:build darwin

package facts

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// ProcReader reads the process table with no fork: kern.proc.all for the list,
// proc_pid_rusage (through purego, no cgo) for footprint and CPU time,
// kern.procargs2 for argv and the job marker. One value belongs to one
// goroutine.
//
// A pid is only a name for a process while it lives. Every per-pid read (rusage,
// procargs2) is followed by a second look at the pid's full start time
// (seconds and microseconds) and is dropped when it no longer matches the table
// row, so data of a process that reused the pid is never put on another's record.
type ProcReader struct {
	rusage func(pid int32, flavor int32, buffer unsafe.Pointer) int32
	// seams (the real ones are set by NewProcReader; tests stage a pid reused
	// between the table and a per-pid read)
	table    func() ([]tableRow, error)
	startOf  func(pid int) (ident, bool)
	procArgs func(pid int) ([]byte, error)

	numer, denom uint64
	buf          [512]byte
	prev         map[int]cpuPrev
	args         map[argKey]procArgs
	sessDir      string
	sessions     map[int]sessionFile
	sessAt       time.Time
	sessMod      time.Time
	idents       map[int]ident
}

// ident is the kernel's full p_starttime: seconds alone do not tell two
// processes apart, a pid can be reused within the same second.
type ident struct {
	sec  int64
	usec int32
}

type cpuPrev struct {
	id ident
	ns uint64
	at time.Time
}

type argKey struct {
	pid int
	id  ident
}

type procArgs struct {
	argv []string
	job  string
}

const (
	rusageInfoV4   = 4
	rusageOffUser  = 16 // ri_user_time (mach absolute time)
	rusageOffSys   = 24 // ri_system_time
	rusageOffFoot  = 72 // ri_phys_footprint
	procTableTries = 4
	sessionRescan  = 30 * time.Second
)

// NewProcReader loads libSystem's proc_pid_rusage and mach_timebase_info.
// sessionsDir is ~/.claude/sessions ("" disables session attribution).
func NewProcReader(sessionsDir string) (r *ProcReader, err error) {
	defer func() {
		if p := recover(); p != nil { // purego panics on a symbol it cannot bind
			r, err = nil, fmt.Errorf("facts: libSystem: %v", p)
		}
	}()
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("facts: dlopen libSystem: %w", err)
	}
	r = &ProcReader{
		prev: map[int]cpuPrev{}, args: map[argKey]procArgs{}, sessDir: sessionsDir, idents: map[int]ident{},
		table: readTable, startOf: kernStartOf,
		procArgs: func(pid int) ([]byte, error) { return unix.SysctlRaw("kern.procargs2", pid) },
	}
	purego.RegisterLibFunc(&r.rusage, lib, "proc_pid_rusage")
	var timebase func(info *[2]uint32) int32
	purego.RegisterLibFunc(&timebase, lib, "mach_timebase_info")
	var tb [2]uint32
	if timebase(&tb) != 0 || tb[0] == 0 || tb[1] == 0 {
		return nil, errors.New("facts: mach_timebase_info failed")
	}
	r.numer, r.denom = uint64(tb[0]), uint64(tb[1])
	return r, nil
}

// kinfo_proc offsets (the kernel's struct, as x/sys lays it out).
var (
	kp           unix.KinfoProc
	offPID       = unsafe.Offsetof(kp.Proc) + unsafe.Offsetof(kp.Proc.P_pid)
	offComm      = unsafe.Offsetof(kp.Proc) + unsafe.Offsetof(kp.Proc.P_comm)
	offStartSec  = unsafe.Offsetof(kp.Proc) + unsafe.Offsetof(kp.Proc.P_starttime) + unsafe.Offsetof(kp.Proc.P_starttime.Sec)
	offStartUsec = unsafe.Offsetof(kp.Proc) + unsafe.Offsetof(kp.Proc.P_starttime) + unsafe.Offsetof(kp.Proc.P_starttime.Usec)
	offPPID      = unsafe.Offsetof(kp.Eproc) + unsafe.Offsetof(kp.Eproc.Ppid)
	offUID       = unsafe.Offsetof(kp.Eproc) + unsafe.Offsetof(kp.Eproc.Ucred) + unsafe.Offsetof(kp.Eproc.Ucred.Uid)
	commLen      = len(kp.Proc.P_comm)
)

type tableRow struct {
	ProcRec
	id ident
}

func readTable() ([]tableRow, error) {
	var buf []byte
	var err error
	for range procTableTries {
		if buf, err = unix.SysctlRaw("kern.proc.all"); !errors.Is(err, unix.ENOMEM) {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}
	if len(buf)%unix.SizeofKinfoProc != 0 {
		return nil, fmt.Errorf("process table is %d bytes, not a multiple of %d", len(buf), unix.SizeofKinfoProc)
	}
	rows := make([]tableRow, 0, len(buf)/unix.SizeofKinfoProc)
	for rec := range slices.Chunk(buf, unix.SizeofKinfoProc) {
		pid := int(int32(binary.NativeEndian.Uint32(rec[offPID:])))
		if pid <= 0 {
			continue
		}
		rows = append(rows, tableRow{
			ProcRec: ProcRec{
				PID:  pid,
				PPID: int(int32(binary.NativeEndian.Uint32(rec[offPPID:]))),
				UID:  int(binary.NativeEndian.Uint32(rec[offUID:])),
				Name: unix.ByteSliceToString(rec[offComm : offComm+uintptr(commLen)]),
			},
			id: ident{
				sec:  int64(binary.NativeEndian.Uint64(rec[offStartSec:])),
				usec: int32(binary.NativeEndian.Uint32(rec[offStartUsec:])),
			},
		})
	}
	return rows, nil
}

// kernStartOf is the full start time of the process that holds pid right now.
func kernStartOf(pid int) (ident, bool) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || k == nil || int(k.Proc.P_pid) != pid {
		return ident{}, false
	}
	return ident{sec: k.Proc.P_starttime.Sec, usec: k.Proc.P_starttime.Usec}, true
}

// stillSame reports whether pid still names the process of the table row.
func (r *ProcReader) stillSame(pid int, id ident) bool {
	now, ok := r.startOf(pid)
	return ok && now == id
}

// Snapshot reads the table and each process's footprint and CPU share since the
// previous snapshot (0 for a process seen the first time). The returned
// context's lookups read argv, the job marker and session files on demand.
func (r *ProcReader) Snapshot(now time.Time) (ProcSnapshot, ProcContext, error) {
	rows, err := r.table()
	if err != nil {
		return ProcSnapshot{}, ProcContext{}, err
	}
	r.refreshSessions(now)
	cur := make(map[int]cpuPrev, len(rows))
	r.idents = make(map[int]ident, len(rows))
	snap := ProcSnapshot{Procs: make([]ProcRec, 0, len(rows))}
	for _, row := range rows {
		p := row.ProcRec
		r.idents[p.PID] = row.id
		// footprint and CPU are kept only when the pid still names this row's process
		if foot, ns, ok := r.usage(p.PID); ok && r.stillSame(p.PID, row.id) {
			p.FootprintBytes = foot
			cur[p.PID] = cpuPrev{id: row.id, ns: ns, at: now}
			if old, ok := r.prev[p.PID]; ok && old.id == row.id && now.After(old.at) && ns >= old.ns {
				p.CPUPct = 100 * float64(ns-old.ns) / float64(now.Sub(old.at).Nanoseconds())
			}
		}
		snap.Procs = append(snap.Procs, p)
	}
	r.prev = cur
	r.dropStaleArgs()
	return snap, ProcContext{JobOf: r.jobOf, SessionOf: r.sessionOf, ArgsOf: r.argsOf}, nil
}

// usage is the footprint and the CPU time (user + system, nanoseconds) of pid.
func (r *ProcReader) usage(pid int) (foot, ns uint64, ok bool) {
	r.buf = [512]byte{}
	if r.rusage(int32(pid), rusageInfoV4, unsafe.Pointer(&r.buf[0])) != 0 {
		return 0, 0, false // gone, or another user's process (EPERM)
	}
	user := binary.LittleEndian.Uint64(r.buf[rusageOffUser:])
	sys := binary.LittleEndian.Uint64(r.buf[rusageOffSys:])
	return binary.LittleEndian.Uint64(r.buf[rusageOffFoot:]), (user + sys) * r.numer / r.denom, true
}

// loadArgs reads argv and the job marker of pid once per process (cached by pid
// and full start time). A read whose pid changed hands meanwhile is dropped and
// not cached: empty, so the caller attributes nothing from it.
func (r *ProcReader) loadArgs(pid int) procArgs {
	id, known := r.idents[pid]
	if !known {
		return procArgs{}
	}
	key := argKey{pid, id}
	if a, ok := r.args[key]; ok {
		return a
	}
	var a procArgs
	if buf, err := r.procArgs(pid); err == nil {
		if !r.stillSame(pid, id) {
			return procArgs{}
		}
		if argv, env, ok := parseProcArgs(buf); ok {
			a.argv = argv
			a.job, _ = JobFromEnv(env) // the environment itself is never kept
		}
	}
	r.args[key] = a
	return a
}

func (r *ProcReader) argsOf(pid int) []string { return r.loadArgs(pid).argv }

func (r *ProcReader) jobOf(pid int) (string, bool) {
	j := r.loadArgs(pid).job
	return j, j != ""
}

func (r *ProcReader) sessionOf(pid int) (string, bool) {
	s, ok := r.sessions[pid]
	id, known := r.idents[pid]
	if !ok || !known || !s.matchesStart(id.sec) {
		return "", false
	}
	return sessionRef(s.sessionID), true
}

// refreshSessions rescans the session directory when its modification time
// moved (a session file is created when a session starts) or, as a backstop,
// every sessionRescan.
func (r *ProcReader) refreshSessions(now time.Time) {
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

// dropStaleArgs forgets the cached argv of processes that are gone.
func (r *ProcReader) dropStaleArgs() {
	for k := range r.args {
		if id, ok := r.idents[k.pid]; !ok || id != k.id {
			delete(r.args, k)
		}
	}
}

// DefaultSessionsDir is ~/.claude/sessions.
func DefaultSessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "sessions")
}

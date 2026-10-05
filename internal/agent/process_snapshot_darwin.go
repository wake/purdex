package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// kernProcPid and kernProcArgs2 are the per-PID sysctls behind Read. Tests
// swap them to stage a PID reused after the snapshot, or an argument area the
// parser has to refuse.
var kernProcPid = func(pid int) (*unix.KinfoProc, error) {
	return unix.SysctlKinfoProc("kern.proc.pid", pid)
}

var kernProcArgs2 = func(pid int) ([]byte, error) {
	return unix.SysctlRaw("kern.procargs2", pid)
}

// kernProcAll is one read of the whole process table: a size query, then the
// read, with no retry; a table that grew in between comes back as ENOMEM.
// Tests swap it to stage a table that keeps growing.
var kernProcAll = func() ([]byte, error) {
	return unix.SysctlRaw("kern.proc.all")
}

// procTableAttempts caps the reads of a table that keeps growing. The
// snapshot runs in a goroutine the inventory may abandon at its deadline, so
// it has to end on its own; x/sys's SysctlKinfoProcSlice retries ENOMEM
// without limit, which under a sustained fork storm may be never.
const procTableAttempts = 4

func readProcTable() ([]byte, error) {
	var err error
	for range procTableAttempts {
		var buf []byte
		if buf, err = kernProcAll(); !errors.Is(err, unix.ENOMEM) {
			return buf, err
		}
	}
	return nil, fmt.Errorf("process table still growing after %d reads: %w", procTableAttempts, err)
}

// rawProc is the part of one kinfo_proc record the snapshot keeps.
type rawProc struct {
	pid, ppid int
	sec       int64
	usec      int32
}

// parseKinfoProcs reads the records of a kern.proc.all buffer field by field.
// Casting the bytes to []unix.KinfoProc would do the same, but the struct
// holds pointers (P_vmspace among them), and kernel bytes are not Go
// pointers. The offsets are x/sys's own layout of the struct, which is the
// kernel's.
func parseKinfoProcs(buf []byte) ([]rawProc, error) {
	if len(buf)%unix.SizeofKinfoProc != 0 {
		return nil, fmt.Errorf("process table is %d bytes, not a multiple of %d", len(buf), unix.SizeofKinfoProc)
	}
	var k unix.KinfoProc
	const (
		pidOff   = unsafe.Offsetof(k.Proc) + unsafe.Offsetof(k.Proc.P_pid)
		ppidOff  = unsafe.Offsetof(k.Eproc) + unsafe.Offsetof(k.Eproc.Ppid)
		startOff = unsafe.Offsetof(k.Proc) + unsafe.Offsetof(k.Proc.P_starttime)
		secOff   = startOff + unsafe.Offsetof(k.Proc.P_starttime.Sec)
		usecOff  = startOff + unsafe.Offsetof(k.Proc.P_starttime.Usec)
	)
	procs := make([]rawProc, 0, len(buf)/unix.SizeofKinfoProc)
	for rec := range slices.Chunk(buf, unix.SizeofKinfoProc) {
		procs = append(procs, rawProc{
			pid:  int(int32(binary.NativeEndian.Uint32(rec[pidOff:]))),
			ppid: int(int32(binary.NativeEndian.Uint32(rec[ppidOff:]))),
			sec:  int64(binary.NativeEndian.Uint64(rec[secOff:])),
			usec: int32(binary.NativeEndian.Uint32(rec[usecOff:])),
		})
	}
	return procs, nil
}

// procIdentity is the kernel's full p_starttime. Seconds alone do not tell
// two processes apart: a PID can be reused within the same second.
type procIdentity struct {
	sec  int64
	usec int32
}

// snapshotProcessesPlatform reads the whole table with one sysctl, no fork.
// PPID and start time are the same kinfo_proc fields ps prints for ppid and
// lstart.
func snapshotProcessesPlatform(ctx context.Context) (map[int]*snapshotEntry, error) {
	// The sysctl cannot be cancelled, so ctx is only honoured up front: a
	// pass whose deadline already passed takes no snapshot.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	buf, err := readProcTable()
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}
	recs, err := parseKinfoProcs(buf)
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}
	procs := make(map[int]*snapshotEntry, len(recs))
	for _, r := range recs {
		if r.pid <= 0 {
			continue
		}
		procs[r.pid] = newDarwinEntry(r.pid, r.ppid, r.sec, r.usec)
	}
	return procs, nil
}

// newDarwinEntry builds the entry for one kinfo_proc row. ps prints lstart
// from p_starttime to the second, in the local zone, and frames store that
// text. StartTime is then that text parsed exactly as the per-PID reader
// parses ps's, not the exact instant: in a fall-back hour the text names two
// instants and the parse may pick the other one, and the registry compares
// StartTime with what that parse gave. The exact instant is kept apart, as
// the identity the re-check compares.
func newDarwinEntry(pid, ppid int, sec int64, usec int32) *snapshotEntry {
	e := &snapshotEntry{
		ppid:     ppid,
		lstart:   time.Unix(sec, 0).Format(psLstartLayout),
		identity: procIdentity{sec: sec, usec: usec},
	}
	e.start, e.startErr = parseLstart(pid, e.lstart)
	return e
}

// procArgsPlatform reads ExePath / Argv for a PID the snapshot saw, then
// checks that the PID still names the process the snapshot saw. Arguments are
// read later than the table, and a PID reused in between would otherwise put
// one process's argv next to another's PPID and start time.
func procArgsPlatform(pid int, e *snapshotEntry) (string, []string, error) {
	exePath, argv, err := readCommArgs(pid)
	// The check runs even when the read failed: a process that exited after
	// the snapshot also fails the read, and the caller has to be able to tell
	// "the snapshot's process went away" from "this process is unreadable".
	kp, kerr := kernProcPid(pid)
	if kerr != nil || kp == nil {
		return "", nil, fmt.Errorf("pid %d is gone: %w", pid, ErrProcessChanged)
	}
	if now := (procIdentity{sec: kp.Proc.P_starttime.Sec, usec: kp.Proc.P_starttime.Usec}); now != e.identity {
		return "", nil, fmt.Errorf("pid %d now started at %d.%06d, snapshot saw %d.%06d: %w",
			pid, now.sec, now.usec, e.identity.sec, e.identity.usec, ErrProcessChanged)
	}
	if err != nil {
		return "", nil, err
	}
	return exePath, argv, nil
}

// readCommArgs answers what readCommArgsPS would, without forking when that
// is provably the same answer. ps prints printable ASCII verbatim (measured);
// every other byte goes through an escaping that is not one rule (TAB becomes
// \011, DEL ^?, 0x80 M^@, some invisible Unicode is rewritten, CJK and emoji
// are not), so it is not re-implemented here: any such argv, any argument
// area the kernel refuses (another user's process, a zombie) and any buffer
// the parser rejects go to ps itself.
func readCommArgs(pid int) (string, []string, error) {
	if buf, err := kernProcArgs2(pid); err == nil {
		if args, ok := parseProcArgs(buf); ok && printableASCII(args) {
			// ps's comm is argv[0] and its args are the strings joined by a
			// space; the same normalisation then turns them into fields.
			exePath, err := exePathFromComm(pid, args[0])
			if err == nil {
				argv, err := argvFromArgs(pid, strings.Join(args, " "))
				if err == nil {
					return exePath, argv, nil
				}
			}
			// An argv the normalisation refuses is rare enough that ps gets
			// the last word on it, errors included.
		}
	}
	return readCommArgsPS(pid)
}

func printableASCII(args []string) bool {
	for _, a := range args {
		for i := 0; i < len(a); i++ {
			if a[i] < 0x20 || a[i] > 0x7e {
				return false
			}
		}
	}
	return true
}

// parseProcArgs reads the argument strings out of a kern.procargs2 buffer
// the way ps does: argc (a native int), the exec path up to its NUL, then
// every NUL after it, then argc NUL-terminated strings. Skipping every NUL
// means an empty argv[0] is taken for padding and counting starts at argv[1],
// running one string into the environment; ps does exactly that (measured),
// and matching it keeps comm / args equal to ps's.
//
// ok is false for any buffer ps would not print as-is (no exec path
// terminator, nothing after the padding, fewer strings than argc), and every
// index is bounded by the buffer, so a short or corrupt buffer sends the
// caller to the ps fallback instead of a panic or a guess.
func parseProcArgs(buf []byte) ([]string, bool) {
	if len(buf) < 4 {
		return nil, false
	}
	argc := int(int32(binary.NativeEndian.Uint32(buf)))
	if argc <= 0 {
		return nil, false
	}
	area := buf[4:]
	i := bytes.IndexByte(area, 0)
	if i < 0 {
		return nil, false
	}
	for i < len(area) && area[i] == 0 {
		i++
	}
	var args []string
	for len(args) < argc {
		n := bytes.IndexByte(area[i:], 0)
		if n < 0 {
			return nil, false
		}
		args = append(args, string(area[i:i+n]))
		i += n + 1
	}
	return args, true
}

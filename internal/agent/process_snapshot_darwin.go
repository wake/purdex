package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

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

// snapshotProcessesPlatform reads the whole table with one sysctl, no fork.
// PPID and start time are the same kinfo_proc fields ps prints for ppid and
// lstart.
func snapshotProcessesPlatform(ctx context.Context) (map[int]*snapshotEntry, error) {
	// The sysctl cannot be cancelled, so ctx is only honoured up front: a
	// pass whose deadline already passed takes no snapshot.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}
	procs := make(map[int]*snapshotEntry, len(kps))
	for i := range kps {
		pid := int(kps[i].Proc.P_pid)
		if pid <= 0 {
			continue
		}
		// ps prints lstart from this field to the second, in the local zone.
		// Frames store that text and the registry compares StartTime to the
		// second, so the snapshot keeps exactly that precision and zone:
		// whole seconds in time.Local, which is also what parsing lstart in
		// time.Local gives the per-PID reader.
		start := time.Unix(kps[i].Proc.P_starttime.Sec, 0)
		procs[pid] = &snapshotEntry{
			ppid:   int(kps[i].Eproc.Ppid),
			lstart: start.Format(psLstartLayout),
			start:  start,
		}
	}
	return procs, nil
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
	switch {
	case kerr != nil || kp == nil:
		return "", nil, fmt.Errorf("pid %d is gone: %w", pid, ErrProcessChanged)
	case kp.Proc.P_starttime.Sec != e.start.Unix():
		return "", nil, fmt.Errorf("pid %d now started %s, snapshot saw %s: %w",
			pid, time.Unix(kp.Proc.P_starttime.Sec, 0).Format(psLstartLayout), e.lstart, ErrProcessChanged)
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

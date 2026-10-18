package agent

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

// kernelProc reads pid's parent and start second from one kern.proc.pid sysctl, the same kinfo_proc fields ps prints for
// ppid and lstart. It is a single fixed-size read: x/sys's SysctlKinfoProcSlice retries ENOMEM without limit, this does not
// use it. A pid that is gone (the sysctl answers no record, or another pid's) is ESRCH.
func kernelProc(pid int) (ppid int, startSec int64, err error) {
	kp, err := kernProcPid(pid)
	if err != nil {
		return 0, 0, err
	}
	if kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, 0, fmt.Errorf("pid %d: %w", pid, unix.ESRCH)
	}
	return int(kp.Eproc.Ppid), kp.Proc.P_starttime.Sec, nil
}

// quickPPIDArgs is pid's parent and its argument fields as `ps -p pid -o ppid= -o args=` prints them, read by sysctl. It answers
// only what readCommArgs answers without ps (printable ASCII; a buffer the parser accepts), so the fields are the same ones
// the ps reader splits; anything else is an error and the caller asks ps. The parent and the full start time (seconds and
// microseconds, as the snapshot's re-check compares) are read again after the argument area: a pid that was reused in between,
// even within the same second, would put one process's args next to another's parent.
func quickPPIDArgs(pid int) (int, []string, error) {
	ppid, id, err := kernelProcIdentity(pid)
	if err != nil {
		return 0, nil, err
	}
	buf, err := kernProcArgs2(pid)
	if err != nil {
		return 0, nil, err
	}
	args, ok := parseProcArgs(buf)
	if !ok || !printableASCII(args) {
		return 0, nil, errQuickUnsupported
	}
	argv, err := argvFromArgs(pid, strings.Join(args, " "))
	if err != nil {
		return 0, nil, err
	}
	ppid2, id2, err := kernelProcIdentity(pid)
	if err != nil || ppid2 != ppid || id2 != id {
		return 0, nil, fmt.Errorf("pid %d changed while its arguments were read", pid)
	}
	return ppid, argv, nil
}

// kernelProcIdentity is kernelProc plus the start time's microseconds: the identity the snapshot re-check compares, fine enough to
// tell a pid that was reused within the same second.
func kernelProcIdentity(pid int) (ppid int, id procIdentity, err error) {
	kp, err := kernProcPid(pid)
	if err != nil {
		return 0, procIdentity{}, err
	}
	if kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, procIdentity{}, fmt.Errorf("pid %d: %w", pid, unix.ESRCH)
	}
	return int(kp.Eproc.Ppid), procIdentity{sec: kp.Proc.P_starttime.Sec, usec: kp.Proc.P_starttime.Usec}, nil
}

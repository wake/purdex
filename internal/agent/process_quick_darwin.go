package agent

import (
	"fmt"

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

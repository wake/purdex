//go:build darwin

package gate

import "golang.org/x/sys/unix"

// procTable reads every process: its parent and its start time (seconds, microseconds folded), so a pid that was
// reused is not taken for the process it was.
func procTable() (map[int]procInfo, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	out := make(map[int]procInfo, len(kps))
	for _, kp := range kps {
		pid := int(kp.Proc.P_pid)
		out[pid] = procInfo{PPID: int(kp.Eproc.Ppid), Start: kp.Proc.P_starttime.Sec*1_000_000 + int64(kp.Proc.P_starttime.Usec)}
	}
	return out, nil
}

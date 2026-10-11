//go:build !darwin

package agent

// kernelProc has no fork-free read off darwin (linux keeps ps for these two questions, as before).
func kernelProc(pid int) (ppid int, startSec int64, err error) {
	return 0, 0, errQuickUnsupported
}

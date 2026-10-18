//go:build !darwin

package agent

// kernelProc has no fork-free read off darwin (linux keeps ps for these two questions, as before).
func kernelProc(pid int) (ppid int, startSec int64, err error) {
	return 0, 0, errQuickUnsupported
}

// quickPPIDArgs has no fork-free read off darwin.
func quickPPIDArgs(pid int) (int, []string, error) {
	return 0, nil, errQuickUnsupported
}

//go:build darwin

package facts

import "golang.org/x/sys/unix"

// jobOfPID reads kern.procargs2 of pid (argc, the executable, argv, then the environment) and looks for the mark in the environment.
func jobOfPID(pid int) (string, bool) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", false
	}
	_, env, ok := parseProcArgs(buf)
	if !ok {
		return "", false
	}
	return JobFromEnv(env)
}

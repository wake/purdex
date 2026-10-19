//go:build !darwin

package facts

import (
	"os"
	"strconv"
)

// jobOfPID reads /proc/<pid>/environ (another user's process, or no /proc: not readable, so no mark).
func jobOfPID(pid int) (string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return "", false
	}
	return JobFromEnv(ParseNULList(b))
}

//go:build !darwin

package facts

import (
	"fmt"
	"os"
	"strings"
)

// ProcIdent identifies the process that holds pid right now by its start time in
// clock ticks since boot (field 22 of /proc/<pid>/stat), so a later process that
// reuses the pid is told apart. ok is false when the pid is not running.
func ProcIdent(pid int) (string, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", false
	}
	// the command name is in parentheses and may hold spaces: count fields after the last ')'
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", false
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 20 { // field 22 overall is index 19 after the name and state
		return "", false
	}
	return fields[19], true
}

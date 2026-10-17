//go:build darwin

package facts

import "fmt"

// ProcIdent identifies the process that holds pid right now by its full start
// time ("sec.usec"), so a later process that reuses the pid is told apart. ok is
// false when the pid is not running.
func ProcIdent(pid int) (string, bool) {
	id, ok := kernStartOf(pid)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d.%06d", id.sec, id.usec), true
}

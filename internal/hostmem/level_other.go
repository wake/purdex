//go:build !darwin

package hostmem

import "errors"

// LevelSysctl is the kernel's memory pressure level: 1 normal, 2 warning, 4 critical (macOS only).
const LevelSysctl = "kern.memorystatus_vm_pressure_level"

// Level has nothing to read elsewhere: the callers treat the error as an unreadable pressure.
func Level() (int, error) { return 0, errors.New("the memory pressure sysctl exists only on macOS") }

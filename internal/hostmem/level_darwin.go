//go:build darwin

package hostmem

import "golang.org/x/sys/unix"

// LevelSysctl is the kernel's memory pressure level: 1 normal, 2 warning, 4 critical.
const LevelSysctl = "kern.memorystatus_vm_pressure_level"

// Level reads the pressure level from the kernel (no fork).
func Level() (int, error) {
	v, err := unix.SysctlUint32(LevelSysctl)
	return int(v), err
}

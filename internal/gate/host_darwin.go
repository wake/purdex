//go:build darwin

package gate

import "golang.org/x/sys/unix"

// The kernel's own numbers (hw.memsize, kern.memorystatus_level, kern.memorystatus_vm_pressure_level) exist on macOS.

func sysctlUint64(name string) (uint64, error) { return unix.SysctlUint64(name) }
func sysctlUint32(name string) (uint32, error) { return unix.SysctlUint32(name) }

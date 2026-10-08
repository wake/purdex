//go:build darwin

package resources

import "golang.org/x/sys/unix"

func osSysctlRaw(name string) ([]byte, error) { return unix.SysctlRaw(name) }
func osSysctlU32(name string) (uint32, error) { return unix.SysctlUint32(name) }
func osSysctlU64(name string) (uint64, error) { return unix.SysctlUint64(name) }

// NewSampler returns the real sampler: sysctl for the load, cpu count,
// memory size and pressure, plus one vm_stat and one ps fork per sample.
func NewSampler() Sampler { return &sysSampler{} }

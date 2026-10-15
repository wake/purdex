//go:build !darwin

package hostmem

import "errors"

// SystemSources off macOS: nothing to read (the fleet's own host is a macOS daemon; other hosts are probed over SSH).
func SystemSources() FastSources {
	no := errors.New("hostmem: the fast reading exists only on macOS")
	return FastSources{
		MachErr:  ErrNoMach,
		Level:    func() (int, error) { return 0, no },
		Load1:    func() (float64, error) { return 0, no },
		NCPU:     func() (int, error) { return 0, no },
		PageSize: func() (uint64, error) { return 0, no },
		MemBytes: func() (uint64, error) { return 0, no },
	}
}

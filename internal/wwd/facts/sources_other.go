//go:build !darwin

package facts

import (
	"errors"
	"time"
)

var errUnsupported = errors.New("facts: no host reads on this platform yet (Linux arrives in WWD-1c)")

// SystemSources on a platform without readers: every read fails, so a wwd built
// there still starts and reports the failure.
func SystemSources() Sources {
	return Sources{
		Now:   time.Now,
		Tools: systemTools(),
		CPU:   func() (RawCPU, error) { return RawCPU{}, errUnsupported },
		Mem:   func() (RawMem, error) { return RawMem{}, errUnsupported },
		Disks: func() ([]RawDisk, error) { return nil, errUnsupported },
	}
}

// SystemSourcesWith is SystemSources; the options are for macOS.
func SystemSourcesWith(SourceOptions) Sources { return SystemSources() }

//go:build !darwin

package hostmem

import "errors"

// ErrNoMach is what NewMach says off macOS: there are no Mach calls to make.
var ErrNoMach = errors.New("hostmem: the Mach host calls exist only on macOS")

// Mach is not available here; every method says so.
type Mach struct{}

// VMCounters are what host_statistics64 gives that a reading uses.
type VMCounters struct {
	Free, Inactive, Speculative uint64
	Pageouts                    uint64
	Decompressions              uint64
	Active, Wired, External     uint64
}

func NewMach() (*Mach, error)                         { return nil, ErrNoMach }
func (*Mach) CPUTicks() (busy, total uint64, e error) { return 0, 0, ErrNoMach }
func (*Mach) VM() (VMCounters, error)                 { return VMCounters{}, ErrNoMach }

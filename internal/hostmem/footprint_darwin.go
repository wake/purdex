//go:build darwin

package hostmem

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

// A process's physical footprint is the figure macOS itself shows as a process's memory (Activity Monitor's "Memory", top's MEM):
// the pages the process owns, compressed ones included. Its resident size leaves the compressed pages out, so under memory pressure
// (when the system compresses the big processes) the biggest ones have a small resident size (#3057). proc_pid_rusage with the
// RUSAGE_INFO_V4 flavor returns it as ri_phys_footprint. Called through purego like the Mach calls: no cgo, no fork.
const (
	rusageInfoV4       = 4
	rusageBufSize      = 512 // sizeof(struct rusage_info_v4) is below this on every release that has the flavor
	rusageOffFootprint = 72  // rusage_info_v4: ri_uuid[16] @0, then u64 user_time @16, system_time @24, pkg_idle_wkups @32, interrupt_wkups @40, pageins @48, wired_size @56, resident_size @64, phys_footprint @72
)

// Footprinter reads the footprint of a process by pid. One value is safe for one goroutine at a time.
type Footprinter struct {
	rusage func(pid int32, flavor int32, buffer unsafe.Pointer) int32
	buf    [rusageBufSize]byte
}

// NewFootprinter loads libSystem and binds proc_pid_rusage. An error means the footprint is not to be had: the caller falls back to the
// resident size.
func NewFootprinter() (f *Footprinter, err error) {
	defer func() { // purego panics on a symbol it cannot bind
		if r := recover(); r != nil {
			f, err = nil, fmt.Errorf("hostmem: libSystem: %v", r)
		}
	}()
	machOnce.Do(func() { machLib, machErr = purego.Dlopen(libSystemPath, purego.RTLD_NOW|purego.RTLD_GLOBAL) })
	if machErr != nil {
		return nil, fmt.Errorf("hostmem: dlopen libSystem: %w", machErr)
	}
	f = &Footprinter{}
	purego.RegisterLibFunc(&f.rusage, machLib, "proc_pid_rusage")
	if f.rusage == nil {
		return nil, errors.New("hostmem: proc_pid_rusage not found")
	}
	return f, nil
}

// Of is the physical footprint of pid in bytes. An error when the process is gone, or is not one this user may ask about (another
// user's: EPERM), or the answer is empty.
func (f *Footprinter) Of(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("hostmem: footprint of pid %d", pid)
	}
	f.buf = [rusageBufSize]byte{}
	if rc := f.rusage(int32(pid), rusageInfoV4, unsafe.Pointer(&f.buf[0])); rc != 0 {
		return 0, fmt.Errorf("hostmem: proc_pid_rusage(%d) = %d", pid, rc)
	}
	v := binary.LittleEndian.Uint64(f.buf[rusageOffFootprint:])
	if v == 0 {
		return 0, fmt.Errorf("hostmem: pid %d has no footprint", pid)
	}
	return v, nil
}

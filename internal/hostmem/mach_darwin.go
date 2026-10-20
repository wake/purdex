//go:build darwin

package hostmem

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The two Mach calls the 1 s reading needs and no sysctl carries: the whole host's CPU ticks (host_processor_info) and the
// paging counters with the page counts (host_statistics64). They are called through purego, which loads libSystem at run time
// with no cgo: the daemon's build does not change (CGO_ENABLED=0, both architectures). Nothing else of libSystem is touched.

const (
	libSystemPath        = "/usr/lib/libSystem.B.dylib"
	processorCPULoadInfo = 2 // PROCESSOR_CPU_LOAD_INFO
	hostVMInfo64         = 4 // HOST_VM_INFO64
	cpuStateCount        = 4 // user, system, idle, nice
	vmStatsWords         = 64
	vmOffFree            = 0  // vm_statistics64: free_count (natural_t)
	vmOffInactive        = 8  // inactive_count
	vmOffPageouts        = 40 // pageouts (uint64)
)

// vm_statistics64 offsets (xnu osfmk/mach/vm_statistics.h): free u32 @0, active u32 @4, inactive u32 @8, wire u32 @12,
// zero_fill u64 @16, reactivations @24, pageins @32, pageouts @40, faults @48, cow_faults @56, lookups @64, hits @72, purges @80,
// purgeable_count u32 @88, speculative_count u32 @92.
const vmOffSpeculative = 92

// active_count u32 @4, wire_count u32 @12, and external_page_count u32 @136 (after compressor_page_count @128 and throttled_count @132):
// the pages that back files. Reading it needs the reply to reach byte 140.
const (
	vmOffActive   = 4
	vmOffWired    = 12
	vmOffExternal = 136
	vmMinBytes    = vmOffExternal + 4
)

// decompressions u64 @96 (after speculative_count at 92, which pads to 96): the compressor's pages brought back since boot.
const vmOffDecompressions = 96

// Mach is the loaded Mach entry points. One value is safe for one goroutine at a time.
type Mach struct {
	hostSelf   func() uint32
	processor  func(host uint32, flavor int32, cpuCount *uint32, info *unsafe.Pointer, infoCnt *uint32) int32
	vmStats    func(host uint32, flavor int32, info *int32, count *uint32) int32
	vmDealloc  func(task uint32, addr unsafe.Pointer, size uintptr) int32
	taskSelf   *uint32
	host       uint32
	vmStatsBuf [vmStatsWords]int32
}

var (
	machOnce sync.Once
	machErr  error
	machLib  uintptr
)

// NewMach loads libSystem and binds the entry points. An error means the readings are not to be had: the caller degrades.
func NewMach() (m *Mach, err error) {
	defer func() { // purego panics on a symbol it cannot bind
		if r := recover(); r != nil {
			m, err = nil, fmt.Errorf("hostmem: libSystem: %v", r)
		}
	}()
	machOnce.Do(func() { machLib, machErr = purego.Dlopen(libSystemPath, purego.RTLD_NOW|purego.RTLD_GLOBAL) })
	if machErr != nil {
		return nil, fmt.Errorf("hostmem: dlopen libSystem: %w", machErr)
	}
	m = &Mach{}
	purego.RegisterLibFunc(&m.hostSelf, machLib, "mach_host_self")
	purego.RegisterLibFunc(&m.processor, machLib, "host_processor_info")
	purego.RegisterLibFunc(&m.vmStats, machLib, "host_statistics64")
	purego.RegisterLibFunc(&m.vmDealloc, machLib, "vm_deallocate")
	sym, err := purego.Dlsym(machLib, "mach_task_self_")
	if err != nil || sym == 0 {
		return nil, errors.New("hostmem: mach_task_self_ not found")
	}
	m.taskSelf = (*uint32)(*(*unsafe.Pointer)(unsafe.Pointer(&sym))) // a libSystem global, not Go memory
	m.host = m.hostSelf()
	if m.host == 0 {
		return nil, errors.New("hostmem: mach_host_self returned no port")
	}
	return m, nil
}

// CPUTicks are the ticks of every core summed: busy (user + system + nice) and total (those and idle), since boot.
func (m *Mach) CPUTicks() (busy, total uint64, err error) {
	var cpus, cnt uint32
	var info unsafe.Pointer
	if kr := m.processor(m.host, processorCPULoadInfo, &cpus, &info, &cnt); kr != 0 {
		return 0, 0, fmt.Errorf("host_processor_info: kern_return %d", kr)
	}
	defer m.vmDealloc(*m.taskSelf, info, uintptr(cnt)*4) // the array is the caller's to give back, every call
	if cpus == 0 || cnt < cpus*cpuStateCount {
		return 0, 0, fmt.Errorf("host_processor_info: %d cpus, %d words", cpus, cnt)
	}
	words := unsafe.Slice((*uint32)(info), int(cnt))
	for c := uint32(0); c < cpus; c++ {
		user, sys, idle, nice := uint64(words[c*4]), uint64(words[c*4+1]), uint64(words[c*4+2]), uint64(words[c*4+3])
		busy += user + sys + nice
		total += user + sys + nice + idle
	}
	return busy, total, nil
}

// VMCounters are what host_statistics64 gives that a reading uses.
type VMCounters struct {
	Free, Inactive, Speculative uint64 // pages
	Pageouts                    uint64 // pages written out since boot
	Decompressions              uint64 // compressed pages brought back since boot (vm_stat's "Decompressions")
	Active, Wired               uint64 // pages
	External                    uint64 // pages that back files (external_page_count; the file cache)
}

// VM reads the page counts and the pageout counter.
func (m *Mach) VM() (VMCounters, error) {
	count := uint32(vmStatsWords)
	if kr := m.vmStats(m.host, hostVMInfo64, &m.vmStatsBuf[0], &count); kr != 0 {
		return VMCounters{}, fmt.Errorf("host_statistics64: kern_return %d", kr)
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(&m.vmStatsBuf[0])), min(int(count), vmStatsWords)*4)
	return parseVMCounters(b)
}

// parseVMCounters reads the counters out of a vm_statistics64 reply. The reply must reach the last word read (external_page_count, 140
// bytes in); a shorter one is an error, not a read past its end.
func parseVMCounters(b []byte) (VMCounters, error) {
	if len(b) < vmMinBytes {
		return VMCounters{}, fmt.Errorf("host_statistics64: %d bytes, need %d", len(b), vmMinBytes)
	}
	u32 := func(off int) uint64 { return uint64(binary.NativeEndian.Uint32(b[off:])) }
	u64 := func(off int) uint64 { return binary.NativeEndian.Uint64(b[off:]) }
	return VMCounters{Free: u32(vmOffFree), Inactive: u32(vmOffInactive), Speculative: u32(vmOffSpeculative), Pageouts: u64(vmOffPageouts),
		Decompressions: u64(vmOffDecompressions), Active: u32(vmOffActive), Wired: u32(vmOffWired), External: u32(vmOffExternal)}, nil
}

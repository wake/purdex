//go:build darwin

package facts

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Which cores are performance and which efficiency is in the IORegistry: every cpuN node under IODeviceTree:/cpus carries a
// "cluster-type" ("E" or "P") and a "logical-cpu-id", the number host_processor_info counts the core by. It is read once, through
// purego (no cgo, no fork), and kept: the cores of a machine do not change while wwd runs.

const (
	cpusPath        = "IODeviceTree:/cpus"
	devicePlane     = "IODeviceTree"
	cfNumberSInt64  = 4 // kCFNumberSInt64Type
	cfStringUTF8Enc = 0x08000100
	ioNameSize      = 128 // io_name_t
)

var (
	kindsOnce sync.Once
	kindsVal  []string
)

// DarwinCoreKinds is each core's kind ("e", "p") for a machine of ncpu cores, or nil when the registry does not say for certain.
// The first call reads the registry; the answer is kept.
func DarwinCoreKinds(ncpu int) []string {
	kindsOnce.Do(func() {
		nodes, err := readCPUNodes()
		if err != nil {
			return
		}
		kindsVal = KindsFromNodes(nodes, ncpu)
	})
	if len(kindsVal) != ncpu { // the core count is not the one the answer was made for
		return nil
	}
	return kindsVal
}

func readCPUNodes() (nodes []CPUNode, err error) {
	defer func() {
		if p := recover(); p != nil { // purego panics on a symbol it cannot bind
			nodes, err = nil, fmt.Errorf("facts: cpu kinds: %v", p)
		}
	}()
	cf, err := purego.Dlopen(frameworkCF, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	io, err := purego.Dlopen(frameworkIOKit, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	var (
		fromPath  func(port uint32, path *byte) uint32
		children  func(entry uint32, plane *byte, it *uint32) int32
		next      func(it uint32) uint32
		name      func(entry uint32, buf *byte) int32
		property  func(entry uint32, key uintptr, alloc uintptr, opts uint32) uintptr
		ioRelease func(obj uint32) int32
		mkString  func(alloc uintptr, c *byte, enc uint32) uintptr
		release   func(ref uintptr)
		typeOf    func(ref uintptr) uint64
		numType   func() uint64
		dataType  func() uint64
		numValue  func(ref uintptr, typ int32, out *int64) uint8
		dataLen   func(ref uintptr) int64
		dataBytes func(ref uintptr) unsafe.Pointer
	)
	purego.RegisterLibFunc(&fromPath, io, "IORegistryEntryFromPath")
	purego.RegisterLibFunc(&children, io, "IORegistryEntryGetChildIterator")
	purego.RegisterLibFunc(&next, io, "IOIteratorNext")
	purego.RegisterLibFunc(&name, io, "IORegistryEntryGetName")
	purego.RegisterLibFunc(&property, io, "IORegistryEntryCreateCFProperty")
	purego.RegisterLibFunc(&ioRelease, io, "IOObjectRelease")
	purego.RegisterLibFunc(&mkString, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&release, cf, "CFRelease")
	purego.RegisterLibFunc(&typeOf, cf, "CFGetTypeID")
	purego.RegisterLibFunc(&numType, cf, "CFNumberGetTypeID")
	purego.RegisterLibFunc(&dataType, cf, "CFDataGetTypeID")
	purego.RegisterLibFunc(&numValue, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&dataLen, cf, "CFDataGetLength")
	purego.RegisterLibFunc(&dataBytes, cf, "CFDataGetBytePtr")

	prop := func(entry uint32, key string) uintptr { // the caller releases a non-zero answer
		k := append([]byte(key), 0)
		ks := mkString(0, &k[0], cfStringUTF8Enc)
		if ks == 0 {
			return 0
		}
		defer release(ks)
		return property(entry, ks, 0, 0)
	}

	path := append([]byte(cpusPath), 0)
	cpus := fromPath(ioKitMainPortNull, &path[0])
	if cpus == 0 {
		return nil, fmt.Errorf("facts: no %s in the IORegistry", cpusPath)
	}
	defer ioRelease(cpus)
	plane := append([]byte(devicePlane), 0)
	var it uint32
	if kr := children(cpus, &plane[0], &it); kr != 0 || it == 0 {
		return nil, fmt.Errorf("facts: IORegistryEntryGetChildIterator: %d", kr)
	}
	defer ioRelease(it)
	for ch := next(it); ch != 0; ch = next(it) {
		var buf [ioNameSize]byte
		if name(ch, &buf[0]) == 0 && isCPUNodeName(cString(buf[:])) {
			var n CPUNode
			if v := prop(ch, "logical-cpu-id"); v != 0 {
				var id int64
				if typeOf(v) == numType() && numValue(v, cfNumberSInt64, &id) != 0 {
					n.LogicalID, n.HaveID = int(id), true
				}
				release(v)
			}
			if v := prop(ch, "cluster-type"); v != 0 {
				if typeOf(v) == dataType() {
					if b := unsafe.Slice((*byte)(dataBytes(v)), int(dataLen(v))); len(b) > 0 {
						n.Cluster, n.HaveCluster = cString(b), true
					}
				}
				release(v)
			}
			nodes = append(nodes, n)
		}
		ioRelease(ch)
	}
	return nodes, nil
}

// cString is the text before the first NUL.
func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

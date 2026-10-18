//go:build darwin

package facts

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The owner rule reads two things IOKit knows, both through purego (no cgo, no
// fork, no TCC permission): the seconds since the last HID event, which is the
// IOHIDSystem service's HIDIdleTime (the figure `ioreg -c IOHIDSystem` prints, and
// the one spec section 8 names), and the IORegistry root's IOConsoleLocked.
//
// It is deliberately not CGEventSourceSecondsSinceLastEventType: that counts events
// software posts (remote control, automation) as input, so on a machine something
// drives, the owner would look present for hours after the last real keypress. On
// air26 the two read 1575 s and 24871 s at the same moment.

const (
	cfNumberSInt64Type = 4             // kCFNumberSInt64Type
	hidSystemClass     = "IOHIDSystem" // the IOKit service whose properties ioreg -c IOHIDSystem prints
	hidIdleKey         = "HIDIdleTime" // nanoseconds, a CFNumber
	cfStringEncUTF8    = 0x08000100    // kCFStringEncodingUTF8
	ioKitMainPortNull  = 0             // kIOMainPortDefault
	frameworkCF        = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	frameworkIOKit     = "/System/Library/Frameworks/IOKit.framework/IOKit"
	consoleLockedKey   = "IOConsoleLocked"
	errNoHIDSystem     = "no IOHIDSystem HIDIdleTime (idle time unavailable)"
)

// DarwinOwnerSources are the real reads. An error means the frameworks could not
// be loaded; the caller then runs without an owner rule.
func DarwinOwnerSources() (src OwnerSources, err error) {
	defer func() {
		if p := recover(); p != nil { // purego panics on a symbol it cannot bind
			src, err = OwnerSources{}, fmt.Errorf("facts: owner frameworks: %v", p)
		}
	}()
	cf, err := purego.Dlopen(frameworkCF, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return src, err
	}
	io, err := purego.Dlopen(frameworkIOKit, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return src, err
	}
	var (
		matching  func(name *byte) uintptr
		service   func(port uint32, matching uintptr) uint32
		numValue  func(ref uintptr, typ int32, out *int64) uint8
		numType   func() uint64
		mkString  func(alloc uintptr, c *byte, enc uint32) uintptr
		release   func(ref uintptr)
		boolValue func(ref uintptr) uint8
		boolType  func() uint64
		typeOf    func(ref uintptr) uint64
		rootEntry func(port uint32) uint32
		property  func(entry uint32, key uintptr, alloc uintptr, opts uint32) uintptr
		ioRelease func(obj uint32) int32
	)
	purego.RegisterLibFunc(&matching, io, "IOServiceMatching")
	purego.RegisterLibFunc(&service, io, "IOServiceGetMatchingService")
	purego.RegisterLibFunc(&numValue, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&numType, cf, "CFNumberGetTypeID")
	purego.RegisterLibFunc(&mkString, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&release, cf, "CFRelease")
	purego.RegisterLibFunc(&boolValue, cf, "CFBooleanGetValue")
	purego.RegisterLibFunc(&boolType, cf, "CFBooleanGetTypeID")
	purego.RegisterLibFunc(&typeOf, cf, "CFGetTypeID")
	purego.RegisterLibFunc(&rootEntry, io, "IORegistryGetRootEntry")
	purego.RegisterLibFunc(&property, io, "IORegistryEntryCreateCFProperty")
	purego.RegisterLibFunc(&ioRelease, io, "IOObjectRelease")

	key := append([]byte(consoleLockedKey), 0)
	hidClass := append([]byte(hidSystemClass), 0)
	hidKey := append([]byte(hidIdleKey), 0)
	return OwnerSources{
		IdleSeconds: func() (float64, error) {
			// IOServiceMatching's dictionary is consumed by IOServiceGetMatchingService, so nothing is released for it
			dict := matching(&hidClass[0])
			if dict == 0 {
				return 0, errors.New(errNoHIDSystem)
			}
			svc := service(ioKitMainPortNull, dict)
			if svc == 0 {
				return 0, errors.New(errNoHIDSystem)
			}
			defer ioRelease(svc)
			k := mkString(0, (*byte)(unsafe.Pointer(&hidKey[0])), cfStringEncUTF8)
			if k == 0 {
				return 0, errors.New("CFStringCreateWithCString failed")
			}
			defer release(k)
			v := property(svc, k, 0, 0)
			if v == 0 {
				return 0, errors.New(errNoHIDSystem)
			}
			defer release(v)
			if typeOf(v) != numType() {
				return 0, errors.New("HIDIdleTime is not a number")
			}
			var ns int64
			if numValue(v, cfNumberSInt64Type, &ns) == 0 || ns < 0 {
				return 0, errors.New("HIDIdleTime is not a usable number")
			}
			return float64(ns) / 1e9, nil
		},
		Locked: func() (bool, error) {
			root := rootEntry(ioKitMainPortNull)
			if root == 0 {
				return false, errors.New("IORegistryGetRootEntry failed")
			}
			defer ioRelease(root)
			k := mkString(0, (*byte)(unsafe.Pointer(&key[0])), cfStringEncUTF8)
			if k == 0 {
				return false, errors.New("CFStringCreateWithCString failed")
			}
			defer release(k)
			v := property(root, k, 0, 0)
			if v == 0 {
				return false, nil // the key is absent while nobody has locked the screen
			}
			defer release(v)
			if typeOf(v) != boolType() {
				return false, errors.New("IOConsoleLocked is not a boolean")
			}
			return boolValue(v) != 0, nil
		},
	}, nil
}

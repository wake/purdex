//go:build darwin

package facts

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The owner rule reads two things the window server and IOKit know, both through
// purego (no cgo, no fork): the seconds since the last HID event
// (CGEventSourceSecondsSinceLastEventType, needs no TCC permission) and the
// IORegistry root's IOConsoleLocked.

const (
	cgCombinedSession  = 0          // kCGEventSourceStateCombinedSessionState: measured equal to ioreg's HIDIdleTime (the HID-system state, 1, was not)
	cgAnyInputEvent    = 0xFFFFFFFF // kCGAnyInputEventType
	cfStringEncUTF8    = 0x08000100 // kCFStringEncodingUTF8
	ioKitMainPortNull  = 0          // kIOMainPortDefault
	frameworkCG        = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	frameworkCF        = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	frameworkIOKit     = "/System/Library/Frameworks/IOKit.framework/IOKit"
	consoleLockedKey   = "IOConsoleLocked"
	errNoWindowServerS = "no window server session (idle time unavailable)"
)

// DarwinOwnerSources are the real reads. An error means the frameworks could not
// be loaded; the caller then runs without an owner rule.
func DarwinOwnerSources() (src OwnerSources, err error) {
	defer func() {
		if p := recover(); p != nil { // purego panics on a symbol it cannot bind
			src, err = OwnerSources{}, fmt.Errorf("facts: owner frameworks: %v", p)
		}
	}()
	cg, err := purego.Dlopen(frameworkCG, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return src, err
	}
	cf, err := purego.Dlopen(frameworkCF, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return src, err
	}
	io, err := purego.Dlopen(frameworkIOKit, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return src, err
	}
	var (
		since     func(state int32, evType uint32) float64
		mkString  func(alloc uintptr, c *byte, enc uint32) uintptr
		release   func(ref uintptr)
		boolValue func(ref uintptr) uint8
		boolType  func() uint64
		typeOf    func(ref uintptr) uint64
		rootEntry func(port uint32) uint32
		property  func(entry uint32, key uintptr, alloc uintptr, opts uint32) uintptr
		ioRelease func(obj uint32) int32
	)
	purego.RegisterLibFunc(&since, cg, "CGEventSourceSecondsSinceLastEventType")
	purego.RegisterLibFunc(&mkString, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&release, cf, "CFRelease")
	purego.RegisterLibFunc(&boolValue, cf, "CFBooleanGetValue")
	purego.RegisterLibFunc(&boolType, cf, "CFBooleanGetTypeID")
	purego.RegisterLibFunc(&typeOf, cf, "CFGetTypeID")
	purego.RegisterLibFunc(&rootEntry, io, "IORegistryGetRootEntry")
	purego.RegisterLibFunc(&property, io, "IORegistryEntryCreateCFProperty")
	purego.RegisterLibFunc(&ioRelease, io, "IOObjectRelease")

	key := append([]byte(consoleLockedKey), 0)
	return OwnerSources{
		IdleSeconds: func() (float64, error) {
			s := since(cgCombinedSession, cgAnyInputEvent)
			if s < 0 || s != s { // negative or NaN: no session
				return 0, errors.New(errNoWindowServerS)
			}
			return s, nil
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

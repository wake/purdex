//go:build darwin

package facts

import (
	"errors"
	"fmt"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// The disk counters of a Mac are in the IORegistry: every IOBlockStorageDriver carries a Statistics dictionary (the figures `ioreg -c
// IOBlockStorageDriver -r -l` prints), and its child IOMedia says whether it is the whole disk (Whole = Yes) and under what name (BSD
// Name, disk0). Read through purego like the owner rule's IOKit reads: no cgo, no fork. Every IOKit object and every Core Foundation
// object created by a call is released before it returns; the key strings are made once and kept for the life of the process.

const (
	ioDriverClass   = "IOBlockStorageDriver"
	ioMediaClass    = "IOMedia"
	ioServicePlane  = "IOService"
	ioKeyStatistics = "Statistics"
	ioKeyWhole      = "Whole"
	ioKeyBSDName    = "BSD Name"
	bsdNameMax      = 64
)

func cstr(s string) *byte { return &append([]byte(s), 0)[0] }

// newDarwinDiskIO binds the IOKit and Core Foundation entry points. An error means they could not be loaded: disk_io stays null.
func newDarwinDiskIO() (read func() ([]RawDiskIO, error), err error) {
	defer func() {
		if p := recover(); p != nil { // purego panics on a symbol it cannot bind
			read, err = nil, fmt.Errorf("facts: disk io frameworks: %v", p)
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
		matching  func(name *byte) uintptr
		services  func(port uint32, matching uintptr, it *uint32) int32
		next      func(it uint32) uint32
		childIter func(entry uint32, plane *byte, it *uint32) int32
		conforms  func(obj uint32, class *byte) uint32
		property  func(entry uint32, key uintptr, alloc uintptr, opts uint32) uintptr
		ioRelease func(obj uint32) int32

		release   func(ref uintptr)
		typeOf    func(ref uintptr) uint64
		mkString  func(alloc uintptr, c *byte, enc uint32) uintptr
		dictGet   func(dict, key uintptr) uintptr
		dictType  func() uint64
		numType   func() uint64
		numValue  func(ref uintptr, typ int32, out *int64) uint8
		boolType  func() uint64
		boolValue func(ref uintptr) uint8
		strType   func() uint64
		strGet    func(ref uintptr, buf *byte, size int64, enc uint32) uint8
	)
	purego.RegisterLibFunc(&matching, io, "IOServiceMatching")
	purego.RegisterLibFunc(&services, io, "IOServiceGetMatchingServices")
	purego.RegisterLibFunc(&next, io, "IOIteratorNext")
	purego.RegisterLibFunc(&childIter, io, "IORegistryEntryGetChildIterator")
	purego.RegisterLibFunc(&conforms, io, "IOObjectConformsTo")
	purego.RegisterLibFunc(&property, io, "IORegistryEntryCreateCFProperty")
	purego.RegisterLibFunc(&ioRelease, io, "IOObjectRelease")
	purego.RegisterLibFunc(&release, cf, "CFRelease")
	purego.RegisterLibFunc(&typeOf, cf, "CFGetTypeID")
	purego.RegisterLibFunc(&mkString, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&dictGet, cf, "CFDictionaryGetValue")
	purego.RegisterLibFunc(&dictType, cf, "CFDictionaryGetTypeID")
	purego.RegisterLibFunc(&numType, cf, "CFNumberGetTypeID")
	purego.RegisterLibFunc(&numValue, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&boolType, cf, "CFBooleanGetTypeID")
	purego.RegisterLibFunc(&boolValue, cf, "CFBooleanGetValue")
	purego.RegisterLibFunc(&strType, cf, "CFStringGetTypeID")
	purego.RegisterLibFunc(&strGet, cf, "CFStringGetCString")

	key := func(s string) uintptr {
		k := mkString(0, cstr(s), cfStringEncUTF8)
		if k == 0 {
			panic("CFStringCreateWithCString failed for " + s)
		}
		return k
	}
	kStats, kWhole, kBSD := key(ioKeyStatistics), key(ioKeyWhole), key(ioKeyBSDName)
	statKeys := map[string]uintptr{ioStatReadBytes: key(ioStatReadBytes), ioStatWriteBytes: key(ioStatWriteBytes),
		ioStatReadOps: key(ioStatReadOps), ioStatWriteOps: key(ioStatWriteOps)}
	driverClass, mediaClass, plane := cstr(ioDriverClass), cstr(ioMediaClass), cstr(ioServicePlane)

	// wholeMedia is the Whole flag and BSD Name of the IOMedia under a driver, for the first media that is a whole disk.
	wholeMedia := func(driver uint32) (bool, string) {
		var it uint32
		if childIter(driver, plane, &it) != 0 {
			return false, ""
		}
		defer ioRelease(it)
		for {
			child := next(it)
			if child == 0 {
				return false, ""
			}
			whole, name := false, ""
			if conforms(child, mediaClass) != 0 {
				if v := property(child, kWhole, 0, 0); v != 0 {
					whole = typeOf(v) == boolType() && boolValue(v) != 0
					release(v)
				}
				if whole {
					if v := property(child, kBSD, 0, 0); v != 0 {
						if typeOf(v) == strType() {
							var buf [bsdNameMax]byte
							if strGet(v, &buf[0], bsdNameMax, cfStringEncUTF8) != 0 {
								name = unix.ByteSliceToString(buf[:])
							}
						}
						release(v)
					}
				}
			}
			ioRelease(child)
			if whole {
				return true, name
			}
		}
	}

	// statistics is the four counters of a driver, by key; nothing for a figure that is missing or not a non-negative number.
	statistics := func(driver uint32) map[string]uint64 {
		stats := property(driver, kStats, 0, 0)
		if stats == 0 {
			return nil
		}
		defer release(stats)
		if typeOf(stats) != dictType() {
			return nil
		}
		out := make(map[string]uint64, len(statKeys))
		for name, k := range statKeys {
			v := dictGet(stats, k) // not retained: it lives as long as the dictionary
			if v == 0 || typeOf(v) != numType() {
				continue
			}
			var n int64
			if numValue(v, cfNumberSInt64Type, &n) != 0 && n >= 0 {
				out[name] = uint64(n)
			}
		}
		return out
	}

	return func() ([]RawDiskIO, error) {
		dict := matching(driverClass)
		if dict == 0 {
			return nil, errors.New("IOServiceMatching failed")
		}
		var it uint32
		if kr := services(ioKitMainPortNull, dict, &it); kr != 0 { // consumes dict
			return nil, fmt.Errorf("IOServiceGetMatchingServices: kern_return %d", kr)
		}
		defer ioRelease(it)
		var out []RawDiskIO
		for {
			driver := next(it)
			if driver == 0 {
				return out, nil
			}
			stats := statistics(driver)
			whole, name := wholeMedia(driver)
			ioRelease(driver)
			if d, ok := rawDiskIOFromProps(stats, whole, name); ok {
				out = append(out, d)
			}
		}
	}, nil
}

package facts

import (
	"encoding/binary"
	"errors"
)

// ParseDarwinLoadavg reads struct loadavg as vm.loadavg returns it: three uint32 fixed-point values, padding, then fscale as
// uint64 (24 bytes in all). It has no build tag so the byte layout is tested on every platform.
func ParseDarwinLoadavg(b []byte) (l1, l5, l15 float64, err error) {
	if len(b) != 24 {
		return 0, 0, 0, errors.New("vm.loadavg: unexpected size")
	}
	fscale := binary.NativeEndian.Uint64(b[16:])
	if fscale == 0 {
		return 0, 0, 0, errors.New("vm.loadavg: fscale is 0")
	}
	f := float64(fscale)
	return float64(binary.NativeEndian.Uint32(b[0:])) / f, float64(binary.NativeEndian.Uint32(b[4:])) / f,
		float64(binary.NativeEndian.Uint32(b[8:])) / f, nil
}

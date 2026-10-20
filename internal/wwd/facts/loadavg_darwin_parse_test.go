package facts

import (
	"encoding/binary"
	"testing"
)

func TestParseDarwinLoadavg(t *testing.T) {
	b := make([]byte, 24)
	binary.NativeEndian.PutUint32(b[0:], 2048)  // 1.0
	binary.NativeEndian.PutUint32(b[4:], 3072)  // 1.5
	binary.NativeEndian.PutUint32(b[8:], 512)   // 0.25
	binary.NativeEndian.PutUint64(b[16:], 2048) // fscale
	l1, l5, l15, err := ParseDarwinLoadavg(b)
	if err != nil || l1 != 1 || l5 != 1.5 || l15 != 0.25 {
		t.Fatalf("%v %v %v %v", l1, l5, l15, err)
	}
	if _, _, _, err := ParseDarwinLoadavg(b[:20]); err == nil {
		t.Error("a short buffer must fail")
	}
	binary.NativeEndian.PutUint64(b[16:], 0)
	if _, _, _, err := ParseDarwinLoadavg(b); err == nil {
		t.Error("fscale 0 must fail")
	}
}

func TestIsCPUNodeName(t *testing.T) {
	for s, want := range map[string]bool{"cpu0": true, "cpu12": true, "cpu": false, "cpus": false, "cpu1a": false, "l2-cache": false, "": false} {
		if isCPUNodeName(s) != want {
			t.Errorf("%q: want %v", s, want)
		}
	}
}

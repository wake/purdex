package fleet

import (
	"errors"
	"testing"
)

func TestDiskMount(t *testing.T) {
	if DiskMount("darwin") != "/System/Volumes/Data" || DiskMount("linux") != "/" {
		t.Error("wrong mount")
	}
}

func TestLocalDisk_Fallback(t *testing.T) {
	var asked []string
	fake := func(vals map[string]float64) func(string) (float64, error) {
		return func(p string) (float64, error) {
			asked = append(asked, p)
			if v, ok := vals[p]; ok {
				return v, nil
			}
			return 0, errors.New("no")
		}
	}
	if v, ok := localDisk("darwin", fake(map[string]float64{"/System/Volumes/Data": 40, "/": 90})); !ok || v != 40 {
		t.Errorf("data volume first: %v %v", v, ok)
	}
	asked = nil
	if v, ok := localDisk("darwin", fake(map[string]float64{"/": 90})); !ok || v != 90 || len(asked) != 2 {
		t.Errorf("darwin falls back to /: %v %v %v", v, ok, asked)
	}
	asked = nil
	if _, ok := localDisk("linux", fake(nil)); ok || len(asked) != 1 {
		t.Errorf("linux has no fallback: %v", asked)
	}
}

func TestUsedPct(t *testing.T) {
	// df: used 60 of (60 + 20 available) = 75 %, although 30 blocks are reserved.
	if v, err := usedPct(110, 50, 20); err != nil || v != 75 {
		t.Errorf("got %v %v", v, err)
	}
	if _, err := usedPct(0, 0, 0); err == nil {
		t.Error("empty fs must be an error")
	}
}

func TestLocalDisk_Live(t *testing.T) {
	if v, ok := LocalDisk(); ok && (v < 0 || v > 100) {
		t.Errorf("implausible %v", v)
	}
}

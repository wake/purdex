//go:build darwin || linux

package facts

import (
	"testing"
	"time"
)

// The CPU time of the process grows when it works, and is never negative.
func TestSelfCPUTimeGrows(t *testing.T) {
	a, err := selfCPUTime()
	if err != nil || a < 0 {
		t.Fatalf("%v %v", a, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for x := 0; ; x++ { // burn until the rusage clock moves; its resolution is coarse on some kernels
		b, err := selfCPUTime()
		if err != nil {
			t.Fatal(err)
		}
		if b > a {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("cpu time did not move from %v in 2 s of spinning (%d)", a, x)
		}
	}
}

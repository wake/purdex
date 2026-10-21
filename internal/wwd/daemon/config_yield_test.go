package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
)

// #3941: [worker] fastlane_yield_after_seconds / fastlane_yield_max_seconds. Not set: 300 and 1800; after 0 switches the giving way off.
func TestConfigFastlaneYield(t *testing.T) {
	c, err := load(t, "[worker]\n")
	if err != nil || c.Worker.FastlaneYield() != (derived.YieldPolicy{After: 5 * time.Minute, Max: 30 * time.Minute}) {
		t.Fatalf("not set: %+v %v", c.Worker.FastlaneYield(), err)
	}
	if c, err = load(t, "[worker]\nfastlane_yield_after_seconds = 0\n"); err != nil || c.Worker.FastlaneYield().After != 0 {
		t.Fatalf("0 is off: %+v %v", c.Worker.FastlaneYield(), err)
	}
	if c, err = load(t, "[worker]\nfastlane_yield_after_seconds = 60\nfastlane_yield_max_seconds = 600\n"); err != nil || c.Worker.FastlaneYield() != (derived.YieldPolicy{After: time.Minute, Max: 10 * time.Minute}) {
		t.Fatalf("set: %+v %v", c.Worker.FastlaneYield(), err)
	}
	for _, bad := range []string{"fastlane_yield_after_seconds = -1", "fastlane_yield_after_seconds = 86401", "fastlane_yield_max_seconds = -1", "fastlane_yield_max_seconds = 0", "fastlane_yield_max_seconds = 86401"} {
		if _, err := load(t, "[worker]\n"+bad+"\n"); err == nil || !strings.Contains(err.Error(), strings.Fields(bad)[0]) {
			t.Errorf("%s accepted (err %v)", bad, err)
		}
	}
	// a cap under the wait would never give way
	if _, err := load(t, "[worker]\nfastlane_yield_after_seconds = 600\nfastlane_yield_max_seconds = 300\n"); err == nil || !strings.Contains(err.Error(), "fastlane_yield_max_seconds") {
		t.Errorf("max below after accepted (err %v)", err)
	}
	// with the wait off, the cap alone is not a mistake
	if _, err := load(t, "[worker]\nfastlane_yield_after_seconds = 0\nfastlane_yield_max_seconds = 300\n"); err != nil {
		t.Errorf("off with a cap: %v", err)
	}
}

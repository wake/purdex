package facts

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// PSIWarn and PSIDanger are the PSI part of the Linux mapping (spec §3.1), exported so the watcher's pressure rules (S3-2) judge a sample by
// the same numbers the zone does, not by a second copy: at or above a threshold counts, just below does not.
func TestPSIWarnAndDangerThresholds(t *testing.T) {
	for _, c := range []struct {
		name         string
		psi          wire.PSI
		warn, danger bool
	}{
		{"calm", wire.PSI{}, false, false},
		{"some10 just under", wire.PSI{Some10: 9.99}, false, false},
		{"some10 at the line", wire.PSI{Some10: 10}, true, false},
		{"full10 just under", wire.PSI{Full10: 0.99}, false, false},
		{"full10 at the line", wire.PSI{Full10: 1}, true, false},
		{"full10 just under danger", wire.PSI{Full10: 9.99}, true, false},
		{"full10 at danger", wire.PSI{Full10: 10}, true, true},
		{"some60 just under danger", wire.PSI{Some60: 24.99}, false, false},
		{"some60 at danger", wire.PSI{Some60: 25}, false, true},
	} {
		if got := PSIWarn(c.psi); got != c.warn {
			t.Errorf("%s: PSIWarn = %v, want %v", c.name, got, c.warn)
		}
		if got := PSIDanger(c.psi); got != c.danger {
			t.Errorf("%s: PSIDanger = %v, want %v", c.name, got, c.danger)
		}
	}
}

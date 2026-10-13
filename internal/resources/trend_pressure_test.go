package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Pressure 0 is a failed read: a window that starts or ends on it says nothing
// about the pressure direction (codex R1).
func TestTrendOf_UnknownPressureEndpointIsIgnored(t *testing.T) {
	step := func(from, to int) []TrendSample {
		return series(windowN, constF(50), constF(30), func(i int) int {
			if i < windowN/2 {
				return from
			}
			return to
		})
	}
	for _, c := range []struct{ from, to int }{{1, 0}, {0, 1}, {4, 0}, {0, 2}} {
		d := step(c.from, c.to)
		assert.Equal(t, TrendFlat, TrendOf(d, lastAt(d), v1Settings()), "%d to %d", c.from, c.to)
	}
	// Mem and cpu still decide when pressure is unknown.
	d := series(windowN, lineF(50, 2), constF(30), pressureF(0))
	assert.Equal(t, TrendRising, TrendOf(d, lastAt(d), v1Settings()))
}

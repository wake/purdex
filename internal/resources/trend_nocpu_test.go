package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// trend_cpu_dppm = 0 takes cpu out of the direction (G1, lead's decision of
// 2026-10-13): a steep cpu slope neither raises nor lowers the trend, memory and
// pressure still decide. (M: drop the guard and any cpu slope is rising.)
func TestTrendOf_CPUDisabledByZero(t *testing.T) {
	off := Settings{TrendWindowS: intp(60), TrendMemDPPM: intp(10), TrendCPUDPPM: intp(0)}.Effective()
	assert.Equal(t, 0.0, off.TrendCPUPPM())
	up := series(windowN, constF(50), lineF(30, 40), pressureF(1))
	down := series(windowN, constF(50), lineF(300, -40), pressureF(1))
	assert.Equal(t, TrendFlat, TrendOf(up, lastAt(up), off), "cpu +40/min does not make it rising")
	assert.Equal(t, TrendFlat, TrendOf(down, lastAt(down), off), "cpu -40/min does not make it falling")
	// The same data with cpu on is rising / falling.
	on := v1Settings()
	assert.Equal(t, TrendRising, TrendOf(up, lastAt(up), on))
	assert.Equal(t, TrendFalling, TrendOf(down, lastAt(down), on))

	// Memory still decides, and so does the pressure level.
	memUp := series(windowN, lineF(50, 2), lineF(30, 40), pressureF(1))
	assert.Equal(t, TrendRising, TrendOf(memUp, lastAt(memUp), off))
	memDown := series(windowN, lineF(60, -2), lineF(30, 40), pressureF(1))
	assert.Equal(t, TrendFalling, TrendOf(memDown, lastAt(memDown), off), "cpu rising is ignored: memory falling is falling")
	pr := series(windowN, constF(50), lineF(30, -40), func(i int) int {
		if i < windowN/2 {
			return 1
		}
		return 2
	})
	assert.Equal(t, TrendRising, TrendOf(pr, lastAt(pr), off))
}

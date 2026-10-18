package derived

import (
	"math/rand"
	"testing"

	"github.com/wake/purdex/internal/resources"
)

// FullLatch is internal/resources.FullLatch, copied because wwd must not import that package. This keeps the two equal
// until WWD-9 removes the Purdex one: the same readings, the same answer at every step.
func TestFullLatchEqualsResourcesFullLatch(t *testing.T) {
	check := func(t *testing.T, steps [][4]float64) {
		t.Helper()
		var mine FullLatch
		var theirs resources.FullLatch
		for i, s := range steps {
			load1, ncpu, mem, pressure := s[0], int(s[1]), s[2], int(s[3])
			got := mine.Update(load1, ncpu, mem, pressure)
			want := theirs.Update(resources.HostUse{Load1: load1, NCPU: ncpu, Mem: mem, Pressure: pressure})
			if got != want {
				t.Fatalf("step %d %v: wwd says %v, resources says %v", i, s, got, want)
			}
		}
	}
	t.Run("walk across the edges", func(t *testing.T) {
		check(t, [][4]float64{
			{0, 10, 10, 1}, {9.99, 10, 10, 1}, {10, 10, 10, 1}, // load enters at ncpu
			{9.5, 10, 10, 1}, {9.0, 10, 10, 1}, {8.99, 10, 10, 1}, // and leaves under 0.9 x ncpu
			{1, 10, 90, 1}, {1, 10, 81, 1}, {1, 10, 80.9, 1}, // memory enters at 90 and leaves under 81
			{1, 10, 10, 2}, {1, 10, 10, 4}, {1, 10, 10, 1}, // pressure warn or worse enters; normal leaves
			{1, 10, 10, 0}, {50, 0, 99, 4}, {1, 10, 10, 1}, // unknown pressure (0) is not warn; no cpu count clears
		})
	})
	t.Run("random readings", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1))
		var steps [][4]float64
		for i := 0; i < 5000; i++ {
			steps = append(steps, [4]float64{
				float64(rng.Intn(1300)) / 100, float64(rng.Intn(4)) * 4, float64(rng.Intn(1000)) / 10, []float64{0, 1, 1, 2, 4}[rng.Intn(5)],
			})
		}
		check(t, steps)
	})
}

package poolmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Moved from internal/fleet/load_test.go with LoadScore (WWD-9 9d-3a).
// LoadScore is the composite load, with the name of the reading that holds it up (the first of cpu, mem, pressure, paging, disk on a tie).
func TestLoadScoreNamesTheDriver(t *testing.T) {
	for _, c := range []struct {
		name   string
		f      LocalFast
		disk   float64
		diskOK bool
		want   float64
		driver string
	}{
		{"cpu leads", LocalFast{CPU: 95, Mem: 40, Level: 1}, 40, true, 95, "cpu"},
		{"mem leads", LocalFast{CPU: 10, Mem: 82, Level: 1}, 40, true, 82, "mem"},
		{"level leads", LocalFast{CPU: 10, Mem: 40, Level: 2}, 40, true, 75, "pressure"},
		{"an idle host is held up by the level", LocalFast{Level: 1}, 40, true, 30, "pressure"},
		{"paging leads", LocalFast{CPU: 5, Mem: 5, Level: 1, PageRate: 60}, 40, true, 60, "paging"},
		{"disk leads", LocalFast{CPU: 5, Mem: 5}, 90, true, 90, "disk"},
		{"disk without a reading does not", LocalFast{CPU: 5}, 99, false, 5, "cpu"},
		{"a tie goes to the first", LocalFast{CPU: 50, Mem: 50}, 40, true, 50, "cpu"},
		{"nothing at all", LocalFast{}, 0, false, 0, "cpu"},
	} {
		score, driver := LoadScore(c.f, c.disk, c.diskOK)
		assert.InDelta(t, c.want, score, 0.0001, c.name)
		assert.Equal(t, c.driver, driver, c.name)
	}
}

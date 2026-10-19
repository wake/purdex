package teammod

import (
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The member cap's ceiling is 9 (spec §5.1): a request for more is clamped to it, 0 falls back, and the ceiling is the wire
// constant the PUT and the CLI check against. Mutation gate: the ceiling back to 8 → red.
func TestNormaliseMaxMembers_CeilingIsNine(t *testing.T) {
	for _, c := range []struct{ asked, def, want int }{{0, 3, 3}, {-1, 3, 3}, {1, 3, 1}, {8, 3, 8}, {9, 3, 9}, {10, 3, 9}, {100, 3, 9}} {
		if got := normaliseMaxMembers(c.asked, c.def); got != c.want {
			t.Errorf("normaliseMaxMembers(%d, %d) = %d, want %d", c.asked, c.def, got, c.want)
		}
	}
	if team.MaxMaxMembers != 9 {
		t.Errorf("MaxMaxMembers = %d, want 9", team.MaxMaxMembers)
	}
}

package pick

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// S3-3b-1: Eligible is the one rule of what a host takes by its labels. Rank applies it to each candidate, and the alert rules of the queue ask
// it of each host of the pool.
func TestEligible(t *testing.T) {
	have := []string{"os:linux", "arch:amd64", "task:purdex/go", "host:mserv", "go"}
	cases := []struct {
		name         string
		need         []string
		explicitOnly bool
		want         bool
	}{
		{"a subset", []string{"go"}, false, true},
		{"not a subset", []string{"go", "node"}, false, false},
		{"no labels at all, any worker", nil, false, true},
		{"no labels, but the host takes only explicit jobs", nil, true, false},
		{"labels that name nothing explicit", []string{"go"}, true, false},
		{"names the os", []string{"os:linux"}, true, true},
		{"names an earned task", []string{"task:purdex/go"}, true, true},
		{"names the host (#3873)", []string{"host:mserv", "go"}, true, true},
		{"explicit but not held", []string{"os:darwin"}, true, false},
		{"explicit only counts for a host that asks for it", []string{"go"}, false, true},
	}
	for _, c := range cases {
		if got := Eligible(c.need, have, c.explicitOnly); got != c.want {
			t.Errorf("%s: Eligible(%v, explicitOnly=%v) = %v, want %v", c.name, c.need, c.explicitOnly, got, c.want)
		}
	}
}

// Rank takes what Eligible takes, nothing more and nothing less: a queued job is ranked for a host exactly when Eligible says so.
func TestRankAgreesWithEligible(t *testing.T) {
	labelSets := [][]string{nil, {"go"}, {"go", "node"}, {"os:darwin"}, {"os:darwin", "go"}, {"host:mserv"}, {"task:purdex/go", "go"}, {"rust"}}
	for _, explicitOnly := range []bool{false, true} {
		env := Env{Now: t0, Labels: []string{"go", "node", "os:darwin", "task:purdex/go", "host:mserv"}, ExplicitLabelsOnly: explicitOnly, SpeedFactor: 1}
		for i, ls := range labelSets {
			j := job("j", wire.ClassMerge, 0)
			j.Labels = ls
			ranked := len(Rank([]Candidate{cand(j)}, env)) == 1
			if want := Eligible(ls, env.Labels, explicitOnly); ranked != want {
				t.Errorf("set %d %v explicitOnly=%v: ranked %v, Eligible %v", i, ls, explicitOnly, ranked, want)
			}
		}
	}
}

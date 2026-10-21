package pick

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func explicitHostEnv() Env {
	env := baseEnv()
	env.Labels = []string{"go", "os:linux", "repo:purdex", "tool:node24", "host:mserv"}
	env.ExplicitLabelsOnly = true
	return env
}

// #3873: a qualify candidate carries repo:/tool:/host: labels and, on purpose, no task: or os: label.
func TestExplicitLabelsOnlyTakesAJobNamingThisHost(t *testing.T) {
	j := job("cand", wire.ClassDev, 0)
	j.Labels = []string{"repo:purdex", "tool:node24", "host:mserv"}
	eq(t, ids(Rank([]Candidate{cand(j)}, explicitHostEnv())), []string{"cand"})
}

func TestExplicitLabelsOnlyStillSkipsAJobWithNoOsTaskOrHostLabel(t *testing.T) {
	old, tool := job("old", wire.ClassDev, 0), job("tool", wire.ClassDev, 0)
	old.Labels = nil
	tool.Labels = []string{"repo:purdex"}
	eq(t, ids(Rank([]Candidate{cand(old), cand(tool)}, explicitHostEnv())), nil)
}

func TestExplicitLabelsOnlySkipsAJobNamingAnotherHost(t *testing.T) {
	j := job("other", wire.ClassDev, 0)
	j.Labels = []string{"repo:purdex", "host:air26"}
	eq(t, ids(Rank([]Candidate{cand(j)}, explicitHostEnv())), nil)
}

func TestNonExplicitHostStillTakesAJobNamingItself(t *testing.T) {
	env := baseEnv()
	env.Labels = []string{"host:mserv"}
	j, other := job("mine", wire.ClassDev, 0), job("other", wire.ClassDev, 0)
	j.Labels = []string{"host:mserv"}
	other.Labels = []string{"host:air26"}
	eq(t, ids(Rank([]Candidate{cand(j), cand(other)}, env)), []string{"mine"})
}

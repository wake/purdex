package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func parCfg(mode string) ParallelismConfig {
	return ParallelismConfig{Mode: mode, ReserveCores: 2, MemPerWorkerMB: 400, GoMemPerWorkerMB: 150, VitestMemPerWorkerMB: 700, VitestMaxWorkers: 3,
		GoP: Param{Auto: true}, VitestWorkers: Param{Auto: true}, PnpmWorkspace: Param{Auto: true},
		ByClass: map[Class]ParallelismOverride{}}
}

// auto: cores − reserve, cut by the memory the workers are estimated to need (go 150 MB, vitest 700 MB, pnpm 400 MB each;
// measured in G0), vitest also held to vitest_max_workers (3), never below 1.
// (M: ignore the memory; M: ignore the reserve; M: ignore the vitest cap; M: one memory figure for every tool.)
func TestParallelism_AutoFollowsCoresMemoryAndTheVitestCap(t *testing.T) {
	c := parCfg("auto")
	for _, tc := range []struct {
		name              string
		f                 HostFacts
		goP, vitest, pnpm int
	}{
		{"mlab idle: 10 cores − 2, memory plenty, vitest held to 3", HostFacts{NCPU: 10, AvailMemMB: 8000}, 8, 3, 4},
		{"memory is the limit for vitest first: 1500 MB / 700 = 2", HostFacts{NCPU: 10, AvailMemMB: 1500}, 8, 2, 3},
		{"and for go: 600 MB / 150 = 4", HostFacts{NCPU: 10, AvailMemMB: 600}, 4, 1, 1},
		{"almost no memory: still 1", HostFacts{NCPU: 10, AvailMemMB: 100}, 1, 1, 1},
		{"few cores", HostFacts{NCPU: 3, AvailMemMB: 8000}, 1, 1, 1},
		{"memory unknown: cores only (vitest still capped)", HostFacts{NCPU: 10}, 8, 3, 4},
		{"cores unknown", HostFacts{}, 1, 1, 1},
		{"a big host: vitest stays at its cap, go follows the cores", HostFacts{NCPU: 32, AvailMemMB: 64000}, 30, 3, 4},
	} {
		l := c.Resolve(ClassHeavy, tc.f)
		assert.Equal(t, tc.goP, l.GoP, tc.name+": go_p")
		assert.Equal(t, tc.vitest, l.VitestWorkers, tc.name+": vitest")
		assert.Equal(t, tc.pnpm, l.PnpmWorkspace, tc.name+": pnpm")
		assert.Equal(t, SrcAuto, l.Src["go_p"], tc.name)
	}
	c.VitestMaxWorkers = 6
	assert.Equal(t, 6, c.Resolve(ClassHeavy, HostFacts{NCPU: 10, AvailMemMB: 8000}).VitestWorkers, "the cap is a setting")
}

// A number the person set is used as it is, and says so.
func TestParallelism_ANumberIsUsedAsGiven(t *testing.T) {
	c := parCfg("auto")
	c.GoP = Param{N: 6}
	l := c.Resolve(ClassHeavy, HostFacts{NCPU: 10, AvailMemMB: 100})
	assert.Equal(t, 6, l.GoP, "not cut by memory: the person asked for 6")
	c.VitestWorkers = Param{N: 8}
	assert.Equal(t, 8, c.Resolve(ClassHeavy, HostFacts{NCPU: 10, AvailMemMB: 100}).VitestWorkers, "a number is not cut by the vitest cap either")
	assert.Equal(t, SrcConfig, l.Src["go_p"])
	assert.Equal(t, SrcAuto, l.Src["vitest_workers"])
}

// A class can set its own.
func TestParallelism_AClassOverrideWinsForThatClass(t *testing.T) {
	c := parCfg("auto")
	two := Param{N: 2}
	c.ByClass[ClassLight] = ParallelismOverride{GoP: &two}
	f := HostFacts{NCPU: 10, AvailMemMB: 8000}
	assert.Equal(t, 2, c.Resolve(ClassLight, f).GoP)
	assert.Equal(t, 8, c.Resolve(ClassHeavy, f).GoP)
	assert.Equal(t, 3, c.Resolve(ClassLight, f).VitestWorkers, "only the key it sets")
}

func TestParallelism_FixedUsesTheFixedDefaultsForAuto(t *testing.T) {
	c := parCfg("fixed")
	l := c.Resolve(ClassHeavy, HostFacts{NCPU: 10, AvailMemMB: 8000})
	assert.Equal(t, 3, l.GoP)
	assert.Equal(t, 3, l.VitestWorkers)
	assert.Equal(t, 2, l.PnpmWorkspace)
	assert.Equal(t, SrcDefault, l.Src["go_p"])
	c.GoP = Param{N: 5}
	assert.Equal(t, 5, c.Resolve(ClassHeavy, HostFacts{}).GoP)
}

func TestParallelism_OffCapsNothing(t *testing.T) {
	l := parCfg("off").Resolve(ClassHeavy, HostFacts{NCPU: 10, AvailMemMB: 8000})
	assert.Zero(t, l.GoP)
	assert.Zero(t, l.VitestWorkers)
	assert.Zero(t, l.PnpmWorkspace)
	// and Capped puts nothing in
	argv, env, added := Capped(words("go test ./..."), []string{"A=1"}, l.GoP, l.VitestWorkers, l.PnpmWorkspace)
	assert.Equal(t, words("go test ./..."), argv)
	assert.Equal(t, []string{"A=1"}, env)
	assert.Empty(t, added)
}

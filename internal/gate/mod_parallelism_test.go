package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func parCfg(mode string) ParallelismConfig {
	return ParallelismConfig{Mode: mode, ReserveCores: 2, MemPerWorkerMB: 400,
		GoP: Param{Auto: true}, VitestWorkers: Param{Auto: true}, PnpmWorkspace: Param{Auto: true},
		ByClass: map[Class]ParallelismOverride{}}
}

// auto: cores − reserve, cut by the memory the workers are estimated to need, never below 1.
// (M: ignore the memory; M: ignore the reserve.)
func TestParallelism_AutoFollowsCoresAndMemory(t *testing.T) {
	c := parCfg("auto")
	for _, tc := range []struct {
		name string
		f    HostFacts
		want int
	}{
		{"mlab idle: 10 cores − 2, memory plenty", HostFacts{NCPU: 10, AvailMemMB: 8000}, 8},
		{"memory is the limit: 1600 MB / 400", HostFacts{NCPU: 10, AvailMemMB: 1600}, 4},
		{"almost no memory: still 1", HostFacts{NCPU: 10, AvailMemMB: 100}, 1},
		{"few cores", HostFacts{NCPU: 2, AvailMemMB: 8000}, 1},
		{"memory unknown: cores only", HostFacts{NCPU: 10}, 8},
		{"cores unknown", HostFacts{}, 1},
	} {
		l := c.Resolve(ClassHeavy, tc.f)
		assert.Equal(t, tc.want, l.GoP, tc.name)
		assert.Equal(t, tc.want, l.VitestWorkers, tc.name)
		assert.Equal(t, SrcAuto, l.Src["go_p"], tc.name)
		assert.Equal(t, min(tc.want, 4), l.PnpmWorkspace, tc.name+": pnpm runs several packages at once, so at most 4")
	}
}

// A number the person set is used as it is, and says so.
func TestParallelism_ANumberIsUsedAsGiven(t *testing.T) {
	c := parCfg("auto")
	c.GoP = Param{N: 6}
	l := c.Resolve(ClassHeavy, HostFacts{NCPU: 10, AvailMemMB: 100})
	assert.Equal(t, 6, l.GoP, "not cut by memory: the person asked for 6")
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
	assert.Equal(t, 8, c.Resolve(ClassLight, f).VitestWorkers, "only the key it sets")
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
	argv, env, added := Capped(words("go test ./..."), []string{"A=1"}, l)
	assert.Equal(t, words("go test ./..."), argv)
	assert.Equal(t, []string{"A=1"}, env)
	assert.Empty(t, added)
}

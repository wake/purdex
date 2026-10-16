package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #2985: `tsc` for one project (-p, no -b) is light. The outer command (what the person typed) and the inner one (the tsc the
// runner then starts, which the shim sees) are classified the same, with and without -C <dir>: a light outer slot whose inner
// command wanted a heavy one would wait for itself.
func TestClassify_TscProjectOuterAndInnerAgree(t *testing.T) {
	for _, c := range []struct {
		outer, inner string
		want         Class
	}{
		{"pnpm exec tsc --noEmit -p .", "tsc --noEmit -p .", ClassLight},
		{"pnpm -C desktop exec tsc --noEmit -p .", "tsc --noEmit -p .", ClassLight},
		{"pnpm --dir desktop exec tsc --noEmit -p .", "tsc --noEmit -p .", ClassLight},
		{"pnpm --filter server exec tsc --noEmit -p .", "tsc --noEmit -p .", ClassLight},
		{"npx tsc --noEmit -p .", "tsc --noEmit -p .", ClassLight},
		{"npx tsc -p spa/tsconfig.json", "tsc -p spa/tsconfig.json", ClassLight},
		{"pnpm exec tsc --project=spa --noEmit", "tsc --project=spa --noEmit", ClassLight},
		// -b stays heavy on both sides, and so does a whole-project run
		{"pnpm exec tsc -b", "tsc -b", ClassHeavy},
		{"pnpm -C desktop exec tsc -b -p .", "tsc -b -p .", ClassHeavy},
		{"pnpm exec tsc --noEmit", "tsc --noEmit", ClassHeavy},
	} {
		outer, inner := Classify(words(c.outer), Env{}), Classify(words(c.inner), Env{})
		assert.Equal(t, c.want, inner, "inner: %s", c.inner)
		assert.Equal(t, c.want, outer, "outer: %s", c.outer)
		assert.Equal(t, outer, inner, "outer and inner of %q agree", c.outer)
	}
}

// A recursive run goes through every package: heavy, as it was; the tsc it starts in each package is light.
func TestClassify_TscProjectRecursiveStaysHeavy(t *testing.T) {
	assert.Equal(t, ClassHeavy, Classify(words("pnpm -r exec tsc --noEmit -p ."), Env{}))
	assert.Equal(t, ClassLight, Classify(words("tsc --noEmit -p ."), Env{}))
}

// A light slot held by the outer command lets the tsc it starts through, as a nested command with no slot of its own.
func TestRun_ALightOuterLetsTscProjectThrough(t *testing.T) {
	g, _ := testGate(t)
	lightOuter := startOuter(t, g, ClassLight, 1001)
	defer func() { lightOuter.release(); <-lightOuter.done }()
	// the other light slots are taken too: a nested command must not need one
	g.SetConfig(func(c *Config) { c.Slots.Light = 1 })

	ig := *g
	ig.PID = func() int { return 2002 }
	ig.Procs = descendantProcs(2002, 1001)
	require.Equal(t, ClassLight, Classify(words("tsc --noEmit -p ."), Env{}))
	code, ran := lightOuter.inner(t, ClassLight, 2002, descendantProcs(2002, 1001), lightOuter.env)
	assert.True(t, ran, "the only light slot is held by the outer command, and the inner one still runs")
	assert.Equal(t, 0, code)

	recs := readDecisions(t, g)
	var nested int
	for _, r := range recs {
		if r["nested"] == true {
			nested++
		}
	}
	assert.Equal(t, 1, nested, "the inner command is recorded as nested")
}

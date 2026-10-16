package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/gate"
)

// aliasCapture replaces gateRunMain with a recorder of the arguments the alias hands the gate.
func aliasCapture(t *testing.T, code int) *[][]string {
	t.Helper()
	var calls [][]string
	old := gateRunMain
	gateRunMain = func(args []string, _, _ io.Writer) int {
		calls = append(calls, append([]string(nil), args...))
		return code
	}
	// nothing in these tests may replace the test process with a real command
	oldExec := gateExec
	gateExec = func(string, []string, []string) error { return nil }
	t.Cleanup(func() { gateRunMain, gateExec = old, oldExec })
	return &calls
}

func TestLeaseGateClass(t *testing.T) {
	for _, c := range []struct {
		kind   string
		weight int
		want   gate.Class
	}{
		{"test-full", 0, gate.ClassHeavy},
		{"build", 0, gate.ClassNone}, // packaging runs at once
		{"test-pkg", 0, gate.ClassLight},
		{"lint-full", 0, gate.ClassLight},
		{"my-custom-kind", 0, gate.ClassHeavy}, // unknown: the side that waits
		{"", 35, gate.ClassHeavy},
		{"", 20, gate.ClassHeavy},
		{"", 19, gate.ClassLight},
		{"", 1, gate.ClassLight},
		{"", 0, gate.ClassHeavy},
	} {
		assert.Equal(t, c.want, leaseGateClass(c.kind, c.weight), "kind=%q weight=%d", c.kind, c.weight)
	}
}

// The alias hands the command to the gate with the mapped class, ignores the mode flags, says once that it is deprecated and
// returns the gate's exit code.
func TestLeaseRunAlias_RunsThroughTheGate(t *testing.T) {
	calls := aliasCapture(t, 5)
	oldRun := leaseRun // this one test goes through the dispatch, to the alias (the legacy tests point leaseRun at the old code)
	leaseRun = func(_ context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, _ []daemonclient.Option) int {
		return runLeaseRunAlias(args, getenv, stdout, stderr)
	}
	t.Cleanup(func() { leaseRun = oldRun })
	var out, errb bytes.Buffer
	code := runLeaseCmd(context.Background(), []string{"run", "--kind", "test-pkg", "--ask", "--client-id", "7b3c6a04-93a7-4d5c-8f0e-2f5a6a0c9b11", "--command", "shown", "--", "go", "test", "./a"}, func(string) string { return "" }, &out, &errb)
	assert.Equal(t, 5, code)
	assert.Equal(t, [][]string{{"--class", "light", "--class-source", "classifier", "--", "go", "test", "./a"}}, *calls)
	assert.Equal(t, 1, strings.Count(errb.String(), "別名"), "one deprecation line: %q", errb.String())
	assert.Contains(t, errb.String(), "--class light")
}

func TestLeaseRunAlias_KeepsTheConfigPathAndMapsHeavy(t *testing.T) {
	calls := aliasCapture(t, 0)
	var out, errb bytes.Buffer
	assert.Equal(t, 0, runLeaseRunAlias([]string{"--config", "/c.toml", "--kind", "test-full", "--wait", "5m", "--", "pnpm", "test"}, func(string) string { return "" }, &out, &errb))
	assert.Equal(t, [][]string{{"--class", "heavy", "--class-source", "classifier", "--config", "/c.toml", "--", "pnpm", "test"}}, *calls)
}

func TestLeaseRunAlias_UsageErrorsNeverReachTheGate(t *testing.T) {
	calls := aliasCapture(t, 0)
	var out, errb bytes.Buffer
	for _, args := range [][]string{
		{"--kind", "build"},                                // no command
		{"--kind", "build", "--bogus", "--", "x"},          // a flag it does not know
		{"--kind", "build", "stray", "--", "x"},            // a word before --
		{"--", "true"},                                     // neither --kind nor --weight
		{"--kind", "build", "--weight", "5", "--", "true"}, // both
		{"--weight", "-1", "--", "true"},                   // out of range
		{"--weight", "1000000", "--", "true"},
		{"--kind", "build", "--wait", "20m", "--", "true"},    // a wait out of range
		{"--kind", "build", "--ask", "--queue", "--", "true"}, // two modes
		{"--kind", "build", "--client-id", "nope", "--", "true"},
	} {
		errb.Reset()
		assert.Equal(t, ExitUsage, runLeaseRunAlias(args, func(string) string { return "" }, &out, &errb), strings.Join(args, " "))
		assert.Contains(t, errb.String(), "pdx lease:")
	}
	assert.Empty(t, *calls)
}

// `--kind build` is packaging (the deploy script builds the Purdex App with it): it runs at once, taking no slot, with the
// deprecation line and the command's own exit code.
func TestLeaseRunAlias_BuildRunsAtOnceWithoutASlot(t *testing.T) {
	calls := aliasCapture(t, 99)
	var execd struct {
		path string
		argv []string
	}
	old := gateExec
	gateExec = func(path string, argv []string, _ []string) error {
		execd.path, execd.argv = path, argv
		return nil
	}
	t.Cleanup(func() { gateExec = old })
	var out, errb bytes.Buffer
	code := runLeaseRunAlias([]string{"--kind", "build", "--queue", "--", "sh", "-c", "exit 3"}, func(string) string { return "" }, &out, &errb)
	assert.Equal(t, 0, code, "gateExec returned (a seam): nothing failed to start")
	assert.Empty(t, *calls, "no slot: the gate is not asked")
	assert.Equal(t, []string{"sh", "-c", "exit 3"}, execd.argv)
	assert.True(t, strings.HasSuffix(execd.path, "/sh"), execd.path)
	assert.Equal(t, 1, strings.Count(errb.String(), "別名"), "one deprecation line: %q", errb.String())
	assert.Contains(t, errb.String(), "不經過閘門")
	// a kind that is not packaging still goes through the gate
	*calls = nil
	runLeaseRunAlias([]string{"--kind", "test-full", "--", "go", "test", "./..."}, func(string) string { return "" }, &out, &errb)
	assert.Len(t, *calls, 1)
}

// The gate's classifier decides when it knows the command; the kind only when it does not (#2964): a vitest run of named files
// is light whatever the kind says, a script nobody told the gate about keeps the kind's pool.
func TestLeaseRunClass_ClassifierFirstKindOtherwise(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "none.toml") // no file: the gate's defaults
	files := []string{"pnpm", "exec", "vitest", "run", "test/x.test.ts"}
	whole := []string{"pnpm", "exec", "vitest", "run"}
	pkg := []string{"go", "test", "./internal/gate/"}
	all := []string{"go", "test", "./..."}
	script := []string{"./bin/test-baseline.sh"}
	for _, c := range []struct {
		kind    string
		command []string
		class   gate.Class
		source  string
	}{
		{"test-full", files, gate.ClassLight, "classifier"}, // the 11-minute queue of #2964
		{"vitest", files, gate.ClassLight, "classifier"},
		{"test-pkg", files, gate.ClassLight, "classifier"},
		{"test-full", whole, gate.ClassHeavy, "classifier"},
		{"test-pkg", whole, gate.ClassHeavy, "classifier"}, // a light kind does not make a whole run light
		{"test-full", pkg, gate.ClassLight, "classifier"},
		{"vitest", all, gate.ClassHeavy, "classifier"},
		{"test-full", []string{"pnpm", "check"}, gate.ClassHeavy, "classifier"},
		{"test-full", script, gate.ClassHeavy, "kind"}, // not known to the rules: the kind decides
		{"vitest", script, gate.ClassHeavy, "kind"},    // a kind nobody mapped is heavy
		{"test-pkg", script, gate.ClassLight, "kind"},
		{"lint-full", script, gate.ClassLight, "kind"},
		{"build", files, gate.ClassNone, "kind"}, // packaging is never held back, whatever the rules say
		{"build", all, gate.ClassNone, "kind"},
	} {
		class, source := leaseRunClass(c.kind, 0, c.command, cfg)
		assert.Equal(t, c.class, class, "%s %v", c.kind, c.command)
		assert.Equal(t, c.source, source, "%s %v", c.kind, c.command)
	}
	// --weight asks the same: the rules first, the weight for what they do not know
	class, source := leaseRunClass("", 35, files, cfg)
	assert.Equal(t, gate.ClassLight, class)
	assert.Equal(t, "classifier", source)
	class, source = leaseRunClass("", 5, script, cfg)
	assert.Equal(t, gate.ClassLight, class)
	assert.Equal(t, "kind", source)
}

// A gate that is off holds nothing, so its rules decide nothing: the kind maps as it always did.
func TestLeaseRunClass_AGateThatIsOffLeavesItToTheKind(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	assert.NoError(t, os.WriteFile(cfg, []byte("[gate]\nenabled = false\n"), 0o644))
	class, source := leaseRunClass("test-full", 0, []string{"pnpm", "exec", "vitest", "run", "a.test.ts"}, cfg)
	assert.Equal(t, gate.ClassHeavy, class)
	assert.Equal(t, "kind", source)
}

// What the alias hands the gate carries where the class came from, and the gate refuses a source it does not know.
func TestLeaseRunAlias_PassesTheClassSource(t *testing.T) {
	calls := aliasCapture(t, 0)
	cfg := filepath.Join(t.TempDir(), "none.toml")
	var out, errb bytes.Buffer
	for _, args := range [][]string{
		{"--config", cfg, "--kind", "test-full", "--", "pnpm", "exec", "vitest", "run", "a.test.ts"},
		{"--config", cfg, "--kind", "test-full", "--", "./bin/test-baseline.sh"},
	} {
		assert.Equal(t, 0, runLeaseRunAlias(args, func(string) string { return "" }, &out, &errb))
	}
	assert.Equal(t, [][]string{
		{"--class", "light", "--class-source", "classifier", "--config", cfg, "--", "pnpm", "exec", "vitest", "run", "a.test.ts"},
		{"--class", "heavy", "--class-source", "kind", "--config", cfg, "--", "./bin/test-baseline.sh"},
	}, *calls)
	var o, e bytes.Buffer
	assert.Equal(t, 2, gateMain([]string{"--class", "heavy", "--class-source", "guess", "--", "true"}, &o, &e))
}

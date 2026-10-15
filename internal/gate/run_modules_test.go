package gate

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lifetime: a command that holds its slot longer than alert_after_min is reported once and left running.
func TestRun_ALongRunIsReportedAndNotStopped(t *testing.T) {
	g, buf := testGate(t)
	g.SetConfig(func(c *Config) { c.Lifetime.AlertAfter = 80 * time.Millisecond })
	r := runSync(t, g, Options{Argv: sh("sleep 0.4; echo done"), Class: ClassHeavy})
	assert.Equal(t, 0, r.code)
	assert.Equal(t, "done\n", r.out, "it was not stopped")
	assert.Equal(t, 1, strings.Count(buf.String(), "has held its heavy slot"), "reported once")
}

// reap: with the module off, what the command leaves behind is left.
func TestRun_ReapOffLeavesTheLeftovers(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Reap.Enabled = false })
	dir := t.TempDir()
	r := runSync(t, g, Options{Argv: sh("sleep 300 & echo $! > " + dir + "/pid; exit 0"), Class: ClassHeavy})
	assert.Equal(t, 0, r.code)
	pid := pidFrom(t, dir+"/pid")
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	assert.False(t, gone(pid), "reap is off: the leftover runs on")
}

// the grace period of the module is the time between SIGTERM and SIGKILL (the gate's KillGrace is only a test seam).
func TestRun_ReapGraceComesFromTheModule(t *testing.T) {
	g, _ := testGate(t)
	g.KillGrace = 0
	g.SetConfig(func(c *Config) { c.Reap.Grace = 200 * time.Millisecond })
	dir := t.TempDir()
	start := time.Now()
	runSync(t, g, Options{Argv: sh("trap '' TERM; sleep 300 & echo $! > " + dir + "/pid; exit 0"), Class: ClassHeavy})
	took := time.Since(start)
	assert.True(t, gone(pidFrom(t, dir+"/pid")))
	assert.Less(t, took, 3*time.Second, "SIGKILL came after the 200 ms grace, not after the 5 s default")
}

// cache: the note is on stderr for a gated go test with -count=1, and strip takes it out of what runs.
func TestRun_TheCacheModuleWarnsOrStrips(t *testing.T) {
	g, buf := testGate(t)
	g.SetConfig(func(c *Config) { c.Cache.Count1 = "warn" })
	g.Tools = Tools{}
	runSync(t, g, Options{Argv: []string{"go", "test", "-count=1", "-run", "TestNothingMatchesThis", "./internal/gate/"}, Class: ClassLight, Real: "/usr/bin/true"})
	assert.Contains(t, buf.String(), "switches off Go's test cache")

	g2, buf2 := testGate(t)
	g2.SetConfig(func(c *Config) { c.Cache.Count1 = "strip" })
	dir := t.TempDir()
	// the real binary prints its words, so what ran can be read back
	script := dir + "/echoargs"
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755))
	r := runSync(t, g2, Options{Argv: []string{"go", "test", "-count=1", "./a", "./b"}, Class: ClassHeavy, Real: script})
	assert.Equal(t, "test ./a ./b\n", r.out)
	assert.Contains(t, buf2.String(), "taken out")

	r = runSync(t, g2, Options{Argv: []string{"go", "test", "-count=1", "./a", "./b"}, Class: ClassHeavy, Real: script, Foreground: true})
	assert.Equal(t, "test -count=1 ./a ./b\n", r.out, "--fg is the release gate: untouched")
}

// parallelism: the resolved numbers reach the command (GOFLAGS), and a gate whose module is off adds nothing.
func TestRun_TheParallelismModuleSetsGoflags(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) {
		c.Parallelism = parCfg("auto")
	})
	g.Facts = func() HostFacts { return HostFacts{NCPU: 10, AvailMemMB: 8000} }
	r := runSync(t, g, Options{Argv: sh(`echo "$GOFLAGS"`), Class: ClassHeavy})
	assert.Equal(t, "-p=8\n", r.out, "10 cores − 2 reserved")

	g.SetConfig(func(c *Config) { c.Parallelism = parCfg("off") })
	r = runSync(t, g, Options{Argv: sh(`echo "[$GOFLAGS]"`), Class: ClassHeavy})
	assert.Equal(t, "[]\n", r.out)
}

// intercept: the module's tool list and rules decide the class when the caller does not.
func TestRun_TheInterceptModuleDecidesTheClass(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) {
		c.Intercept.Rules = []Rule{{Match: "sh", Class: ClassHeavy}}
	})
	dir := t.TempDir()
	res := runAsync(g, t.Context(), Options{Argv: sh(holdScript(dir, "1"))})
	eventually(t, "start", func() bool { return exists(dir + "/started-1") })
	assert.Len(t, g.Status().Heavy, 1, "the rule made sh heavy")
	require.NoError(t, os.WriteFile(dir+"/go-1", nil, 0o644))
	assert.Equal(t, 0, (<-res).code)
}

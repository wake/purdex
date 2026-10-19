package gate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #3649: a command the gate lets through without a slot (the gate is off, or it runs below a held slot) does not make the gate directory
// for its decision line; where the directory exists already (any command that ever queued made it) the line is written as before. A
// fresh directory therefore keeps no line of such a command: that is the trade, so the hot path of a shim touches no file.

func TestDecision_ADisabledGateDoesNotMakeTheGateDirectory(t *testing.T) {
	g, _ := testGate(t)
	dir := filepath.Join(t.TempDir(), "fresh")
	g.SetConfig(func(c *Config) { c.Enabled = false; c.Dir = dir })
	assert.Equal(t, 0, runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy}).code)
	assert.NoDirExists(t, dir)
}

func TestDecision_ANestedLineDoesNotMakeTheGateDirectory(t *testing.T) {
	g, _ := testGate(t)
	dir := filepath.Join(t.TempDir(), "fresh")
	g.recordRun(dir, &Decision{Outcome: "nested", Nested: true, dir: dir})
	assert.NoDirExists(t, dir)
	g.recordRun(dir, &Decision{Outcome: "off", dir: dir})
	assert.NoDirExists(t, dir)
}

func TestDecision_WhereTheDirectoryExistsTheLineIsStillWritten(t *testing.T) {
	g, _ := testGate(t)
	dir := t.TempDir()
	g.SetConfig(func(c *Config) { c.Dir = dir })
	g.recordRun(dir, &Decision{Outcome: "nested", Nested: true, dir: dir})
	g.recordRun(dir, &Decision{Outcome: "off", dir: dir})
	b, err := os.ReadFile(filepath.Join(dir, decisionsFile))
	require.NoError(t, err)
	assert.Contains(t, string(b), `"nested":true`)
	assert.Contains(t, string(b), `"outcome":"off"`)
	// the outcomes that took a slot still make the directory
	fresh := filepath.Join(t.TempDir(), "fresh")
	g.recordRun(fresh, &Decision{Outcome: "slot", dir: fresh})
	assert.DirExists(t, fresh)
}

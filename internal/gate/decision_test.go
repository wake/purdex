package gate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readDecisions(t *testing.T, g *Gate) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(g.Config().Dir, decisionsFile))
	require.NoError(t, err)
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(l), &m), l)
		out = append(out, m)
	}
	return out
}

// Every command leaves one line: its class, slot, applied parameters with their source, exit code and duration.
func TestRun_EveryCommandLeavesADecisionRecord(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) {
		c.Priority.Mode = "nice"
		c.Priority.Nice = 7
		c.Sources = map[string]Source{"priority.nice": SrcProfile, "priority.mode": SrcConfig, "slots.heavy": SrcConfig}
	})
	r := runSync(t, g, Options{Argv: sh("exit 3"), Class: ClassHeavy})
	assert.Equal(t, 3, r.code)
	g.SetConfig(func(c *Config) { c.Enabled = false })
	runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy})

	recs := readDecisions(t, g)
	require.Len(t, recs, 2)
	assert.Equal(t, "slot", recs[0]["outcome"])
	assert.Equal(t, "heavy", recs[0]["class"])
	assert.Equal(t, "heavy-1", recs[0]["slot"])
	assert.EqualValues(t, 3, recs[0]["exit"])
	assert.NotNil(t, recs[0]["duration_s"])
	applied := recs[0]["applied"].(map[string]any)
	assert.Equal(t, map[string]any{"value": "7", "source": "profile"}, applied["priority.nice"])
	assert.Equal(t, map[string]any{"value": "2", "source": "config"}, applied["slots.heavy"])
	assert.Equal(t, "off", recs[1]["outcome"])
}

// A command that waits says how long and why.
func TestRun_TheDecisionRecordSaysWhyItWaited(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	hold := runAsync(g, context.Background(), Options{Argv: sh("sleep 0.4"), Class: ClassHeavy})
	eventually(t, "the first command to hold the slot", func() bool { return len(g.Status().Heavy) == 1 })
	r := runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy})
	<-hold
	assert.Equal(t, 0, r.code)
	recs := readDecisions(t, g)
	require.Len(t, recs, 2)
	waited := recs[1]
	assert.Equal(t, []any{"queue"}, waited["wait_reasons"])
	assert.Greater(t, waited["wait_s"].(float64), 0.0)
}

// A decision file that cannot be written does not stop the command.
func TestRun_ADecisionThatCannotBeWrittenIsNotAnError(t *testing.T) {
	g, _ := testGate(t)
	require.NoError(t, os.MkdirAll(filepath.Join(g.Config().Dir, decisionsFile), 0o755)) // a directory where the file goes
	r := runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy})
	assert.Equal(t, 0, r.code)
}

// Past the size limit the file is moved aside and a new one starts.
func TestDecisions_AreRotated(t *testing.T) {
	g, _ := testGate(t)
	dir := g.Config().Dir
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, decisionsFile), make([]byte, decisionsMaxSize+1), 0o644))
	g.record(dir, &Decision{Argv: []string{"x"}})
	_, err := os.Stat(filepath.Join(dir, decisionsFile+".1"))
	assert.NoError(t, err)
	b, _ := os.ReadFile(filepath.Join(dir, decisionsFile))
	assert.Less(t, len(b), 4096)
}

// status lists the processes that hold a slot's lock file.
func TestStatus_NamesWhoHoldsTheLock(t *testing.T) {
	g, _ := testGate(t)
	g.Holders = func(string) []Straggler { return []Straggler{{PID: 77, Cmd: "tmux"}} }
	hold := runAsync(g, context.Background(), Options{Argv: sh("sleep 0.5"), Class: ClassHeavy})
	eventually(t, "a held slot", func() bool { return len(g.Status().Heavy) == 1 })
	st := g.Status()
	<-hold
	assert.Equal(t, []Straggler{{PID: 77, Cmd: "tmux"}}, st.Heavy[0].Locked)
	assert.NotEmpty(t, st.Settings)
}

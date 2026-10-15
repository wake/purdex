package gate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A setting changed while the command waits is the one it starts with, and the record says so (review of G1b).
func TestRun_ASettingChangedWhileWaitingIsTheOneInForce(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) {
		c.Slots.Heavy = 1
		c.Priority.Mode = "nice"
		c.Priority.Nice = 10
		c.Sources = map[string]Source{"priority.nice": SrcDefault, "priority.mode": SrcDefault}
	})
	g.Tools = Tools{Nice: "/usr/bin/nice"}
	hold := runAsync(g, context.Background(), Options{Argv: sh("sleep 0.6"), Class: ClassHeavy})
	eventually(t, "the first command to hold the slot", func() bool { return len(g.Status().Heavy) == 1 })
	waiter := runAsync(g, context.Background(), Options{Argv: sh("true"), Class: ClassHeavy})
	eventually(t, "the second to queue", func() bool { return g.Status().WaitHeavy == 1 })
	g.SetConfig(func(c *Config) {
		c.Priority.Nice = 4
		c.Sources["priority.nice"] = SrcProfile
	})
	<-hold
	<-waiter
	recs := readDecisions(t, g)
	require.Len(t, recs, 2)
	applied := recs[1]["applied"].(map[string]any)
	assert.Equal(t, map[string]any{"value": "4", "source": "profile"}, applied["priority.nice"])
}

package gate

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Options.WaitedOut says whether the wait ran out, which exit 75 alone does not: a refusal for another cause exits 75 too.
// Mutation gate: WaitedOut true on every exit 75 → the refusal case goes red.
func TestRun_WaitedOutTellsAWaitThatRanOutFromOtherRefusals(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	var waited bool
	code, _ := g.Run(context.Background(), Options{Argv: sh("true"), Class: ClassHeavy, Env: cleanEnv(), WaitMax: time.Minute, WaitedOut: &waited, Stdout: &bytes.Buffer{}})
	assert.Equal(t, 0, code)
	assert.False(t, waited, "an admitted command did not wait out")

	release, holder := holderOf(t, g)
	waited = false
	code, _ = g.Run(context.Background(), Options{Argv: sh("true"), Class: ClassHeavy, Env: cleanEnv(), WaitMax: 200 * time.Millisecond, WaitedOut: &waited, Stdout: &bytes.Buffer{}})
	assert.Equal(t, ExitRefused, code)
	assert.True(t, waited, "the wait ran out")
	release()
	<-holder

	g.SetConfig(func(c *Config) { c.Admission.DiskMinGB = 1 << 20 })
	waited = true
	code, _ = g.Run(context.Background(), Options{Argv: sh("true"), Class: ClassHeavy, Env: cleanEnv(), WaitMax: time.Minute, WaitedOut: &waited, Stdout: &bytes.Buffer{}})
	assert.Equal(t, ExitRefused, code, "the disk floor refuses at once")
	assert.False(t, waited, "a refusal that was not the wait is not the wait running out")
}

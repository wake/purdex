package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
)

// wwdalertsStub is a module named "wwdalerts" that reports a status, like the real one.
type wwdalertsStub struct {
	stubModule
	status map[string]any
}

func (w *wwdalertsStub) Status() map[string]any { return w.status }

// GET /api/info carries the wwdalerts module's own status under "wwdalerts" (docs/wwd-deploy.md §7), and null while the module is not
// mounted: the key is always there, so a reader tells "not mounted" from "an older daemon that does not report it" only by its being null
// rather than missing.
func TestHandleInfo_ReportsTheWwdalertsStatus(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	body := infoOf(t, c)
	v, present := body["wwdalerts"]
	require.True(t, present, "the key is always in the answer")
	assert.Nil(t, v, "null while the module is not mounted")

	c.AddModule(&wwdalertsStub{stubModule: stubModule{name: "wwdalerts"}, status: map[string]any{"push": "off", "deliverer": true, "suppressed": 2}})
	got, ok := infoOf(t, c)["wwdalerts"].(map[string]any)
	require.True(t, ok, "an object once the module is mounted")
	assert.Equal(t, "off", got["push"])
	assert.Equal(t, true, got["deliverer"])
	assert.EqualValues(t, 2, got["suppressed"])
}

// A module that is mounted but has no Status (not a StatusReporter) is null too, not an empty object that reads as "reporting nothing".
func TestHandleInfo_WwdalertsWithoutAStatusIsNull(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	c.AddModule(&stubModule{name: "wwdalerts"})
	assert.Nil(t, infoOf(t, c)["wwdalerts"])
}

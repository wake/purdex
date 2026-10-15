package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// release needs no table: UseProfile calls it known, and it runs as --fg (#2941).
func TestUseProfile_ReleaseIsKnownWithoutATable(t *testing.T) {
	p := cfgFile(t, "[gate.slots]\nheavy = 1\n")
	known, err := UseProfile(p, ReleaseProfile)
	require.NoError(t, err)
	assert.True(t, known)
	cfg, _ := Load(p)
	assert.Equal(t, ReleaseProfile, cfg.Profile)
	assert.True(t, cfg.foreground(Options{}))
}

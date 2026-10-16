package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A bad value is refused even while a profile that overrides the key is active.
func TestSet_ABadValueIsNotHiddenByTheActiveProfile(t *testing.T) {
	p := cfgFile(t, "[gate]\nprofile = \"night\"\n[gate.profiles.night.slots]\nheavy = 3\n")
	before := read(t, p)
	_, err := Set(p, "", "slots.heavy", "0")
	require.Error(t, err)
	assert.Equal(t, before, read(t, p))
	res, err := Set(p, "", "slots.heavy", "2")
	require.NoError(t, err)
	assert.Equal(t, "3", res.New, "the profile still wins, and the report says so")
	assert.Equal(t, SrcProfile, res.NewSrc)
}

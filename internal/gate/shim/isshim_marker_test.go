package shim

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A wrapper that only mentions the marker (not on its second line) is a real binary, not a shim.
func TestIsShim_MarkerElsewhereIsNotAShim(t *testing.T) {
	p := filepath.Join(t.TempDir(), "go")
	body := "#!/bin/sh\n# real wrapper\n# see: " + shimMarker + " go) in the docs\nexec /real/go \"$@\"\n"
	require.NoError(t, os.WriteFile(p, []byte(body), 0o755))
	assert.False(t, IsShim(p))
	require.NoError(t, os.WriteFile(p, []byte{0, 1, 2, 3}, 0o755))
	assert.False(t, IsShim(p))
}

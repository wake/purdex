//go:build !darwin

package qos

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Off macOS there is no taskpolicy: the command runs as it is, and the caller is told it is not in the background.
func TestBackground_IsAPassThroughOffMacOS(t *testing.T) {
	argv := []string{"/bin/echo", "x"}
	w, ok := Background(argv)
	assert.False(t, ok)
	assert.Equal(t, argv, w)
	w, bg := For(wire.ClassDev, true, argv)
	assert.False(t, bg)
	assert.Equal(t, argv, w)
}

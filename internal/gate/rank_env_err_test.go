package gate

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A born-back enqueue in a directory that denies writes stays "unavailable" (the gate then runs the command without a slot): the
// error is not turned into "broken" by being sorted twice (Enqueue's fail sorts what lockAdmit and writeRankFile already sorted).
func TestRankEnv_UnavailableStaysUnavailable(t *testing.T) {
	un := dirError(&os.PathError{Op: "open", Path: "x", Err: syscall.EACCES})
	require.ErrorIs(t, un, ErrUnavailable)
	assert.ErrorIs(t, dirError(un), ErrUnavailable)
	assert.NotErrorIs(t, dirError(un), ErrBroken)
	assert.ErrorIs(t, dirError(os.ErrNotExist), ErrBroken)
}

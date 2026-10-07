package terminal

import (
	"errors"
	"testing"

	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
)

// A failed Setsize must not be remembered as applied: the next tick has to try
// the same size again, or the PTY stays stale for good (and a stale mirror PTY
// can become tmux's sizing source once the desktop client leaves).
func TestApplyPTYSize_RetriesAfterFailure(t *testing.T) {
	cur := pty.Winsize{Cols: 100, Rows: 31}
	want := pty.Winsize{Cols: 100, Rows: 32}
	var calls int
	fail := true
	set := func(w pty.Winsize) error {
		calls++
		if fail {
			return errors.New("ioctl: boom")
		}
		return nil
	}

	applyPTYSize(&cur, want, set)
	assert.Equal(t, 1, calls)
	assert.Equal(t, uint16(31), cur.Rows, "failure keeps the old applied size")

	fail = false
	applyPTYSize(&cur, want, set)
	assert.Equal(t, 2, calls, "same target is retried")
	assert.Equal(t, uint16(32), cur.Rows)

	applyPTYSize(&cur, want, set)
	assert.Equal(t, 2, calls, "an applied size is not re-applied")
}

package gate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A ticket whose waiter is gone is not counted and is not removed: would-wait writes nothing (review).
func TestWouldWait_ADeadTicketIsNeitherCountedNorRemoved(t *testing.T) {
	g, _ := testGate(t)
	p := g.pool(g.Config(), ClassHeavy)
	tk, err := p.Enqueue()
	require.NoError(t, err)
	files, _ := filepath.Glob(filepath.Join(p.queueDir(), "*"))
	require.Len(t, files, 1)
	tk.Drop()
	require.NoError(t, os.WriteFile(files[0], nil, 0o644)) // put back as a file nobody holds: a dead ticket
	w := g.WouldWait(ClassHeavy)
	assert.False(t, w.Wait)
	assert.Equal(t, 0, w.Ahead)
	_, err = os.Stat(files[0])
	assert.NoError(t, err, "the dead ticket is still there")
	assert.Equal(t, 0, p.Waiting())
	_, err = os.Stat(files[0])
	assert.Error(t, err, "Waiting (status) does clean it")
}

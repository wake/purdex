package gate

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gates that find the file too big at the same moment rotate it once: no line lands in the archive or is lost.
func TestDecisions_ConcurrentRotationLosesNothing(t *testing.T) {
	for round := 0; round < 20; round++ {
		g, _ := testGate(t)
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, decisionsFile), make([]byte, decisionsMaxSize+1), 0o644))
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				g.record(dir, &Decision{Argv: []string{"x"}, Outcome: "slot"})
			}()
		}
		close(start)
		wg.Wait()
		b, err := os.ReadFile(filepath.Join(dir, decisionsFile))
		require.NoError(t, err)
		assert.Equal(t, 12, len(strings.Split(strings.TrimSpace(string(b)), "\n")), "round %d", round)
	}
}

package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cache that Read would reuse gives Peek the same pressure as Read, even when the kernel level has moved since.
func TestHostReader_PeekAgreesWithReadOnAFreshCache(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	samples := sampleFile{Samples: []cachedSample{
		{At: now.Add(-5 * time.Second).UnixMilli(), Level: 1, Pageouts: 0},
		{At: now.Add(-1 * time.Second).UnixMilli(), Level: 1, Pageouts: 800},
	}}
	b, _ := json.Marshal(samples)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pressure.json"), b, 0o644))
	r := &HostReader{Dir: dir, Now: func() time.Time { return now }, Level: func() (int, error) { return 4, nil },
		Pages: func() (uint64, uint64, error) { t.Fatal("no sampling"); return 0, 0, nil }}
	read, err := r.Read()
	require.NoError(t, err)
	peek, have := r.Peek()
	assert.True(t, have)
	assert.Equal(t, read, peek)
	assert.Equal(t, 1, peek.Level, "the cached level, as Read has it")
}

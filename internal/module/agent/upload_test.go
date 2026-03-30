package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeduplicateFilename(t *testing.T) {
	dir := t.TempDir()

	// No conflict — returns original name.
	got := deduplicateFilename(dir, "photo.png")
	assert.Equal(t, "photo.png", got)

	// Create file to trigger conflict.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "photo.png"), []byte("x"), 0644))
	got = deduplicateFilename(dir, "photo.png")
	assert.Equal(t, "photo-1.png", got)

	// Second conflict.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "photo-1.png"), []byte("x"), 0644))
	got = deduplicateFilename(dir, "photo.png")
	assert.Equal(t, "photo-2.png", got)

	// No extension.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0644))
	got = deduplicateFilename(dir, "README")
	assert.Equal(t, "README-1", got)
}

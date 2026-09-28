package fsutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateDedupFile(t *testing.T) {
	dir := t.TempDir()

	// No conflict — returns original name and creates the file.
	f, got, err := CreateDedupFile(dir, "photo.png")
	require.NoError(t, err)
	f.Close()
	assert.Equal(t, "photo.png", got)

	// File already exists (created above) — should return "photo-1.png".
	f, got, err = CreateDedupFile(dir, "photo.png")
	require.NoError(t, err)
	f.Close()
	assert.Equal(t, "photo-1.png", got)

	// Second conflict — "photo-1.png" now exists too, expect "photo-2.png".
	f, got, err = CreateDedupFile(dir, "photo.png")
	require.NoError(t, err)
	f.Close()
	assert.Equal(t, "photo-2.png", got)

	// No extension.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0644))
	f, got, err = CreateDedupFile(dir, "README")
	require.NoError(t, err)
	f.Close()
	assert.Equal(t, "README-1", got)
}

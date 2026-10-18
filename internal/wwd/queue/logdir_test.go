package queue

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Nobody else makes the log directory: an upload into one that is not there was a 500, and the worker went on retrying it.
func TestNewMakesTheLogDirectoryAndTheUploadWorks(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(t.TempDir(), "data", "joblogs") // neither level is there
	q, err := New(f.db, Options{Origin: "mlab", Now: f.clk.Now, LogDir: dir, Pinner: f.pins})
	require.NoError(t, err)
	fi, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, fi.IsDir())
	require.Equal(t, os.FileMode(0o700), fi.Mode().Perm())

	// a log goes into the directory the queue made
	f.q = q
	id, c := f.running(t)
	require.NoError(t, f.putLog(t, id, c.ClaimID, "hello\n"))
	require.Equal(t, "hello\n", f.readLog(t, id))
}

func TestNewKeepsAnExistingLogDirectory(t *testing.T) {
	db := openDB(t)
	require.NoError(t, Migrate(context.Background(), db))
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep"), []byte("x"), 0o600))
	_, err := New(db, Options{Origin: "mlab", LogDir: dir})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "keep"))
	require.NoError(t, err)
}

func TestNewRefusesALogDirectoryThatIsALink(t *testing.T) {
	db := openDB(t)
	require.NoError(t, Migrate(context.Background(), db))
	real, base := t.TempDir(), t.TempDir()
	link := filepath.Join(base, "joblogs")
	require.NoError(t, os.Symlink(real, link))
	_, err := New(db, Options{Origin: "mlab", LogDir: link})
	require.Error(t, err)
}

func TestNewRefusesALogDirectoryThatIsAFile(t *testing.T) {
	db := openDB(t)
	require.NoError(t, Migrate(context.Background(), db))
	file := filepath.Join(t.TempDir(), "joblogs")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	_, err := New(db, Options{Origin: "mlab", LogDir: file})
	require.Error(t, err)
}

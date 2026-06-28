package backup

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// openTestBackupStore opens an in-memory BackupStore for ordinary unit tests.
func openTestBackupStore(t *testing.T) *BackupStore {
	t.Helper()
	s, err := OpenBackup(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

// openTempBackupStore opens a file-backed BackupStore (MaxOpenConns(2)) for
// concurrency/serialisation tests — a :memory: store pins MaxOpenConns(1) and
// never exercises real SQLite writer contention.
func openTempBackupStore(t *testing.T) *BackupStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.db")
	s, err := OpenBackup(path)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenBackup_SchemaPresent(t *testing.T) {
	s := openTestBackupStore(t)

	for _, name := range []string{"backup_blobs", "backup_snapshots"} {
		var got string
		err := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name,
		).Scan(&got)
		require.NoError(t, err, "table %s should exist", name)
		require.Equal(t, name, got)
	}

	var idx string
	err := s.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_snap_store'`,
	).Scan(&idx)
	require.NoError(t, err, "idx_snap_store should exist")
	require.Equal(t, "idx_snap_store", idx)
}

func TestOpenBackup_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.db")

	s1, err := OpenBackup(path)
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	// Re-opening the same path runs migrate again (CREATE TABLE IF NOT EXISTS)
	// with no error and no duplicate schema objects.
	s2, err := OpenBackup(path)
	require.NoError(t, err)
	t.Cleanup(func() { s2.Close() })

	var tableCount int
	err = s2.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('backup_blobs','backup_snapshots')`,
	).Scan(&tableCount)
	require.NoError(t, err)
	require.Equal(t, 2, tableCount)
}

func TestGCStub_NoError(t *testing.T) {
	s := openTestBackupStore(t)
	require.NoError(t, s.GC(s.now()))
}

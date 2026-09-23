// internal/store/meta_test.go
package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/store"
	_ "modernc.org/sqlite"
)

func TestMetaStoreGetSetDelete(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	// Initially empty
	metas, err := ms.ListMeta()
	require.NoError(t, err)
	assert.Empty(t, metas)

	// Set meta
	err = ms.SetMeta("$0", store.SessionMeta{Mode: "terminal"})
	require.NoError(t, err)

	// Get meta
	meta, err := ms.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, "terminal", meta.Mode)

	// Update meta
	mode := "stream"
	cwd := "/home/dev/project"
	err = ms.UpdateMeta("$0", store.MetaUpdate{Mode: &mode, Cwd: &cwd})
	require.NoError(t, err)
	meta, _ = ms.GetMeta("$0")
	assert.Equal(t, "stream", meta.Mode)
	assert.Equal(t, "/home/dev/project", meta.Cwd)

	// Delete meta
	err = ms.DeleteMeta("$0")
	require.NoError(t, err)
	meta, _ = ms.GetMeta("$0")
	assert.Nil(t, meta)
}

func TestMetaStoreGetMissing(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	meta, err := ms.GetMeta("$999")
	require.NoError(t, err)
	assert.Nil(t, meta)
}

func TestMetaStoreCleanOrphans(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	ms.SetMeta("$0", store.SessionMeta{Mode: "terminal"})
	ms.SetMeta("$1", store.SessionMeta{Mode: "stream"})
	ms.SetMeta("$2", store.SessionMeta{Mode: "terminal"})
	removed, err := ms.CleanOrphans([]string{"$0", "$2"})
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	meta, _ := ms.GetMeta("$1")
	assert.Nil(t, meta, "$1 should be cleaned")
}

func TestMetaStoreCleanOrphansEmptySlice(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	// Populate some meta
	ms.SetMeta("$0", store.SessionMeta{Mode: "terminal", Cwd: "/abc"})
	ms.SetMeta("$1", store.SessionMeta{Mode: "stream", Cwd: "/def"})

	// Empty liveTmuxIDs means tmux unavailable — should NOT delete anything
	removed, err := ms.CleanOrphans([]string{})
	require.NoError(t, err)
	assert.Equal(t, 0, removed)

	// Both records must survive
	m0, _ := ms.GetMeta("$0")
	m1, _ := ms.GetMeta("$1")
	assert.NotNil(t, m0, "$0 should survive empty-slice cleanup")
	assert.NotNil(t, m1, "$1 should survive empty-slice cleanup")
}

func TestMetaStoreCleanOrphansNilSlice(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	ms.SetMeta("$0", store.SessionMeta{Mode: "terminal"})

	// nil also means tmux unavailable
	removed, err := ms.CleanOrphans(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, removed)

	m0, _ := ms.GetMeta("$0")
	assert.NotNil(t, m0, "$0 should survive nil-slice cleanup")
}

func TestMetaStoreResetStaleModes(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	ms.SetMeta("$0", store.SessionMeta{Mode: "stream"})
	ms.SetMeta("$1", store.SessionMeta{Mode: "term"})
	ms.SetMeta("$2", store.SessionMeta{Mode: "jsonl"})
	err = ms.ResetStaleModes()
	require.NoError(t, err)
	m0, _ := ms.GetMeta("$0")
	m1, _ := ms.GetMeta("$1")
	m2, _ := ms.GetMeta("$2")
	assert.Equal(t, "terminal", m0.Mode)
	assert.Equal(t, "terminal", m1.Mode)
	assert.Equal(t, "terminal", m2.Mode)
}

// TestMetaStore_OldSchemaWithExtraColumns: P-D.2 dropped cc_session_id and
// cc_model from the session_meta schema, but the alpha rule is "no persist
// migration" — a live meta.db created before P-D.2 still carries both
// columns with an empty-string default. Every statement must keep working against that
// table: inserts that omit the columns fall back to the defaults, selects
// name their columns explicitly, and partial updates never touch them.
func TestMetaStore_OldSchemaWithExtraColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")

	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec(`
		CREATE TABLE session_meta (
			tmux_id       TEXT PRIMARY KEY,
			mode          TEXT DEFAULT 'terminal',
			cc_session_id TEXT DEFAULT '',
			cc_model      TEXT DEFAULT '',
			cwd           TEXT DEFAULT '',
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	require.NoError(t, err)
	// A pre-existing row written by the old daemon, with the legacy columns
	// populated, must still be readable.
	_, err = raw.Exec(`
		INSERT INTO session_meta (tmux_id, mode, cc_session_id, cc_model, cwd)
		VALUES ('$7', 'stream', 'old-cc-session', 'opus', '/old')
	`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	ms, err := store.OpenMeta(path)
	require.NoError(t, err, "OpenMeta must accept the old schema")
	defer ms.Close()

	// SetMeta (insert + upsert)
	require.NoError(t, ms.SetMeta("$0", store.SessionMeta{TmuxID: "$0", Mode: "terminal", Cwd: "/new"}))
	require.NoError(t, ms.SetMeta("$0", store.SessionMeta{TmuxID: "$0", Mode: "terminal", Cwd: "/new2"}))

	// GetMeta
	got, err := ms.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "terminal", got.Mode)
	assert.Equal(t, "/new2", got.Cwd)

	old, err := ms.GetMeta("$7")
	require.NoError(t, err)
	require.NotNil(t, old)
	assert.Equal(t, "stream", old.Mode)
	assert.Equal(t, "/old", old.Cwd)

	// UpdateMeta
	mode := "terminal"
	cwd := "/updated"
	require.NoError(t, ms.UpdateMeta("$7", store.MetaUpdate{Mode: &mode, Cwd: &cwd}))
	old, err = ms.GetMeta("$7")
	require.NoError(t, err)
	require.NotNil(t, old)
	assert.Equal(t, "terminal", old.Mode)
	assert.Equal(t, "/updated", old.Cwd)

	// ListMeta
	all, err := ms.ListMeta()
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "$0", all[0].TmuxID)
	assert.Equal(t, "$7", all[1].TmuxID)

	// The legacy columns are untouched by the new statements.
	raw, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	defer raw.Close()
	var ccID, ccModel string
	require.NoError(t, raw.QueryRow(`SELECT cc_session_id, cc_model FROM session_meta WHERE tmux_id = '$7'`).Scan(&ccID, &ccModel))
	assert.Equal(t, "old-cc-session", ccID)
	assert.Equal(t, "opus", ccModel)
}

// #1293: the session-list chain reads the meta DB under the list's deadline.
// A context that has ended fails the read with an error wrapping ctx.Err()
// and changes nothing.
func TestMetaStoreContextReadsHonourContext(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	require.NoError(t, ms.SetMeta("$1", store.SessionMeta{Mode: "terminal"}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = ms.GetMetaContext(ctx, "$1")
	assert.ErrorIs(t, err, context.Canceled)
	_, err = ms.CleanOrphansContext(ctx, []string{"$2"})
	assert.ErrorIs(t, err, context.Canceled)

	got, err := ms.GetMetaContext(context.Background(), "$1")
	require.NoError(t, err)
	require.NotNil(t, got, "a cancelled CleanOrphans must not have deleted anything")
	n, err := ms.CleanOrphansContext(context.Background(), []string{"$2"})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestMetaStoreDeleteMetaContext(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	require.NoError(t, ms.SetMeta("$1", store.SessionMeta{Mode: "terminal"}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, ms.DeleteMetaContext(ctx, "$1"), context.Canceled)
	got, err := ms.GetMetaContext(context.Background(), "$1")
	require.NoError(t, err)
	require.NotNil(t, got, "a cancelled DeleteMeta must not have deleted anything")

	require.NoError(t, ms.DeleteMetaContext(context.Background(), "$1"))
	got, err = ms.GetMetaContext(context.Background(), "$1")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// holdMetaLock opens a second connection to the meta DB at path and holds its
// write lock until release is called, so a write on the MetaStore
// waits in SQLite's busy handler — a query that has already STARTED when its
// context ends.
func holdMetaLock(t *testing.T, path string) (release func()) {
	t.Helper()
	raw, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)")
	require.NoError(t, err)
	conn, err := raw.Conn(context.Background())
	require.NoError(t, err)
	for _, stmt := range []string{
		`BEGIN IMMEDIATE`,
		`UPDATE session_meta SET mode = mode`,
	} {
		_, err = conn.ExecContext(context.Background(), stmt)
		require.NoError(t, err, stmt)
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		conn.Close()
		raw.Close()
	}
}

// #1293: a statement already running when its context ends can fail with the
// driver's own error ("interrupted", "database is locked") instead of
// ctx.Err(). The meta calls on the session read chain still report it as the
// context's error, so a caller can tell a deadline from a DB fault.
func TestMetaStoreContextCancelMidQueryWrapsCtxErr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")
	ms, err := store.OpenMeta(path)
	require.NoError(t, err)
	defer ms.Close()
	require.NoError(t, ms.SetMeta("$1", store.SessionMeta{Mode: "terminal"}))

	ops := []struct {
		name string
		run  func(ctx context.Context) error
	}{
		// GetMetaContext is not here: under WAL a reader is never made to
		// wait by a writer, so there is no way to hold a read mid-query. It
		// goes through the same ctxErr wrapping as the two writes.
		{"CleanOrphansContext", func(ctx context.Context) error {
			_, err := ms.CleanOrphansContext(ctx, []string{"$2"})
			return err
		}},
		{"DeleteMetaContext", func(ctx context.Context) error {
			return ms.DeleteMetaContext(ctx, "$1")
		}},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			release := holdMetaLock(t, path)
			defer release()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timer := time.AfterFunc(50*time.Millisecond, cancel)
			defer timer.Stop()
			err := op.run(ctx)
			require.Error(t, err)
			assert.ErrorIs(t, err, context.Canceled, "got %v", err)
		})
	}
}

// The driver maps the cancel above to ctx.Err() itself (modernc v1.54 checks
// its interrupt flag), so that test holds even without the store's own
// wrapping. This one pins the store's contract regardless of the driver: when
// a meta call on the read chain fails for a reason that is NOT ctx.Err()
// (here database/sql's "database is closed", which it reports before looking
// at ctx) while ctx has ended, the error still satisfies errors.Is(ctx.Err())
// and keeps the driver's message for diagnosis.
func TestMetaStoreContextFailureWithEndedCtxWrapsCtxErr(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	require.NoError(t, ms.Close())

	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	<-ctx.Done()

	_, err = ms.GetMetaContext(ctx, "$1")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "GetMetaContext: %v", err)
	assert.ErrorContains(t, err, "closed")
	_, err = ms.CleanOrphansContext(ctx, []string{"$2"})
	assert.ErrorIs(t, err, context.DeadlineExceeded, "CleanOrphansContext: %v", err)
	assert.ErrorContains(t, err, "closed")
	err = ms.DeleteMetaContext(ctx, "$1")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "DeleteMetaContext: %v", err)
	assert.ErrorContains(t, err, "closed")

	// A failure with a live ctx is passed through unchanged.
	_, err = ms.GetMetaContext(context.Background(), "$1")
	require.Error(t, err)
	assert.NotErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, context.Canceled)
}

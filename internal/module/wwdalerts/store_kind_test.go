package wwdalerts

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A ledger made before the kind was kept opens, keeps its rows, and reads their kind as "the wwd only" (what every silence said then);
// opening it again changes nothing.
func TestAnOlderLedgerGainsTheKindColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wwdalerts.db")
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = old.Exec(`CREATE TABLE deliveries (
		host TEXT NOT NULL, key TEXT NOT NULL, since_ms INTEGER NOT NULL,
		attempt_ms INTEGER NOT NULL DEFAULT 0, sent_ms INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (host, key));
		INSERT INTO deliveries (host, key, since_ms, attempt_ms, sent_ms) VALUES ('(silence)', 'silence:mserv', 1, 2, 3);`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	for i := 0; i < 2; i++ { // the second open finds the column there
		s, err := openStore(path)
		require.NoError(t, err)
		d, ok := s.delivery("(silence)", "silence:mserv")
		require.True(t, ok)
		assert.Equal(t, delivery{Since: 1, Attempt: 2, Sent: 3}, d)
		hd, ok := s.toldKind("(silence)", "silence:mserv")
		assert.True(t, ok)
		assert.Equal(t, i == 1, hd, "the first open reads the default (the wwd only); the second finds what the first wrote")
		require.NoError(t, s.setKind("(silence)", "silence:mserv", time.UnixMilli(1), true))
		hd, _ = s.toldKind("(silence)", "silence:mserv")
		assert.True(t, hd)
		_, ok = s.toldKind("(silence)", "silence:none")
		assert.False(t, ok)
		require.NoError(t, s.close())
	}
}

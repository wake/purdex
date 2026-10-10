package backup

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// The store is released by Close (after the HTTP server drained), not by Stop: a request in flight during shutdown must
// not meet a closed database (#1033). Mutation gate: Stop closes the store again → red.
func TestStopLeavesTheStoreOpenAndCloseReleasesIt(t *testing.T) {
	m := New()
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})))
	var _ core.Closer = m
	st := m.store

	require.NoError(t, m.Stop(context.Background()))
	require.NoError(t, st.db.Ping(), "Stop closed the store while HTTP can still be serving")

	require.NoError(t, m.Close())
	require.Error(t, st.db.Ping(), "Close must release the store")
	require.NoError(t, m.Close(), "Close is idempotent")
}

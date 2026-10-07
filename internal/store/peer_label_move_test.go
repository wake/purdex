package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lead-team-relay spec §8.4: the title moves to the new session id. The
// old row is released (kept, label NULL), the new row carries the label
// with a fresh rev, and a second Move is a no-op (the boot reconciliation
// re-runs it for a cleared op).
func TestPeerLabels_MoveCarriesLabelOnce(t *testing.T) {
	ms, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	ls := ms.PeerLabels()
	now := time.UnixMilli(1000)

	_, err = ls.Claim("sid-old", "purdex-tester", now) // rev 1
	require.NoError(t, err)

	moved, err := ls.Move("sid-old", "sid-new", time.UnixMilli(2000))
	require.NoError(t, err)
	assert.True(t, moved)
	rows, err := ls.Snapshot()
	require.NoError(t, err)
	byID := map[string]PeerLabel{}
	for _, r := range rows {
		byID[r.SessionID] = r
	}
	require.Len(t, byID, 2)
	assert.Equal(t, "purdex-tester", byID["sid-new"].Label)
	assert.Equal(t, int64(2), byID["sid-new"].Rev)
	assert.Equal(t, "", byID["sid-old"].Label, "the old row is released, not deleted")
	assert.Equal(t, int64(3), byID["sid-old"].Rev)
	assert.Equal(t, int64(2000), byID["sid-old"].SetAt.UnixMilli())

	// Idempotent: the old row has no label now, so nothing moves and no rev is spent.
	moved, err = ls.Move("sid-old", "sid-new", time.UnixMilli(3000))
	require.NoError(t, err)
	assert.False(t, moved)
	rows, _ = ls.Snapshot()
	for _, r := range rows {
		if r.SessionID == "sid-new" {
			assert.Equal(t, int64(2), r.Rev, "a no-op move must not bump the new row")
		}
	}

	// No row at all, same id, or empty ids: false, no error.
	for _, c := range [][2]string{{"never", "x"}, {"sid-new", "sid-new"}, {"", "x"}, {"x", ""}} {
		moved, err := ls.Move(c[0], c[1], now)
		require.NoError(t, err, c)
		assert.False(t, moved, c)
	}

	// The new session already had a label: the relayed identity replaces it.
	_, err = ls.Claim("sid-a", "alpha", now)
	require.NoError(t, err)
	_, err = ls.Claim("sid-b", "beta", now)
	require.NoError(t, err)
	moved, err = ls.Move("sid-a", "sid-b", now)
	require.NoError(t, err)
	assert.True(t, moved)
	rows, _ = ls.Snapshot()
	for _, r := range rows {
		if r.SessionID == "sid-b" {
			assert.Equal(t, "alpha", r.Label)
		}
	}
}

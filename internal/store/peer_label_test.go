// internal/store/peer_label_test.go
package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerLabels_ClaimReleaseRev(t *testing.T) {
	ms, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	ls := ms.PeerLabels()
	now := time.UnixMilli(1000)

	a, err := ls.Claim("sid-a", "tester", now)
	require.NoError(t, err)
	assert.Equal(t, "tester", a.Label)
	assert.Equal(t, int64(1), a.Rev)

	// Re-claiming a different label for the same session replaces it and bumps rev.
	a2, err := ls.Claim("sid-a", "tester-2", now)
	require.NoError(t, err)
	assert.Equal(t, int64(2), a2.Rev)
	rows, _ := ls.Snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, "tester-2", rows[0].Label)

	// Another session takes "tester-2" too: both rows keep it (Peer Address
	// v3 D5 — a label is a display name, so it need not be unique, and the
	// incumbent is left exactly where it was).
	b, err := ls.Claim("sid-b", "tester-2", now)
	require.NoError(t, err)
	assert.Equal(t, int64(3), b.Rev)
	rows, _ = ls.Snapshot()
	require.Len(t, rows, 2)
	for _, r := range rows {
		assert.Equal(t, "tester-2", r.Label, "claiming a held label evicts nobody")
	}
	assert.Equal(t, int64(2), rows[0].Rev, "sid-a's revision is untouched by sid-b's claim")

	// Release keeps the row with a NULL label and a higher rev.
	rel, ok, err := ls.Release("sid-b", now)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "", rel.Label)
	assert.Equal(t, int64(4), rel.Rev)
	rows, _ = ls.Snapshot()
	require.Len(t, rows, 2)
	assert.Equal(t, "tester-2", rows[0].Label, "sid-a still holds the label sid-b released")
	assert.Equal(t, "", rows[1].Label)

	// Releasing an unknown session is a no-op.
	_, ok, err = ls.Release("nobody", now)
	require.NoError(t, err)
	assert.False(t, ok)

	// A third session's claim and release leave every row in place.
	_, err = ls.Claim("sid-c", "x1", now)
	require.NoError(t, err)
	_, _, err = ls.Release("sid-c", now)
	require.NoError(t, err)
	rows, _ = ls.Snapshot()
	assert.Len(t, rows, 3)
}

func TestPeerLabels_RevSurvivesReopen(t *testing.T) {
	path := t.TempDir() + "/meta.db"
	ms, err := OpenMeta(path)
	require.NoError(t, err)
	_, err = ms.PeerLabels().Claim("sid", "a1", time.Now())
	require.NoError(t, err)
	require.NoError(t, ms.Close())

	ms2, err := OpenMeta(path)
	require.NoError(t, err)
	defer ms2.Close()
	row, err := ms2.PeerLabels().Claim("sid", "a2", time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(2), row.Rev)
}

// internal/store/peer_message_test.go
package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerMessagesSchemaAndIndexes(t *testing.T) {
	ms, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	pm := ms.PeerMessages()
	require.NotNil(t, pm)

	// Table exists.
	var tableName string
	err = ms.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'peer_messages'`).
		Scan(&tableName)
	require.NoError(t, err)
	assert.Equal(t, "peer_messages", tableName)

	// Both indexes exist.
	var idxTS string
	err = ms.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'peer_messages_ts'`).
		Scan(&idxTS)
	require.NoError(t, err)
	assert.Equal(t, "peer_messages_ts", idxTS)

	var idxMsg string
	err = ms.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'peer_messages_msg'`).
		Scan(&idxMsg)
	require.NoError(t, err)
	assert.Equal(t, "peer_messages_msg", idxMsg)
}

func TestPeerMessagesInsertRoundTripAndMsPrecision(t *testing.T) {
	ms, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	pm := ms.PeerMessages()

	ts := time.UnixMilli(1_700_000_000_123)
	id, err := pm.Insert(PeerMessage{
		MsgID:         "m1",
		NativeMsgID:   "n1",
		Direction:     DirOut,
		TS:            ts,
		FromHostID:    "hostA",
		FromSessionID: "sessA",
		ToHostID:      "hostB",
		ToSessionID:   "sessB",
		DeclaredMode:  "text",
		EffectiveMode: "",
		Bytes:         42,
		Result:        "",
		Error:         "",
	})
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	rows, err := pm.Tail(10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	got := rows[0]
	assert.Equal(t, id, got.ID)
	assert.Equal(t, "m1", got.MsgID)
	assert.Equal(t, "n1", got.NativeMsgID)
	assert.Equal(t, DirOut, got.Direction)
	assert.True(t, ts.Equal(got.TS), "ts round-trip: want %v got %v", ts, got.TS)
	assert.Equal(t, ts.UnixMilli(), got.TS.UnixMilli())
	assert.Equal(t, "hostA", got.FromHostID)
	assert.Equal(t, "sessA", got.FromSessionID)
	assert.Equal(t, "hostB", got.ToHostID)
	assert.Equal(t, "sessB", got.ToSessionID)
	assert.Equal(t, "text", got.DeclaredMode)
	assert.Equal(t, "", got.EffectiveMode)
	assert.Equal(t, 42, got.Bytes)
}

func TestPeerMessagesSetResultOverwritesEffectiveModeOnlyWhenNonEmpty(t *testing.T) {
	ms, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	pm := ms.PeerMessages()

	id, err := pm.Insert(PeerMessage{
		MsgID:         "m1",
		Direction:     DirOut,
		TS:            time.UnixMilli(1_700_000_000_000),
		DeclaredMode:  "text",
		EffectiveMode: "text",
	})
	require.NoError(t, err)

	// Empty effectiveMode: keeps the inserted one.
	err = pm.SetResult(id, "", "ok", "")
	require.NoError(t, err)
	rows, err := pm.Tail(10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "text", rows[0].EffectiveMode)
	assert.Equal(t, "ok", rows[0].Result)
	assert.Equal(t, "", rows[0].Error)

	// Non-empty effectiveMode: overwrites it.
	err = pm.SetResult(id, "binary", "failed", "boom")
	require.NoError(t, err)
	rows, err = pm.Tail(10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "binary", rows[0].EffectiveMode)
	assert.Equal(t, "failed", rows[0].Result)
	assert.Equal(t, "boom", rows[0].Error)
}

func TestPeerMessagesTailOrderingOldestFirst(t *testing.T) {
	ms, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	pm := ms.PeerMessages()

	base := time.UnixMilli(1_700_000_000_000)
	var ids []int64
	for i := 0; i < 5; i++ {
		id, err := pm.Insert(PeerMessage{
			MsgID:     "m",
			Direction: DirOut,
			TS:        base.Add(time.Duration(i) * time.Millisecond),
		})
		require.NoError(t, err)
		ids = append(ids, id)
	}

	rows, err := pm.Tail(3)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	// Newest 3 are ids[2], ids[3], ids[4]; Tail returns oldest first.
	assert.Equal(t, ids[2], rows[0].ID)
	assert.Equal(t, ids[3], rows[1].ID)
	assert.Equal(t, ids[4], rows[2].ID)
}

func TestPeerMessagesTailZeroIsEmptyNonNil(t *testing.T) {
	ms, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	pm := ms.PeerMessages()

	_, err = pm.Insert(PeerMessage{
		MsgID:     "m1",
		Direction: DirOut,
		TS:        time.UnixMilli(1_700_000_000_000),
	})
	require.NoError(t, err)

	rows, err := pm.Tail(0)
	require.NoError(t, err)
	require.NotNil(t, rows)
	assert.Empty(t, rows)
}

func TestPeerMessagesDuplicateMsgIDDirectionAllowedDistinctIDs(t *testing.T) {
	ms, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	pm := ms.PeerMessages()

	id1, err := pm.Insert(PeerMessage{
		MsgID:     "dup",
		Direction: DirOut,
		TS:        time.UnixMilli(1_700_000_000_000),
	})
	require.NoError(t, err)

	id2, err := pm.Insert(PeerMessage{
		MsgID:     "dup",
		Direction: DirOut,
		TS:        time.UnixMilli(1_700_000_000_001),
	})
	require.NoError(t, err)

	assert.NotEqual(t, id1, id2)

	rows, err := pm.Tail(10)
	require.NoError(t, err)
	require.Len(t, rows, 2)
}

func TestMetaTableUnaffectedByPeerMessagesMigration(t *testing.T) {
	// Guard against regressions in the shared migration function: existing
	// session_meta behavior must be untouched.
	ms, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	err = ms.SetMeta("$0", SessionMeta{Mode: "terminal"})
	require.NoError(t, err)
	meta, err := ms.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, "terminal", meta.Mode)
}

package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func openConvStore(t *testing.T) (*MetaStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "meta.db")
	m, err := OpenMeta(path)
	require.NoError(t, err)
	t.Cleanup(func() { m.Close() })
	return m, path
}

func convRow(id string) ConversationIndexRow {
	return ConversationIndexRow{
		SessionID:       id,
		TranscriptPath:  "/root/slug/" + id + ".jsonl",
		Cwd:             "/work/專案",
		FirstEntrypoint: "cli",
		LastEntrypoint:  "sdk-cli",
		CustomTitle:     "custom",
		AITitle:         "ai",
		FirstPrompt:     "你好，世界 hello",
		Size:            1234,
		MtimeMs:         1700000000123,
		Inode:           987654,
		HeadOffset:      512,
		HeadDone:        true,
		FirstSeenAt:     1000,
		LastSeenAt:      2000,
	}
}

func TestConversationIndex_FreshDBIsEmptyWithTable(t *testing.T) {
	m, _ := openConvStore(t)
	rows, err := m.Conversations().All(context.Background())
	require.NoError(t, err)
	require.Empty(t, rows)
	var n int
	require.NoError(t, m.db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='conversation_index'`).Scan(&n))
	require.Equal(t, 1, n)
}

func TestConversationIndex_UpsertAndAllRoundTrip(t *testing.T) {
	m, _ := openConvStore(t)
	a := convRow("aaaaaaaa-0000-0000-0000-000000000001")
	b := convRow("bbbbbbbb-0000-0000-0000-000000000002")
	b.HeadDone = false
	b.FirstPrompt = ""
	require.NoError(t, m.Conversations().UpsertBatch(context.Background(), []ConversationIndexRow{b, a}))
	got, err := m.Conversations().All(context.Background())
	require.NoError(t, err)
	require.Equal(t, []ConversationIndexRow{a, b}, got)
}

func TestConversationIndex_UpsertUpdatesButKeepsFirstSeen(t *testing.T) {
	m, _ := openConvStore(t)
	ctx := context.Background()
	a := convRow("aaaaaaaa-0000-0000-0000-000000000001")
	require.NoError(t, m.Conversations().UpsertBatch(ctx, []ConversationIndexRow{a}))
	n := a
	n.TranscriptPath = "/other/path.jsonl"
	n.Cwd = "/new"
	n.FirstEntrypoint = "x"
	n.LastEntrypoint = "y"
	n.CustomTitle = "c2"
	n.AITitle = "a2"
	n.FirstPrompt = "p2"
	n.Size = 9999
	n.MtimeMs = 42
	n.Inode = 7
	n.HeadOffset = 4096
	n.HeadDone = false
	n.FirstSeenAt = 5000
	n.LastSeenAt = 6000
	require.NoError(t, m.Conversations().UpsertBatch(ctx, []ConversationIndexRow{n}))
	got, err := m.Conversations().All(ctx)
	require.NoError(t, err)
	n.FirstSeenAt = a.FirstSeenAt
	require.Equal(t, []ConversationIndexRow{n}, got)
}

func TestConversationIndex_HugeInodeRoundTrips(t *testing.T) {
	m, _ := openConvStore(t)
	ctx := context.Background()
	a := convRow("aaaaaaaa-0000-0000-0000-000000000001")
	a.Inode = 1<<63 + 12345
	require.NoError(t, m.Conversations().UpsertBatch(ctx, []ConversationIndexRow{a}))
	got, err := m.Conversations().All(ctx)
	require.NoError(t, err)
	require.Equal(t, a.Inode, got[0].Inode)
}

func TestConversationIndex_ReopenIsIdempotent(t *testing.T) {
	m, path := openConvStore(t)
	ctx := context.Background()
	a := convRow("aaaaaaaa-0000-0000-0000-000000000001")
	require.NoError(t, m.Conversations().UpsertBatch(ctx, []ConversationIndexRow{a}))
	require.NoError(t, m.Close())
	m2, err := OpenMeta(path)
	require.NoError(t, err)
	defer m2.Close()
	got, err := m2.Conversations().All(ctx)
	require.NoError(t, err)
	require.Equal(t, []ConversationIndexRow{a}, got)
}

func TestConversationIndex_EmptyBatchNoop(t *testing.T) {
	m, _ := openConvStore(t)
	require.NoError(t, m.Conversations().UpsertBatch(context.Background(), nil))
	got, err := m.Conversations().All(context.Background())
	require.NoError(t, err)
	require.Empty(t, got)
}

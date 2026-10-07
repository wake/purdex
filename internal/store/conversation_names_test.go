package store

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const nameSID = "AAAAAAAA-0000-0000-0000-000000000001"

func TestConversationNames_UpsertOverwritesAndAll(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.ConversationNames()
	ctx := context.Background()
	require.NoError(t, s.Upsert(ctx, nameSID, "first", 10))
	require.NoError(t, s.Upsert(ctx, nameSID, "second", 20))
	require.NoError(t, s.Upsert(ctx, "bbbbbbbb-0000-0000-0000-000000000002", "other", 30))
	got, err := s.All(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"aaaaaaaa-0000-0000-0000-000000000001": "second",
		"bbbbbbbb-0000-0000-0000-000000000002": "other",
	}, got)
}

func TestConversationNames_RejectsEmpty(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.ConversationNames()
	ctx := context.Background()
	require.Error(t, s.Upsert(ctx, nameSID, "", 1))
	require.Error(t, s.Upsert(ctx, nameSID, "  \t ", 1))
	require.Error(t, s.Upsert(ctx, "  ", "name", 1))
	got, err := s.All(ctx)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestConversationNames_TrimsAndTruncatesOnRuneBoundary(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.ConversationNames()
	ctx := context.Background()
	long := "  " + strings.Repeat("名", 150) + "  "
	require.NoError(t, s.Upsert(ctx, nameSID, long, 1))
	got, err := s.All(ctx)
	require.NoError(t, err)
	name := got[strings.ToLower(nameSID)]
	require.Equal(t, strings.Repeat("名", 120), name)
}

func TestConversationNames_SessionIDCaseNormalized(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.ConversationNames()
	ctx := context.Background()
	require.NoError(t, s.Upsert(ctx, nameSID, "a", 1))
	require.NoError(t, s.Upsert(ctx, strings.ToLower(nameSID), "b", 2))
	got, err := s.All(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "b", got[strings.ToLower(nameSID)])
}

func TestConversationNames_SeenAtUpdatedOnConflict(t *testing.T) {
	m, _ := openConvStore(t)
	s := m.ConversationNames()
	ctx := context.Background()
	require.NoError(t, s.Upsert(ctx, nameSID, "same", 100))
	at, err := s.seenAt(ctx, nameSID)
	require.NoError(t, err)
	require.Equal(t, int64(100), at)
	require.NoError(t, s.Upsert(ctx, nameSID, "same", 200))
	at, err = s.seenAt(ctx, nameSID)
	require.NoError(t, err)
	require.Equal(t, int64(200), at)
	got, err := s.All(ctx)
	require.NoError(t, err)
	require.Equal(t, "same", got[strings.ToLower(nameSID)])
}

package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Name pool v2 (spec §6.1): who was a lead and who worked for whom. Written only through the name service.

func TestNameHistory_LeadsCountAndKeepTheLastTime(t *testing.T) {
	m, _ := openConvStore(t)
	h := m.NameHistory()
	ctx := context.Background()
	require.NoError(t, h.RecordLead(ctx, "hiro", 100))
	require.NoError(t, h.RecordLead(ctx, "hiro", 300))
	require.NoError(t, h.RecordLead(ctx, "wei", 200))
	got, err := h.Leads(ctx)
	require.NoError(t, err)
	require.Equal(t, []NameLead{{"hiro", 2, 300}, {"wei", 1, 200}}, got, "most recent first")
}

func TestNameHistory_PairsCountPerLeadAndMember(t *testing.T) {
	m, _ := openConvStore(t)
	h := m.NameHistory()
	ctx := context.Background()
	require.NoError(t, h.RecordPair(ctx, "hiro", "freya", 100))
	require.NoError(t, h.RecordPair(ctx, "hiro", "freya", 150))
	require.NoError(t, h.RecordPair(ctx, "hiro", "kenji", 120))
	require.NoError(t, h.RecordPair(ctx, "wei", "freya", 400))
	got, err := h.Pairs(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []NamePair{
		{"hiro", "freya", 2, 150}, {"hiro", "kenji", 1, 120}, {"wei", "freya", 1, 400},
	}, got)
}

func TestNameHistory_RefusesEmptyCodes(t *testing.T) {
	m, _ := openConvStore(t)
	h := m.NameHistory()
	ctx := context.Background()
	require.Error(t, h.RecordLead(ctx, "", 1))
	require.Error(t, h.RecordPair(ctx, "hiro", "", 1))
	require.Error(t, h.RecordPair(ctx, "", "freya", 1))
}

// Familiar = every lead code ∪ every member code, most recent first, each code once, capped.
func TestNameHistory_FamiliarIsTheUnionMostRecentFirstAndCapped(t *testing.T) {
	m, _ := openConvStore(t)
	h := m.NameHistory()
	ctx := context.Background()
	require.NoError(t, h.RecordLead(ctx, "hiro", 100))
	require.NoError(t, h.RecordPair(ctx, "hiro", "freya", 500))
	require.NoError(t, h.RecordPair(ctx, "hiro", "kenji", 300))
	require.NoError(t, h.RecordLead(ctx, "freya", 200)) // freya is both a member (500) and a lead (200)
	got, err := h.Familiar(ctx, 64)
	require.NoError(t, err)
	require.Equal(t, []string{"freya", "kenji", "hiro"}, got)
	got, err = h.Familiar(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"freya", "kenji"}, got, "the cap keeps the most recent")
	empty, err := m.NameHistory().Familiar(ctx, 0)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestNameHistory_MigrationRunsTwice(t *testing.T) {
	m, _ := openConvStore(t)
	require.NoError(t, migrateNameHistory(m.db))
	require.NoError(t, migrateNameHistory(m.db))
}

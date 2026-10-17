package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeDecisions(t *testing.T, dir, name string, ds ...Decision) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(t, err)
	defer f.Close()
	for _, d := range ds {
		b, _ := json.Marshal(d)
		_, _ = f.Write(append(b, '\n'))
	}
}

// Only the commands that were offered to a host are history; newest first; across the rotated file; a bad line is skipped.
func TestHistory_OnlyWhatWasOfferedToAHostNewestFirst(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeDecisions(t, dir, decisionsFile+".1", Decision{Time: now.Add(-3 * time.Hour), Argv: []string{"old"}, Host: "air26"})
	writeDecisions(t, dir, decisionsFile,
		Decision{Time: now.Add(-2 * time.Hour), Argv: []string{"plain"}},
		Decision{Time: now.Add(-time.Hour), Argv: []string{"ran-there"}, Host: "air26", Tried: "air26"},
		Decision{Time: now.Add(-30 * time.Minute), Argv: []string{"fell-back"}, Tried: "air26", AssistWhy: "ssh to air26 failed"},
		Decision{Time: now.Add(-10 * time.Minute), Argv: []string{"on-mserv"}, Host: "mserv", Tried: "mserv"})
	f, _ := os.OpenFile(filepath.Join(dir, decisionsFile), os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("not json\n")
	f.Close()

	all, err := History(dir, HistoryFilter{})
	require.NoError(t, err)
	var names []string
	for _, d := range all {
		names = append(names, d.Argv[0])
	}
	assert.Equal(t, []string{"on-mserv", "fell-back", "ran-there", "old"}, names)

	air, _ := History(dir, HistoryFilter{Host: "air26"})
	assert.Len(t, air, 3, "a fallback counts for the host it was offered to")
	recent, _ := History(dir, HistoryFilter{Since: now.Add(-45 * time.Minute)})
	assert.Len(t, recent, 2)
	one, _ := History(dir, HistoryFilter{Limit: 1})
	assert.Equal(t, "on-mserv", one[0].Argv[0])
}

// A long command that ends after a newer short one is written after it: the order is by time, and --limit keeps the newest.
func TestHistory_IsOrderedByTimeNotByTheOrderOfTheEnds(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeDecisions(t, dir, decisionsFile,
		Decision{Time: now.Add(-time.Minute), Argv: []string{"newer-short"}, Host: "air26"},
		Decision{Time: now.Add(-time.Hour), Argv: []string{"older-long"}, Host: "air26"})
	recs, err := History(dir, HistoryFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "newer-short", recs[0].Argv[0])
}

// A rotation between the two opens leaves the older name pointing at the file already open as the current one: it is read once.
func TestHistory_AFileSeenUnderBothNamesIsReadOnce(t *testing.T) {
	dir := t.TempDir()
	writeDecisions(t, dir, decisionsFile, Decision{Time: time.Now(), Argv: []string{"x"}, Host: "air26"})
	require.NoError(t, os.Link(filepath.Join(dir, decisionsFile), filepath.Join(dir, decisionsFile+".1")))
	recs, err := History(dir, HistoryFilter{})
	require.NoError(t, err)
	assert.Len(t, recs, 1)
}

func TestHistory_NoFileIsNoHistory(t *testing.T) {
	recs, err := History(t.TempDir(), HistoryFilter{})
	require.NoError(t, err)
	assert.Empty(t, recs)
}

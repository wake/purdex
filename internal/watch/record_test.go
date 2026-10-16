package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newRec(t *testing.T, now *time.Time) (*Recorder, string) {
	dir := filepath.Join(t.TempDir(), "watch")
	r := NewRecorder(dir, "mlab")
	r.now = func() time.Time { return *now }
	return r, dir
}

func TestRecorderWritesOneLinePerEvent(t *testing.T) {
	now := st0
	r, dir := newRec(t, &now)
	id, err := r.Record(Event{Kind: KindUnmanagedHeavy, At: st0, Label: LabelGateGap, Signature: "pnpm build",
		Argv: []string{"pnpm", "build", "--token=abc123secret"}, Cwd: "/w", Session: "s1", PeakCores: 2.5, PeakMB: 2048,
		Duration: 90 * time.Second, New: true})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(id, "mlab-"))
	_, err = r.Record(Event{Kind: KindSlotOrphaned, At: st0, Slot: "heavy-1", HolderPID: 7, LockPIDs: []int{8}})
	require.NoError(t, err)

	b, err := os.ReadFile(filepath.Join(dir, "events-20261015.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	require.Len(t, lines, 2)
	require.NotContains(t, string(b), "abc123secret")
	recs, err := ReadLog(dir, time.Time{}, "")
	require.NoError(t, err)
	require.Len(t, recs, 2)
	require.Equal(t, id, recs[0].ID)
	require.Equal(t, "mlab", recs[0].Host)
	require.Equal(t, KindUnmanagedHeavy, recs[0].Kind)
	require.Equal(t, 90.0, recs[0].DurationS)
	require.True(t, recs[0].New)
	require.False(t, recs[0].Handled)
	require.Equal(t, "heavy-1", recs[1].Slot)
	require.Equal(t, []int{8}, recs[1].Holders)
	require.NotEqual(t, recs[0].ID, recs[1].ID)
}

func TestRecorderRotatesAfter30DaysOncePerDay(t *testing.T) {
	now := st0
	r, dir := newRec(t, &now)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	for _, n := range []string{"events-20260901.jsonl", "events-20260915.jsonl", "events-20260916.jsonl", "other.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("{}\n"), 0o600))
	}
	_, err := r.Record(Event{Kind: KindPressureHigh, At: st0}) // 10-15: cutoff is 09-15 (30 days)
	require.NoError(t, err)
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	require.NotContains(t, names, "events-20260901.jsonl")
	require.Contains(t, names, "events-20260915.jsonl")
	require.Contains(t, names, "events-20260916.jsonl")
	require.Contains(t, names, "other.txt")

	// same day: a file that appears later is not looked at again
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events-20260801.jsonl"), []byte("{}\n"), 0o600))
	_, _ = r.Record(Event{Kind: KindPressureHigh, At: st0})
	_, err = os.Stat(filepath.Join(dir, "events-20260801.jsonl"))
	require.NoError(t, err)
	now = st0.Add(24 * time.Hour)
	_, _ = r.Record(Event{Kind: KindPressureHigh, At: now})
	_, err = os.Stat(filepath.Join(dir, "events-20260801.jsonl"))
	require.True(t, os.IsNotExist(err), "the next day's first record cleans again")
}

func TestReadLogFilters(t *testing.T) {
	now := st0
	r, dir := newRec(t, &now)
	for i, k := range []Kind{KindUnmanagedHeavy, KindPressureHigh, KindUnmanagedHeavy} {
		at := st0.Add(time.Duration(i) * 48 * time.Hour)
		now = at
		_, err := r.Record(Event{Kind: k, At: at})
		require.NoError(t, err)
	}
	all, _ := ReadLog(dir, time.Time{}, "")
	require.Len(t, all, 3)
	require.True(t, all[0].At.Before(all[2].At), "oldest first")
	e1, _ := ReadLog(dir, time.Time{}, KindUnmanagedHeavy)
	require.Len(t, e1, 2)
	recent, _ := ReadLog(dir, st0.Add(24*time.Hour), "")
	require.Len(t, recent, 2)
}

func TestReadLogSkipsBadLinesAndMissingDir(t *testing.T) {
	recs, err := ReadLog(filepath.Join(t.TempDir(), "none"), time.Time{}, "")
	require.NoError(t, err)
	require.Empty(t, recs)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events-20261015.jsonl"),
		[]byte("garbage\n{\"id\":\"x\",\"kind\":\"pressure_high\",\"at\":\"2026-10-15T12:00:00Z\"}\n{\"id\":\"half"), 0o600))
	recs, skipped, err := ReadLogDiag(dir, time.Time{}, "")
	require.NoError(t, err)
	require.Equal(t, 2, skipped, "the skipped lines are counted, not hidden")
	require.Len(t, recs, 1, "a torn last line from a crash does not hide the rest")
}

func TestReadLogReportsWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	// a record longer than the scanner's limit: an error, not a silently shorter log
	long := `{"id":"x","kind":"pressure_high","at":"2026-10-15T12:00:00Z","detail":"` + strings.Repeat("a", 2<<20) + `"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events-20261015.jsonl"), []byte(long), 0o600))
	_, err := ReadLog(dir, time.Time{}, "")
	require.Error(t, err)
	// a directory that is there but cannot be read
	if os.Getuid() != 0 {
		d2 := filepath.Join(t.TempDir(), "w")
		require.NoError(t, os.Mkdir(d2, 0o000))
		defer os.Chmod(d2, 0o700)
		_, err = ReadLog(d2, time.Time{}, "")
		require.Error(t, err)
	}
}

func TestRecordFull_ReturnsTheLineItWrote(t *testing.T) {
	dir := t.TempDir()
	r := NewRecorder(dir, "mlab")
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	rec, err := r.RecordFull(Event{Kind: KindPressureHigh, At: at, Pressure: 2, Detail: "pressure level 2 for 2m0s"})
	require.NoError(t, err)
	require.NotEmpty(t, rec.ID)
	require.Equal(t, "mlab", rec.Host)
	require.Equal(t, KindPressureHigh, rec.Kind)
	got, err := ReadLog(dir, time.Time{}, "")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, rec.ID, got[0].ID)
}

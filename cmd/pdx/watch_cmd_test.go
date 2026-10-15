package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/watch"
)

func seedWatchLog(t *testing.T) string {
	dir := t.TempDir()
	r := watch.NewRecorder(dir, "mlab")
	now := time.Now()
	for _, ev := range []watch.Event{
		{Kind: watch.KindUnmanagedHeavy, At: now.Add(-72 * time.Hour), Label: watch.LabelGateGap, Signature: "pnpm build", Argv: []string{"pnpm", "build"}, PeakCores: 3, PeakMB: 900, Duration: time.Minute},
		{Kind: watch.KindPressureHigh, At: now.Add(-time.Hour), Detail: "pressure level 2 for 2m0s"},
		{Kind: watch.KindSlotOrphaned, At: now.Add(-time.Minute), Slot: "heavy-1", HolderPID: 9},
	} {
		_, err := r.Record(ev)
		require.NoError(t, err)
	}
	return dir
}

func TestWatchLogText(t *testing.T) {
	dir := seedWatchLog(t)
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"log", "--dir", dir}, &out, &errb), errb.String())
	require.Equal(t, 3, strings.Count(strings.TrimSpace(out.String()), "\n")+1)
	require.Contains(t, out.String(), "unmanaged_heavy")
	require.Contains(t, out.String(), "pnpm build")
	require.Contains(t, out.String(), "heavy-1")
}

func TestWatchLogFilters(t *testing.T) {
	dir := seedWatchLog(t)
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"log", "--dir", dir, "--since", "2h", "--kind", "pressure_high", "--json"}, &out, &errb), errb.String())
	var rec watch.Record
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(out.Bytes()), &rec))
	require.Equal(t, watch.KindPressureHigh, rec.Kind)

	out.Reset()
	require.Equal(t, 0, watchMain([]string{"log", "--dir", dir, "--since", "1d"}, &out, &errb))
	require.Equal(t, 2, strings.Count(strings.TrimSpace(out.String()), "\n")+1, "1d = 24 hours: the 72-hour-old one is out")
}

func TestWatchLogBadArgs(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	require.Equal(t, 2, watchMain([]string{"log", "--dir", dir, "--since", "yesterday-ish"}, &out, &errb))
	require.Equal(t, 2, watchMain([]string{"log", "--dir", dir, "--kind", "nope"}, &out, &errb))
	require.Equal(t, 2, watchMain([]string{}, &out, &errb))
	require.Equal(t, 2, watchMain([]string{"bogus"}, &out, &errb))
	out.Reset()
	require.Equal(t, 0, watchMain([]string{"log", "--dir", dir}, &out, &errb), "an empty log is not an error")
	require.Contains(t, out.String(), "沒有")
}

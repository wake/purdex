package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/store"
)

// #3843: how the run before a start ended. A run that is asked to stop (SIGTERM, SIGINT) leaves a mark in the data directory; the next start
// reads it and deletes it, and records how the run before it ended in its own start record. The crash-loop rule counts only the starts that
// followed a run with no mark.

var t0 = time.Date(2026, 10, 21, 6, 0, 0, 0, time.UTC)

func markPath(dir string) string { return filepath.Join(dir, cleanExitFile) }

func TestWriteCleanExitLeavesTheStartOfTheRunAndNoTempFile(t *testing.T) {
	dir := t.TempDir()
	if err := writeCleanExit(dir, t0); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(markPath(dir))
	if err != nil || string(b) != strconv.FormatInt(t0.UnixMilli(), 10)+"\n" {
		t.Fatalf("%q %v", b, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("%d files in the data dir, want only the mark", len(entries))
	}
	if err := writeCleanExit(dir, t0.Add(time.Minute)); err != nil { // a second one replaces the first
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(markPath(dir)); string(b) != strconv.FormatInt(t0.Add(time.Minute).UnixMilli(), 10)+"\n" {
		t.Fatalf("%q", b)
	}
}

func TestJudgePrevExit(t *testing.T) {
	last := store.LastStart{At: t0, PrevExit: store.PrevExitClean}
	ms := func(d time.Duration) string { return strconv.FormatInt(t0.Add(d).UnixMilli(), 10) + "\n" }
	cases := []struct {
		name    string
		mark    *string // nil: no mark file
		last    *store.LastStart
		want    string
		comment string
	}{
		{"no start before it: the first", nil, nil, store.PrevExitFirst, ""},
		{"a mark and no start before it is still the first", ptr(ms(0)), nil, store.PrevExitFirst, "the mark is consumed too"},
		{"the mark of the run before it", ptr(ms(0)), &last, store.PrevExitClean, ""},
		{"a mark of a run that was never recorded (newer than the last record)", ptr(ms(5 * time.Minute)), &last, store.PrevExitClean, "a stop that came before the start could be recorded"},
		{"no mark: the run before it did not end on request", nil, &last, store.PrevExitUnclean, "panic, kill -9, the OOM killer, a power cut"},
		{"a mark older than the last record: left over", ptr(ms(-time.Hour)), &last, store.PrevExitUnclean, "not the mark of the run before it"},
		{"a mark that does not read", ptr("garbage\n"), &last, store.PrevExitUnclean, ""},
		{"an empty mark", ptr(""), &last, store.PrevExitUnclean, ""},
		{"a start recorded by an older wwd: not known", ptr(ms(0)), &store.LastStart{At: t0, PrevExit: ""}, store.PrevExitUnknown, "a deployed database has these"},
		{"an older wwd's record and no mark", nil, &store.LastStart{At: t0, PrevExit: ""}, store.PrevExitUnknown, "the upgrade restart is not a crash"},
		{"a person's own text in the record is the new format", nil, &store.LastStart{At: t0, PrevExit: "signal: killed"}, store.PrevExitUnclean, ""},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if c.mark != nil {
			if err := os.WriteFile(markPath(dir), []byte(*c.mark), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got := judgePrevExit(dir, c.last)
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if _, err := os.Stat(markPath(dir)); !os.IsNotExist(err) {
			t.Errorf("%s: the mark is read once and deleted (%v)", c.name, err)
		}
	}
}

func ptr(s string) *string { return &s }

// A run of wwd as the rule sees it: it starts (judging the run before it), and ends on request or not.
func startRun(t *testing.T, st *store.Store, dir string, at time.Time, cleanEnd bool) {
	t.Helper()
	var last *store.LastStart
	if l, ok, err := st.LastStart(); err != nil {
		t.Fatal(err)
	} else if ok {
		last = &l
	}
	if err := st.RecordStartAt(at, "v", judgePrevExit(dir, last)); err != nil {
		t.Fatal(err)
	}
	if cleanEnd {
		if err := writeCleanExit(dir, at); err != nil {
			t.Fatal(err)
		}
	}
}

func openStartsStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func unclean(t *testing.T, st *store.Store) int {
	t.Helper()
	n, err := st.UncleanStartsSince(t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Two planned restarts (a deploy and a settings change) are not a crash loop, however many starts they make.
func TestPlannedRestartsAreNotCounted(t *testing.T) {
	st, dir := openStartsStore(t), t.TempDir()
	for i := 0; i < 5; i++ {
		startRun(t, st, dir, t0.Add(time.Duration(i)*time.Minute), true)
	}
	if all, _ := st.StartsSince(t0.Add(-time.Hour)); all != 5 {
		t.Fatalf("all=%d", all)
	}
	if n := unclean(t, st); n != 0 {
		t.Fatalf("%d: starts after a stop on request are not crashes", n)
	}
}

// Three crashes in a row are a loop: each crash makes the next start an unclean one.
func TestThreeCrashesAreCounted(t *testing.T) {
	st, dir := openStartsStore(t), t.TempDir()
	for i := 0; i < 4; i++ { // the first start, then three that follow a run that did not end cleanly
		startRun(t, st, dir, t0.Add(time.Duration(i)*time.Minute), false)
	}
	if n := unclean(t, st); n != 3 {
		t.Fatalf("%d, want 3", n)
	}
}

// Planned restarts between crashes do not hide them or add to them.
func TestAMixOfPlannedRestartsAndCrashes(t *testing.T) {
	st, dir := openStartsStore(t), t.TempDir()
	for i, clean := range []bool{true, true, false, true, false} { // start 1 ends clean, 2 ends clean, 3 crashes, 4 ends clean, 5 crashes
		startRun(t, st, dir, t0.Add(time.Duration(i)*time.Minute), clean)
	}
	startRun(t, st, dir, t0.Add(10*time.Minute), true) // the start after the crash of 5
	if n := unclean(t, st); n != 2 {
		t.Fatalf("%d, want 2: the starts after the two crashes", n)
	}
}

// A database that an older wwd wrote (every prev_exit is empty): the first start of this wwd is not counted, the ones after it are judged.
func TestAnUpgradedDatabaseIsJudgedFromTheSecondStart(t *testing.T) {
	st, dir := openStartsStore(t), t.TempDir()
	for i := 0; i < 4; i++ {
		if err := st.RecordStartAt(t0.Add(time.Duration(i-10)*time.Minute), "old", ""); err != nil {
			t.Fatal(err)
		}
	}
	startRun(t, st, dir, t0, false) // the upgrade restart: the old wwd left no mark, and it is not known how it ended
	if n := unclean(t, st); n != 0 {
		t.Fatalf("%d: the upgrade restart is not a crash", n)
	}
	startRun(t, st, dir, t0.Add(time.Minute), true) // this wwd's first run crashed
	if n := unclean(t, st); n != 1 {
		t.Fatalf("%d, want 1", n)
	}
	startRun(t, st, dir, t0.Add(2*time.Minute), true)
	if n := unclean(t, st); n != 1 {
		t.Fatalf("%d: a planned restart after it is not counted", n)
	}
}

// The clock went back between two runs: the last record is the last one inserted, so the mark of the run before it still matches.
func TestAClockThatWentBackDoesNotMakeACleanStopUnclean(t *testing.T) {
	st, dir := openStartsStore(t), t.TempDir()
	startRun(t, st, dir, t0, true)
	startRun(t, st, dir, t0.Add(-2*time.Hour), true) // the clock went back; this run ended cleanly too
	startRun(t, st, dir, t0.Add(-time.Hour), true)
	if n, _ := st.UncleanStartsSince(t0.Add(-24 * time.Hour)); n != 0 {
		t.Fatalf("%d", n)
	}
}

// What a signal does: the mark is written before the context ends, so a process that is killed while it shuts down slowly (launchd's
// SIGKILL after the exit timeout) was still asked to stop. A context that is ended from inside (an error return) leaves no mark.
func TestAStopOnRequestLeavesTheMarkBeforeTheContextEnds(t *testing.T) {
	dir := t.TempDir()
	sigs := make(chan os.Signal, 1)
	ctx, stop := watchStop(sigs, dir, t0, func(string, ...any) {})
	defer stop()
	sigs <- syscall.SIGTERM
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the signal did not end the context")
	}
	if _, err := os.Stat(markPath(dir)); err != nil {
		t.Fatalf("no mark when the context ended: %v", err)
	}
}

func TestAnInternalStopLeavesNoMark(t *testing.T) {
	dir := t.TempDir()
	sigs := make(chan os.Signal, 1)
	ctx, stop := watchStop(sigs, dir, t0, func(string, ...any) {})
	stop() // run() returns an error and cancels its own context
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not end the context")
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(markPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("an error return is not a stop on request: %v", err)
	}
}

// The start is recorded where the caller says (after the ports are bound), with how the run before it ended, and the mark is consumed there.
func TestRecordThisStartJudgesRecordsAndConsumesTheMark(t *testing.T) {
	st, dir := openStartsStore(t), t.TempDir()
	if err := st.RecordStartAt(t0.Add(-time.Minute), "v", store.PrevExitFirst); err != nil {
		t.Fatal(err)
	}
	if err := writeCleanExit(dir, t0.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	recordThisStart(context.Background(), st, dir, t0, "v2", nil, func(string, ...any) {})
	last, ok, err := st.LastStart()
	if err != nil || !ok || !last.At.Equal(t0) || last.PrevExit != store.PrevExitClean {
		t.Fatalf("%+v %v %v", last, ok, err)
	}
	if _, err := os.Stat(markPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("the mark is consumed: %v", err)
	}
}

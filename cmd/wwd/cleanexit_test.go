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

// #3843: how the run before a start ended. A run that is asked to stop (SIGTERM, SIGINT) leaves a mark in the data directory, named for the
// run; the next start reads the marks of other runs and deletes them, and records how the run before it ended in its own start record. The
// crash-loop rule counts only the starts that followed a run with no mark.

var t0 = time.Date(2026, 10, 21, 6, 0, 0, 0, time.UTC)

func markFile(dir string, run time.Time) string { return cleanExitPath(dir, run) }

func marks(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, cleanExitFile+".*"))
	return m
}

func TestWriteCleanExitLeavesAMarkNamedForTheRunAndNoTempFile(t *testing.T) {
	dir := t.TempDir()
	if err := writeCleanExit(dir, t0); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(markFile(dir, t0))
	if err != nil || string(b) != strconv.FormatInt(t0.UnixMilli(), 10)+"\n" {
		t.Fatalf("%q %v", b, err)
	}
	if err := writeCleanExit(dir, t0.Add(time.Minute)); err != nil { // another run's mark does not replace it
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("%d files in the data dir, want the two marks and no temporary file", len(entries))
	}
}

func TestJudgePrevExit(t *testing.T) {
	last := store.LastStart{At: t0, PrevExit: store.PrevExitClean}
	own := t0.Add(time.Hour) // the run that is starting
	cases := []struct {
		name  string
		marks []time.Time // runs that left a mark
		last  *store.LastStart
		want  string
	}{
		{"no start before it: the first", nil, nil, store.PrevExitFirst},
		{"a mark and no start before it is still the first", []time.Time{t0}, nil, store.PrevExitFirst},
		{"the mark of the run before it", []time.Time{t0}, &last, store.PrevExitClean},
		{"a mark of a run that was never recorded (newer than the last record)", []time.Time{t0.Add(5 * time.Minute)}, &last, store.PrevExitClean},
		{"no mark: the run before it did not end on request (panic, kill -9, the OOM killer, a power cut)", nil, &last, store.PrevExitUnclean},
		{"a mark older than the last record: left over from an earlier stop", []time.Time{t0.Add(-time.Hour)}, &last, store.PrevExitUnclean},
		{"an old mark and the right one", []time.Time{t0.Add(-time.Hour), t0}, &last, store.PrevExitClean},
		{"a start recorded by an older wwd: not known", []time.Time{t0}, &store.LastStart{At: t0, PrevExit: ""}, store.PrevExitUnknown},
		{"an older wwd's record and no mark: the upgrade restart is not a crash", nil, &store.LastStart{At: t0, PrevExit: ""}, store.PrevExitUnknown},
		{"a person's own text in the record is the new format", nil, &store.LastStart{At: t0, PrevExit: "signal: killed"}, store.PrevExitUnclean},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for _, m := range c.marks {
			if err := writeCleanExit(dir, m); err != nil {
				t.Fatal(err)
			}
		}
		if got := judgePrevExit(dir, c.last, own); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if left := marks(t, dir); len(left) != 0 {
			t.Errorf("%s: the marks of other runs are read once and deleted (%v)", c.name, left)
		}
	}
	// a temporary file left by a kill halfway is not a mark, and is not taken for one
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, cleanExitFile+".tmp-123"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := judgePrevExit(dir, &last, own); got != store.PrevExitUnclean {
		t.Errorf("a temporary file: %q", got)
	}
}

// A stop that arrives after the ports are bound and before the start is judged leaves this run's own mark: it is not the mark of the run
// before (codex R2), and it is still there for the next start (which finds this run ended on request).
func TestAStopBeforeTheStartIsJudgedIsNotTakenForTheRunBeforeItsMark(t *testing.T) {
	st, dir := openStartsStore(t), t.TempDir()
	startRun(t, st, dir, t0, false) // the run before it crashed: no mark
	own := t0.Add(time.Minute)
	if err := writeCleanExit(dir, own); err != nil { // SIGTERM: the mark is written first ...
		t.Fatal(err)
	}
	recordThisStart(context.Background(), st, dir, own, "v", nil, func(string, ...any) {}) // ... and then the start is judged
	if last, _, _ := st.LastStart(); last.PrevExit != store.PrevExitUnclean {
		t.Fatalf("%q: the run before it crashed, and its own stop is not its mark", last.PrevExit)
	}
	if _, err := os.Stat(markFile(dir, own)); err != nil {
		t.Fatalf("the mark of this run is consumed by its own start: %v", err)
	}
	startRun(t, st, dir, t0.Add(2*time.Minute), true) // the next start
	if last, _, _ := st.LastStart(); last.PrevExit != store.PrevExitClean {
		t.Fatalf("%q: this run was asked to stop, and the next start must see it", last.PrevExit)
	}
}

// A run of wwd as the rule sees it: it starts (judging the run before it), and ends on request or not.
func startRun(t *testing.T, st *store.Store, dir string, at time.Time, cleanEnd bool) {
	t.Helper()
	var last *store.LastStart
	if l, ok, err := st.LastStart(); err != nil {
		t.Fatal(err)
	} else if ok {
		last = &l
	}
	if err := st.RecordStartAt(at, "v", judgePrevExit(dir, last, at)); err != nil {
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
	if _, err := os.Stat(markFile(dir, t0)); err != nil {
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
	if m := marks(t, dir); len(m) != 0 {
		t.Fatalf("an error return is not a stop on request: %v", m)
	}
}

// The start is recorded where the caller says (after the ports are bound), with how the run before it ended, and the marks of other runs
// are consumed there.
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
	if m := marks(t, dir); len(m) != 0 {
		t.Fatalf("the mark of the run before is consumed: %v", m)
	}
}

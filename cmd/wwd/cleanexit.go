package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/store"
)

// How the run before a start ended (#3843). The crash-loop rule used to count every start in an hour, so a deploy and a settings change were
// a loop. Now a run that is asked to stop (SIGTERM or SIGINT: systemctl stop and restart, launchctl kickstart -k and bootout, so also both
// steps of `wwd install`) leaves a mark in the data directory, and the start after it records "clean"; a start that finds no mark records
// "unclean" (a panic, kill -9, the OOM killer, a power cut leave nothing). The rule counts only the unclean ones.
//
// The mark is a file, not a row, so that the deployed wwd.db needs no migration: the start record already has a prev_exit column, which has
// been written empty since v5. A record an older wwd made has it empty, so a start that follows one is "unknown" and is not counted (the
// restart that upgrades wwd is not a crash).

// cleanExitFile is the mark in the data directory: the start (milliseconds since the epoch) of the run that was asked to stop.
const cleanExitFile = "clean_exit"

// writeCleanExit leaves the mark for the run that started at startedAt: a temporary file, synced, then renamed over the mark, so a kill
// halfway leaves the old mark or none and never half of one.
func writeCleanExit(dir string, startedAt time.Time) error {
	f, err := os.CreateTemp(dir, cleanExitFile+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.WriteString(strconv.FormatInt(startedAt.UnixMilli(), 10) + "\n")
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, cleanExitFile))
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// judgePrevExit says how the run before this start ended, from the last start record (nil: none) and the mark, and deletes the mark: it is
// the mark of one run and is read once. A mark counts when it is not older than the last record (a run that was asked to stop before its
// own start could be recorded left a newer one); an older one is left over from some earlier stop and is not the mark of the run before.
func judgePrevExit(dir string, last *store.LastStart) string {
	p := filepath.Join(dir, cleanExitFile)
	b, readErr := os.ReadFile(p)
	if readErr == nil {
		_ = os.Remove(p)
	}
	switch {
	case last == nil:
		return store.PrevExitFirst
	case last.PrevExit == "":
		return store.PrevExitUnknown
	case readErr != nil:
		return store.PrevExitUnclean
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || ms < last.At.UnixMilli() {
		return store.PrevExitUnclean
	}
	return store.PrevExitClean
}

// recordThisStart judges how the run before this one ended (and consumes the mark) and records this start with it, with the retries of
// recordStart. It is called once the host has its ports: a start that could not bind (a second wwd, `wwd` run by mistake) is not a start of
// the service, and leaves the mark of the run before it where it is.
func recordThisStart(ctx context.Context, st *store.Store, dataDir string, startedAt time.Time, version string, delays []time.Duration, logf func(string, ...any)) {
	var last *store.LastStart
	if l, ok, err := st.LastStart(); err != nil {
		logf("wwd: reading the last start record: %v (how the run before ended is not known)", err)
		prev := store.PrevExitUnknown
		_ = os.Remove(filepath.Join(dataDir, cleanExitFile))
		recordStart(ctx, func() error { return st.RecordStartAt(startedAt, version, prev) }, delays, logf)
		return
	} else if ok {
		last = &l
	}
	prev := judgePrevExit(dataDir, last)
	recordStart(ctx, func() error { return st.RecordStartAt(startedAt, version, prev) }, delays, logf)
}

// watchStop is the context of the run: it ends when the process is asked to stop (a signal on sigs), after leaving the mark, or when the
// returned stop is called (the run returned an error: no mark, that is not a stop on request). The mark comes first because a service manager
// follows its SIGTERM with a SIGKILL when the shutdown is slow, and that run was asked to stop all the same.
func watchStop(sigs <-chan os.Signal, dataDir string, startedAt time.Time, logf func(string, ...any)) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-sigs:
			if err := writeCleanExit(dataDir, startedAt); err != nil {
				logf("wwd: leaving the clean-exit mark: %v (the next start will count as unclean)", err)
			}
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

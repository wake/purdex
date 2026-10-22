package main

import (
	"context"
	"os"
	"time"
)

// waitWorkRoot makes the work root, waiting while it cannot be made (the volume it is on is not mounted yet) rather than returning an
// error: launchd would start wwd again every few seconds, and every start would add a line to wwd.log. It tries again every retry, says
// why on the first failure and then at most once per logEvery, and says how long it waited when the directory finally exists. It
// returns false when ctx ends first (a stop on request), true once the directory is there. A directory that could be made at once costs
// no wait and no log line. Any MkdirAll error is waited out, including one that will never go away (a mistyped path): the same
// start-over loop as before, only quiet, and the log line says what is wrong.
func waitWorkRoot(ctx context.Context, dir string, retry, logEvery time.Duration, now func() time.Time, logf func(string, ...any)) bool {
	err := os.MkdirAll(dir, 0o700)
	if err == nil {
		return true
	}
	start := now()
	lastLog := start
	logf("wwd: work root %s cannot be made yet (%v); waiting for it, trying again every %s", dir, err, retry)
	timer := time.NewTimer(retry)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
		}
		if err = os.MkdirAll(dir, 0o700); err == nil {
			logf("wwd: work root %s is there after waiting %s", dir, now().Sub(start))
			return true
		}
		if t := now(); t.Sub(lastLog) >= logEvery {
			lastLog = t
			logf("wwd: work root %s still cannot be made (%v); waited %s so far", dir, err, t.Sub(start))
		}
		timer.Reset(retry)
	}
}

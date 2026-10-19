package main

import (
	"context"
	"time"
)

// startRetryDelays are the waits between the tries of a start record that could not be written: a locked or briefly failing wwd.db
// is given about a minute, then the record is given up on (and said so in the log).
var startRetryDelays = []time.Duration{time.Second, 3 * time.Second, 10 * time.Second, 30 * time.Second}

// recordStart writes this run's start record (spec §13.1). The first try is made here, so a healthy start-up has the record before
// it goes on; a failure does not stop the host from starting, but the record is the crash-loop rule's only input, so the write is
// tried again in the background after each of delays, until it works, the delays are used up, or ctx ends.
func recordStart(ctx context.Context, write func() error, delays []time.Duration, logf func(string, ...any)) {
	err := write()
	if err == nil {
		return
	}
	logf("wwd: recording this start: %v (trying again)", err)
	go func() {
		for _, d := range delays {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d):
			}
			if err = write(); err == nil {
				return
			}
		}
		logf("wwd: this start could not be recorded: %v (the crash-loop rule will count one start fewer)", err)
	}()
}

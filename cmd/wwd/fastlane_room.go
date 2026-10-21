package main

import (
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/wire"
)

// fastLaneRoom is fastlane.Room (localruns.Service.Room): why a fast-lane run may not start now, from the latest record and the live counts.
// A heavy run also gives way to a queued job this worker would take once it has waited as the policy says (#3941); when the oldest job has
// waited past the policy's cap the fast lane does not give way to it any more, and that is logged once for the job. It reads memory only (the
// fast lane asks with its lock held).
func fastLaneRoom(latest func() (wire.Facts, bool), yield derived.YieldPolicy, now func() time.Time, logf func(string, ...any)) func(heavy bool, running wire.Slots) []string {
	var overTold atomic.Value // the job the fast lane stopped giving way to
	return func(heavy bool, running wire.Slots) []string {
		f, ok := latest()
		if !ok {
			return []string{"no_facts_yet"}
		}
		t := now()
		why := derived.AdmitLocalAt(f, heavy, running, t, yield)
		if heavy && len(why) == 0 {
			if _, over := yield.Judge(f.Worker.Waiting, t); over {
				if id := f.Worker.Waiting.OldestID; overTold.Swap(id) != id {
					logf("fastlane: not giving way to %s any more: it has waited past fastlane_yield_max_seconds (%s)", id, t.Sub(f.Worker.Waiting.OldestSince).Round(time.Second))
				}
			}
		}
		return why
	}
}

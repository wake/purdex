package advisor

import (
	"sort"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Outcome is what one rule would have done over a replayed stretch.
type Outcome struct {
	// Episodes is how many times the rule went from not holding to holding: the number of alerts it would have raised. Hysteresis applies as
	// it does live.
	Episodes int
	// HeldMinutes is the number of replayed minutes it held.
	HeldMinutes int
	// FirstAt and LastAt are when the first and the last episode began (zero without one).
	FirstAt, LastAt time.Time
	// Errors is how many minutes it could not be judged.
	Errors int
}

// Replay runs the rules over recorded one-minute records (oldest first), judging at each record's time with only what was known by then:
// the records up to it, and the starts before it (starts, oldest first). It is how a rule is counted over the last week before it is
// switched on. The answer has an entry for every rule.
func Replay(recs []wire.Facts, starts []time.Time, workRoot string, rules ...Rule) map[wire.AlertKind]Outcome {
	out := map[wire.AlertKind]Outcome{}
	raised := map[string]bool{}
	for _, r := range rules {
		out[r.Kind()] = Outcome{}
	}
	for i, f := range recs {
		now := f.At
		upTo := recs[:i+1]
		in := Input{
			Now: now, Facts: f, WorkRoot: workRoot,
			Minutes: func(since time.Time) []wire.Facts {
				j := sort.Search(len(upTo), func(k int) bool { return !upTo[k].At.Before(since) })
				return upTo[j:]
			},
			UncleanStartsSince: func(since time.Time) (int, error) {
				lo := sort.Search(len(starts), func(k int) bool { return !starts[k].Before(since) })
				hi := sort.Search(len(starts), func(k int) bool { return starts[k].After(now) })
				if hi < lo {
					return 0, nil
				}
				return hi - lo, nil
			},
			Raised: func(key string) bool { return raised[key] },
		}
		for _, r := range rules {
			o := out[r.Kind()]
			v, err := r.Eval(in)
			if err != nil {
				o.Errors++
				out[r.Kind()] = o
				continue
			}
			if v.Holds {
				o.HeldMinutes++
				if !raised[v.Key] {
					o.Episodes++
					if o.FirstAt.IsZero() {
						o.FirstAt = now
					}
					o.LastAt = now
				}
			}
			raised[v.Key] = v.Holds
			out[r.Kind()] = o
		}
	}
	return out
}

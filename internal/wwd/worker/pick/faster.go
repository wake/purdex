package pick

import (
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/wire"
)

// FasterHost says whether a slower host should leave job to another one, and which: the first host of peers (the pool without this one) that
// answers, has its worker on, announces a speed_factor that is known and strictly below selfSpeed, takes the job by its labels (Eligible, with
// its own explicit_labels_only) and has a heavy slot free for it right now (derived.HeavyRoomFor, the reading its own worker has). A job that
// has waited as long as patience is not left: the faster host did not take it, whatever the reason, and this one must. patience <= 0 is
// "never leave a job" (the whole thing off). selfSpeed that is not a positive number is 1, as in Rank.
//
// A peer whose speed is not announced (a wwd from before the field) is not known to be faster and is never named; two hosts at the same
// speed leave nothing to each other, so they cannot both wait for the other.
func FasterHost(job wire.Job, selfSpeed float64, peers []wire.Peer, now time.Time, patience time.Duration) (alias string, ok bool) {
	if patience <= 0 || now.Sub(job.CreatedAt) >= patience {
		return "", false
	}
	if !finite(selfSpeed) || selfSpeed <= 0 {
		selfSpeed = 1
	}
	for _, p := range peers {
		w := p.Info.Worker
		if !p.Reachable || !w.Enabled || !finite(w.SpeedFactor) || w.SpeedFactor <= 0 || w.SpeedFactor >= selfSpeed {
			continue
		}
		if !Eligible(job.Labels, p.Info.Labels, w.ExplicitLabelsOnly) {
			continue
		}
		if p.Facts == nil || !derived.HeavyRoomFor(*p.Facts, job.Class, job.Repo.Name) {
			continue
		}
		return p.Info.Alias, true
	}
	return "", false
}

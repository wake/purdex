// Package pick decides which queued job a worker tries to claim next (spec §4.6, §5 Loop). It is pure: the caller reads the queued
// jobs of every origin, passes what it knows about this host, and gets the claimable jobs back best first. Nothing here claims,
// locks or talks to a queue.
package pick

import (
	"sort"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Candidate is one queued job as read from one origin.
type Candidate struct {
	Source string    // the origin's host id (for the caller's own use: which queue to claim from)
	Zone   wire.Zone // that origin's zone, for origin_bonus
	Job    wire.Job
}

// PinMode is a manual position (spec §4.6): a person or the watcher moved a queued job.
type PinMode string

const (
	PinFront PinMode = "front"
	PinBack  PinMode = "back"
	PinHold  PinMode = "hold"
)

// Pin is a job's manual position; Seq orders pins of the same mode, smallest first.
type Pin struct {
	Mode PinMode
	Seq  int64
}

// Env is what the worker knows about its own host. A nil function means "no restriction" (or zero, for Estimate).
type Env struct {
	Now    time.Time
	Labels []string // this host's labels; a job's labels must be a subset
	// SpeedFactor multiplies Estimate in size_penalty; 0 is taken as 1.
	SpeedFactor float64
	Estimate    func(wire.Job) float64 // the task's estimated minutes; nil = 0
	Locked      func(keys []string) bool
	// Warm: the repo clone and the dependency cache are on this host. A missing clone is not a reason to skip a job: the workspace then
	// comes from the origin's tar (spec 5 Workspace), so a clone only earns warm_bonus.
	Warm      func(wire.Job) bool
	Suspended func(repo string) bool
	Pinned    func(wire.Job) (Pin, bool)
}

// Scored is a claimable job with its score.
type Scored struct {
	Candidate
	Score float64
}

const (
	classWeight       = 1000
	warningBonus      = 200
	dangerBonus       = 400
	warmBonus         = 50
	sameSessionWeight = 30
)

func classRank(c wire.JobClass) float64 {
	switch c {
	case wire.ClassRelease:
		return 3
	case wire.ClassMerge:
		return 2
	}
	return 1
}

func subset(need, have []string) bool {
	for _, n := range need {
		found := false
		for _, h := range have {
			if h == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Rank returns the jobs this host may try, best first: manual "front" pins (by Seq), then the scored jobs (highest score, ties by
// created_at then id), then "back" pins (by Seq). Held jobs, jobs that are not queued, and jobs this host cannot run are left
// out. The input is not modified.
func Rank(cands []Candidate, env Env) []Scored {
	speed := env.SpeedFactor
	if speed == 0 {
		speed = 1
	}
	// same_session_penalty counts every earlier queued job of the submitter's session that is visible, claimable here or not.
	bySession := map[string][]time.Time{}
	for _, c := range cands {
		if c.Job.State == wire.StateQueued && c.Job.Submitter.Session != "" {
			s := c.Job.Submitter.Session
			bySession[s] = append(bySession[s], c.Job.CreatedAt)
		}
	}
	earlier := func(j wire.Job) int {
		n := 0
		for _, at := range bySession[j.Submitter.Session] {
			if at.Before(j.CreatedAt) {
				n++
			}
		}
		return n
	}

	type entry struct {
		Scored
		pin    Pin
		pinned bool
	}
	var es []entry
	for _, c := range cands {
		j := c.Job
		if j.State != wire.StateQueued || !subset(j.Labels, env.Labels) {
			continue
		}
		if env.Locked != nil && env.Locked(j.Locks) {
			continue
		}
		if env.Suspended != nil && env.Suspended(j.Repo.Name) {
			continue
		}
		var e entry
		if env.Pinned != nil {
			e.pin, e.pinned = env.Pinned(j)
		}
		if e.pinned && e.pin.Mode == PinHold {
			continue
		}
		wait := env.Now.Sub(j.CreatedAt).Minutes()
		if wait < 0 {
			wait = 0
		}
		score := classWeight*classRank(j.Class) + float64(int(wait))
		switch c.Zone {
		case wire.ZoneWarning:
			score += warningBonus
		case wire.ZoneDanger:
			score += dangerBonus
		}
		if env.Warm != nil && env.Warm(j) {
			score += warmBonus
		}
		if env.Estimate != nil {
			score -= speed * env.Estimate(j)
		}
		score -= sameSessionWeight * float64(earlier(j))
		e.Scored = Scored{Candidate: c, Score: score}
		es = append(es, e)
	}

	// group: 0 front, 1 normal, 2 back.
	group := func(e entry) int {
		if e.pinned {
			switch e.pin.Mode {
			case PinFront:
				return 0
			case PinBack:
				return 2
			}
		}
		return 1
	}
	sort.SliceStable(es, func(i, k int) bool {
		a, b := es[i], es[k]
		if ga, gb := group(a), group(b); ga != gb {
			return ga < gb
		}
		if group(a) != 1 && a.pin.Seq != b.pin.Seq {
			return a.pin.Seq < b.pin.Seq
		}
		if group(a) == 1 && a.Score != b.Score {
			return a.Score > b.Score
		}
		if !a.Job.CreatedAt.Equal(b.Job.CreatedAt) {
			return a.Job.CreatedAt.Before(b.Job.CreatedAt)
		}
		return a.Job.ID < b.Job.ID
	})
	out := make([]Scored, len(es))
	for i, e := range es {
		out[i] = e.Scored
	}
	return out
}

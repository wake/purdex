// Package pick decides which queued job a worker tries to claim next (spec §4.6, §5 Loop). It is pure: the caller reads the queued
// jobs of every origin, passes what it knows about this host, and gets the claimable jobs back best first. Nothing here claims,
// locks or talks to a queue.
package pick

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Candidate is one queued job as read from one origin.
type Candidate struct {
	Source string    // the origin's host id (for the caller's own use: which queue to claim from)
	Zone   wire.Zone // that origin's zone, for origin_bonus
	Job    wire.Job
}

// Env is what the worker knows about its own host. A nil function means "no restriction" (or zero, for Estimate).
type Env struct {
	Now    time.Time
	Labels []string // this host's labels; a job's labels must be a subset
	// ExplicitLabelsOnly: take only a job whose labels name this host's os: label, a task:<repo>/<task> label it holds (#3441) or a host: label (#3873). A job
	// from an older commit has no labels at all, and without this any worker, mserv included, would claim it.
	ExplicitLabelsOnly bool
	// SpeedFactor multiplies Estimate in size_penalty; anything not a positive finite number is taken as 1.
	SpeedFactor float64
	Estimate    func(wire.Job) float64 // the task's estimated minutes; nil, negative, NaN or Inf = 0
	Locked      func(keys []string) bool
	// Warm: the repo clone and the dependency cache are on this host. A missing clone is not a reason to skip a job: the workspace then
	// comes from the origin's tar (spec 5 Workspace), so a clone only earns warm_bonus.
	Warm func(wire.Job) bool
	// Suspended: this host could not run a job of this repo from this origin a moment ago (an infrastructure failure), so its jobs are
	// skipped for a while. The origin is part of the key: one origin cannot make the host skip another origin's repo of the same name.
	Suspended func(source, repo string) bool
	// HeldOff: the origin of this job answered the request for its snapshot wrongly a moment ago (#3379), so this one job is skipped for a
	// while, not the repo: the failure is the origin's and often one job's.
	HeldOff func(source, id string) bool
	// TaskHeld: the command of this task was not found on this host a moment ago (#3908), so the jobs of this one task are skipped for a
	// while, and the repo's other tasks are not. The origin and the repo are part of the key, like Suspended's.
	TaskHeld func(source, repo, task string) bool
	// Admit: whether the holds with `allow` (spec §13.4) let this job through. A job it refuses is left out of the ranking. nil: no such hold.
	Admit func(wire.Job) bool
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

// finite: a score must be a number the comparator can order; NaN and Inf from a bad estimate are treated as "no estimate".
func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// Explicit: the job names an os: label, an earned task: label or a host: label (#3873: a qualify candidate names its host and, on purpose, no
// task:; a host: label only the named host holds is an assignment as explicit as the others). The caller has already checked the job's labels are a subset of the
// host's, so such a label is one this host holds.
func Explicit(labels []string) bool {
	for _, l := range labels {
		if strings.HasPrefix(l, "os:") || strings.HasPrefix(l, "task:") || strings.HasPrefix(l, "host:") {
			return true
		}
	}
	return false
}

// Eligible says whether a host that holds the labels have, and takes only explicitly labelled jobs if explicitOnly, may take a job that asks
// for need: the labels are a subset of the host's, and (explicitOnly) name something explicit. It is the one rule of what a host can take by its
// labels: Rank applies it to every candidate, and the alert rules of the queue (a job nobody can take, a free slot a job could use) ask it of
// each host of the pool, so what they call takeable is what a worker takes.
func Eligible(need, have []string, explicitOnly bool) bool {
	return Subset(need, have) && (!explicitOnly || Explicit(need))
}

// Subset says whether every label of need is among have.
func Subset(need, have []string) bool {
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
	if !finite(speed) || speed <= 0 {
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
	}
	var es []entry
	for _, c := range cands {
		j := c.Job
		if j.State != wire.StateQueued || !Eligible(j.Labels, env.Labels, env.ExplicitLabelsOnly) {
			continue
		}
		if env.Locked != nil && env.Locked(j.Locks) {
			continue
		}
		if env.Suspended != nil && env.Suspended(c.Source, j.Repo.Name) {
			continue
		}
		if env.HeldOff != nil && env.HeldOff(c.Source, j.ID) {
			continue
		}
		if env.TaskHeld != nil && env.TaskHeld(c.Source, j.Repo.Name, j.Task) {
			continue
		}
		if j.Manual == wire.PositionHold {
			continue
		}
		if env.Admit != nil && !env.Admit(j) {
			continue
		}
		var e entry
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
			if est := env.Estimate(j); finite(est) && est > 0 {
				score -= speed * est
			}
		}
		score -= sameSessionWeight * float64(earlier(j))
		e.Scored = Scored{Candidate: c, Score: score}
		es = append(es, e)
	}

	// group: 0 front, 1 normal, 2 back.
	group := func(e entry) int {
		switch e.Job.Manual {
		case wire.PositionFront:
			return 0
		case wire.PositionBack:
			return 2
		}
		return 1
	}
	sort.SliceStable(es, func(i, k int) bool {
		a, b := es[i], es[k]
		if ga, gb := group(a), group(b); ga != gb {
			return ga < gb
		}
		if group(a) != 1 && a.Job.ManualSeq != b.Job.ManualSeq {
			return a.Job.ManualSeq < b.Job.ManualSeq
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

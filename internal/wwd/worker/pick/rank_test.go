package pick

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func job(id string, class wire.JobClass, age time.Duration) wire.Job {
	return wire.Job{
		ID: id, Class: class, CreatedAt: t0.Add(-age), State: wire.StateQueued,
		Repo: wire.Repo{Name: "purdex"}, Labels: []string{"go"},
		Submitter: wire.Submitter{Session: "s-" + id},
	}
}

func cand(j wire.Job) Candidate { return Candidate{Source: "mlab", Job: j} }

func baseEnv() Env {
	return Env{Now: t0, Labels: []string{"go", "node"}, SpeedFactor: 1}
}

func ids(r []Scored) []string {
	var out []string
	for _, s := range r {
		out = append(out, s.Job.ID)
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestClassDominatesWait(t *testing.T) {
	// a dev job that waited 10 hours (600 points) still loses to a fresh merge job (1000 points ahead).
	r := Rank([]Candidate{cand(job("dev", wire.ClassDev, 10*time.Hour)), cand(job("merge", wire.ClassMerge, 0)), cand(job("rel", wire.ClassRelease, 0))}, baseEnv())
	eq(t, ids(r), []string{"rel", "merge", "dev"})
}

func TestWaitMinutesAddUp(t *testing.T) {
	r := Rank([]Candidate{cand(job("young", wire.ClassDev, time.Minute)), cand(job("old", wire.ClassDev, 30*time.Minute))}, baseEnv())
	eq(t, ids(r), []string{"old", "young"})
	if r[0].Score != 1000+30 {
		t.Fatalf("score = %v, want 1030", r[0].Score)
	}
}

func TestOriginBonusByZone(t *testing.T) {
	a := cand(job("a", wire.ClassDev, 0))
	b := cand(job("b", wire.ClassDev, 0))
	b.Zone = wire.ZoneWarning
	c := cand(job("c", wire.ClassDev, 0))
	c.Zone = wire.ZoneDanger
	r := Rank([]Candidate{a, b, c}, baseEnv())
	eq(t, ids(r), []string{"c", "b", "a"})
	if r[0].Score != 1400 || r[1].Score != 1200 || r[2].Score != 1000 {
		t.Fatalf("scores %v %v %v", r[0].Score, r[1].Score, r[2].Score)
	}
}

func TestWarmBonus(t *testing.T) {
	env := baseEnv()
	env.Warm = func(j wire.Job) bool { return j.ID == "w" }
	r := Rank([]Candidate{cand(job("c", wire.ClassDev, time.Minute)), cand(job("w", wire.ClassDev, 0))}, env)
	eq(t, ids(r), []string{"w", "c"})
	if r[0].Score != 1050 {
		t.Fatalf("score = %v", r[0].Score)
	}
}

func TestSizePenaltyUsesSpeedFactorAndEstimate(t *testing.T) {
	env := baseEnv()
	env.SpeedFactor = 2
	env.Estimate = func(j wire.Job) float64 {
		if j.ID == "big" {
			return 20
		}
		return 1
	}
	r := Rank([]Candidate{cand(job("big", wire.ClassDev, 0)), cand(job("small", wire.ClassDev, 0))}, env)
	eq(t, ids(r), []string{"small", "big"})
	if r[1].Score != 1000-40 {
		t.Fatalf("big score = %v, want 960", r[1].Score)
	}
}

func TestSameSessionPenaltyCountsEarlierQueuedJobs(t *testing.T) {
	mk := func(id string, age time.Duration) Candidate {
		j := job(id, wire.ClassDev, age)
		j.Submitter.Session = "same"
		return cand(j)
	}
	r := Rank([]Candidate{mk("third", 0), mk("first", 3*time.Minute), mk("second", 2*time.Minute)}, baseEnv())
	scores := map[string]float64{}
	for _, s := range r {
		scores[s.Job.ID] = s.Score
	}
	if scores["first"] != 1000+3 || scores["second"] != 1000+2-30 || scores["third"] != 1000-60 {
		t.Fatalf("scores %v", scores)
	}
}

func TestSamePenaltyCountsJobsThatCannotRunHere(t *testing.T) {
	// the penalty is about the submitter's queue, not about what this host can run.
	other := job("other", wire.ClassDev, 5*time.Minute)
	other.Labels = []string{"php"}
	other.Submitter.Session = "same"
	mine := job("mine", wire.ClassDev, 0)
	mine.Submitter.Session = "same"
	r := Rank([]Candidate{cand(other), cand(mine)}, baseEnv())
	eq(t, ids(r), []string{"mine"})
	if r[0].Score != 1000-30 {
		t.Fatalf("score = %v", r[0].Score)
	}
}

func TestTieBreaksByCreatedAtThenID(t *testing.T) {
	a := job("b", wire.ClassDev, 0)
	b := job("a", wire.ClassDev, 0)
	r := Rank([]Candidate{cand(a), cand(b)}, baseEnv())
	eq(t, ids(r), []string{"a", "b"})

	// equal score by different route: created_at breaks it, the older first.
	older := job("older", wire.ClassDev, 0)
	older.CreatedAt = t0.Add(-30 * time.Second)
	newer := job("newer", wire.ClassDev, 0)
	newer.CreatedAt = t0.Add(-30*time.Second + 1)
	env := baseEnv()
	env.Now = t0.Add(-30 * time.Second) // wait_minutes whole-number floor makes both 0
	r = Rank([]Candidate{cand(newer), cand(older)}, env)
	eq(t, ids(r), []string{"older", "newer"})
}

func TestFilters(t *testing.T) {
	env := baseEnv()
	env.Locked = func(keys []string) bool { return len(keys) > 0 && keys[0] == "mysql" }
	env.HasClone = func(repo string) bool { return repo != "noclone" }
	env.Suspended = func(repo string) bool { return repo == "paused" }
	locked := job("locked", wire.ClassDev, 0)
	locked.Locks = []string{"mysql"}
	label := job("label", wire.ClassDev, 0)
	label.Labels = []string{"php"}
	noclone := job("noclone", wire.ClassDev, 0)
	noclone.Repo.Name = "noclone"
	paused := job("paused", wire.ClassDev, 0)
	paused.Repo.Name = "paused"
	claimed := job("claimed", wire.ClassDev, 0)
	claimed.State = wire.StateClaimed
	ok := job("ok", wire.ClassDev, 0)
	r := Rank([]Candidate{cand(locked), cand(label), cand(noclone), cand(paused), cand(claimed), cand(ok)}, env)
	eq(t, ids(r), []string{"ok"})
}

func TestNoLabelsRequiredAlwaysRuns(t *testing.T) {
	j := job("x", wire.ClassDev, 0)
	j.Labels = nil
	env := baseEnv()
	env.Labels = nil
	eq(t, ids(Rank([]Candidate{cand(j)}, env)), []string{"x"})
}

func TestPinnedOrder(t *testing.T) {
	env := baseEnv()
	pins := map[string]Pin{
		"f2": {Mode: PinFront, Seq: 2}, "f1": {Mode: PinFront, Seq: 1},
		"b2": {Mode: PinBack, Seq: 4}, "b1": {Mode: PinBack, Seq: 3},
		"h": {Mode: PinHold, Seq: 9},
	}
	env.Pinned = func(j wire.Job) (Pin, bool) { p, ok := pins[j.ID]; return p, ok }
	r := Rank([]Candidate{
		cand(job("b2", wire.ClassRelease, time.Hour)), cand(job("n-dev", wire.ClassDev, 0)), cand(job("f2", wire.ClassDev, 0)),
		cand(job("h", wire.ClassRelease, time.Hour)), cand(job("n-merge", wire.ClassMerge, 0)),
		cand(job("f1", wire.ClassDev, 0)), cand(job("b1", wire.ClassRelease, time.Hour)),
	}, env)
	eq(t, ids(r), []string{"f1", "f2", "n-merge", "n-dev", "b1", "b2"})
}

func TestRankDoesNotMutateInput(t *testing.T) {
	in := []Candidate{cand(job("a", wire.ClassDev, 0)), cand(job("b", wire.ClassRelease, 0))}
	Rank(in, baseEnv())
	if in[0].Job.ID != "a" || in[1].Job.ID != "b" {
		t.Fatalf("input reordered: %v", in)
	}
}

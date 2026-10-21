package pick

import (
	"math"
	"sort"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func job(id string, class wire.JobClass, age time.Duration) wire.Job {
	return wire.Job{
		ID: id, Origin: "mlab", Task: "test", Snapshot: wire.Snapshot{Commit: "abc"}, Class: class, CreatedAt: t0.Add(-age), State: wire.StateQueued,
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

func TestMissingCloneIsNotAFilter(t *testing.T) {
	// no clone here: the workspace comes from the origin's tar (spec 5), so the job stays claimable, only without warm_bonus.
	env := baseEnv()
	env.Warm = func(j wire.Job) bool { return false }
	r := Rank([]Candidate{cand(job("far", wire.ClassDev, 0))}, env)
	eq(t, ids(r), []string{"far"})
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
	env.Suspended = func(source, repo string) bool { return source == "mlab" && repo == "paused" }
	locked := job("locked", wire.ClassDev, 0)
	locked.Locks = []string{"mysql"}
	label := job("label", wire.ClassDev, 0)
	label.Labels = []string{"php"}
	paused := job("paused", wire.ClassDev, 0)
	paused.Repo.Name = "paused"
	claimed := job("claimed", wire.ClassDev, 0)
	claimed.State = wire.StateClaimed
	ok := job("ok", wire.ClassDev, 0)
	r := Rank([]Candidate{cand(locked), cand(label), cand(paused), cand(claimed), cand(ok)}, env)
	eq(t, ids(r), []string{"ok"})
}

// A hold with `allow` (spec §13.4) lets through only the jobs it lists; the worker says which in Env.Admit. A job it refuses is out of the
// ranking altogether, so it is neither tried nor claimed; the others keep their order.
func TestAdmitFiltersTheRanking(t *testing.T) {
	env := baseEnv()
	rel := job("rel", wire.ClassRelease, 0)
	dev := job("dev", wire.ClassDev, time.Hour) // the oldest, which would win without the filter
	merge := job("merge", wire.ClassMerge, 0)
	env.Admit = func(j wire.Job) bool { return j.Class == wire.ClassRelease || j.Class == wire.ClassMerge }
	eq(t, ids(Rank([]Candidate{cand(dev), cand(merge), cand(rel)}, env)), []string{"rel", "merge"})
	env.Admit = func(wire.Job) bool { return false }
	if r := Rank([]Candidate{cand(dev), cand(rel)}, env); len(r) != 0 {
		t.Fatalf("a hold that admits nothing: %v", ids(r))
	}
	env.Admit = nil // no selective hold: no restriction
	eq(t, ids(Rank([]Candidate{cand(dev), cand(rel)}, env)), []string{"rel", "dev"})
}

func TestNoLabelsRequiredAlwaysRuns(t *testing.T) {
	j := job("x", wire.ClassDev, 0)
	j.Labels = nil
	env := baseEnv()
	env.Labels = nil
	eq(t, ids(Rank([]Candidate{cand(j)}, env)), []string{"x"})
}

func TestExplicitLabelsOnlySkipsJobsWithoutAnOsOrTaskLabel(t *testing.T) {
	env := baseEnv()
	env.Labels = []string{"go", "os:linux", "task:purdex/test"}
	env.ExplicitLabelsOnly = true
	none, plain, withOS, withTask := job("none", wire.ClassDev, 0), job("plain", wire.ClassDev, 0), job("os", wire.ClassDev, 0), job("task", wire.ClassDev, 0)
	none.Labels = nil
	withOS.Labels = []string{"os:linux"}
	withTask.Labels = []string{"go", "task:purdex/test"}
	r := Rank([]Candidate{cand(none), cand(plain), cand(withOS), cand(withTask)}, env)
	got := ids(r)
	sort.Strings(got)
	eq(t, got, []string{"os", "task"})
}

func TestExplicitLabelsOnlyOffKeepsUnlabeledJobs(t *testing.T) {
	none := job("none", wire.ClassDev, 0)
	none.Labels = nil
	eq(t, ids(Rank([]Candidate{cand(none)}, baseEnv())), []string{"none"})
}

func manual(j wire.Job, pos wire.Position, seq int64) wire.Job {
	j.Manual, j.ManualSeq = pos, seq
	return j
}

func TestManualOrder(t *testing.T) {
	// front by manual_seq ascending (a later front has the smaller one), then the scored jobs, then back by manual_seq ascending.
	r := Rank([]Candidate{
		cand(manual(job("b2", wire.ClassRelease, time.Hour), wire.PositionBack, 4)), cand(job("n-dev", wire.ClassDev, 0)),
		cand(manual(job("f2", wire.ClassDev, 0), wire.PositionFront, -1)),
		cand(manual(job("h", wire.ClassRelease, time.Hour), wire.PositionHold, 0)), cand(job("n-merge", wire.ClassMerge, 0)),
		cand(manual(job("f1", wire.ClassDev, 0), wire.PositionFront, -2)), cand(manual(job("b1", wire.ClassRelease, time.Hour), wire.PositionBack, 3)),
	}, baseEnv())
	eq(t, ids(r), []string{"f1", "f2", "n-merge", "n-dev", "b1", "b2"})
}

func TestHeldJobIsNeverChosenEvenAloneOrWithTopClass(t *testing.T) {
	r := Rank([]Candidate{cand(manual(job("h", wire.ClassRelease, 10*time.Hour), wire.PositionHold, 0))}, baseEnv())
	if len(r) != 0 {
		t.Fatalf("got %v", ids(r))
	}
}

func TestFrontDoesNotBypassFilters(t *testing.T) {
	env := baseEnv()
	env.Locked = func([]string) bool { return true }
	j := manual(job("f", wire.ClassDev, 0), wire.PositionFront, -1)
	j.Locks = []string{"mysql"}
	php := manual(job("php", wire.ClassDev, 0), wire.PositionFront, -2)
	php.Labels = []string{"php"}
	if r := Rank([]Candidate{cand(j), cand(php)}, env); len(r) != 0 {
		t.Fatalf("a front place must not make a job runnable that is not: %v", ids(r))
	}
}

func TestRankDoesNotMutateInput(t *testing.T) {
	in := []Candidate{cand(job("a", wire.ClassDev, 0)), cand(job("b", wire.ClassRelease, 0))}
	Rank(in, baseEnv())
	if in[0].Job.ID != "a" || in[1].Job.ID != "b" {
		t.Fatalf("input reordered: %v", in)
	}
}

func TestFutureCreatedAtDoesNotLowerScore(t *testing.T) {
	// clock skew between hosts can put created_at ahead of now; wait is never negative.
	r := Rank([]Candidate{cand(job("skew", wire.ClassDev, -5*time.Minute))}, baseEnv())
	if r[0].Score != 1000 {
		t.Fatalf("score = %v, want 1000", r[0].Score)
	}
}

func TestBadEstimateAndSpeedAreIgnored(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	for name, c := range map[string]struct{ speed, est float64 }{
		"nan estimate": {1, nan}, "inf estimate": {1, inf}, "negative estimate": {1, -50},
		"nan speed": {nan, 10}, "inf speed": {inf, 10}, "negative speed": {-3, 10}, "zero speed": {0, 10},
	} {
		env := baseEnv()
		env.SpeedFactor = c.speed
		env.Estimate = func(wire.Job) float64 { return c.est }
		r := Rank([]Candidate{cand(job("a", wire.ClassDev, 0))}, env)
		want := 1000.0
		if c.est == 10 {
			want = 990 // a bad speed factor is 1, the estimate still counts
		}
		if len(r) != 1 || r[0].Score != want {
			t.Errorf("%s: score = %v, want %v", name, r[0].Score, want)
		}
	}
}

func TestNaNEstimateDoesNotReorderOthers(t *testing.T) {
	env := baseEnv()
	env.Estimate = func(j wire.Job) float64 {
		if j.ID == "bad" {
			return math.NaN()
		}
		return 0
	}
	in := []Candidate{cand(job("low", wire.ClassDev, 0)), cand(job("bad", wire.ClassDev, time.Minute)), cand(job("high", wire.ClassMerge, 0))}
	eq(t, ids(Rank(in, env)), []string{"high", "bad", "low"})
	eq(t, ids(Rank([]Candidate{in[2], in[1], in[0]}, env)), []string{"high", "bad", "low"})
}

// The hold is on an origin's repo, not on the name: the same repo name from another origin is still picked.
func TestSuspendedIsPerOrigin(t *testing.T) {
	env := baseEnv()
	env.Suspended = func(source, repo string) bool { return source == "evil" && repo == "purdex" }
	mine, theirs := job("mine", wire.ClassDev, 0), job("theirs", wire.ClassDev, 0)
	mine.Repo.Name, theirs.Repo.Name = "purdex", "purdex"
	r := Rank([]Candidate{{Source: "mlab", Job: mine}, {Source: "evil", Job: theirs}}, env)
	eq(t, ids(r), []string{"mine"})
}

// A task whose command is not on this host is skipped, the repo's other tasks are not, and another host (its own env) still takes the task:
// the same candidates are ranked by two envs (#3908).
func TestTaskHeldSkipsThatTaskOnThisHostOnly(t *testing.T) {
	mod, lint, other := job("mod", wire.ClassMerge, 0), job("lint", wire.ClassMerge, 0), job("mod2", wire.ClassMerge, 0)
	mod.Task, lint.Task, other.Task = "mod-hooks", "lint", "mod-hooks"
	other.Repo.Name = "aigora"
	far := mod
	far.ID = "mod3"
	cands := []Candidate{cand(mod), cand(lint), cand(other), {Source: "air26", Job: far}}

	lacks := baseEnv()
	lacks.TaskHeld = func(source, repo, task string) bool {
		return source == "mlab" && repo == "purdex" && task == "mod-hooks"
	}
	got := map[string]bool{}
	for _, sc := range Rank(cands, lacks) {
		got[sc.Source+"/"+sc.Job.ID] = true
	}
	if len(got) != 3 || !got["mlab/lint"] || !got["mlab/mod2"] || !got["air26/mod3"] || got["mlab/mod"] {
		t.Fatalf("the host without the command ranked %v: want lint (same repo), mod2 (another repo) and air26/mod3 (another origin), not mod", got)
	}

	has := baseEnv() // another host: no hold, takes everything
	if n := len(Rank(cands, has)); n != 4 {
		t.Errorf("a host that has the command ranked %d of 4", n)
	}
}

// A job whose origin answered wrongly a moment ago is skipped; another job of the same repo is not, and neither is the same id at
// another origin (#3379).
func TestHeldOffSkipsThatJobOnly(t *testing.T) {
	env := baseEnv()
	env.HeldOff = func(source, id string) bool { return source == "mlab" && id == "bad" }
	bad, good, other := job("bad", wire.ClassDev, 0), job("good", wire.ClassDev, 0), job("bad", wire.ClassDev, 0)
	r := Rank([]Candidate{{Source: "mlab", Job: bad}, {Source: "mlab", Job: good}, {Source: "evil", Job: other}}, env)
	got := map[string]bool{}
	for _, sc := range r {
		got[sc.Source+"/"+sc.Job.ID] = true
	}
	if len(r) != 2 || !got["mlab/good"] || !got["evil/bad"] || got["mlab/bad"] {
		t.Fatalf("ranked %v: want mlab/good and evil/bad only", got)
	}
}

package advisor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
)

// ---- the queue's four rules (spec §13.3, WWD-9 S3-3b) ----

// host is a peer of the pool: it answers, its worker is on, it has labels and heavy room, and takes any job its labels hold unless explicit.
type hostOpt func(*wire.Peer)

func host(alias string, labels []string, opts ...hostOpt) wire.Peer {
	p := wire.Peer{Reachable: true, Info: wire.Info{Alias: alias, Labels: labels, Worker: wire.InfoWorker{Enabled: true}}, Facts: &wire.Facts{}}
	p.Facts.Derived.Headroom = wire.Slots{Heavy: 1, Light: 1}
	for _, o := range opts {
		o(&p)
	}
	return p
}

func workerOff(p *wire.Peer)    { p.Info.Worker.Enabled = false }
func down(p *wire.Peer)         { p.Reachable = false }
func full(p *wire.Peer)         { p.Facts.Derived.Headroom.Heavy = 0 }
func explicitOnly(p *wire.Peer) { p.Info.Worker.ExplicitLabelsOnly = true }
func noFactsYet(p *wire.Peer)   { p.Facts = nil }

type jobOpt func(*queue.QueuedJob)

// qjob is a queued job that was submitted `age` ago and last changed `touched` ago (0: when it was submitted).
func qjob(id string, labels []string, age time.Duration, opts ...jobOpt) queue.QueuedJob {
	j := queue.QueuedJob{ID: id, Labels: labels, CreatedAt: t0.Add(-age), Touched: t0.Add(-age)}
	for _, o := range opts {
		o(&j)
	}
	return j
}

func held(j *queue.QueuedJob) { j.Held = true }
func attempts(n int) jobOpt   { return func(j *queue.QueuedJob) { j.Attempts = n } }
func touched(ago time.Duration) jobOpt {
	return func(j *queue.QueuedJob) { j.Touched = t0.Add(-ago) }
}

func queueIn(jobs []queue.QueuedJob, pool ...wire.Peer) Input {
	return Input{Now: t0, Queue: func() ([]queue.QueuedJob, error) { return jobs, nil }, Pool: func() []wire.Peer { return pool }}
}

var (
	darwin = []string{"os:darwin", "arch:arm64", "repo:purdex"}
	linux  = []string{"os:linux", "arch:amd64"}
)

func holds(t *testing.T, r Rule, in Input) (bool, Verdict) {
	t.Helper()
	v, err := r.Eval(in)
	if err != nil {
		t.Fatalf("%s: %v", r.Kind(), err)
	}
	if v.Key != string(r.Kind()) || v.Level != wire.AlertWatch {
		t.Fatalf("%s: key %q level %q", r.Kind(), v.Key, v.Level)
	}
	return v.Holds, v
}

// ---- queue backlog ----

func TestQueueBacklog_TheOldestWaitingJobTwentyMinutes(t *testing.T) {
	want := map[time.Duration]bool{20*time.Minute - time.Second: false, 20 * time.Minute: true, 45 * time.Minute: true}
	for age, w := range want {
		got, v := holds(t, QueueBacklog{}, queueIn([]queue.QueuedJob{qjob("j_old", darwin, age), qjob("j_new", darwin, time.Minute)}))
		if got != w {
			t.Errorf("oldest waited %v: holds %v, want %v", age, got, w)
		}
		if got && !strings.Contains(v.Detail, "j_old") {
			t.Errorf("detail %q does not name the job", v.Detail)
		}
	}
}

func TestQueueBacklog_AJobOnHoldDoesNotCountAndTheNextOldestDoes(t *testing.T) {
	jobs := []queue.QueuedJob{qjob("j_hold", darwin, time.Hour, held), qjob("j_next", darwin, 25*time.Minute), qjob("j_young", darwin, time.Minute)}
	got, v := holds(t, QueueBacklog{}, queueIn(jobs))
	if !got || !strings.Contains(v.Detail, "j_next") || strings.Contains(v.Detail, "j_hold") {
		t.Fatalf("holds %v, %q", got, v.Detail)
	}
	if got, _ := holds(t, QueueBacklog{}, queueIn([]queue.QueuedJob{qjob("j_hold", darwin, time.Hour, held), qjob("j_young", darwin, time.Minute)})); got {
		t.Error("only the held job is old")
	}
	if got, _ := holds(t, QueueBacklog{}, queueIn(nil)); got {
		t.Error("an empty queue")
	}
}

// A job that was put back a minute ago is still the oldest wait: this rule counts from the submission.
func TestQueueBacklog_CountsFromTheSubmissionNotFromTheLastChange(t *testing.T) {
	got, _ := holds(t, QueueBacklog{}, queueIn([]queue.QueuedJob{qjob("j", darwin, 30*time.Minute, touched(time.Minute))}))
	if !got {
		t.Error("30 minutes since the submission")
	}
}

// ---- job requeued ----

func TestJobRequeued_AttemptsTwoAndThree(t *testing.T) {
	for n, w := range map[int]bool{0: false, 1: false, 2: false, 3: true, 7: true} {
		got, _ := holds(t, JobRequeued{}, queueIn([]queue.QueuedJob{qjob("j_1", darwin, time.Minute, attempts(n))}))
		if got != w {
			t.Errorf("attempts %d: holds %v, want %v", n, got, w)
		}
	}
}

func TestJobRequeued_AJobOnHoldStillCountsAndTheAlertNamesThemAll(t *testing.T) {
	jobs := []queue.QueuedJob{
		qjob("j_a", darwin, time.Minute, attempts(3)), qjob("j_b", darwin, time.Minute, attempts(4), held), qjob("j_ok", darwin, time.Minute, attempts(2)),
	}
	got, v := holds(t, JobRequeued{}, queueIn(jobs))
	if !got || !strings.Contains(v.Detail, "j_a（3 次）") || !strings.Contains(v.Detail, "j_b（4 次）") || strings.Contains(v.Detail, "j_ok") {
		t.Fatalf("holds %v, %q", got, v.Detail)
	}
	// more than three are named three, and how many there are
	var many []queue.QueuedJob
	for _, id := range []string{"j_1", "j_2", "j_3", "j_4", "j_5"} {
		many = append(many, qjob(id, darwin, time.Minute, attempts(3)))
	}
	_, v = holds(t, JobRequeued{}, queueIn(many))
	if !strings.Contains(v.Detail, "j_3") || strings.Contains(v.Detail, "j_4") || !strings.Contains(v.Detail, "5 件") {
		t.Errorf("detail %q", v.Detail)
	}
}

// ---- queue stalled ----

func TestQueueStalled_FiveMinutesWithAFreeSlotThatCouldTakeIt(t *testing.T) {
	pool := []wire.Peer{host("mlab", darwin)}
	for age, w := range map[time.Duration]bool{5*time.Minute - time.Second: false, 5 * time.Minute: true} {
		got, v := holds(t, QueueStalled{}, queueIn([]queue.QueuedJob{qjob("j_1", darwin[:1], age)}, pool...))
		if got != w {
			t.Errorf("waited %v: holds %v, want %v", age, got, w)
		}
		if got && (!strings.Contains(v.Detail, "mlab") || !strings.Contains(v.Detail, "j_1")) {
			t.Errorf("detail %q", v.Detail)
		}
	}
}

func TestQueueStalled_WhatIsNotAReasonToTakeIt(t *testing.T) {
	job := qjob("j_1", darwin[:1], 10*time.Minute)
	cases := map[string]struct {
		jobs []queue.QueuedJob
		pool []wire.Peer
	}{
		"no host has a slot free":                   {[]queue.QueuedJob{job}, []wire.Peer{host("mlab", darwin, full)}},
		"the host that has room has its worker off": {[]queue.QueuedJob{job}, []wire.Peer{host("mlab", darwin, workerOff)}},
		"the host is down":                          {[]queue.QueuedJob{job}, []wire.Peer{host("mlab", darwin, down)}},
		"the labels are not the host's":             {[]queue.QueuedJob{job}, []wire.Peer{host("mserv", linux)}},
		"no facts yet, so no room is known":         {[]queue.QueuedJob{job}, []wire.Peer{host("mlab", darwin, noFactsYet)}},
		"the job is on hold":                        {[]queue.QueuedJob{qjob("j_1", darwin[:1], 10*time.Minute, held)}, []wire.Peer{host("mlab", darwin)}},
		"put back a minute ago, not waiting":        {[]queue.QueuedJob{qjob("j_1", darwin[:1], time.Hour, attempts(1), touched(time.Minute))}, []wire.Peer{host("mlab", darwin)}},
		"an empty queue":                            {nil, []wire.Peer{host("mlab", darwin)}},
		"an explicit-only host, a job with no explicit label": {
			[]queue.QueuedJob{qjob("j_1", []string{"repo:purdex"}, 10*time.Minute)}, []wire.Peer{host("mserv", append([]string{"repo:purdex"}, linux...), explicitOnly)}},
	}
	for name, c := range cases {
		if got, _ := holds(t, QueueStalled{}, queueIn(c.jobs, c.pool...)); got {
			t.Errorf("%s: holds", name)
		}
	}
}

func TestQueueStalled_AnotherHostWithRoomIsEnough(t *testing.T) {
	jobs := []queue.QueuedJob{qjob("j_1", darwin[:1], 10*time.Minute)}
	got, v := holds(t, QueueStalled{}, queueIn(jobs, host("air26", darwin, full), host("mlab", darwin)))
	if !got || !strings.Contains(v.Detail, "mlab") {
		t.Fatalf("holds %v, %q", got, v.Detail)
	}
}

// ---- queue unclaimable ----

// settledHolds is the verdict of a fresh QueueUnclaimable for in, after it has looked once, ten minutes before in.Now, at the same queue and
// pool: the rule holds only for what has gone unclaimable for ten minutes running, and a rule that has just started has not seen that yet.
func settledHolds(t *testing.T, in Input) (bool, Verdict) {
	t.Helper()
	r := &QueueUnclaimable{}
	first := in
	first.Now = in.Now.Add(-queueUnclaimableAfter)
	if _, err := r.Eval(first); err != nil {
		t.Fatalf("first look: %v", err)
	}
	return holds(t, r, in)
}

// A host that is late for one poll (load1 50 to 80: /v1/info took over 3 seconds, so it was unreachable for 5 seconds) must not raise the
// alert, whatever the job's age; the ten minutes start again at every look that finds a host that takes the job (#3941).
func TestQueueUnclaimable_ABlipOfAHostRestartsTheTenMinutes(t *testing.T) {
	job := []queue.QueuedJob{qjob("j_1", darwin[:1], 30*time.Minute)}
	r := &QueueUnclaimable{}
	at := func(d time.Duration, pool ...wire.Peer) bool {
		in := queueIn(job, pool...)
		in.Now = t0.Add(d)
		got, _ := holds(t, r, in)
		return got
	}
	mlabDown, mlabUp := host("mlab", darwin, down), host("mlab", darwin)
	if at(0, mlabDown) {
		t.Fatal("the first look")
	}
	if at(9*time.Minute, mlabDown) {
		t.Error("nine minutes without a host")
	}
	if at(9*time.Minute+5*time.Second, mlabUp) {
		t.Error("the host answered")
	}
	if at(9*time.Minute+10*time.Second, mlabDown) || at(19*time.Minute, mlabDown) {
		t.Error("it went down again: the ten minutes start from here, not from the first look")
	}
	if !at(19*time.Minute+10*time.Second, mlabDown) {
		t.Error("ten minutes running without a host")
	}
	if at(19*time.Minute+15*time.Second, mlabUp) {
		t.Error("a host takes it again")
	}
}

// A job that left the queue and came back starts its own ten minutes, and the memory of a job that is gone (or on hold) does not grow.
func TestQueueUnclaimable_ForgetsAJobThatLeftTheQueue(t *testing.T) {
	r := &QueueUnclaimable{}
	pool := host("mlab", darwin, down)
	look := func(d time.Duration, jobs ...queue.QueuedJob) bool {
		in := queueIn(jobs, pool)
		in.Now = t0.Add(d)
		got, _ := holds(t, r, in)
		return got
	}
	j := qjob("j_1", darwin[:1], time.Hour)
	look(0, j)
	look(time.Minute) // gone
	if len(r.since) != 0 {
		t.Errorf("remembers %v", r.since)
	}
	if look(11*time.Minute, j) {
		t.Error("it is back: its ten minutes start from this look")
	}
	hold := j
	hold.Held = true
	look(12*time.Minute, hold)
	if len(r.since) != 0 {
		t.Errorf("remembers a held job: %v", r.since)
	}
}

// Eval is called from the evaluator's goroutine: the memory is guarded.
func TestQueueUnclaimable_EvalIsSafeFromSeveralGoroutines(t *testing.T) {
	r := &QueueUnclaimable{}
	in := queueIn([]queue.QueuedJob{qjob("j_1", darwin[:1], time.Hour)}, host("mlab", darwin, down))
	done := make(chan struct{})
	for range 4 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 200 {
				_, _ = r.Eval(in)
			}
		}()
	}
	for range 4 {
		<-done
	}
}

func TestQueueUnclaimable_TenMinutesAndNoHostTakesIt(t *testing.T) {
	jobs := func(age time.Duration) []queue.QueuedJob {
		return []queue.QueuedJob{qjob("j_1", []string{"repo:aigora-v2"}, age)}
	}
	pool := []wire.Peer{host("mlab", darwin), host("mserv", linux)}
	for age, w := range map[time.Duration]bool{10*time.Minute - time.Second: false, 10 * time.Minute: true} {
		got, v := settledHolds(t, queueIn(jobs(age), pool...))
		if got != w {
			t.Errorf("waited %v: holds %v, want %v", age, got, w)
		}
		if got && !strings.Contains(v.Detail, "j_1") {
			t.Errorf("detail %q", v.Detail)
		}
	}
}

// Every worker off (a release holds the pool, or the hosts are down): a queued job past ten minutes is unclaimable. That is expected during a
// release and is a call to the watcher only.
func TestQueueUnclaimable_EveryWorkerOffOrDownRaises(t *testing.T) {
	jobs := []queue.QueuedJob{qjob("j_1", darwin[:1], 15*time.Minute)}
	if got, _ := settledHolds(t, queueIn(jobs, host("mlab", darwin, workerOff), host("air26", darwin, workerOff))); !got {
		t.Error("workers off")
	}
	if got, _ := settledHolds(t, queueIn(jobs, host("mlab", darwin, down))); !got {
		t.Error("the only host down")
	}
	if got, _ := settledHolds(t, queueIn(jobs)); !got {
		t.Error("no host at all")
	}
}

func TestQueueUnclaimable_WhatDoesNotRaise(t *testing.T) {
	job := qjob("j_1", darwin[:1], 15*time.Minute)
	cases := map[string]struct {
		jobs []queue.QueuedJob
		pool []wire.Peer
	}{
		"a host with its worker on holds the labels":                             {[]queue.QueuedJob{job}, []wire.Peer{host("mlab", darwin)}},
		"it holds them though it has no room now (that is stalled, if anything)": {[]queue.QueuedJob{job}, []wire.Peer{host("mlab", darwin, full)}},
		"the job is on hold":     {[]queue.QueuedJob{qjob("j_1", darwin[:1], time.Hour, held)}, []wire.Peer{host("mserv", linux)}},
		"submitted a minute ago": {[]queue.QueuedJob{qjob("j_1", darwin[:1], time.Minute)}, []wire.Peer{host("mserv", linux)}},
		"an empty queue":         {nil, []wire.Peer{host("mserv", linux)}},
		"an explicit-only host takes a job that names its os": {
			[]queue.QueuedJob{qjob("j_1", linux[:1], 15*time.Minute)}, []wire.Peer{host("mserv", linux, explicitOnly)}},
	}
	for name, c := range cases {
		if got, _ := settledHolds(t, queueIn(c.jobs, c.pool...)); got {
			t.Errorf("%s: holds", name)
		}
	}
}

// What a worker would not take, the rule does not call takeable: an explicit-only host (mserv) leaves a job with no os:/task:/host: label with
// nobody, which is unclaimable, not "taken by a host that will not".
func TestQueueUnclaimable_AnExplicitOnlyHostDoesNotTakeAnUnlabelledJob(t *testing.T) {
	job := qjob("j_1", []string{"repo:purdex"}, 15*time.Minute)
	mserv := host("mserv", append([]string{"repo:purdex"}, linux...), explicitOnly)
	if got, _ := settledHolds(t, queueIn([]queue.QueuedJob{job}, mserv)); !got {
		t.Error("only a host that takes the job only when it names its os: the job names none")
	}
	// a host that is not explicit-only takes it
	if got, _ := settledHolds(t, queueIn([]queue.QueuedJob{job}, mserv, host("mlab", append([]string{"repo:purdex"}, darwin...)))); got {
		t.Error("mlab takes it")
	}
}

// ---- both: one job is never both stalled and unclaimable ----

func TestStalledAndUnclaimableNameDifferentJobs(t *testing.T) {
	jobs := []queue.QueuedJob{
		qjob("j_takeable", darwin[:1], 15*time.Minute), // mlab holds it and has room
		qjob("j_nobody", []string{"repo:aigora-v2"}, 15*time.Minute),
	}
	pool := []wire.Peer{host("mlab", darwin)}
	_, s := holds(t, QueueStalled{}, queueIn(jobs, pool...))
	_, u := settledHolds(t, queueIn(jobs, pool...))
	if !strings.Contains(s.Detail, "j_takeable") || strings.Contains(s.Detail, "j_nobody") {
		t.Errorf("stalled: %q", s.Detail)
	}
	if !strings.Contains(u.Detail, "j_nobody") || strings.Contains(u.Detail, "j_takeable") {
		t.Errorf("unclaimable: %q", u.Detail)
	}
}

// ---- when the inputs are not there ----

func TestQueueRulesAreUnjudgedWithoutTheQueueOrThePool(t *testing.T) {
	rules := []Rule{QueueStalled{}, JobRequeued{}, QueueBacklog{}, &QueueUnclaimable{}}
	pool := func() []wire.Peer { return nil }
	q := func() ([]queue.QueuedJob, error) { return nil, nil }
	for name, in := range map[string]Input{
		"neither":                  {Now: t0},
		"no pool":                  {Now: t0, Queue: q},
		"no queue":                 {Now: t0, Pool: pool},
		"the queue cannot be read": {Now: t0, Pool: pool, Queue: func() ([]queue.QueuedJob, error) { return nil, errors.New("locked") }},
	} {
		for _, r := range rules {
			if _, err := r.Eval(in); err == nil {
				t.Errorf("%s: %s judged", name, r.Kind())
			}
		}
	}
}

// Through the evaluator: a rule that cannot be judged leaves its alert as it was; one that holds raises, and clears when it stops.
func TestQueueRulesThroughTheEvaluator(t *testing.T) {
	book := newBook()
	jobs := []queue.QueuedJob{qjob("j_1", darwin[:1], 30*time.Minute)}
	broken := false
	in := Input{Now: t0, Pool: func() []wire.Peer { return []wire.Peer{host("mlab", darwin)} }, Queue: func() ([]queue.QueuedJob, error) {
		if broken {
			return nil, errors.New("locked")
		}
		return jobs, nil
	}}
	ev := NewEvaluator(book, func() (Input, bool) { return in, true }, nil, QueueStalled{}, JobRequeued{}, QueueBacklog{}, &QueueUnclaimable{})
	ev.Pass()
	if _, ok := book.active["queue_backlog"]; !ok || book.active["queue_backlog"].Level != wire.AlertWatch || book.active["queue_backlog"].Kind != wire.AlertQueueBacklog {
		t.Fatalf("backlog not raised: %+v", book.active)
	}
	if _, ok := book.active["queue_stalled"]; !ok {
		t.Fatalf("stalled not raised: %+v", book.active)
	}
	if len(book.active) != 2 {
		t.Fatalf("only those two hold: %+v", book.active)
	}
	broken = true
	ev.Pass()
	if len(book.active) != 2 {
		t.Fatalf("an unreadable queue leaves the alerts as they were: %+v", book.active)
	}
	broken, jobs = false, nil
	ev.Pass()
	if len(book.active) != 0 {
		t.Fatalf("an empty queue clears them: %+v", book.active)
	}
}

// ---- the clock of (1) and (4): a person moving a job that was never put back does not start it again (lead's ruling on #3902) ----

func TestQueueStalled_AJobNeverPutBackIgnoresTouched(t *testing.T) {
	pool := []wire.Peer{host("mlab", darwin)}
	// submitted 6 minutes ago, moved to the front 1 minute ago, attempts 0: still waiting since the submission.
	got, _ := holds(t, QueueStalled{}, queueIn([]queue.QueuedJob{qjob("j", darwin[:1], 6*time.Minute, touched(time.Minute))}, pool...))
	if !got {
		t.Error("a move must not reset the clock of a job that was never put back")
	}
}

func TestQueueStalled_AJobPutBackCountsFromTheLastChange(t *testing.T) {
	pool := []wire.Peer{host("mlab", darwin)}
	got, _ := holds(t, QueueStalled{}, queueIn([]queue.QueuedJob{qjob("j", darwin[:1], 6*time.Minute, attempts(1), touched(time.Minute))}, pool...))
	if got {
		t.Error("put back a minute ago: the five minutes start again")
	}
}

func TestQueueUnclaimable_CountsFromTheSubmissionNotFromTheLastChange(t *testing.T) {
	pool := []wire.Peer{host("mlab", darwin)}
	for _, n := range []int{0, 2} {
		got, _ := settledHolds(t, queueIn([]queue.QueuedJob{qjob("j", []string{"repo:aigora-v2"}, 12*time.Minute, attempts(n), touched(time.Minute))}, pool...))
		if !got {
			t.Errorf("attempts %d: a move must not reset the clock", n)
		}
	}
}

// A selective hold (an exception with `allow`) leaves room only for the jobs it lists: the rule reads it as the worker does (codex R1 on #3903).
func selectiveHold(repos ...string) hostOpt {
	return func(p *wire.Peer) {
		p.Facts.Derived.Zone, p.Facts.Derived.Headroom.Heavy = wire.ZoneNormal, 0
		p.Facts.Worker.Enabled, p.Facts.Worker.Slots.Heavy = true, 1
		p.Facts.Exception = []wire.FactExc{{Name: "dev", Allow: &wire.HoldAllow{Repos: repos}}}
	}
}

func TestQueueStalled_ASelectiveHoldOnlyLeavesRoomForTheJobsItLists(t *testing.T) {
	pool := []wire.Peer{host("mlab", darwin, selectiveHold("purdex"))}
	listed := qjob("j_in", darwin[:1], 10*time.Minute, func(j *queue.QueuedJob) { j.Repo = "purdex" })
	other := qjob("j_out", darwin[:1], 10*time.Minute, func(j *queue.QueuedJob) { j.Repo = "aigora" })
	if got, _ := holds(t, QueueStalled{}, queueIn([]queue.QueuedJob{listed}, pool...)); !got {
		t.Error("a job the hold lists is waiting for a slot the worker would give it")
	}
	if got, _ := holds(t, QueueStalled{}, queueIn([]queue.QueuedJob{other}, pool...)); got {
		t.Error("a job the hold does not list is not waiting for anything the worker would give it")
	}
}

// Two jobs that only different hosts can take: each is told with the host it waits on, not both with the first host (codex R2).
func TestQueueStalled_EachJobIsNamedWithItsOwnHost(t *testing.T) {
	pool := []wire.Peer{host("mlab", darwin), host("mserv", linux)}
	jobs := []queue.QueuedJob{qjob("j_mac", darwin[:1], 10*time.Minute), qjob("j_lin", linux[:1], 10*time.Minute)}
	got, v := holds(t, QueueStalled{}, queueIn(jobs, pool...))
	if !got {
		t.Fatal("both are waiting")
	}
	if !strings.Contains(v.Detail, "mlab 有空位，j_mac") || !strings.Contains(v.Detail, "mserv 有空位，j_lin") {
		t.Errorf("detail %q", v.Detail)
	}
}

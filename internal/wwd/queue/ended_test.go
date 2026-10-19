package queue

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

const specLabel = "task:purdex/spa-related" // the fixture's repo and task

func (f *fixture) reference(host string) {
	f.q.opt.ReferenceHost = func(repo, task string) string { return host }
}

// endAs runs a job of the fixture's task through submit, claim and result as a worker does and returns its id.
func (f *fixture) endAs(t *testing.T, worker string, claimLabels []string, exit int, edit ...func(*wire.JobSubmission)) string {
	t.Helper()
	edit = append([]func(*wire.JobSubmission){func(s *wire.JobSubmission) { s.DefHash = "def1"; s.SupersedeKey = "" }}, edit...)
	id := f.submit(t, edit...)
	c := f.claimAs(t, id, worker, claimLabels, fpA)
	state := wire.StateSucceeded
	if exit != 0 {
		state = wire.StateFailed
	}
	f.postResult(t, id, c.ClaimID, func(r *wire.ResultRequest) { r.State, r.Exit = state, &exit })
	return id
}

func (f *fixture) outcomeRows(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM task_outcomes`).Scan(&n))
	return n
}

// The outcome of a job is recorded in the transaction that ends it: a reference host's success and an earned host's failure of the same
// work are a disagreement about the earned host's label, found when the second job ends, dated by the origin's clock.
func TestAnOutcomeIsRecordedWhenTheJobEnds(t *testing.T) {
	f := newFixture(t)
	f.reference("air26")
	ref := f.endAs(t, "air26", []string{"os:linux"}, 0) // the reference holds the label without it being in its claim
	assert.Equal(t, 1, f.outcomeRows(t))
	bad := f.endAs(t, "mserv", []string{"os:linux", specLabel}, 1)

	got, _, err := f.q.Disagreements(bg, "mserv", 0, 100)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "air26", got[0].OtherHost)
	assert.Equal(t, [2]string{ref, bad}, got[0].Jobs)
	assert.Equal(t, specLabel, got[0].Label)
	assert.True(t, got[0].At.Equal(f.clk.Now()), "dated by the origin's clock")
	none, _, err := f.q.Disagreements(bg, "air26", 0, 100)
	require.NoError(t, err)
	assert.Empty(t, none, "the reference host is never the subject")
}

// What has no outcome to compare, or a host nobody has vouched for, is not recorded.
func TestWhatIsNotRecordedWhenAJobEnds(t *testing.T) {
	for name, tc := range map[string]struct {
		worker string
		labels []string
		exit   int
		sub    func(*wire.JobSubmission)
		post   func(*wire.ResultRequest)
		want   int
	}{
		"an earned host":            {worker: "mserv", labels: []string{specLabel}, exit: 1, want: 1},
		"the reference":             {worker: "air26", labels: []string{"os:linux"}, want: 1},
		"a host with no standing":   {worker: "mini", labels: []string{"os:linux"}, exit: 1},
		"a claim that said nothing": {worker: "mserv", labels: nil, exit: 1},
		"not cacheable":             {worker: "mserv", labels: []string{specLabel}, exit: 1, sub: func(s *wire.JobSubmission) { s.Cacheable = false }},
		"an undeclared one":         {worker: "mserv", labels: []string{specLabel}, exit: 1, sub: func(s *wire.JobSubmission) { s.AdHoc, s.Cacheable = true, false }},
		"no definition hash":        {worker: "mserv", labels: []string{specLabel}, exit: 1, sub: func(s *wire.JobSubmission) { s.DefHash = "" }},
		"no exit (a timeout)":       {worker: "mserv", labels: []string{specLabel}, post: func(r *wire.ResultRequest) { r.State, r.Exit, r.Reason = wire.StateFailed, nil, "timeout" }},
	} {
		f := newFixture(t)
		f.reference("air26")
		edit := []func(*wire.JobSubmission){func(s *wire.JobSubmission) { s.DefHash = "def1"; s.SupersedeKey = "" }}
		if tc.sub != nil {
			edit = append(edit, tc.sub)
		}
		id := f.submit(t, edit...)
		c := f.claimAs(t, id, tc.worker, tc.labels, fpA)
		state := wire.StateSucceeded
		if tc.exit != 0 {
			state = wire.StateFailed
		}
		f.postResult(t, id, c.ClaimID, func(r *wire.ResultRequest) {
			exit := tc.exit
			r.State, r.Exit = state, &exit
			if tc.post != nil {
				tc.post(r)
			}
		})
		assert.Equal(t, tc.want, f.outcomeRows(t), name)
	}
}

// A run replaced while it ran, and an echo of a result, are recorded once or not at all.
func TestAReplacedRunIsNotRecordedAndAnEchoIsNotRecordedTwice(t *testing.T) {
	f := newFixture(t)
	f.reference("mserv")
	id, c := f.running(t)
	f.submit(t) // replaces the running one
	f.postResult(t, id, c.ClaimID)
	assert.Zero(t, f.outcomeRows(t), "replaced while it ran")

	g := newFixture(t)
	g.reference("mserv")
	id2 := g.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1" })
	c2 := g.claimAs(t, id2, "mserv", lblA, fpA)
	g.postResult(t, id2, c2.ClaimID)
	g.postResult(t, id2, c2.ClaimID)
	assert.Equal(t, 1, g.outcomeRows(t), "the echo records nothing more")
}

// The reference host is the one of the moment the job ends: it is asked then, not when something is looked at afterwards.
func TestTheReferenceIsTheOneOfTheMomentTheJobEnds(t *testing.T) {
	f := newFixture(t)
	f.reference("air26")
	ref := f.endAs(t, "air26", []string{"os:linux"}, 0) // air26 is the reference now: its outcome is recorded as the reference's
	bad := f.endAs(t, "mini", []string{"os:linux", specLabel}, 1)
	got, _, err := f.q.Disagreements(bg, "mini", 0, 100)
	require.NoError(t, err)
	require.Len(t, got, 1, "air26's outcome was the reference's when it ended")
	assert.Equal(t, [2]string{ref, bad}, got[0].Jobs)

	f.reference("mserv") // and then the reference is another host: what came out under air26 is of another generation
	f.endAs(t, "mini", []string{"os:linux", specLabel}, 1)
	again, _, err := f.q.Disagreements(bg, "mini", 0, 100)
	require.NoError(t, err)
	assert.Len(t, again, 1, "no new disagreement: the new reference has not answered yet, and air26 no longer speaks for the work")
	none, _, err := f.q.Disagreements(bg, "air26", 0, 100)
	require.NoError(t, err)
	assert.Empty(t, none, "it is not made a subject by having stopped being the reference")
}

// The reference changes from one host to another and the two disagree: they are not compared as two references (nobody to quarantine) and
// the old reference is not the authority over the new generation: the earned host is compared with the reference of its own time.
func TestAReferenceRotationDoesNotSilenceOrMisdirect(t *testing.T) {
	f := newFixture(t)
	f.reference("air26")
	f.endAs(t, "air26", []string{"os:linux"}, 0) // reference A succeeds
	f.reference("mserv")
	f.endAs(t, "mserv", []string{"os:linux"}, 1) // reference B fails the same work
	f.endAs(t, "mini", []string{"os:linux", specLabel}, 1)
	for _, subject := range []string{"air26", "mserv", "mini"} {
		got, _, err := f.q.Disagreements(bg, subject, 0, 100)
		require.NoError(t, err)
		if subject == "mini" {
			assert.Empty(t, got, "mini agrees with the reference of its time (B), and A is not the authority over it")
		} else {
			assert.Empty(t, got, subject)
		}
	}
	f.endAs(t, "mini", []string{"os:linux", specLabel}, 0) // now mini differs from B
	got, _, err := f.q.Disagreements(bg, "mini", 0, 100)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "mserv", got[0].OtherHost)
}

// The record and the end are one transaction: if the outcome cannot be written, the result is not taken (the worker repeats it), and when
// it can be, the job ends and the outcome is there. Nothing is left that ended without its outcome.
func TestTheOutcomeAndTheEndAreOneTransaction(t *testing.T) {
	f := newFixture(t)
	f.reference("air26")
	id := f.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1" })
	c := f.claimAs(t, id, "air26", []string{"os:linux"}, fpA)
	_, err := f.db.Exec(`ALTER TABLE task_outcomes RENAME TO task_outcomes_away`) // the table the record goes into is not there
	require.NoError(t, err)

	zero := 0
	_, err = f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: &zero})
	require.Error(t, err)
	assert.Equal(t, wire.StateClaimed, f.get(t, id).State, "the result was not taken")

	_, err = f.db.Exec(`ALTER TABLE task_outcomes_away RENAME TO task_outcomes`)
	require.NoError(t, err)
	_, err = f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: &zero})
	require.NoError(t, err, "the worker's repeat is taken")
	assert.Equal(t, wire.StateSucceeded, f.get(t, id).State)
	assert.Equal(t, 1, f.outcomeRows(t))
}

// A reference that cannot be had (the hook panics) is not "no reference": the result is not taken, so the worker repeats it and the
// outcome is judged with the reference it should have had.
func TestAReferenceThatCannotBeHadFailsTheResult(t *testing.T) {
	f := newFixture(t)
	f.q.opt.ReferenceHost = func(repo, task string) string { panic("manifest unreadable") }
	id := f.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1" })
	c := f.claimAs(t, id, "air26", []string{"os:linux"}, fpA)
	zero := 0
	_, err := f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: &zero})
	require.Error(t, err)
	assert.Equal(t, wire.StateClaimed, f.get(t, id).State, "the result was not taken")

	f.reference("air26")
	_, err = f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: &zero})
	require.NoError(t, err)
	assert.Equal(t, 1, f.outcomeRows(t), "recorded as the reference's once it can be had")
}

// The echo of a result that was taken is answered whatever the reference hook does: only a result that is going to end the job needs it.
func TestTheEchoOfATakenResultDoesNotNeedTheReference(t *testing.T) {
	f := newFixture(t)
	f.reference("air26")
	id := f.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1" })
	c := f.claimAs(t, id, "air26", []string{"os:linux"}, fpA)
	zero := 0
	req := wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: &zero}
	_, err := f.q.Result(bg, id, req)
	require.NoError(t, err)

	f.q.opt.ReferenceHost = func(repo, task string) string { panic("manifest unreadable") }
	got, err := f.q.Result(bg, id, req)
	require.NoError(t, err, "the same result again is the stored job")
	assert.Equal(t, wire.StateSucceeded, got.State)
	assert.Equal(t, 1, f.outcomeRows(t))
}

// Of a host's many outcomes of the same work only the latest is compared, however many there are: the host ends on a failure, which
// disagrees with the reference's success, and there is one disagreement per pair, not one per earlier outcome.
func TestOnlyAHostsLatestOutcomeOfTheWorkIsCompared(t *testing.T) {
	f := newFixture(t)
	f.reference("air26")
	f.endAs(t, "mserv", []string{"os:linux", specLabel}, 1) // an old failure ...
	for i := 0; i < 5; i++ {
		f.endAs(t, "mserv", []string{"os:linux", specLabel}, 0) // ... and then only successes
	}
	f.endAs(t, "air26", []string{"os:linux"}, 0)
	none, _, err := f.q.Disagreements(bg, "mserv", 0, 100)
	require.NoError(t, err)
	assert.Empty(t, none, "the old failure does not count: the host's latest agrees with the reference")
	bad := f.endAs(t, "mserv", []string{"os:linux", specLabel}, 1)
	got, _, err := f.q.Disagreements(bg, "mserv", 0, 100)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, bad, got[0].Jobs[1])
}

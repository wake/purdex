package queue

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

type offered struct {
	jobs []wire.Job
	prod []Produced
}

func (f *fixture) offers() *offered {
	o := &offered{}
	f.q.opt.OnSucceeded = func(_ context.Context, j wire.Job, p Produced) {
		o.jobs, o.prod = append(o.jobs, j), append(o.prod, p)
	}
	return o
}

func (f *fixture) claimAs(t *testing.T, id, worker string, labels []string, fp map[string]string) wire.ClaimResponse {
	t.Helper()
	r, err := f.q.Claim(bg, id, wire.ClaimRequest{Worker: worker, Labels: labels, Fingerprint: fp})
	require.NoError(t, err)
	return r
}

func ok0() wire.ResultRequest {
	zero := 0
	return wire.ResultRequest{State: wire.StateSucceeded, Exit: &zero}
}

func (f *fixture) postResult(t *testing.T, id, claimID string, edit ...func(*wire.ResultRequest)) {
	t.Helper()
	r := ok0()
	r.ClaimID = claimID
	for _, e := range edit {
		e(&r)
	}
	_, err := f.q.Result(bg, id, r)
	require.NoError(t, err)
}

var (
	lblA = []string{"os:linux", "arch:amd64", "repo:purdex"}
	fpA  = map[string]string{"os_version": "6.8", "node": "24.14.0"}
	fpB  = map[string]string{"os_version": "6.9", "node": "24.14.0"}
)

// A submission's definition hash and a claim's labels and fingerprint are kept with the job, and the success is offered with them.
func TestASucceededResultIsOfferedWithTheClaimsWords(t *testing.T) {
	f := newFixture(t)
	o := f.offers()
	id := f.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1" })
	c := f.claimAs(t, id, "mserv", lblA, fpA)
	f.postResult(t, id, c.ClaimID)

	require.Len(t, o.jobs, 1)
	assert.Equal(t, id, o.jobs[0].ID)
	assert.Equal(t, wire.StateSucceeded, o.jobs[0].State)
	assert.Equal(t, Produced{DefHash: "def1", Labels: lblA, Fingerprint: fpA}, o.prod[0])
	assert.Equal(t, "mserv", o.jobs[0].Result.Host)
}

// A job that is claimed again (the lease ran out) is filed under the claim that ended it: the columns follow the claim, and the first
// claim's result is refused.
func TestAReclaimedJobIsFiledUnderTheClaimThatEndedIt(t *testing.T) {
	f := newFixture(t)
	o := f.offers()
	id := f.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1" })
	first := f.claimAs(t, id, "mserv", lblA, fpA)
	f.clk.Advance(61 * time.Second)
	_, err := f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, wire.StateQueued, f.get(t, id).State, "the lapsed lease put it back")
	second := f.claimAs(t, id, "air26", []string{"os:darwin", "arch:arm64"}, fpB)

	r := ok0()
	r.ClaimID = first.ClaimID
	_, err = f.q.Result(bg, id, r)
	require.Error(t, err, "the lapsed claim's result is refused")
	assert.Empty(t, o.jobs)

	f.postResult(t, id, second.ClaimID)
	require.Len(t, o.jobs, 1)
	assert.Equal(t, Produced{DefHash: "def1", Labels: []string{"os:darwin", "arch:arm64"}, Fingerprint: fpB}, o.prod[0])
	assert.Equal(t, "air26", o.jobs[0].Result.Host)
}

// The same result posted again is an echo: it is answered, and not offered a second time.
func TestAnEchoedResultIsNotOfferedAgain(t *testing.T) {
	f := newFixture(t)
	o := f.offers()
	id := f.submit(t)
	c := f.claimAs(t, id, "mserv", lblA, fpA)
	f.postResult(t, id, c.ClaimID)
	f.postResult(t, id, c.ClaimID)
	assert.Len(t, o.jobs, 1)
}

// Only a worker's success of a cacheable job that was not replaced is offered.
func TestWhatIsNotOfferedForReuse(t *testing.T) {
	one := 1
	for name, tc := range map[string]struct {
		sub  func(*wire.JobSubmission)
		post func(*wire.ResultRequest)
	}{
		"a failure":         {post: func(r *wire.ResultRequest) { r.State, r.Exit = wire.StateFailed, &one }},
		"not cacheable":     {sub: func(s *wire.JobSubmission) { s.Cacheable = false }},
		"an undeclared one": {sub: func(s *wire.JobSubmission) { s.AdHoc = true; s.Cacheable = false }},
	} {
		f := newFixture(t)
		o := f.offers()
		var edit []func(*wire.JobSubmission)
		if tc.sub != nil {
			edit = append(edit, tc.sub)
		}
		id := f.submit(t, edit...)
		c := f.claimAs(t, id, "mserv", lblA, fpA)
		var post []func(*wire.ResultRequest)
		if tc.post != nil {
			post = append(post, tc.post)
		}
		f.postResult(t, id, c.ClaimID, post...)
		assert.Empty(t, o.jobs, name)
	}
}

// A run that a newer submission replaced while it ran (and that could not be stopped) ends with superseded_by: it is not offered.
func TestARunReplacedWhileItRanIsNotOffered(t *testing.T) {
	f := newFixture(t)
	o := f.offers()
	id, c := f.running(t)
	f.submit(t) // the newer submission with the same key
	f.postResult(t, id, c.ClaimID)
	assert.Empty(t, o.jobs)
}

// A worker that said nothing about itself is offered with nothing: the daemon will not file it.
func TestAWorkerThatSaidNothingIsOfferedWithNothing(t *testing.T) {
	f := newFixture(t)
	o := f.offers()
	id := f.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1" })
	c := f.claimAs(t, id, "old-worker", nil, nil)
	f.postResult(t, id, c.ClaimID)
	require.Len(t, o.prod, 1)
	assert.Equal(t, Produced{DefHash: "def1"}, o.prod[0])
}

// Version 6 → 7: the three columns appear, with nothing in them for the jobs already there, and the step can run again.
func TestAVersionSixDatabaseGainsTheReuseColumns(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	for _, col := range []string{"def_hash", "claim_labels", "claim_fingerprint"} {
		_, err := f.db.Exec(`ALTER TABLE jobs DROP COLUMN ` + col)
		require.NoError(t, err)
	}
	_, err := f.db.Exec(`UPDATE queue_schema SET version = 6`)
	require.NoError(t, err)

	require.NoError(t, Migrate(bg, f.db))
	require.NoError(t, Migrate(bg, f.db))
	var def, lbl, fp string
	require.NoError(t, f.db.QueryRow(`SELECT def_hash, claim_labels, claim_fingerprint FROM jobs WHERE id = ?`, id).Scan(&def, &lbl, &fp))
	assert.Equal(t, [3]string{}, [3]string{def, lbl, fp})
	var v int
	require.NoError(t, f.db.QueryRow(`SELECT version FROM queue_schema`).Scan(&v))
	assert.Equal(t, 7, v)
}

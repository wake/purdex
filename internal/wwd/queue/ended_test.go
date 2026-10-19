package queue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

func (f *fixture) endings() *[]wire.Job {
	var got []wire.Job
	f.q.opt.OnEnded = func(_ context.Context, j wire.Job, _ Produced) { got = append(got, j) }
	return &got
}

// A success and a failure with an exit of their own are outcomes to compare; the failure is not a reusable success.
func TestASuccessAndAFailureWithAnExitAreOffered(t *testing.T) {
	one := 1
	for name, post := range map[string]func(*wire.ResultRequest){
		"a success": nil,
		"a failure": func(r *wire.ResultRequest) { r.State, r.Exit = wire.StateFailed, &one },
	} {
		f := newFixture(t)
		ended := f.endings()
		reused := f.offers()
		id := f.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1" })
		c := f.claimAs(t, id, "mserv", lblA, fpA)
		var edit []func(*wire.ResultRequest)
		if post != nil {
			edit = append(edit, post)
		}
		f.postResult(t, id, c.ClaimID, edit...)
		require.Len(t, *ended, 1, name)
		assert.Equal(t, id, (*ended)[0].ID, name)
		assert.Equal(t, name == "a success", len(reused.jobs) == 1, name)
	}
}

// What says nothing of the host's toolchain, or is not a worker's own result, is not offered as an outcome.
func TestWhatIsNotAnOutcomeToCompare(t *testing.T) {
	for name, tc := range map[string]struct {
		sub  func(*wire.JobSubmission)
		post func(*wire.ResultRequest)
	}{
		"no exit (a timeout)": {post: func(r *wire.ResultRequest) { r.State, r.Exit, r.Reason = wire.StateFailed, nil, "timeout" }},
		"not cacheable":       {sub: func(s *wire.JobSubmission) { s.Cacheable = false }},
		"an undeclared one":   {sub: func(s *wire.JobSubmission) { s.AdHoc, s.Cacheable = true, false }},
	} {
		f := newFixture(t)
		ended := f.endings()
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
		assert.Empty(t, *ended, name)
	}
}

// A run replaced while it ran, and an echo of a result, are not offered.
func TestAReplacedRunAndAnEchoAreNotOfferedAsOutcomes(t *testing.T) {
	f := newFixture(t)
	ended := f.endings()
	id, c := f.running(t)
	f.submit(t) // replaces the running one
	f.postResult(t, id, c.ClaimID)
	assert.Empty(t, *ended, "replaced while it ran")

	g := newFixture(t)
	ended = g.endings()
	id2 := g.submit(t)
	c2 := g.claimAs(t, id2, "mserv", lblA, fpA)
	g.postResult(t, id2, c2.ClaimID)
	g.postResult(t, id2, c2.ClaimID)
	assert.Len(t, *ended, 1, "the echo is not offered again")
}

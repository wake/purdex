package daemon

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
)

func endedJob(id, host string, exit int, edit ...func(*wire.Job)) wire.Job {
	fin := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	j := wire.Job{ID: id, Repo: wire.Repo{Name: "purdex"}, Snapshot: wire.Snapshot{Tree: "7ee"}, Task: "go-changed", Args: []string{"./a"},
		Cacheable: true, State: wire.StateSucceeded, Result: &wire.Result{Exit: &exit, Host: host, Log: id + "/log", FinishedAt: &fin}}
	if exit != 0 {
		j.State = wire.StateFailed
	}
	for _, e := range edit {
		e(&j)
	}
	return j
}

func claimOf(withTaskLabel bool) queue.Produced {
	l := []string{"os:linux", "arch:amd64"}
	if withTaskLabel {
		l = append(l, "task:purdex/go-changed")
	}
	return queue.Produced{DefHash: "def1", Labels: l, Fingerprint: map[string]string{"k": "v"}}
}

func disagreementsAbout(t *testing.T, h *jobHost, subject string) wire.DisagreementList {
	t.Helper()
	code, body, _ := h.socketDo(t, "GET", "/v1/outcomes/disagreements?subject="+subject, nil)
	require.Equal(t, 200, code, string(body))
	l, err := wire.Decode[wire.DisagreementList](body)
	require.NoError(t, err)
	require.NoError(t, l.Validate())
	return l
}

// The outcome of a job counts when the host vouches for the task: it held the task's label at the claim, or it is the reference host. A
// pair of different outcomes of one piece of work is a disagreement about the non-reference host's label.
func TestAnEarnedHostThatDiffersFromTheReferenceIsADisagreement(t *testing.T) {
	h := newJobHost(t, "mlab")
	d := New(Options{Alias: "mlab", Queue: h.q, ReferenceTasks: func() []labels.RefTask {
		return []labels.RefTask{{Repo: "purdex", Task: "go-changed", Reference: "air26"}}
	}})
	ctx := t.Context()
	d.NoteOutcome(ctx, endedJob("j_ref", "air26", 0), claimOf(false)) // the reference holds the label without it being in its claim
	assert.Empty(t, disagreementsAbout(t, h, "mserv").Items)
	d.NoteOutcome(ctx, endedJob("j_bad", "mserv", 1), claimOf(true))
	l := disagreementsAbout(t, h, "mserv")
	require.Len(t, l.Items, 1)
	assert.Equal(t, "air26", l.Items[0].OtherHost)
	assert.Equal(t, [2]string{"j_ref", "j_bad"}, l.Items[0].Jobs)
	assert.Equal(t, "task:purdex/go-changed", l.Items[0].Label)
	assert.Empty(t, disagreementsAbout(t, h, "air26").Items, "the reference host is never the subject")
}

// A host nobody has vouched for has no label to lose: its outcome is not recorded, so it cannot start a disagreement.
func TestWhatIsNotRecordedAsAnOutcome(t *testing.T) {
	h := newJobHost(t, "mlab")
	d := New(Options{Alias: "mlab", Queue: h.q, ReferenceTasks: func() []labels.RefTask {
		return []labels.RefTask{{Repo: "purdex", Task: "go-changed", Reference: "air26"}}
	}})
	d.NoteOutcome(t.Context(), endedJob("j_ref", "air26", 0), claimOf(false))
	for name, tc := range map[string]struct {
		job wire.Job
		p   queue.Produced
	}{
		"a host with no standing":   {endedJob("j_a", "mini", 1), claimOf(false)},
		"no definition hash":        {endedJob("j_b", "mserv", 1), queue.Produced{Labels: claimOf(true).Labels}},
		"a claim that said nothing": {endedJob("j_c", "mserv", 1), queue.Produced{DefHash: "def1"}},
		"no exit":                   {endedJob("j_d", "mserv", 1, func(j *wire.Job) { j.Result.Exit = nil }), claimOf(true)},
		"no result":                 {endedJob("j_e", "mserv", 1, func(j *wire.Job) { j.Result = nil }), claimOf(true)},
		"no host in the result":     {endedJob("j_f", "", 1), claimOf(true)},
	} {
		d.NoteOutcome(t.Context(), tc.job, tc.p)
		assert.Empty(t, disagreementsAbout(t, h, "mserv").Items, name)
		assert.Empty(t, disagreementsAbout(t, h, "mini").Items, name)
	}
	// and a daemon with no queue does nothing
	New(Options{Alias: "mlab"}).NoteOutcome(t.Context(), endedJob("j_g", "mserv", 1), claimOf(true))
}

// The route is on both surfaces: the socket for the host's own reader, TCP with the pool token for a peer; the parameter that names the
// subject is not the one the socket forwards on, and the TCP surface refuses ?host= whatever the route.
func TestTheDisagreementsRouteOnBothSurfaces(t *testing.T) {
	h := newJobHost(t, "mlab")
	_, err := h.q.RecordOutcome(t.Context(), queue.Outcome{JobID: "j_r", Host: "air26", Label: "task:purdex/go-changed", Tree: "t", Task: "go-changed",
		DefHash: "d", OK: true, Standing: queue.StandingReference, At: time.Now()})
	require.NoError(t, err)
	_, err = h.q.RecordOutcome(t.Context(), queue.Outcome{JobID: "j_m", Host: "mserv", Label: "task:purdex/go-changed", Tree: "t", Task: "go-changed",
		DefHash: "d", OK: false, Standing: queue.StandingEarned, At: time.Now()})
	require.NoError(t, err)

	code, body, _ := h.tcpDo(t, "GET", "/v1/outcomes/disagreements?subject=mserv", "Bearer "+testToken, nil)
	require.Equal(t, http.StatusOK, code, string(body))
	l, err := wire.Decode[wire.DisagreementList](body)
	require.NoError(t, err)
	require.NoError(t, l.Validate())
	assert.Len(t, l.Items, 1)
	assert.Equal(t, "mlab", l.Origin)

	code, _, _ = h.tcpDo(t, "GET", "/v1/outcomes/disagreements?subject=mserv", "", nil)
	assert.Equal(t, http.StatusUnauthorized, code, "a peer needs the pool token")
	code, _, _ = h.tcpDo(t, "GET", "/v1/outcomes/disagreements?subject=mserv&host=air26", "Bearer "+testToken, nil)
	assert.Equal(t, http.StatusBadRequest, code, "?host= is not accepted over TCP")
	code, _, _ = h.socketDo(t, "GET", "/v1/outcomes/disagreements?subject=mserv&host=air26", nil)
	assert.Equal(t, http.StatusBadRequest, code, "and the socket does not forward this read")
	code, _, _ = h.tcpDo(t, "POST", "/v1/outcomes/disagreements?subject=mserv", "Bearer "+testToken, nil)
	assert.NotEqual(t, http.StatusOK, code, "there is no way to write one over TCP")
}

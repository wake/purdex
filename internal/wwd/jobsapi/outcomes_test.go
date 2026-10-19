package jobsapi

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
)

func outcomesRig(t *testing.T) (http.Handler, *rig) {
	t.Helper()
	r := newRig(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/outcomes/disagreements", New(r.q).Disagreements())
	// the reference passes and mserv fails the same work, for three different snapshots
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for i, tree := range []string{"t1", "t2", "t3"} {
		for _, o := range []queue.Outcome{
			{JobID: "j_r" + tree, Host: "air26", Standing: queue.StandingReference, OK: true},
			{JobID: "j_m" + tree, Host: "mserv", Standing: queue.StandingEarned, OK: false},
		} {
			o.Label, o.Tree, o.Task, o.DefHash, o.At = "task:purdex/go-changed", tree, "go-changed", "def1", at.Add(time.Duration(i)*time.Minute)
			_, err := r.q.RecordOutcome(t.Context(), o)
			require.NoError(t, err)
		}
	}
	return mux, r
}

// The answer is the disagreements that concern the subject, as a list a reader can validate; the cursor moves, and a piece is cut at the limit.
func TestDisagreementsAreServedBySubjectAndCursor(t *testing.T) {
	h, r := outcomesRig(t)
	w := call(h, "GET", "/v1/outcomes/disagreements?subject=mserv", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	all := decode[wire.DisagreementList](t, w)
	require.NoError(t, all.Validate())
	assert.Equal(t, "mserv", all.Host)
	assert.Equal(t, r.q.Origin(), all.Origin)
	require.Len(t, all.Items, 3)
	assert.False(t, all.More)
	assert.Equal(t, all.Items[2].ID, all.Cursor)

	w = call(h, "GET", "/v1/outcomes/disagreements?subject=mserv&limit=2", nil)
	first := decode[wire.DisagreementList](t, w)
	require.NoError(t, first.Validate())
	assert.Len(t, first.Items, 2)
	assert.True(t, first.More)

	w = call(h, "GET", "/v1/outcomes/disagreements?subject=mserv&after="+itoa(first.Cursor), nil)
	rest := decode[wire.DisagreementList](t, w)
	require.NoError(t, rest.Validate())
	require.Len(t, rest.Items, 1)
	assert.Equal(t, all.Items[2].ID, rest.Items[0].ID)

	// past the last one: an empty list that keeps the cursor
	w = call(h, "GET", "/v1/outcomes/disagreements?subject=mserv&after="+itoa(all.Cursor), nil)
	none := decode[wire.DisagreementList](t, w)
	require.NoError(t, none.Validate())
	assert.Empty(t, none.Items)
	assert.Equal(t, all.Cursor, none.Cursor)

	// the reference host is never the subject of one
	w = call(h, "GET", "/v1/outcomes/disagreements?subject=air26", nil)
	assert.Empty(t, decode[wire.DisagreementList](t, w).Items)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// The query is strict: a missing, repeated or malformed parameter, a parameter that is not one of these, and in particular ?host=
// (the socket's forwarding parameter, which the TCP surface refuses) is a 400, and nothing is read.
func TestTheDisagreementsQueryIsStrict(t *testing.T) {
	h, _ := outcomesRig(t)
	for name, target := range map[string]string{
		"no subject":                         "/v1/outcomes/disagreements",
		"an empty subject":                   "/v1/outcomes/disagreements?subject=",
		"a subject given twice":              "/v1/outcomes/disagreements?subject=mserv&subject=air26",
		"a subject with a control character": "/v1/outcomes/disagreements?subject=a%0Ab",
		"an empty cursor":                    "/v1/outcomes/disagreements?subject=mserv&after=",
		"an empty limit":                     "/v1/outcomes/disagreements?subject=mserv&limit=",
		"an empty cursor, then a good one":   "/v1/outcomes/disagreements?subject=mserv&after=&after=1",
		"an empty limit, then a good one":    "/v1/outcomes/disagreements?subject=mserv&limit=&limit=2",
		"host instead of subject":            "/v1/outcomes/disagreements?host=mserv",
		"host beside a good subject":         "/v1/outcomes/disagreements?subject=mserv&host=air26",
		"an unknown parameter":               "/v1/outcomes/disagreements?subject=mserv&x=1",
		"a negative cursor":                  "/v1/outcomes/disagreements?subject=mserv&after=-1",
		"a cursor that is not a number":      "/v1/outcomes/disagreements?subject=mserv&after=abc",
		"a cursor given twice":               "/v1/outcomes/disagreements?subject=mserv&after=1&after=2",
		"a limit of 0":                       "/v1/outcomes/disagreements?subject=mserv&limit=0",
		"a limit over the maximum":           "/v1/outcomes/disagreements?subject=mserv&limit=201",
		"a limit that is not a number":       "/v1/outcomes/disagreements?subject=mserv&limit=many",
	} {
		assert.Equal(t, 400, call(h, "GET", target, nil).Code, name)
	}
	assert.Equal(t, 200, call(h, "GET", "/v1/outcomes/disagreements?subject=mserv&limit=200", nil).Code, "the maximum is allowed")
	// only GET: the route has no write method
	assert.Equal(t, 405, call(h, "POST", "/v1/outcomes/disagreements?subject=mserv", nil).Code)
}

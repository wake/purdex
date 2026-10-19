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

func disagreementsAbout(t *testing.T, h *jobHost, subject string) wire.DisagreementList {
	t.Helper()
	code, body, _ := h.socketDo(t, "GET", "/v1/outcomes/disagreements?subject="+subject, nil)
	require.Equal(t, 200, code, string(body))
	l, err := wire.Decode[wire.DisagreementList](body)
	require.NoError(t, err)
	require.NoError(t, l.Validate())
	return l
}

// What the queue asks when a job ends (Options.ReferenceHost) is the daemon's answer from the manifests of its repos: the host named
// by the task's qualify.reference, "" for a task none names.
func TestTheDaemonNamesTheReferenceHostOfATask(t *testing.T) {
	d := New(Options{Alias: "mlab", ReferenceTasks: func() []labels.RefTask {
		return []labels.RefTask{{Repo: "purdex", Task: "go-changed", Reference: "air26"}}
	}})
	assert.Equal(t, "air26", d.ReferenceHost("purdex", "go-changed"))
	assert.Equal(t, "", d.ReferenceHost("purdex", "lint"))
	assert.Equal(t, "", d.ReferenceHost("other", "go-changed"))
	assert.Equal(t, "", New(Options{Alias: "mlab"}).ReferenceHost("purdex", "go-changed"), "no manifests: no reference")
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
	assert.Len(t, disagreementsAbout(t, h, "mserv").Items, 1, "and over the socket")

	code, _, _ = h.tcpDo(t, "GET", "/v1/outcomes/disagreements?subject=mserv", "", nil)
	assert.Equal(t, http.StatusUnauthorized, code, "a peer needs the pool token")
	code, _, _ = h.tcpDo(t, "GET", "/v1/outcomes/disagreements?subject=mserv&host=air26", "Bearer "+testToken, nil)
	assert.Equal(t, http.StatusBadRequest, code, "?host= is not accepted over TCP")
	code, _, _ = h.socketDo(t, "GET", "/v1/outcomes/disagreements?subject=mserv&host=air26", nil)
	assert.Equal(t, http.StatusBadRequest, code, "and the socket does not forward this read")
	code, _, _ = h.tcpDo(t, "POST", "/v1/outcomes/disagreements?subject=mserv", "Bearer "+testToken, nil)
	assert.NotEqual(t, http.StatusOK, code, "there is no way to write one over TCP")
}

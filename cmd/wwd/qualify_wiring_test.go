package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/clones"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/qualify"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The route reaches a real coordinator: a request on the socket takes a snapshot of the real clone, queues the two halves into the real queue
// (this host's candidate half without the Mac-only labels, the reference's with host:<reference>), and the status lists the pair. This is the
// test that the pieces are wired to each other, not each alone.
func TestARequestOnTheSocketQueuesTheTwoHalvesOfAPair(t *testing.T) {
	root := qualifyRepo(t, qualifyManifest)
	st, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	require.NoError(t, err)
	defer st.Close()
	require.NoError(t, queue.Migrate(t.Context(), st.DB()))
	q, err := queue.New(st.DB(), queue.Options{Origin: "mserv", LogDir: t.TempDir()})
	require.NoError(t, err)

	var d *daemon.Daemon
	cfg := daemon.Config{Repos: map[string]string{"purdex": root}}
	coordinator := qualify.New(qualify.Config{
		Queue: q, Store: st,
		Source: &qualifySource{repos: clones.NewSource(cfg.Repos), host: func() qualify.Host {
			return qualify.Host{Alias: "mserv", WorkerOn: true, Labels: []string{"repo:purdex", "tool:node24", "os:linux"}, Fingerprint: map[string]string{"node": "24"}}
		}},
		Granted: func() { d.RefreshLabels() },
	})
	d = daemon.New(daemonOptions(daemonParts{HostID: "h1", Alias: "mserv", Token: "tok", Cfg: cfg, Queue: q, Store: st, Qualifier: coordinator}))

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/qualify", strings.NewReader(body))
		w := httptest.NewRecorder()
		d.SocketHandler().ServeHTTP(w, req)
		return w
	}
	w := post(`{"task":"purdex/tsc"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var started wire.QualifyStarted
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &started))
	assert.Equal(t, "task:purdex/tsc", started.Label)

	cand, err := q.Get(t.Context(), started.Candidate)
	require.NoError(t, err)
	ref, err := q.Get(t.Context(), started.Reference)
	require.NoError(t, err)
	assert.Equal(t, "tsc", cand.Task)
	assert.Equal(t, wire.ClassDev, cand.Class)
	assert.Equal(t, cand.Snapshot.Tree, ref.Snapshot.Tree, "both halves share one snapshot")
	assert.True(t, slices.Contains(cand.Labels, "host:mserv"), "the candidate half is for this host: %v", cand.Labels)
	assert.False(t, slices.Contains(cand.Labels, "os:darwin"), "and does not ask for the Mac-only labels: %v", cand.Labels)
	assert.True(t, slices.Contains(ref.Labels, "host:air26"), "the reference half is for the reference host: %v", ref.Labels)
	assert.True(t, slices.Contains(ref.Labels, "os:darwin"), "and keeps the task's own labels: %v", ref.Labels)

	// a second request for the same task while the pair runs is refused in the coordinator's words
	again := post(`{"task":"purdex/tsc"}`)
	assert.Equal(t, http.StatusConflict, again.Code)
	assert.Contains(t, again.Body.String(), qualify.ErrInFlight.Error())
	assert.Equal(t, http.StatusNotFound, post(`{"task":"purdex/plain"}`).Code, "a task with no qualify")

	sw := httptest.NewRecorder()
	d.SocketHandler().ServeHTTP(sw, httptest.NewRequest(http.MethodGet, "/v1/qualify", nil))
	var status wire.QualifyStatus
	require.NoError(t, json.Unmarshal(sw.Body.Bytes(), &status))
	require.Len(t, status.InFlight, 1)
	assert.Equal(t, started.Candidate, status.InFlight[0].Candidate)

	// and it is the socket's alone
	tcp := httptest.NewRecorder()
	treq := httptest.NewRequest(http.MethodPost, "/v1/qualify", strings.NewReader(`{"task":"purdex/tsc"}`))
	treq.Header.Set("Authorization", "Bearer tok")
	d.TCPHandler().ServeHTTP(tcp, treq)
	assert.Equal(t, http.StatusNotFound, tcp.Code)
}

// A facts record alone starts the pair: Notify, the trigger's goroutine and the real coordinator, with no request from anyone.
func TestAFactsRecordStartsAPairByItself(t *testing.T) {
	root := qualifyRepo(t, qualifyManifest)
	st, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	require.NoError(t, err)
	defer st.Close()
	require.NoError(t, queue.Migrate(t.Context(), st.DB()))
	q, err := queue.New(st.DB(), queue.Options{Origin: "mserv", LogDir: t.TempDir()})
	require.NoError(t, err)
	coordinator := qualify.New(qualify.Config{
		Queue: q, Store: st,
		Source: &qualifySource{repos: clones.NewSource(map[string]string{"purdex": root}), host: func() qualify.Host {
			return qualify.Host{Alias: "mserv", WorkerOn: true, Labels: []string{"repo:purdex", "tool:node24", "os:linux"}, Fingerprint: map[string]string{"node": "24"}}
		}},
		Granted: func() {},
	})
	trigger := newQualifyTrigger()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go trigger.Run(ctx, coordinator.Tick)

	trigger.Notify()
	require.Eventually(t, func() bool {
		inFlight, _ := coordinator.Status()
		return len(inFlight) > 0
	}, 10*time.Second, 20*time.Millisecond, "no pair was started by a facts record")
	inFlight, _ := coordinator.Status()
	labels := map[string]bool{}
	for _, p := range inFlight {
		labels[p.Label] = true
	}
	assert.True(t, labels["task:purdex/tsc"], "%v", labels)
}

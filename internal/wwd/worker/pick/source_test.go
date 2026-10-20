package pick

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

type fakeQueue struct {
	jobs     []wire.Job
	forPeers bool
}

func (f *fakeQueue) Queued(_ context.Context, forPeers bool) ([]wire.Job, error) {
	f.forPeers = forPeers
	return f.jobs, nil
}

func TestLocalSourceReadsOwnQueueIncludingAdHoc(t *testing.T) {
	q := &fakeQueue{jobs: []wire.Job{job("a", wire.ClassDev, 0)}}
	s := NewLocalSource("mlab", q)
	jobs, err := s.Queued(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].ID != "a" {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	if q.forPeers {
		t.Fatal("a worker reading its own queue must see ad-hoc jobs (spec 5): ForPeers must be false")
	}
	if s.Host() != "mlab" {
		t.Fatalf("host %q", s.Host())
	}
}

func TestPeerSourceSendsTokenAndQuery(t *testing.T) {
	var gotAuth, gotPath, gotState string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotState = r.Header.Get("Authorization"), r.URL.Path, r.URL.Query().Get("state")
		_ = json.NewEncoder(w).Encode(wire.JobsListResponse{Jobs: []wire.Job{job("p", wire.ClassMerge, time.Minute)}})
	}))
	defer srv.Close()
	s := NewPeerSource("air26", srv.URL+"/", "tok", srv.Client())
	jobs, err := s.Queued(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].ID != "p" {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	if gotAuth != "Bearer tok" || gotPath != "/v1/jobs" || gotState != "queued" {
		t.Fatalf("auth=%q path=%q state=%q", gotAuth, gotPath, gotState)
	}
}

func TestPeerSourceErrors(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(wire.JobsListResponse{Jobs: []wire.Job{job("leak", wire.ClassDev, 0)}}) // a well-formed body must still not be trusted
		}))
		s := NewPeerSource("x", srv.URL, "tok", srv.Client())
		if _, err := s.Queued(context.Background()); err == nil || !strings.Contains(err.Error(), "x") {
			t.Fatalf("code %d: err = %v, want an error naming the peer", code, err)
		}
		srv.Close()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) }))
	defer srv.Close()
	if _, err := NewPeerSource("x", srv.URL, "tok", srv.Client()).Queued(context.Background()); err == nil {
		t.Fatal("garbage answer must be an error")
	}
}

func TestPeerSourceDropsJobsNotQueuedOrNotItsOwn(t *testing.T) {
	// a peer must never hand us an ad-hoc or local job (spec 4.4); if one slips through, it is dropped, not scored.
	adhoc := job("adhoc", wire.ClassDev, 0)
	adhoc.AdHoc = true
	local := job("local", wire.ClassDev, 0)
	local.Local = true
	running := job("running", wire.ClassDev, 0)
	running.State = wire.StateRunning
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(wire.JobsListResponse{Jobs: []wire.Job{adhoc, local, running, job("ok", wire.ClassDev, 0)}})
	}))
	defer srv.Close()
	jobs, err := NewPeerSource("p", srv.URL, "t", srv.Client()).Queued(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].ID != "ok" {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
}

type fixedSource struct {
	host string
	jobs []wire.Job
	err  error
}

func (f fixedSource) Host() string { return f.host }
func (f fixedSource) Queued(context.Context) ([]wire.Job, error) {
	return f.jobs, f.err
}

func TestCollectSkipsFailingSourceAndReportsIt(t *testing.T) {
	zones := map[string]wire.Zone{"b": wire.ZoneDanger}
	cands, errs := Collect(context.Background(), []Source{
		fixedSource{host: "a", err: context.DeadlineExceeded},
		fixedSource{host: "b", jobs: []wire.Job{job("x", wire.ClassDev, 0)}},
	}, func(h string) wire.Zone { return zones[h] })
	if len(cands) != 1 || cands[0].Source != "b" || cands[0].Zone != wire.ZoneDanger {
		t.Fatalf("cands=%v", cands)
	}
	if len(errs) != 1 || errs["a"] == nil {
		t.Fatalf("errs=%v", errs)
	}
}

func TestPeerSourceTimesOutOnSilentPeer(t *testing.T) {
	old := peerTimeout
	peerTimeout = 100 * time.Millisecond
	defer func() { peerTimeout = old }()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	start := time.Now()
	if _, err := NewPeerSource("slow", srv.URL, "t", nil).Queued(context.Background()); err == nil {
		t.Fatal("a peer that never answers must be an error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

// A peer's queue is read for what the contract says — framing, types, values — but a field of a later build is skipped (#3710).
func TestPeerSourceToleratesAFieldOfALaterBuild(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"jobs":[],"surprise":1}`)) }))
	defer srv.Close()
	if _, err := NewPeerSource("p", srv.URL, "t", srv.Client()).Queued(context.Background()); err != nil {
		t.Fatalf("an unknown field: %v", err)
	}
}

func TestPeerSourceDecodesWhatTheContractSays(t *testing.T) {
	for name, body := range map[string]string{
		"trailing data": `{"jobs":[]} {}`,
		"bad cursor":    `{"jobs":[],"cursor":"x"}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		if _, err := NewPeerSource("p", srv.URL, "t", srv.Client()).Queued(context.Background()); err == nil {
			t.Errorf("%s: want an error", name)
		}
		srv.Close()
	}
}

func TestPeerSourceRefusesOversizedAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// valid JSON that is only too long, so that a size limit is the one thing that can refuse it
		_, _ = w.Write([]byte(`{"jobs":[]}`))
		_, _ = w.Write(bytes.Repeat([]byte(" "), maxAnswer))
	}))
	defer srv.Close()
	if _, err := NewPeerSource("p", srv.URL, "t", srv.Client()).Queued(context.Background()); err == nil {
		t.Fatal("want an error")
	}
}

type slowSource struct {
	host string
	wait time.Duration
}

func (s slowSource) Host() string { return s.host }
func (s slowSource) Queued(ctx context.Context) ([]wire.Job, error) {
	select {
	case <-time.After(s.wait):
		return nil, context.DeadlineExceeded
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestCollectReadsInParallelAndKeepsOrder(t *testing.T) {
	srcs := []Source{
		slowSource{"s1", 300 * time.Millisecond}, slowSource{"s2", 300 * time.Millisecond}, slowSource{"s3", 300 * time.Millisecond},
		fixedSource{host: "a", jobs: []wire.Job{job("x", wire.ClassDev, 0)}},
		fixedSource{host: "b", jobs: []wire.Job{job("y", wire.ClassDev, 0)}},
	}
	start := time.Now()
	cands, errs := Collect(context.Background(), srcs, nil)
	if d := time.Since(start); d > 800*time.Millisecond {
		t.Fatalf("three silent sources took %v: they were read one after another", d)
	}
	if len(errs) != 3 || len(cands) != 2 || cands[0].Source != "a" || cands[1].Source != "b" {
		t.Fatalf("cands=%v errs=%v", cands, errs)
	}
}

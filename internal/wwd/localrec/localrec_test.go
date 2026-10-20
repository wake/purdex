package localrec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/fastlane"
	"github.com/wake/purdex/internal/wwd/wire"
)

const claim = "0123456789abcdef0123456789abcdef"

type fakeAPI struct {
	regErr     error
	resp       wire.LocalRunResponse
	registered []wire.LocalRunRequest
	hbErr      error
	cancel     bool
	logErr     error
	logs       [][]byte
	resultErr  error
	results    []wire.ResultRequest
	order      []string
}

func (f *fakeAPI) RegisterLocal(_ context.Context, r wire.LocalRunRequest) (wire.LocalRunResponse, error) {
	f.registered = append(f.registered, r)
	return f.resp, f.regErr
}
func (f *fakeAPI) Heartbeat(context.Context, string, string) (wire.HeartbeatResponse, error) {
	return wire.HeartbeatResponse{LeaseS: 60, RenewS: 15, Cancel: f.cancel}, f.hbErr
}
func (f *fakeAPI) PostResult(_ context.Context, id string, r wire.ResultRequest) error {
	f.order = append(f.order, "result")
	f.results = append(f.results, r)
	return f.resultErr
}
func (f *fakeAPI) PutLog(_ context.Context, id, claimID string, log []byte) error {
	f.order = append(f.order, "log")
	f.logs = append(f.logs, log)
	return f.logErr
}

func newAPI() *fakeAPI {
	return &fakeAPI{resp: wire.LocalRunResponse{ID: "j_local1", ClaimID: claim, LeaseS: 60, RenewS: 15}}
}

func body() wire.JobSubmission {
	return wire.JobSubmission{
		Repo: wire.Repo{Name: "r", Root: "abc"}, Snapshot: wire.Snapshot{Commit: "c", Tree: "t", Base: "b", RelCwd: "."},
		Task: "spa-file", Args: []string{}, Class: wire.ClassDev, Labels: []string{}, Locks: []string{}, TimeoutS: 60,
		GitDir: "/r/.git",
	}
}

func TestRegisterSendsTheRunAndReturnsTheClaim(t *testing.T) {
	api := newAPI()
	h, err := Recorder{API: api}.Register(context.Background(), body())
	if err != nil {
		t.Fatal(err)
	}
	if h.JobID() != "j_local1" || h.Lease() != 60*time.Second || h.RenewEvery() != 15*time.Second {
		t.Fatalf("handle = %s %v %v", h.JobID(), h.Lease(), h.RenewEvery())
	}
	if len(api.registered) != 1 || api.registered[0].GitDir != "/r/.git" || api.registered[0].Task != "spa-file" {
		t.Fatalf("registered = %+v", api.registered)
	}
}

func TestRegisterRefusesWhatWwdWouldRefuse(t *testing.T) {
	api := newAPI()
	b := body()
	b.Cacheable = true // a fast-lane run is never cacheable (wire)
	if _, err := (Recorder{API: api}).Register(context.Background(), b); err == nil || len(api.registered) != 0 {
		t.Fatalf("a cacheable local run was sent: %v", err)
	}
}

func TestNoSlotBecomesErrSlotBusy(t *testing.T) {
	api := newAPI()
	api.regErr = &client.SlotBusyError{Slot: "light", Reasons: []string{"zone_danger"}}
	_, err := (Recorder{API: api}).Register(context.Background(), body())
	if !errors.Is(err, fastlane.ErrSlotBusy) || errors.Is(err, fastlane.ErrLocksBusy) || !strings.Contains(err.Error(), "zone_danger") {
		t.Fatalf("err = %v", err)
	}
}

func TestBusyLocksBecomeErrLocksBusy(t *testing.T) {
	api := newAPI()
	api.regErr = &client.LocksBusyError{Keys: []string{"db"}, Holders: []wire.LockHolder{{Key: "db", Job: "j_07"}}}
	_, err := (Recorder{API: api}).Register(context.Background(), body())
	if err != nil && !strings.Contains(err.Error(), "db (held by j_07)") {
		t.Fatalf("the wait names the job that holds the key: %v", err)
	}
	if _, err := (Recorder{API: api}).Register(context.Background(), body()); !errors.Is(err, fastlane.ErrLocksBusy) {
		t.Fatalf("err = %v", err)
	}
	api.regErr = errors.New("wwd down")
	if _, err := (Recorder{API: api}).Register(context.Background(), body()); err == nil || errors.Is(err, fastlane.ErrLocksBusy) {
		t.Fatalf("an unreachable wwd is not a busy lock: %v", err)
	}
}

func TestHeartbeat(t *testing.T) {
	api := newAPI()
	h, _ := Recorder{API: api}.Register(context.Background(), body())
	if cancel, err := h.Heartbeat(context.Background()); err != nil || cancel {
		t.Fatalf("cancel %v err %v", cancel, err)
	}
	api.cancel = true
	if cancel, err := h.Heartbeat(context.Background()); err != nil || !cancel {
		t.Fatalf("cancel %v err %v", cancel, err)
	}
	api.hbErr = client.ErrClaimLost
	if _, err := h.Heartbeat(context.Background()); !errors.Is(err, fastlane.ErrStale) {
		t.Fatalf("a lost claim must be ErrStale: %v", err)
	}
	api.hbErr = errors.New("timeout")
	if _, err := h.Heartbeat(context.Background()); err == nil || errors.Is(err, fastlane.ErrStale) {
		t.Fatalf("a transient error is not a lost claim: %v", err)
	}
}

func TestFinishUploadsTheLogThenPostsTheResult(t *testing.T) {
	api := newAPI()
	h, _ := Recorder{API: api}.Register(context.Background(), body())
	logPath := filepath.Join(t.TempDir(), "log")
	os.WriteFile(logPath, []byte("hello log\n"), 0o600)
	zero := 0
	now := time.Now()
	err := h.Finish(context.Background(), fastlane.Outcome{
		State: wire.StateSucceeded, Exit: &zero, Summary: wire.Summary{Passed: 2, Failures: []wire.Failure{}, Tail: []string{}},
		LogPath: logPath, StartedAt: now.Add(-time.Second), FinishedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(api.order, ",") != "log,result" || string(api.logs[0]) != "hello log\n" {
		t.Fatalf("order %v logs %q", api.order, api.logs)
	}
	r := api.results[0]
	if r.ClaimID != claim || r.State != wire.StateSucceeded || r.Exit == nil || *r.Exit != 0 || r.Summary.Passed != 2 || r.StartedAt == nil || r.FinishedAt == nil {
		t.Fatalf("result = %+v", r)
	}
}

// The result carries the reason of a failed run when it is one a worker may post (oom, timeout, infra); the origin's own words and
// every reason of a run that did not fail stay off it (the route refuses them).
func TestFinishPostsOnlyTheReasonsAWorkerMay(t *testing.T) {
	for name, tc := range map[string]struct {
		state  wire.JobState
		reason string
		want   string
	}{
		"timeout":                  {wire.StateFailed, "timeout", "timeout"},
		"infra":                    {wire.StateFailed, "infra", "infra"},
		"oom":                      {wire.StateFailed, "oom", "oom"},
		"lost is the origin's":     {wire.StateFailed, "lost", ""},
		"queue_timeout":            {wire.StateFailed, "queue_timeout", ""},
		"expired turned to failed": {wire.StateExpired, "queue_timeout", ""},
		"no reason":                {wire.StateFailed, "", ""},
		"a success has none":       {wire.StateSucceeded, "timeout", ""},
		"cancelled has none":       {wire.StateCancelled, "infra", ""},
	} {
		api := newAPI()
		h, _ := Recorder{API: api}.Register(context.Background(), body())
		if err := h.Finish(context.Background(), fastlane.Outcome{State: tc.state, Reason: tc.reason}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(api.results) != 1 || api.results[0].Reason != tc.want {
			t.Errorf("%s: posted reason %q, want %q", name, api.results[0].Reason, tc.want)
		}
		if err := api.results[0].Validate(); err != nil {
			t.Errorf("%s: the route would refuse it: %v", name, err)
		}
	}
}

func TestFinishMapsAStateTheRouteDoesNotTake(t *testing.T) {
	api := newAPI()
	h, _ := Recorder{API: api}.Register(context.Background(), body())
	logPath := filepath.Join(t.TempDir(), "log")
	os.WriteFile(logPath, []byte("x"), 0o600)
	if err := h.Finish(context.Background(), fastlane.Outcome{State: wire.StateExpired, LogPath: logPath}); err != nil {
		t.Fatal(err)
	}
	if len(api.results) != 1 || api.results[0].State != wire.StateFailed || api.results[0].Exit != nil {
		t.Fatalf("results = %+v", api.results)
	}
}

func TestAnUnstoredLogIsAnErrorAndNoResultGoesBeforeIt(t *testing.T) {
	api := newAPI()
	api.logErr = errors.New("timeout")
	h, _ := Recorder{API: api}.Register(context.Background(), body())
	logPath := filepath.Join(t.TempDir(), "log")
	os.WriteFile(logPath, []byte("x"), 0o600)
	if err := h.Finish(context.Background(), fastlane.Outcome{State: wire.StateFailed, LogPath: logPath}); err == nil || len(api.results) != 0 {
		t.Fatalf("err %v results %d", err, len(api.results))
	}
	api.logErr = nil // the retry
	if err := h.Finish(context.Background(), fastlane.Outcome{State: wire.StateFailed, LogPath: logPath}); err != nil || len(api.results) != 1 {
		t.Fatalf("err %v results %d", err, len(api.results))
	}
}

func TestFinishWithoutALogAndWithALostClaim(t *testing.T) {
	api := newAPI()
	h, _ := Recorder{API: api}.Register(context.Background(), body())
	if err := h.Finish(context.Background(), fastlane.Outcome{State: wire.StateCancelled}); err != nil || len(api.logs) != 0 {
		t.Fatalf("err %v logs %d", err, len(api.logs))
	}
	api.resultErr = client.ErrClaimLost
	if err := h.Finish(context.Background(), fastlane.Outcome{State: wire.StateFailed}); !errors.Is(err, fastlane.ErrStale) {
		t.Fatalf("a lost claim at the result: %v", err)
	}
	api.resultErr = nil
	api.logErr = client.ErrClaimLost
	os.WriteFile(filepath.Join(t.TempDir(), "x"), nil, 0o600)
	logPath := filepath.Join(t.TempDir(), "log")
	os.WriteFile(logPath, []byte("x"), 0o600)
	api.results = nil
	if err := h.Finish(context.Background(), fastlane.Outcome{State: wire.StateFailed, LogPath: logPath}); !errors.Is(err, fastlane.ErrStale) || len(api.results) != 0 {
		t.Fatalf("a lost claim at the log must stop before the result: err %v results %d", err, len(api.results))
	}
}

func TestReadLogCutsToTheEnd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	big := strings.Repeat("a", maxLog) + "THE END"
	os.WriteFile(p, []byte(big), 0o600)
	b, err := readLog(p)
	if err != nil || len(b) != maxLog || !strings.HasSuffix(string(b), "THE END") {
		t.Fatalf("len %d, ends %q", len(b), string(b[len(b)-7:]))
	}
	if b, err := readLog(""); b != nil || err != nil {
		t.Fatal("no path is no log")
	}
	if b, err := readLog(filepath.Join(t.TempDir(), "none")); b != nil || err != nil {
		t.Fatal("a missing file is no log")
	}
	if _, err := readLog(t.TempDir()); err == nil { // a directory exists and cannot be read
		t.Fatal("an unreadable log must be an error")
	}
	empty := filepath.Join(t.TempDir(), "e")
	os.WriteFile(empty, nil, 0o600)
	if b, _ := readLog(empty); b != nil {
		t.Fatal("an empty log is not uploaded")
	}
}

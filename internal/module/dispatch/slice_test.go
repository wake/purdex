package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/bridge"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/execution"
	"github.com/wake/purdex/internal/module/stream"
	"github.com/wake/purdex/internal/tmux"
)

// ---- fakes for the vertical slice ---------------------------------------------

// sliceAdmitter stands in for *execution.Admitter: it runs fn with a canned
// admission (happy path) or short-circuits with admitErr (rejection), satisfying
// the Coordinator's unexported repoAdmitter port by method set.
type sliceAdmitter struct {
	adm      execution.Admission
	admitErr error
	calls    int
}

func (a *sliceAdmitter) WithRepoLock(_ context.Context, _ string, fn func(*execution.Admission) error) error {
	a.calls++
	if a.admitErr != nil {
		return a.admitErr
	}
	return fn(&a.adm)
}

// sliceLauncher stands in for the real tmux/relay launcher.
type sliceLauncher struct {
	result  execution.LaunchResult
	err     error
	gotSpec execution.LaunchSpec
}

func (l *sliceLauncher) Launch(_ context.Context, spec execution.LaunchSpec) (execution.LaunchResult, error) {
	l.gotSpec = spec
	if l.err != nil {
		return execution.LaunchResult{}, l.err
	}
	return l.result, nil
}

// slicePoster is the fake Ploom report endpoint: it records every posted report
// and acks it immediately (ack_seq = the report's own seq), so accepted(1) is
// acked and the ordering gate lets running/failed(2) through.
type slicePoster struct {
	mu      sync.Mutex
	reports []postedReport
}

type postedReport struct {
	dispatchID string
	status     string
	seq        int
	errCode    string
}

func (p *slicePoster) Report(_ context.Context, dispatchID string, payload []byte) (ReportResult, error) {
	var m struct {
		Status string `json:"status"`
		Seq    int    `json:"seq"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &m); err != nil {
		return ReportResult{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reports = append(p.reports, postedReport{dispatchID, m.Status, m.Seq, m.Error.Code})
	return ReportResult{AckSeq: m.Seq}, nil
}

func (p *slicePoster) sorted() []postedReport {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := append([]postedReport(nil), p.reports...)
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// sliceFixture wires the full P.7 consume→launch→report slice with fakes at the
// Ploom (poll/claim/fetch + report) and launcher edges, but the real store,
// outbox, sender, launchReporter, and Coordinator in between.
type sliceFixture struct {
	worker *Worker
	store  *execution.ExecutionStore
	outbox *execution.Outbox
	sender *Sender
	poster *slicePoster
	client *fakeClient
	adm    *sliceAdmitter
	launch *sliceLauncher
}

func newSliceFixture(t *testing.T, fc *fakeClient, adm *sliceAdmitter, fl *sliceLauncher) *sliceFixture {
	t.Helper()
	// Host policy = danger-full so the slice exercises the full consume→launch→report
	// path without the sandbox clamp narrowing the requested profiles.
	return newSliceFixtureWithPolicy(t, fc, adm, fl, execution.ProfileDangerFull)
}

func newSliceFixtureWithPolicy(t *testing.T, fc *fakeClient, adm *sliceAdmitter, fl *sliceLauncher, hostPolicy execution.Profile) *sliceFixture {
	t.Helper()
	store, err := execution.OpenExecution(filepath.Join(t.TempDir(), "exec.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	// The outbox lives in the SAME database as the execution rows, so the durable
	// cut's transitions and their reports commit atomically.
	outbox := store.Outbox()

	poster := &slicePoster{}
	sender := NewSender(outbox, poster, WithExecutionReader(executionStoreReader{store}))
	coord := execution.NewCoordinator(adm, store, launchReporter{}, fl, hostPolicy)

	m := &DispatchModule{sender: sender}
	worker := NewWorker(fc, WithSink(m.consumeSink(coord)))

	return &sliceFixture{worker, store, outbox, sender, poster, fc, adm, fl}
}

// ---- happy path: pending → claim → fetch → launch → running -------------------

func TestSlice_HappyPath_AcceptedToRunning(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_a1", IssueID: "iss_1"}},
		fetchFn: func(id string) (DispatchDetail, error) {
			return DispatchDetail{
				DispatchID:     id,
				Issue:          Issue{IssueID: "iss_1", Title: "Fix the bug", Body: "steps to repro"},
				RepoLocation:   RepoLocation{ProjectID: "prj_1", LocalDir: "/canon/repo", IsOrigin: true},
				SandboxProfile: "workspace-write",
			}, nil
		},
	}
	adm := &sliceAdmitter{adm: execution.Admission{CanonicalPath: "/canon/repo", HeadAtStart: "head01", DirtyAtStart: false}}
	fl := &sliceLauncher{result: execution.LaunchResult{SessionCode: "code42"}}
	f := newSliceFixture(t, fc, adm, fl)

	require.NoError(t, f.worker.RunOnce(context.Background()))

	// The launcher received the composed prompt + canonical cwd.
	require.Equal(t, "Fix the bug\n\nsteps to repro", f.launch.gotSpec.Prompt)
	require.Equal(t, "/canon/repo", f.launch.gotSpec.Cwd)

	// Exactly one execution row, transitioned to running/launched with session_code.
	ids, err := f.outbox.DueExecutions(1 << 60)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	execID := ids[0]

	row, ok, err := f.store.GetByID(execID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, execution.StatusRunning, row.Status)
	require.Equal(t, execution.LaunchLaunched, row.LaunchState)
	require.Equal(t, "code42", row.SessionCode.String)
	require.Equal(t, "dsp_a1", row.DispatchID)

	// Outbox holds accepted(1) then running(2), in order.
	recs, err := f.outbox.UnackedRecords(execID)
	require.NoError(t, err)
	require.Len(t, recs, 2)
	require.Equal(t, "accepted", recs[0].Status)
	require.Equal(t, 1, recs[0].Seq)
	require.Equal(t, "running", recs[1].Status)
	require.Equal(t, 2, recs[1].Seq)

	// End-to-end: the persisted accepted echoes the FULL repo_location object
	// (project_id/is_origin), sourced from the row that survived the durable cut.
	var acc struct {
		RepoLocation struct {
			ProjectID string `json:"project_id"`
			LocalDir  string `json:"local_dir"`
			IsOrigin  bool   `json:"is_origin"`
		} `json:"repo_location"`
	}
	require.NoError(t, json.Unmarshal(recs[0].Payload, &acc))
	require.Equal(t, "prj_1", acc.RepoLocation.ProjectID)
	require.Equal(t, "/canon/repo", acc.RepoLocation.LocalDir)
	require.True(t, acc.RepoLocation.IsOrigin)

	// Draining the sender delivers accepted then running to fake Ploom.
	require.NoError(t, f.sender.Flush(context.Background()))
	got := f.poster.sorted()
	require.Len(t, got, 2)
	require.Equal(t, postedReport{"dsp_a1", "accepted", 1, ""}, got[0])
	require.Equal(t, postedReport{"dsp_a1", "running", 2, ""}, got[1])
}

// ---- admission rejection → failed report --------------------------------------

func TestSlice_AdmissionRejected_ReportsFailed(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_busy", IssueID: "iss_2"}},
		fetchFn: func(id string) (DispatchDetail, error) {
			return DispatchDetail{
				DispatchID:     id,
				Issue:          Issue{IssueID: "iss_2", Title: "Second dispatch"},
				RepoLocation:   RepoLocation{LocalDir: "/canon/repo"},
				SandboxProfile: "read-only",
			}, nil
		},
	}
	// Admission rejects: repo already has a live execution.
	adm := &sliceAdmitter{admitErr: fmt.Errorf("%w: /canon/repo", execution.ErrRepoBusy)}
	fl := &sliceLauncher{}
	f := newSliceFixture(t, fc, adm, fl)

	require.NoError(t, f.worker.RunOnce(context.Background()))

	// F3: the rejection is backed by a REAL execution row — Ploom's projection and
	// the Purdex runtime SOT agree on the execution_id.
	ids, err := f.outbox.DueExecutions(1 << 60)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	execID := ids[0]
	require.Regexp(t, `^exc_[0-9a-f]{16}$`, execID, "a real store-generated id, not a synthetic one")

	row, ok, err := f.store.GetByID(execID)
	require.NoError(t, err)
	require.True(t, ok, "the rejection must be queryable in the runtime SOT")
	require.Equal(t, execution.StatusFailed, row.Status)
	require.Equal(t, execution.LaunchNone, row.LaunchState)
	require.Equal(t, string(execution.OutcomeRejected), row.OutcomeSource.String)
	require.Equal(t, "dsp_busy", row.DispatchID)
	require.Equal(t, "/canon/repo", row.RepoLocation)

	// A rejected row is terminal, so it neither blocks the repo nor shows up for
	// the reconcile sweep.
	live, err := f.store.ListLive()
	require.NoError(t, err)
	require.Empty(t, live)

	require.NoError(t, f.sender.Flush(context.Background()))
	got := f.poster.sorted()
	require.Len(t, got, 2)
	require.Equal(t, "accepted", got[0].status)
	require.Equal(t, 1, got[0].seq)
	require.Equal(t, "failed", got[1].status)
	require.Equal(t, 2, got[1].seq)
	require.Equal(t, "dsp_busy", got[1].dispatchID)
	require.Equal(t, "repo_busy", got[1].errCode)
}

// F3 — the accepted echoed for a rejection carries the CLAMPED effective profile,
// and is rebuildable from the row after a restart (durability cut §3.3), exactly
// like a launched execution's accepted.
func TestSlice_Rejection_EchoesClampedProfileAndRebuilds(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_clamp", IssueID: "iss_c"}},
		fetchFn: func(id string) (DispatchDetail, error) {
			return DispatchDetail{
				DispatchID:     id,
				Issue:          Issue{IssueID: "iss_c", Title: "Wants the world"},
				RepoLocation:   RepoLocation{ProjectID: "prj_1", LocalDir: "/canon/repo", IsOrigin: true},
				SandboxProfile: "danger-full",
			}, nil
		},
	}
	adm := &sliceAdmitter{admitErr: fmt.Errorf("%w: /canon/repo", execution.ErrRepoBusy)}
	// Host policy only permits `ask` — the echo must never claim danger-full.
	f := newSliceFixtureWithPolicy(t, fc, adm, &sliceLauncher{}, execution.ProfileAsk)

	require.NoError(t, f.worker.RunOnce(context.Background()))

	ids, err := f.outbox.DueExecutions(1 << 60)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	execID := ids[0]

	row, ok, err := f.store.GetByID(execID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "ask", row.SandboxProfile)

	recs, err := f.outbox.UnackedRecords(execID)
	require.NoError(t, err)
	require.Len(t, recs, 2)
	var acc struct {
		EffectiveSandboxProfile string `json:"effective_sandbox_profile"`
		ExecutionID             string `json:"execution_id"`
	}
	require.NoError(t, json.Unmarshal(recs[0].Payload, &acc))
	require.Equal(t, "ask", acc.EffectiveSandboxProfile, "clamped, never the requested danger-full")
	require.Equal(t, execID, acc.ExecutionID)

	// Restart reconstruction: the accepted rebuilt from the durable row alone is
	// byte-identical to the one queued at rejection time.
	loaded, found, err := (executionStoreReader{f.store}).LoadAcceptedRow(execID)
	require.NoError(t, err)
	require.True(t, found)
	rebuilt, err := BuildAcceptedPayload(loaded)
	require.NoError(t, err)
	require.JSONEq(t, string(recs[0].Payload), string(rebuilt))
}

// TestSlice_LaunchFailure_ReportsFailed: a pre-relay launch failure (the launcher
// errors before running is enqueued) must still surface a terminal failed report.
// The row goes terminal, so ListLive-based reconcile can never see it — without
// the Coordinator enqueuing failed(2) here, Ploom would stay wedged at accepted.
// The outbox must replay accepted(1)→failed(2) end-to-end with launch_failed.
func TestSlice_LaunchFailure_ReportsFailed(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_lf", IssueID: "iss_lf"}},
		fetchFn: func(id string) (DispatchDetail, error) {
			return DispatchDetail{
				DispatchID:     id,
				Issue:          Issue{IssueID: "iss_lf", Title: "Launch me"},
				RepoLocation:   RepoLocation{ProjectID: "prj_1", LocalDir: "/canon/repo", IsOrigin: true},
				SandboxProfile: "workspace-write",
			}, nil
		},
	}
	adm := &sliceAdmitter{adm: execution.Admission{CanonicalPath: "/canon/repo", HeadAtStart: "head01"}}
	fl := &sliceLauncher{err: fmt.Errorf("relay never connected")}
	f := newSliceFixture(t, fc, adm, fl)

	require.NoError(t, f.worker.RunOnce(context.Background()))

	// A row was created and driven terminal (failed); it is not live.
	ids, err := f.outbox.DueExecutions(1 << 60)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	execID := ids[0]
	row, ok, err := f.store.GetByID(execID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, execution.StatusFailed, row.Status)

	// Reconcile has nothing to do: ListLive excludes the terminal row.
	live, err := f.store.ListLive()
	require.NoError(t, err)
	require.Empty(t, live)

	// Outbox replays accepted(1) then failed(2) with launch_failed.
	require.NoError(t, f.sender.Flush(context.Background()))
	got := f.poster.sorted()
	require.Len(t, got, 2)
	require.Equal(t, "accepted", got[0].status)
	require.Equal(t, 1, got[0].seq)
	require.Equal(t, "failed", got[1].status)
	require.Equal(t, 2, got[1].seq)
	require.Equal(t, "launch_failed", got[1].errCode)
	require.Equal(t, "dsp_lf", got[1].dispatchID)
}

// TestSlice_UnknownSandboxProfile_ReportsFailed: a dispatch requesting an unknown
// sandbox profile is rejected at Accept (before any lock/row) and projected as
// accepted(1)+failed(2) carrying unknown_sandbox_profile (spec §8.1 / fixture
// sandbox.unknown.json).
func TestSlice_UnknownSandboxProfile_ReportsFailed(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_yolo", IssueID: "iss_3"}},
		fetchFn: func(id string) (DispatchDetail, error) {
			return DispatchDetail{
				DispatchID:     id,
				Issue:          Issue{IssueID: "iss_3", Title: "Third dispatch"},
				RepoLocation:   RepoLocation{LocalDir: "/canon/repo"},
				SandboxProfile: "yolo-mode",
			}, nil
		},
	}
	adm := &sliceAdmitter{adm: execution.Admission{CanonicalPath: "/canon/repo", HeadAtStart: "h"}}
	fl := &sliceLauncher{}
	f := newSliceFixture(t, fc, adm, fl)

	require.NoError(t, f.worker.RunOnce(context.Background()))

	// Admission was never invoked (fail-closed before the lock); nothing launched.
	require.Zero(t, adm.calls)
	require.Equal(t, execution.LaunchSpec{}, fl.gotSpec)

	require.NoError(t, f.sender.Flush(context.Background()))
	got := f.poster.sorted()
	require.Len(t, got, 2)
	require.Equal(t, "accepted", got[0].status)
	require.Equal(t, "failed", got[1].status)
	require.Equal(t, "unknown_sandbox_profile", got[1].errCode)
}

// F3 (boundary) — an Accept failure that leaves NO row behind (a store/enqueue
// error inside the durable cut aborts the insert transaction) used to be only
// logged, leaving Ploom holding a claimed dispatch it never hears about again.
// It must now also produce a backed, reportable rejection.
func TestSlice_AcceptFailsWithoutRow_StillReported(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_norow", IssueID: "iss_n"}},
		fetchFn: func(id string) (DispatchDetail, error) {
			return DispatchDetail{
				DispatchID:     id,
				Issue:          Issue{IssueID: "iss_n", Title: "Never admitted"},
				RepoLocation:   RepoLocation{LocalDir: "/canon/repo"},
				SandboxProfile: "workspace-write",
			}, nil
		},
	}
	// An unclassified admission failure: not one of the known rejection sentinels,
	// and no row was created.
	adm := &sliceAdmitter{admitErr: fmt.Errorf("execution db unavailable")}
	f := newSliceFixture(t, fc, adm, &sliceLauncher{})

	require.NoError(t, f.worker.RunOnce(context.Background()))

	ids, err := f.outbox.DueExecutions(1 << 60)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	row, ok, err := f.store.GetByID(ids[0])
	require.NoError(t, err)
	require.True(t, ok, "the failure must still leave a queryable row")
	require.Equal(t, execution.StatusFailed, row.Status)

	require.NoError(t, f.sender.Flush(context.Background()))
	got := f.poster.sorted()
	require.Len(t, got, 2)
	require.Equal(t, "accepted", got[0].status)
	require.Equal(t, "failed", got[1].status)
	require.Equal(t, "launch_failed", got[1].errCode)
	require.Equal(t, "dsp_norow", got[1].dispatchID)
}

// A launch failure already creates and fails its own row, so the Reject fallback
// must NOT add a second row or a duplicate report pair.
func TestSlice_LaunchFailure_RejectFallbackIsNoOp(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_dup", IssueID: "iss_d"}},
		fetchFn: func(id string) (DispatchDetail, error) {
			return DispatchDetail{
				DispatchID:     id,
				Issue:          Issue{IssueID: "iss_d", Title: "Launch me"},
				RepoLocation:   RepoLocation{LocalDir: "/canon/repo"},
				SandboxProfile: "workspace-write",
			}, nil
		},
	}
	adm := &sliceAdmitter{adm: execution.Admission{CanonicalPath: "/canon/repo", HeadAtStart: "h"}}
	f := newSliceFixture(t, fc, adm, &sliceLauncher{err: fmt.Errorf("relay never connected")})

	require.NoError(t, f.worker.RunOnce(context.Background()))

	ids, err := f.outbox.DueExecutions(1 << 60)
	require.NoError(t, err)
	require.Len(t, ids, 1, "exactly one execution for the dispatch")
	recs, err := f.outbox.UnackedRecords(ids[0])
	require.NoError(t, err)
	require.Len(t, recs, 2, "accepted(1)+failed(2) only — no duplicate pair")
}

// ---- module wiring ------------------------------------------------------------

// newWiringCore builds a Core with the registry pre-populated the way the real
// daemon does: execution store + stream relay gateway. PDX_PLOOM_URL is set so
// the consumer is enabled.
func newWiringCore(t *testing.T, withGateway bool) *core.Core {
	t.Helper()
	t.Setenv(envPloomURL, "https://ploom.test")
	t.Setenv(envPloomToken, "tok")

	dir := t.TempDir()
	store, err := execution.OpenExecution(filepath.Join(dir, "exec.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	reg := core.NewServiceRegistry()
	reg.Register(execution.RegistryKey, store)
	reg.Register(stream.TerminalSeamKey, &fakeSeam{})
	if withGateway {
		reg.Register(stream.RelayGatewayKey, bridge.New())
	}

	c := core.New(core.CoreDeps{
		Config:   &config.Config{DataDir: dir, Bind: "127.0.0.1", Port: 7860, Token: "tok"},
		Tmux:     tmux.NewFakeExecutor(),
		Registry: reg,
	})
	return c
}

func TestModule_Init_BuildsLaunchSink(t *testing.T) {
	c := newWiringCore(t, true)
	m := New()
	require.NoError(t, m.Init(c))
	require.NotNil(t, m.worker, "worker must be built when Ploom URL is set")
	require.NotNil(t, m.sender)
	require.NotNil(t, m.sink)
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
}

// F2: a missing relay gateway can no longer degrade into claim-and-drop — the
// consumer is disabled entirely and the worker is never built, while the rest of
// the daemon still boots (see dependency_test.go).
func TestModule_Init_GatewayMissing_FailsClosed(t *testing.T) {
	c := newWiringCore(t, false) // no relay gateway registered
	m := New()
	require.NoError(t, m.Init(c), "a dispatch-scoped fault must not abort daemon boot")
	require.Nil(t, m.worker, "consumer must not poll or claim without a launch path")
	require.Nil(t, m.client, "no Ploom client when the consumer is disabled")
}

func TestModule_Init_NoPloomURL_Disabled(t *testing.T) {
	t.Setenv(envPloomURL, "")
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	m := New()
	require.NoError(t, m.Init(c))
	require.Nil(t, m.worker, "consumer disabled when no Ploom URL")
}

func TestModule_Dependencies_OrderAfterStreamAndExecution(t *testing.T) {
	deps := New().Dependencies()
	require.Contains(t, deps, "execution")
	require.Contains(t, deps, "stream")
}

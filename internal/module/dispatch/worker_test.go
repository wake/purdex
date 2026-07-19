package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClient is a scripted PloomClient recording call order for assertions.
type fakeClient struct {
	mu sync.Mutex

	pending    []PendingDispatch
	pollErr    error
	claimFn    func(id string) (ClaimResult, error)
	fetchFn    func(id string) (DispatchDetail, error)
	callLog    []string
	pollCalls  int
	claimCalls int
	fetchCalls int
}

func (f *fakeClient) PollPending(context.Context) ([]PendingDispatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pollCalls++
	f.callLog = append(f.callLog, "poll")
	if f.pollErr != nil {
		return nil, f.pollErr
	}
	return f.pending, nil
}

func (f *fakeClient) Claim(_ context.Context, id string) (ClaimResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimCalls++
	f.callLog = append(f.callLog, "claim:"+id)
	if f.claimFn != nil {
		return f.claimFn(id)
	}
	return ClaimResult{DispatchID: id, Status: "claimed"}, nil
}

func (f *fakeClient) Fetch(_ context.Context, id string) (DispatchDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchCalls++
	f.callLog = append(f.callLog, "fetch:"+id)
	if f.fetchFn != nil {
		return f.fetchFn(id)
	}
	return DispatchDetail{DispatchID: id}, nil
}

// collectSink records every ClaimedDispatch handed to the worker.
func collectSink() (FetchSink, *[]ClaimedDispatch, *sync.Mutex) {
	var mu sync.Mutex
	var got []ClaimedDispatch
	sink := func(_ context.Context, c ClaimedDispatch) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, c)
	}
	return sink, &got, &mu
}

func TestWorker_PollClaimFetch_HappyPath(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{
			{DispatchID: "dsp_a1", IssueID: "iss_42"},
			{DispatchID: "dsp_a2", IssueID: "iss_43"},
		},
		fetchFn: func(id string) (DispatchDetail, error) {
			return DispatchDetail{
				DispatchID:     id,
				Issue:          Issue{IssueID: "iss_for_" + id},
				SandboxProfile: "workspace-write",
			}, nil
		},
	}
	sink, got, mu := collectSink()
	w := NewWorker(fc, WithSink(sink))

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 2 {
		t.Fatalf("sink got %d, want 2", len(*got))
	}
	if fc.claimCalls != 2 || fc.fetchCalls != 2 {
		t.Errorf("claims=%d fetches=%d, want 2/2", fc.claimCalls, fc.fetchCalls)
	}
	// Claim must precede Fetch for each dispatch.
	assertOrder(t, fc.callLog, "claim:dsp_a1", "fetch:dsp_a1")
	assertOrder(t, fc.callLog, "claim:dsp_a2", "fetch:dsp_a2")
	// The fetched detail rides through to the sink.
	if (*got)[0].Detail.Issue.IssueID != "iss_for_dsp_a1" {
		t.Errorf("sink detail not propagated: %+v", (*got)[0].Detail)
	}
	if (*got)[0].Pending.IssueID != "iss_42" {
		t.Errorf("sink pending not propagated: %+v", (*got)[0].Pending)
	}
}

func TestWorker_SkipAlreadyClaimed(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{
			{DispatchID: "dsp_taken"},
			{DispatchID: "dsp_mine"},
		},
		claimFn: func(id string) (ClaimResult, error) {
			if id == "dsp_taken" {
				return ClaimResult{}, fmt.Errorf("%w: lost race", ErrAlreadyClaimed)
			}
			return ClaimResult{DispatchID: id, Status: "claimed"}, nil
		},
	}
	sink, got, mu := collectSink()
	w := NewWorker(fc, WithSink(sink))

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("already_claimed is a benign skip, RunOnce returned %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 1 || (*got)[0].Pending.DispatchID != "dsp_mine" {
		t.Fatalf("sink should only get dsp_mine, got %+v", *got)
	}
	// The skipped dispatch must not be fetched.
	if fc.fetchCalls != 1 {
		t.Errorf("fetchCalls = %d, want 1 (taken dispatch not fetched)", fc.fetchCalls)
	}
}

func TestWorker_DuplicateSameDaemon_Idempotent(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_a1"}},
		claimFn: func(id string) (ClaimResult, error) {
			return ClaimResult{DispatchID: id, Status: "claimed", ExecutionID: "exc_9"}, nil
		},
	}
	sink, got, mu := collectSink()
	w := NewWorker(fc, WithSink(sink))

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 1 {
		t.Fatalf("sink got %d, want 1", len(*got))
	}
	if (*got)[0].Claim.ExecutionID != "exc_9" {
		t.Errorf("idempotent execution_id not carried to sink: %+v", (*got)[0].Claim)
	}
}

func TestWorker_SkipDispatchNotFound(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_gone"}},
		claimFn: func(string) (ClaimResult, error) {
			return ClaimResult{}, fmt.Errorf("%w", ErrDispatchNotFound)
		},
	}
	sink, got, mu := collectSink()
	w := NewWorker(fc, WithSink(sink))

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("dispatch_not_found is a benign skip, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 0 {
		t.Fatalf("sink should be empty, got %+v", *got)
	}
	if fc.fetchCalls != 0 {
		t.Errorf("fetchCalls = %d, want 0", fc.fetchCalls)
	}
}

func TestWorker_SchemaIncompatible_Propagates(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_a1"}},
		claimFn: func(string) (ClaimResult, error) {
			return ClaimResult{}, fmt.Errorf("%w: v2", ErrSchemaIncompatible)
		},
	}
	sink, got, mu := collectSink()
	w := NewWorker(fc, WithSink(sink))

	err := w.RunOnce(context.Background())
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Fatalf("schema_incompatible must surface, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 0 {
		t.Errorf("sink must not fire on schema_incompatible: %+v", *got)
	}
}

func TestWorker_FetchError_Propagates(t *testing.T) {
	fetchErr := errors.New("boom")
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_a1"}},
		fetchFn: func(string) (DispatchDetail, error) {
			return DispatchDetail{}, fetchErr
		},
	}
	sink, got, mu := collectSink()
	w := NewWorker(fc, WithSink(sink))

	err := w.RunOnce(context.Background())
	if !errors.Is(err, fetchErr) {
		t.Fatalf("fetch error must surface, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 0 {
		t.Errorf("sink must not fire when fetch fails: %+v", *got)
	}
}

func TestWorker_PollError_Propagates(t *testing.T) {
	pollErr := errors.New("network down")
	fc := &fakeClient{pollErr: pollErr}
	w := NewWorker(fc)

	if err := w.RunOnce(context.Background()); !errors.Is(err, pollErr) {
		t.Fatalf("poll error must surface, got %v", err)
	}
	if fc.claimCalls != 0 {
		t.Errorf("no claim should happen after poll failure, got %d", fc.claimCalls)
	}
}

func TestWorker_Run_StopsOnContextCancel(t *testing.T) {
	fc := &fakeClient{pending: nil}
	w := NewWorker(fc, WithInterval(time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	// Let it run a few cycles, then cancel.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancel")
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.pollCalls < 1 {
		t.Errorf("Run should have polled at least once, got %d", fc.pollCalls)
	}
}

// assertOrder checks that a appears before b in log.
func assertOrder(t *testing.T, log []string, a, b string) {
	t.Helper()
	ai, bi := indexOf(log, a), indexOf(log, b)
	if ai < 0 || bi < 0 {
		t.Fatalf("missing entries %q(%d)/%q(%d) in %v", a, ai, b, bi, log)
	}
	if ai > bi {
		t.Errorf("expected %q before %q in %v", a, b, log)
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

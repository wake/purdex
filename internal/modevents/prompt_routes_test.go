package modevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// U3-0b: the mod socket's prompt routes and the registry's owner choice.

type fakePrompt struct {
	job     any
	gotNext []string
	results []PromptResult
	resErr  error
}

func (f *fakePrompt) NextPrompt(_ context.Context, stream, sid string, _ time.Duration) (any, bool) {
	f.gotNext = append(f.gotNext, stream+"/"+sid)
	return f.job, f.job != nil
}

func (f *fakePrompt) PromptResult(_ string, r PromptResult) error {
	f.results = append(f.results, r)
	return f.resErr
}

// promptReg: testStream announced prompt.v1, "Zz9_-other1" workbook.v2 only; both on testSID.
func promptReg() *Registry {
	reg := NewRegistry(time.Now)
	_, _ = reg.Apply(Batch{V: 1, Stream: testStream, Agent: "cc", Caps: []string{CapPromptV1},
		Events: []Event{{Seq: 1, SID: testSID, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	_, _ = reg.Apply(Batch{V: 1, Stream: "Zz9_-other1", Agent: "cc", Caps: []string{CapWorkbookV2},
		Events: []Event{{Seq: 1, SID: testSID, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	return reg
}

func promptHandler(f *fakePrompt) http.Handler {
	return NewHandler(promptReg(), WithPrompt(func() PromptService {
		if f == nil {
			return nil
		}
		return f
	}))
}

func TestPromptNext_HandsTheJobToAPromptCapableStream(t *testing.T) {
	f := &fakePrompt{job: map[string]string{"id": "pj-1", "kind": "submit"}}
	rec := post(t, promptHandler(f), http.MethodPost, PromptNextPath, nextBody(testStream, testSID, 0))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"pj-1"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(f.gotNext) != 1 || f.gotNext[0] != testStream+"/"+testSID {
		t.Fatalf("asked = %v", f.gotNext)
	}
	empty := &fakePrompt{}
	if rec := post(t, promptHandler(empty), http.MethodPost, PromptNextPath, nextBody(testStream, testSID, 0)); rec.Code != http.StatusNoContent {
		t.Fatalf("no job: %d", rec.Code)
	}
}

// A stream that did not announce prompt.v1 (a workbook-only mod) or is not the session's never reaches the queue: 204.
// Mutation gate: skip the capability check → red.
func TestPromptNext_OnlyAPromptCapableStreamOfTheSession(t *testing.T) {
	f := &fakePrompt{job: map[string]string{"id": "pj-1"}}
	h := promptHandler(f)
	for name, body := range map[string]string{
		"v2 only":       nextBody("Zz9_-other1", testSID, 0),
		"other session": nextBody(testStream, "99999999-aaaa-bbbb-cccc-dddddddddddd", 0),
		"unknown":       nextBody("Zz9_-unknown", testSID, 0),
	} {
		if rec := post(t, h, http.MethodPost, PromptNextPath, body); rec.Code != http.StatusNoContent {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if len(f.gotNext) != 0 {
		t.Fatalf("the queue was asked: %v", f.gotNext)
	}
}

func TestPromptNext_Validation(t *testing.T) {
	h := promptHandler(&fakePrompt{})
	for name, body := range map[string]string{
		"bad stream":   nextBody("x", testSID, 0),
		"bad session":  nextBody(testStream, "nope", 0),
		"wait too big": nextBody(testStream, testSID, 15001),
		"wait missing": fmt.Sprintf(`{"stream":%q,"session_id":%q}`, testStream, testSID),
		"not json":     `nope`,
	} {
		if rec := post(t, h, http.MethodPost, PromptNextPath, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", name, rec.Code)
		}
	}
	if rec := post(t, h, http.MethodGet, PromptNextPath, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("get: %d", rec.Code)
	}
	if rec := post(t, promptHandler(nil), http.MethodPost, PromptNextPath, nextBody(testStream, testSID, 0)); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no queue: %d", rec.Code)
	}
}

func resultBodyP(stream, job, status, reason string) string {
	b, _ := json.Marshal(map[string]string{"stream": stream, "job_id": job, "status": status, "reason": reason})
	return string(b)
}

func TestPromptResult_Reported(t *testing.T) {
	f := &fakePrompt{}
	rec := post(t, promptHandler(f), http.MethodPost, PromptResultPath, resultBodyP(testStream, "pj-1", "dropped", "session_changed"))
	if rec.Code != http.StatusOK || len(f.results) != 1 || f.results[0].Status != "dropped" || f.results[0].Reason != "session_changed" || f.results[0].JobID != "pj-1" {
		t.Fatalf("%d %s %+v", rec.Code, rec.Body.String(), f.results)
	}
}

func TestPromptResult_Conflicts(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
		body string
	}{
		{ErrPromptNotLeased, http.StatusConflict, "not_leased"},
		{ErrPromptNotOwner, http.StatusConflict, "not_owner"},
		{errors.New("boom"), http.StatusInternalServerError, "internal"},
	} {
		rec := post(t, promptHandler(&fakePrompt{resErr: c.err}), http.MethodPost, PromptResultPath, resultBodyP(testStream, "pj-1", "accepted", ""))
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.body) {
			t.Fatalf("%v: %d %s", c.err, rec.Code, rec.Body.String())
		}
	}
}

func TestPromptResult_Validation(t *testing.T) {
	h := promptHandler(&fakePrompt{})
	for name, body := range map[string]string{
		"bad stream":     resultBodyP("x", "pj-1", "accepted", ""),
		"no job":         resultBodyP(testStream, "", "accepted", ""),
		"unknown status": resultBodyP(testStream, "pj-1", "unknown", ""),
		"long reason":    resultBodyP(testStream, "pj-1", "dropped", strings.Repeat("r", 129)),
		"not json":       `nope`,
	} {
		if rec := post(t, h, http.MethodPost, PromptResultPath, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", name, rec.Code)
		}
	}
	if rec := post(t, promptHandler(nil), http.MethodPost, PromptResultPath, resultBodyP(testStream, "pj-1", "accepted", "")); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no queue: %d", rec.Code)
	}
}

// The prompt gate is the queue's own: a stream long-polling the workbook queue does not block its prompt poll.
func TestPromptNext_HasItsOwnPollGate(t *testing.T) {
	h := &handler{}
	relA, ok := h.polls.acquire(context.Background(), testStream)
	if !ok {
		t.Fatal("workbook gate")
	}
	defer relA()
	done := make(chan struct{})
	go func() {
		rel, ok := h.promptPolls.acquire(context.Background(), testStream)
		if ok {
			rel()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the prompt poll waited for the workbook poll")
	}
}

// The owner of a session's prompts is the live stream of that session that announced prompt.v1 most recently.
func TestRegistry_NewestCapableStream(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := &now
	reg := NewRegistry(func() time.Time { return *clock })
	announce := func(stream, sid string, caps ...string) {
		_, _ = reg.Apply(Batch{V: 1, Stream: stream, Agent: "cc", Caps: caps,
			Events: []Event{{Seq: 1, SID: sid, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	}
	if _, ok := reg.NewestCapableStream(testSID, CapPromptV1, CapsFresh); ok {
		t.Fatal("an owner with no streams")
	}
	announce("Aaaaaaaaaaaaaaaaaaaaaa", testSID, CapPromptV1)
	*clock = now.Add(5 * time.Second)
	announce("Bbbbbbbbbbbbbbbbbbbbbb", testSID, CapPromptV1)
	announce("Cccccccccccccccccccccc", testSID, CapWorkbookV2) // newer, but not prompt-capable
	announce("Dddddddddddddddddddddd", "99999999-aaaa-bbbb-cccc-dddddddddddd", CapPromptV1)
	if got, ok := reg.NewestCapableStream(testSID, CapPromptV1, CapsFresh); !ok || got != "Bbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("owner = %q %v, want the newest announcer", got, ok)
	}
	*clock = now.Add(5*time.Second + CapsFresh + time.Second) // every announcement is stale
	if _, ok := reg.NewestCapableStream(testSID, CapPromptV1, CapsFresh); ok {
		t.Fatal("a stale announcement still owns the session")
	}
}

// After a /clear the old session's standing poll (up to 15 s, it cannot be cancelled) must not hold up the new session's:
// the prompt gate is per stream AND session (codex attack). Mutation gate: key the gate by stream only → red.
func TestPromptNext_ANewSessionOfTheSameStreamIsNotBlockedByTheOldPoll(t *testing.T) {
	const other = "99999999-aaaa-bbbb-cccc-dddddddddddd"
	reg := NewRegistry(time.Now)
	_, _ = reg.Apply(Batch{V: 1, Stream: testStream, Agent: "cc", Caps: []string{CapPromptV1},
		Events: []Event{{Seq: 1, SID: testSID, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	block := make(chan struct{})
	svc := &blockingPrompt{block: block, started: make(chan struct{}, 1)}
	h := NewHandler(reg, WithPrompt(func() PromptService { return svc }))
	go func() { post(t, h, http.MethodPost, PromptNextPath, nextBody(testStream, testSID, 5000)) }()
	<-svc.started // the old session's poll is parked in the queue
	// the process switches session: the same stream now announces for the other id
	_, _ = reg.Apply(Batch{V: 1, Stream: testStream, Agent: "cc", Caps: []string{CapPromptV1},
		Events: []Event{{Seq: 2, SID: other, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	done := make(chan int, 1)
	go func() { done <- post(t, h, http.MethodPost, PromptNextPath, nextBody(testStream, other, 0)).Code }()
	select {
	case code := <-done:
		if code != http.StatusNoContent {
			t.Fatalf("new session's poll: %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the new session's poll waited behind the old one")
	}
	close(block)
}

type blockingPrompt struct {
	block   chan struct{}
	started chan struct{}
}

func (b *blockingPrompt) NextPrompt(ctx context.Context, _, sid string, _ time.Duration) (any, bool) {
	if sid == testSID {
		b.started <- struct{}{}
		select {
		case <-b.block:
		case <-ctx.Done():
		}
	}
	return nil, false
}

func (b *blockingPrompt) PromptResult(string, PromptResult) error { return nil }

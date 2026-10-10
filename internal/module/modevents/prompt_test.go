package modeventsmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/promptq"
)

// U3-0b: the real queue behind the real mod-socket handler, with the owner chosen from the real stream registry.

const sidP = "aaaaaaaa-1111-4111-8111-111111111111"

func announce(reg *modevents.Registry, stream string, caps ...string) {
	_, _ = reg.Apply(modevents.Batch{V: 1, Stream: stream, Agent: "cc", Caps: caps,
		Events: []modevents.Event{{Seq: 1, SID: sidP, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
}

func sock(h http.Handler, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

func TestPrompt_SubmitThroughTheSocket(t *testing.T) {
	reg := modevents.NewRegistry(time.Now)
	q := promptq.New(streamOwners{reg})
	m := &Module{queue: q}
	h := modevents.NewHandler(reg, modevents.WithPrompt(m.promptService))
	announce(reg, "Aaaaaaaaaaaaaaaaaaaaaa", modevents.CapPromptV1)

	got := make(chan promptq.Result, 1)
	go func() { r, _ := q.Submit(context.Background(), sidP, "cm-1", "hello"); got <- r }()

	// a stream that did not announce prompt.v1 is told nothing
	announce(reg, "Bbbbbbbbbbbbbbbbbbbbbb", modevents.CapWorkbookV2)
	if rec := sock(h, modevents.PromptNextPath, `{"stream":"Bbbbbbbbbbbbbbbbbbbbbb","session_id":"`+sidP+`","wait_ms":0}`); rec.Code != http.StatusNoContent {
		t.Fatalf("incapable stream: %d", rec.Code)
	}
	var rec *httptest.ResponseRecorder
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec = sock(h, modevents.PromptNextPath, `{"stream":"Aaaaaaaaaaaaaaaaaaaaaa","session_id":"`+sidP+`","wait_ms":100}`)
		if rec.Code == http.StatusOK || time.Now().After(deadline) {
			break
		}
	}
	var job struct {
		Job promptq.Job `json:"job"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &job) != nil || job.Job.Text != "hello" || job.Job.Kind != promptq.KindSubmit {
		t.Fatalf("next: %d %s", rec.Code, rec.Body.String())
	}
	body := `{"stream":"Aaaaaaaaaaaaaaaaaaaaaa","job_id":"` + job.Job.ID + `","status":"accepted"}`
	if rec := sock(h, modevents.PromptResultPath, body); rec.Code != http.StatusOK {
		t.Fatalf("result: %d %s", rec.Code, rec.Body.String())
	}
	if r := <-got; r.Status != promptq.Accepted {
		t.Fatalf("answer = %+v", r)
	}
	// the same job again: no longer leased
	if rec := sock(h, modevents.PromptResultPath, body); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not_leased") {
		t.Fatalf("second result: %d %s", rec.Code, rec.Body.String())
	}
}

// Two live streams for one session: only the newer announcer owns it; the older one's result after the change is
// refused as not_owner. Mutation gate: pick the older stream → red.
func TestPrompt_OnlyTheNewestAnnouncerOwnsTheSession(t *testing.T) {
	now := time.Now()
	clock := &now
	reg := modevents.NewRegistry(func() time.Time { return *clock })
	q := promptq.New(streamOwners{reg})
	q.Wait, q.HandTimeout = time.Second, time.Second
	m := &Module{queue: q}
	h := modevents.NewHandler(reg, modevents.WithPrompt(m.promptService))
	announce(reg, "Aaaaaaaaaaaaaaaaaaaaaa", modevents.CapPromptV1)
	*clock = now.Add(2 * time.Second)
	announce(reg, "Bbbbbbbbbbbbbbbbbbbbbb", modevents.CapPromptV1) // a reloaded mod: the newer announcer

	go func() { _, _ = q.Submit(context.Background(), sidP, "cm-1", "x") }()
	if rec := sock(h, modevents.PromptNextPath, `{"stream":"Aaaaaaaaaaaaaaaaaaaaaa","session_id":"`+sidP+`","wait_ms":50}`); rec.Code != http.StatusNoContent {
		t.Fatalf("the older stream was handed a job: %d", rec.Code)
	}
	var rec *httptest.ResponseRecorder
	for deadline := time.Now().Add(2 * time.Second); ; {
		rec = sock(h, modevents.PromptNextPath, `{"stream":"Bbbbbbbbbbbbbbbbbbbbbb","session_id":"`+sidP+`","wait_ms":100}`)
		if rec.Code == http.StatusOK || time.Now().After(deadline) {
			break
		}
	}
	var job struct {
		Job promptq.Job `json:"job"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &job) != nil || job.Job.ID == "" {
		t.Fatalf("the owner got nothing: %d %s", rec.Code, rec.Body.String())
	}
	// the older stream announces again, newer than B: ownership moves; B's result is refused
	*clock = now.Add(4 * time.Second)
	announce(reg, "Aaaaaaaaaaaaaaaaaaaaaa", modevents.CapPromptV1)
	rec = sock(h, modevents.PromptResultPath, `{"stream":"Bbbbbbbbbbbbbbbbbbbbbb","job_id":"`+job.Job.ID+`","status":"accepted"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not_owner") {
		t.Fatalf("result from a former owner: %d %s", rec.Code, rec.Body.String())
	}
}

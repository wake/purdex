package modeventsmod

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/promptq"
)

// The two spellings of the notice capability are one string.
func TestNotice_CapabilitySpellingsAgree(t *testing.T) {
	if modevents.CapLeaseNoticeV1 != promptq.CapLeaseNotice {
		t.Fatalf("modevents %q, promptq %q", modevents.CapLeaseNoticeV1, promptq.CapLeaseNotice)
	}
}

// A notice goes through the socket to the stream that announced the notice capability, and only to it: a stream that
// announced the prompt capability alone is told nothing. (M: OwnerOfCap answers the prompt owner.)
func TestNotice_ThroughTheSocketToTheCapableStream(t *testing.T) {
	reg := modevents.NewRegistry(time.Now)
	q := promptq.New(streamOwners{reg})
	m := &Module{queue: q}
	h := modevents.NewHandler(reg, modevents.WithPrompt(m.promptService))
	announce(reg, "Cccccccccccccccccccccc", modevents.CapPromptV1, modevents.CapLeaseNoticeV1)
	time.Sleep(5 * time.Millisecond)
	announce(reg, "Aaaaaaaaaaaaaaaaaaaaaa", modevents.CapPromptV1) // the newer announcer owns the prompts, not the notices

	got := make(chan promptq.Result, 1)
	go func() { r, _ := q.SubmitNotice(context.Background(), sidP, "n-1", "輪到你了"); got <- r }()

	if rec := sock(h, modevents.PromptNextPath, `{"stream":"Aaaaaaaaaaaaaaaaaaaaaa","session_id":"`+sidP+`","wait_ms":50}`); rec.Code != http.StatusNoContent {
		t.Fatalf("the stream without the notice capability: %d %s", rec.Code, rec.Body.String())
	}
	var rec = sock(h, modevents.PromptNextPath, `{"stream":"Cccccccccccccccccccccc","session_id":"`+sidP+`","wait_ms":2000}`)
	var job struct {
		Job promptq.Job `json:"job"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &job) != nil || job.Job.Kind != promptq.KindNotice || job.Job.Text != "輪到你了" {
		t.Fatalf("next: %d %s", rec.Code, rec.Body.String())
	}
	body := `{"stream":"Cccccccccccccccccccccc","job_id":"` + job.Job.ID + `","status":"accepted"}`
	if rec := sock(h, modevents.PromptResultPath, body); rec.Code != http.StatusOK {
		t.Fatalf("result: %d %s", rec.Code, rec.Body.String())
	}
	if r := <-got; r.Status != promptq.Accepted {
		t.Fatalf("answer = %+v", r)
	}
}

// A stream that announced only the notice capability can poll for its notices too (it is the owner of them): the poll
// gate follows the owner rule. (M: gate the poll on prompt.v1 alone.)
func TestNotice_ANoticeOnlyStreamCanPoll(t *testing.T) {
	reg := modevents.NewRegistry(time.Now)
	q := promptq.New(streamOwners{reg})
	m := &Module{queue: q}
	h := modevents.NewHandler(reg, modevents.WithPrompt(m.promptService))
	announce(reg, "Dddddddddddddddddddddd", modevents.CapLeaseNoticeV1)

	got := make(chan promptq.Result, 1)
	go func() { r, _ := q.SubmitNotice(context.Background(), sidP, "n-2", "輪到你了"); got <- r }()
	rec := sock(h, modevents.PromptNextPath, `{"stream":"Dddddddddddddddddddddd","session_id":"`+sidP+`","wait_ms":2000}`)
	var job struct {
		Job promptq.Job `json:"job"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &job) != nil || job.Job.Kind != promptq.KindNotice {
		t.Fatalf("next: %d %s", rec.Code, rec.Body.String())
	}
	body := `{"stream":"Dddddddddddddddddddddd","job_id":"` + job.Job.ID + `","status":"accepted"}`
	if rec := sock(h, modevents.PromptResultPath, body); rec.Code != http.StatusOK {
		t.Fatalf("result: %d %s", rec.Code, rec.Body.String())
	}
	if r := <-got; r.Status != promptq.Accepted {
		t.Fatalf("answer = %+v", r)
	}
}

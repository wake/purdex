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

// K-1a-1 review: the mod's `unknown` status and a read's `truncated` flag cross the socket into the queue.
func TestPrompt_UnknownAndTruncatedCrossTheSocket(t *testing.T) {
	reg := modevents.NewRegistry(time.Now)
	q := promptq.New(streamOwners{reg})
	m := &Module{queue: q}
	h := modevents.NewHandler(reg, modevents.WithPrompt(m.promptService))
	const stream = "Aaaaaaaaaaaaaaaaaaaaaa"
	announce(reg, stream, modevents.CapPromptV1)
	run := func(start func() promptq.Result, result func(id string) string) promptq.Result {
		got := make(chan promptq.Result, 1)
		go func() { got <- start() }()
		var job struct {
			Job promptq.Job `json:"job"`
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			rec := sock(h, modevents.PromptNextPath, `{"stream":"`+stream+`","session_id":"`+sidP+`","wait_ms":100}`)
			if rec.Code == http.StatusOK {
				_ = json.Unmarshal(rec.Body.Bytes(), &job)
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("no job")
			}
		}
		if rec := sock(h, modevents.PromptResultPath, result(job.Job.ID)); rec.Code != http.StatusOK {
			t.Fatalf("result: %d %s", rec.Code, rec.Body.String())
		}
		return <-got
	}
	r := run(func() promptq.Result { r, _ := q.SubmitFill(context.Background(), sidP, "cm-u", "x"); return r },
		func(id string) string {
			return `{"stream":"` + stream + `","job_id":"` + id + `","status":"unknown","reason":"fill_error"}`
		})
	if r.Status != promptq.Unknown || r.Reason != "fill_error" || r.Phase != promptq.PhaseUnknown {
		t.Fatalf("unknown: %+v", r)
	}
	r = run(func() promptq.Result { r, _ := q.Read(context.Background(), sidP); return r },
		func(id string) string {
			return `{"stream":"` + stream + `","job_id":"` + id + `","status":"accepted","text":"cut","truncated":true}`
		})
	if r.Text != "cut" || !r.Truncated {
		t.Fatalf("read: %+v", r)
	}
}

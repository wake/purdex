package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

const testClaim = "0123456789abcdef0123456789abcdef"

func TestRegisterLocal_PostsAndDecodesTheClaim(t *testing.T) {
	var got map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/local-runs", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		writeJSON(w, wire.LocalRunResponse{ID: "j_01JC", ClaimID: testClaim, LeaseS: 60, RenewS: 15})
	})
	req := wire.LocalRunRequest{}
	req.Task = "lint"
	resp, err := New(fakeWWD(t, mux)).RegisterLocal(context.Background(), req)
	if err != nil || resp.ID != "j_01JC" || resp.ClaimID != testClaim || resp.RenewS != 15 {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if got["task"] != "lint" {
		t.Fatalf("server saw %v", got)
	}
}

func TestRegisterLocal_ALockConflictCarriesTheKeys(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/local-runs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, wire.LocksBusy{Reason: wire.LocksBusyReason, Keys: []string{"tmux", "port:7860"},
			Holders: []wire.LockHolder{{Key: "tmux", Job: "j_01"}, {Key: "port:7860", Job: "j_02"}}})
	})
	_, err := New(fakeWWD(t, mux)).RegisterLocal(context.Background(), wire.LocalRunRequest{})
	var lb *LocksBusyError
	if !errors.As(err, &lb) || len(lb.Keys) != 2 || lb.Keys[1] != "port:7860" || len(lb.Holders) != 2 || lb.Holders[1].Job != "j_02" {
		t.Fatalf("err = %v", err)
	}
	if got := lb.Held(); got != "tmux (held by j_01), port:7860 (held by j_02)" {
		t.Fatalf("Held = %q", got)
	}
}

func TestRegisterLocal_AnotherConflictBodyStaysAStatusError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/local-runs", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "something else", http.StatusConflict)
	})
	_, err := New(fakeWWD(t, mux)).RegisterLocal(context.Background(), wire.LocalRunRequest{})
	var se *StatusError
	var lb *LocksBusyError
	if !errors.As(err, &se) || errors.As(err, &lb) {
		t.Fatalf("err = %v", err)
	}
}

func TestRegisterLocal_ANoSlotAnswerIsASlotBusyError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/local-runs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		writeJSON(w, wire.SlotBusy{Reason: wire.SlotBusyReason, Slot: "heavy", Reasons: []string{"slots_full", "gate_full"}})
	})
	_, err := New(fakeWWD(t, mux)).RegisterLocal(context.Background(), wire.LocalRunRequest{})
	var sb *SlotBusyError
	if !errors.As(err, &sb) || sb.Slot != "heavy" || len(sb.Reasons) != 2 || sb.Error() != "no heavy slot: slots_full, gate_full" {
		t.Fatalf("err = %v", err)
	}
}

func TestRegisterLocal_AnotherTooManyRequestsBodyStaysAStatusError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/local-runs", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	_, err := New(fakeWWD(t, mux)).RegisterLocal(context.Background(), wire.LocalRunRequest{})
	var se *StatusError
	var sb *SlotBusyError
	if !errors.As(err, &se) || errors.As(err, &sb) {
		t.Fatalf("err = %v", err)
	}
}

// A 429 whose JSON merely decodes (an empty object, another reason, a bare refusal) is not this protocol: it stays a StatusError, so
// the caller does not wait out its whole queue_timeout on a real service error.
func TestRegisterLocal_AnotherTooManyRequestsJSONStaysAStatusError(t *testing.T) {
	for _, body := range []string{`{}`, `{"reason":"other","slot":"light","reasons":["x"]}`, `{"reason":"slot_busy","slot":"medium","reasons":["x"]}`, `{"reason":"slot_busy","slot":"light","reasons":[]}`} {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/local-runs", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(body))
		})
		_, err := New(fakeWWD(t, mux)).RegisterLocal(context.Background(), wire.LocalRunRequest{})
		var se *StatusError
		var sb *SlotBusyError
		if !errors.As(err, &se) || errors.As(err, &sb) {
			t.Fatalf("body %s: err = %v", body, err)
		}
	}
}

func TestHeartbeat_SendsTheClaimAndAConflictIsClaimLost(t *testing.T) {
	var got wire.HeartbeatRequest
	conflict := false
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/jobs/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "j_a" {
			t.Errorf("id = %q", r.PathValue("id"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		if conflict {
			http.Error(w, "gone", http.StatusConflict)
			return
		}
		writeJSON(w, wire.HeartbeatResponse{LeaseS: 60, RenewS: 15, Cancel: true})
	})
	c := New(fakeWWD(t, mux))
	hb, err := c.Heartbeat(context.Background(), "j_a", testClaim)
	if err != nil || !hb.Cancel || got.ClaimID != testClaim {
		t.Fatalf("hb %+v err %v got %+v", hb, err, got)
	}
	conflict = true
	if _, err := c.Heartbeat(context.Background(), "j_a", testClaim); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("err = %v", err)
	}
}

func TestPostResult_ValidatesBeforeSendingAndMapsAConflict(t *testing.T) {
	calls := 0
	status := http.StatusNoContent
	var got wire.ResultRequest
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/jobs/{id}/result", func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
	})
	c := New(fakeWWD(t, mux))
	if err := c.PostResult(context.Background(), "j_a", wire.ResultRequest{ClaimID: "bad", State: wire.StateSucceeded}); err == nil || calls != 0 {
		t.Fatalf("an invalid result went out: err %v calls %d", err, calls)
	}
	if err := c.PostResult(context.Background(), "j_a", wire.ResultRequest{ClaimID: testClaim, State: wire.StateQueued}); err == nil || calls != 0 {
		t.Fatalf("an unpostable state went out: err %v calls %d", err, calls)
	}
	code := 0
	if err := c.PostResult(context.Background(), "j_a", wire.ResultRequest{ClaimID: testClaim, State: wire.StateSucceeded, Exit: &code}); err != nil || calls != 1 || got.State != wire.StateSucceeded {
		t.Fatalf("err %v calls %d got %+v", err, calls, got)
	}
	status = http.StatusConflict
	if err := c.PostResult(context.Background(), "j_a", wire.ResultRequest{ClaimID: testClaim, State: wire.StateFailed}); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("err = %v", err)
	}
}

func TestPutLog_SendsTheClaimInTheQueryAndTheBodyVerbatim(t *testing.T) {
	var gotClaim, gotType, gotBody string
	status := http.StatusNoContent
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/jobs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		gotClaim, gotType = r.URL.Query().Get("claim_id"), r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(status)
	})
	c := New(fakeWWD(t, mux))
	if err := c.PutLog(context.Background(), "j_a", testClaim, []byte("line 1\nline 2\n")); err != nil {
		t.Fatal(err)
	}
	if gotClaim != testClaim || gotBody != "line 1\nline 2\n" || gotType != "text/plain; charset=utf-8" {
		t.Fatalf("claim %q type %q body %q", gotClaim, gotType, gotBody)
	}
	if err := c.PutLog(context.Background(), "j_a", "not a claim", nil); err == nil {
		t.Fatal("an invalid claim id was sent")
	}
	status = http.StatusConflict
	if err := c.PutLog(context.Background(), "j_a", testClaim, []byte("x")); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("err = %v", err)
	}
}

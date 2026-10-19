package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestQualifyPostsTheTaskAndReadsThePair(t *testing.T) {
	var got struct{ Task string }
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/qualify", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got.Task == "purdex/nope" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no task \"purdex/nope\" with a qualify in this host's prepared clones"}`))
			return
		}
		_, _ = w.Write([]byte(`{"label":"task:purdex/tsc","candidate":"j_1","reference":"j_2"}`))
	})
	c := New(fakeWWD(t, mux))
	ctx := context.Background()
	s, err := c.Qualify(ctx, "purdex/tsc")
	if err != nil || got.Task != "purdex/tsc" || s.Label != "task:purdex/tsc" || s.Candidate != "j_1" || s.Reference != "j_2" {
		t.Fatalf("got %+v task %q err %v", s, got.Task, err)
	}
	var se *StatusError
	if _, err := c.Qualify(ctx, "purdex/nope"); !errors.As(err, &se) || se.Code != http.StatusNotFound {
		t.Fatalf("an unknown task: %v", err)
	}
	// a task that is not <repo>/<task> never leaves the process
	if _, err := c.Qualify(ctx, "tsc"); err == nil {
		t.Fatal("a bare task name was sent")
	}
}

func TestQualifyStatusIsCheckedBeforeItIsReturned(t *testing.T) {
	body := `{"in_flight":[{"label":"task:purdex/tsc","candidate":"j_1","reference":"j_2"}],"recent":[{"label":"task:purdex/tsc","outcome":"match","why":"","at":"2026-10-20T03:00:00Z","candidate":"j_1","reference":"j_2"}]}`
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/qualify", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
	st, err := New(fakeWWD(t, mux)).QualifyStatus(context.Background())
	if err != nil || len(st.InFlight) != 1 || st.Recent[0].Outcome != "match" {
		t.Fatalf("got %+v err %v", st, err)
	}
	body = `{"in_flight":[],"recent":[{"label":"task:purdex/tsc","outcome":"maybe"}]}`
	if _, err := New(fakeWWD(t, mux)).QualifyStatus(context.Background()); err == nil {
		t.Fatal("an outcome that is not one of the three was shown")
	}
}

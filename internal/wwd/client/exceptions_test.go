package client

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestExceptions_SendsTheHostAndDecodesTheHolds(t *testing.T) {
	var gotHost string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/exceptions", func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.URL.Query().Get("host")
		_, _ = w.Write([]byte(`[{"name":"pkg","until":"2026-10-19T09:00:00Z","pid":null,"holds":["heavy","light"]}]`))
	})
	c := New(fakeWWD(t, mux))
	list, known, err := c.Exceptions(context.Background(), "air26")
	if err != nil || !known || gotHost != "air26" || len(list) != 1 || len(list[0].Holds) != 2 {
		t.Fatalf("host=%q known=%v list=%+v err=%v", gotHost, known, list, err)
	}
}

// A host whose wwd predates the route answers 404 through the forward: not known, not an error, and an empty list (never null).
func TestExceptions_A404IsNotKnown(t *testing.T) {
	mux := http.NewServeMux()
	list, known, err := New(fakeWWD(t, mux)).Exceptions(context.Background(), "mserv")
	if err != nil || known || list == nil || len(list) != 0 {
		t.Fatalf("known=%v list=%+v err=%v", known, list, err)
	}
}

// ...but the local wwd's own "unknown host" 404 is a mistake of the caller.
func TestExceptions_AnUnknownHostIsStillAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/exceptions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"unknown host","host":"nowhere"}`))
	})
	_, _, err := New(fakeWWD(t, mux)).Exceptions(context.Background(), "nowhere")
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 404 {
		t.Fatalf("err = %v", err)
	}
}

// Only net/http's own "no such route" 404 is an old host; a proxy's 404 page or any other 404 is an answer, not "not known" (R1).
func TestExceptions_AnotherA404IsAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/exceptions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html>proxy: nothing here</html>"))
	})
	_, known, err := New(fakeWWD(t, mux)).Exceptions(context.Background(), "air26")
	var se *StatusError
	if known || !errors.As(err, &se) || se.Code != 404 {
		t.Fatalf("known=%v err=%v", known, err)
	}
}

// A host named in the call may be forwarded by the local wwd (its own limit is 10 s), so that call outlasts the short default (R1).
func TestExceptions_AForwardedCallOutlastsTheShortTimeout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/exceptions", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(400 * time.Millisecond):
			_, _ = w.Write([]byte(`[]`))
		case <-r.Context().Done():
		}
	})
	c := New(fakeWWD(t, mux))
	c.Timeout = 100 * time.Millisecond
	if _, _, err := c.Exceptions(context.Background(), ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("local: err = %v, want ErrUnavailable", err)
	}
	if _, _, err := c.Exceptions(context.Background(), "mserv"); err != nil {
		t.Fatalf("forwarded: %v", err)
	}
}

func TestExceptions_RefusesWhatTheContractRefuses(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field": `[{"name":"x","until":"2026-10-19T09:00:00Z","pid":null,"holds":[],"zzz":1}]`,
		"bad hold":      `[{"name":"x","until":"2026-10-19T09:00:00Z","pid":null,"holds":["gpu"]}]`,
		"not a list":    `{"name":"x"}`,
	} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/exceptions", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		_, _, err := New(fakeWWD(t, mux)).Exceptions(context.Background(), "")
		if !errors.Is(err, ErrBadResponse) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

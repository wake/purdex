package client

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// Client.Qualification (WWD-7b2-0a-2) is Exceptions' twin: the host is forwarded by the local wwd, a wwd that predates the route is "not known".
func TestQualification_SendsTheHostAndDecodes(t *testing.T) {
	var gotHost string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/qualification", func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.URL.Query().Get("host")
		_, _ = w.Write([]byte(`{"host":"air26","tasks":[{"label":"task:purdex/tsc","reference":"air26","hold":"reference","progress":"idle"}],"recent":[]}`))
	})
	v, known, err := New(fakeWWD(t, mux)).Qualification(context.Background(), "air26")
	if err != nil || !known || gotHost != "air26" || v.Host != "air26" || len(v.Tasks) != 1 || v.Tasks[0].Hold != "reference" {
		t.Fatalf("host=%q known=%v v=%+v err=%v", gotHost, known, v, err)
	}
}

func TestQualification_A404FromAnOlderWwdIsNotKnownWithEmptyLists(t *testing.T) {
	for _, mux := range []*http.ServeMux{http.NewServeMux()} {
		v, known, err := New(fakeWWD(t, mux)).Qualification(context.Background(), "mserv")
		if err != nil || known || v.Tasks == nil || len(v.Tasks) != 0 || v.Recent == nil || len(v.Recent) != 0 {
			t.Fatalf("known=%v v=%+v err=%v", known, v, err)
		}
	}
}

// The local wwd's own "unknown host" 404 (the alias is not in the pool) and any other 404 are answers, not "not known".
func TestQualification_AnotherA404IsAnError(t *testing.T) {
	for name, body := range map[string]string{"unknown host": `{"error":"unknown host","host":"nowhere"}`, "a proxy page": "<html>proxy</html>"} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/qualification", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(body))
		})
		_, known, err := New(fakeWWD(t, mux)).Qualification(context.Background(), "nowhere")
		var se *StatusError
		if known || !errors.As(err, &se) || se.Code != 404 {
			t.Fatalf("%s: known=%v err=%v", name, known, err)
		}
	}
}

// A 405 is never "an older wwd": no wwd had another method on this path (unlike /v1/exceptions), so it is the refusal it says, even
// with net/http's own body.
func TestQualification_A405IsARefusalNotAnOldHost(t *testing.T) {
	for name, body := range map[string]string{"the mux's own": "Method Not Allowed\n", "a handler's": `{"error":"read only"}`} {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/qualification", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
		if name == "a handler's" {
			mux = http.NewServeMux()
			mux.HandleFunc("GET /v1/qualification", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusMethodNotAllowed)
				_, _ = w.Write([]byte(body))
			})
		}
		_, known, err := New(fakeWWD(t, mux)).Qualification(context.Background(), "air19")
		var se *StatusError
		if known || !errors.As(err, &se) || se.Code != 405 {
			t.Fatalf("%s: known=%v err=%v", name, known, err)
		}
	}
}

// A 200 that is not an answer of the contract is a bad response (502 upstream), never "known".
func TestQualification_ABadAnswerIsABadResponse(t *testing.T) {
	for name, body := range map[string]string{
		"empty object":   `{}`,
		"bad task label": `{"host":"mserv","tasks":[{"label":"tool:x","hold":"none","progress":"ready"}],"recent":[]}`,
		"no progress":    `{"host":"mserv","tasks":[{"label":"task:purdex/tsc","hold":"none"}],"recent":[]}`,
		"bad outcome":    `{"host":"mserv","tasks":[],"recent":[{"label":"task:purdex/tsc","outcome":"maybe","why":"","at":"2026-10-22T09:00:00Z","candidate":"","reference":""}]}`,
	} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/qualification", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		_, known, err := New(fakeWWD(t, mux)).Qualification(context.Background(), "")
		if known || !errors.Is(err, ErrBadResponse) {
			t.Errorf("%s: known=%v err=%v", name, known, err)
		}
	}
}

// A null list in an answer is an empty one: the App reads arrays.
func TestQualification_NullListsBecomeEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/qualification", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"host":"mserv","tasks":null,"recent":null}`))
	})
	v, known, err := New(fakeWWD(t, mux)).Qualification(context.Background(), "")
	if err != nil || !known || v.Tasks == nil || v.Recent == nil {
		t.Fatalf("known=%v v=%+v err=%v", known, v, err)
	}
}

// A forwarded call may take the local wwd's own forward limit (10 s), longer than the short default.
func TestQualification_AForwardedCallOutlastsTheShortTimeout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/qualification", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(400 * time.Millisecond):
			_, _ = w.Write([]byte(`{"host":"air26","tasks":[],"recent":[]}`))
		case <-r.Context().Done():
		}
	})
	c := New(fakeWWD(t, mux))
	c.Timeout = 100 * time.Millisecond
	if _, _, err := c.Qualification(context.Background(), ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a local call keeps the short limit: %v", err)
	}
	if _, known, err := c.Qualification(context.Background(), "air26"); err != nil || !known {
		t.Fatalf("a forwarded call outlasts it: known=%v err=%v", known, err)
	}
}

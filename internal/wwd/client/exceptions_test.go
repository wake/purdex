package client

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A wwd that predates GET /v1/exceptions but already has POST /v1/exceptions answers a GET with its mux's 405 (the path exists, the method
// does not), not a 404: it is the same "this wwd does not know the route", not a rejection (#3681).
func TestExceptions_A405FromAnOlderWwdIsNotKnownEither(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/exceptions", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, host := range []string{"", "air19"} {
		list, known, err := New(fakeWWD(t, mux)).Exceptions(context.Background(), host)
		if err != nil || known || list == nil || len(list) != 0 {
			t.Fatalf("host=%q known=%v list=%+v err=%v", host, known, list, err)
		}
	}
}

// Only the mux's own 405 is a missing GET: another 405 (a handler's own reason, a proxy's page) is the answer it is. And only the call
// that knows an older wwd can answer so (Exceptions) asks: RouteMissing, which the others use, stays the 404 alone.
func TestRouteOrMethodMissing_Only405OfTheMuxCounts(t *testing.T) {
	mux405 := &StatusError{Code: 405, Body: "Method Not Allowed\n"}
	if !RouteOrMethodMissing(mux405) {
		t.Error("net/http's own 405 is a route this wwd does not have for GET")
	}
	if !RouteOrMethodMissing(&StatusError{Code: 404, Body: "404 page not found\n"}) {
		t.Error("and its 404 still is")
	}
	if RouteMissing(mux405) {
		t.Error("RouteMissing must stay the 404: a 405 for a route that exists is not what its callers (FactsSeries) mean")
	}
	for _, e := range []error{
		&StatusError{Code: 405, Body: `{"error":"read only"}`},
		&StatusError{Code: 405, Body: "<html>405 from a proxy</html>"},
		&StatusError{Code: 405, Body: "Method Not Allowed, but more"},
		&StatusError{Code: 405, Body: ""},
		&StatusError{Code: 403, Body: "Method Not Allowed"},
	} {
		if RouteOrMethodMissing(e) {
			t.Errorf("RouteOrMethodMissing(%v) = true", e)
		}
	}
}

// FactsSeries has a route on every wwd that answers it at all, so a 405 there is a refusal (a handler or a layer in front of it), and it is
// not answered by asking for ten times the data.
func TestFactsSeries_A405IsNotAMissingRoute(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/series", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
	_, err := New(fakeWWD(t, mux)).FactsSeries(context.Background(), wire.FactsSeriesQuery{FactsHistoryQuery: wire.FactsHistoryQuery{Step: wire.Step1m}})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 405 || RouteMissing(err) {
		t.Fatalf("err = %v, want the 405 kept as a refusal that is not RouteMissing", err)
	}
}

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
		"bad hold":   `[{"name":"x","until":"2026-10-19T09:00:00Z","pid":null,"holds":["gpu"]}]`,
		"not a list": `{"name":"x"}`,
	} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/exceptions", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		_, _, err := New(fakeWWD(t, mux)).Exceptions(context.Background(), "")
		if !errors.Is(err, ErrBadResponse) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// StartExceptionOn / StopExceptionOn send the host to the local wwd, which forwards; a refusal comes back as a *StatusError like the
// calls that read, so the command can tell a wwd that does not know holds from a bad request.
func TestExceptionOnForwardsAndReportsRefusals(t *testing.T) {
	var method, path, host, body string
	status := http.StatusOK
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/exceptions", func(w http.ResponseWriter, r *http.Request) {
		method, path, host = r.Method, r.URL.Path, r.URL.Query().Get("host")
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		body = string(b[:n])
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"x"}`))
	})
	mux.HandleFunc("/v1/exceptions/", func(w http.ResponseWriter, r *http.Request) {
		method, path, host = r.Method, r.URL.Path, r.URL.Query().Get("host")
		w.WriteHeader(status)
	})
	c := New(fakeWWD(t, mux))
	until := time.Now().Add(time.Hour).UTC()
	e := wire.Exception{Name: "hold-x", Until: &until, Holds: []wire.Hold{wire.HoldHeavy}, Flags: []wire.HoldFlag{wire.HoldFlagRelease}}
	if err := c.StartExceptionOn(context.Background(), "air26", e); err != nil {
		t.Fatal(err)
	}
	if method != "POST" || path != "/v1/exceptions" || host != "air26" || !strings.Contains(body, `"flags":["release"]`) {
		t.Fatalf("%s %s host=%q body=%s", method, path, host, body)
	}
	if err := c.StopExceptionOn(context.Background(), "air26", "hold-x"); err != nil {
		t.Fatal(err)
	}
	if method != "DELETE" || path != "/v1/exceptions/hold-x" || host != "air26" {
		t.Fatalf("%s %s host=%q", method, path, host)
	}
	status = http.StatusBadRequest
	var se *StatusError
	if err := c.StartExceptionOn(context.Background(), "air26", e); !errors.As(err, &se) || se.Code != 400 || !strings.Contains(se.Body, "error") {
		t.Fatalf("a refusal: %v", err)
	}
	if err := c.StopExceptionOn(context.Background(), "air26", "hold-x"); !errors.As(err, &se) || se.Code != 400 {
		t.Fatalf("a refusal: %v", err)
	}
	// host "" is this host: the plain calls
	status = http.StatusOK
	if err := c.StartExceptionOn(context.Background(), "", e); err != nil || host != "" {
		t.Fatalf("this host: %v host=%q", err, host)
	}
}

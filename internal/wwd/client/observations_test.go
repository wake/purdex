package client

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

const obsBody = `{"host":"air26","since":"2026-10-20T01:00:00Z","observations":[{"id":"o_1_2","host":"air26","session":"s1","via":"session","command":"go test","signature":"go test","started_at":"2026-10-20T01:00:00.123456789+08:00","ended_at":null,"cpu_seconds":61.5,"cpu_peak":100,"mem_peak_mb":300,"procs_peak":3}],"truncated":true,"next_until":"2026-10-20T01:00:00.123456789+08:00"}`

// The answer comes back byte for byte as wwd wrote it (next_until is a cursor that must not be re-formatted), after it passed the contract;
// the host, since and until are sent as given and an absent one is not sent.
func TestObservations_ReturnsWhatWwdWroteAndSendsTheQuery(t *testing.T) {
	var q map[string][]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/observations", func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		_, _ = w.Write([]byte(obsBody))
	})
	c := New(fakeWWD(t, mux))
	raw, known, err := c.Observations(context.Background(), "air26", "2026-10-19T00:00:00Z", "2026-10-20T01:00:00.123456789+08:00")
	if err != nil || !known || string(raw) != obsBody {
		t.Fatalf("known=%v err=%v raw=%s", known, err, raw)
	}
	if q["host"][0] != "air26" || q["since"][0] != "2026-10-19T00:00:00Z" || q["until"][0] != "2026-10-20T01:00:00.123456789+08:00" {
		t.Fatalf("query = %v", q)
	}
	if _, _, err := c.Observations(context.Background(), "", "", ""); err != nil {
		t.Fatal(err)
	}
	if len(q) != 0 {
		t.Fatalf("nothing asked, nothing sent: %v", q)
	}
}

// A wwd without the route (its mux's 404) is "not known", not an error; the local wwd's own "unknown host" 404 is still an error.
func TestObservations_ARouteThatIsMissingIsNotKnown(t *testing.T) {
	raw, known, err := New(fakeWWD(t, http.NewServeMux())).Observations(context.Background(), "mserv", "", "")
	if err != nil || known || raw != nil {
		t.Fatalf("raw=%s known=%v err=%v", raw, known, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/observations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"unknown host","host":"nowhere"}`))
	})
	_, _, err = New(fakeWWD(t, mux)).Observations(context.Background(), "nowhere", "", "")
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 404 {
		t.Fatalf("err = %v", err)
	}
}

func TestObservations_RefusesWhatTheContractRefuses(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/observations", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"host":"","since":"2026-10-20T01:00:00Z","observations":[],"truncated":false,"next_until":null}`))
	})
	mux.HandleFunc("GET /v1/observations/undeclared", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"host":"air26","since":"2026-10-20T01:00:00Z","groups":[{"signature":"","count":1,"last_via":"session"}],"truncated":false}`))
	})
	c := New(fakeWWD(t, mux))
	if _, _, err := c.Observations(context.Background(), "", "", ""); !errors.Is(err, ErrBadResponse) {
		t.Errorf("observations: err = %v, want ErrBadResponse", err)
	}
	if _, _, err := c.Undeclared(context.Background(), "", ""); !errors.Is(err, ErrBadResponse) {
		t.Errorf("undeclared: err = %v, want ErrBadResponse", err)
	}
}

func TestUndeclared_ReturnsWhatWwdWroteAndNotKnownOnAMissingRoute(t *testing.T) {
	const body = `{"host":"air26","since":"2026-10-13T00:00:00Z","groups":[{"signature":"go test","count":2,"cpu_seconds":130.5,"max_mem_mb":900,"last_at":"2026-10-20T01:00:00Z","last_session":"s1","last_via":"codex","last_command":"go test ./..."}],"truncated":false}`
	var host, since string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/observations/undeclared", func(w http.ResponseWriter, r *http.Request) {
		host, since = r.URL.Query().Get("host"), r.URL.Query().Get("since")
		_, _ = w.Write([]byte(body))
	})
	raw, known, err := New(fakeWWD(t, mux)).Undeclared(context.Background(), "air26", "2026-10-13T00:00:00Z")
	if err != nil || !known || string(raw) != body || host != "air26" || since != "2026-10-13T00:00:00Z" {
		t.Fatalf("known=%v err=%v host=%q since=%q raw=%s", known, err, host, since, raw)
	}
	raw, known, err = New(fakeWWD(t, http.NewServeMux())).Undeclared(context.Background(), "air19", "")
	if err != nil || known || raw != nil {
		t.Fatalf("raw=%s known=%v err=%v", raw, known, err)
	}
}

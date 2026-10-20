package client

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestAlerts_SendsTheHostAndDecodes(t *testing.T) {
	var gotHost string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/alerts", func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.URL.Query().Get("host")
		_, _ = w.Write([]byte(`{"host":"air26","alerts":[{"key":"thrash","kind":"thrash","level":"digest","since":"2026-10-19T04:00:00Z","last":"2026-10-19T04:01:00Z","cleared_at":null}]}`))
	})
	c := New(fakeWWD(t, mux))
	l, known, err := c.Alerts(context.Background(), "air26")
	if err != nil || !known || gotHost != "air26" || l.Host != "air26" || len(l.Alerts) != 1 {
		t.Fatalf("host=%q known=%v list=%+v err=%v", gotHost, known, l, err)
	}
	if _, _, err := c.Alerts(context.Background(), ""); err != nil || gotHost != "" {
		t.Fatalf("no host asked: %q %v", gotHost, err)
	}
}

// Spec §13.1: a reader treats 404 on /v1/alerts as "this host has no alerts yet" (its wwd predates the contract, or it was built
// without the store). That is not an error and not a list the host vouched for.
func TestAlerts_A404IsNoAlertsYet(t *testing.T) {
	mux := http.NewServeMux() // no route: the mux answers 404
	l, known, err := New(fakeWWD(t, mux)).Alerts(context.Background(), "air19")
	if err != nil || known || l.Host != "air19" || l.Alerts == nil || len(l.Alerts) != 0 {
		t.Fatalf("known=%v list=%+v err=%v", known, l, err)
	}
}

// ...but the local wwd's own "unknown host" 404 is a mistake of the caller, not an old peer.
func TestAlerts_AnUnknownHostIsStillAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/alerts", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"unknown host","host":"nowhere"}`))
	})
	_, _, err := New(fakeWWD(t, mux)).Alerts(context.Background(), "nowhere")
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 404 {
		t.Fatalf("err = %v", err)
	}
}

func TestAlerts_RefusesWhatTheContractRefuses(t *testing.T) {
	for name, body := range map[string]string{
		"bad kind": `{"host":"air26","alerts":[{"key":"k","kind":"meltdown","level":"push","since":"2026-10-19T04:00:00Z","last":"2026-10-19T04:00:00Z","cleared_at":null}]}`,
		"no host":  `{"host":"","alerts":[]}`,
	} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/alerts", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		_, _, err := New(fakeWWD(t, mux)).Alerts(context.Background(), "")
		if !errors.Is(err, ErrBadResponse) {
			t.Errorf("%s: err = %v, want ErrBadResponse", name, err)
		}
	}
}

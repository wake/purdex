package client

import (
	"context"
	"net/http"
	"testing"
)

func TestEarnedLabels_Decodes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/earned-labels", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"host":"mserv","labels":[{"label":"task:purdex/tsc","state":"earned","fingerprint":{"node":"24.14.0"},"snapshot":"abc","granted_at":"2026-10-20T03:00:00Z"}]}`))
	})
	l, known, err := New(fakeWWD(t, mux)).EarnedLabels(context.Background())
	if err != nil || !known || l.Host != "mserv" || len(l.Labels) != 1 || l.Labels[0].Fingerprint["node"] != "24.14.0" {
		t.Fatalf("known=%v list=%+v err=%v", known, l, err)
	}
}

// A wwd that predates the route answers 404: "none yet", not an error and not a list the host vouched for.
func TestEarnedLabels_A404IsNoneYet(t *testing.T) {
	mux := http.NewServeMux()
	l, known, err := New(fakeWWD(t, mux)).EarnedLabels(context.Background())
	if err != nil || known || l.Labels == nil || len(l.Labels) != 0 {
		t.Fatalf("known=%v list=%+v err=%v", known, l, err)
	}
}

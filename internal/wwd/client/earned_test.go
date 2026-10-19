package client

import (
	"context"
	"encoding/json"
	"errors"
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

// QuarantineLabel and ReleaseLabel post the label to their own routes; a label that is not a task label never leaves the process, and a
// host without the label answers a StatusError the command can read.
func TestQuarantineAndReleaseLabelPostTheLabel(t *testing.T) {
	var got []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/earned-labels/{action}", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Label string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, r.PathValue("action")+" "+body.Label)
		if body.Label == "task:purdex/none" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	c := New(fakeWWD(t, mux))
	ctx := context.Background()
	if err := c.QuarantineLabel(ctx, "task:purdex/tsc"); err != nil {
		t.Fatal(err)
	}
	if err := c.ReleaseLabel(ctx, "task:purdex/tsc"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "quarantine task:purdex/tsc" || got[1] != "release task:purdex/tsc" {
		t.Fatalf("posted %v", got)
	}
	if err := c.QuarantineLabel(ctx, "os:linux"); err == nil || len(got) != 2 {
		t.Fatalf("a label that is not a task label was sent: %v err %v", got, err)
	}
	var se *StatusError
	if err := c.QuarantineLabel(ctx, "task:purdex/none"); !errors.As(err, &se) || se.Code != http.StatusNotFound {
		t.Fatalf("err %v", err)
	}
}

package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func TestWorkerOnAndSetWorkerOnCarryTheHostAndTheEtag(t *testing.T) {
	var gotHost, gotMethod string
	var gotBody wire.WorkerPut
	status := 200
	answer := wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 2, Light: 1}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/worker", func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotMethod = r.URL.Query().Get("host"), r.Method
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			gotBody = wire.WorkerPut{}
			if err := json.Unmarshal(b, &gotBody); err != nil {
				t.Errorf("body %s: %v", b, err)
			}
		}
		w.WriteHeader(status)
		if status == http.StatusConflict {
			// longer than the 200 bytes an ordinary call keeps of a refusal
			writeJSON(w, wire.WorkerEtagMismatch{Reason: wire.WorkerEtagMismatchReason, Etag: answer.Etag(), Worker: wire.WorkerSettings{OwnerRule: strings.Repeat("x", 300)}.View()})
			return
		}
		writeJSON(w, answer.View())
	})
	c := New(fakeWWD(t, mux))
	ctx := context.Background()

	for _, host := range []string{"", "air26"} {
		v, err := c.WorkerOn(ctx, host)
		if err != nil || gotMethod != http.MethodGet || gotHost != host || v.Etag != answer.Etag() || v.Slots.Heavy != 2 {
			t.Fatalf("WorkerOn(%q): %+v %v (method %s host %q)", host, v, err, gotMethod, gotHost)
		}
		gotBody = wire.WorkerPut{}
		v, err = c.SetWorkerOn(ctx, host, answer, "e1")
		if err != nil || gotMethod != http.MethodPut || gotHost != host || gotBody.ExpectEtag != "e1" || !gotBody.Enabled || v.Etag != answer.Etag() {
			t.Fatalf("SetWorkerOn(%q): %+v %v (method %s host %q body %+v)", host, v, err, gotMethod, gotHost, gotBody)
		}
		// no expect: the field is not in the body at all, which is what a wwd from before the etag accepts
		if _, err := c.SetWorkerOn(ctx, host, answer, ""); err != nil || gotBody.ExpectEtag != "" {
			t.Fatalf("SetWorkerOn(%q) without expect: %v %+v", host, err, gotBody)
		}
	}

	// a refusal is a *StatusError that keeps the code and the body, local or forwarded
	status = http.StatusConflict
	for _, host := range []string{"", "air26"} {
		_, err := c.SetWorkerOn(ctx, host, answer, "stale")
		var se *StatusError
		if !errors.As(err, &se) || se.Code != http.StatusConflict {
			t.Fatalf("SetWorkerOn(%q) 409: %v", host, err)
		}
		// the whole refusal comes back, whichever host it is about
		if m, e := wire.DecodeLenient[wire.WorkerEtagMismatch]([]byte(se.Body)); e != nil || m.Reason != wire.WorkerEtagMismatchReason || len(m.Worker.OwnerRule) != 300 {
			t.Errorf("SetWorkerOn(%q) 409 body (%d bytes) was cut: %v", host, len(se.Body), e)
		}
	}
}

func TestWorkerOnReadsAnAnswerWithNoEtag(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/worker", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1}})
	})
	v, err := New(fakeWWD(t, mux)).WorkerOn(context.Background(), "")
	if err != nil || v.Etag != "" || !v.Enabled {
		t.Fatalf("%+v %v", v, err)
	}
}

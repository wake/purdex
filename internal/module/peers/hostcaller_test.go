// internal/module/peers/hostcaller_test.go
package peers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
)

type callBody struct {
	ID     string `json:"id"`
	ToHost string `json:"to_host_id"`
}

// hostsHolder is a mutable "live" peer list, as config is.
type hostsHolder struct{ v atomic.Value }

func newHolder(h ...config.PeerHost) *hostsHolder {
	x := &hostsHolder{}
	x.v.Store(h)
	return x
}
func (h *hostsHolder) set(p ...config.PeerHost) { h.v.Store(p) }
func (h *hostsHolder) get() []config.PeerHost   { return h.v.Load().([]config.PeerHost) }

func callerFor(h *hostsHolder) *HostCaller { return NewHostCaller(h.get, nil) }

func serve(t *testing.T, fn http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(fn)
	t.Cleanup(s.Close)
	return s
}

func okJSON(w http.ResponseWriter, hostID string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "host_id": hostID, "outcome": map[string]string{"state": "ok"}})
}

func TestHostCaller_DoneAndRequestShape(t *testing.T) {
	var gotAuth, gotPath, gotCT string
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotCT = r.Header.Get("Authorization"), r.URL.Path, r.Header.Get("Content-Type")
		okJSON(w, "hostB")
	})
	h := newHolder(config.PeerHost{Alias: "b", URL: s.URL, HostID: "hostB", Token: "tok1"})
	res := callerFor(h).Call(context.Background(), "hostB", "/api/peers/team/commands", callBody{ID: "c1", ToHost: "hostB"})
	if res.Class != ClassDone || res.Status != 200 {
		t.Fatalf("res = %+v", res)
	}
	if gotAuth != "Bearer tok1" || gotPath != "/api/peers/team/commands" || gotCT != "application/json" {
		t.Fatalf("auth=%q path=%q ct=%q", gotAuth, gotPath, gotCT)
	}
	if !strings.Contains(string(res.Body), `"outcome"`) {
		t.Fatalf("body = %s", res.Body)
	}
}

func TestHostCaller_NoEntryIsUnpaired(t *testing.T) {
	res := callerFor(newHolder()).Call(context.Background(), "hostB", "/x", callBody{ToHost: "hostB"})
	if res.Class != ClassUnpaired {
		t.Fatalf("res = %+v", res)
	}
}

// Rule 1: the entry is resolved by host id at each call, never by alias.
// An alias deleted and re-created for another host must not receive it.
func TestHostCaller_ResolvesByHostIDNotAlias(t *testing.T) {
	var hitA, hitC int32
	sa := serve(t, func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hitA, 1); okJSON(w, "hostB") })
	sc := serve(t, func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hitC, 1); okJSON(w, "hostC") })
	h := newHolder(config.PeerHost{Alias: "b", URL: sa.URL, HostID: "hostB", Token: "tok-xyz"})
	c := callerFor(h)
	if res := c.Call(context.Background(), "hostB", "/x", callBody{ToHost: "hostB"}); res.Class != ClassDone {
		t.Fatalf("first = %+v", res)
	}
	// alias "b" is recreated pointing at another host
	h.set(config.PeerHost{Alias: "b", URL: sc.URL, HostID: "hostC", Token: "t2"})
	res := c.Call(context.Background(), "hostB", "/x", callBody{ToHost: "hostB"})
	if res.Class != ClassUnpaired {
		t.Fatalf("res = %+v", res)
	}
	if atomic.LoadInt32(&hitC) != 0 || atomic.LoadInt32(&hitA) != 1 {
		t.Fatalf("hitA=%d hitC=%d", hitA, hitC)
	}
}

func TestHostCaller_TokenReadFromLiveEntry(t *testing.T) {
	var auth string
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { auth = r.Header.Get("Authorization"); okJSON(w, "hostB") })
	h := newHolder(config.PeerHost{URL: s.URL, HostID: "hostB", Token: "old"})
	c := callerFor(h)
	c.Call(context.Background(), "hostB", "/x", callBody{ToHost: "hostB"})
	h.set(config.PeerHost{URL: s.URL, HostID: "hostB", Token: "new"})
	c.Call(context.Background(), "hostB", "/x", callBody{ToHost: "hostB"})
	if auth != "Bearer new" {
		t.Fatalf("auth = %q", auth)
	}
}

func TestHostCaller_BodyToHostMismatchIsLocalRefusal(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { t.Error("must not be sent"); okJSON(w, "hostB") })
	h := newHolder(config.PeerHost{URL: s.URL, HostID: "hostB", Token: "tok-xyz"})
	res := callerFor(h).Call(context.Background(), "hostB", "/x", callBody{ToHost: "other"})
	if res.Class != ClassRefused || res.Code != "bad_request_local" {
		t.Fatalf("res = %+v", res)
	}
}

func TestHostCaller_RedirectNotFollowed(t *testing.T) {
	var followed int32
	target := serve(t, func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&followed, 1); okJSON(w, "hostB") })
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	h := newHolder(config.PeerHost{URL: s.URL, HostID: "hostB", Token: "tok-xyz"})
	res := callerFor(h).Call(context.Background(), "hostB", "/x", callBody{ToHost: "hostB"})
	if res.Class != ClassTransient || res.Status != 302 {
		t.Fatalf("res = %+v", res)
	}
	if atomic.LoadInt32(&followed) != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestHostCaller_ResponseHostIDMismatchIsWrongHost(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { okJSON(w, "someoneElse") })
	h := newHolder(config.PeerHost{URL: s.URL, HostID: "hostB", Token: "tok-xyz"})
	res := callerFor(h).Call(context.Background(), "hostB", "/x", callBody{ToHost: "hostB"})
	if res.Class != ClassWrongHost {
		t.Fatalf("res = %+v", res)
	}
}

func TestHostCaller_Classification(t *testing.T) {
	jsonErr := func(code int, errCode string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": errCode, "detail": "d"})
		}
	}
	plain := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", code) }
	}
	cases := []struct {
		name  string
		fn    http.HandlerFunc
		class CallClass
		code  string
	}{
		{"500", plain(500), ClassTransient, ""},
		{"503", plain(503), ClassTransient, ""},
		{"429", jsonErr(429, "rate_limited"), ClassTransient, "rate_limited"},
		{"401 plain", plain(401), ClassUnauthorized, ""},
		{"404", plain(404), ClassUnsupported, ""},
		{"403 non-json", plain(403), ClassUnsupported, ""},
		{"403 json", jsonErr(403, "host_unverified"), ClassRefused, "host_unverified"},
		{"400 json", jsonErr(400, "unsupported_kind"), ClassRefused, "unsupported_kind"},
		{"409 wrong_host", jsonErr(409, "wrong_host"), ClassWrongHost, "wrong_host"},
		{"409 id_conflict", jsonErr(409, "id_conflict"), ClassRefused, "id_conflict"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := serve(t, tc.fn)
			h := newHolder(config.PeerHost{URL: s.URL, HostID: "hostB", Token: "tok-xyz"})
			res := callerFor(h).Call(context.Background(), "hostB", "/x", callBody{ToHost: "hostB"})
			if res.Class != tc.class || res.Code != tc.code {
				t.Fatalf("res = %+v, want %s/%q", res, tc.class, tc.code)
			}
		})
	}
}

func TestHostCaller_TransportErrorIsTransient(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	url := s.URL
	s.Close()
	h := newHolder(config.PeerHost{URL: url, HostID: "hostB", Token: "tok-xyz"})
	res := callerFor(h).Call(context.Background(), "hostB", "/x", callBody{ToHost: "hostB"})
	if res.Class != ClassTransient || res.Err == nil {
		t.Fatalf("res = %+v", res)
	}
}

func TestHostCaller_ResponseCapIsTransient(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("a", maxHostCallRespBytes+10)))
	})
	h := newHolder(config.PeerHost{URL: s.URL, HostID: "hostB", Token: "tok-xyz"})
	res := callerFor(h).Call(context.Background(), "hostB", "/x", callBody{ToHost: "hostB"})
	if res.Class != ClassTransient || res.Err == nil {
		t.Fatalf("res = %+v", res)
	}
}

func TestHostCaller_RemoteTextRedactsToken(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad", "detail": "echo SECRETTOK"})
	})
	h := newHolder(config.PeerHost{URL: s.URL, HostID: "hostB", Token: "SECRETTOK"})
	res := callerFor(h).Call(context.Background(), "hostB", "/x", callBody{ToHost: "hostB"})
	if strings.Contains(res.Detail, "SECRETTOK") {
		t.Fatalf("detail leaks token: %q", res.Detail)
	}
}

func TestEscalate401(t *testing.T) {
	first := time.Unix(1000, 0)
	if got := Escalate401(first, first.Add(9*time.Minute)); got != ClassTransient {
		t.Fatalf("9m = %s", got)
	}
	if got := Escalate401(first, first.Add(10*time.Minute)); got != ClassUnpairedByPeer {
		t.Fatalf("10m = %s", got)
	}
}

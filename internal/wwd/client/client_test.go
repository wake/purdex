package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// fakeWWD serves h on a unix socket in a short temp dir (a socket path is limited to ~104 bytes) and returns its path.
func fakeWWD(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wwd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "w.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return sock
}

func serveFixture(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(filepath.Join("..", "wire", "testdata", name))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}
}

func TestPool_DecodesTheContractFixture(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/pool", serveFixture("pool.json"))
	c := New(fakeWWD(t, mux))
	p, err := c.Pool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Peers) != 1 || p.Peers[0].Info.Alias != "mlab" || p.Peers[0].Reachable {
		t.Fatalf("pool = %+v", p)
	}
}

func TestInfo_DecodesTheContractFixture(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/info", serveFixture("info.json"))
	c := New(fakeWWD(t, mux))
	i, err := c.Info(context.Background())
	if err != nil || i.Alias != "mlab" || i.HostID != "h-mlab" {
		t.Fatalf("info = %+v, %v", i, err)
	}
}

func TestFactsHistory_SendsTheQueryAndDecodes(t *testing.T) {
	var gotSince, gotStep string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) {
		gotSince, gotStep = r.URL.Query().Get("since"), r.URL.Query().Get("step")
		serveFixture("facts_history_response.json")(w, r)
	})
	c := New(fakeWWD(t, mux))
	since := time.Date(2026, 10, 18, 1, 2, 3, 0, time.UTC)
	h, err := c.FactsHistory(context.Background(), wire.FactsHistoryQuery{Since: since, Step: wire.Step1m})
	if err != nil {
		t.Fatal(err)
	}
	if gotSince != "2026-10-18T01:02:03Z" || gotStep != "1m" || h.Step != "1m" {
		t.Fatalf("since=%q step=%q answer=%+v", gotSince, gotStep, h)
	}
}

// wwd is not running: a dial error that says so (ErrUnavailable), quickly, never a hang.
func TestUnavailable_WhenThereIsNoSocket(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "none.sock"))
	_, err := c.Pool(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// wwd answers something the contract refuses (a bad proto, not json): ErrBadResponse, so the caller can tell a broken wwd from an absent
// one. (A field this build does not know is not refused: lenient_test.go.)
func TestBadResponse_UnknownFieldAndBadProto(t *testing.T) {
	for name, body := range map[string]string{
		"bad proto": `{"peers": [{"info": {"host_id": "h", "alias": "a", "version": "0", "proto": 9, "platform": "darwin", "arch": "arm64", "worker": {"enabled": false}, "labels": []}, "facts": null, "reachable": false}]}`,
		"not json":  `<html>`,
	} {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /v1/pool", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) })
			c := New(fakeWWD(t, mux))
			if _, err := c.Pool(context.Background()); !errors.Is(err, ErrBadResponse) {
				t.Fatalf("err = %v, want ErrBadResponse", err)
			}
		})
	}
}

// A non-200 answer is a StatusError carrying the code; a 5xx is not "unavailable" (wwd is there).
func TestStatusError_CarriesTheCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/pool", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) })
	c := New(fakeWWD(t, mux))
	_, err := c.Pool(context.Background())
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 500 || errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

// A wwd that accepts and never answers is cut by the client's timeout, not left hanging.
func TestTimeout_AHungWWDEndsTheCall(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/pool", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	c := New(fakeWWD(t, mux))
	c.Timeout = 100 * time.Millisecond
	start := time.Now()
	_, err := c.Pool(context.Background())
	if !errors.Is(err, ErrUnavailable) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestDefaultSocket_EnvOverridesHome(t *testing.T) {
	t.Setenv("PDX_WWD_SOCK", "/x/y.sock")
	if got := DefaultSocket(); got != "/x/y.sock" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("PDX_WWD_SOCK", "")
	t.Setenv("HOME", "/home/u")
	if got := DefaultSocket(); got != "/home/u/.local/share/wwd/wwd.sock" {
		t.Fatalf("got %q", got)
	}
}

func TestFactsSeries_SendsTheQueryAndDecodes(t *testing.T) {
	var got url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/series", func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Write([]byte(`{"step":"1m","points":[{"at":"2026-10-18T01:00:00Z","zone":"normal","cpu_idle_pct":50,"mem_available_mb":1,"pressure":1,"page_out_rate":null,"disk_max_used_pct":null,"headroom":{"heavy":1,"light":2}}],"truncated":true}`))
	})
	c := New(fakeWWD(t, mux))
	q := wire.FactsSeriesQuery{FactsHistoryQuery: wire.FactsHistoryQuery{Since: time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC), Step: wire.Step1m, Host: "mserv"}, Limit: 2000}
	r, err := c.FactsSeries(context.Background(), q)
	if err != nil || len(r.Points) != 1 || !r.Truncated || r.Points[0].Headroom.Light != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	if got.Get("host") != "mserv" || got.Get("limit") != "2000" || got.Get("step") != "1m" || got.Get("since") != "2026-10-18T01:00:00Z" {
		t.Fatalf("query = %v", got)
	}
}

// A wwd without the route answers its mux's plain 404; one that does not know the host answers a JSON 404 naming it. Only the first
// is "the route is missing" (the caller falls back to the full history); the second is the caller's mistake and must not be retried.
func TestRouteMissing(t *testing.T) {
	old := http.NewServeMux() // no series route: net/http's own 404
	c := New(fakeWWD(t, old))
	_, err := c.FactsSeries(context.Background(), wire.FactsSeriesQuery{FactsHistoryQuery: wire.FactsHistoryQuery{Step: wire.Step1m}})
	if !RouteMissing(err) {
		t.Fatalf("an old wwd: err = %v, want RouteMissing", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/series", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"unknown host","host":"nowhere"}`))
	})
	c = New(fakeWWD(t, mux))
	_, err = c.FactsSeries(context.Background(), wire.FactsSeriesQuery{FactsHistoryQuery: wire.FactsHistoryQuery{Step: wire.Step1m, Host: "nowhere"}})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 404 || RouteMissing(err) {
		t.Fatalf("an unknown host: err = %v, want a 404 that is not RouteMissing", err)
	}
	for _, e := range []error{
		nil, ErrUnavailable, &StatusError{Code: 500, Body: "404 page not found"},
		&StatusError{Code: 404, Body: "<html>502 Bad Gateway: not found</html>"}, // a proxy's page
		&StatusError{Code: 404, Body: `{"error":"no such series"}`},              // another route's reason
		&StatusError{Code: 404, Body: `{"error":"Unknown Host"}`},                // not even the unknown-host spelling
		&StatusError{Code: 404, Body: "404 page not found, but more"},
	} {
		if RouteMissing(e) {
			t.Errorf("RouteMissing(%v) = true", e)
		}
	}
	if !RouteMissing(&StatusError{Code: 404, Body: "404 page not found\n"}) {
		t.Error("net/http's own 404 (with its newline) is a missing route")
	}
}

// Like the history, a series of another host waits for wwd's forwarding.
func TestFactsSeries_AForwardedCallOutlastsTheShortTimeout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/series", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(400 * time.Millisecond):
			w.Write([]byte(`{"step":"1m","points":[],"truncated":false}`))
		case <-r.Context().Done():
		}
	})
	c := New(fakeWWD(t, mux))
	c.Timeout = 100 * time.Millisecond
	q := wire.FactsSeriesQuery{FactsHistoryQuery: wire.FactsHistoryQuery{Step: wire.Step1m}}
	if _, err := c.FactsSeries(context.Background(), q); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("local: err = %v, want ErrUnavailable", err)
	}
	q.Host = "mserv"
	if _, err := c.FactsSeries(context.Background(), q); err != nil {
		t.Fatalf("forwarded: %v", err)
	}
}

// historyBody is a history answer of about n bytes: one facts record, repeated.
func historyBody(t *testing.T, n int) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "wire", "testdata", "facts_darwin.json"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := wire.Decode[wire.Facts](raw)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := json.Marshal(f)
	h := wire.FactsHistoryResponse{Step: wire.Step1m}
	for i := 0; i < n/len(one)+1; i++ {
		h.Records = append(h.Records, f)
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A day of 1 m points is about 10 MB: more than the general cap, still an answer (spec §10 series).
func TestFactsHistory_AcceptsAnAnswerOverTheGeneralCap(t *testing.T) {
	body := historyBody(t, maxBody+(2<<20))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	mux.HandleFunc("GET /v1/pool", func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	c := New(fakeWWD(t, mux))
	h, err := c.FactsHistory(context.Background(), wire.FactsHistoryQuery{Step: wire.Step1m})
	if err != nil || len(h.Records) == 0 {
		t.Fatalf("%d bytes: %d records, %v", len(body), len(h.Records), err)
	}
	// ...and the general cap still holds for everything else
	if _, err := c.Pool(context.Background()); !errors.Is(err, ErrBadResponse) {
		t.Fatalf("pool err = %v, want ErrBadResponse", err)
	}
}

func TestFactsHistory_RefusesAnAnswerOverItsCap(t *testing.T) {
	body := historyBody(t, maxHistoryBody+(1<<20))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	c := New(fakeWWD(t, mux))
	if _, err := c.FactsHistory(context.Background(), wire.FactsHistoryQuery{Step: wire.Step1m}); !errors.Is(err, ErrBadResponse) {
		t.Fatalf("err = %v, want ErrBadResponse", err)
	}
}

// The local wwd may forward a history to a peer for up to 10 s: a call that names a host waits longer than the short default; one for
// this host does not.
func TestFactsHistory_AForwardedCallOutlastsTheShortTimeout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(400 * time.Millisecond):
			serveFixture("facts_history_response.json")(w, r)
		case <-r.Context().Done():
		}
	})
	c := New(fakeWWD(t, mux))
	c.Timeout = 100 * time.Millisecond
	if _, err := c.FactsHistory(context.Background(), wire.FactsHistoryQuery{Step: wire.Step1m}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("local: err = %v, want ErrUnavailable", err)
	}
	if _, err := c.FactsHistory(context.Background(), wire.FactsHistoryQuery{Step: wire.Step1m, Host: "mserv"}); err != nil {
		t.Fatalf("forwarded: %v", err)
	}
}

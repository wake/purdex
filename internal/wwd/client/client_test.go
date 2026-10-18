package client

import (
	"context"
	"errors"
	"net"
	"net/http"
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

// wwd answers something the contract refuses (an unknown field, a bad proto): ErrBadResponse, so the caller can tell a
// broken wwd from an absent one.
func TestBadResponse_UnknownFieldAndBadProto(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field": `{"peers": [], "extra": 1}`,
		"bad proto":     `{"peers": [{"info": {"host_id": "h", "alias": "a", "version": "0", "proto": 9, "platform": "darwin", "arch": "arm64", "worker": {"enabled": false}, "labels": []}, "facts": null, "reachable": false}]}`,
		"not json":      `<html>`,
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

package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Behaviour of the server newHTTPServer builds (#1523). Its real timeouts
// are scaled down by shrinkFactor so the tests run in milliseconds while
// keeping the production ratios. The scaling covers every timeout field, so
// a stray ReadTimeout/WriteTimeout in newHTTPServer would show up here.
const shrinkFactor = 50

func scaled(d time.Duration) time.Duration { return d / shrinkFactor }

func startShrunkServer(t *testing.T, h http.Handler) (*httptest.Server, *http.Server) {
	t.Helper()
	cfg := newHTTPServer("", h)
	cfg.ReadHeaderTimeout = scaled(cfg.ReadHeaderTimeout)
	cfg.IdleTimeout = scaled(cfg.IdleTimeout)
	cfg.ReadTimeout = scaled(cfg.ReadTimeout)
	cfg.WriteTimeout = scaled(cfg.WriteTimeout)
	ts := httptest.NewUnstartedServer(h)
	ts.Config = cfg
	ts.Start()
	t.Cleanup(ts.Close)
	return ts, cfg
}

// pause is several times the scaled ReadHeaderTimeout.
func pause(cfg *http.Server) time.Duration { return 5 * cfg.ReadHeaderTimeout }

func TestHTTPServer_SlowHeaderIsRejected(t *testing.T) {
	ts, cfg := startShrunkServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n")
	time.Sleep(pause(cfg)) // never finish the header block
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Fatalf("slow header was served")
		}
	}
	// Either an error response or a closed connection counts as rejected.
}

// ReadHeaderTimeout must not apply to the body: a slow body whose total time
// is many times the timeout still arrives whole.
func TestHTTPServer_SlowBodyNotCut(t *testing.T) {
	ts, cfg := startShrunkServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, "%d", len(b))
	}))
	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 6\r\n\r\n")
	start := time.Now()
	for i := 0; i < 6; i++ {
		io.WriteString(conn, "x")
		time.Sleep(cfg.ReadHeaderTimeout)
	}
	if time.Since(start) < 3*cfg.ReadHeaderTimeout {
		t.Fatal("test bug: not slow enough")
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("slow body cut: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "6" {
		t.Fatalf("got %d %q, want 200 \"6\"", resp.StatusCode, b)
	}
}

func TestHTTPServer_WebSocketSurvivesIdle(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	ts, cfg := startShrunkServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}))
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	time.Sleep(pause(cfg) + cfg.IdleTimeout) // idle beyond both timeouts
	if err := c.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("write after idle: %v", err)
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := c.ReadMessage()
	if err != nil || string(msg) != "ping" {
		t.Fatalf("echo = %q, %v", msg, err)
	}
}

func TestHTTPServer_SSEAndLongRequestSurvive(t *testing.T) {
	var cfg *http.Server
	ts, c := startShrunkServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/long" {
			time.Sleep(pause(cfg))
			fmt.Fprint(w, "done")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i := 0; i < 6; i++ {
			fmt.Fprintf(w, "data: %d\n\n", i)
			fl.Flush()
			time.Sleep(cfg.ReadHeaderTimeout)
		}
	}))
	cfg = c

	resp, err := http.Get(ts.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || strings.Count(string(b), "data:") != 6 {
		t.Fatalf("sse = %q, %v; want 6 events", b, err)
	}

	resp, err = http.Get(ts.URL + "/long")
	if err != nil {
		t.Fatal(err)
	}
	b, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(b) != "done" {
		t.Fatalf("long = %q, %v", b, err)
	}
}

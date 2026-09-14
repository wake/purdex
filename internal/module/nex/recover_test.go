package nex

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// syncBuf is a concurrency-safe bytes.Buffer for capturing log output from
// concurrent test requests and the server's own ErrorLog.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestRecoverer_PanicBeforeWrite_Returns500AndLogsThenServerStillWorks(t *testing.T) {
	var logBuf syncBuf
	logf := func(format string, args ...any) {
		fmt.Fprintf(&logBuf, format+"\n", args...)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	mux.HandleFunc("/healthy", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(recoverer(logf, mux))
	// Capture net/http's own "superfluous WriteHeader" log (if any) into a
	// separate buffer so test output stays pristine; not asserted on here.
	var serverErrLog syncBuf
	srv.Config.ErrorLog = log.New(&serverErrLog, "", 0)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/panic", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST /panic: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "POST") {
		t.Errorf("log output missing method POST: %q", logged)
	}
	if !strings.Contains(logged, "/panic") {
		t.Errorf("log output missing path /panic: %q", logged)
	}
	if !strings.Contains(logged, "boom") {
		t.Errorf("log output missing panic value \"boom\": %q", logged)
	}

	// The server must keep serving other requests after a panic.
	resp2, err := http.Get(srv.URL + "/healthy")
	if err != nil {
		t.Fatalf("GET /healthy after panic: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status after recovery = %d, want %d", resp2.StatusCode, http.StatusOK)
	}
}

func TestRecoverer_LogfPanics_StillReturns500AndServerKeepsServing(t *testing.T) {
	logf := func(format string, args ...any) {
		panic("logf itself blew up")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	mux.HandleFunc("/healthy", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(recoverer(logf, mux))
	var serverErrLog syncBuf
	srv.Config.ErrorLog = log.New(&serverErrLog, "", 0)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/panic", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST /panic: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (a panicking logf must not prevent the 500)", resp.StatusCode, http.StatusInternalServerError)
	}

	// The server must keep serving other requests even after logf panicked.
	resp2, err := http.Get(srv.URL + "/healthy")
	if err != nil {
		t.Fatalf("GET /healthy after logf panic: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status after recovery = %d, want %d", resp2.StatusCode, http.StatusOK)
	}
}

func TestRecoverer_FlushThenPanic_ClientSeesFlushedLineThenEOF(t *testing.T) {
	var logBuf syncBuf
	logf := func(format string, args ...any) {
		fmt.Fprintf(&logBuf, format+"\n", args...)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("ResponseWriter passed to handler is not an http.Flusher")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "first line")
		flusher.Flush()
		panic("stream boom")
	})
	mux.HandleFunc("/healthy", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(recoverer(logf, mux))
	var serverErrLog syncBuf
	srv.Config.ErrorLog = log.New(&serverErrLog, "", 0)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/stream")
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d (headers already sent before panic)", resp.StatusCode, http.StatusOK)
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil && readErr != io.EOF {
		t.Fatalf("reading body: unexpected error %v", readErr)
	}
	if got, want := string(body), "first line\n"; got != want {
		t.Errorf("body = %q, want %q (client should see flushed line then EOF)", got, want)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "stream boom") {
		t.Errorf("log output missing panic value \"stream boom\": %q", logged)
	}

	// Server keeps serving after streaming panic.
	resp2, err := http.Get(srv.URL + "/healthy")
	if err != nil {
		t.Fatalf("GET /healthy after stream panic: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status after recovery = %d, want %d", resp2.StatusCode, http.StatusOK)
	}
}

func TestRecoverer_ErrAbortHandlerPropagates(t *testing.T) {
	logf := func(format string, args ...any) {
		t.Errorf("logf should not be called for http.ErrAbortHandler, got: "+format, args...)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})

	h := recoverer(logf, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/abort", nil)

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		h.ServeHTTP(rec, req)
	}()

	if recovered != http.ErrAbortHandler {
		t.Errorf("recovered = %v, want http.ErrAbortHandler", recovered)
	}
}

func TestRecoverer_HijackerPreservedThroughRecoverer(t *testing.T) {
	logf := func(format string, args ...any) {}

	hijacked := make(chan bool, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/hijack", func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			hijacked <- false
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			hijacked <- false
			return
		}
		hijacked <- true
		buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
		buf.Flush()
		conn.Close()
	})

	srv := httptest.NewServer(recoverer(logf, mux))
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "GET /hijack HTTP/1.1\r\nHost: %s\r\n\r\n", srv.Listener.Addr().String()); err != nil {
		t.Fatalf("write request: %v", err)
	}

	select {
	case ok := <-hijacked:
		if !ok {
			t.Fatalf("handler could not assert http.Hijacker or hijack failed")
		}
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading hijacked response: %v", err)
	}
	if !strings.Contains(line, "200") {
		t.Errorf("hijacked response status line = %q, want it to contain 200", line)
	}
}

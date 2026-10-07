package middleware

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const stallTestD = 300 * time.Millisecond

type readResult struct {
	n   int
	err error
}

// startStallServer serves a handler that wraps the body with
// StallTimeoutBody and reports what io.ReadAll saw.
func startStallServer(t *testing.T, results chan<- readResult) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = StallTimeoutBody(w, r, stallTestD)
		b, err := io.ReadAll(r.Body)
		results <- readResult{len(b), err}
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestTimeout)
			return
		}
		fmt.Fprintf(w, "read %d", len(b))
	}))
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func dialChunked(t *testing.T, srv *httptest.Server) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_, err = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func sendChunk(t *testing.T, conn net.Conn, s string) {
	t.Helper()
	if _, err := fmt.Fprintf(conn, "%x\r\n%s\r\n", len(s), s); err != nil {
		t.Fatal(err)
	}
}

func waitResult(t *testing.T, ch <-chan readResult, within time.Duration) readResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(within):
		t.Fatal("handler did not finish read in time")
		return readResult{}
	}
}

// A steady trickle whose total time exceeds d must not be cut: each Read
// extends the deadline.
func TestStallTimeoutBody_SteadyTrickleSurvives(t *testing.T) {
	results := make(chan readResult, 1)
	srv := startStallServer(t, results)
	conn := dialChunked(t, srv)

	start := time.Now()
	const chunks = 10
	for i := 0; i < chunks; i++ {
		sendChunk(t, conn, "x")
		time.Sleep(stallTestD / 3)
	}
	if _, err := io.WriteString(conn, "0\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed <= stallTestD {
		t.Fatalf("test bug: total %v not longer than d %v", elapsed, stallTestD)
	}

	res := waitResult(t, results, 5*time.Second)
	if res.err != nil || res.n != chunks {
		t.Fatalf("read = %d bytes, err %v; want %d, nil", res.n, res.err, chunks)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// One byte, then silence longer than d: the read must fail.
func TestStallTimeoutBody_StallFailsRead(t *testing.T) {
	results := make(chan readResult, 1)
	srv := startStallServer(t, results)
	conn := dialChunked(t, srv)

	sendChunk(t, conn, "x")
	// Never send anything else; the handler must give up on its own.
	res := waitResult(t, results, 5*time.Second)
	if res.err == nil {
		t.Fatalf("read succeeded with %d bytes; want a timeout error", res.n)
	}
	var ne net.Error
	if !errors.As(res.err, &ne) || !ne.Timeout() {
		t.Fatalf("err = %v; want a timeout", res.err)
	}
}

// After the body hits EOF the deadline must be cleared: a handler that keeps
// running past d afterwards must not have its connection (and request
// context) torn down by a stale read deadline.
func TestStallTimeoutBody_EOFClearsDeadline(t *testing.T) {
	ctxErr := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = StallTimeoutBody(w, r, stallTestD)
		if _, err := io.ReadAll(r.Body); err != nil {
			ctxErr <- err
			return
		}
		select {
		case <-r.Context().Done():
			ctxErr <- r.Context().Err()
		case <-time.After(3 * stallTestD):
			ctxErr <- nil
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	resp, err := http.Post(srv.URL, "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	select {
	case err := <-ctxErr:
		if err != nil {
			t.Fatalf("request torn down after EOF: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not finish")
	}
}

// A ResponseWriter that does not support deadlines (httptest.ResponseRecorder)
// must not panic or fail the read.
func TestStallTimeoutBody_UnsupportedWriterIsNoop(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader("hello"))
	body := StallTimeoutBody(rec, r, time.Second)
	b, err := io.ReadAll(body)
	if err != nil || string(b) != "hello" {
		t.Fatalf("ReadAll = %q, %v", b, err)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// deadlineRecorder records every SetReadDeadline the body wrapper issues.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (d *deadlineRecorder) SetReadDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)
	return nil
}

// Deterministic check of the deadline lifecycle: extended before each read,
// zeroed at EOF and again at Close.
func TestStallTimeoutBody_DeadlineLifecycle(t *testing.T) {
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	r := httptest.NewRequest("POST", "/", strings.NewReader("hi"))
	body := StallTimeoutBody(rec, r, time.Minute)

	rec.deadlines = nil // drop the support probe
	buf := make([]byte, 8)
	if n, err := body.Read(buf); n != 2 || err != nil {
		t.Fatalf("first read = %d, %v", n, err)
	}
	if len(rec.deadlines) != 1 || !rec.deadlines[0].After(time.Now()) {
		t.Fatalf("deadline not extended before read: %v", rec.deadlines)
	}
	if _, err := body.Read(buf); err != io.EOF {
		t.Fatalf("second read err = %v, want EOF", err)
	}
	if last := rec.deadlines[len(rec.deadlines)-1]; !last.IsZero() {
		t.Fatalf("deadline not cleared at EOF: %v", last)
	}
	rec.deadlines = nil
	if err := body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(rec.deadlines) != 1 || !rec.deadlines[0].IsZero() {
		t.Fatalf("deadline not cleared at Close: %v", rec.deadlines)
	}
}

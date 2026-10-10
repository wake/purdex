package middleware

import (
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// UploadStallTimeout is how long an upload body may go without delivering a
// byte before the read fails.
const UploadStallTimeout = 30 * time.Second

// StallTimeoutBody wraps r.Body so that every Read first pushes the
// connection's read deadline to now+d. Data that keeps flowing never trips
// it, however long the whole upload takes; a client that goes silent for
// longer than d makes the next Read fail with a timeout. Assign the result
// back to r.Body (inside any MaxBytesReader).
//
// The deadline is cleared again on EOF and on Close so it cannot outlive the
// body and tear down the connection (or the request context, via the
// server's background read) while the handler keeps working.
//
// If the ResponseWriter does not support read deadlines (for example
// httptest.ResponseRecorder), the body is returned as-is.
func StallTimeoutBody(w http.ResponseWriter, r *http.Request, d time.Duration) io.ReadCloser {
	rc := http.NewResponseController(w)
	// Probe once: clearing a deadline is a no-op for the connection but
	// reports whether the writer supports deadlines at all.
	if err := rc.SetReadDeadline(time.Time{}); err != nil {
		return r.Body
	}
	return &stallBody{rc: rc, body: r.Body, d: d}
}

type stallBody struct {
	rc   *http.ResponseController
	body io.ReadCloser
	d    time.Duration
}

func (s *stallBody) Read(p []byte) (int, error) {
	_ = s.rc.SetReadDeadline(time.Now().Add(s.d))
	n, err := s.body.Read(p)
	if err == io.EOF {
		_ = s.rc.SetReadDeadline(time.Time{})
	}
	return n, err
}

func (s *stallBody) Close() error {
	_ = s.rc.SetReadDeadline(time.Time{})
	return s.body.Close()
}

// ErrUploadAborted is what an UploadBody read returns once Abort was called.
var ErrUploadAborted = errors.New("upload aborted")

// UploadBody is StallTimeoutBody plus two ways to end the upload from outside: an absolute deadline for the whole body
// (a client that sends one byte per stall window never trips the per-read timeout) and Abort (the principal was
// revoked). Both end a Read that is blocked on the socket, because they act through the connection's read deadline.
type UploadBody struct {
	rc      *http.ResponseController // nil when the writer has no deadlines (a recorder)
	body    io.ReadCloser
	stall   time.Duration
	end     time.Time // zero: no total deadline
	aborted atomic.Bool
}

// NewUploadBody wraps r.Body; total <= 0 means no absolute deadline. Assign it back to r.Body (inside any MaxBytesReader).
func NewUploadBody(w http.ResponseWriter, r *http.Request, stall, total time.Duration) *UploadBody {
	b := &UploadBody{body: r.Body, stall: stall}
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Time{}); err == nil {
		b.rc = rc
	}
	if total > 0 {
		b.end = time.Now().Add(total)
	}
	return b
}

// Abort makes the current and every later Read fail with ErrUploadAborted. Safe from any goroutine.
func (b *UploadBody) Abort() {
	b.aborted.Store(true)
	if b.rc != nil {
		_ = b.rc.SetReadDeadline(time.Unix(1, 0)) // wakes a Read blocked on the socket
	}
}

// Aborted reports whether Abort was called.
func (b *UploadBody) Aborted() bool { return b.aborted.Load() }

func (b *UploadBody) Read(p []byte) (int, error) {
	if b.aborted.Load() {
		return 0, ErrUploadAborted
	}
	if b.rc != nil {
		dl := time.Now().Add(b.stall)
		if !b.end.IsZero() && b.end.Before(dl) {
			dl = b.end
		}
		_ = b.rc.SetReadDeadline(dl)
		if b.aborted.Load() { // Abort landed between the check above and the line before: its deadline was overwritten
			_ = b.rc.SetReadDeadline(time.Unix(1, 0))
		}
	}
	n, err := b.body.Read(p)
	if err == io.EOF && b.rc != nil {
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	if err != nil && err != io.EOF && b.aborted.Load() {
		err = ErrUploadAborted
	}
	return n, err
}

func (b *UploadBody) Close() error {
	if b.rc != nil {
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	return b.body.Close()
}

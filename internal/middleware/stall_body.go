package middleware

import (
	"io"
	"net/http"
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

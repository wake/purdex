package main

import (
	"net/http"
	"time"
)

const (
	// httpReadHeaderTimeout bounds how long a client may take to send the
	// request headers (slowloris). It does not apply to bodies, WebSocket or
	// SSE streams.
	httpReadHeaderTimeout = 10 * time.Second
	// httpIdleTimeout caps an idle keep-alive connection; Go has no limit
	// when both this and ReadTimeout are zero.
	httpIdleTimeout = 120 * time.Second
)

// newHTTPServer builds the daemon's http.Server. ReadTimeout and
// WriteTimeout are deliberately left unset: a global ReadTimeout would cut
// slow large uploads (those use middleware.StallTimeoutBody instead) and
// WriteTimeout would cut long-lived streams.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
}

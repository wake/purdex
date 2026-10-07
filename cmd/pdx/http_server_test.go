package main

import (
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPServer_Timeouts(t *testing.T) {
	h := http.NewServeMux()
	srv := newHTTPServer("127.0.0.1:1234", h)
	if srv.Addr != "127.0.0.1:1234" {
		t.Errorf("Addr = %q", srv.Addr)
	}
	if srv.Handler != http.Handler(h) {
		t.Errorf("Handler not passed through")
	}
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %v, want 120s", srv.IdleTimeout)
	}
	// A global ReadTimeout would kill slow large uploads; WriteTimeout would
	// kill WS/SSE. Both must stay unset.
	if srv.ReadTimeout != 0 {
		t.Errorf("ReadTimeout = %v, want 0", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want 0", srv.WriteTimeout)
	}
}

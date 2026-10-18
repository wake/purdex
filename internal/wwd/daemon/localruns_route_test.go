package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/localruns"
	"github.com/wake/purdex/internal/wwd/worker/locks"
)

func localRunsDaemon(t *testing.T) *Daemon {
	t.Helper()
	// no queue behind it: the service has not rebuilt its locks, so a registration is answered 503 — enough to tell the route exists
	svc := localruns.New(locks.New(), nil, "testhost")
	return New(Options{HostID: "h1", Alias: "testhost", Version: "v0", Token: testToken, LocalRuns: svc})
}

func postLocalRun(h http.Handler, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/local-runs", strings.NewReader("{}"))
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLocalRunsIsOnTheSocketOnly(t *testing.T) {
	d := localRunsDaemon(t)
	if w := postLocalRun(d.SocketHandler(), ""); w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed {
		t.Fatalf("the socket has no /v1/local-runs: %d", w.Code)
	}
	if w := postLocalRun(d.TCPHandler(), "Bearer "+testToken); w.Code != http.StatusNotFound {
		t.Fatalf("a peer with the pool token reached /v1/local-runs: %d, want 404", w.Code)
	}
	if w := postLocalRun(d.TCPHandler(), ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("without the token: %d, want 401", w.Code)
	}
}

func TestWithoutTheServiceThereIsNoLocalRunsRoute(t *testing.T) {
	if w := postLocalRun(newTestDaemon(t).SocketHandler(), ""); w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", w.Code)
	}
}

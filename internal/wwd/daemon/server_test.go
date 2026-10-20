package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

const testToken = "tok-0123456789abcdef0123456789abcdef"

func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	return New(Options{HostID: "h1", Alias: "testhost", Version: "v0", Token: testToken})
}

func do(h http.Handler, method, path, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestTCPRequiresToken(t *testing.T) {
	h := newTestDaemon(t).TCPHandler()
	paths := []string{"/v1/info", "/v1/facts", "/v1/jobs", "/v1/nope", "/", "/v1/worker", "/v1/exceptions"}
	for _, m := range []string{"GET", "POST", "PUT", "DELETE"} {
		for _, p := range paths {
			for _, auth := range []string{"", "Bearer wrong", "Bearer ", "Basic " + testToken, testToken, "bearer " + testToken + "x"} {
				if w := do(h, m, p, auth); w.Code != http.StatusUnauthorized {
					t.Errorf("%s %s auth=%q: code %d, want 401", m, p, auth, w.Code)
				}
			}
		}
	}
}

func TestTCPInfoWithToken(t *testing.T) {
	h := newTestDaemon(t).TCPHandler()
	w := do(h, "GET", "/v1/info", "Bearer "+testToken)
	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	info, err := wire.Decode[wire.Info](w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if info.HostID != "h1" || info.Alias != "testhost" || info.Proto != wire.Proto || info.Labels == nil {
		t.Fatalf("info: %+v", info)
	}
	if info.Platform == "" || info.Arch == "" {
		t.Fatalf("platform/arch empty: %+v", info)
	}
}

func TestSubmissionAbsentOnTCP(t *testing.T) {
	h := newTestDaemon(t).TCPHandler()
	// Even with a valid token a submission route does not exist on TCP.
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		if w := do(h, m, "/v1/jobs", "Bearer "+testToken); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /v1/jobs on TCP: code %d, want 404/405", m, w.Code)
		}
	}
}

func TestSocketNeedsNoTokenAndHasSubmissionRoute(t *testing.T) {
	h := newTestDaemon(t).SocketHandler()
	if w := do(h, "GET", "/v1/info", ""); w.Code != 200 {
		t.Fatalf("socket info code %d", w.Code)
	}
	// Submission route exists on the socket (501 until WWD-3b wires the queue).
	if w := do(h, "POST", "/v1/jobs", ""); w.Code != http.StatusNotImplemented {
		t.Fatalf("socket POST /v1/jobs: code %d, want 501", w.Code)
	}
}

func TestListenBindsOnlyAllowedAddrs(t *testing.T) {
	d := newTestDaemon(t)
	sock := filepath.Join(shortSockDir(t), "wwd.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run, err := d.Listen(ctx, ListenSpec{Addrs: []string{"127.0.0.1"}, Port: 0, SocketPath: sock})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	for _, a := range run.TCPAddrs() {
		host, _, _ := net.SplitHostPort(a)
		if err := ValidateBindAddr(host); err != nil {
			t.Errorf("bound %s: %v", a, err)
		}
		if net.ParseIP(host).IsUnspecified() {
			t.Fatalf("wildcard bind %s", a)
		}
	}
	// token enforced through the real listener
	resp, err := http.Get("http://" + run.TCPAddrs()[0] + "/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("real listener no token: %d", resp.StatusCode)
	}
	// socket works without token
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	resp, err = c.Get("http://wwd/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil || resp.StatusCode != 200 {
		t.Fatalf("socket info: %d %v", resp.StatusCode, err)
	}
}

// Close is done once: the second call (main makes one next to the one that follows the context) does not remove a socket that a new
// process has made at the path since the first.
func TestRunningCloseRemovesTheSocketOnlyOnce(t *testing.T) {
	d := newTestDaemon(t)
	dir := shortSockDir(t)
	sock := filepath.Join(dir, "s")
	run, err := d.Listen(context.Background(), ListenSpec{Addrs: []string{"127.0.0.1"}, Port: 0, SocketPath: sock})
	if err != nil {
		t.Fatal(err)
	}
	// the servers are serving (a Serve that starts after Shutdown closes its listener late, and that unlinks the path under the test)
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	resp, err := c.Get("http://wwd/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	c.CloseIdleConnections()
	run.Close()
	if _, err := os.Lstat(sock); err == nil {
		t.Fatal("the first Close left the socket")
	}
	if err := os.WriteFile(sock, []byte("the next process's socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	run.Close() // must not touch what is there now
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("the second Close removed the new process's socket: %v", err)
	}
}

// A new process binds the path while the old one is still answering its last request (its listener is closed and the path is free):
// the old Close, when it ends, does not take the new socket away.
func TestRunningCloseDoesNotRemoveASocketBoundDuringTheShutdown(t *testing.T) {
	dir := shortSockDir(t)
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release // the last request, still being answered
	})}
	go srv.Serve(ln)
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	go c.Get("http://wwd/")
	<-started
	run := &Running{srvs: []*http.Server{srv}, sock: sock}
	closed := make(chan struct{})
	go func() { run.Close(); close(closed) }()
	for i := 0; ; i++ { // the old listener is closed when its path is free
		if _, err := os.Lstat(sock); err != nil {
			break
		}
		if i > 500 {
			t.Fatal("the old listener did not close")
		}
		time.Sleep(10 * time.Millisecond)
	}
	newLn, err := net.Listen("unix", sock) // the new process
	if err != nil {
		t.Fatal(err)
	}
	defer newLn.Close()
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("the old Close took the new process's socket: %v", err)
	}
	conn.Close()
}

// A request that is not done when the grace ends has its connection closed, which ends the context it runs under; Close returns
// then, and not before the grace (the request is given its time).
func TestRunningCloseEndsTheContextOfARequestThatOutlastsTheGrace(t *testing.T) {
	old := closeGrace
	closeGrace = 200 * time.Millisecond
	defer func() { closeGrace = old }()
	started, ended := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done() // a handler that stops when its connection goes
		close(ended)
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	go http.Get("http://" + ln.Addr().String() + "/")
	<-started
	run := &Running{srvs: []*http.Server{srv}, sock: filepath.Join(t.TempDir(), "none")}
	t0 := time.Now()
	run.Close()
	if took := time.Since(t0); took < 150*time.Millisecond {
		t.Fatalf("Close did not give the request its grace: %s", took)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the request's context was not ended by Close")
	}
}

func TestListenDoesNotDeleteRegularFile(t *testing.T) {
	d := newTestDaemon(t)
	p := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Listen(context.Background(), ListenSpec{Addrs: []string{"127.0.0.1"}, SocketPath: p}); err == nil {
		t.Fatal("must refuse a non-socket path")
	}
	if b, err := os.ReadFile(p); err != nil || string(b) != "data" {
		t.Fatalf("file damaged: %q %v", b, err)
	}
}

func TestListenRefusesLiveSocketAndReplacesStale(t *testing.T) {
	d := newTestDaemon(t)
	dir := shortSockDir(t)
	sock := filepath.Join(dir, "w.sock")
	r1, err := d.Listen(context.Background(), ListenSpec{Addrs: []string{"127.0.0.1"}, SocketPath: sock})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Listen(context.Background(), ListenSpec{Addrs: []string{"127.0.0.1"}, SocketPath: sock}); err == nil {
		t.Fatal("second instance on a live socket must fail")
	}
	r1.Close()
	// a stale socket file (nobody listening) is replaced
	ln, _ := net.Listen("unix", sock)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	r2, err := d.Listen(context.Background(), ListenSpec{Addrs: []string{"127.0.0.1"}, SocketPath: sock})
	if err != nil {
		t.Fatalf("stale socket must be replaced: %v", err)
	}
	r2.Close()
}

func TestListenFailsClosedOnAmbiguousDialError(t *testing.T) {
	d := newTestDaemon(t)
	dir := shortSockDir(t)
	sock := filepath.Join(dir, "w.sock")
	ln, _ := net.Listen("unix", sock)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	// Dial now fails with EACCES, not ECONNREFUSED: not provably stale.
	if err := os.Chmod(sock, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Listen(context.Background(), ListenSpec{Addrs: []string{"127.0.0.1"}, SocketPath: sock}); err == nil {
		t.Fatal("must fail closed when liveness is unknown")
	}
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("socket was removed: %v", err)
	}
}

func TestListenRefusesBadAddr(t *testing.T) {
	d := newTestDaemon(t)
	_, err := d.Listen(context.Background(), ListenSpec{Addrs: []string{"0.0.0.0"}, Port: 0, SocketPath: filepath.Join(t.TempDir(), "s")})
	if err == nil {
		t.Fatal("0.0.0.0 must be refused")
	}
}

package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Options configures a Daemon.
type Options struct {
	HostID, Alias, Version string
	Token                  string
	WorkerEnabled          bool
	Sampler                *Sampler // nil: the facts routes are not registered
	Peers                  []PeerAddr
	HTTPClient             *http.Client // for forwarding and polling; nil: a client with timeouts that follows no redirect
}

// Daemon holds the state both HTTP surfaces serve.
type Daemon struct {
	opts   Options
	broker *Broker
	pool   *PoolPoller
	fwd    *http.Client
}

func New(o Options) *Daemon {
	// forwarding may wait for a slow answer; polling must finish inside one interval.
	// Both carry the pool token, so neither follows a redirect.
	fwd, poll := o.HTTPClient, o.HTTPClient
	if fwd == nil {
		fwd = &http.Client{Timeout: forwardTimeout, CheckRedirect: noRedirect}
	}
	if poll == nil {
		poll = &http.Client{Timeout: peerTimeout, CheckRedirect: noRedirect}
	}
	return &Daemon{opts: o, broker: NewBroker(), pool: NewPoolPoller(o.Peers, o.Token, poll), fwd: fwd}
}

// Events is the broker the SSE streams read; the sampler and the queue publish to it.
func (d *Daemon) Events() *Broker { return d.broker }

// PublishJob announces a job state change on /v1/events (non-blocking; the queue's
// Options.Publish). Ad-hoc jobs reach the local socket only.
func (d *Daemon) PublishJob(ev wire.JobEvent) { d.broker.PublishJob(ev) }

// routes registers the routes available on both surfaces. peer is true for TCP.
func (d *Daemon) routes(mux *http.ServeMux, peer bool) {
	mux.HandleFunc("GET /v1/info", d.handleInfo)
	mux.HandleFunc("GET /v1/events", d.handleEvents(peer))
	d.factsRoutes(mux, peer)
}

// TCPHandler serves the tailnet/loopback listeners: every request, including
// unknown paths, needs the pool token. Job submission is not registered here.
func (d *Daemon) TCPHandler() http.Handler {
	mux := http.NewServeMux()
	d.routes(mux, true)
	return d.requireToken(rejectHostParam(mux))
}

// SocketHandler serves the local socket (same user, trusted, no token). It
// additionally carries the routes that exist only locally.
func (d *Daemon) SocketHandler() http.Handler {
	mux := http.NewServeMux()
	d.routes(mux, false)
	d.poolRoutes(mux)
	// Submission is local only (spec §4.4); the queue arrives in WWD-3b.
	mux.HandleFunc("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "job queue not implemented", http.StatusNotImplemented)
	})
	return mux
}

func (d *Daemon) requireToken(next http.Handler) http.Handler {
	want := []byte(d.opts.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || len(want) == 0 || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (d *Daemon) info() wire.Info {
	return wire.Info{
		HostID: d.opts.HostID, Alias: d.opts.Alias, Version: d.opts.Version,
		Proto: wire.Proto, Platform: runtime.GOOS, Arch: runtime.GOARCH,
		Worker: wire.InfoWorker{Enabled: d.opts.WorkerEnabled},
		Labels: []string{"os:" + runtime.GOOS, "arch:" + runtime.GOARCH},
	}
}

func (d *Daemon) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.info())
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ListenSpec says where to listen. Port 0 picks a free port (tests).
type ListenSpec struct {
	Addrs      []string
	Port       int
	SocketPath string
}

// Running is a started daemon.
type Running struct {
	srvs  []*http.Server
	tcp   []string
	sock  string
	errCh chan error
}

// Listen validates every address, binds the TCP listeners and the socket, and
// starts serving. Nothing is bound if any address is refused.
func (d *Daemon) Listen(ctx context.Context, spec ListenSpec) (*Running, error) {
	if len(spec.Addrs) == 0 {
		return nil, errors.New("no bind addresses")
	}
	for _, a := range spec.Addrs {
		if err := ValidateBindAddr(a); err != nil {
			return nil, err
		}
	}
	r := &Running{errCh: make(chan error, len(spec.Addrs)+1), sock: spec.SocketPath}
	var lns []net.Listener
	closeAll := func() {
		for _, l := range lns {
			l.Close()
		}
	}
	for _, a := range spec.Addrs {
		ln, err := net.Listen("tcp", net.JoinHostPort(a, strconv.Itoa(spec.Port)))
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("listen %s: %w", a, err)
		}
		lns = append(lns, ln)
		r.tcp = append(r.tcp, ln.Addr().String())
	}
	if err := os.MkdirAll(dirOf(spec.SocketPath), 0o700); err != nil {
		closeAll()
		return nil, err
	}
	if err := clearStaleSocket(spec.SocketPath); err != nil {
		closeAll()
		return nil, err
	}
	uln, err := net.Listen("unix", spec.SocketPath)
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("listen %s: %w", spec.SocketPath, err)
	}
	if err := os.Chmod(spec.SocketPath, 0o600); err != nil {
		closeAll()
		uln.Close()
		return nil, err
	}
	serve := func(ln net.Listener, h http.Handler) {
		s := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
		r.srvs = append(r.srvs, s)
		go func() {
			if err := s.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				r.errCh <- err
			}
		}()
	}
	for _, ln := range lns {
		serve(ln, d.TCPHandler())
	}
	serve(uln, d.SocketHandler())
	go func() { <-ctx.Done(); r.Close() }()
	return r, nil
}

// clearStaleSocket removes path only when it is a Unix socket nobody listens
// on. A regular file or a live socket (another wwd) is an error, never deleted.
func clearStaleSocket(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode().Type()&os.ModeSocket == 0 {
		return fmt.Errorf("socket path %s exists and is not a socket", path)
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	switch {
	case err == nil:
		c.Close()
		return fmt.Errorf("socket %s is in use (another wwd running?)", path)
	case errors.Is(err, syscall.ECONNREFUSED):
		return os.Remove(path) // nobody listens: provably stale
	case errors.Is(err, syscall.ENOENT):
		return nil
	default: // timeout, EACCES, EMFILE…: liveness unknown, fail closed
		return fmt.Errorf("socket %s: cannot tell whether it is in use: %w", path, err)
	}
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "."
}

// TCPAddrs are the bound TCP addresses (host:port).
func (r *Running) TCPAddrs() []string { return r.tcp }

// Err reports a serve failure.
func (r *Running) Err() <-chan error { return r.errCh }

// Close stops both surfaces and removes the socket file.
func (r *Running) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, s := range r.srvs {
		_ = s.Shutdown(ctx)
	}
	_ = os.Remove(r.sock)
	return nil
}

package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"math"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/wwd/fault"
	"github.com/wake/purdex/internal/wwd/jobsapi"
	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/localruns"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// Options configures a Daemon.
type Options struct {
	HostID, Alias, Version string
	Token                  string
	WorkerEnabled          bool
	// ExplicitLabelsOnly is `[worker] explicit_labels_only`, announced in /v1/info so that the pool can tell which hosts take a job.
	ExplicitLabelsOnly bool
	// SpeedFactor is `[worker] speed_factor`, announced in /v1/info (1 when it is not set) so that a slower host can leave a job to this one (#3904).
	SpeedFactor   float64
	Fault         *fault.Hooks // test hooks (nil: none); /v1/info declares them
	Sampler       *Sampler     // nil: the facts routes are not registered
	Peers         []PeerAddr
	PeersLastFile string       // where the last known info of the peers is kept across restarts (pool_last.go); "": not kept
	HTTPClient    *http.Client // for forwarding and polling; nil: a client with timeouts that follows no redirect
	State         *HostState   // worker settings and exceptions; nil: the worker/exception routes are not registered
	Broker        *Broker      // the event broker (shared with State); nil: a new one
	// Queue is this host's job queue (one per process); the job routes serve from it. nil: no queue.
	Queue *queue.Queue
	// LocalRuns serves POST /v1/local-runs on the socket (the fast lane, spec 5.2); main builds it over Queue and feeds it the
	// queue's job events. nil: the route is not registered.
	LocalRuns *localruns.Service
	// ClaimRoutes registers the claim routes (heartbeat, result, log) on the socket mux. POST /v1/local-runs is registered only
	// when this is set too. nil: neither exists.
	ClaimRoutes func(mux *http.ServeMux)
	// RepoSource lists the repos this host has a prepared clone of (they become
	// repo:<name> labels). Called once per facts record: cheap, never blocking.
	// nil: no repo labels.
	RepoSource func() []string
	// Earned is the store of this host's earned and quarantined task labels (spec §7): the earned ones join the advertised labels, a
	// quarantined one does not. nil: none. GET /v1/earned-labels serves from it on the local socket.
	Earned EarnedStore
	// ReferenceTasks lists the tasks whose qualify.reference is a host, from the manifests of this host's repos; this host holds the
	// task label of those that name it, without qualifying, when it has what they need. Called once per facts record and per
	// RefreshLabels: cheap, never blocking. nil: none.
	ReferenceTasks func() []labels.RefTask
	// ResultCache is the result cache a cacheable submission is answered from (spec §4.5). nil: no submission is.
	ResultCache ResultCache
	// Observer is this host's observation records (spec 8.4); the observation routes serve from it. nil: they are not registered.
	Observer *Observer
	// Alerts is the store of this host's alerts (spec §13.3); GET /v1/alerts serves from it. nil: the route is not registered.
	Alerts AlertStore
	// Usage is the store of finished jobs' usage; GET /v1/usage serves from it on the local socket. nil: the route is not registered.
	Usage UsageStore
	// Labels and CacheDrop are the earned-label table and the result cache the quarantine acts on (spec §7): a disagreement read from an
	// origin quarantines the label here, and pdx wwd quarantine|release change it by hand. nil: neither exists.
	Labels    LabelKeeper
	CacheDrop CacheDropper
	// Qualifier is the qualification coordinator (spec §7): POST /v1/qualify and GET /v1/qualify on the socket serve from it. nil: no such routes.
	Qualifier Qualifier
	// Now is the clock the observation and usage routes take their default periods from. nil: time.Now.
	Now func() time.Time
}

// Daemon holds the state both HTTP surfaces serve.
type Daemon struct {
	opts   Options
	broker *Broker
	pool   *PoolPoller
	fwd    *http.Client
	labels labels.Tracker

	// partnerAt is when the local Purdex daemon last read this host's alerts on the socket (unix nanoseconds; the daemon's own start until
	// it does): the partner check of the silence rules (spec §13.3) says the daemon is down when this is old.
	partnerAt atomic.Int64

	// labelsMu serializes choosing the record, collecting the labels, updating the tracker and announcing: OnFacts and RefreshLabels
	// never interleave. It guards lastFacts and lastEarned.
	labelsMu   sync.Mutex
	lastFacts  *wire.Facts // the newest record, for RefreshLabels
	lastEarned []string    // the earned task labels the store last gave

	quar quarantineState // where the quarantine pass has read to (memory only)
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
	b := o.Broker
	if b == nil {
		b = NewBroker()
	}
	if o.Sampler != nil {
		b.SetShape(o.Sampler.Shaped)
	}
	pool := NewPoolPoller(o.Peers, o.Token, poll)
	if o.PeersLastFile != "" {
		pool.UseLastKnownFile(o.PeersLastFile)
	}
	d := &Daemon{opts: o, broker: b, pool: pool, fwd: fwd}
	d.partnerAt.Store(d.clock().UnixNano())
	return d
}

func (d *Daemon) clock() time.Time {
	if d.opts.Now != nil {
		return d.opts.Now()
	}
	return time.Now()
}

// PartnerSeen is when the local Purdex daemon last read this host's own alerts on the socket (the daemon's start before the first read).
// The deliverer of every Purdex daemon reads every host's list every 30 s, this host's included, so a long silence here means the daemon is
// down or has stopped reading.
func (d *Daemon) PartnerSeen() time.Time { return time.Unix(0, d.partnerAt.Load()) }

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
	d.workerRoutes(mux, peer)
	d.qualificationRoutes(mux, peer)
	d.jobRoutes(mux, peer)
	d.observeRoutes(mux, peer)
	d.alertsRoutes(mux, peer)
}

// jobRoutes registers the job reads and cancel (spec 4.4) on both surfaces, from the process's one queue; a peer's view leaves out the
// ad-hoc and fast-lane jobs. On the socket ?host=<alias> forwards the call to that host like the worker routes. Submission is not here:
// it exists on the socket only (SocketHandler).
func (d *Daemon) jobRoutes(mux *http.ServeMux, peer bool) {
	if d.opts.Queue == nil {
		return
	}
	a := jobsapi.New(d.opts.Queue).WithFault(d.opts.Fault) // the log upload a remote worker makes arrives here (test hook H1)
	mux.HandleFunc("GET /v1/jobs", d.viaHost(peer, a.List(peer)))
	mux.HandleFunc("GET /v1/jobs/{id}", d.viaHost(peer, a.Get(peer)))
	mux.HandleFunc("GET /v1/jobs/{id}/log", d.viaHost(peer, a.Log(peer)))
	mux.HandleFunc("POST /v1/jobs/{id}/cancel", d.viaHost(peer, a.Cancel(peer)))
	// The disagreements the owner of a label reads from the origins it reads (spec §7): read-only, and not forwarded (a reader asks each
	// origin itself; its subject= is the host the answer concerns, not a host to forward to).
	mux.HandleFunc("GET /v1/outcomes/disagreements", a.Disagreements())
	if peer {
		// What a worker on another host does with a job (spec 4.3), and the snapshot it fetches for a claim it holds. On the socket the
		// worker routes are the caller's to register (Options.ClaimRoutes), since they open the fast lane too.
		a.WorkerRoutes(mux, true)
		mux.Handle("GET /v1/jobs/{id}/snapshot", snapshot.NewHandler(jobsapi.SnapshotSource{Q: d.opts.Queue}))
	}
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
	d.usageRoutes(mux)
	d.earnedRoutes(mux)
	d.quarantineRoutes(mux)
	d.qualifyRoutes(mux)
	if d.opts.ClaimRoutes != nil {
		d.opts.ClaimRoutes(mux)
	}
	// The fast lane hands out a claim its runner heartbeats and reports with, so it opens only together with the claim routes: a
	// daemon without them answers 404 and the caller runs the task locally without recording it.
	if d.opts.LocalRuns != nil && d.opts.ClaimRoutes != nil {
		mux.HandleFunc("POST /v1/local-runs", d.opts.LocalRuns.Handler()) // socket only: a peer never records a run here
	}
	// Submission is local only (spec §4.4): a job is always submitted on its own host, so the TCP surface has no such route.
	if d.opts.Queue != nil {
		sub := jobsapi.New(d.opts.Queue)
		if r := d.Reuser(); r != nil { // not WithReuse(d.Reuser()): a nil *reuser in the interface would not be nil
			sub = sub.WithReuse(r)
		}
		mux.HandleFunc("POST /v1/jobs", sub.Submit())
		// The manual place of a queued job is set on the socket only (spec 4.6): the TCP surface has no such route, and a ?host= is not
		// forwarded (the person moves a job on the host it is queued on).
		mux.HandleFunc("POST /v1/jobs/{id}/position", d.localOnly(jobsapi.New(d.opts.Queue).Position()))
	} else {
		mux.HandleFunc("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "job queue not implemented", http.StatusNotImplemented)
		})
	}
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

// Labels is this host's labels and toolchain fingerprint now (what GET /v1/info says): the build's own arch and os before the first
// facts record. The slices and the map are copies.
func (d *Daemon) Labels() (labels []string, fingerprint map[string]string) {
	i := d.info()
	return append([]string(nil), i.Labels...), maps.Clone(i.Fingerprint)
}

func (d *Daemon) info() wire.Info {
	enabled := d.opts.WorkerEnabled
	if d.opts.State != nil {
		enabled = d.opts.State.Worker().Enabled // the switch can be flipped at run time
	}
	speed := d.opts.SpeedFactor
	if math.IsNaN(speed) || math.IsInf(speed, 0) || speed <= 0 {
		speed = 1 // not set is the reference host's speed, as pick.Rank reads it
	}
	lbl, fp := d.labels.Current()
	if len(lbl) == 0 { // no facts record yet: what the build says
		lbl = []string{"arch:" + runtime.GOARCH, "os:" + runtime.GOOS}
	}
	return wire.Info{
		Fault:  d.opts.Fault.Declared(),
		HostID: d.opts.HostID, Alias: d.opts.Alias, Version: d.opts.Version,
		Proto: wire.Proto, Platform: runtime.GOOS, Arch: runtime.GOARCH,
		Worker: wire.InfoWorker{Enabled: enabled, ExplicitLabelsOnly: d.opts.ExplicitLabelsOnly, SpeedFactor: speed},
		Labels: lbl, Fingerprint: fp,
	}
}

// OnFacts takes a new facts record (the sampler's OnFacts): it announces the
// record, refreshes the labels and the fingerprint, and announces the labels
// only when they really changed.
func (d *Daemon) OnFacts(f wire.Facts) {
	d.labelsMu.Lock() // the record's event goes out under the same lock as its labels, after any refresh that is already running
	defer d.labelsMu.Unlock()
	d.broker.PublishFacts(f)
	d.lastFacts = &f
	d.updateLabels(f)
}

// RefreshLabels recomputes the labels from the newest facts record and announces them if they changed: for when the earned labels
// changed (a label earned, quarantined or revoked) between two facts records. Before the first record there is nothing to do. It runs
// under the same lock as OnFacts, so a refresh that started on an older record can never land after a newer record's update.
func (d *Daemon) RefreshLabels() {
	d.labelsMu.Lock()
	defer d.labelsMu.Unlock()
	if d.lastFacts != nil {
		d.updateLabels(*d.lastFacts)
	}
}

// updateLabels is the labels of a record plus what no record says: repo:<name> for each prepared clone, host:<alias> (it narrows who
// may claim a job and grants nothing, spec §7), the task labels held as the reference, and the earned task labels. The caller holds
// labelsMu.
func (d *Daemon) updateLabels(f wire.Facts) {
	var extra []string
	if d.opts.RepoSource != nil {
		for _, r := range d.opts.RepoSource() {
			if r != "" {
				extra = append(extra, "repo:"+r)
			}
		}
	}
	if d.opts.Alias != "" {
		extra = append(extra, "host:"+d.opts.Alias)
	}
	if d.opts.ReferenceTasks != nil {
		base, _ := labels.FromFacts(f)
		extra = append(extra, labels.ReferenceLabels(d.opts.Alias, append(base, extra...), d.opts.ReferenceTasks())...)
	}
	extra = append(extra, d.earnedLabels()...)
	if d.labels.Update(f, extra...) {
		l, _ := d.labels.Current()
		d.broker.PublishLabels(wire.LabelsEvent{Host: d.opts.Alias, Labels: l})
	}
}

// earnedLabels is the task labels this host advertises because it earned them; a quarantined one is left out, and so is a label that
// is not a task label (a damaged row never grants a capability). A store that cannot be read at all keeps the last set it gave, so a
// passing read error does not revoke every label and announce it; a store that has never answered advertises none (nothing is invented).
// A table with damaged rows still answers with the rest. The caller holds labelsMu.
func (d *Daemon) earnedLabels() []string {
	if d.opts.Earned == nil {
		return nil
	}
	rows, err := d.opts.Earned.EarnedLabels()
	var skipped *store.SkippedRowsError
	if err != nil && !errors.As(err, &skipped) {
		log.Printf("wwd: reading the earned labels: %v (keeping the last set)", err)
		return d.lastEarned
	}
	out := []string{}
	for _, e := range rows {
		if e.State == wire.EarnedLabelEarned && wire.ValidTaskLabel(e.Label) {
			out = append(out, e.Label)
		}
	}
	d.lastEarned = out
	return out
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
	once  sync.Once
	uln   net.Listener // the Unix listener: closing it unlinks the socket file
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
	r.uln = uln
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

// closeGrace is how long Close lets the requests in flight finish.
var closeGrace = 3 * time.Second

// Close stops both surfaces (closing the Unix listener removes the socket file). It does this once: a second call (the shutdown that follows the context, and
// the one main makes to be sure the servers are quiet) waits for the first to finish and does nothing more, so it cannot remove the
// socket a new process has made since.
func (r *Running) Close() error {
	r.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), closeGrace)
		defer cancel()
		// The Unix listener is closed first, by us, whether or not its server has started to serve: that unlinks the socket file, once and
		// before the requests in flight are waited for, so a new process may bind the path from then on and nothing here takes it away.
		if r.uln != nil {
			_ = r.uln.Close()
		}
		for _, s := range r.srvs {
			if err := s.Shutdown(ctx); err != nil {
				// the requests in flight did not finish in time: their connections are closed, which ends the contexts they run under (a handler
				// that does not look at its context can still go on for a while; there is no bound on that)
				_ = s.Close()
			}
		}
	})
	return nil
}

// localOnly refuses a request that names another host: the route acts on this host's queue and is not forwarded.
func (d *Daemon) localOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("host") { // present at all: an empty or a repeated one is no more local than a named one
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this route acts on the local host only"})
			return
		}
		h(w, r)
	}
}

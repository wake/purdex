package fleetmod

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"time"

	leaf "github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/module/hostconfig"
)

// This file is the aggregator: the newest reading of every host, kept in memory only (spec §2.5: nothing a sample says is
// written to a database or to the log), and Measure, the measuring side the module drives: sessions, the 1 s tick, the push.

const (
	tickEvery     = time.Second
	ownStaleAfter = offlineAfter // the own host with no new reading for this long reads offline, like a remote one
	// pollConfigEvery is how often the host config is re-read when there is no cheap revision to look at.
	pollConfigEvery  = 30 * time.Second
	retryConfigAfter = 30 * time.Second
)

// Kinds of host.
const (
	KindDaemon = "daemon"
	KindProbe  = "probe"
)

// Deps are the seams of a Measure; the module fills them from the registry, tests with fakes.
type Deps struct {
	Home  string
	Clock timeSource // nil = the real one
	Local leaf.LocalSource
	Role  leaf.RoleView
	// Probe and Fleet read the host config rows. Both re-read ~/.ssh/config on every call, so they run only when the config's
	// revision moved (Revisions), never per tick.
	Probe func() ([]hostconfig.ProbeHost, int64, error)
	Fleet func() (hostconfig.FleetConfig, int64, error)
	// Revisions is the cheap look at the two rows' revisions (a point read); nil = re-read every pollConfigEvery.
	Revisions  func() (probe, fleet int64, err error)
	SelfHostID func() string
	PeerHostID func(alias string) string // the daemon host id of a peer alias, "" when unknown
	Events     Pusher
	// Disk is the own host's disk figures, as of the fleet's mount; nil = leaf.LocalDisk (percentage only).
	Disk func() (usedPct, freeGB float64, ok bool)
	Logf func(format string, args ...any)
	// Session carries the session tunables and seams (LookPath, Valid, timings); Sink, Home, Clock, Logf and Limit are set here.
	Session sessionCfg
}

// FastView is the one-second part of a host (spec §6).
type FastView struct {
	At         time.Time `json:"at"`
	CPU        float64   `json:"cpu"`
	Mem        float64   `json:"mem"`
	Level      int       `json:"level"`
	PageRate   float64   `json:"page_rate"`
	Disk       float64   `json:"disk"`
	DiskFreeGB float64   `json:"disk_free_gb"`
	Load1      float64   `json:"load1"`
	NCPU       int       `json:"ncpu"`
	Zone       string    `json:"zone"`
	Load       float64   `json:"load"`
}

// SlowView is the five-second part. It carries the queue summary, never the gate's own status.
type SlowView struct {
	At     time.Time   `json:"at"`
	TopCPU []leaf.Proc `json:"top_cpu"`
	TopMem []leaf.Proc `json:"top_mem"`
	Queue  *leaf.Queue `json:"queue,omitempty"`
}

// HostJSON is a host as GET /api/fleet answers it.
type HostJSON struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Alias      string          `json:"alias"`
	Name       string          `json:"name,omitempty"`
	Icon       string          `json:"icon,omitempty"`
	Colors     json.RawMessage `json:"colors,omitempty"`
	IconWeight string          `json:"icon_weight,omitempty"`
	OS         string          `json:"os,omitempty"`
	Status     string          `json:"status"`
	Error      string          `json:"error,omitempty"`
	LastOKAt   *time.Time      `json:"last_ok_at,omitempty"`
	Fast       *FastView       `json:"fast,omitempty"`
	Slow       *SlowView       `json:"slow,omitempty"`
}

// PushHost is a host in a fleet.changed push: what moves every second; the look and the alias come from GET /api/fleet.
type PushHost struct {
	ID       string     `json:"id"`
	Status   string     `json:"status"`
	Error    *string    `json:"error"`          // always present: null, or one of the codes
	LastOKAt *time.Time `json:"last_ok_at"`     // always present: null before the first good reading
	Fast     *FastView  `json:"fast"`           // always present: null before the first reading
	Slow     *SlowView  `json:"slow,omitempty"` // only in the push right after it changed
}

type hostRow struct {
	key                    string
	kind                   string
	fleetAlias             string // a daemon host's alias in the fleet order (peer rows resolve their id from it)
	staticID               string
	sshAlias               string // "" = measured locally
	name, icon, iconWeight string
	colors                 json.RawMessage

	os       string
	status   leaf.Status
	code     leaf.Code
	lastOK   time.Time
	fast     *FastView
	lastFast leaf.LocalFast
	slow     *SlowView
	slowSeq  uint64
	zone     leaf.HostZone

	// own host only
	ownAt   time.Time // At of the newest local reading seen
	ownSlow time.Time
	ownSeen time.Time // when it was seen (receive time)
}

// Measure is the measuring side: while this daemon is primary it reads its own host from the LocalSource, keeps one SSH
// session per other host, and pushes the fleet once a second.
type Measure struct {
	d     Deps
	clock timeSource
	limit *hourlyLimiter

	mu         sync.Mutex
	hosts      map[string]*hostRow
	order      []string
	newestAt   time.Time
	haveNewest bool
	pushedSlow map[string]uint64

	life    sync.Mutex // activate / deactivate
	active  bool
	stopped bool // Stop was called: nothing is activated any more
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	base    context.Context

	// touched only by the goroutine that runs reconcile (activate, then the tick loop)
	sessions   map[string]*liveSession
	haveRev    bool
	probeRev   int64
	fleetRev   int64
	nextConfig time.Time
}

type liveSession struct {
	alias string
	s     *session
}

func NewMeasure(d Deps) *Measure {
	m := &Measure{d: d, clock: d.Clock, limit: &hourlyLimiter{}}
	if m.clock == nil {
		m.clock = realClock{}
	}
	if m.d.Logf == nil {
		m.d.Logf = func(string, ...any) {}
	}
	m.resetLocked()
	return m
}

func (m *Measure) resetLocked() {
	m.hosts = map[string]*hostRow{}
	m.order = nil
	m.haveNewest = false
	m.newestAt = time.Time{}
	m.pushedSlow = map[string]uint64{}
	m.sessions = map[string]*liveSession{}
	m.haveRev = false
}

// Start follows the role: primary starts the measuring, standby and off stop it.
func (m *Measure) Start(ctx context.Context) error {
	m.life.Lock()
	m.base = ctx
	m.stopped = false
	m.life.Unlock()
	if m.d.Role == nil {
		return nil
	}
	m.d.Role.OnChange(m.onRole)
	m.onRole(m.d.Role.State())
	return nil
}

// Stop ends the measuring and waits for every session.
func (m *Measure) Stop() {
	m.life.Lock()
	defer m.life.Unlock()
	m.stopped = true
	m.deactivateLocked()
}

func (m *Measure) onRole(st leaf.RoleState) {
	m.life.Lock()
	defer m.life.Unlock()
	if m.stopped {
		return
	}
	switch {
	case st.Role == leaf.RolePrimary && !m.active:
		m.activateLocked()
	case st.Role != leaf.RolePrimary && m.active:
		m.deactivateLocked()
	}
}

func (m *Measure) activateLocked() {
	if m.stopped {
		return
	}
	base := m.base
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	m.cancel, m.active = cancel, true
	m.mu.Lock()
	m.resetLocked()
	m.mu.Unlock()
	m.d.Local.Start()
	m.reconcile(ctx, true)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			t := m.clock.NewTimer(tickEvery)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C():
				m.tick(ctx)
			}
		}
	}()
}

// deactivateLocked stops the tick, then every session (each waited for), then the local source. A following activate starts
// from no readings at all.
func (m *Measure) deactivateLocked() {
	if !m.active {
		return
	}
	m.cancel()
	m.wg.Wait()
	var sw sync.WaitGroup
	for _, ls := range m.sessions {
		sw.Add(1)
		go func(s *session) { defer sw.Done(); s.stop() }(ls.s)
	}
	sw.Wait()
	m.d.Local.Stop()
	m.active = false
	m.mu.Lock()
	m.resetLocked()
	m.mu.Unlock()
}

// NewestAge is the age of the newest reading of any host, the own one or one over SSH. Once there has been any reading it
// answers (age, true) for good, whatever fails afterwards, and keeps growing: only a primary that has never read anything
// answers false (the role engine reads that as "no news" for its first 60 s). A restart of the measuring starts over.
func (m *Measure) NewestAge() (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.haveNewest {
		return 0, false
	}
	return max(m.clock.Now().Sub(m.newestAt), 0), true
}

func (m *Measure) noteReadingLocked(now time.Time) {
	m.newestAt, m.haveNewest = now, true
}

// ---- configuration ----

type desired struct {
	key, kind, fleetAlias, staticID, ssh string
	name, icon, weight                   string
	colors                               json.RawMessage
}

// reconcile brings the hosts and their sessions in line with the config. The config readers re-read ~/.ssh/config, so this
// runs only when a revision moved (or on the slow poll when there is no revision to look at, or after a failed read).
func (m *Measure) reconcile(ctx context.Context, force bool) {
	now := m.clock.Now()
	if !force {
		if m.d.Revisions != nil {
			p, f, err := m.d.Revisions()
			if err == nil && m.haveRev && p == m.probeRev && f == m.fleetRev {
				return
			}
			if err != nil && now.Before(m.nextConfig) {
				return
			}
		} else if now.Before(m.nextConfig) {
			return
		}
	}
	m.nextConfig = now.Add(pollConfigEvery)

	st := m.d.Role.State()
	var fc hostconfig.FleetConfig
	var probes []hostconfig.ProbeHost
	var prevRev, fleetRev int64
	var err error
	if m.d.Fleet != nil {
		if fc, fleetRev, err = m.d.Fleet(); err != nil {
			m.d.Logf("[fleet] reading the fleet row: %v", err)
			m.nextConfig = now.Add(retryConfigAfter)
			return
		}
	}
	if m.d.Probe != nil {
		if probes, prevRev, err = m.d.Probe(); err != nil {
			m.d.Logf("[fleet] reading the probe hosts: %v", err)
			m.nextConfig = now.Add(retryConfigAfter)
			return
		}
	}
	m.probeRev, m.fleetRev, m.haveRev = prevRev, fleetRev, true

	var want []desired
	want = append(want, desired{key: "self", kind: KindDaemon, fleetAlias: st.Self, staticID: m.d.SelfHostID()})
	if fc.PeerSSHAlias != "" {
		for _, a := range fc.Order {
			if !strings.EqualFold(a, st.Self) {
				want = append(want, desired{key: "peer", kind: KindDaemon, fleetAlias: a, ssh: fc.PeerSSHAlias})
				break
			}
		}
	}
	for _, p := range probes {
		want = append(want, desired{key: "probe:" + p.ID, kind: KindProbe, fleetAlias: p.SSHAlias, staticID: p.ID, ssh: p.SSHAlias,
			name: p.Name, icon: p.Icon, weight: p.IconWeight, colors: p.Colors})
	}

	// Decide under the lock, stop sessions outside it (a stopping session's sink call takes the lock).
	var stop []*session
	var start []desired
	m.mu.Lock()
	keep := map[string]bool{}
	for _, w := range want {
		keep[w.key] = true
	}
	for key, ls := range m.sessions {
		w, ok := find(want, key)
		if !ok || w.ssh != ls.alias {
			stop = append(stop, ls.s)
			delete(m.sessions, key)
		}
	}
	for key := range m.hosts {
		if !keep[key] {
			delete(m.hosts, key)
		}
	}
	m.order = m.order[:0]
	for _, w := range want {
		h := m.hosts[w.key]
		if h == nil {
			h = &hostRow{key: w.key, status: leaf.StatusOffline}
			m.hosts[w.key] = h
		}
		h.kind, h.fleetAlias, h.staticID, h.sshAlias = w.kind, w.fleetAlias, w.staticID, w.ssh
		h.name, h.icon, h.iconWeight, h.colors = w.name, w.icon, w.weight, w.colors
		m.order = append(m.order, w.key)
		if w.ssh != "" {
			if _, running := m.sessions[w.key]; !running {
				start = append(start, w)
			}
		}
	}
	m.mu.Unlock()

	// Cancel all first, then wait side by side (like deactivateLocked): the tick is held for one grace, not one per host.
	var sw sync.WaitGroup
	for _, s := range stop {
		sw.Add(1)
		go func(s *session) { defer sw.Done(); s.stop() }(s)
	}
	sw.Wait()
	for _, w := range start {
		m.mu.Lock()
		h := m.hosts[w.key]
		h.zone.Reset()
		if h.fast != nil {
			h.fast.Zone = "unknown" // like rowSink.State: no zone shown until the new session reads
		}
		m.mu.Unlock()
		cfg := m.d.Session
		cfg.Home, cfg.Clock, cfg.Logf, cfg.Limit = m.d.Home, m.clock, m.d.Logf, m.limit
		cfg.Sink = &rowSink{m: m, h: h}
		m.sessions[w.key] = &liveSession{alias: w.ssh, s: startSession(ctx, cfg, w.ssh)}
	}
}

func find(ds []desired, key string) (desired, bool) {
	for _, d := range ds {
		if d.key == key {
			return d, true
		}
	}
	return desired{}, false
}

// ---- readings ----

// rowSink is the sink of one remote host's session.
type rowSink struct {
	m *Measure
	h *hostRow
}

func (r *rowSink) Reading(f leaf.Fast, s *leaf.Slow) {
	m := r.m
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applyFastLocked(r.h, f, f.At)
	if s != nil {
		m.applySlowLocked(r.h, s.TopCPU, s.TopMem, s.Gate, s.At)
	}
	r.h.os, r.h.status, r.h.code, r.h.lastOK = f.OS, leaf.StatusOK, leaf.CodeNone, f.At
	m.noteReadingLocked(m.clock.Now())
}

func (r *rowSink) State(st leaf.Status, code leaf.Code, _ time.Time) {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	h := r.h
	h.status, h.code = st, code
	if st != leaf.StatusOK {
		h.zone.Reset()
		if h.fast != nil {
			h.fast.Zone = "unknown"
		}
	}
}

func (m *Measure) applyFastLocked(h *hostRow, f leaf.Fast, at time.Time) {
	z := h.zone.Update(at, f.LocalFast, f.DiskPct, true)
	h.lastFast = f.LocalFast
	h.fast = &FastView{At: at, CPU: f.CPU, Mem: f.Mem, Level: f.Level, PageRate: f.PageRate, Disk: f.DiskPct,
		DiskFreeGB: f.DiskFreeGB, Load1: f.Load1, NCPU: f.NCPU, Zone: z.String(),
		Load: leaf.Composite(f.LocalFast, f.DiskPct, true)}
}

func (m *Measure) applySlowLocked(h *hostRow, topCPU, topMem []leaf.Proc, g *gate.Status, at time.Time) {
	v := &SlowView{At: at, TopCPU: orEmpty(topCPU), TopMem: orEmpty(topMem)}
	if g != nil && h.kind == KindDaemon {
		q := leaf.QueueSummary(*g, h.lastFast, at)
		v.Queue = &q
	}
	h.slow = v
	h.slowSeq++
}

func orEmpty(p []leaf.Proc) []leaf.Proc {
	if p == nil {
		return []leaf.Proc{}
	}
	return p
}

// localGOOS is runtime.GOOS; tests pin it.
var localGOOS = runtime.GOOS

// localOS is the own os in the words a remote frame uses (leaf.ParseFast): "Darwin" or "Linux", "" for anything else.
func localOS() string {
	switch localGOOS {
	case "darwin":
		return "Darwin"
	case "linux":
		return "Linux"
	}
	return ""
}

// readLocal takes the newest reading of the own host from the LocalSource (under its own lock). now is the receive time.
func (m *Measure) readLocal(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hosts["self"]
	if h == nil {
		return
	}
	if f, ok := m.d.Local.Fast(); ok && f.At.After(h.ownAt) {
		h.ownAt, h.ownSeen = f.At, now
		h.os = localOS()
		pct, free, dok := m.disk()
		lf := leaf.Fast{LocalFast: f, DiskPct: pct, DiskFreeGB: free, RateKnown: true, LevelKnown: f.Level > 0}
		m.applyFastLocked(h, lf, now)
		if !dok && h.fast != nil {
			h.fast.Disk, h.fast.DiskFreeGB = 0, 0
		}
		h.status, h.code, h.lastOK = leaf.StatusOK, leaf.CodeNone, now
		m.noteReadingLocked(now)
	}
	if s, ok := m.d.Local.Slow(); ok && s.At.After(h.ownSlow) {
		h.ownSlow = s.At
		m.applySlowLocked(h, s.TopCPU, s.TopMem, s.Gate, now)
	}
	// No new reading for a while: the own host reads offline too (it keeps the last values), never ok on an old figure.
	if !h.ownSeen.IsZero() && now.Sub(h.ownSeen) >= ownStaleAfter && h.status == leaf.StatusOK {
		h.status, h.code = leaf.StatusOffline, leaf.CodeTimeout
		h.zone.Reset()
		if h.fast != nil {
			h.fast.Zone = "unknown"
		}
	}
}

func (m *Measure) disk() (pct, free float64, ok bool) {
	if m.d.Disk != nil {
		return m.d.Disk()
	}
	pct, ok = leaf.LocalDisk()
	return pct, 0, ok
}

// tick is the 1 s step of the primary: the config if it changed, the own host, then the push.
func (m *Measure) tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	m.reconcile(ctx, false)
	m.readLocal(m.clock.Now())
	m.push()
}

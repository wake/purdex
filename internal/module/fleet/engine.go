package fleetmod

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/fleet"
	ipeers "github.com/wake/purdex/internal/peers"
)

// Timing of the role engine (spec §4).
const (
	HeartbeatEvery = 30 * time.Second
	// AliveAge: the primary is alive when its newest reading is at most this old.
	AliveAge = 10 * time.Second
	// orderWarnEvery bounds the order-mismatch warning.
	orderWarnEvery = time.Hour
)

// callTimeout is max(5 s, the peers inter-daemon timeout) (spec §4 Heartbeat).
func callTimeout() time.Duration {
	if ipeers.InterDaemonTimeout > 5*time.Second {
		return ipeers.InterDaemonTimeout
	}
	return 5 * time.Second
}

// Settings is what the engine needs of the fleet config and this daemon's identity.
type Settings struct {
	Order   []string
	Enabled bool
	Self    string // Cfg.Peers.Alias
}

// SettingsSource hands the engine its settings. Rev is cheap (one revision read); Read may be expensive (it re-reads the SSH
// config), so the engine calls it only when Rev moved.
type SettingsSource interface {
	Rev() (int64, error)
	Read() (Settings, error)
}

// RoleRequest is the caller's side of a heartbeat: who it is and what it believes.
type RoleRequest struct {
	FromAlias string
	Role      string // primary | standby | off | starting
	Term      int64
	Order     []string // the caller's fleet order
}

// RoleAnswer is the callee's reply (spec §4).
type RoleAnswer struct {
	HostID      string   `json:"host_id"`
	Alias       string   `json:"alias"`
	Role        string   `json:"role"`
	Term        int64    `json:"term"`
	Order       []string `json:"order"`
	NewestAgeMS *int64   `json:"newest_age_ms"`
	// PushReady says whether the callee's push module is ready to send a phone push. Absent from an older daemon's answer, and from an
	// engine nothing supplies it to: a reader takes absent as "not reported", never as false. A caller ignores fields it does not know,
	// so adding it changes no existing pair.
	PushReady *bool `json:"push_ready,omitempty"`
}

// Peer reaches the other daemon. An error means unreachable (or refused).
type Peer interface {
	Role(ctx context.Context, req RoleRequest) (RoleAnswer, error)
	Announce(ctx context.Context, term int64) error
}

// roleStarting is what a daemon calls itself while it decides.
const roleStarting = "starting"

// Engine is the primary / standby election (spec §4). It implements fleet.RoleView.
type Engine struct {
	peer     Peer
	settings SettingsSource
	now      func() time.Time
	logf     func(format string, args ...any)
	path     string // state.json ("" = memory only)
	hostID   string

	mu          sync.Mutex
	set         Settings
	setRev      int64
	haveSet     bool
	role        string
	term        int64
	primary     string
	reason      string
	lastHeard   time.Time
	announce    bool // an announcement is owed to the peer
	newestAge   func() (time.Duration, bool)
	pushReady   func() bool    // whether this daemon's push module is ready; nil = not reported in the answer
	peerPush    fleet.PeerPush // what the other daemon said about its push at the last call
	peerEpoch   int            // the roleEpoch peerPush was recorded in
	roleEpoch   int            // counts the changes of role
	lastOrderWn time.Time
	readErr     string        // the last settings read error that was logged
	backoff     time.Duration // the settings re-read delay after errors
	retryAt     time.Time
	started     bool

	// notification: states queue up under nmu and one goroutine delivers them, outside every lock.
	nmu     sync.Mutex
	queue   []fleet.RoleState
	cbs     []func(fleet.RoleState)
	running bool
	last    fleet.RoleState // last state queued
}

// NewEngine returns an engine in standby, term 0 (or what path holds).
func NewEngine(peer Peer, src SettingsSource, now func() time.Time, logf func(string, ...any), path, hostID string) *Engine {
	if now == nil {
		now = time.Now
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	e := &Engine{peer: peer, settings: src, now: now, logf: logf, path: path, hostID: hostID, role: fleet.RoleStandby}
	if path != "" {
		p := loadState(path)
		e.term, e.lastHeard = p.Term, p.LastHeard
	}
	return e
}

// SetNewestAge supplies (f must not call any method of the engine: it runs under its lock) the age of the newest reading of any host (the aggregator, F-1c2); nil answers null.
func (e *Engine) SetNewestAge(f func() (time.Duration, bool)) {
	e.mu.Lock()
	e.newestAge = f
	e.mu.Unlock()
}

// SetPushReady supplies (f must not call any method of the engine: it runs under its lock) whether this daemon's push module is ready; the
// role answer carries it as push_ready. nil takes it away again.
func (e *Engine) SetPushReady(f func() bool) {
	e.mu.Lock()
	e.pushReady = f
	e.mu.Unlock()
}

// PeerPush implements fleet.PeerPushView: what the other daemon said about its push at this daemon's last call to it, as long as this
// daemon's role has not changed since (then it is unknown: a reader that takes unknown as "the other cannot push" errs towards a
// duplicate push, never towards none). It changes only when a call completes, so a push module that stops or starts on the other
// daemon shows here after at most one heartbeat: an alert is late by that much, never lost, since it stays active and undelivered.
func (e *Engine) PeerPush() fleet.PeerPush {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.peerEpoch != e.roleEpoch {
		return fleet.PeerPush{}
	}
	p := e.peerPush
	if p.Ready != nil {
		v := *p.Ready
		p.Ready = &v // a copy: the caller cannot reach into the engine
	}
	return p
}

// recordPeerLocked notes the outcome of a call to the other daemon, under the role the daemon has now. Callers record after any change
// of role the answer caused, so the record belongs to the role the daemon ends up in.
func (e *Engine) recordPeerLocked(ans RoleAnswer, err error) {
	e.peerEpoch = e.roleEpoch
	if err != nil {
		e.peerPush = fleet.PeerPush{Known: true}
		return
	}
	e.peerPush = fleet.PeerPush{Known: true, Answered: true}
	if ans.PushReady != nil {
		v := *ans.PushReady
		e.peerPush.Ready = &v
	}
}

// ---- fleet.RoleView ----

func (e *Engine) State() fleet.RoleState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stateLocked()
}

func (e *Engine) stateLocked() fleet.RoleState {
	return fleet.RoleState{Role: e.role, Term: e.term, Primary: e.primary, Self: e.set.Self, Reason: e.reason}
}

// OnChange registers f. It does not replay the current state: register first, then read State(). It is called after every change of the state, in order, one call at a time, from one goroutine and
// never under the engine's lock: f may call State or anything that changes the state.
func (e *Engine) OnChange(f func(fleet.RoleState)) {
	e.nmu.Lock()
	e.cbs = append(e.cbs, f)
	e.nmu.Unlock()
}

// notifyLocked queues the state if it differs from the last queued one. Called with e.mu held (it takes nmu inside; nothing
// takes them in the other order).
func (e *Engine) notifyLocked() {
	s := e.stateLocked()
	e.nmu.Lock()
	if s == e.last {
		e.nmu.Unlock()
		return
	}
	e.last = s
	e.queue = append(e.queue, s)
	start := !e.running
	e.running = true
	e.nmu.Unlock()
	if start {
		go e.drain()
	}
}

func (e *Engine) drain() {
	for {
		e.nmu.Lock()
		if len(e.queue) == 0 {
			e.running = false
			e.nmu.Unlock()
			return
		}
		s := e.queue[0]
		e.queue = e.queue[1:]
		cbs := append([]func(fleet.RoleState){}, e.cbs...)
		e.nmu.Unlock()
		for _, cb := range cbs {
			cb(s)
		}
	}
}

// persistLocked writes state.json; a failure is logged and the role stays in memory.
func (e *Engine) persistLocked() {
	if err := saveState(e.path, persisted{Role: e.role, Term: e.term, LastHeard: e.lastHeard}); err != nil {
		e.logf("[fleet] state.json not written: %v", err)
	}
}

// ---- settings ----

// refresh reads the settings when their revision moved (or never read); a read error keeps the previous settings. The same
// error is logged once until it changes or a read succeeds, and the retry backs off from 2 s to 60 s.
func (e *Engine) refresh() {
	e.mu.Lock()
	if !e.retryAt.IsZero() && e.now().Before(e.retryAt) {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()
	rev, err := e.settings.Rev()
	e.mu.Lock()
	have, cur := e.haveSet, e.setRev
	e.mu.Unlock()
	if err == nil && have && rev == cur {
		return
	}
	s, err := e.settings.Read()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		if msg := err.Error(); msg != e.readErr {
			e.readErr = msg
			e.logf("[fleet] fleet config not read: %v", err)
		}
		if e.backoff < 2*time.Second {
			e.backoff = 2 * time.Second
		} else if e.backoff *= 2; e.backoff > time.Minute {
			e.backoff = time.Minute
		}
		e.retryAt = e.now().Add(e.backoff)
		return
	}
	e.readErr, e.backoff, e.retryAt = "", 0, time.Time{}
	e.set, e.setRev, e.haveSet = s, rev, true
}

// offReason is why the fleet is off here ("" = on).
func (s Settings) offReason() string {
	switch {
	case !s.Enabled:
		return "disabled"
	case strings.TrimSpace(s.Self) == "":
		return "this daemon has no peers alias"
	case indexOf(s.Order, s.Self) < 0:
		return "own alias is not in the fleet order"
	}
	return ""
}

func indexOf(order []string, alias string) int {
	for i, a := range order {
		if strings.EqualFold(a, alias) {
			return i
		}
	}
	return -1
}

// peerAlias is the other daemon host: the first entry of order that is not this daemon.
func (s Settings) peerAlias() string {
	for _, a := range s.Order {
		if !strings.EqualFold(a, s.Self) {
			return a
		}
	}
	return ""
}

// ---- transitions (callers hold e.mu) ----

// setRoleLocked sets the role. A real change starts a new roleEpoch, which makes what was recorded about the other daemon's push
// unknown (see recordPeerLocked): it was seen from another role, and the other daemon may be a different one by now.
func (e *Engine) setRoleLocked(r string) {
	if e.role != r {
		e.roleEpoch++
	}
	e.role = r
}

func (e *Engine) becomeOffLocked(reason string) {
	e.setRoleLocked(fleet.RoleOff)
	e.reason, e.primary, e.announce = reason, "", false
	e.persistLocked()
	e.notifyLocked()
}

func (e *Engine) becomePrimaryLocked(term int64) {
	e.setRoleLocked(fleet.RolePrimary)
	e.term, e.reason, e.primary = term, "", e.set.Self
	e.persistLocked()
	e.notifyLocked()
}

func (e *Engine) becomeStandbyLocked(primary string, term int64) {
	e.setRoleLocked(fleet.RoleStandby)
	e.reason, e.announce = "", false
	if term > e.term {
		e.term = term
	}
	if primary != "" {
		e.primary = primary
	} else if e.primary == e.set.Self {
		e.primary = ""
	}
	e.persistLocked()
	e.notifyLocked()
}

// adoptTermLocked raises the highest term seen (and, for a standby, restarts the primary's grace when the term is new).
func (e *Engine) adoptTermLocked(term int64) {
	if term > e.term {
		e.term = term
		e.persistLocked()
		e.notifyLocked()
	}
}

// staysOnTieLocked decides two primaries of equal term: true when this daemon stays. When both daemons' orders are the same
// (an empty theirOrder counts as the same) the one first in the order stays; when they differ, or other is not in the order,
// the smaller alias (case-insensitive) stays, which both sides compute alike.
func (e *Engine) staysOnTieLocked(other string, theirOrder []string) bool {
	self := e.set.Self
	if (len(theirOrder) == 0 || equalFold(theirOrder, e.set.Order)) && indexOf(e.set.Order, other) >= 0 && indexOf(e.set.Order, self) >= 0 {
		return indexOf(e.set.Order, self) < indexOf(e.set.Order, other)
	}
	return strings.ToLower(self) < strings.ToLower(other)
}

// observePrimaryLocked applies what a primary named from says (a heartbeat answer, a heartbeat call or an announcement).
// theirOrder nil (an announcement carries none) leaves an equal-term tie to the next heartbeat.
func (e *Engine) observePrimaryLocked(from string, term int64, theirOrder []string) {
	switch e.role {
	case fleet.RolePrimary:
		if term > e.term || (term == e.term && theirOrder != nil && !e.staysOnTieLocked(from, theirOrder)) {
			e.becomeStandbyLocked(from, term)
		}
	case fleet.RoleStandby:
		if term >= e.term {
			e.primary = from
			e.lastHeard = e.now()
			e.adoptTermLocked(term)
			e.persistLocked()
			e.notifyLocked()
		}
	}
}

// ---- peer-facing (the routes) ----

// HandleRole applies the caller's role / term like an announcement and answers with this daemon's state. The settings are
// refreshed only when their revision moved.
func (e *Engine) HandleRole(req RoleRequest) RoleAnswer {
	e.refresh()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.role != fleet.RoleOff {
		switch req.Role {
		case fleet.RolePrimary:
			e.observePrimaryLocked(req.FromAlias, req.Term, req.Order)
		default:
			// a non-primary caller only teaches the term
			if req.Term > e.term && e.role == fleet.RoleStandby {
				e.adoptTermLocked(req.Term)
			}
		}
	}
	return e.answerLocked()
}

// HandleAnnounce applies a primary's announcement.
func (e *Engine) HandleAnnounce(from string, term int64) {
	e.refresh()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.role == fleet.RoleOff {
		return
	}
	e.observePrimaryLocked(from, term, nil)
}

func (e *Engine) answerLocked() RoleAnswer {
	a := RoleAnswer{HostID: e.hostID, Alias: e.set.Self, Role: e.role, Term: e.term, Order: append([]string{}, e.set.Order...)}
	if e.newestAge != nil {
		if d, ok := e.newestAge(); ok {
			ms := d.Milliseconds()
			a.NewestAgeMS = &ms
		}
	}
	if e.pushReady != nil {
		ready := e.pushReady()
		a.PushReady = &ready
	}
	return a
}

// ---- start and heartbeat ----

func (e *Engine) callPeer(ctx context.Context, req RoleRequest) (RoleAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout())
	defer cancel()
	return e.peer.Role(ctx, req)
}

// Startup is the start procedure of spec §4: ask the other, then decide.
func (e *Engine) Startup(ctx context.Context) {
	e.refresh()
	e.mu.Lock()
	e.started = true
	if r := e.set.offReason(); r != "" || !e.haveSet {
		if r == "" {
			r = "fleet config not readable"
		}
		e.becomeOffLocked(r)
		e.mu.Unlock()
		return
	}
	self, term, order := e.set.Self, e.term, append([]string{}, e.set.Order...)
	e.mu.Unlock()

	ans, err := e.callPeer(ctx, RoleRequest{FromAlias: self, Role: roleStarting, Term: term, Order: order})

	if ctx.Err() != nil { // stopped: that is not the other being dead
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.recordPeerLocked(ans, err) // after the role it decides below, so the record belongs to that role
	switch {
	case err != nil: // unreachable: primary with persisted+1, an announcement owed
		e.becomePrimaryLocked(e.term + 1)
		e.announce = true
	case ans.Role == fleet.RolePrimary:
		e.lastHeard = e.now()
		e.becomeStandbyLocked(ans.Alias, ans.Term)
	default: // standby, off or starting: the first in order is primary
		hi := maxInt64(e.term, ans.Term)
		if !e.staysOnTieLocked(e.set.peerAlias(), ans.Order) {
			e.becomeStandbyLocked("", hi)
		} else {
			e.becomePrimaryLocked(hi + 1)
		}
	}
}

// Beat is one heartbeat (both roles call the other's role).
func (e *Engine) Beat(ctx context.Context) {
	e.refresh()
	e.mu.Lock()
	r := e.set.offReason()
	if !e.haveSet {
		e.mu.Unlock()
		return
	}
	wasOff := e.role == fleet.RoleOff
	switch {
	case r != "" && !wasOff:
		e.becomeOffLocked(r) // a primary turned off stops at once
		e.mu.Unlock()
		return
	case r != "":
		if e.reason != r {
			e.reason = r
			e.notifyLocked()
		}
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()
	if wasOff { // switched on: run the start procedure again
		e.Startup(ctx)
		return
	}

	e.mu.Lock()
	req := RoleRequest{FromAlias: e.set.Self, Role: e.role, Term: e.term, Order: append([]string{}, e.set.Order...)}
	e.mu.Unlock()
	ans, err := e.callPeer(ctx, req)
	if ctx.Err() != nil { // stopped mid-call: no takeover, nothing written
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.role == fleet.RoleOff { // changed under us
		return
	}
	if err == nil {
		e.warnOrderLocked(ans.Order)
	}
	switch e.role {
	case fleet.RolePrimary:
		if err != nil {
			return
		}
		e.lastHeard = e.now()
		if ans.Role == fleet.RolePrimary {
			e.observePrimaryLocked(ans.Alias, ans.Term, ans.Order)
		} else if ans.Term > e.term {
			e.adoptTermLocked(ans.Term)
		}
		if e.role == fleet.RolePrimary && e.announce {
			term := e.term
			e.mu.Unlock()
			aerr := e.announceTo(ctx, term)
			e.mu.Lock()
			if aerr == nil && e.role == fleet.RolePrimary && e.term == term {
				e.announce = false
			}
		}
	case fleet.RoleStandby:
		if err != nil || !e.aliveLocked(ans) {
			e.takeoverLocked(ctx, ans, err == nil)
			e.recordPeerLocked(ans, err)
			return
		}
		e.lastHeard = e.now()
		e.observePrimaryLocked(ans.Alias, ans.Term, ans.Order)
		e.recordPeerLocked(ans, err) // after observePrimaryLocked: that may change the role
	}
}

func (e *Engine) announceTo(ctx context.Context, term int64) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout())
	defer cancel()
	return e.peer.Announce(ctx, term)
}

// aliveLocked: the primary answered, says it is primary, and its newest reading is not stale. A null age means it has nothing
// measurable (no source wired, nothing to measure yet): a takeover would not help and would only make the two flip.
func (e *Engine) aliveLocked(a RoleAnswer) bool {
	if a.Role != fleet.RolePrimary {
		return false
	}
	if a.NewestAgeMS == nil {
		return true
	}
	return time.Duration(*a.NewestAgeMS)*time.Millisecond <= AliveAge
}

func (e *Engine) takeoverLocked(ctx context.Context, a RoleAnswer, answered bool) {
	hi := e.term
	if answered && a.Term > hi {
		hi = a.Term
	}
	e.becomePrimaryLocked(hi + 1)
	term := e.term
	// announce best effort, with the lock released (the caller's deferred Unlock still pairs with the Lock below)
	e.mu.Unlock()
	aerr := e.announceTo(ctx, term)
	e.mu.Lock()
	if aerr != nil && e.role == fleet.RolePrimary && e.term == term {
		e.announce = true // owed: the next heartbeat that gets an answer repeats it
	}
}

// warnOrderLocked logs, at most once an hour, that the two daemons' order differ.
func (e *Engine) warnOrderLocked(theirs []string) {
	if len(theirs) == 0 || equalFold(theirs, e.set.Order) {
		return
	}
	if !e.lastOrderWn.IsZero() && e.now().Sub(e.lastOrderWn) < orderWarnEvery {
		return
	}
	e.lastOrderWn = e.now()
	e.logf("[fleet] the fleet order differs between the two daemons: here %v, there %v", e.set.Order, theirs)
}

func equalFold(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Run is Startup, then a heartbeat every HeartbeatEvery, and a cheap settings check every settingsPoll so that turning the
// fleet off stops a primary at once. It returns when ctx ends.
func (e *Engine) Run(ctx context.Context) {
	e.Startup(ctx)
	hb := time.NewTicker(HeartbeatEvery)
	defer hb.Stop()
	poll := time.NewTicker(settingsPoll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hb.C:
			e.Beat(ctx)
		case <-poll.C:
			e.checkOff()
		}
	}
}

const settingsPoll = 2 * time.Second

// checkOff stops at once when the settings (by revision) now say off, and starts again when they say on.
func (e *Engine) checkOff() {
	e.refresh()
	e.mu.Lock()
	defer e.mu.Unlock()
	if r := e.set.offReason(); e.haveSet && r != "" && e.role != fleet.RoleOff {
		e.becomeOffLocked(r)
	}
}

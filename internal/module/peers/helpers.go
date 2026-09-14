package peers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/peers/proxyhelper"
)

// Helper lifecycle constants (spec §4.5).
const (
	// HelperCap is the most `pdx peer-proxy` helpers one daemon runs at
	// once — starting, ready and stopping instances all count, as does
	// every unresolved record whose process may still be alive.
	HelperCap = 32
	// HelperIdleReap releases a helper that has seen no traffic in either
	// direction for this long or longer.
	HelperIdleReap = 30 * time.Minute
	// HelperReadyTimeout bounds the wait for a helper's ready line.
	HelperReadyTimeout = 3 * time.Second
	// HelperTermGrace is the SIGTERM→SIGKILL grace for a helper being
	// stopped (Release) and for a recorded pid being swept.
	HelperTermGrace = 2 * time.Second

	// sweepPoll is how often Sweep re-checks a signalled pid while it
	// waits (≤ termGrace) for it to go away.
	sweepPoll = 10 * time.Millisecond
)

var (
	// ErrProxyLimit: HelperCap helpers (or reserved slots) already exist.
	ErrProxyLimit = errors.New("proxy_limit")
	// ErrProxySpawnFailed: the helper for this origin could not be started.
	// The error wraps the cause; the caller decides whether to retry.
	ErrProxySpawnFailed = errors.New("proxy_spawn_failed")
	// ErrNotReady: the manager has not swept yet, is stopping, or a
	// previous run's helper for this origin may still be alive.
	ErrNotReady = errors.New("not_ready")
)

// helperState is where one helper instance is in its life. Transitions
// happen only under helperManager.mu:
//
//	starting ─(ready)─▶ ready ─(Release)─▶ stopping ─(process gone)─▶ exited
//	    └─(spawn failed)──────────────────────────────────────────────▶ exited
type helperState int

const (
	helperStarting helperState = iota
	helperReady
	helperStopping
	helperExited
)

func (s helperState) String() string {
	switch s {
	case helperStarting:
		return "starting"
	case helperReady:
		return "ready"
	case helperStopping:
		return "stopping"
	case helperExited:
		return "exited"
	}
	return fmt.Sprintf("helperState(%d)", int(s))
}

// revUnapplied is the appliedRev of a helper spawned by a v1 request (no
// from.address): no v2 address has been applied yet, so the first v2
// address applies regardless of its revision (spec §3.5).
const revUnapplied int64 = -1

// helper is one instance of a helper process for one origin. A key may
// see several instances over the daemon's life (spawn, reap, spawn
// again); gen tells them apart, and every operation on an instance is
// bound to that instance, never to the key.
type helper struct {
	key   ipeers.OriginKey
	name  string // the registry name the instance currently carries; read via Name, changed only by ApplyAddress
	gen   uint64 // manager-wide monotonic; identifies THIS instance
	state helperState

	// appliedRev is the address_rev of the request whose address the
	// instance currently carries (spec §3.5): stored at admission from
	// the request that spawned it, advanced by ApplyAddress; revUnapplied
	// for a v1 spawn until a v2 address is applied.
	appliedRev int64

	ready  chan struct{} // closed when state leaves helperStarting (ready or failed)
	exited chan struct{} // closed when the instance has left the map (exited or failed)
	err    error         // set (under mu) before ready closes when starting failed

	handle    proxyhelper.Handle // nil until ready
	pid       int
	procStart string
	sock      string
	files     []string
	lastUsed  time.Time

	stopOnce sync.Once
}

// unresolvedRecord is a proxyRecord whose cleanup could not be completed.
// occupies is true when the recorded process is alive or unclassifiable
// — a helper for that origin may still exist, so the origin is blocked
// and the slot counts toward HelperCap. A record whose process is proven
// dead (only files were left behind) occupies nothing. Recomputed only by
// Sweep.
type unresolvedRecord struct {
	proxyRecord
	occupies bool
}

// helperManagerConfig is everything newHelperManager needs. Every seam
// left nil takes its production default.
type helperManagerConfig struct {
	Start       proxyhelper.Starter
	ProxiesPath string
	RegistryDir string
	SockDir     string
	Version     string

	Now         func() time.Time
	ProcStart   func(pid int) (string, error)
	PidAlive    func(pid int) bool
	DialRefused func(sock string) bool
	Signal      func(pid int, sig os.Signal) error
	LiveEntries func() []ipeers.Entry

	ReadyTimeout time.Duration
	TermGrace    time.Duration

	OnFrame func(h *helper, line string)
	Log     func(format string, args ...any)
}

// helperManager owns every `pdx peer-proxy` helper process of this
// daemon: one per remote origin, spawned on demand, capped, recorded in
// proxies.json before it is handed out, idle-reaped, swept on startup and
// joined on shutdown.
type helperManager struct {
	mu      sync.Mutex
	helpers map[ipeers.OriginKey]*helper // every instance from starting until exited — the cap counts ALL of them
	nextGen uint64
	closed  bool           // set by Stop: no new admissions
	wg      sync.WaitGroup // every startup goroutine and every pump goroutine

	procCtx    context.Context // lifetime of every helper process; cancelled last, as a backstop
	procCancel context.CancelFunc

	start       proxyhelper.Starter
	now         func() time.Time
	procStart   func(pid int) (string, error)
	pidAlive    func(pid int) bool
	dialRefused func(sock string) bool // true when connect fails with ECONNREFUSED/ENOENT
	signal      func(pid int, sig os.Signal) error
	liveEntries func() []ipeers.Entry

	proxiesPath  string
	registryDir  string
	sockDir      string
	version      string
	readyTimeout time.Duration
	termGrace    time.Duration

	onFrame func(h *helper, line string)
	log     func(format string, args ...any)

	swept      bool
	unresolved []unresolvedRecord
}

// newHelperManager builds a manager; nil seams take production defaults.
func newHelperManager(cfg helperManagerConfig) *helperManager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &helperManager{
		helpers:      make(map[ipeers.OriginKey]*helper),
		procCtx:      ctx,
		procCancel:   cancel,
		start:        cfg.Start,
		now:          cfg.Now,
		procStart:    cfg.ProcStart,
		pidAlive:     cfg.PidAlive,
		dialRefused:  cfg.DialRefused,
		signal:       cfg.Signal,
		liveEntries:  cfg.LiveEntries,
		proxiesPath:  cfg.ProxiesPath,
		registryDir:  cfg.RegistryDir,
		sockDir:      cfg.SockDir,
		version:      cfg.Version,
		readyTimeout: cfg.ReadyTimeout,
		termGrace:    cfg.TermGrace,
		onFrame:      cfg.OnFrame,
		log:          cfg.Log,
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.procStart == nil {
		m.procStart = ccuds.DefaultProcStart
	}
	if m.pidAlive == nil {
		m.pidAlive = defaultPidAlive
	}
	if m.dialRefused == nil {
		m.dialRefused = proxyhelper.DialRefused
	}
	if m.signal == nil {
		m.signal = defaultSignal
	}
	if m.liveEntries == nil {
		m.liveEntries = func() []ipeers.Entry { return nil }
	}
	if m.readyTimeout <= 0 {
		m.readyTimeout = HelperReadyTimeout
	}
	if m.termGrace <= 0 {
		m.termGrace = HelperTermGrace
	}
	if m.log == nil {
		m.log = log.Printf
	}
	return m
}

// defaultPidAlive is kill(pid, 0): alive when it succeeds or is refused
// for permissions (the process exists, it is just not ours).
func defaultPidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// defaultSignal delivers sig to pid.
func defaultSignal(pid int, sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return fmt.Errorf("signal %v is not a syscall.Signal", sig)
	}
	return syscall.Kill(pid, s)
}

// Acquire returns the ready helper for key, spawning one when none
// exists. waitCtx bounds only THIS caller's wait; every process runs
// under procCtx and every startup runs in a manager-owned goroutine, so
// no caller — the creator included — ever executes Spawn on its own
// stack (R2-M2). Every wake re-inspects under the lock: the instance may
// be ready, failed (⇒ its error, reported to every waiter — nobody
// retries, R3-M1), replaced (wait on the new one) or stopping (wait on
// exited). A fresh instance is spawned only after the old one has left
// the map and only by a new Acquire call — never two processes for one
// key, never more than the cap, never a spawn loop.
//
// name and rev are the admitting request's helper name and address_rev
// (revUnapplied for a v1 request): both are stored on the instance at
// creation, under the same lock that admits it, so a spawn whose waiter
// cancelled still carries the revision that named it and a later, older
// request that joins the spawn cannot rename it (spec §3.5). A caller
// that finds an existing instance passes its own name/rev to
// ApplyAddress afterwards; Acquire never renames.
func (m *helperManager) Acquire(waitCtx context.Context, key ipeers.OriginKey, name string, rev int64) (*helper, error) {
	for {
		m.mu.Lock()
		if !m.swept || m.closed {
			m.mu.Unlock()
			return nil, ErrNotReady
		}
		if m.unresolvedOccupies(key) {
			// R3-M4: a previous run's helper for this origin may still be alive.
			m.mu.Unlock()
			return nil, ErrNotReady
		}
		h := m.helpers[key]
		if h == nil {
			if len(m.helpers)+m.unresolvedAlive() >= HelperCap {
				m.mu.Unlock()
				return nil, ErrProxyLimit
			}
			m.nextGen++
			h = &helper{
				key:        key,
				name:       name,
				appliedRev: rev,
				gen:        m.nextGen,
				state:      helperStarting,
				ready:      make(chan struct{}),
				exited:     make(chan struct{}),
			}
			m.helpers[key] = h
			m.wg.Add(1)
			go m.startup(h)
		}
		state := h.state
		if state == helperReady {
			h.lastUsed = m.now()
		}
		m.mu.Unlock()

		switch state {
		case helperReady:
			return h, nil
		case helperStarting:
			select {
			case <-h.ready:
			case <-waitCtx.Done():
				return nil, waitCtx.Err()
			}
			m.mu.Lock()
			err := h.err
			m.mu.Unlock()
			if err != nil {
				return nil, err
			}
			// It became ready, or was replaced/stopped meanwhile: re-inspect.
		default: // stopping, exited
			select {
			case <-h.exited:
			case <-waitCtx.Done():
				return nil, waitCtx.Err()
			}
		}
	}
}

// startup runs in a manager goroutine (wg-tracked): spawn, prove the
// process identity, make ownership durable, and only then mark ready.
func (m *helperManager) startup(h *helper) {
	defer m.wg.Done()

	fail := func(err error) {
		m.mu.Lock()
		h.err = fmt.Errorf("%w: %w", ErrProxySpawnFailed, err)
		if m.helpers[h.key] == h {
			delete(m.helpers, h.key)
		}
		h.state = helperExited
		m.mu.Unlock()
		close(h.ready)
		close(h.exited)
	}

	cfg := proxyhelper.Config{
		Name:         h.name,
		RegistryDir:  m.registryDir,
		SockDir:      m.sockDir,
		Version:      m.version,
		Cwd:          m.registryDir,
		SessionID:    uuid.NewString(),
		PeerFeatures: m.peerFeatures(),
	}

	// Spawn guarantees no process and no files remain on failure.
	handle, err := proxyhelper.Spawn(m.procCtx, m.start, cfg, m.readyTimeout)
	if err != nil {
		fail(err)
		return
	}

	// rollback undoes a spawn that succeeded but could not be made
	// durable: stop the helper, then remove what a helper that died
	// without its own cleanup leaves behind — under the same ownership
	// rules as Release and Sweep (unlinkOwned): a registry file only while
	// it carries ps (an unknown ps unlinks no file), the socket only while
	// nobody listens on it.
	rollback := func(ps string, err error) {
		handle.Stop(m.termGrace)
		m.unlinkOwned(fmt.Sprintf("helper %d startup", handle.PID()), proxyRecord{
			PID: handle.PID(), ProcStart: ps, Sock: handle.Sock(), Files: handle.Files(),
		})
		fail(err)
	}

	ps, err := m.procStart(handle.PID())
	if err != nil {
		rollback("", fmt.Errorf("proc start of pid %d: %w", handle.PID(), err))
		return
	}

	m.mu.Lock()
	h.handle = handle
	h.pid = handle.PID()
	h.procStart = ps
	h.sock = handle.Sock()
	h.files = handle.Files()
	// Ownership must be durable BEFORE the helper is handed out (M3): a
	// MISSING record is the dangerous case — a crash between here and the
	// next write would orphan a process nobody could prove is ours.
	if err := m.writeProxiesLocked(); err != nil {
		m.mu.Unlock()
		rollback(ps, fmt.Errorf("write %s: %w", m.proxiesPath, err))
		return
	}
	h.state = helperReady
	h.lastUsed = m.now()
	m.mu.Unlock()
	close(h.ready)

	m.wg.Add(1)
	go m.pump(h)
}

// pump forwards every inbound frame to onFrame and touches the instance.
// Frames closes when the helper's stdout reaches EOF or after
// Handle.Stop joined the Handle's own pump (R2-M5), so the loop always
// ends; a helper that died on its own is then released.
func (m *helperManager) pump(h *helper) {
	defer m.wg.Done()
	for line := range h.handle.Frames() {
		m.touch(h)
		if m.onFrame != nil {
			m.onFrame(h, line)
		}
	}
	m.Release(h, "exited")
}

// touch refreshes h.lastUsed if the instance is still ready.
func (m *helperManager) touch(h *helper) {
	m.mu.Lock()
	if h.state == helperReady {
		h.lastUsed = m.now()
	}
	m.mu.Unlock()
}

// Touch refreshes the current instance for key (outbound traffic).
func (m *helperManager) Touch(key ipeers.OriginKey) {
	m.mu.Lock()
	if h := m.helpers[key]; h != nil && h.state == helperReady {
		h.lastUsed = m.now()
	}
	m.mu.Unlock()
}

// Name returns the registry name instance h currently carries, read
// under the lock (ApplyAddress may change it at any time).
func (m *helperManager) Name(h *helper) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return h.name
}

// ApplyAddress makes instance h carry name when rev is newer than the
// instance's applied revision — or when no v2 address was ever applied
// (a v1 spawn, revUnapplied) — and returns the name to use for THIS
// request's wrapper (spec §3.5): the new name when it applied, the
// current one otherwise. The rename is in place: the registry file is
// rewritten (ccuds.RewriteRegistryName), the process, socket and pid
// stay. Rules:
//
//  1. h is not the current instance for its key, or is not ready ⇒ the
//     current name, untouched: a starting instance is still being
//     registered, a stopping one is cleaning its files up.
//  2. rev <= appliedRev (with something applied) ⇒ the current name.
//  3. appliedRev advances to rev even when name is unchanged, so an
//     A→B→A sequence whose late B request arrives with an older rev
//     cannot win (the same-name request is not a hole in the order).
//  4. A differing name is written to the registry file first; on failure
//     the revision is rolled back (the request did not apply), the
//     failure is logged, and the current name is returned.
//
// Everything — the state check, the revision compare and the file
// rewrite — runs under m.mu. That is deliberate: the flip to stopping
// (Release) and the rename cannot interleave, so a rename can never
// recreate a file the cleanup just unlinked, and two requests for one
// origin cannot rewrite the file out of order. The recorded trade-off is
// that Acquire/Release/Touch/ProxyPIDs/FindBySock of EVERY origin wait
// for the rewrite — one small file, read, rewritten to a temp, fsynced
// and renamed — for the rare request that actually renames.
func (m *helperManager) ApplyAddress(h *helper, name string, rev int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.helpers[h.key] != h || h.state != helperReady {
		return h.name
	}
	if h.appliedRev != revUnapplied && rev <= h.appliedRev {
		return h.name
	}
	prev := h.appliedRev
	h.appliedRev = rev
	if name != h.name {
		if err := ccuds.RewriteRegistryName(m.registryDir, h.pid, name, m.now().UnixMilli()); err != nil {
			m.log("peers: helper %d (%s): rename to %q (rev %d) failed, keeping the current name: %v", h.pid, h.name, name, rev, err)
			h.appliedRev = prev
			return h.name
		}
		h.name = name
	}
	return h.name
}

// peerFeatures copies the peerFeatures of the newest live non-proxy
// Claude Code entry (by ParseProcStart, ties by pid desc), so a future
// feature flag is not silently missing; DefaultPeerFeatures otherwise.
func (m *helperManager) peerFeatures() []string {
	var (
		best  *ipeers.Entry
		bestT time.Time
	)
	entries := m.liveEntries()
	for i := range entries {
		e := &entries[i]
		if e.IsProxy {
			continue
		}
		t, err := ipeers.ParseProcStart(e.ProcStart)
		if err != nil {
			continue
		}
		if best == nil || t.After(bestT) || (t.Equal(bestT) && e.PID > best.PID) {
			best, bestT = e, t
		}
	}
	if best != nil {
		if features, ok := ccuds.ReadPeerFeatures(m.registryDir, best.PID); ok {
			return features
		}
	}
	return append([]string(nil), ccuds.DefaultPeerFeatures...)
}

// Release stops instance h. It is instance-bound and keeps the instance
// in the map until the process is gone (R2-M1, R2-M4): while stopping,
// Acquire waits on h.exited, the cap still counts it and ProxyPIDs still
// lists it. A stale callback (h already replaced) or a second Release of
// an instance already stopping is a no-op.
func (m *helperManager) Release(h *helper, reason string) {
	m.release(h, reason, nil)
}

// release is Release with an optional extra precondition, evaluated under
// the lock together with the instance/state check, right before the flip
// to stopping — so a decision taken on a snapshot (ReapIdle's idle set)
// is re-validated against the current state (R2-B). Caller holds nothing.
func (m *helperManager) release(h *helper, reason string, still func() bool) {
	m.mu.Lock()
	if m.helpers[h.key] != h || h.state != helperReady || (still != nil && !still()) {
		m.mu.Unlock()
		return
	}
	h.state = helperStopping
	m.mu.Unlock()

	h.stopOnce.Do(func() {
		// Closes stdin (the helper removes its own files), grace, SIGKILL,
		// Wait, joins the Handle's pump.
		if err := h.handle.Stop(m.termGrace); err != nil {
			m.log("peers: helper %d (%s): stopped (%s): %v", h.pid, h.name, reason, err)
		}
		// The process is gone; what it left behind is unlinked only while
		// it is provably its own (R2-C): the window between its exit and
		// here is one a same-UID process, or a reused pid, can fill.
		cleanupOK := m.unlinkOwned(fmt.Sprintf("helper %d", h.pid), recordOf(h))

		m.mu.Lock()
		delete(m.helpers, h.key)
		if !cleanupOK {
			// The file keeps naming the leftovers until a Sweep resolves
			// them; the process itself is gone, so nothing is occupied.
			m.unresolved = append(m.unresolved, unresolvedRecord{proxyRecord: recordOf(h)})
		}
		h.state = helperExited
		err := m.writeProxiesLocked()
		m.mu.Unlock()
		if err != nil {
			// A STALE record is harmless — Sweep proves ownership before
			// touching anything.
			m.log("peers: helper %d: rewrite %s after release: %v", h.pid, m.proxiesPath, err)
		}
		close(h.exited)
	})
}

// ReapIdle releases every ready helper idle for HelperIdleReap or longer.
// The candidates are collected under the lock, but each Release runs
// unlocked and waits its grace, during which Acquire/Touch may refresh a
// later candidate's lastUsed: idleness is therefore re-checked under the
// lock right before each candidate flips to stopping, and a helper
// touched meanwhile is skipped (R2-B).
func (m *helperManager) ReapIdle() {
	idleFor := func(h *helper) bool { return m.now().Sub(h.lastUsed) >= HelperIdleReap } // caller holds mu
	m.mu.Lock()
	var idle []*helper
	for _, h := range m.helpers {
		if h.state == helperReady && idleFor(h) {
			idle = append(idle, h)
		}
	}
	m.mu.Unlock()
	for _, h := range idle {
		m.release(h, "idle", func() bool { return idleFor(h) })
	}
}

// ProxyPIDs lists the pid of every instance in the map that has one
// (ready or stopping) — the peers inventory hides these as proxies.
func (m *helperManager) ProxyPIDs() map[int]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int]bool, len(m.helpers))
	for _, h := range m.helpers {
		if h.pid != 0 {
			out[h.pid] = true
		}
	}
	return out
}

// FindBySock returns the instance whose helper listens on sock: ready or
// stopping, never one still starting (its sock is filled before the
// ownership write, and a failed write rolls the instance back).
func (m *helperManager) FindBySock(sock string) (*helper, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.helpers {
		if h.state != helperStarting && h.pid != 0 && h.sock == sock {
			return h, true
		}
	}
	return nil, false
}

// unresolvedAlive counts unresolved records whose process may still be
// alive; each reserves a cap slot. Caller holds mu.
func (m *helperManager) unresolvedAlive() int {
	n := 0
	for _, u := range m.unresolved {
		if u.occupies {
			n++
		}
	}
	return n
}

// unresolvedOccupies reports whether a possibly-alive unresolved record
// names key's origin. Caller holds mu.
func (m *helperManager) unresolvedOccupies(key ipeers.OriginKey) bool {
	for _, u := range m.unresolved {
		if u.occupies && u.Origin == key {
			return true
		}
	}
	return false
}

// Stop closes admission FIRST, then joins EVERY instance (R2-B1, R3-M2):
// a starting one is waited for and, if it became ready, released; a
// ready one is released; a stopping one (a Release already in flight
// from ReapIdle or a target_gone) is waited for so its unlink and
// proxies.json write finish before Stop returns. Then every startup and
// pump goroutine is joined (onFrame can no longer be called) and procCtx
// is cancelled as a backstop. Idempotent.
func (m *helperManager) Stop() {
	m.mu.Lock()
	m.closed = true
	snapshot := make([]*helper, 0, len(m.helpers))
	for _, h := range m.helpers {
		snapshot = append(snapshot, h)
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, h := range snapshot {
		wg.Add(1)
		go func(h *helper) {
			defer wg.Done()
			<-h.ready // already closed unless starting
			m.mu.Lock()
			ready := h.state == helperReady
			m.mu.Unlock()
			if ready {
				m.Release(h, "shutdown")
			}
			<-h.exited
		}(h)
	}
	wg.Wait()

	m.wg.Wait()
	m.procCancel()
}

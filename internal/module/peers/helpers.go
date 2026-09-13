package peers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
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
	// direction for this long.
	HelperIdleReap = 30 * time.Minute
	// HelperReadyTimeout bounds the wait for a helper's ready line.
	HelperReadyTimeout = 3 * time.Second
	// HelperTermGrace is the SIGTERM→SIGKILL grace for a helper being
	// stopped (Release) and for a recorded pid being swept.
	HelperTermGrace = 2 * time.Second

	// sweepPoll is how often Sweep re-checks a signalled pid while it
	// waits (≤ termGrace) for it to go away.
	sweepPoll = 10 * time.Millisecond
	// dialProbeTimeout bounds defaultDialRefused's connect.
	dialProbeTimeout = 500 * time.Millisecond
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

// helper is one instance of a helper process for one origin. A key may
// see several instances over the daemon's life (spawn, reap, spawn
// again); gen tells them apart, and every operation on an instance is
// bound to that instance, never to the key.
type helper struct {
	key   ipeers.OriginKey
	name  string
	gen   uint64 // manager-wide monotonic; identifies THIS instance
	state helperState

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

// proxyRecord is one entry of proxies.json (spec §4.5): enough to prove,
// after a restart, which process and which files were ours.
type proxyRecord struct {
	PID       int              `json:"pid"`
	ProcStart string           `json:"proc_start"`
	Sock      string           `json:"sock"`
	Files     []string         `json:"files"`
	Origin    ipeers.OriginKey `json:"origin"`
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
		m.dialRefused = defaultDialRefused
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

// defaultDialRefused reports whether connecting to sock fails because
// nobody listens (ECONNREFUSED) or the path is gone (ENOENT). A live
// listener or any other failure is not a licence to unlink.
func defaultDialRefused(sock string) bool {
	c, err := net.DialTimeout("unix", sock, dialProbeTimeout)
	if err == nil {
		c.Close()
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOENT)
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
func (m *helperManager) Acquire(waitCtx context.Context, key ipeers.OriginKey, name string) (*helper, error) {
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
				key:    key,
				name:   name,
				gen:    m.nextGen,
				state:  helperStarting,
				ready:  make(chan struct{}),
				exited: make(chan struct{}),
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

	rollback := func(err error) {
		handle.Stop(m.termGrace)
		if rmErr := ccuds.RemoveRegistry(handle.Files()); rmErr != nil {
			m.log("peers: helper %d: remove registry files after failed startup: %v", handle.PID(), rmErr)
		}
		fail(err)
	}

	ps, err := m.procStart(handle.PID())
	if err != nil {
		rollback(fmt.Errorf("proc start of pid %d: %w", handle.PID(), err))
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
		rollback(fmt.Errorf("write %s: %w", m.proxiesPath, err))
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
	m.mu.Lock()
	if m.helpers[h.key] != h || h.state != helperReady {
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
		cleanupOK := true
		if err := ccuds.RemoveRegistry(h.files); err != nil {
			m.log("peers: helper %d: remove registry files: %v", h.pid, err)
			cleanupOK = false
		}
		if err := os.Remove(h.sock); err != nil && !errors.Is(err, fs.ErrNotExist) {
			m.log("peers: helper %d: unlink %s: %v", h.pid, h.sock, err)
			cleanupOK = false
		}

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

// ReapIdle releases every ready helper idle for longer than HelperIdleReap.
func (m *helperManager) ReapIdle() {
	now := m.now()
	m.mu.Lock()
	var idle []*helper
	for _, h := range m.helpers {
		if h.state == helperReady && now.Sub(h.lastUsed) > HelperIdleReap {
			idle = append(idle, h)
		}
	}
	m.mu.Unlock()
	for _, h := range idle {
		m.Release(h, "idle")
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

// FindBySock returns the instance whose helper listens on sock (ready or
// stopping).
func (m *helperManager) FindBySock(sock string) (*helper, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.helpers {
		if h.pid != 0 && h.sock == sock {
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

// recordOf is h's proxies.json entry. Caller holds mu (or owns h).
func recordOf(h *helper) proxyRecord {
	return proxyRecord{
		PID:       h.pid,
		ProcStart: h.procStart,
		Sock:      h.sock,
		Files:     append([]string(nil), h.files...),
		Origin:    h.key,
	}
}

// writeProxiesLocked writes the record of every instance with a pid plus
// every unresolved record to proxiesPath atomically: temp file in the
// same directory, fsync, rename. Caller holds mu.
func (m *helperManager) writeProxiesLocked() error {
	records := make([]proxyRecord, 0, len(m.helpers)+len(m.unresolved))
	for _, h := range m.helpers {
		if h.pid != 0 {
			records = append(records, recordOf(h))
		}
	}
	for _, u := range m.unresolved {
		records = append(records, u.proxyRecord)
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].PID < records[j].PID })
	return writeProxiesFile(m.proxiesPath, records)
}

// writeProxiesFile is the atomic write behind writeProxiesLocked.
func writeProxiesFile(path string, records []proxyRecord) error {
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

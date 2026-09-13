package peers

// Startup sweep of proxies.json (spec §4.5): the part of helperManager
// that reconciles a previous run's helper processes and files before the
// daemon accepts its first delivery. Lives apart from helpers.go only for
// size; every method here is a helperManager method.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/peers/ccuds"
)

// pidIdentity is Sweep's three-state answer to "is the recorded pid still
// the process we started?" (R2-M3, R3-M3). The states are never folded:
// unknown means we must not touch anything.
type pidIdentity int

const (
	identityUnknown pidIdentity = iota
	identitySame
	identityDifferent
)

func (i pidIdentity) String() string {
	switch i {
	case identitySame:
		return "same"
	case identityDifferent:
		return "different"
	}
	return "unknown"
}

func (m *helperManager) identity(r proxyRecord) pidIdentity {
	ps, err := m.procStart(r.PID)
	if err != nil {
		return identityUnknown
	}
	if ps == r.ProcStart {
		return identitySame
	}
	return identityDifferent
}

// waitGone waits at most termGrace until r's pid is dead or no longer
// carries our identity. It reports the final (alive, identity) pair.
func (m *helperManager) waitGone(r proxyRecord) (alive bool, id pidIdentity) {
	deadline := time.NewTimer(m.termGrace)
	defer deadline.Stop()
	for {
		alive = m.pidAlive(r.PID)
		if !alive {
			return false, identityUnknown
		}
		if id = m.identity(r); id != identitySame {
			return true, id
		}
		select {
		case <-deadline.C:
			return true, identitySame
		case <-time.After(sweepPoll):
		}
	}
}

// Sweep (spec §4.5) reconciles proxies.json from a previous run. Called
// once from Start BEFORE the HTTP server accepts. For every record it
// classifies the pid (same / different / unknown — re-checked before
// every signal), terminates a process proven ours, unlinks only files
// that still carry our procStart and only a socket nobody listens on,
// and retains whatever it could not resolve. A retained record whose
// process is alive or unclassifiable occupies its origin and a cap slot
// (R3-M4). The rewritten file is the durable ownership record the
// daemon must not run without (R2-M4): a write failure fails Sweep.
func (m *helperManager) Sweep() error {
	records := m.readProxies()
	var unresolved []unresolvedRecord
	for _, r := range records {
		if u, keep := m.sweepRecord(r); keep {
			unresolved = append(unresolved, u)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.unresolved = unresolved
	if err := m.writeProxiesLocked(); err != nil {
		return fmt.Errorf("peers: write %s: %w", m.proxiesPath, err)
	}
	m.swept = true
	return nil
}

// readProxies loads proxiesPath: missing ⇒ none; unparsable ⇒ logged and
// treated as none (nothing in it can prove ownership of anything).
func (m *helperManager) readProxies() []proxyRecord {
	data, err := os.ReadFile(m.proxiesPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			m.log("peers: read %s: %v (treating as empty)", m.proxiesPath, err)
		}
		return nil
	}
	var records []proxyRecord
	if err := json.Unmarshal(data, &records); err != nil {
		m.log("peers: %s is unparsable: %v (treating as empty; nothing in it can be proven ours)", m.proxiesPath, err)
		return nil
	}
	return records
}

// sweepRecord resolves one record; keep is true when it must be retained.
func (m *helperManager) sweepRecord(r proxyRecord) (u unresolvedRecord, keep bool) {
	alive := m.pidAlive(r.PID)
	if alive {
		switch m.identity(r) {
		case identityUnknown:
			// A live pid we cannot classify may still be our helper.
			m.log("peers: sweep: pid %d is alive but its start time is unknown; leaving %s and its files alone", r.PID, r.Sock)
			return unresolvedRecord{proxyRecord: r, occupies: true}, true
		case identitySame:
			if err := m.signal(r.PID, syscall.SIGTERM); err != nil {
				m.log("peers: sweep: SIGTERM pid %d: %v", r.PID, err)
			}
			alive2, id := m.waitGone(r)
			if alive2 && id == identitySame {
				// Re-checked immediately before sending: the pid may have
				// been reused during the grace.
				if alive2 = m.pidAlive(r.PID); alive2 {
					id = m.identity(r)
				}
				if alive2 && id == identitySame {
					if err := m.signal(r.PID, syscall.SIGKILL); err != nil {
						m.log("peers: sweep: SIGKILL pid %d: %v", r.PID, err)
					}
					alive2, id = m.waitGone(r)
				}
			}
			if alive2 && id != identityDifferent {
				// Still alive as ours, or unknown during the wait: not
				// proven gone, files untouched.
				m.log("peers: sweep: pid %d survived SIGTERM/SIGKILL (identity %s); record retained", r.PID, id)
				return unresolvedRecord{proxyRecord: r, occupies: true}, true
			}
		case identityDifferent:
			// The pid was reused by someone else: nothing to signal.
		}
	}

	// The process is dead or is no longer ours. Unlink only what still
	// carries our identity.
	cleanupOK := true
	for _, path := range r.Files {
		got := ccuds.RegistryProcStart(path)
		if got == "" && !fileExists(path) {
			continue
		}
		// An empty recorded proc_start proves nothing: it would "match"
		// any unreadable file, so such a record never unlinks anything.
		if r.ProcStart == "" || got != r.ProcStart {
			m.log("peers: sweep: %s carries procStart %q, not ours (%q); left alone", path, got, r.ProcStart)
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			m.log("peers: sweep: unlink %s: %v", path, err)
			cleanupOK = false
		}
	}
	if r.Sock != "" {
		if !m.dialRefused(r.Sock) {
			m.log("peers: sweep: %s has a live listener; left alone", r.Sock)
		} else if err := os.Remove(r.Sock); err != nil && !errors.Is(err, fs.ErrNotExist) {
			m.log("peers: sweep: unlink %s: %v", r.Sock, err)
			cleanupOK = false
		}
	}
	if !cleanupOK {
		return unresolvedRecord{proxyRecord: r, occupies: false}, true
	}
	return unresolvedRecord{}, false
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

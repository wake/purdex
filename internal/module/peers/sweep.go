package peers

// Startup sweep of proxies.json (spec §4.5): the part of helperManager
// that reconciles a previous run's helper processes and files before the
// daemon accepts its first delivery. Lives apart from helpers.go only for
// size; every method here is a helperManager method.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
)

// identity classifies r's pid: the shared tri-state (ipeers.ClassifyProc,
// R2-M3, R3-M3) — same / different / unknown, never folded: unknown means
// we must not touch anything. alive is false for a dead pid (whose start
// time is never asked for).
func (m *helperManager) identity(r proxyRecord) (alive bool, id ipeers.ProcIdentity) {
	return ipeers.ClassifyProc(r.PID, r.ProcStart, m.pidAlive, m.procStart)
}

// waitGone waits at most termGrace until r's pid is dead or no longer
// carries our identity. It reports the final (alive, identity) pair.
func (m *helperManager) waitGone(r proxyRecord) (alive bool, id ipeers.ProcIdentity) {
	deadline := time.NewTimer(m.termGrace)
	defer deadline.Stop()
	for {
		if alive, id = m.identity(r); !alive || id != ipeers.ProcSame {
			return alive, id
		}
		select {
		case <-deadline.C:
			return true, ipeers.ProcSame
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
// daemon must not run without (R2-M4): a write failure fails Sweep. A
// second call after a successful one is a no-op: from then on
// proxies.json names the daemon's OWN live helpers, which must never be
// signalled.
func (m *helperManager) Sweep() error {
	m.mu.Lock()
	swept := m.swept
	m.mu.Unlock()
	if swept {
		return nil
	}

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

// sweepRecord resolves one record; keep is true when it must be retained.
func (m *helperManager) sweepRecord(r proxyRecord) (u unresolvedRecord, keep bool) {
	if alive, id := m.identity(r); alive {
		switch id {
		case ipeers.ProcUnknown:
			// A live pid we cannot classify may still be our helper.
			m.log("peers: sweep: pid %d is alive but its start time is unknown; leaving %s and its files alone", r.PID, r.Sock)
			return unresolvedRecord{proxyRecord: r, occupies: true}, true
		case ipeers.ProcSame:
			if err := m.signal(r.PID, syscall.SIGTERM); err != nil {
				m.log("peers: sweep: SIGTERM pid %d: %v", r.PID, err)
			}
			alive2, id := m.waitGone(r)
			if alive2 && id == ipeers.ProcSame {
				// Re-checked immediately before sending: the pid may have
				// been reused during the grace.
				alive2, id = m.identity(r)
				if alive2 && id == ipeers.ProcSame {
					if err := m.signal(r.PID, syscall.SIGKILL); err != nil {
						m.log("peers: sweep: SIGKILL pid %d: %v", r.PID, err)
					}
					alive2, id = m.waitGone(r)
				}
			}
			if alive2 && id != ipeers.ProcDifferent {
				// Still alive as ours, or unknown during the wait: not
				// proven gone, files untouched.
				m.log("peers: sweep: pid %d survived SIGTERM/SIGKILL (identity %s); record retained", r.PID, id)
				return unresolvedRecord{proxyRecord: r, occupies: true}, true
			}
		case ipeers.ProcDifferent:
			// The pid was reused by someone else: nothing to signal.
		}
	}

	// The process is dead or is no longer ours. Unlink only what still
	// carries our identity.
	if !m.unlinkOwned("sweep", r) {
		return unresolvedRecord{proxyRecord: r, occupies: false}, true
	}
	return unresolvedRecord{}, false
}

// unlinkOwned removes what a dead helper left behind, under the one
// ownership rule Sweep, Release and the startup rollback share (R2-C): a
// registry file is unlinked only while it still carries r.ProcStart (an
// empty recorded proc_start proves nothing — it would "match" any
// unreadable file — so such a record never unlinks a file), the socket
// only while nobody listens on it. A mismatch or a live listener is
// logged (prefixed who) and left alone; only an unlink that FAILS makes
// the cleanup incomplete (false).
func (m *helperManager) unlinkOwned(who string, r proxyRecord) (cleanupOK bool) {
	cleanupOK = true
	for _, path := range r.Files {
		got := ccuds.RegistryProcStart(path)
		if got == "" && !fileExists(path) {
			continue
		}
		if r.ProcStart == "" || got != r.ProcStart {
			m.log("peers: %s: %s carries procStart %q, not ours (%q); left alone", who, path, got, r.ProcStart)
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			m.log("peers: %s: unlink %s: %v", who, path, err)
			cleanupOK = false
		}
	}
	if r.Sock != "" {
		if !m.dialRefused(r.Sock) {
			m.log("peers: %s: %s has a live listener; left alone", who, r.Sock)
		} else if err := os.Remove(r.Sock); err != nil && !errors.Is(err, fs.ErrNotExist) {
			m.log("peers: %s: unlink %s: %v", who, r.Sock, err)
			cleanupOK = false
		}
	}
	return cleanupOK
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

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
	"os/exec"
	"strconv"
	"strings"
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
	// ps is forked at most once per zombieSettle, not once per poll.
	var nextProbe time.Time
	for {
		if alive, id = m.identity(r); !alive || id != ipeers.ProcSame {
			return alive, id
		}
		if !time.Now().Before(nextProbe) {
			if m.tryReapZombie(r) {
				return false, id
			}
			nextProbe = time.Now().Add(m.zombieSettle)
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
// sweepFlight is one in-flight Sweep: err is set before done closes and never changes after.
type sweepFlight struct {
	done chan struct{}
	err  error
}

func (m *helperManager) Sweep() error {
	m.mu.Lock()
	if m.swept {
		m.mu.Unlock()
		return nil
	}
	if f := m.sweepFlight; f != nil {
		// A scan is in flight: wait for it and share ITS result (a failure included), so a second caller can
		// neither run it twice nor read an unfinished or failed scan as success. The result lives in the flight
		// itself, so a later retry cannot overwrite what this waiter reads.
		m.mu.Unlock()
		<-f.done
		return f.err
	}
	f := &sweepFlight{done: make(chan struct{})}
	m.sweepFlight = f // claim the scan before reading proxies.json
	m.mu.Unlock()

	var result error
	defer func() { // a failed Sweep stays retryable: the claim is released either way
		f.err = result // before close: the waiters read it after <-f.done
		m.mu.Lock()
		m.sweepFlight = nil
		m.mu.Unlock()
		close(f.done)
	}()

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
		result = fmt.Errorf("peers: write %s: %w", m.proxiesPath, err)
		return result
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
			if m.tryReapZombie(r) {
				// An unwaited zombie of ours: dead, nothing to signal.
				break
			}
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

// defaultZombieSettle is how long a pid must keep showing as a zombie
// before Sweep reaps it (see tryReapZombie).
const defaultZombieSettle = 200 * time.Millisecond

// defaultProcState is `ps -o stat=,ppid= -p pid` (BSD ps, as macOS ships):
// the first field is the state letters, the second the parent pid. Anything
// that does not parse is an error.
func defaultProcState(pid int) (string, int, error) {
	out, err := exec.Command("ps", "-o", "stat=,ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", 0, fmt.Errorf("ps -p %d: %w", pid, err)
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return "", 0, fmt.Errorf("ps -p %d: unexpected output %q", pid, strings.TrimSpace(string(out)))
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return "", 0, fmt.Errorf("ps -p %d: ppid %q: %w", pid, f[1], err)
	}
	return f[0], ppid, nil
}

// defaultReap is a non-blocking wait4 on pid. Only "collected exactly this
// pid" is true: r == 0 is a direct child still running, ECHILD means not our
// child, anything else is a failure — all false.
func defaultReap(pid int) bool {
	var ws syscall.WaitStatus
	r, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
	return err == nil && r == pid
}

// isOurZombie reports whether pid currently shows as a zombie whose
// parent is this daemon, returning the ppid it saw.
func (m *helperManager) isOurZombie(pid int) (ppid int, ok bool) {
	state, ppid, err := m.procState(pid)
	if err != nil || !strings.HasPrefix(state, "Z") || ppid != m.ownPID {
		return 0, false
	}
	return ppid, true
}

// tryReapZombie reaps r's pid when it is provably an unwaited zombie child
// of this daemon: ps says Z with ppid == ownPID, and after zombieSettle it
// still does (a child some Cmd.Wait is blocked on is collected within
// milliseconds, so a zombie that outlives the settle has no waiter). The
// caller has established identity ProcSame. Every failure is false and the
// caller falls back to the signal path.
func (m *helperManager) tryReapZombie(r proxyRecord) bool {
	ppid, ok := m.isOurZombie(r.PID)
	if !ok {
		return false
	}
	time.Sleep(m.zombieSettle)
	if ppid2, ok := m.isOurZombie(r.PID); !ok || ppid2 != ppid {
		return false
	}
	// Re-prove the identity right before wait4: during the settle the original process may have been collected
	// and its pid recycled into another child that also shows as Z under the same parent.
	if alive, id := m.identity(r); !alive || id != ipeers.ProcSame {
		return false
	}
	if !m.reap(r.PID) {
		return false
	}
	m.log("peers: sweep: reaped zombie pid %d (identity same)", r.PID)
	return true
}

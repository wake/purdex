package codexbroker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// DefaultTermTimeout is the spec §5.4 line 452 budget for SIGTERM to take
// effect.
const DefaultTermTimeout = 5 * time.Second

// DefaultKillTimeout is the spec §5.4 line 455 budget for SIGKILL to be
// reaped by the kernel.
const DefaultKillTimeout = 2 * time.Second

// lstartTolerance is the wallclock slack accepted between BrokerRecord.Lstart
// and the freshly-fetched ps row in VerifyIdentity. The tolerance defends
// against ps lstart string-formatting round-trip (BSD lstart only resolves
// to seconds and the parser may shift sub-second wallclock data).
const lstartTolerance = 1 * time.Second

// VerifyIdentity is spec §5.4 Step 0 (lines 434-437): re-fetch ps for
// rec.PID; confirm lstart matches (±1s) and cmdline contains the broker
// task-worker marker. A mismatch causes the kill sequence to abort and
// triggers E2 quarantine for this brokerKey.
//
// Returns (ok, detail). detail is a short audit-trace string explaining
// why the verify succeeded or failed.
//
// PID-reuse window: the time between this verify and Step 3's SIGTERM
// is bounded by gracefulShutdownTimeout (5s). Per plan §9 risks table,
// PID reuse within that window is documented as an acceptable known
// risk; E2 catches it on the next sweep.
func VerifyIdentity(rec BrokerRecord, lister ProcessLister) (bool, string) {
	if lister == nil {
		return false, "no-lister"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	rows, err := lister.List(ctx)
	if err != nil {
		return false, "lister-error"
	}
	for _, row := range rows {
		if row.PID != rec.PID {
			continue
		}
		// Cmdline check first — even a matching pid is suspicious if the
		// process isn't a broker.
		if !strings.Contains(row.Cmdline, brokerTaskWorkerCmdlineMarker) {
			return false, "cmdline-mismatch"
		}
		// Lstart tolerance check.
		drift := row.Lstart.Sub(rec.Lstart)
		if drift < 0 {
			drift = -drift
		}
		if drift > lstartTolerance {
			return false, "lstart-drift"
		}
		return true, "match"
	}
	return false, "pid-not-found"
}

// KillSequence is the per-broker kill orchestrator. Steps 0-6 per spec
// §5.4 are run sequentially via Run (task P).
//
// In task J the struct only needs to compile so VerifyIdentity has a
// natural caller; subsequent tasks (K-P) populate Step 1 (audit
// preimage), Step 2 (graceful), Steps 3+4 (TERM/KILL), Step 5 (verify
// gone), Step 6 (cleanup), and the Run wiring.
type KillSequence struct {
	Rec       BrokerRecord
	Lister    ProcessLister
	Dialer    Dialer
	Signaller Signaller
	FS        FS
	AuditDir  string

	GracefulTimeout time.Duration // default DefaultGracefulShutdownTimeout
	TermTimeout     time.Duration // default 5s
	KillTimeout     time.Duration // default 2s

	// PgidLookup is the seam over unix.Getpgid so tests can drive Step 3 and
	// Step 4 without spawning a real process group. Production wiring leaves
	// the field nil and falls through to defaultPgidLookup which calls
	// unix.Getpgid directly.
	PgidLookup func(pid int) (int, error)
}

// KillResult is the audit-postscript payload (spec §5.5 lines 495-500)
// plus the Step 6 cleanup outcome. StepLatencyMs[0]=graceful,
// [1]=SIGTERM, [2]=SIGKILL.
type KillResult struct {
	GracefulOk    bool
	TermOk        bool
	KillOk        bool
	StepLatencyMs [3]int64
	CleanedUp     bool
	Err           error
}

// runStep1 writes the audit preimage. Per spec §5.4 line 465, this MUST
// commit before any signal is sent to the broker, so KillSequence.Run
// (task P) aborts the entire sequence on any error from this method.
//
// Returns the audit file path on success (used by Step 6's
// AppendPostscript) or "" + err on failure.
//
// The broker.log tail is read via ks.FS so test scaffolding can inject
// in-memory fixtures without touching disk.
func (ks *KillSequence) runStep1(_ context.Context, decision DecisionResult) (string, error) {
	tail := ReadBrokerLogTail(ks.Rec.StateDir, ks.FS, DefaultBrokerLogTailLines)
	return WritePreimage(ks.AuditDir, ks.Rec, decision, tail)
}

// stepGraceful implements spec §5.4 Step 2: dial the broker's unix socket
// endpoint and POST a shutdown request, then poll the lister until the
// broker process exits or GracefulTimeout elapses.
//
// Returns true iff the broker is observed to be gone within budget. A
// missing endpoint, nil dialer/lister, dial failure, or RPC timeout all
// surface as false; the caller proceeds to Step 3 (SIGTERM).
//
// The graceful path is best-effort: SIGTERM is the reliable kill path per
// plan §3 / §9, and the audit preimage already committed in Step 1 is the
// durable record regardless of whether graceful succeeds.
func (ks *KillSequence) stepGraceful(ctx context.Context) bool {
	if ks.Rec.Endpoint == "" || ks.Dialer == nil {
		return false
	}
	addr := strings.TrimPrefix(ks.Rec.Endpoint, "unix:")
	if addr == ks.Rec.Endpoint {
		// Non-unix endpoint scheme — we don't speak it in P2.
		return false
	}

	budget := ks.GracefulTimeout
	if budget <= 0 {
		budget = DefaultGracefulShutdownTimeout
	}
	rpcCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	// Fire-and-forget the shutdown request in a goroutine; the actual
	// liveness signal is the lister no longer returning a row for our pid,
	// not the HTTP response code. The goroutine is bounded by rpcCtx.
	go func() {
		conn, err := ks.Dialer.Dial("unix", addr)
		if err != nil {
			return
		}
		defer conn.Close()
		if dl, ok := rpcCtx.Deadline(); ok {
			_ = conn.SetDeadline(dl)
		}
		req := "POST /shutdown HTTP/1.1\r\nHost: broker\r\nContent-Length: 0\r\n\r\n"
		_, _ = conn.Write([]byte(req))
		// Read at most one byte of the response so we don't block on a
		// shutdown handler that closes the conn before flushing.
		var buf [1]byte
		_, _ = conn.Read(buf[:])
	}()

	// Poll the lister at a moderate cadence until either the process is
	// gone or the budget expires.
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ks.processGone(rpcCtx) {
			return true
		}
		select {
		case <-rpcCtx.Done():
			return false
		case <-ticker.C:
			// next tick
		}
	}
}

// defaultPgidLookup is the production unix.Getpgid wrapper. Returning the
// raw os/syscall error lets stepSIGTERM/SIGKILL distinguish ESRCH from
// other failures if needed in future.
func defaultPgidLookup(pid int) (int, error) {
	return unix.Getpgid(pid)
}

// stepSIGTERM implements spec §5.4 Step 3 (lines 448-453): resolve the
// target process group and send SIGTERM to -pgid. Refuses to signal if
// pgid <= 1 (defensive against init / kernel weirdness). Polls the lister
// until the process exits or TermTimeout elapses.
//
// Returns true iff the process is observed gone within budget. The caller
// (Run wiring in task P) records the latency and proceeds to Step 4 on
// false.
func (ks *KillSequence) stepSIGTERM(ctx context.Context) bool {
	return ks.signalAndWait(ctx, syscall.SIGTERM, ks.termBudget())
}

// stepSIGKILL implements spec §5.4 Step 4 (line 455): unconditional SIGKILL
// to -pgid. Same pgid-safety guards as stepSIGTERM. Polls until reap or
// KillTimeout. False here means the process group ignored even SIGKILL —
// rare, but recorded as KillOk=false in the kill result postscript.
func (ks *KillSequence) stepSIGKILL(ctx context.Context) bool {
	return ks.signalAndWait(ctx, syscall.SIGKILL, ks.killBudget())
}

// signalAndWait is the shared body of Steps 3 and 4. The pgid resolution
// and pgid≤1 refusal sit here so a future Step (e.g. SIGUSR1 fault probe)
// shares the same safety guard.
func (ks *KillSequence) signalAndWait(ctx context.Context, sig syscall.Signal, budget time.Duration) bool {
	if ks.Signaller == nil {
		return false
	}
	lookup := ks.PgidLookup
	if lookup == nil {
		lookup = defaultPgidLookup
	}
	pgid, err := lookup(ks.Rec.PID)
	if err != nil {
		return false
	}
	if pgid <= 1 {
		// Defensive: signalling pgid 1 (init) or 0 (current pgrp) is
		// catastrophic. Spec §5.4 line 451 mandates refusal.
		return false
	}
	if err := ks.Signaller.Kill(-pgid, sig); err != nil {
		return false
	}
	return ks.waitForExit(ctx, budget)
}

// waitForExit polls the lister at 20ms cadence until the row for ks.Rec.PID
// is gone or budget expires. The polling is identical to stepGraceful's
// inner loop but does not rely on the dialer.
func (ks *KillSequence) waitForExit(ctx context.Context, budget time.Duration) bool {
	waitCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ks.processGone(waitCtx) {
			return true
		}
		select {
		case <-waitCtx.Done():
			return false
		case <-ticker.C:
			// next tick
		}
	}
}

func (ks *KillSequence) termBudget() time.Duration {
	if ks.TermTimeout > 0 {
		return ks.TermTimeout
	}
	return DefaultTermTimeout
}

func (ks *KillSequence) killBudget() time.Duration {
	if ks.KillTimeout > 0 {
		return ks.KillTimeout
	}
	return DefaultKillTimeout
}

// DefaultVerifyGoneTimeout caps the spec §5.4 Step 5 lister scan; spec
// describes Step 5 as best-effort and bounds it tightly so a slow ps
// doesn't stall the kill sequence.
const DefaultVerifyGoneTimeout = 2 * time.Second

// stepVerifyGone implements spec §5.4 Step 5 (lines 457-459): re-list the
// process table and confirm no pid in the original (pid, lstart) family
// remains. The "family" is defined as either:
//
//   - a row whose PID equals ks.Rec.PID, OR
//   - a row whose Lstart matches ks.Rec.Lstart (within ±1s tolerance,
//     identical to the VerifyIdentity tolerance) AND whose Cmdline contains
//     the broker task-worker marker.
//
// Best-effort: if the lister errors out, returns true so cleanup is not
// blocked by transient ps unavailability. The kill itself is deemed
// complete after Step 4; Step 5 is a sanity probe only.
func (ks *KillSequence) stepVerifyGone(ctx context.Context) bool {
	if ks.Lister == nil {
		return true
	}
	listCtx, cancel := context.WithTimeout(ctx, DefaultVerifyGoneTimeout)
	defer cancel()
	rows, err := ks.Lister.List(listCtx)
	if err != nil {
		// Spec §5.4 line 459: best-effort. A transient lister failure
		// must not gate cleanup.
		return true
	}
	for _, row := range rows {
		if row.PID == ks.Rec.PID {
			return false
		}
		if !ks.Rec.Lstart.IsZero() && rowMatchesFamily(row, ks.Rec.Lstart) {
			return false
		}
	}
	return true
}

func rowMatchesFamily(row RawProcess, lstart time.Time) bool {
	if row.Lstart.IsZero() {
		return false
	}
	drift := row.Lstart.Sub(lstart)
	if drift < 0 {
		drift = -drift
	}
	if drift > lstartTolerance {
		return false
	}
	return strings.Contains(row.Cmdline, brokerTaskWorkerCmdlineMarker)
}

// DefaultSocketVerifyTimeout caps the spec §5.4 line 461 socket-inode
// verification budget. lsof / proc walk must complete inside this window or
// cleanup is deferred conservatively (held=true).
const DefaultSocketVerifyTimeout = 1 * time.Second

// stepCleanup implements spec §5.4 Step 6 (lines 461-462): if the broker
// owned a cxc-* socket dir, verify no live pid still holds the socket
// inode; if free, RemoveAll the dir. If still held, defer (return false)
// so the next sweep retries — preventing removal of a socket still in use
// by a stray child.
//
// Returns true on successful cleanup OR on a no-op when the broker had no
// SocketDir. Returns false only when the verifier reports held — the
// caller (Run wiring in task P) records that in the audit postscript.
func (ks *KillSequence) stepCleanup(_ context.Context, verifier SocketVerifier) bool {
	sockDir := ks.Rec.SocketDir
	if sockDir == "" {
		return true
	}
	if verifier != nil {
		// Look for a broker.sock under the dir; the verifier expects a
		// concrete socket path. Fall back to the dir itself if the
		// canonical socket name isn't there yet.
		sockPath := filepath.Join(sockDir, "broker.sock")
		if _, err := os.Stat(sockPath); err != nil {
			sockPath = sockDir
		}
		held, vErr := verifier.AnyPidHoldsSocket(sockPath, DefaultSocketVerifyTimeout)
		if vErr == nil && held {
			return false
		}
	}
	if err := os.RemoveAll(sockDir); err != nil {
		// Treat removal failure as defer: next sweep can retry.
		return false
	}
	return true
}

// processGone returns true iff the lister no longer returns a row whose
// pid matches ks.Rec.PID. A nil lister or a lister error returns false
// (conservative: do not declare graceful success unless we positively
// confirm the row is gone).
func (ks *KillSequence) processGone(ctx context.Context) bool {
	if ks.Lister == nil {
		return false
	}
	listCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	rows, err := ks.Lister.List(listCtx)
	if err != nil {
		return false
	}
	for _, row := range rows {
		if row.PID == ks.Rec.PID {
			return false
		}
	}
	return true
}

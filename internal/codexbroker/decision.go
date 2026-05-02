package codexbroker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultGracefulShutdownTimeout is the wall-clock budget the predicate-A
// thread/list RPC and the kill-sequence graceful step share. Spec §5.4
// line 445.
const DefaultGracefulShutdownTimeout = 5 * time.Second

// threadListResponse decodes the broker's /thread/list JSON body. We only
// look at id + status; any extra fields are ignored.
type threadListResponse struct {
	Threads []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"threads"`
}

// EvalPredicateA implements spec §5.1 row A "Active execution".
//
// Strategy (per plan §4 task C):
//
//  1. Try the broker RPC thread/list via dialer; if ≥1 thread reports
//     status=active OR a pending approval → A=true.
//  2. Otherwise (RPC unreachable, RPC OK with no active threads, or RPC
//     timeout) inspect the shared jobs slice the caller already loaded:
//     a queued job → A=true; a running job that is NOT stale (per
//     IsStaleRunning) → A=true; otherwise → A=false.
//
// The conflict rule (spec §5.1 lines 367-368) lives in this predicate: an
// RPC stall does not penalise the broker — state.json drives the decision
// when the RPC is down.
//
// The jobs slice is owned by the caller (EvalDecision in task F) so the
// state.json read happens once per broker per scan.
func EvalPredicateA(ctx context.Context, rec BrokerRecord, jobs []StateJobLite, lister ProcessLister, dialer Dialer) (bool, string) {
	// Step 1 — RPC.
	rpcOK, rpcSawActive, rpcDetail := tryThreadList(ctx, rec, dialer)
	if rpcSawActive {
		return true, "rpc:" + rpcDetail
	}

	// Step 2 — fall through to state.json jobs slice.
	now := time.Now()
	for _, job := range jobs {
		switch job.Status {
		case "queued":
			return true, "state:queued:" + job.ID
		case "running":
			if stale, _ := IsStaleRunning(job, lister, DefaultStaleRunningThreshold, now); !stale {
				return true, "state:running-fresh:" + job.ID
			}
		}
	}
	if rpcOK {
		return false, "rpc:no-active-threads"
	}
	return false, "rpc-down-and-state-quiet"
}

// tryThreadList performs the broker RPC. Returns:
//   - rpcOK: the broker accepted the connection, parsed the response, and
//     produced a non-error status code. (False on dial error, body parse
//     error, or non-200.)
//   - sawActive: rpcOK and at least one thread is active or awaiting
//     approval.
//   - detail: short descriptor for the audit trace.
//
// The function never blocks longer than DefaultGracefulShutdownTimeout or
// the caller's context deadline, whichever is smaller.
func tryThreadList(ctx context.Context, rec BrokerRecord, dialer Dialer) (rpcOK bool, sawActive bool, detail string) {
	if dialer == nil || rec.Endpoint == "" {
		return false, false, "no-endpoint-or-dialer"
	}
	addr := strings.TrimPrefix(rec.Endpoint, "unix:")
	if addr == rec.Endpoint {
		// Endpoint scheme not unix:; we don't speak it in P2.
		return false, false, "non-unix-endpoint"
	}

	rpcCtx, cancel := context.WithTimeout(ctx, DefaultGracefulShutdownTimeout)
	defer cancel()

	type result struct {
		ok     bool
		active bool
		detail string
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := dialer.Dial("unix", addr)
		if err != nil {
			ch <- result{false, false, "dial-error"}
			return
		}
		defer conn.Close()
		// Apply ctx deadline to read/write operations.
		if dl, ok := rpcCtx.Deadline(); ok {
			_ = conn.SetDeadline(dl)
		}
		req := "POST /thread/list HTTP/1.1\r\nHost: broker\r\nContent-Length: 0\r\n\r\n"
		if _, err := conn.Write([]byte(req)); err != nil {
			ch <- result{false, false, "write-error"}
			return
		}
		// Read response — minimal HTTP/1.1 parser.
		body, parseErr := readHTTPBody(conn)
		if parseErr != nil {
			ch <- result{false, false, "read-error"}
			return
		}
		var resp threadListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			ch <- result{false, false, "decode-error"}
			return
		}
		if len(resp.Threads) == 0 {
			ch <- result{true, false, "no-threads"}
			return
		}
		for _, th := range resp.Threads {
			if isActiveStatus(th.Status) {
				ch <- result{true, true, "thread-" + th.Status + ":" + th.ID}
				return
			}
		}
		ch <- result{true, false, "no-active-threads"}
	}()

	select {
	case <-rpcCtx.Done():
		return false, false, "timeout"
	case r := <-ch:
		return r.ok, r.active, r.detail
	}
}

// readHTTPBody peels the body out of a one-shot HTTP/1.1 response, skipping
// status line + headers. Honours the Content-Length header; falls back to
// reading to EOF if no length was advertised. Bounded by the conn deadline.
func readHTTPBody(conn net.Conn) ([]byte, error) {
	br := bufio.NewReader(conn)
	// status line
	if _, err := br.ReadString('\n'); err != nil {
		return nil, err
	}
	contentLength := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trim := strings.TrimRight(line, "\r\n")
		if trim == "" {
			break
		}
		if eq := strings.IndexByte(trim, ':'); eq > 0 {
			name := strings.ToLower(strings.TrimSpace(trim[:eq]))
			val := strings.TrimSpace(trim[eq+1:])
			if name == "content-length" {
				if n, err := atoi(val); err == nil {
					contentLength = n
				}
			}
		}
	}
	if contentLength >= 0 {
		buf := make([]byte, contentLength)
		_, err := readFull(br, buf)
		if err != nil {
			return nil, err
		}
		return buf, nil
	}
	// Read until EOF / deadline.
	var buf []byte
	chunk := make([]byte, 4096)
	for {
		n, err := br.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			if errors.Is(err, net.ErrClosed) || err.Error() == "EOF" {
				return buf, nil
			}
			return buf, nil // best-effort — partial body is okay for our decode
		}
	}
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := r.Read(buf[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}

func atoi(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("non-digit")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func isActiveStatus(s string) bool {
	switch strings.ToLower(s) {
	case "active", "running", "pending", "pending_approval", "approval_required":
		return true
	default:
		return false
	}
}

// DefaultRecentResultWindow is the spec §5.1 row B default: any job whose
// completedAt falls within this window AND has a parseable jobs/<id>.json
// counts as a recent delivery readable.
const DefaultRecentResultWindow = 30 * time.Minute

// jobResultProbe is the minimal shape we look for in jobs/<id>.json. The
// presence of EITHER `result` or `rendered` (any non-empty value) confirms
// the broker successfully wrote a delivery; either field absent is treated
// as not-readable.
type jobResultProbe struct {
	Result   json.RawMessage `json:"result,omitempty"`
	Rendered json.RawMessage `json:"rendered,omitempty"`
}

// EvalPredicateB implements spec §5.1 row B "Recent delivery readable".
//
// The caller (EvalDecision in task F) provides the same shared
// []StateJobLite slice already loaded for predicate A — predicate B does
// not re-read state.json.
//
// True iff at least one job satisfies all three:
//   - status == "completed"
//   - completedAt is non-nil and within `window` of now
//   - <stateDir>/jobs/<id>.json exists, opens, decodes successfully, and
//     contains at least one of {result, rendered}.
func EvalPredicateB(rec BrokerRecord, jobs []StateJobLite, fs FS, window time.Duration) (bool, string) {
	if rec.StateDir == "" || fs == nil {
		return false, "no-state-dir"
	}
	now := time.Now()
	for _, job := range jobs {
		if job.Status != "completed" || job.CompletedAt == nil {
			continue
		}
		if now.Sub(*job.CompletedAt) > window {
			continue
		}
		if jobResultReadable(rec.StateDir, job.ID, fs) {
			return true, "completed:" + job.ID
		}
	}
	return false, "no-recent-delivery"
}

// jobResultReadable opens <stateDir>/jobs/<id>.json and confirms it contains
// at least one of {result, rendered}. Any I/O or decode failure → false.
func jobResultReadable(stateDir, id string, fs FS) bool {
	path := filepath.Join(stateDir, "jobs", id+".json")
	rc, err := fs.Open(path)
	if err != nil {
		return false
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return false
	}
	var p jobResultProbe
	if err := json.Unmarshal(body, &p); err != nil {
		return false
	}
	return len(p.Result) > 0 || len(p.Rendered) > 0
}

// LaunchRegistryReader is the read-only API predicate C and the decision
// composer (task F) consume. The full LaunchRegistryFile (task I) satisfies
// it. Empty() lets the composer distinguish "no purdex broker tracked yet"
// (P2 steady state on mlab) from "this specific brokerKey is foreign"
// (post-spawn-hook), which both feed AnomalyForeignOwner classification.
type LaunchRegistryReader interface {
	Lookup(brokerKey string) (*LaunchEntry, bool)
	Empty() bool
}

// PaneAliveChecker is the seam over the tmux pane-list exec. Production
// wiring (task R) injects a checker backed by `tmux list-panes -F
// '#{pane_id}'` cached per scan; tests inject a fake.
//
// The interface is split out from EvalPredicateC explicitly so the unit
// tests do not need to fork a real tmux server. Plan task E line 204
// describes this exec; making it injectable is the standard testability
// elaboration and does not redesign the semantic.
type PaneAliveChecker interface {
	IsAlive(pane string) (bool, error)
}

// EvalPredicateC implements spec §5.1 row C "Live ownership".
//
// Per spec §5.1 lines 365-366: cwd path must exist with definitive os.Stat
// success; then at least one of (a) tmuxPane mapped to this brokerKey via
// the launch registry is still alive, (b) registered Purdex caller session
// alive, or (c) explicit lease — (b) and (c) are deferred to P3 / Lights so
// P2 only checks (a).
//
// Transient cwd stat failures (ESTALE, EIO, EACCES — surfaced as
// AnomalyCwdTransientStatError on the BrokerRecord) fall through to pane
// evaluation; only definitive ENOENT (AnomalyCwdMissing) hard-fails C.
//
// Per plan task E line 207: predicate C reports ownership *evidence* only.
// The "no registry entry → quarantine, never auto-kill" foreign-broker
// contract is enforced one layer up in EvalDecision (task F) via direct
// inspection of registry.Empty() / Lookup so a purdex-owned broker whose
// pane just closed is not silently absorbed into the foreign-quarantine
// path.
func EvalPredicateC(rec BrokerRecord, fs FS, registry LaunchRegistryReader, panes PaneAliveChecker) (bool, string) {
	// 1. cwd gate: definitive ENOENT short-circuits to false.
	if recordHasAnomaly(rec, AnomalyCwdMissing) {
		return false, "cwd-missing"
	}
	// Transient errors fall through; otherwise stat the path now (only if
	// the BrokerRecord didn't already fail/transient earlier in the scan
	// path — both flows converge on the same evidence here).
	if !rec.CwdExists && !recordHasAnomaly(rec, AnomalyCwdTransientStatError) {
		// Re-stat in this layer for completeness; some test paths set
		// CwdExists=false without an explicit anomaly.
		if rec.Cwd != "" && fs != nil {
			if _, err := fs.Stat(rec.Cwd); err != nil {
				if isENOENT(err) {
					return false, "cwd-stat-enoent"
				}
				// transient — fall through
			}
		}
	}
	// 2. Registry lookup → pane alive.
	if registry == nil {
		return false, "no-registry"
	}
	entry, ok := registry.Lookup(rec.Key)
	if !ok {
		return false, "no-registry-entry"
	}
	if entry.TmuxPane == "" {
		return false, "no-tmux-pane-in-entry"
	}
	if panes == nil {
		return false, "no-pane-checker"
	}
	alive, err := panes.IsAlive(entry.TmuxPane)
	if err != nil {
		return false, "pane-checker-error"
	}
	if !alive {
		return false, "pane-closed"
	}
	return true, "pane-alive:" + entry.TmuxPane
}

// recordHasAnomaly returns true if rec.Anomalies contains the given code.
// Named to disambiguate from the existing pointer-receiver hasAnomaly test
// helper in reconcile_test.go (which takes *BrokerRecord).
func recordHasAnomaly(rec BrokerRecord, code AnomalyCode) bool {
	for _, a := range rec.Anomalies {
		if a.Code == code {
			return true
		}
	}
	return false
}

// isENOENT inspects an os/filepath error for the strict not-exist case.
// Wraps both errors.Is(err, os.ErrNotExist) and the os.IsNotExist heuristic
// to defend against syscall errno wrapping that doesn't reach the modern
// sentinel.
func isENOENT(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return os.IsNotExist(err)
}

// DecisionOpts configures EvalDecision. The seams (FS, Lister, Dialer,
// Registry, Panes) are interfaces so unit tests can run without filesystem
// or network access.
//
// E1Tracker (added in task G) is daemon-lifetime and owned by Module. May
// be nil in narrow test paths; production wiring (task R) always supplies
// a non-nil tracker.
type DecisionOpts struct {
	FS             FS
	Lister         ProcessLister
	Dialer         Dialer
	Registry       LaunchRegistryReader
	Panes          PaneAliveChecker
	IdleTimeout    time.Duration
	ResultWindow   time.Duration
	StaleThreshold time.Duration
	E1Tracker      *E1Tracker
}

// EvalDecision composes predicates A/B/C with the idle-timeout gate to
// produce the baseline kill verdict.
//
// Two-layer responsibility (round-3 finding #1):
//
//   - DecisionResult.Kill reflects ONLY the baseline rule
//     (¬A ∧ ¬B ∧ ¬C ∧ idleExpired). It is NOT short-circuited by
//     foreign-quarantine status. A foreign broker that is also baseline-
//     killable has Kill=true; the sweep handler decides whether to honour
//     it depending on whether the request is unfiltered (NEVER kills
//     foreign — mass-kill safety) or filtered with explicit brokerKey
//     (operator override unblocks the foreign guard, baseline Kill=true is
//     then honoured).
//
//   - Foreign classification is reported separately via
//     Reason="foreign_quarantine" + AnomaliesAdded=[AnomalyForeignOwner].
//     Foreign reason takes precedence over baseline Reason
//     ("idle_timeout") in the audit trail because foreign is the
//     semantically-dominant reason for any operator action on this broker.
//
// EvalDecision reads state.json once at the top via ReadStateJobs and feeds
// the resulting []StateJobLite into both predicate A and predicate B. On
// ReadStateJobs error, an empty slice is fed and AnomalyStateJSONUnreadable
// is added to AnomaliesAdded so the audit trace explains the degraded
// predicate result.
func EvalDecision(ctx context.Context, rec BrokerRecord, opts DecisionOpts) DecisionResult {
	now := time.Now()

	// Read state.json once; predicates A and B share the slice.
	jobs, jobsErr := ReadStateJobs(opts.FS, rec.StateDir)
	res := DecisionResult{}
	if jobsErr != nil {
		// Don't double-tag if the inventory layer already noted it.
		if !recordHasAnomaly(rec, AnomalyStateJSONUnreadable) {
			res.AnomaliesAdded = append(res.AnomaliesAdded, AnomalyStateJSONUnreadable)
		}
	}

	// Predicate A.
	aTrue, aDetail := EvalPredicateA(ctx, rec, jobs, opts.Lister, opts.Dialer)
	res.Predicates.A = aTrue
	res.Predicates.ADetail = aDetail

	// Predicate B.
	bWindow := opts.ResultWindow
	if bWindow <= 0 {
		bWindow = DefaultRecentResultWindow
	}
	bTrue, bDetail := EvalPredicateB(rec, jobs, opts.FS, bWindow)
	res.Predicates.B = bTrue
	res.Predicates.BDetail = bDetail

	// Predicate C.
	cTrue, cDetail := EvalPredicateC(rec, opts.FS, opts.Registry, opts.Panes)
	res.Predicates.C = cTrue
	res.Predicates.CDetail = cDetail

	// Idle calculation — nil LastJobUpdatedAt is treated as ∞ (most permissive
	// kill direction; a broker that never dispatched a job is a valid orphan
	// candidate per plan task F).
	idleSeconds := -1
	if rec.LastJobUpdatedAt != nil {
		idleSeconds = int(now.Sub(*rec.LastJobUpdatedAt).Seconds())
	}
	res.IdleSeconds = idleSeconds

	idleTimeout := opts.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = 30 * time.Minute
	}
	idleExpired := idleSeconds < 0 || float64(idleSeconds) >= idleTimeout.Seconds()

	// E1 — daemon-lifetime tracker (task G). Nil-safe so unit tests can
	// omit it; production wiring (task R) always supplies one.
	if opts.E1Tracker != nil {
		triggered, _ := opts.E1Tracker.Observe(rec, now)
		if triggered {
			res.OverrideE1 = true
		}
	}
	// E2 override hook lands in task H.

	// Baseline kill rule per spec §5.1 line 371.
	res.Kill = !aTrue && !bTrue && !cTrue && idleExpired

	// Foreign classification — orthogonal to baseline kill, computed for
	// every record so the sweep handler and audit trail both see it.
	isForeign := false
	switch {
	case opts.Registry == nil:
		isForeign = true
	case opts.Registry.Empty():
		isForeign = true
	default:
		if _, ok := opts.Registry.Lookup(rec.Key); !ok {
			isForeign = true
		}
	}
	if isForeign || recordHasAnomaly(rec, AnomalyForeignOwner) {
		// Add the anomaly only if the inventory layer hasn't already.
		if !recordHasAnomaly(rec, AnomalyForeignOwner) {
			res.AnomaliesAdded = append(res.AnomaliesAdded, AnomalyForeignOwner)
		}
		res.Reason = "foreign_quarantine"
	} else if res.Kill {
		res.Reason = "idle_timeout"
	}

	return res
}

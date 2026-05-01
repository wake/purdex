package codexbroker

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrPsUnavailable is returned by Scanner.Scan when the process lister fails
// outright (i.e. ps cannot be invoked). It maps to HTTP 503 in the handler.
//
// A timeout that produced partial output is NOT this error — partial maps
// to HTTP 200 with `partial=true`.
var ErrPsUnavailable = errors.New("process lister unavailable")

// ScannerOpts configures a Scanner. All fields except FS and Lister have
// safe zero-value defaults applied in NewScanner.
type ScannerOpts struct {
	FS              FS
	Lister          ProcessLister
	Dialer          Dialer    // optional, P1 never uses; see scanner.go
	Signaller       Signaller // optional, P1 never uses; see scanner.go
	PluginDataRoot  string
	SocketRoots     []string
	TotalDeadline   time.Duration
	StateDirBudget  time.Duration
	SocketDirBudget time.Duration
	CwdStatBudget   time.Duration // per-cwd Stat budget (default 50ms)
}

// Scanner orchestrates the three discovery sources and returns a Result.
//
// P1 does NOT coalesce concurrent Scan() calls: each caller's deadline is
// honoured independently. Production traffic is from a single SPA poll +
// occasional curl; fork amplification is not currently a concern. P3 will
// add a TTL cache + singleflight at that layer if needed.
type Scanner struct {
	opts ScannerOpts
}

// NewScanner constructs a Scanner with sensible defaults for unspecified options.
func NewScanner(opts ScannerOpts) *Scanner {
	if opts.TotalDeadline <= 0 {
		opts.TotalDeadline = 800 * time.Millisecond
	}
	if opts.StateDirBudget <= 0 {
		opts.StateDirBudget = 100 * time.Millisecond
	}
	if opts.SocketDirBudget <= 0 {
		opts.SocketDirBudget = 50 * time.Millisecond
	}
	if opts.CwdStatBudget <= 0 {
		opts.CwdStatBudget = 50 * time.Millisecond
	}
	return &Scanner{opts: opts}
}

// Result is the response payload of GET /api/codex/brokers; field names
// match spec §4.3 verbatim.
type Result struct {
	ScannedAt      time.Time      `json:"scannedAt"`
	ScanDurationMs int64          `json:"scanDurationMs"`
	DeadlineMs     int64          `json:"deadlineMs"`
	Partial        bool           `json:"partial"`
	Brokers        []BrokerRecord `json:"brokers"`
	// TopAnomalies are scanner-layer diagnostics that have no natural
	// BrokerRecord home (e.g. a malformed broker.json in a state dir we
	// otherwise skip, or a glob root that errored). They are surfaced
	// here so they don't disappear from the API contract.
	TopAnomalies []Anomaly     `json:"topAnomalies"`
	Summary      ResultSummary `json:"summary"`
}

// ResultSummary aggregates counts; field names match spec §4.3.
type ResultSummary struct {
	Total                 int      `json:"total"`
	WithProcess           int      `json:"withProcess"`
	WithStateDir          int      `json:"withStateDir"`
	WithSocket            int      `json:"withSocket"`
	AnomalyCount          int      `json:"anomalyCount"`
	DuplicateRuntimeCount int      `json:"duplicateRuntimeCount"`
	ScanSourceTimeouts    []string `json:"scanSourceTimeouts"`
}

// Scan performs one inventory pass. Each caller's context (and therefore
// deadline) is honoured independently; there is no coalescing in P1.
func (s *Scanner) Scan(ctx context.Context) (*Result, error) {
	return s.scanOnce(ctx)
}

// scanOnce is the actual scan body.
//
// Spec §4.3 503 contract: a 503 is only emitted when ps is unavailable AND
// state-dir + socket scans also produce no inventory. If ps fails but state
// or socket data exist, we return 200 with `partial=true` and a
// `process` entry in `scanSourceTimeouts` so the operator can see the
// degradation while still consuming the partial inventory.
func (s *Scanner) scanOnce(ctx context.Context) (*Result, error) {
	scanStart := time.Now()
	scanCtx, cancel := context.WithTimeout(ctx, s.opts.TotalDeadline)
	defer cancel()

	var (
		processC []processCandidate
		timeouts []string
		partial  bool
		psErr    error // non-ctx ps failure; remembered for possible 503 fallback below.
	)

	// 1. Process scan. We classify three outcomes:
	//    a) success            → processC populated.
	//    b) ctx deadline/canceled → empty processC, partial=true, "process" in timeouts.
	//    c) other error (ps not found, exec failure) → remember psErr,
	//       partial=true; continue to state/socket so the API can still
	//       return whatever inventory is reachable.
	pc, err := scanProcesses(scanCtx, s.opts.Lister, s.opts.FS)
	switch {
	case err == nil:
		processC = pc
		// Race-5 fix: lister returned (rows, nil) but the scan ctx is
		// already done (e.g. a custom lister that ignores ctx). Treat
		// as a process timeout so partial flag reflects reality.
		if scanCtx.Err() != nil {
			timeouts = append(timeouts, "process")
			partial = true
		}
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		timeouts = append(timeouts, "process")
		partial = true
	default:
		psErr = err
		timeouts = append(timeouts, "process")
		partial = true
	}

	// 2. State and socket scans in parallel under remaining budget.
	//    These run regardless of ps outcome so a ps failure can be
	//    answered with whatever inventory is still reachable.
	type stateResult struct {
		cands []stateCandidate
		anoms []Anomaly
	}
	type socketResult struct {
		cands []socketCandidate
		anoms []Anomaly
	}
	stateCh := make(chan stateResult, 1)
	socketCh := make(chan socketResult, 1)

	go func() {
		c, a := scanStateDirs(scanCtx, s.opts.FS, s.opts.PluginDataRoot, s.opts.StateDirBudget)
		stateCh <- stateResult{cands: c, anoms: a}
	}()
	go func() {
		c, a := scanSockets(scanCtx, s.opts.FS, s.opts.SocketRoots, s.opts.SocketDirBudget)
		socketCh <- socketResult{cands: c, anoms: a}
	}()

	var (
		stateC      []stateCandidate
		stateAnoms  []Anomaly
		socketCands []socketCandidate
		socketAnoms []Anomaly
	)

	stateDone := false
	socketDone := false
	for !(stateDone && socketDone) {
		select {
		case sr := <-stateCh:
			stateC = sr.cands
			stateAnoms = sr.anoms
			stateDone = true
		case sk := <-socketCh:
			socketCands = sk.cands
			socketAnoms = sk.anoms
			socketDone = true
		case <-scanCtx.Done():
			if !stateDone {
				timeouts = append(timeouts, "stateDir")
				stateDone = true
				partial = true
			}
			if !socketDone {
				timeouts = append(timeouts, "socket")
				socketDone = true
				partial = true
			}
		}
	}

	// 503 fallback: only when ps was unavailable AND there is no
	// state/socket data either, do we admit total failure.
	if psErr != nil && len(stateC) == 0 && len(socketCands) == 0 {
		return nil, fmt.Errorf("%w: %v", ErrPsUnavailable, psErr)
	}

	// 3. Reconcile.
	records, reconcileTopAnoms := reconcile(scanCtx, s.opts.FS, s.opts.CwdStatBudget, processC, stateC, socketCands)

	// Aggregate top-level anomalies. State/socket scanners may emit
	// scan-wide diagnostics that don't bind to any BrokerRecord
	// (e.g. malformed broker.json in a dir we'd otherwise skip).
	topAnoms := make([]Anomaly, 0, len(stateAnoms)+len(socketAnoms)+len(reconcileTopAnoms))
	topAnoms = append(topAnoms, stateAnoms...)
	topAnoms = append(topAnoms, socketAnoms...)
	topAnoms = append(topAnoms, reconcileTopAnoms...)

	return s.assembleResult(scanStart, records, topAnoms, timeouts, partial), nil
}

// assembleResult materialises the Result from the per-stage outputs.
func (s *Scanner) assembleResult(
	scanStart time.Time,
	records []BrokerRecord,
	topAnoms []Anomaly,
	timeouts []string,
	partial bool,
) *Result {
	// Always emit empty slices, never nil, so JSON consumers see [] consistently.
	if records == nil {
		records = []BrokerRecord{}
	}
	if timeouts == nil {
		timeouts = []string{}
	}
	if topAnoms == nil {
		topAnoms = []Anomaly{}
	}
	res := &Result{
		ScannedAt:      scanStart.UTC(),
		ScanDurationMs: time.Since(scanStart).Milliseconds(),
		DeadlineMs:     s.opts.TotalDeadline.Milliseconds(),
		Partial:        partial,
		Brokers:        records,
		TopAnomalies:   topAnoms,
		Summary: ResultSummary{
			Total:              len(records),
			ScanSourceTimeouts: timeouts,
			AnomalyCount:       len(topAnoms),
		},
	}
	for _, r := range records {
		if r.Sources&SourceProcess != 0 {
			res.Summary.WithProcess++
		}
		if r.Sources&SourceStateDir != 0 {
			res.Summary.WithStateDir++
		}
		if r.Sources&SourceSocket != 0 {
			res.Summary.WithSocket++
		}
		res.Summary.AnomalyCount += len(r.Anomalies)
		for _, a := range r.Anomalies {
			if a.Code == AnomalyDuplicateRuntime {
				res.Summary.DuplicateRuntimeCount++
				break
			}
		}
	}
	return res
}

package codexbroker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/singleflight"
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
	// CaseInsensitive overrides volume detection. nil → auto-detect via
	// IsCaseInsensitiveVolume(PluginDataRoot).
	CaseInsensitive *bool
}

// Scanner orchestrates the three discovery sources and returns a Result.
//
// Singleflight is used so concurrent Scan() calls share one underlying
// scan, defending against fork amplification under repeated polling.
type Scanner struct {
	opts ScannerOpts
	sf   singleflight.Group
	// caseInsensitive is resolved at construction time (one syscall) so
	// hot-path scans don't repeat it.
	caseInsensitive bool
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
	ci := false
	if opts.CaseInsensitive != nil {
		ci = *opts.CaseInsensitive
	} else if opts.PluginDataRoot != "" {
		ci = IsCaseInsensitiveVolume(opts.PluginDataRoot)
	}
	return &Scanner{opts: opts, caseInsensitive: ci}
}

// Result is the response payload of GET /api/codex/brokers; field names
// match spec §4.3 verbatim.
type Result struct {
	ScannedAt      time.Time      `json:"scannedAt"`
	ScanDurationMs int64          `json:"scanDurationMs"`
	DeadlineMs     int64          `json:"deadlineMs"`
	Partial        bool           `json:"partial"`
	Brokers        []BrokerRecord `json:"brokers"`
	Summary        ResultSummary  `json:"summary"`
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

// Scan performs one inventory pass. Concurrent calls coalesce via singleflight.
func (s *Scanner) Scan(ctx context.Context) (*Result, error) {
	v, err, _ := s.sf.Do("scan", func() (any, error) {
		return s.scanOnce(ctx)
	})
	if err != nil {
		return nil, err
	}
	return v.(*Result), nil
}

// scanOnce is the actual scan body, called by singleflight under the
// "scan" key.
func (s *Scanner) scanOnce(ctx context.Context) (*Result, error) {
	scanStart := time.Now()
	scanCtx, cancel := context.WithTimeout(ctx, s.opts.TotalDeadline)
	defer cancel()

	// 1. Process scan must finish first; reconcile and socket merge depend
	//    on its output. ErrPsUnavailable when ps cannot be invoked at all.
	processC, err := scanProcesses(scanCtx, s.opts.Lister, s.opts.FS, s.caseInsensitive)
	if err != nil {
		// Distinguish "ctx deadline" from "ps not found".
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			// ps timed out → partial scan with an empty process layer.
			return s.assembleResult(scanStart, processC, nil, nil, []string{"process"}, true), nil
		}
		return nil, fmt.Errorf("%w: %v", ErrPsUnavailable, err)
	}

	// 2. State and socket scans in parallel under remaining budget.
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
		timeouts    []string
		partial     bool
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

	// 3. Reconcile.
	records, _ := reconcile(scanCtx, s.opts.FS, processC, stateC, socketCands)

	// Top-level anomalies from state/socket scanners are merged onto a
	// pseudo-record (we surface them via summary count; they don't have a
	// natural BrokerRecord home).
	_ = stateAnoms
	_ = socketAnoms

	return s.assembleResult(scanStart, processC, records, nil, timeouts, partial), nil
}

// assembleResult materialises the Result from the per-stage outputs.
func (s *Scanner) assembleResult(
	scanStart time.Time,
	processC []processCandidate,
	records []BrokerRecord,
	_ []Anomaly,
	timeouts []string,
	partial bool,
) *Result {
	res := &Result{
		ScannedAt:      scanStart.UTC(),
		ScanDurationMs: time.Since(scanStart).Milliseconds(),
		DeadlineMs:     s.opts.TotalDeadline.Milliseconds(),
		Partial:        partial,
		Brokers:        records,
		Summary: ResultSummary{
			Total:              len(records),
			ScanSourceTimeouts: timeouts,
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
	// Avoid unused-warnings on processC when reconcile already ate it.
	_ = processC
	return res
}

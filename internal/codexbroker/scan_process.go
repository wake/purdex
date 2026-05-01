package codexbroker

import (
	"context"
	"strings"
	"time"
)

// processCandidate is the per-process intermediate produced by the process
// scanner. Reconcile (Task I) merges these with state-dir / socket
// candidates into a final BrokerRecord.
type processCandidate struct {
	Key            string
	PID            int
	PPID           int
	Lstart         time.Time
	LstartParseErr string
	RSSKB          int64
	RawCwd         string
	CwdResolved    string
	Endpoint       string
	PidFile        string
	Anomalies      []Anomaly
}

// brokerCmdlineToken is the literal substring that identifies a broker
// process row in ps output. The codex CLI invokes the broker via
// `node .../scripts/app-server-broker.mjs serve --cwd ...` so this
// substring uniquely identifies broker rows; child processes (`codex
// app-server`, the rust `codex` binary) do not contain it.
const brokerCmdlineToken = "app-server-broker.mjs"

// isBrokerRow returns true when the row is a broker process. We additionally
// require the literal "serve" subcommand token so historical/spawned-but-
// not-yet-execed shells are not falsely matched.
func isBrokerRow(r RawProcess) bool {
	if !strings.Contains(r.Cmdline, brokerCmdlineToken) {
		return false
	}
	// Conservative: require the `serve` keyword as a free-standing token
	// to avoid matching a path containing the substring "serve".
	for _, tok := range strings.Fields(r.Cmdline) {
		if tok == "serve" {
			return true
		}
	}
	return false
}

// scanProcesses runs the process-layer discovery pass: list all processes,
// filter to broker rows, parse argv, derive brokerKey, and produce one
// processCandidate per matching row.
//
// Per-row failures (argv truncated, EvalSymlinks failed, lstart unparseable)
// become anomalies on the candidate, not whole-scan errors. Whole-scan
// failures (lister returning err) bubble up so the orchestrator can decide
// 503 vs partial.
func scanProcesses(ctx context.Context, lister ProcessLister, fs FS) ([]processCandidate, error) {
	rows, err := lister.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]processCandidate, 0)
	for _, r := range rows {
		if !isBrokerRow(r) {
			continue
		}
		c := processCandidate{
			PID:            r.PID,
			PPID:           r.PPID,
			Lstart:         r.Lstart,
			LstartParseErr: r.LstartParseErr,
			RSSKB:          r.RSSKB,
		}
		if r.LstartParseErr != "" {
			c.Anomalies = append(c.Anomalies, Anomaly{
				Code:   AnomalyLstartUnparseable,
				Detail: r.LstartParseErr,
			})
		}
		argv, parseErr := ParseBrokerArgv(r.Cmdline)
		if parseErr != nil {
			c.Anomalies = append(c.Anomalies, Anomaly{
				Code:   AnomalyArgvTruncated,
				Detail: parseErr.Error(),
			})
		} else {
			c.RawCwd = argv.Cwd
			c.Endpoint = argv.Endpoint
			c.PidFile = argv.PidFile
		}

		// Compute brokerKey. If argv parse failed, fall back to a key derived
		// from the cmdline so duplicate detection still has *some* discriminator.
		keyInput := c.RawCwd
		if keyInput == "" {
			keyInput = r.Cmdline
		}
		// Even with parseErr we still call BrokerKey so tests can verify
		// CwdResolved / anomaly emission.
		key, resolved, anom := BrokerKey(keyInput, fs)
		c.Key = key
		c.CwdResolved = resolved
		if anom != nil && parseErr == nil {
			// Only attach cwd_unresolvable when we actually had a cwd to resolve.
			c.Anomalies = append(c.Anomalies, Anomaly{
				Code:   *anom,
				Detail: "EvalSymlinks failed",
			})
		}

		out = append(out, c)
	}
	return out, nil
}

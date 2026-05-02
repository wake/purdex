package codexbroker

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BrokerRecord is one runtime instance of a codex broker. Multiple records
// can share Key (when distinct (pid, lstart) tuples produce identical brokerKey
// after canonical-cwd hashing).
//
// The runtime identity unit is (Key, PID, Lstart); see spec §3.4.
type BrokerRecord struct {
	// Identity ----------------------------------------------------------
	Key    string    `json:"key"`              // brokerKey = sha256(realpath(cwd))[:16]; correlation only.
	PID    int       `json:"pid"`              // 0 if no live process matched.
	Lstart time.Time `json:"lstart,omitempty"` // zero if PID==0.

	// Process layer (zero if no process matched) ------------------------
	PPID        int    `json:"ppid,omitempty"`
	RSSBytes    int64  `json:"rssBytes,omitempty"`
	Cwd         string `json:"cwd,omitempty"`         // raw argv value
	CwdResolved string `json:"cwdResolved,omitempty"` // EvalSymlinks(Cwd); empty if unresolvable
	Endpoint    string `json:"endpoint,omitempty"`    // unix:<sock>
	SocketDir   string `json:"socketDir,omitempty"`
	PidFile     string `json:"pidFile,omitempty"`

	// State-directory layer (zero if no matching state dir) -------------
	StateDir          string `json:"stateDir,omitempty"`
	HasBrokerJSON     bool   `json:"hasBrokerJSON,omitempty"`
	BrokerJSONPID     int    `json:"brokerJSONPID,omitempty"`
	StateJSONReadable bool   `json:"stateJSONReadable,omitempty"`

	// Diagnostic-only fields. Not used as decision input in P1.
	JobCounts        JobCounts  `json:"jobCounts"`
	LastJobUpdatedAt *time.Time `json:"lastJobUpdatedAt,omitempty"`

	// Ownership signal (raw) --------------------------------------------
	CwdExists  bool   `json:"cwdExists"`
	CwdStatErr string `json:"cwdStatErr,omitempty"` // "" if CwdExists; otherwise classified.

	// Discovery diagnostics ---------------------------------------------
	Sources   SourceMask `json:"sources"`
	Anomalies []Anomaly  `json:"anomalies,omitempty"`
}

// JobCounts rolls up state.json.jobs[].status into bucket counts.
type JobCounts struct {
	Queued    int `json:"queued"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
	Unknown   int `json:"unknown"` // status not in known set; defends against schema drift.
}

// Anomaly is a structured diagnostic event attached to a BrokerRecord.
type Anomaly struct {
	Code   AnomalyCode `json:"code"`
	Detail string      `json:"detail,omitempty"`
}

// AnomalyCode is a typed string with a closed list of legal values; see
// spec §4.2. AllAnomalyCodes is the canonical literal slice; new codes
// require a spec amendment.
type AnomalyCode string

const (
	// AnomalyBrokerJSONPidMismatch fires when state-dir broker.json.pid does
	// not match the pid found in ps for the same brokerKey.
	AnomalyBrokerJSONPidMismatch AnomalyCode = "broker_json_pid_mismatch"

	// AnomalyStateDirOrphan fires when a state dir contains broker.json but
	// no live process matches its claimed pid.
	AnomalyStateDirOrphan AnomalyCode = "state_dir_orphan"

	// AnomalyProcessOrphan fires when a broker process is in ps but no
	// matching state dir exists.
	AnomalyProcessOrphan AnomalyCode = "process_orphan"

	// AnomalySocketOrphan fires when a cxc-* dir exists but the pid in
	// broker.pid is dead (or no live process owns the socket).
	AnomalySocketOrphan AnomalyCode = "socket_orphan"

	// AnomalyCwdUnresolvable fires when filepath.EvalSymlinks fails on the
	// argv --cwd value; raw cwd is preserved and used for hashing.
	AnomalyCwdUnresolvable AnomalyCode = "cwd_unresolvable"

	// AnomalyCwdMissing fires when os.Stat on the cwd path returns
	// definitive ENOENT.
	AnomalyCwdMissing AnomalyCode = "cwd_missing"

	// AnomalyCwdTransientStatError fires when os.Stat on cwd returns
	// ESTALE / EIO / EACCES / timeout / other (NOT definitive ENOENT).
	AnomalyCwdTransientStatError AnomalyCode = "cwd_transient_stat_error"

	// AnomalyLstartUnparseable fires when ps lstart format is unrecognised.
	AnomalyLstartUnparseable AnomalyCode = "lstart_unparseable"

	// AnomalyArgvTruncated fires when ps argv could not be parsed cleanly.
	AnomalyArgvTruncated AnomalyCode = "argv_truncated"

	// AnomalyStateJSONUnreadable fires when state.json is missing,
	// malformed, or unreadable.
	AnomalyStateJSONUnreadable AnomalyCode = "state_json_unreadable"

	// AnomalyBrokerJSONUnreadable fires when broker.json is present but
	// malformed or unreadable.
	AnomalyBrokerJSONUnreadable AnomalyCode = "broker_json_unreadable"

	// AnomalyDuplicateRuntime fires when multiple live processes share the
	// same brokerKey.
	AnomalyDuplicateRuntime AnomalyCode = "duplicate_runtime"

	// AnomalyBrokerKeyCollision fires when distinct canonical cwds hash to
	// the same brokerKey prefix.
	AnomalyBrokerKeyCollision AnomalyCode = "broker_key_collision"

	// AnomalyStateDirNoMatch fires when a process record could not be matched
	// to any state dir (process exists but state-dir lookup by suffix fails).
	AnomalyStateDirNoMatch AnomalyCode = "state_dir_no_match"

	// AnomalyForeignOwner is reserved for P2: a broker without a Purdex
	// launch-registry entry. P1 NEVER populates this code (P1 has no
	// positive-ownership signal); see spec §4.2 + plan task I.
	AnomalyForeignOwner AnomalyCode = "foreign_owner"
)

// AllAnomalyCodes is the canonical literal slice; tests assert it contains
// every code defined above and that runtime-emitted codes belong to this set.
var AllAnomalyCodes = []AnomalyCode{
	AnomalyBrokerJSONPidMismatch,
	AnomalyStateDirOrphan,
	AnomalyProcessOrphan,
	AnomalySocketOrphan,
	AnomalyCwdUnresolvable,
	AnomalyCwdMissing,
	AnomalyCwdTransientStatError,
	AnomalyLstartUnparseable,
	AnomalyArgvTruncated,
	AnomalyStateJSONUnreadable,
	AnomalyBrokerJSONUnreadable,
	AnomalyDuplicateRuntime,
	AnomalyBrokerKeyCollision,
	AnomalyStateDirNoMatch,
	AnomalyForeignOwner,
}

// SourceMask is a bitmask describing which discovery layers contributed to a
// BrokerRecord.
type SourceMask uint8

const (
	// SourceProcess: record was seen in ps output.
	SourceProcess SourceMask = 1 << iota
	// SourceStateDir: record had a matching state directory with broker.json.
	SourceStateDir
	// SourceSocket: record had a matching cxc-* socket directory.
	SourceSocket
)

// String returns a pipe-joined human label for the mask.
func (m SourceMask) String() string {
	if m == 0 {
		return "none"
	}
	var parts []string
	if m&SourceProcess != 0 {
		parts = append(parts, "process")
	}
	if m&SourceStateDir != 0 {
		parts = append(parts, "state-dir")
	}
	if m&SourceSocket != 0 {
		parts = append(parts, "socket")
	}
	return strings.Join(parts, "|")
}

// MarshalJSON renders SourceMask as its String() form for human-readable JSON.
func (m SourceMask) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

// UnmarshalJSON accepts the String() form back into a SourceMask.
func (m *SourceMask) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	var out SourceMask
	if s == "none" || s == "" {
		*m = 0
		return nil
	}
	for _, p := range strings.Split(s, "|") {
		switch p {
		case "process":
			out |= SourceProcess
		case "state-dir":
			out |= SourceStateDir
		case "socket":
			out |= SourceSocket
		}
	}
	*m = out
	return nil
}

// -- P2 type extensions (per plan §3 / task A) ----------------------------
//
// All P2 additions are additive — no existing P1 type is modified. These
// types power the decision layer (PredicateResult/DecisionResult), the
// quarantine + launch registry persistence (QuarantineEntry, LaunchEntry),
// the kill-sequence audit dump (AuditDump), and the predicate-A/B inputs
// (StateJobLite + ReadStateJobs helper).

// PredicateResult is the per-predicate evidence summary captured in the
// decision trace. The {A,B,C}Detail strings are short human-readable
// explanations suitable for audit dumps and operator dashboards.
type PredicateResult struct {
	A       bool   `json:"a"`
	ADetail string `json:"aDetail,omitempty"`
	B       bool   `json:"b"`
	BDetail string `json:"bDetail,omitempty"`
	C       bool   `json:"c"`
	CDetail string `json:"cDetail,omitempty"`
}

// DecisionResult composes the three predicates plus idle/override flags into
// the baseline kill verdict.
//
// Per round-3 finding #1: Kill reflects ONLY the baseline rule
// (¬A ∧ ¬B ∧ ¬C ∧ idleExpired). Foreign-quarantine classification is reported
// separately via Reason="foreign_quarantine" + AnomaliesAdded=[AnomalyForeignOwner].
// The sweep handler combines them per the kill-semantics table in plan §4 task Q.
type DecisionResult struct {
	Predicates     PredicateResult `json:"predicates"`
	IdleSeconds    int             `json:"idleSeconds"`
	OverrideE1     bool            `json:"overrideE1"`
	OverrideE2     bool            `json:"overrideE2"`
	Kill           bool            `json:"kill"`
	Reason         string          `json:"reason,omitempty"`
	AnomaliesAdded []AnomalyCode   `json:"anomaliesAdded,omitempty"`
}

// SweepRequest captures the parsed query parameters of POST /api/codex/brokers/sweep.
type SweepRequest struct {
	Mode      string `json:"mode"`
	BrokerKey string `json:"brokerKey,omitempty"`
}

// BrokerDecision pairs a BrokerRecord with its DecisionResult for the sweep
// response body.
type BrokerDecision struct {
	Broker   BrokerRecord   `json:"broker"`
	Decision DecisionResult `json:"decision"`
}

// SweepResponse is the JSON body returned by POST /api/codex/brokers/sweep.
//
// Quarantined holds brokerKeys that the sweep handler moved into E2
// quarantine (PR review finding C). A broker can appear in both Evaluated
// and Quarantined when its kill attempt aborts at Step 0 (PID-reuse race
// inside the kill sequence) — Applied stays empty because no signal
// reached the kernel.
type SweepResponse struct {
	DryRun      bool             `json:"dryRun"`
	Evaluated   []BrokerDecision `json:"evaluated"`
	Applied     []string         `json:"applied"`
	Quarantined []string         `json:"quarantined,omitempty"`
	Errors      []string         `json:"errors,omitempty"`
}

// BrokerFingerprint is the P2 broker identity tuple used for E2 PID-reuse
// detection (spec §5.3 line 400).
type BrokerFingerprint struct {
	Executable string `json:"executable"`
	Cmdline    string `json:"cmdline"`
	PidFile    string `json:"pidFile"`
}

// QuarantineEntry is one record in audit/quarantine.json. Schema matches
// spec §5.3 lines 408-422.
type QuarantineEntry struct {
	BrokerKey     string            `json:"brokerKey"`
	PID           int               `json:"pid"`
	Lstart        time.Time         `json:"lstart"`
	QuarantinedAt time.Time         `json:"quarantinedAt"`
	Reason        string            `json:"reason"`
	Fingerprint   BrokerFingerprint `json:"fingerprint"`
	ExpiresAt     time.Time         `json:"expiresAt"`
}

// QuarantineFile is the on-disk container for QuarantineEntry slices, with a
// schema version field so future migrations remain reversible.
type QuarantineFile struct {
	Version int               `json:"version"`
	Entries []QuarantineEntry `json:"entries"`
}

// LaunchEntry is one record in launch-registry.json. The registry maps a
// brokerKey to the originating tmux pane / caller session so predicate C
// can decide whether the broker is still purdex-owned.
type LaunchEntry struct {
	BrokerKey       string    `json:"brokerKey"`
	Pid             int       `json:"pid"`
	Lstart          time.Time `json:"lstart"`
	TmuxPane        string    `json:"tmuxPane"`
	CallerSessionID string    `json:"callerSessionId"`
	LaunchedAt      time.Time `json:"launchedAt"`
}

// LaunchRegistryFile is the on-disk container for LaunchEntry slices.
//
// P2 reads this file but does NOT write to it during sweep operations. The
// spawn-time write path is deferred to PR-G (Lights spawn-hook integration)
// per plan §11 promise #1. On mlab the file will initially be absent, which
// causes every visible broker to be classified as foreign_quarantine.
type LaunchRegistryFile struct {
	Version int           `json:"version"`
	Entries []LaunchEntry `json:"entries"`
}

// AuditKillSequence is the kill-result postscript appended to the audit
// preimage after the kill sequence completes. Spec §5.5 lines 495-500.
type AuditKillSequence struct {
	GracefulOk    bool     `json:"gracefulOk"`
	TermOk        bool     `json:"termOk"`
	KillOk        bool     `json:"killOk"`
	StepLatencyMs [3]int64 `json:"stepLatencyMs"`
}

// AuditDump is the full audit/orphan-<key>-<ts>.json payload. Spec §5.5
// lines 481-502.
type AuditDump struct {
	KilledAt      time.Time         `json:"killedAt"`
	Reason        string            `json:"reason"`
	Broker        BrokerRecord      `json:"broker"`
	Decision      DecisionResult    `json:"decision"`
	KillSequence  AuditKillSequence `json:"killSequence"`
	BrokerLogTail []string          `json:"brokerLogTail"`
}

// StateJobLite is the minimal per-job record extracted from
// state.json.jobs[]. P1's BrokerRecord only carries the rollup JobCounts +
// LastJobUpdatedAt; predicates A and B both need per-job detail.
//
// The *int Pid is essential for the spec §5.2 stale-running rule
// (spec lines 386-390): a nil pid past staleRunningThreshold is terminal-
// abandoned; a non-nil pid must be verified alive AND cmdline-matched.
type StateJobLite struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Pid         *int       `json:"pid"`
}

// E1State tracks the daemon-lifetime view of one broker's E1 ENOENT
// observations. Two ENOENT scans separated by ≥60 s confirm E1
// (spec §5.3 line 399). The struct lives in types.go so the tracker
// (Module field) and the override evaluator share one shape.
type E1State struct {
	BrokerKey        string     `json:"brokerKey"`
	FirstSeenAt      time.Time  `json:"firstSeenAt"`
	FirstConfirmedAt *time.Time `json:"firstConfirmedAt,omitempty"`
}

// stateJobsFile mirrors the subset of state.json that ReadStateJobs cares
// about; private so the public surface stays minimal.
type stateJobsFile struct {
	Jobs []StateJobLite `json:"jobs"`
}

// ReadStateJobs loads <stateDir>/state.json and decodes only the jobs[] slice
// into StateJobLite. Returns (nil, nil) when:
//   - the file is absent on disk (treated as "no jobs" — broker may simply
//     not have dispatched anything yet);
//   - the file exists but jobs[] is empty.
//
// Returns a parse error when the JSON is malformed; callers (predicate A
// composer in EvalDecision, task F) should treat parse errors as a degraded
// path and tag AnomalyStateJSONUnreadable.
func ReadStateJobs(fs FS, stateDir string) ([]StateJobLite, error) {
	if stateDir == "" {
		return nil, nil
	}
	rc, err := fs.Open(filepath.Join(stateDir, "state.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	var f stateJobsFile
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, err
	}
	if len(f.Jobs) == 0 {
		return nil, nil
	}
	return f.Jobs, nil
}

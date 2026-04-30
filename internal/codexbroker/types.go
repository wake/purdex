package codexbroker

import (
	"encoding/json"
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

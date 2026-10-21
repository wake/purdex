package wire

import "time"

// ---- the qualification state (WWD-7b2-0a): GET /v1/qualification, and the Purdex daemon's GET /api/pool/hosts/{alias}/qualification ----

// What a host holds of a task label now (QualificationTask.Hold). A reader draws a value it does not know as it comes.
const (
	QualHoldReference   = "reference"   // this host is the task's reference and advertises the label by the reference rule
	QualHoldEarned      = "earned"      // the label was earned by a pair and is advertised
	QualHoldQuarantined = "quarantined" // earned once, set aside; not advertised until the task is qualified again
	QualHoldNone        = "none"
)

// What the coordinator does next about a task (QualificationTask.Progress).
const (
	QualProgressPairing = "pairing" // a pair is in flight
	QualProgressWaiting = "waiting" // the last pair did not earn the label and the 24 h retry wait holds
	QualProgressReady   = "ready"   // the next facts tick starts a pair
	QualProgressBlocked = "blocked" // see Blocked
	QualProgressIdle    = "idle"    // nothing to do: the label is held (reference, earned), or the task is gone
)

// Why a task is blocked (QualificationBlocked.Code).
const (
	QualBlockedNoDefinition     = "no_definition"
	QualBlockedReferenceMissing = "reference_missing" // this host is the reference but lacks a label the task needs, so it does not hold the label
	QualBlockedWorkerOff        = "worker_off"
	QualBlockedMissing          = "missing" // lacks repo: / tool: labels (Labels)
)

// QualificationEarned is the row behind an earned or quarantined label.
type QualificationEarned struct {
	GrantedAt   time.Time         `json:"granted_at"`
	Fingerprint map[string]string `json:"fingerprint"`
	Snapshot    string            `json:"snapshot"`
}

// QualificationPair is the pair in flight: the two job ids and when it was queued.
type QualificationPair struct {
	Candidate string    `json:"candidate"`
	Reference string    `json:"reference"`
	Since     time.Time `json:"since"`
}

// QualificationLast is the last pair that did not earn the label. Why comes from the coordinator's memory and is absent after a restart.
type QualificationLast struct {
	Outcome string    `json:"outcome"` // mismatch | void
	At      time.Time `json:"at"`
	Why     string    `json:"why,omitempty"`
}

// QualificationBlocked says what stands in the way; Labels are the missing labels (for missing and reference_missing).
type QualificationBlocked struct {
	Code   string   `json:"code"`
	Labels []string `json:"labels,omitempty"`
}

// QualificationTask is one task of a host on two axes: Hold (what the host has) and Progress (what happens next). Fields that a value does
// not need are absent. Reference is empty for a task that is gone (the manifest no longer says).
type QualificationTask struct {
	Label     string                `json:"label"`
	Reference string                `json:"reference,omitempty"`
	Gone      bool                  `json:"gone,omitempty"`
	Hold      string                `json:"hold"`
	Progress  string                `json:"progress"`
	Earned    *QualificationEarned  `json:"earned,omitempty"`
	Pair      *QualificationPair    `json:"pair,omitempty"`
	Last      *QualificationLast    `json:"last,omitempty"`
	RetryAt   *time.Time            `json:"retry_at,omitempty"`
	Blocked   *QualificationBlocked `json:"blocked,omitempty"`
}

// HostQualification is the body of GET /v1/qualification: the host's tasks (sorted by label), the repos whose manifest could not be read (their
// tasks are not listed), and the last pairs that ended, newest first.
type HostQualification struct {
	Host       string              `json:"host"`
	Tasks      []QualificationTask `json:"tasks"`
	Unreadable []string            `json:"unreadable,omitempty"`
	Recent     []QualifyRecord     `json:"recent"`
}

// PoolHostQualification is the Purdex daemon's GET /api/pool/hosts/{alias}/qualification: HostQualification with Known, false when the host's
// wwd predates the route (it answered 404): nothing is claimed about it and the lists are empty.
type PoolHostQualification struct {
	Host       string              `json:"host"`
	Known      bool                `json:"known"`
	Tasks      []QualificationTask `json:"tasks"`
	Unreadable []string            `json:"unreadable,omitempty"`
	Recent     []QualifyRecord     `json:"recent"`
}

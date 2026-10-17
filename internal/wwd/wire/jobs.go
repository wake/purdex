package wire

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

type Repo struct {
	Name   string `json:"name"`
	Root   string `json:"root"`
	Remote string `json:"remote"`
}

type Snapshot struct {
	Commit string `json:"commit"`
	Tree   string `json:"tree"`
	Base   string `json:"base"`
	RelCwd string `json:"rel_cwd"`
}

type Submitter struct {
	Session string `json:"session"`
	Ref     string `json:"ref"`
	Team    string `json:"team"`
	Host    string `json:"host"`
}

type Claim struct {
	Worker  string    `json:"worker"`
	Attempt int       `json:"attempt"`
	Since   time.Time `json:"since"`
}

// Failure is one failed test in a Summary.
type Failure struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Message string `json:"message"`
}

// Summary is the parsed task report (spec §5).
type Summary struct {
	Passed   int       `json:"passed"`
	Failed   int       `json:"failed"`
	Skipped  int       `json:"skipped"`
	Failures []Failure `json:"failures"`
	Tail     []string  `json:"tail"`
}

// Result is a job's stored outcome (spec §4.1). Exit is null when no command
// outcome exists (cancelled, expired); SupersededBy is null unless superseded.
// Reason is a free string, not an enumeration: lost, oom, timeout, infra, queue_timeout… (WWD-0 M-L classes).
type Result struct {
	Exit         *int       `json:"exit"`
	Summary      *Summary   `json:"summary"`
	Log          string     `json:"log"`
	Host         string     `json:"host"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	CacheHit     bool       `json:"cache_hit"`
	Reason       string     `json:"reason"`
	SupersededBy *string    `json:"superseded_by"`
}

// Job is the queue record (spec §4.1). It never carries a claim_id.
type Job struct {
	ID           string   `json:"id"`
	Origin       string   `json:"origin"`
	Repo         Repo     `json:"repo"`
	Snapshot     Snapshot `json:"snapshot"`
	Task         string   `json:"task"`
	Args         []string `json:"args"`
	Class        JobClass `json:"class"`
	Labels       []string `json:"labels"`
	Locks        []string `json:"locks"`
	Cacheable    bool     `json:"cacheable"`
	TimeoutS     int      `json:"timeout_s"`
	SupersedeKey string   `json:"supersede_key"`
	// Interruptible, QueueTimeoutS and AdHoc are properties of the task that the submitter copies from the manifest at submit time
	// (the origin never rereads a manifest). Interruptible: a newer job with the same supersede_key may stop this one while it runs.
	// QueueTimeoutS: 0 means the default (2 h). AdHoc: an undeclared command (spec §8.3); it is never listed to peers.
	Interruptible bool      `json:"interruptible"`
	QueueTimeoutS int       `json:"queue_timeout_s"`
	AdHoc         bool      `json:"adhoc"`
	Submitter     Submitter `json:"submitter"`
	CreatedAt     time.Time `json:"created_at"`
	State         JobState  `json:"state"`
	Claim         *Claim    `json:"claim"`
	Result        *Result   `json:"result"`
}

// JobSubmission is the body of POST /v1/jobs (local socket only, spec §4.4): what the submitter fixes. The origin assigns the
// id, state, created_at, claim and result, so a submission carrying any of them is rejected.
type JobSubmission struct {
	Repo          Repo      `json:"repo"`
	Snapshot      Snapshot  `json:"snapshot"`
	Task          string    `json:"task"`
	Args          []string  `json:"args"`
	Class         JobClass  `json:"class"`
	Labels        []string  `json:"labels"`
	Locks         []string  `json:"locks"`
	Cacheable     bool      `json:"cacheable"`
	TimeoutS      int       `json:"timeout_s"`
	SupersedeKey  string    `json:"supersede_key"`
	Interruptible bool      `json:"interruptible"`
	QueueTimeoutS int       `json:"queue_timeout_s"`
	AdHoc         bool      `json:"adhoc"`
	Submitter     Submitter `json:"submitter"`
}

func (s JobSubmission) Validate() error {
	if !s.Class.Valid() {
		return fmt.Errorf("submission: invalid class %q", s.Class)
	}
	if err := validatePayload(s.Task, s.Repo, s.Snapshot, s.TimeoutS, s.QueueTimeoutS); err != nil {
		return fmt.Errorf("submission: %w", err)
	}
	return nil
}

func (j Job) Validate() error {
	if !j.Class.Valid() {
		return fmt.Errorf("job: invalid class %q", j.Class)
	}
	if !j.State.Valid() {
		return fmt.Errorf("job: invalid state %q", j.State)
	}
	if j.ID == "" || j.Origin == "" {
		return fmt.Errorf("job: missing id or origin")
	}
	// the same payload checks as a submission: a job that cannot be indexed, fetched or run is refused at the boundary
	return validatePayload(j.Task, j.Repo, j.Snapshot, j.TimeoutS, j.QueueTimeoutS)
}

func validatePayload(task string, repo Repo, snap Snapshot, timeoutS, queueTimeoutS int) error {
	switch {
	case task == "":
		return fmt.Errorf("missing task")
	case repo.Name == "":
		return fmt.Errorf("missing repo name")
	case snap.Commit == "":
		return fmt.Errorf("missing snapshot commit")
	case timeoutS < 0:
		return fmt.Errorf("negative timeout_s")
	case queueTimeoutS < 0:
		return fmt.Errorf("negative queue_timeout_s")
	}
	return nil
}

// ---- claim / lease calls (spec §4.3) ----

var claimIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NewClaimID returns a fresh 128-bit random claim id as 32 lowercase hex chars.
func NewClaimID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("wire: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// ValidClaimID reports whether s has the claim id format.
func ValidClaimID(s string) bool { return claimIDRe.MatchString(s) }

func checkClaimID(s string) error {
	if !ValidClaimID(s) {
		return fmt.Errorf("invalid claim_id")
	}
	return nil
}

type ClaimRequest struct {
	Worker      string            `json:"worker"`
	Labels      []string          `json:"labels"`
	Fingerprint map[string]string `json:"fingerprint"`
}

func (r ClaimRequest) Validate() error {
	if r.Worker == "" {
		return fmt.Errorf("claim: missing worker")
	}
	return nil
}

type ClaimResponse struct {
	Job     Job    `json:"job"`
	ClaimID string `json:"claim_id"`
	LeaseS  int    `json:"lease_s"`
	RenewS  int    `json:"renew_s"`
}

func (r ClaimResponse) Validate() error {
	if err := checkClaimID(r.ClaimID); err != nil {
		return err
	}
	return r.Job.Validate()
}

type StartRequest struct {
	ClaimID string `json:"claim_id"`
}

func (r StartRequest) Validate() error { return checkClaimID(r.ClaimID) }

// Lease is the answer to start.
type Lease struct {
	LeaseS int `json:"lease_s"`
	RenewS int `json:"renew_s"`
}

type HeartbeatRequest struct {
	ClaimID string `json:"claim_id"`
}

func (r HeartbeatRequest) Validate() error { return checkClaimID(r.ClaimID) }

type HeartbeatResponse struct {
	LeaseS int  `json:"lease_s"`
	RenewS int  `json:"renew_s"`
	Cancel bool `json:"cancel"`
}

type ReleaseRequest struct {
	ClaimID string        `json:"claim_id"`
	Reason  ReleaseReason `json:"reason"`
	Detail  string        `json:"detail"`
}

func (r ReleaseRequest) Validate() error {
	if err := checkClaimID(r.ClaimID); err != nil {
		return err
	}
	if !r.Reason.Valid() {
		return fmt.Errorf("release: invalid reason %q", r.Reason)
	}
	return nil
}

type ResultRequest struct {
	ClaimID    string     `json:"claim_id"`
	State      JobState   `json:"state"`
	Exit       *int       `json:"exit"`
	Summary    *Summary   `json:"summary"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

func (r ResultRequest) Validate() error {
	if err := checkClaimID(r.ClaimID); err != nil {
		return err
	}
	if !r.State.ResultState() {
		return fmt.Errorf("result: state %q cannot be posted", r.State)
	}
	return nil
}

// Conflict409 is the body of a 409: the job's current state.
type Conflict409 struct {
	State JobState `json:"state"`
}

func (c Conflict409) Validate() error {
	if !c.State.Valid() {
		return fmt.Errorf("conflict: invalid state %q", c.State)
	}
	return nil
}

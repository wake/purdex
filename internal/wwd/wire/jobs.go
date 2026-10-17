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
	ID           string    `json:"id"`
	Origin       string    `json:"origin"`
	Repo         Repo      `json:"repo"`
	Snapshot     Snapshot  `json:"snapshot"`
	Task         string    `json:"task"`
	Args         []string  `json:"args"`
	Class        JobClass  `json:"class"`
	Labels       []string  `json:"labels"`
	Locks        []string  `json:"locks"`
	Cacheable    bool      `json:"cacheable"`
	TimeoutS     int       `json:"timeout_s"`
	SupersedeKey string    `json:"supersede_key"`
	Submitter    Submitter `json:"submitter"`
	CreatedAt    time.Time `json:"created_at"`
	State        JobState  `json:"state"`
	Claim        *Claim    `json:"claim"`
	Result       *Result   `json:"result"`
}

func (j Job) Validate() error {
	if !j.Class.Valid() {
		return fmt.Errorf("job: invalid class %q", j.Class)
	}
	if !j.State.Valid() {
		return fmt.Errorf("job: invalid state %q", j.State)
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

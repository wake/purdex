package wire

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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
	// PassedDigest identifies which tests passed and which failed (WWD-5b-2w: the lines "P <id>" and "F <id>", sorted; see summary/digest.go
	// for the exact form, which the parsers own, and for when a report has none). Originally "the set of tests that passed": "sha256:" and the hex of the SHA-256 of the sorted passed test ids, each
	// followed by a newline (WWD-5b-2r; the summary parsers fill it from 5b-2w on, only while every pool host reports proto >= 3).
	// Empty: not computed (an older writer, or a task whose report has no per-test ids). Qualification (spec §7) compares it and treats
	// a missing one on either half as a mismatch.
	PassedDigest string `json:"passed_digest,omitempty"`
}

var rePassedDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Validate checks the digest form; the counts and lists are the parsers' own.
func (s *Summary) Validate() error {
	if s == nil {
		return nil
	}
	if s.PassedDigest != "" && !rePassedDigest.MatchString(s.PassedDigest) {
		return fmt.Errorf("summary: passed_digest %q is not sha256:<64 hex>", s.PassedDigest)
	}
	return nil
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
	Interruptible bool `json:"interruptible"`
	QueueTimeoutS int  `json:"queue_timeout_s"`
	AdHoc         bool `json:"adhoc"`
	// Local marks a fast-lane job (spec 5.2): it runs in the submitter's own working tree on this host, takes no snapshot, never
	// leaves the host, is never listed to peers and never read from or written to the result cache.
	Local     bool      `json:"local"`
	Submitter Submitter `json:"submitter"`
	CreatedAt time.Time `json:"created_at"`
	State     JobState  `json:"state"`
	Claim     *Claim    `json:"claim"`
	Result    *Result   `json:"result"`
	// Manual and ManualSeq are the manual place of a queued job (spec §4.6): "" (the score decides), front, back or hold. Held jobs are
	// not handed out; fronts go before every scored job and backs after, each by ManualSeq, smallest first (a later front has a smaller
	// one than the fronts before it, a later back a larger one). ManualSeq is 0 when Manual is empty or hold.
	Manual    Position `json:"manual"`
	ManualSeq int64    `json:"manual_seq"`
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
	// DefHash is the manifest's definition hash of the task (manifest.Task.DefinitionHash): what a stored result is compared on
	// (spec §4.5). NoCache asks the origin not to answer this submission from a stored result. Both are the submitter's word on its
	// own host's socket; neither reaches a Job or a peer.
	DefHash string `json:"def_hash,omitempty"`
	NoCache bool   `json:"no_cache,omitempty"`
	// GitDir is the repo's common git directory as an absolute path (git rev-parse --path-format=absolute --git-common-dir), not the
	// working tree: a member's worktree may be removed before the job runs, and the snapshot's objects live in the common directory
	// (spec 4.1). It is queue-private: a Job has no such field, so it can never reach a listing, an event or a peer.
	GitDir string `json:"git_dir"`
}

func (s JobSubmission) Validate() error {
	if !s.Class.Valid() {
		return fmt.Errorf("submission: invalid class %q", s.Class)
	}
	if err := validatePayload(s.Task, s.Repo, s.Snapshot, s.TimeoutS, s.QueueTimeoutS); err != nil {
		return fmt.Errorf("submission: %w", err)
	}
	if !filepath.IsAbs(s.GitDir) {
		return fmt.Errorf("submission: git_dir must be an absolute path, got %q", s.GitDir)
	}
	return nil
}

// LocalRunRequest registers a fast-lane run (spec 5.2): POST /v1/local-runs, local socket only. It is a submission whose snapshot is
// the working tree's HEAD (only a reference: the tree may hold uncommitted changes, so it is never used for reuse or transfer). A
// local run is never cacheable.
type LocalRunRequest struct {
	JobSubmission
}

func (r LocalRunRequest) Validate() error {
	if err := r.JobSubmission.Validate(); err != nil {
		return err
	}
	if r.Cacheable {
		return fmt.Errorf("local run: a fast-lane run is never cacheable")
	}
	return nil
}

// LocalRunResponse is the registered run's claim: the runner heartbeats, posts its result and uploads its log with it, like any worker.
type LocalRunResponse struct {
	ID      string `json:"id"`
	ClaimID string `json:"claim_id"`
	LeaseS  int    `json:"lease_s"`
	RenewS  int    `json:"renew_s"`
}

func (r LocalRunResponse) Validate() error {
	switch {
	case r.ID == "":
		return fmt.Errorf("local run: missing id")
	case checkClaimID(r.ClaimID) != nil:
		return fmt.Errorf("local run: invalid claim_id")
	case r.LeaseS <= 0 || r.RenewS <= 0:
		return fmt.Errorf("local run: lease_s and renew_s must be positive")
	}
	return nil
}

// LocksBusy is the 409 body of a local-run registration whose lock keys are held on this host: the caller waits and asks again
// (a fast-lane task never goes to the queue, D12). Keys are the held keys; Holders say which job holds each (spec §5.2).
type LocksBusy struct {
	Reason  string       `json:"reason"`
	Keys    []string     `json:"keys"`
	Holders []LockHolder `json:"holders"`
}

// SlotBusy is the 429 body of a local-run registration that finds no slot on this host (spec §5.2): the caller waits and asks again,
// like for LocksBusy. Slot is the slot the run takes ("light" for a declared task, "heavy" for an adhoc command); Reasons say why it
// has no room now (the reasons of the headroom, spec §3.2).
type SlotBusy struct {
	Reason  string   `json:"reason"`
	Slot    string   `json:"slot"`
	Reasons []string `json:"reasons"`
}

// SlotBusyReason is the only reason a SlotBusy carries.
const SlotBusyReason = "slot_busy"

// LockHolder is the job that holds a lock key.
type LockHolder struct {
	Key string `json:"key"`
	Job string `json:"job"`
}

// LocksBusyReason is the only reason a LocksBusy carries.
const LocksBusyReason = "locks_busy"

func (b LocksBusy) Validate() error {
	if b.Reason != LocksBusyReason {
		return fmt.Errorf("locks busy: unexpected reason %q", b.Reason)
	}
	if len(b.Keys) == 0 {
		return fmt.Errorf("locks busy: no keys")
	}
	if len(b.Holders) == 0 {
		return nil // a daemon from before holders were named: the keys alone are enough to wait on
	}
	if len(b.Holders) != len(b.Keys) {
		return fmt.Errorf("locks busy: %d holders for %d keys", len(b.Holders), len(b.Keys))
	}
	for i, h := range b.Holders {
		if h.Key != b.Keys[i] || h.Job == "" {
			return fmt.Errorf("locks busy: holder %d is %q/%q, want key %q and a job", i, h.Key, h.Job, b.Keys[i])
		}
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
	if strings.Contains(j.ID, "/") {
		// a worker names a job "source/id" (worker/ident): an alias may hold a slash, so an id that did too would make two jobs one key
		return fmt.Errorf("job: id %q holds a slash", j.ID)
	}
	if j.Local && j.Cacheable {
		return fmt.Errorf("job: a local job is never cacheable")
	}
	if !j.Manual.ValidOnJob() {
		return fmt.Errorf("job: invalid manual place %q", j.Manual)
	}
	// the number fits the place: none and hold have none, a front is below zero and a back above it
	if (j.Manual == "" || j.Manual == PositionHold) != (j.ManualSeq == 0) ||
		(j.Manual == PositionFront && j.ManualSeq > 0) || (j.Manual == PositionBack && j.ManualSeq < 0) {
		return fmt.Errorf("job: manual_seq %d does not fit the place %q", j.ManualSeq, j.Manual)
	}
	if err := j.Result.Validate(); err != nil {
		return err
	}
	// the same payload checks as a submission: a job that cannot be indexed, fetched or run is refused at the boundary
	return validatePayload(j.Task, j.Repo, j.Snapshot, j.TimeoutS, j.QueueTimeoutS)
}

// Validate checks what a stored result carries that has a form of its own: the summary's digest. Nil-safe, so a job that has no result
// yet passes. A result read back from a peer or from the cache goes through here, as a posted one goes through ResultRequest.Validate.
func (r *Result) Validate() error {
	if r == nil {
		return nil
	}
	return r.Summary.Validate()
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

// PositionRequest is the body of POST /v1/jobs/{id}/position (local socket only, spec §4.6): the manual place to put a queued job in.
type PositionRequest struct {
	Position Position `json:"position"`
}

func (r PositionRequest) Validate() error {
	if !r.Position.ValidRequest() {
		return fmt.Errorf("position %q is not front, back, hold or normal", r.Position)
	}
	return nil
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
	// Reason is why a run failed, in the words a worker may write: oom, timeout or infra (spec 4.1). Empty is the normal case. The
	// origin's own words (lost, queue_timeout, superseded_by:<id>) are not accepted here.
	Reason string `json:"reason,omitempty"`
}

// ResultReasons are the reasons a worker may post.
var ResultReasons = []string{"oom", "timeout", "infra"}

func (r ResultRequest) Validate() error {
	if err := checkClaimID(r.ClaimID); err != nil {
		return err
	}
	if !r.State.ResultState() {
		return fmt.Errorf("result: state %q cannot be posted", r.State)
	}
	if err := r.Summary.Validate(); err != nil {
		return err
	}
	if r.Reason != "" && !slices.Contains(ResultReasons, r.Reason) {
		return fmt.Errorf("result: reason %q is not one a worker may post", r.Reason)
	}
	if r.Reason != "" && r.State != StateFailed {
		return fmt.Errorf("result: reason %q goes with state failed, not %q", r.Reason, r.State)
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

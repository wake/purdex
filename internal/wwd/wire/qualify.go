package wire

import (
	"fmt"
	"time"
)

// ---- qualification (spec §7): POST /v1/qualify and GET /v1/qualify, on the local socket only ----

// QualifyRequest is the body of POST /v1/qualify: a person asks this host to start a qualification pair for a task of one of its prepared
// clones, <repo>/<task> (the same name as the task label without its prefix).
type QualifyRequest struct {
	Task string `json:"task"`
}

// Validate checks that Task is <repo>/<task> in the shape a task label has.
func (r QualifyRequest) Validate() error {
	if !ValidTaskLabel("task:" + r.Task) {
		return fmt.Errorf("qualify: %q is not <repo>/<task>", r.Task)
	}
	return nil
}

// QualifyStarted is a pair that was queued: the task label and the two jobs (the host's own half and the reference's).
type QualifyStarted struct {
	Label     string `json:"label"`
	Candidate string `json:"candidate"`
	Reference string `json:"reference"`
}

// QualifyRecord is how a pair ended (or why one was not started).
type QualifyRecord struct {
	Label     string    `json:"label"`
	Outcome   string    `json:"outcome"` // match | mismatch | void
	Why       string    `json:"why"`
	At        time.Time `json:"at"`
	Candidate string    `json:"candidate"` // job ids; empty when no job was queued
	Reference string    `json:"reference"`
}

// QualifyStatus is the body of GET /v1/qualify: the pairs in flight and the last ones that ended, newest first.
type QualifyStatus struct {
	InFlight []QualifyStarted `json:"in_flight"`
	Recent   []QualifyRecord  `json:"recent"`
}

// Validate checks a status as a reader must before it shows it.
func (s QualifyStatus) Validate() error {
	for i, p := range s.InFlight {
		if !ValidTaskLabel(p.Label) {
			return fmt.Errorf("qualify status: in_flight[%d] label %q", i, p.Label)
		}
	}
	for i, r := range s.Recent {
		switch {
		case !ValidTaskLabel(r.Label):
			return fmt.Errorf("qualify status: recent[%d] label %q", i, r.Label)
		case r.Outcome != "match" && r.Outcome != "mismatch" && r.Outcome != "void":
			return fmt.Errorf("qualify status: recent[%d] outcome %q", i, r.Outcome)
		}
	}
	return nil
}

package team

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	ipeers "github.com/wake/purdex/internal/peers"
)

// ---- T: reports (spec D-3) ----

// ReportKind is what a member's report says.
type ReportKind string

const (
	ReportAck      ReportKind = "ack"      // received, starting      -> task in_progress
	ReportProgress ReportKind = "progress" // a step done
	ReportQuestion ReportKind = "question" // needs a decision (needs)
	ReportReady    ReportKind = "ready"    // PR reviewed, asks to merge (pr, reviews) -> metadata.prs
	ReportMerged   ReportKind = "merged"   // merged (pr, sha)        -> metadata.shas
	ReportBlocked  ReportKind = "blocked"  // cannot proceed (needs)
	ReportDone     ReportKind = "done"     // done-when met           -> task completed
)

// ValidReportKind reports whether k is one of the seven kinds.
func ValidReportKind(k ReportKind) bool {
	switch k {
	case ReportAck, ReportProgress, ReportQuestion, ReportReady, ReportMerged, ReportBlocked, ReportDone:
		return true
	}
	return false
}

// Report field limits.
const (
	MaxReportSummaryRunes = 200
	MaxReportBodyLen      = 32 * 1024 // bytes
	MaxReportReviews      = 10
	MaxReportReviewRunes  = 200
	minReportSHALen       = 7
	maxReportSHALen       = 40
)

// ReportRequest is what the CLI posts to report on a task. ID is the
// idempotency key (a UUID, kept on a retry); Task may be empty, in which case
// the daemon uses the member's only in_progress task.
type ReportRequest struct {
	ID      string     `json:"id,omitempty"`
	Task    string     `json:"task,omitempty"`
	Kind    ReportKind `json:"kind"`
	Summary string     `json:"summary"`
	Needs   string     `json:"needs,omitempty"`   // question, blocked: lead | user
	PR      int        `json:"pr,omitempty"`      // ready, merged
	Reviews []string   `json:"reviews,omitempty"` // ready: "stage=job"
	SHA     string     `json:"sha,omitempty"`     // merged
	Body    string     `json:"body,omitempty"`
}

// Report is the wire view of one stored report. Task is the display id and
// Member the reporting member as it is at view time.
type Report struct {
	ID        string     `json:"id"`
	Task      string     `json:"task"`
	Kind      ReportKind `json:"kind"`
	Summary   string     `json:"summary"`
	Needs     string     `json:"needs,omitempty"`
	PR        int        `json:"pr,omitempty"`
	Reviews   []string   `json:"reviews,omitempty"`
	SHA       string     `json:"sha,omitempty"`
	Body      string     `json:"body,omitempty"`
	Member    TaskOwner  `json:"member"`
	CreatedAt int64      `json:"created_at"`
}

// ValidReportID checks a report id: a lower-case 8-4-4-4-12 UUID of version 4
// (the version digit is 4) and the RFC 4122 variant (the variant digit is 8,
// 9, a or b), which is what every id generator in the repo produces. An
// all-zero id or a version 1 id is refused.
func ValidReportID(id string) error {
	if !ipeers.IsUUID(id) || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) {
		return fmt.Errorf("id must be a lower-case UUID v4, got %q", id)
	}
	return nil
}

// validReportReview checks one "stage=job" entry: exactly one '=', both parts non-empty, no
// whitespace or control character, at most 200 runes in all.
func validReportReview(i int, e string) error {
	what := fmt.Sprintf("reviews[%d]", i)
	if err := ipeers.ValidateText(e); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n := utf8.RuneCountInString(e); n > MaxReportReviewRunes {
		return fmt.Errorf("%s is %d runes, at most %d", what, n, MaxReportReviewRunes)
	}
	for _, r := range e {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain whitespace or control characters, it has %U", what, r)
		}
	}
	if strings.Count(e, "=") != 1 {
		return fmt.Errorf("%s must look like stage=job with exactly one '=', got %q", what, e)
	}
	stage, job, _ := strings.Cut(e, "=")
	if stage == "" || job == "" {
		return fmt.Errorf("%s must look like stage=job (both parts non-empty), got %q", what, e)
	}
	return nil
}

// validReportSHA checks a commit id: 7-40 hex digits, either case. The store
// keeps it lower-case.
func validReportSHA(s string) error {
	if len(s) < minReportSHALen || len(s) > maxReportSHALen {
		return fmt.Errorf("sha must be %d-%d hex digits, got %d characters", minReportSHALen, maxReportSHALen, len(s))
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return fmt.Errorf("sha must be hex digits, it has %q", c)
		}
	}
	return nil
}

// ValidateReport checks a report request per spec D-3. summary and body are
// accepted by every kind; needs, pr, reviews and sha belong to some kinds
// only, and a field on a kind that does not take it is an error. Every error
// starts with the name of the offending field, so the CLI can print it as is
// (exit 2). The id is checked apart, by ValidReportID.
func ValidateReport(r ReportRequest) error {
	if !ValidReportKind(r.Kind) {
		return fmt.Errorf("kind must be one of ack, progress, question, ready, merged, blocked, done, got %q", string(r.Kind))
	}
	if err := validLine("summary", r.Summary, MaxReportSummaryRunes); err != nil {
		return err
	}
	if err := validMultiline("body", r.Body, MaxReportBodyLen); err != nil {
		return err
	}

	takesNeeds := r.Kind == ReportQuestion || r.Kind == ReportBlocked
	takesPR := r.Kind == ReportReady || r.Kind == ReportMerged
	switch {
	case takesNeeds && r.Needs != "lead" && r.Needs != "user":
		return fmt.Errorf("needs is required for %s and must be lead or user, got %q", r.Kind, r.Needs)
	case !takesNeeds && r.Needs != "":
		return fmt.Errorf("needs does not belong to a %s report (only question and blocked)", r.Kind)
	}
	switch {
	case takesPR && r.PR <= 0:
		return fmt.Errorf("pr is required for %s and must be positive, got %d", r.Kind, r.PR)
	case !takesPR && r.PR != 0:
		return fmt.Errorf("pr does not belong to a %s report (only ready and merged)", r.Kind)
	}
	if r.Kind == ReportReady {
		if len(r.Reviews) == 0 {
			return errors.New("reviews is required for ready: at least one stage=job entry")
		}
		if len(r.Reviews) > MaxReportReviews {
			return fmt.Errorf("reviews has %d entries, at most %d", len(r.Reviews), MaxReportReviews)
		}
		for i, e := range r.Reviews {
			if err := validReportReview(i, e); err != nil {
				return err
			}
		}
	} else if len(r.Reviews) != 0 {
		return fmt.Errorf("reviews does not belong to a %s report (only ready)", r.Kind)
	}
	if r.Kind == ReportMerged {
		if r.SHA == "" {
			return errors.New("sha is required for merged: 7-40 hex digits")
		}
		if err := validReportSHA(r.SHA); err != nil {
			return err
		}
	} else if r.SHA != "" {
		return fmt.Errorf("sha does not belong to a %s report (only merged)", r.Kind)
	}
	return nil
}

package wire

import (
	"regexp"
	"time"
)

// Earned label states (spec §7). An earned label is advertised; a quarantined one is kept on record and is not, until the task is
// qualified again.
const (
	EarnedLabelEarned      = "earned"
	EarnedLabelQuarantined = "quarantined"
)

// EarnedLabel is one `task:<repo>/<task>` label a host earned by qualification (or lost to a disagreement), with the fingerprint it
// was earned under.
type EarnedLabel struct {
	Label       string            `json:"label"`
	State       string            `json:"state"`
	Fingerprint map[string]string `json:"fingerprint"`
	Snapshot    string            `json:"snapshot"`
	GrantedAt   time.Time         `json:"granted_at"`
}

// EarnedLabelList is the body of GET /v1/earned-labels (local socket only): this host's earned and quarantined labels, oldest label
// name first.
type EarnedLabelList struct {
	Host   string        `json:"host"`
	Labels []EarnedLabel `json:"labels"`
}

// The repo part follows the repo names of a host's config; the task part is the manifest's task name rule (^[a-z0-9][a-z0-9_-]*$) with
// no length of its own, so the whole label is bounded instead.
var reTaskLabel = regexp.MustCompile(`^task:[A-Za-z0-9][A-Za-z0-9._-]{0,63}/[a-z0-9][a-z0-9_-]*$`)

// MaxTaskLabelBytes bounds a whole task label.
const MaxTaskLabelBytes = 200

// ValidTaskLabel says whether s is a task label, task:<repo>/<task>: the only kind a host earns. Anything else in an earned_labels row
// (an os:, tool: or repo: label, blanks, an over-long value) is damage and is neither stored nor advertised.
func ValidTaskLabel(s string) bool { return len(s) <= MaxTaskLabelBytes && reTaskLabel.MatchString(s) }

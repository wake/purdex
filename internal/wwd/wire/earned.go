package wire

import "time"

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

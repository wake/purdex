package convfeed

import "time"

// ExecOwnerKey is the service-registry key under which the nex module publishes its execution-owner lookup and the
// conversation module reads it: the two share this package, never each other.
const ExecOwnerKey = "nex.conversation-owner"

// ExecOwner is the live worker execution that runs a Claude session: a neutral value type, so the module that finds it
// (nex) and the one that reads it (conversation) share it without importing each other.
type ExecOwner struct {
	ExecID         string
	Light          string // running | waiting | idle | error | unknown
	TranscriptPath string // the row's transcript_path; may be empty
	Live           bool
	At             time.Time // when Light was read
}

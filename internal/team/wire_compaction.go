package team

// Per-session compaction control (Phase 2a). A nil field in CompactionOverride means "follow the host default",
// so the JSON keeps null instead of dropping the key.
const (
	CompactionRoute       = "/api/team/compaction/{session_id}"
	MemberCompactionRoute = "/api/team/member-compaction"
	CapabilityCompaction  = "team.compaction.v1"

	ErrCompactUnsupported = "compact_unsupported" // the member's mod does not know the compact job
	ErrCompactFailed      = "compact_failed"      // the compact job was dropped, or the member's mod did not answer

	CompactionReasonSelfRelay  = "self_relay"
	CompactionReasonMemberAuto = "member_auto"
)

type CompactionOverride struct {
	Auto      *bool `json:"auto"`
	Threshold *int  `json:"threshold"`
	Idle      *bool `json:"idle"`
}

type CompactionEffective struct {
	Managed   bool   `json:"managed"`
	Reason    string `json:"reason,omitempty"`
	Auto      bool   `json:"auto"`
	Threshold int    `json:"threshold"`
	Idle      bool   `json:"idle"`
}

type CompactionView struct {
	Role      string              `json:"role"`
	Override  CompactionOverride  `json:"override"`
	Effective CompactionEffective `json:"effective"`
}

// MemberCompactionRequest is POST /api/team/member-compaction: a lead reads (neither Set nor Reset), merges Set into,
// or clears the override of one of its members.
type MemberCompactionRequest struct {
	OriginInbox string              `json:"origin_inbox"`
	Target      string              `json:"target"`
	Set         *CompactionOverride `json:"set,omitempty"`
	Reset       bool                `json:"reset,omitempty"`
	// Now compacts the member now, or when its running turn ends; it takes neither Set nor Reset.
	Now bool `json:"now,omitempty"`
}

type MemberCompactionView struct {
	Address string `json:"address"`
	Ref     string `json:"ref"`
	CompactionView
	Now string `json:"now,omitempty"` // answer to Now: "now" | "turn_end"
}

package team

// Per-session compaction control (Phase 2a). A nil field in CompactionOverride means "follow the host default",
// so the JSON keeps null instead of dropping the key.
const (
	CompactionRoute       = "/api/team/compaction/{session_id}"
	MemberCompactionRoute = "/api/team/member-compaction"
	CapabilityCompaction  = "team.compaction.v1"

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

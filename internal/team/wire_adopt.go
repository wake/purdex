package team

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ---- U24: adopt and release (adopt spec D-U24-2, D-U24-3; plan PL-1a) ----

// Adopt error codes (APIError.Error on /api/team/*, and Approval.CloseReason
// when a re-check at decide time cancels the request); 409 and CLI exit 13.
const (
	ErrAdoptSelf            = "adopt_self"           // the target is the caller itself
	ErrAdoptTargetIsLead    = "adopt_target_is_lead" // the target leads a live team
	ErrAdoptAlreadyMember   = "adopt_already_member" // the target is already a live member
	ErrAdoptTargetNotFound  = "adopt_target_not_found"
	ErrAdoptTargetAmbiguous = "adopt_target_ambiguous" // two live conversations carry the target's ref; name it by session id or full address
	ErrKillFailed           = "kill_failed"            // 500: signalling an adopted member's process failed (EPERM...); nothing was marked
	ErrRemoteUnsupported    = "remote_unsupported"     // the target lives on another host; plan v3 P4b-4 reuses it
)

// Member origins: Member.Origin, how a member joined its team.
const (
	MemberOriginSpawned = "spawned" // pdx spawn
	MemberOriginAdopted = "adopted" // pdx adopt
)

// Notice kinds: what the outbox delivers to a session whose membership
// changed (PL-1d1). NoticeGiveUpS is how long it keeps trying.
const (
	NoticeAdopted  = "adopted"
	NoticeReleased = "released"

	NoticeGiveUpS = 600
)

// Notice texts. AdoptNoticeFmt takes the lead's address, the team id and the
// address to report to; ReleaseNoticeFmt the lead's address and the team id.
const (
	AdoptNoticeFmt   = "[pdx team] 你已成為 %s 的 member（team %s）。自我接力已關閉，接力由 lead 安排；回報請送 %s。"
	ReleaseNoticeFmt = "[pdx team] %s 已讓你離開 team %s：你現在是一般 session，自我接力依這台主機的設定。"
)

// The notices of a remote member (cross-host team spec §4.4) are M's own fixed text; only the lead's address and the
// team's name (each cleaned and at most 64 bytes) are filled in. RemoteHandoverNoticeFmt takes the new lead's address, the
// team's name and the address to report to; RemoteTeamEndedNoticeFmt the lead's address and the team's name;
// RemoteLocalEndNoticeFmt the team's name and the lead's address. A remote adopt and release use AdoptNoticeFmt and
// ReleaseNoticeFmt, with the team's name in the place of the id.
const (
	RemoteHandoverNoticeFmt  = "[pdx team] 你的 lead 已改為 %s（team %s）；回報請送 %s。"
	RemoteTeamEndedNoticeFmt = "[pdx team] %s 已結束 team %s：你現在是一般 session，自我接力依這台主機的設定。"
	RemoteLocalEndNoticeFmt  = "[pdx team] 這台主機的操作者已讓你離開 team %s（lead %s）：你現在是一般 session，自我接力依這台主機的設定。"
)

// AdoptPayload is Approval.Payload for KindAdopt. The target fields are
// what the dialog shows and what the decide-time re-check compares.
type AdoptPayload struct {
	TeamID          string `json:"team_id"`
	LeadSessionID   string `json:"lead_session_id"`
	TargetRef       string `json:"target_ref"` // the target's current ref, "_xxxxxx"
	TargetSessionID string `json:"target_session_id"`
	Title           string `json:"title,omitempty"` // the target's title
	TargetName      string `json:"target_name,omitempty"`
	TargetAddress   string `json:"target_address,omitempty"`
	TargetCwd       string `json:"target_cwd,omitempty"`
	TargetTmux      string `json:"target_tmux,omitempty"` // "<session>:@<win>.%<pane>"
	// TargetHostID and TargetHostAlias name the member host of a REMOTE target (cross-host team spec §4.3); both absent for
	// a session on the lead's own host. For a remote target the approval means "the user consents", not "adopted".
	TargetHostID    string `json:"target_host_id,omitempty"`
	TargetHostAlias string `json:"target_host_alias,omitempty"`
}

// AdoptionsRoute is GET /api/team/adoptions/{approval_id}: the membership a remote adopt's approval led to.
const AdoptionsRoute = "/api/team/adoptions/"

// Adoption states (the answer of AdoptionsRoute): the membership row's state as the lead host holds it, with a failed row
// whose reason is `remote_unreachable` (the 10 minute void of §3.3) reported as AdoptionVoid.
const (
	AdoptionJoining = "joining"
	AdoptionActive  = "active"
	AdoptionFailed  = "failed"
	AdoptionVoid    = "void"
)

// Adoption is the answer of AdoptionsRoute. State is one of the Adoption* states, or the row's later state
// (releasing, released, killing, killed, gone) once it was a member; Code is the failure code of a failed one.
type Adoption struct {
	ApprovalID string `json:"approval_id"`
	State      string `json:"state"`
	Code       string `json:"code,omitempty"`
}

// ReleaseRequest is POST /api/team/release: the same body as a kill.
type ReleaseRequest = KillRequest

// AdoptPayloadOf is the daemon's strict read of an adopt approval's
// payload: the kind must be KindAdopt, the payload one JSON object with no
// key AdoptPayload does not name, and nothing after it.
func AdoptPayloadOf(a Approval) (AdoptPayload, error) {
	var p AdoptPayload
	if a.Kind != KindAdopt {
		return p, fmt.Errorf("approval %q is kind %q, not %q", a.ID, a.Kind, KindAdopt)
	}
	if bytes.Equal(bytes.TrimSpace(a.Payload), []byte("null")) {
		return AdoptPayload{}, fmt.Errorf("adopt payload: null")
	}
	dec := json.NewDecoder(bytes.NewReader(a.Payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return AdoptPayload{}, fmt.Errorf("adopt payload: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return AdoptPayload{}, fmt.Errorf("adopt payload: trailing data after the object")
	}
	return p, nil
}

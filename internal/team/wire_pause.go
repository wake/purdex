package team

import "encoding/json"

// ---- The pause of an unattended session that has no relay quota left ----
// (docs/specs/2026-10-12-unattended-quota-pause-spec-plan.md §3.1, UP-1a)
//
// A paused session is the session's own state, kept in team.db as one row per (session, cause). It is paused iff it has any row.

// PausedRoute is GET /api/team/paused: the sessions that are paused now.
const PausedRoute = "/api/team/paused"

// Causes of a pause. A session may hold both at once: its own quota ran out, and its lead was paused.
const (
	PauseCauseQuota = "quota" // the session's own relay found no quota
	PauseCauseLead  = "lead"  // its lead was paused and the daemon paused its members with it
	// PauseCauseUnanswered: the session's final relay ask (RelayLastAskPct) was not answered (RT-2a1): it stops working until a person
	// answers, and members are not paused with it.
	PauseCauseUnanswered = "relay_unanswered"
)

// Roles a paused session has: a lead of a live team, a live local member of one, or neither.
const (
	PauseRoleSolo   = "solo"
	PauseRoleLead   = "lead"
	PauseRoleMember = "member"
)

// PausedSession is one paused session of GET /api/team/paused. Causes is never empty; Since is the earliest of its rows;
// LeadRef is the ref of the team's lead ("" for a solo session); Bounced counts the messages bounced and not yet listed to it.
type PausedSession struct {
	SessionID string   `json:"session_id"`
	Ref       string   `json:"ref"`
	Title     string   `json:"title"`
	Role      string   `json:"role"`
	Causes    []string `json:"causes"`
	Since     int64    `json:"since"`
	LeadRef   string   `json:"lead_ref"`
	Bounced   int      `json:"bounced"`
}

// PausedReleaseRoute is POST /api/team/paused/{session_id}/release: a person clears one session's pause (UP-3b).
const PausedReleaseRoute = "/api/team/paused/{session_id}/release"

// ErrNotPaused is the 404 of a release for a session that holds no pause row.
const ErrNotPaused = "not_paused"

// PauseReleaseRequest is the body of the release: an audit claim like PUT /api/team/roots, not a security boundary.
type PauseReleaseRequest struct {
	Client Client `json:"client"`
}

// PauseReleaseResponse is the 200 answer: how many sessions are no longer paused (the session, and the members of a lead that were
// paused only because it was).
type PauseReleaseResponse struct {
	Released int `json:"released"`
}

// PausedView is the answer of GET /api/team/paused.
type PausedView struct {
	Sessions []PausedSession `json:"sessions"` // never null (MarshalJSON)
}

// MarshalJSON writes no sessions as sessions:[], not null; and no causes as [].
func (v PausedView) MarshalJSON() ([]byte, error) {
	type plain PausedView
	if v.Sessions == nil {
		v.Sessions = []PausedSession{}
	}
	return json.Marshal(plain(v))
}

// RosterPause is the pause of a roster session: its causes (sorted) and since when. A session that is not paused has none:
// RosterSession.Paused is null.
type RosterPause struct {
	Causes []string `json:"causes"`
	Since  int64    `json:"since"`
}

// ErrPausedNoQuota is the 409 of a relay begin that found no relay quota while the host is unattended: the session was paused
// instead of getting a card (spec §3.3). No op was opened. An already paused session gets the same answer.
const ErrPausedNoQuota = "paused_no_quota"

// ErrMemberPausedNoQuota is the 409 the lead's `pdx relay _<ref>` gets when the member's relay found no pool left while the host is
// unattended: the member was paused instead of getting a card (spec §3.3, UP-2b). No op was opened. detail names the member.
const ErrMemberPausedNoQuota = "member_paused_no_quota"

// Kinds of a pause_outbox row (spec §3.1): a notice text to the session, a push request to the phones.
const (
	PauseOutboxNotice = "notice"
	PauseOutboxPush   = "push"
)

// The texts the daemon says on entering a pause (spec §3.3), word for word.
const (
	PauseNoticeSelf        = "[pdx team] 接力額度用完（無人值守），先暫停：手邊這一步收尾後不要開始新的工作，等使用者回來。"
	PauseNoticeLeadPaused  = "[pdx team] lead 已暫停（接力額度用完）：手邊這一步收尾後暫停，等 lead 指示。"
	PauseNoticeMemberQuota = "[pdx team] lead 安排：接力額度用完，先暫停，等 lead 指示。"
)

// Leaving a pause (spec §3.4, UP-3a). A reask outbox row carries the control message and its token (reask_id).
const (
	PauseOutboxReask = "reask"
	// PauseReaskControlPrefix is the control message that tells a mod to ask for the relay again: `[pdx-relay:control] reask id=<uuid>`.
	PauseReaskControlPrefix = "[pdx-relay:control] reask id="
	// MinPauseReaskModVersion is the mod protocol that understands the reask control and `pdx relay begin --reask` (UP-4 reports it in
	// its hello). A mod that says less gets the fallback notice and no token.
	MinPauseReaskModVersion = 5
	// PauseTokenTTLMs: a release token older than this is deleted by the reconciliation (24 h).
	PauseTokenTTLMs = 24 * 60 * 60 * 1000
)

// The texts the daemon says on leaving a pause (spec §3.4), word for word.
const (
	PauseResumedSolo = "[pdx team] 管理者已回位。接力申請已重新送出，等使用者核准。"
	// PauseReleasedSolo is a release by a person (not the switch): the lead's or the solo session's resume notice.
	PauseReleasedSolo = "[pdx team] 已解除暫停。接力申請已重新送出，等使用者核准。"
	// PauseResumedByRelay: the person relayed the session by hand (/relay): the pause ends and nothing is asked again (RT-1a).
	PauseResumedByRelay     = "[pdx team] 使用者已直接接力，恢復作業。"
	PauseResumedSoloOldMod  = "[pdx team] 管理者已回位。請在這個 session 打 /relay 重新申請接力。"
	PauseResumedMemberLead  = "[pdx team] lead 已恢復，等 lead 指示。"
	PauseResumedMemberQuota = "[pdx team] 管理者已回位，等 lead 指示。"
	// PauseMemberQuotaListFmt is the lead's one notice naming the members that were paused for their own quota; %s is the list.
	PauseMemberQuotaListFmt = "[pdx team] 這些 member 之前因額度用完暫停：%s，要不要接力由你決定。"
	// PauseBouncedFmt heads the list of the messages bounced during a pause; %d is how many.
	PauseBouncedFmt = "[pdx team] 暫停期間有 %d 則訊息被退回："
)

// PauseNotifyKey is the service-registry key under which the push module publishes its PauseNotifier.
const PauseNotifyKey = "push.pause-notifier"

// PauseNotifier is how the team module's pause outbox asks for a phone push, in process (spec §3.3). NotifyPause says done when
// the request was queued or deliberately skipped (a person is at the computer, no device to tell) and not done when the push
// side is not up yet, so the outbox row stays. CollapseID is "pause:<session>:<since>": a repeat replaces the earlier push.
type PauseNotifier interface {
	NotifyPause(p PausePush) (done bool)
}

// PausePush is one phone push of a pause. The payload carries the session (session_id; session_code when TmuxSession resolves to
// one on this host, so a tap goes to it) and host_id; open_approvals / open_approval_keys come from the push module's own read at
// send time or are left out, never written empty (the same sender an approval push uses).
type PausePush struct {
	CollapseID  string // pause:<session>:<since>
	Text        string // the title
	SessionID   string
	TmuxSession string // the tmux session NAME the paused session runs in; "" when not in tmux
	SinceMs     int64  // when the pause began (unix ms)
}

// The relay_unanswered pause (relay/team rules v2, RT-2a1): its texts, word for word.
const (
	PauseNoticeUnanswered = "[pdx team] 接力申請沒有回應，先暫停作業：手邊這一步收尾後不要開始新的工作，等使用者確認。"
	// What the session is told when the pause ends, by how the card ended.
	PauseResumedUnansweredApproved = "[pdx team] 接力已核准，恢復作業。"
	PauseResumedUnansweredDenied   = "[pdx team] 使用者決定不接力，恢復作業；之後不會再申請。"
	PauseResumedUnansweredEnded    = "[pdx team] 接力申請已結束，恢復作業。"
	// PausePushUnansweredFmt is the phone's title; %s is the session's title or ref.
	PausePushUnansweredFmt = "%s 已暫停：接力申請沒有回應，等你核准"
	// What a message to the paused session is bounced with (RT-2a2): to a solo session or a lead, and to a member.
	PauseBounceUnanswered = "我目前暫停作業，等待使用者確認中"
	// A member whose final relay ask its lead did not answer (RT-2b T23).
	PauseNoticeUnansweredMember = "[pdx team] 接力申請 lead 沒有回應，先暫停作業，等 lead 確認。"
	PauseResumedMemberAccepted  = "[pdx team] lead 已安排接力，恢復作業。"
	// A person released a member its lead had not answered (the App's 「解除」, #2816): the ask is denied by the person.
	PauseResumedMemberDeniedByUser = "[pdx team] 使用者決定不接力，恢復作業。"
	// RelayAskDeniedByUserLeadFmt is the lead's notice of it; %s is the member's address.
	RelayAskDeniedByUserLeadFmt = "[pdx team] member %s 的接力申請已由使用者拒絕，恢復作業"
	PauseResumedMemberDenied    = "[pdx team] lead 決定不接力，恢復作業。"
	PauseBounceUnansweredMember = "我目前暫停作業，等待 lead 確認中"
)

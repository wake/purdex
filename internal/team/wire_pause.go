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

// Kinds of a pause_outbox row (spec §3.1): a notice text to the session, a push request to the phones.
const (
	PauseOutboxNotice = "notice"
	PauseOutboxPush   = "push"
)

// The texts the daemon says on entering a pause (spec §3.3), word for word.
const (
	PauseNoticeSelf       = "[pdx team] 接力額度用完（無人值守），先暫停：手邊這一步收尾後不要開始新的工作，等使用者回來。"
	PauseNoticeLeadPaused = "[pdx team] lead 已暫停（接力額度用完）：手邊這一步收尾後暫停，等 lead 指示。"
)

// PauseNotifyKey is the service-registry key under which the push module publishes its PauseNotifier.
const PauseNotifyKey = "push.pause-notifier"

// PauseNotifier is how the team module's pause outbox asks for a phone push, in process (spec §3.3). NotifyPause says done when
// the request was queued or deliberately skipped (a person is at the computer, no device to tell) and not done when the push
// side is not up yet, so the outbox row stays. collapseID is "pause:<session>:<since>": a repeat replaces the earlier push.
type PauseNotifier interface {
	NotifyPause(collapseID, text string) (done bool)
}

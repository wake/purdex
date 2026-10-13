package team

// MembershipReaderKey is the service-registry key under which the team module publishes its MembershipReader; the push
// module reads it at event time (badge spec §3.1).
const MembershipReaderKey = "team.membership"

// MembershipReader says whether a Claude Code session is a team member as the app-icon badge means it: an unread event of a
// member is not counted on the phone, and a member pushes only when it has a tab.
//
// True when the session is (a) a local member row (a seat that runs on this host; never a lead) in state `active` of a team
// that has not ended, or (b) an `active` remote member (adopted by a lead elsewhere, running here). A lead is not a member;
// an empty session id is not a member. An error means "unknown": the caller must not read it as "not a member".
type MembershipReader interface {
	Membership(sessionID string) (member bool, err error)
}

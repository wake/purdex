package team

// LeadNoticerKey is the service-registry key under which the team module publishes its LeadNoticer; the resources
// module's notice outbox reads it at send time, every time (lease v2 amendment A, P2d-2).
const LeadNoticerKey = "team.leadnotice"

// LeadNoticer sends a daemon notice about a session to the lead of the team the session is an active member of: the
// fallback of a notice that has no Purdex mod channel to the session itself.
//
// member is false when the session is no active member of a live team (a solo session, a lead, an unknown id); nothing
// was sent, and the caller tells the person instead. member true with delivered false means the send did not go (the
// lead has no live inbox, the lead lookup failed): the caller tries again. delivered is also true when the peer send
// reports only that the wait for the receiver timed out (the frame was written; it is not sent twice).
type LeadNoticer interface {
	NoticeLeadOf(sessionID, text string) (member, delivered bool)
}

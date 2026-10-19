package team

// ---- The lead's name at approval (name pool v2 spec §5.3) ----
//
// A lead request carries the names its session could take (LeadPayload.LeadNames); the card sends the one the person picked
// (Grant.LeadName), and the daemon renames the session as the approval is made. The names are persona codes.

const (
	// ErrNameTaken is the 409 of a decide whose lead_name is held by another session by now: the request stays open and the body
	// carries the refreshed list (APIError.LeadNames), so the card reopens with it.
	ErrNameTaken = "name_taken"
	// ErrNotOpen is the 409 of the lead-names route for a request that is already closed: there is nothing left to pick for.
	ErrNotOpen = "not_open"

	// The kinds of a LeadName: the session's own name (first), a persona that has led before, a default lead, and the others.
	LeadNameKeep   = "keep"
	LeadNameFormer = "former"
	LeadNameLead   = "lead"
	LeadNameOther  = "other"

	// LeadNamesRoute is the pattern of GET /api/team/approvals/{id}/lead-names.
	LeadNamesRoute = "GET /api/team/approvals/{id}/lead-names"
)

// LeadName is one name a lead can take: Code is the persona code, Name its display name, Kind one of the LeadName* kinds, and
// LastAt (unix ms) when a former lead last led, 0 for the others.
type LeadName struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	LastAt int64  `json:"last_at"`
}

// LeadNamesResponse is GET /api/team/approvals/{id}/lead-names: the list computed now. Never null.
type LeadNamesResponse struct {
	LeadNames []LeadName `json:"lead_names"`
}

package team

// The cross-host roots-change route (docs/specs/2026-10-12-host-roots-from-lead-spec-plan.md §3.1): a lead host asks a
// member host to add or remove one directory in the TeamRoots it granted that lead host, or to read them. A request is
// convergent set membership ("path P present / absent"), not a command: it has no receiver-side log, and a replay of one id
// answers the state at that moment.

// RootsChangeRoute is the member host's route; a host principal's, never the admin's.
const RootsChangeRoute = "/api/peers/team/roots-change"

// Ops of a RootsChangeRequest.
const (
	RootsOpAdd    = "add"
	RootsOpRemove = "remove"
	RootsOpRead   = "read"
)

// Results of a RootsChangeResponse.
const (
	RootsResultAdded     = "added"
	RootsResultRemoved   = "removed"
	RootsResultUnchanged = "unchanged"
)

// Refusal codes of the route beyond the ones every host route shares (host_unverified, admin_not_allowed, rate_limited,
// bad_request, wrong_host, host_not_allowed — the ErrCommand* values) and ErrRootsInUse (409, detail: refs / op ids, comma list).
const (
	ErrRootsBadRoot = "bad_root" // 400: add of a path that is not an absolute existing directory, or past 16 roots (detail: the reason)
)

// RootsChangeRequest is the body: ID is a UUID v4 per change (the lead never reuses one); Path is absolute and clean for
// add / remove and empty for read.
type RootsChangeRequest struct {
	ID       string `json:"id"`
	ToHostID string `json:"to_host_id"`
	Op       string `json:"op"`
	Path     string `json:"path"`
}

// RootsChangeResponse is the 200 body. Path is the canonical path for add, the stored root that matched for remove, "" for
// read and for a remove that matched nothing. TeamRoots / TeamRootsRev are the grant after the request.
type RootsChangeResponse struct {
	ID           string   `json:"id"`
	HostID       string   `json:"host_id"`
	Op           string   `json:"op"`
	Result       string   `json:"result"`
	Path         string   `json:"path"`
	TeamRoots    []string `json:"team_roots"`
	TeamRootsRev int64    `json:"team_roots_rev"`
}

package team

// ---- PUT /api/team/roots: the App changes a live team's spawn roots ----

const RootsRoute = "/api/team/roots" // PUT

// MaxRoots is how many roots a team may hold (the same bound a forwarded grant has, config.MaxTeamRoots).
const MaxRoots = 16

// Kinds of RootUse.
const (
	RootUseMember   = "member"   // an active member's cwd
	RootUseSpawn    = "spawn"    // a spawn op still starting
	RootUseAdoption = "adoption" // an open adopt request
)

// RootsPutRequest is the body. The App's alone: Client.Kind is the caller's own claim (told, not enforced, as max-members);
// no pdx command writes it. Roots is the WHOLE new list, not a delta; IfRev is the roots_rev the client last saw.
type RootsPutRequest struct {
	TeamID string   `json:"team_id"`
	IfRev  int      `json:"if_rev"`
	Roots  []string `json:"roots"`
	Client Client   `json:"client"`
}

// RootsView is the 200 answer: the roots as stored (real paths) and the new revision (if_rev + 1).
type RootsView struct {
	TeamID   string   `json:"team_id"`
	Roots    []string `json:"roots"`
	RootsRev int      `json:"roots_rev"`
}

// RootUse is one thing that still has its cwd under a root being removed.
type RootUse struct {
	Root string `json:"root"`          // the root being removed
	Kind string `json:"kind"`          // RootUseMember | RootUseSpawn | RootUseAdoption
	Ref  string `json:"ref,omitempty"` // "_xxxxxx" for a member
	Cwd  string `json:"cwd"`
}

// RootsRefusal is the body of the 409 roots_in_use. Nothing changed.
type RootsRefusal struct {
	Error  string    `json:"error"`
	Detail string    `json:"detail,omitempty"`
	InUse  []RootUse `json:"in_use"`
}

// RootsRevMismatch is the body of the 409 rev_mismatch: if_rev is not the stored revision (another client changed the
// roots). Nothing changed; it carries what is stored so the App can refresh.
type RootsRevMismatch struct {
	Error    string   `json:"error"`
	Detail   string   `json:"detail,omitempty"`
	Roots    []string `json:"roots"`
	RootsRev int      `json:"roots_rev"`
}

// RootsBadRequest is the body of a 400 that names the offending entry ("roots[3]").
type RootsBadRequest struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
	Field  string `json:"field,omitempty"`
}

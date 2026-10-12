package team

// ---- The lead host's routes for the directories it may use on another host (docs/specs/2026-10-12-host-roots-from-lead-spec-plan.md §3.2) ----
//
// The App asks the lead host to add / remove one directory in the TeamRoots a member host granted it; the lead host records the
// change, delivers it (the reconciler, XR-2a) and reports what each member host last said.

const (
	HostRootsRoute        = "/api/team/host-roots"         // POST (a change) and GET (every paired host)
	HostRootsDismissRoute = "/api/team/host-roots/dismiss" // POST {id}
)

// Error codes of the routes beyond the shared ones (bad_request, roots_in_use, bad_root).
const (
	ErrUnknownHost      = "unknown_host"       // 400: no paired, verified host answers to the name
	ErrNotARoot         = "not_a_root"         // 400: a remove of a path that is neither a cached root nor a pending add
	ErrRootBeingRemoved = "root_being_removed" // 409: a spawn / adopt under a path whose removal is pending
	ErrChangePending    = "pending"            // 409 of a dismiss of a change that is still pending
	ErrNoSuchChange     = "no_such_change"     // 404 of a dismiss
)

// HostRootsPostRequest is POST /api/team/host-roots. Client is the App's claim, as for PUT /api/team/roots (told, not enforced).
type HostRootsPostRequest struct {
	Host   string `json:"host"` // an alias (case-insensitive) or a host id of a paired, verified host
	Op     string `json:"op"`   // add | remove
	Path   string `json:"path"` // absolute and clean
	Client Client `json:"client"`
}

// HostRootsDismissRequest is POST /api/team/host-roots/dismiss: a refused or dropped change is deleted.
type HostRootsDismissRequest struct {
	ID string `json:"id"`
}

// HostRootChange is one change as the App sees it: pending (waiting for the host), refused (with the host's reason) or dropped.
type HostRootChange struct {
	ID        string `json:"id"`
	HostID    string `json:"host_id"`
	Op        string `json:"op"`
	Path      string `json:"path"`
	State     string `json:"state"`
	Code      string `json:"code,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Canonical string `json:"canonical,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// HostRootsHost is one paired host in GET /api/team/host-roots. Status is the class of the last contact (ok | offline |
// consent_off | unsupported | unauthorized, "unknown" before the first); Roots / Rev / SeenAt are the cache (Roots never null).
type HostRootsHost struct {
	HostID  string           `json:"host_id"`
	Alias   string           `json:"alias"`
	Status  string           `json:"status"`
	Roots   []string         `json:"roots"`
	Rev     int64            `json:"rev"`
	SeenAt  int64            `json:"seen_at"`
	Changes []HostRootChange `json:"changes"` // pending, refused and dropped; never null
}

// HostRootsView is the answer of GET /api/team/host-roots.
type HostRootsView struct {
	Hosts []HostRootsHost `json:"hosts"`
}

// HostRootUse is one thing of this lead running on the host under a directory being removed.
type HostRootUse struct {
	Ref   string `json:"ref,omitempty"`   // "_xxxxxx" of a member
	OpID  string `json:"op_id,omitempty"` // a forwarded spawn still starting
	Title string `json:"title,omitempty"`
	Cwd   string `json:"cwd"`
}

// HostRootsInUse is the body of the 409 roots_in_use of a remove: nothing was written.
type HostRootsInUse struct {
	Error  string        `json:"error"`
	Detail string        `json:"detail,omitempty"`
	Uses   []HostRootUse `json:"uses"`
}

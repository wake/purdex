package peers

// Envelope is GET /api/peers' body for one host: the daemon's own local
// inventory (scope unset or scope=local), and the shape of each host's row
// when the CLI's fan-out client (P2) unwraps a scope=all response.
//
// Partial has three independent causes (spec §3.3), each with its own
// explicit signal so a consumer never has to infer one from the absence
// of the others: owner lookups that did not run (visible per row as
// agent:null with an empty reason), UnknownRegistryFiles, and
// TitlesUnavailable. A fourth, rarer cause has no field of its own (#1293):
// the inventory budget ran out before the tmux-generation re-check, so the
// answer cannot vouch that its rows come from one tmux generation. A
// consumer treats it like an owner lookup that did not run — retry.
//
// The three are not equally serious, and TitlesUnavailable is the mild one
// (spec §6.1): every address in Peers is built from that row's own registry
// name and sessionId, so an unreadable title store costs the title column
// nothing else. Under v2 it did reach the address — a default label was
// minted from the tmux session name and resolved over the very label rows
// that could not be read — which is why the flag reads as graver than it
// now is.
type Envelope struct {
	HostID string `json:"host_id"`
	// Alias is what this host calls ITSELF (config PeerAlias()). A reader uses
	// it to name a newly paired peer the way that peer names itself, so an
	// address means the same string on both machines. It is self-reported and
	// therefore attacker-controlled: validate before storing, exactly as
	// host_id already is.
	Alias                string       `json:"alias"`
	OK                   bool         `json:"ok"`
	Error                string       `json:"error,omitempty"`
	Partial              bool         `json:"partial"`
	Peers                []PeerRecord `json:"peers"`                  // never null
	DaemonVersion        string       `json:"daemon_version"`         // this daemon's buildinfo.Version
	UnknownRegistryFiles []string     `json:"unknown_registry_files"` // never null; alive-but-undecodable registry files (Diagnosis.BlockingUnknown)
	TitlesUnavailable    bool         `json:"titles_unavailable"`     // the title store could not be read: every row renders without its title. Addresses are unaffected
}

// HostResult is one host's row in a scope=all response: like Envelope, plus
// the alias/host_id identifying which peer host it came from.
type HostResult struct {
	Alias  string `json:"alias"`
	HostID string `json:"host_id"` // configured or learned; "" if unknown
	// SelfAlias is what the peer calls ITSELF (its Envelope.Alias), carried
	// beside Alias — which is what WE call it. "" means the peer never said:
	// an old daemon, or a fetch that never reached one. "" is not a
	// disagreement, and a reader must not treat it as one.
	//
	// Drift is surfaced, never followed. Alias stays authoritative: it is the
	// head every address on this host resolves against, so silently adopting
	// a peer's rename would move every address out from under whoever had
	// written one down. `pdx peers --all` says the two disagree and stops
	// there; renaming stays a deliberate `pdx peers host` edit.
	SelfAlias            string       `json:"self_alias"`
	OK                   bool         `json:"ok"`
	Error                string       `json:"error,omitempty"`
	Partial              bool         `json:"partial"`
	Peers                []PeerRecord `json:"peers"`                  // never null
	DaemonVersion        string       `json:"daemon_version"`         // "" when this row is a local fetch failure
	UnknownRegistryFiles []string     `json:"unknown_registry_files"` // never null
	TitlesUnavailable    bool         `json:"titles_unavailable"`     // copied from the host's Envelope
}

// AllEnvelope is GET /api/peers?scope=all's body.
type AllEnvelope struct {
	Hosts []HostResult `json:"hosts"` // local host first
}

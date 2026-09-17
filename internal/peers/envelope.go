package peers

// Envelope is GET /api/peers' body for one host: the daemon's own local
// inventory (scope unset or scope=local), and the shape of each host's row
// when the CLI's fan-out client (P2) unwraps a scope=all response.
//
// Partial has three independent causes (spec §3.3), each with its own
// explicit signal so a consumer never has to infer one from the absence
// of the others: owner lookups that did not run (visible per row as
// agent:null with an empty reason), UnknownRegistryFiles, and
// TitlesUnavailable.
//
// The three are not equally serious, and TitlesUnavailable is the mild one
// (spec §6.1): every address in Peers is built from that row's own registry
// name and sessionId, so an unreadable title store costs the title column
// nothing else. Under v2 it did reach the address — a default label was
// minted from the tmux session name and resolved over the very label rows
// that could not be read — which is why the flag reads as graver than it
// now is.
type Envelope struct {
	HostID               string       `json:"host_id"`
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
	Alias                string       `json:"alias"`
	HostID               string       `json:"host_id"` // configured or learned; "" if unknown
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

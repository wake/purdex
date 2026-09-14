package peers

// Envelope is GET /api/peers' body for one host: the daemon's own local
// inventory (scope unset or scope=local), and the shape of each host's row
// when the CLI's fan-out client (P2) unwraps a scope=all response.
type Envelope struct {
	HostID               string       `json:"host_id"`
	OK                   bool         `json:"ok"`
	Error                string       `json:"error,omitempty"`
	Partial              bool         `json:"partial"`
	Peers                []PeerRecord `json:"peers"`                  // never null
	DaemonVersion        string       `json:"daemon_version"`         // this daemon's buildinfo.Version
	UnknownRegistryFiles []string     `json:"unknown_registry_files"` // never null; alive-but-undecodable registry files (Diagnosis.BlockingUnknown)
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
}

// AllEnvelope is GET /api/peers?scope=all's body.
type AllEnvelope struct {
	Hosts []HostResult `json:"hosts"` // local host first
}

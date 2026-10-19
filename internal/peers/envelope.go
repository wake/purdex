package peers

import "regexp"

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
	// AddressVersion states which address rules Peers follow, so a reader
	// never infers them from the rows. AddressVersionV5 (every current
	// daemon) means a row is named only by PeerRecord.Name, and the registry
	// name in agent.peer_name routes nothing — not even when no row carries a
	// Name (the name store failed that pass). Absent (0) is a daemon from
	// before v5, whose registry names still route. Self-reported, like every
	// field here: it decides only how the reader treats this host's own rows.
	AddressVersion int `json:"address_version,omitempty"`
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
	LineageUnavailable   bool         `json:"lineage_unavailable"`    // the relay lineage could not be read: rows carry no previous_refs, and a ref that matches no live row is not-ready rather than not-found (lead-team-relay spec §8.4)
	// ExecutionsUnavailable: the execution list could not be read, so no
	// execution row is listed, and an address that matches no row is
	// not-ready rather than not-found (peer mailbox spec §4.1).
	ExecutionsUnavailable bool `json:"executions_unavailable,omitempty"`
	// Team states which cross-host team kinds this host applies and whether
	// it lets the ASKING principal use them (cross-host team spec §3.1 rule
	// 7). Absent: an older daemon, which supports none.
	Team *TeamCaps `json:"team,omitempty"`
	// Names is what this host's name history shares with the others (name pool v2 spec §6.1). Absent: an older daemon, or a
	// history that could not be read; a reader takes it as an empty set either way.
	Names *NamesInfo `json:"names,omitempty"`
}

// MaxFamiliar is how many codes a host publishes in Names.Familiar, and how many a reader takes from one host.
const MaxFamiliar = 64

// NamesInfo is Envelope.Names. Familiar is the codes this host has seen as a lead or as a member (name_leads.code ∪
// name_pairs.member_code), most recent first, at most MaxFamiliar; never null. Unavailable says the history could not be read this
// time (Familiar is then empty and means nothing): a reader keeps what the host published before, rather than taking the
// silence for an older daemon's empty set.
type NamesInfo struct {
	Familiar    []string `json:"familiar"`
	Unavailable bool     `json:"unavailable,omitempty"`
}

var familiarCode = regexp.MustCompile(`^[a-z]{2,16}$`)

// NormalizeFamiliar is codes as a reader keeps them: the well-formed ones only (a remote is attacker-controlled; a name code
// is lower-case letters), each once in its first position, at most MaxFamiliar. Never nil.
func NormalizeFamiliar(codes []string) []string {
	out := make([]string, 0, min(len(codes), MaxFamiliar))
	seen := make(map[string]bool, len(out))
	for _, c := range codes {
		if len(out) == MaxFamiliar {
			break
		}
		if !familiarCode.MatchString(c) || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// TeamCaps is Envelope.Team. Kinds is never null; AllowTeam is the asking
// host principal's consent entry (false for admin and every other reader).
type TeamCaps struct {
	Kinds     []string `json:"kinds"`
	AllowTeam bool     `json:"allow_team"`
	// FactKinds are the member-host facts this host applies as a lead host (POST /api/peers/team/facts, X3b-2). Absent:
	// an older daemon, which has no facts route.
	FactKinds []string `json:"fact_kinds,omitempty"`
	// RootsChange: this daemon applies POST /api/peers/team/roots-change (cross-host roots spec 2026-10-12): a capability
	// boolean, not a team-command kind. Absent: an older daemon, where a lead host keeps its root changes pending.
	RootsChange bool `json:"roots_change,omitempty"`
}

// AddressVersionV5 is Envelope.AddressVersion for Peer Address v5 (peer
// mailbox spec §3): rows are addressed by the pdx-assigned virtual name.
const AddressVersionV5 = 5

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
	LineageUnavailable   bool         `json:"lineage_unavailable"`    // copied from the host's Envelope
	// ExecutionsUnavailable is copied from the host's Envelope.
	ExecutionsUnavailable bool `json:"executions_unavailable,omitempty"`
}

// AllEnvelope is GET /api/peers?scope=all's body.
type AllEnvelope struct {
	Hosts []HostResult `json:"hosts"` // local host first
}

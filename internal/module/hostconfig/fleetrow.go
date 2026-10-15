package hostconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/config"
)

// KeyFleet is the host_config row of the fleet's setting (fleet spec §4): the order of the daemon hosts, the SSH alias this
// daemon uses for the other one, and the switch. It is one row (an object), not a list.
const KeyFleet = "fleet"

// FleetConfigKey is the registry key of the module as a FleetConfigReader (the fleet module reads the row through it).
const FleetConfigKey = "hostconfig.fleet"

const maxFleetOrder = 4

// FleetConfig is the `fleet` row. Whether Order holds this daemon's own alias, and whether the others resolve to a peer, are
// the fleet module's run-time checks (a config must save while a peer is offline), not this row's.
type FleetConfig struct {
	Order        []string `json:"order"`
	PeerSSHAlias string   `json:"peer_ssh_alias"`
	Enabled      bool     `json:"enabled"`
}

// DefaultFleetConfig is the row of a never-written key.
func DefaultFleetConfig() FleetConfig {
	return FleetConfig{Order: []string{"air26", "mlab"}, PeerSSHAlias: "", Enabled: true}
}

// FleetConfigReader is what the fleet module type-asserts on the registry value: the row and its revision.
type FleetConfigReader interface {
	FleetConfig() (FleetConfig, int64, error)
}

var fleetDefaultJSON = func() string {
	b, err := json.Marshal(DefaultFleetConfig())
	if err != nil {
		panic(err) // a fixed struct of strings and a bool
	}
	return string(b)
}()

// normalizeFleet validates a PUT body (and a stored value): an object of order (1 to 4 aliases of the peer alias shape,
// unique case-insensitively), peer_ssh_alias (empty, or an alias of the SSH config) and enabled (default true); nothing else.
func normalizeFleet(raw json.RawMessage) (FleetConfig, error) {
	c, _, err := parseFleet(raw, false)
	return c, err
}

// parseFleet is the one parser. lenient (the read side) turns a peer_ssh_alias that is gone from the SSH config into an empty
// one with a reason, instead of refusing the row: a vanished alias must not send the order back to its default.
func parseFleet(raw json.RawMessage, lenient bool) (cfg FleetConfig, reason string, err error) {
	if err := rejectDuplicateKeys(raw); err != nil {
		return FleetConfig{}, "", err
	}
	if firstByte(raw) != '{' {
		return FleetConfig{}, "", errors.New("items must be a JSON object")
	}
	var in struct {
		Order        *[]string       `json:"order"`
		PeerSSHAlias json.RawMessage `json:"peer_ssh_alias"` // raw: a null is not "left out"
		Enabled      json.RawMessage `json:"enabled"`
	}
	if err := strictDecode(raw, &in); err != nil {
		return FleetConfig{}, "", fmt.Errorf("fleet: %v", err)
	}
	if in.Order == nil {
		return FleetConfig{}, "", errors.New("order is required")
	}
	if n := len(*in.Order); n < 1 || n > maxFleetOrder {
		return FleetConfig{}, "", fmt.Errorf("order must list 1 to %d host aliases", maxFleetOrder)
	}
	seen := map[string]bool{}
	for _, a := range *in.Order {
		if err := config.ValidateSelfAlias(a, nil); err != nil { // the shape rule a peer host's alias is held to
			return FleetConfig{}, "", fmt.Errorf("order: %v", err)
		}
		k := strings.ToLower(a)
		if seen[k] {
			return FleetConfig{}, "", fmt.Errorf("order: %q is listed twice", a)
		}
		seen[k] = true
	}
	out := FleetConfig{Order: append([]string{}, *in.Order...), Enabled: true}
	if in.PeerSSHAlias != nil {
		var alias string
		if err := json.Unmarshal(in.PeerSSHAlias, &alias); err != nil || string(in.PeerSSHAlias) == "null" {
			return FleetConfig{}, "", errors.New("peer_ssh_alias must be a string")
		}
		if alias != "" && !sshAliasOK(alias) {
			if !lenient {
				return FleetConfig{}, "", fmt.Errorf("unknown_ssh_alias: %s", alias)
			}
			reason = fmt.Sprintf("peer_ssh_alias cleared: unknown_ssh_alias: %s", alias)
			alias = ""
		}
		out.PeerSSHAlias = alias
	}
	if in.Enabled != nil {
		switch string(in.Enabled) {
		case "true", "false":
			out.Enabled = string(in.Enabled) == "true"
		default:
			return FleetConfig{}, "", errors.New("enabled must be true or false")
		}
	}
	return out, reason, nil
}

// readFleet is normalizeFleet's lenient twin (the GET's view): a value that does not read is invalid and answers the default
// row (never what its owner did not write: FleetConfig refuses it).
func readFleet(raw json.RawMessage) readout {
	c, reason, err := parseFleet(raw, true)
	if err != nil {
		return readout{items: DefaultFleetConfig(), invalid: err}
	}
	r := readout{items: c}
	if reason != "" {
		r.drop(reason)
	}
	return r
}

// FleetConfig reads the stored row and its revision; the default (revision 0) for a never-written key. A stored value that
// no longer reads is an error.
func (m *Module) FleetConfig() (FleetConfig, int64, error) {
	e, err := m.store.Get(KeyFleet)
	if err != nil {
		return FleetConfig{}, 0, err
	}
	if e.Value == nil {
		return DefaultFleetConfig(), e.Revision, nil
	}
	c, _, err := parseFleet(e.Value, true)
	return c, e.Revision, err
}

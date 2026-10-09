package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
)

// KeyRelayQuota is the host_config row of the relay-quota rule's switch (#2062, spec §3.6): while it is on, an
// automatic approval of a self relay under unattended mode spends one of the chain's self_left; while it is off
// (the default) unattended mode auto-approves self relays as U23 always did and nothing is spent. It is its OWN key,
// not a field of `relay`: that payload is a full replace whose missing booleans reset to their defaults, so an older
// App saving the relay switches would silently write the rule back to false. Read per decision, so flipping it needs
// no restart. The user's, like the unattended switch: no pdx command, and the skill forbids an agent to change it.
const KeyRelayQuota = "relay_quota"

// RelayQuotaKey is the service-registry key of the module as a RelayQuotaReader.
const RelayQuotaKey = "hostconfig.relay-quota"

// relayQuotaJSON is the switch as the GET answers it for a never-written key (emptyFor).
const relayQuotaJSON = `{"rule":false}`

// RelayQuotaSwitch is the stored shape and the GET field `relayQuota.items`.
type RelayQuotaSwitch struct {
	Rule bool `json:"rule"`
}

// RelayQuotaReader is what the team module type-asserts on the registry value.
type RelayQuotaReader interface {
	// RelayQuotaRule reads the switch: off for a never-written key. A stored value that does not decode is an
	// error and the caller treats it as off (the behaviour the host had before the rule existed).
	RelayQuotaRule() (bool, error)
}

// relayQuotaOf reads an object of exactly {"rule": boolean}; null, a string, a missing or a further field is refused —
// a misspelt switch must not save as "left out = off".
func relayQuotaOf(raw json.RawMessage) (RelayQuotaSwitch, error) {
	var fields map[string]json.RawMessage
	if firstByte(raw) != '{' || json.Unmarshal(raw, &fields) != nil {
		return RelayQuotaSwitch{}, errors.New("items must be a JSON object")
	}
	for k := range fields {
		if k != "rule" {
			return RelayQuotaSwitch{}, errors.New("unknown relay_quota field " + k + "; only rule")
		}
	}
	v, present := fields["rule"]
	var b bool
	if !present || bytes.Equal(bytes.TrimSpace(v), []byte("null")) || json.Unmarshal(v, &b) != nil {
		return RelayQuotaSwitch{}, errors.New("rule must be a boolean")
	}
	return RelayQuotaSwitch{Rule: b}, nil
}

// normalizeRelayQuota validates a PUT body.
func normalizeRelayQuota(raw json.RawMessage) (RelayQuotaSwitch, error) { return relayQuotaOf(raw) }

// readRelayQuota is normalizeRelayQuota's lenient twin (the GET's view): a value that does not read is invalid and
// answers off.
func readRelayQuota(raw json.RawMessage) readout {
	s, err := relayQuotaOf(raw)
	if err != nil {
		return readout{items: RelayQuotaSwitch{}, invalid: err}
	}
	return readout{items: s}
}

// RelayQuotaRule reads the stored switch, off for a never-written key.
func (m *Module) RelayQuotaRule() (bool, error) {
	e, err := m.store.Get(KeyRelayQuota)
	if err != nil {
		return false, err
	}
	if e.Value == nil {
		return false, nil
	}
	s, err := relayQuotaOf(e.Value)
	return s.Rule, err
}

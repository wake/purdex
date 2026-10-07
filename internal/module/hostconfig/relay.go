package hostconfig

import (
	"encoding/json"
	"errors"
)

// KeyRelay is the host_config row of the relay switches (lead-team-relay
// spec §8.7 (a)): per host, read by the team module when a session asks to
// self-relay. Both default to true; a member has no switch (U13).
const KeyRelay = "relay"

// RelaySwitchesKey is the service-registry key under which Init publishes
// the module as a RelaySwitchReader for the team module.
const RelaySwitchesKey = "hostconfig.relay-switches"

// RelaySwitches is the stored shape and the GET field `relay.items`.
type RelaySwitches struct {
	SelfSolo bool `json:"self_solo"` // a session that is neither lead nor member
	SelfLead bool `json:"self_lead"` // a lead
}

// DefaultRelaySwitches is what a host that never wrote the row reads as.
var DefaultRelaySwitches = RelaySwitches{SelfSolo: true, SelfLead: true}

// relaySwitchesJSON is DefaultRelaySwitches as the GET answers it for a
// never-written key (emptyFor).
const relaySwitchesJSON = `{"self_solo":true,"self_lead":true}`

// RelaySwitchReader is what the team module type-asserts on the registry value.
type RelaySwitchReader interface {
	RelaySwitches() (RelaySwitches, error)
}

// normalizeRelay validates a PUT body: a JSON object whose two fields are
// booleans; a field left out keeps its default (true). Anything else is
// a validation error.
func normalizeRelay(raw json.RawMessage) (RelaySwitches, error) {
	if firstByte(raw) != '{' {
		return RelaySwitches{}, errors.New("items must be a JSON object")
	}
	var in struct {
		SelfSolo *bool `json:"self_solo"`
		SelfLead *bool `json:"self_lead"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return RelaySwitches{}, errors.New("self_solo and self_lead must be booleans")
	}
	out := DefaultRelaySwitches
	if in.SelfSolo != nil {
		out.SelfSolo = *in.SelfSolo
	}
	if in.SelfLead != nil {
		out.SelfLead = *in.SelfLead
	}
	return out, nil
}

// RelaySwitches reads the stored switches, defaults for a never-written
// key. A stored value that no longer decodes is an error, not a silent
// "on": the team module then refuses self relay with 503 rather than
// relaying against a switch it could not read.
func (m *Module) RelaySwitches() (RelaySwitches, error) {
	e, err := m.store.Get(KeyRelay)
	if err != nil {
		return RelaySwitches{}, err
	}
	if e.Value == nil {
		return DefaultRelaySwitches, nil
	}
	return normalizeRelay(e.Value)
}

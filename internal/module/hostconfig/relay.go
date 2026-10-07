package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/wake/purdex/internal/team"
)

// KeyRelay is the host_config row of the relay switches (lead-team-relay
// spec §8.7 (a)): per host, read by the team module when a session asks to
// self-relay. Both default to true; a member has no switch (U13). The same
// row holds the three relay prompt bodies (spec §8.8, U21 (a)).
const KeyRelay = "relay"

// RelaySwitchesKey is the service-registry key under which Init publishes
// the module as a RelaySwitchReader for the team module.
const RelaySwitchesKey = "hostconfig.relay-switches"

// RelayPromptsKey is the registry key of the module as a RelayPromptReader
// (GET /api/relay/prompts, spec §8.8).
const RelayPromptsKey = "hostconfig.relay-prompts"

// RelaySwitches is the stored shape and the GET field `relay.items`: the two
// switches, and the prompt bodies, where "" is unset (the built-in
// default). The bodies are omitempty, so a row without them serializes as
// it did before P9a.
type RelaySwitches struct {
	SelfSolo    bool   `json:"self_solo"` // a session that is neither lead nor member
	SelfLead    bool   `json:"self_lead"` // a lead
	PromptWrite string `json:"prompt_write,omitempty"`
	PromptFix   string `json:"prompt_fix,omitempty"`
	PromptSeed  string `json:"prompt_seed,omitempty"`
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

// RelayPromptReader is what the team module type-asserts for the prompts:
// the stored bodies, "" for each one unset.
type RelayPromptReader interface {
	RelayPrompts() (team.RelayPromptBodies, error)
}

// relayFields splits a relay object into its raw fields. Anything but an
// object of the five known keys is an error: a misspelt switch must not
// save as "left out = on" (fail-open).
func relayFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if firstByte(raw) != '{' || json.Unmarshal(raw, &fields) != nil {
		return nil, errors.New("items must be a JSON object")
	}
	for k := range fields {
		switch k {
		case "self_solo", "self_lead", "prompt_write", "prompt_fix", "prompt_seed":
		default:
			return nil, errors.New("unknown relay field " + k + "; only self_solo, self_lead, prompt_write, prompt_fix and prompt_seed")
		}
	}
	return fields, nil
}

// relaySwitchesOf reads the two switches: booleans, a field left out keeps
// its default (true). Field by field so that an explicit null is seen: a
// *bool would read `{"self_solo":null}` as "left out" and silently reset
// the switch.
func relaySwitchesOf(fields map[string]json.RawMessage) (RelaySwitches, error) {
	out := DefaultRelaySwitches
	for _, f := range []struct {
		key string
		dst *bool
	}{{"self_solo", &out.SelfSolo}, {"self_lead", &out.SelfLead}} {
		v, present := fields[f.key]
		if !present {
			continue
		}
		var b bool
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) || json.Unmarshal(v, &b) != nil {
			return RelaySwitches{}, errors.New("self_solo and self_lead must be booleans")
		}
		*f.dst = b
	}
	return out, nil
}

// relayPromptsOf reads the three bodies: strings (null refused), checked
// for UTF-8 on the field's raw bytes before decoding — encoding/json turns
// an invalid byte into U+FFFD, which a check of the decoded string can
// never see. Empty or whitespace only is "" (the default, U21 (a)); any
// other text must pass team.ValidateRelayPromptBody and is kept as written.
// A field left out is "".
func relayPromptsOf(fields map[string]json.RawMessage) (team.RelayPromptBodies, error) {
	var out team.RelayPromptBodies
	for _, f := range []struct {
		key string
		dst *string
	}{{"prompt_write", &out.Write}, {"prompt_fix", &out.Fix}, {"prompt_seed", &out.Seed}} {
		v, present := fields[f.key]
		if !present {
			continue
		}
		if !utf8.Valid(v) {
			return team.RelayPromptBodies{}, fmt.Errorf("%s: %w", f.key, team.ErrRelayPromptNotUTF8)
		}
		var s string
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) || json.Unmarshal(v, &s) != nil {
			return team.RelayPromptBodies{}, errors.New(f.key + " must be a string")
		}
		if strings.TrimSpace(s) == "" {
			continue
		}
		if err := team.ValidateRelayPromptBody(s); err != nil {
			return team.RelayPromptBodies{}, fmt.Errorf("%s: %w", f.key, err)
		}
		*f.dst = s
	}
	return out, nil
}

// normalizeRelay validates a PUT body: a JSON object of the two switches and
// the three prompt bodies, each optional. Anything else is a validation
// error.
func normalizeRelay(raw json.RawMessage) (RelaySwitches, error) {
	fields, err := relayFields(raw)
	if err != nil {
		return RelaySwitches{}, err
	}
	out, err := relaySwitchesOf(fields)
	if err != nil {
		return RelaySwitches{}, err
	}
	p, err := relayPromptsOf(fields)
	if err != nil {
		return RelaySwitches{}, err
	}
	out.PromptWrite, out.PromptFix, out.PromptSeed = p.Write, p.Fix, p.Seed
	return out, nil
}

// RelaySwitches reads the stored switches, defaults for a never-written
// key; the prompt fields of its answer are always "". A stored value whose
// switches no longer decode is an error, not a silent "on": the team module
// then refuses self relay with 503 rather than relaying against a switch it
// could not read. The bodies are not decoded here: a bad one must not turn
// self relay into a 503 (RelayPrompts reports it).
func (m *Module) RelaySwitches() (RelaySwitches, error) {
	e, err := m.store.Get(KeyRelay)
	if err != nil {
		return RelaySwitches{}, err
	}
	if e.Value == nil {
		return DefaultRelaySwitches, nil
	}
	fields, err := relayFields(e.Value)
	if err != nil {
		return RelaySwitches{}, err
	}
	return relaySwitchesOf(fields)
}

// RelayPrompts reads the stored prompt bodies ("" = unset); a stored body
// that no longer validates is an error. It reads the store on every call:
// an edit applies from the next relay (spec §8.8).
func (m *Module) RelayPrompts() (team.RelayPromptBodies, error) {
	e, err := m.store.Get(KeyRelay)
	if err != nil || e.Value == nil {
		return team.RelayPromptBodies{}, err
	}
	fields, err := relayFields(e.Value)
	if err != nil {
		return team.RelayPromptBodies{}, err
	}
	return relayPromptsOf(fields)
}

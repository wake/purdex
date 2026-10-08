package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/resources"
)

// KeyResources is the host_config row of the host resource settings (lease
// plan Task 1.1): mode, kind weights, deadline, warmup, floor, max hold and
// the EWMA half-life. The resources module reads it through
// resources.SettingsKey.
const KeyResources = "resources"

// resourcesDefaultJSON is resources.DefaultSettings() as the GET answers it
// for a never-written key (emptyFor).
var resourcesDefaultJSON = func() string {
	b, err := json.Marshal(resources.DefaultSettings())
	if err != nil {
		panic(err) // a fixed struct of strings and ints
	}
	return string(b)
}()

// resourcesFields splits a resources object into its raw fields. Anything
// but an object of the known keys is an error: a misspelt field must not
// save as "left out = default".
func resourcesFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if firstByte(raw) != '{' || json.Unmarshal(raw, &fields) != nil {
		return nil, errors.New("items must be a JSON object")
	}
	for k := range fields {
		switch k {
		case "mode", "kinds", "deadline_s", "warmup_s", "floor_pct", "max_hold_s", "ewma_half_life_s":
		default:
			return nil, errors.New("unknown resources field " + k +
				"; only mode, kinds, deadline_s, warmup_s, floor_pct, max_hold_s and ewma_half_life_s")
		}
	}
	return fields, nil
}

func isJSONNull(v json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(v), []byte("null")) }

// resourcesInt reads one optional integer field; null, strings and fractions
// are refused (a *int would read null as left out).
func resourcesInt(fields map[string]json.RawMessage, key string) (*int, error) {
	v, present := fields[key]
	if !present {
		return nil, nil
	}
	var n int
	if isJSONNull(v) || json.Unmarshal(v, &n) != nil {
		return nil, fmt.Errorf("%s must be an integer", key)
	}
	return &n, nil
}

// normalizeResources validates a PUT body (and a stored value): a JSON object
// of the known fields, each optional, then returns the settings with every
// default filled in, so that a stored row says everything it means.
func normalizeResources(raw json.RawMessage) (resources.Settings, error) {
	fields, err := resourcesFields(raw)
	if err != nil {
		return resources.Settings{}, err
	}
	var s resources.Settings
	if v, present := fields["mode"]; present {
		if isJSONNull(v) || json.Unmarshal(v, &s.Mode) != nil {
			return resources.Settings{}, errors.New("mode must be a string")
		}
		if s.Mode == "" { // Validate reads "" as unset; a written "" is a mistake
			return resources.Settings{}, errors.New("mode must be off, measure, advise or lease")
		}
	}
	if v, present := fields["kinds"]; present {
		var kinds map[string]json.RawMessage
		if firstByte(v) != '{' || json.Unmarshal(v, &kinds) != nil {
			return resources.Settings{}, errors.New("kinds must be an object of kind name to weight")
		}
		s.Kinds = make(map[string]int, len(kinds))
		for name, w := range kinds {
			var n int
			if isJSONNull(w) || json.Unmarshal(w, &n) != nil {
				return resources.Settings{}, fmt.Errorf("kinds.%s must be an integer weight", name)
			}
			s.Kinds[name] = n
		}
	}
	for _, f := range []struct {
		key string
		dst **int
	}{
		{"deadline_s", &s.DeadlineS}, {"warmup_s", &s.WarmupS}, {"floor_pct", &s.FloorPct},
		{"max_hold_s", &s.MaxHoldS}, {"ewma_half_life_s", &s.EWMAHalfLifeS},
	} {
		if *f.dst, err = resourcesInt(fields, f.key); err != nil {
			return resources.Settings{}, err
		}
	}
	if err := s.Validate(); err != nil {
		return resources.Settings{}, err
	}
	return s.Effective(), nil
}

// readResources is normalizeResources' lenient twin (the GET's view). The row
// is one setting, so a value that does not validate is invalid and answers
// {} — never the defaults, which its owner did not write (ResourcesSettings
// refuses it too).
func readResources(raw json.RawMessage) readout {
	s, err := normalizeResources(raw)
	if err != nil {
		return readout{items: struct{}{}, invalid: err}
	}
	return readout{items: s}
}

// ResourcesSettings reads the stored settings, the defaults for a
// never-written key. A stored value that no longer validates is an error,
// not a silent default: the caller decides what to do without a setting.
func (m *Module) ResourcesSettings() (resources.Settings, error) {
	e, err := m.store.Get(KeyResources)
	if err != nil {
		return resources.Settings{}, err
	}
	if e.Value == nil {
		return resources.DefaultSettings(), nil
	}
	return normalizeResources(e.Value)
}

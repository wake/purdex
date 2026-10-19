package hostconfig

import (
	"encoding/json"
	"errors"

	"github.com/wake/purdex/internal/alertsettings"
)

// KeyWwdAlerts is the host_config row of the wwd alert delivery's switch (WWD-8a-2a-0): {"push": bool}, off by default. The wwdalerts
// module reads it through alertsettings.Key at every push.
const KeyWwdAlerts = "wwd_alerts"

// wwdAlertsDefaultJSON is alertsettings.Default() as the GET answers it for a never-written key (emptyFor).
var wwdAlertsDefaultJSON = func() string {
	b, err := json.Marshal(alertsettings.Default())
	if err != nil {
		panic(err) // a fixed struct of one bool
	}
	return string(b)
}()

// normalizeWwdAlerts validates a PUT body (and a stored value): an object of exactly {"push": true|false}. A misspelt, missing or extra
// field is refused, so a save never means "left out = default".
func normalizeWwdAlerts(raw json.RawMessage) (alertsettings.Settings, error) {
	if err := rejectDuplicateKeys(raw); err != nil { // a stored value is checked like a PUT body: the last duplicate must not win
		return alertsettings.Settings{}, err
	}
	var fields map[string]json.RawMessage
	if firstByte(raw) != '{' || json.Unmarshal(raw, &fields) != nil {
		return alertsettings.Settings{}, errors.New("items must be a JSON object")
	}
	for k := range fields {
		if k != "push" {
			return alertsettings.Settings{}, errors.New("unknown wwd_alerts field " + k + "; only push")
		}
	}
	v, present := fields["push"]
	var on bool
	if !present || isJSONNull(v) || json.Unmarshal(v, &on) != nil {
		return alertsettings.Settings{}, errors.New("push must be true or false")
	}
	return alertsettings.Settings{Push: on}, nil
}

// readWwdAlerts is normalizeWwdAlerts's lenient twin (the GET's view): a value that does not read is invalid and answers the default.
func readWwdAlerts(raw json.RawMessage) readout {
	s, err := normalizeWwdAlerts(raw)
	if err != nil {
		return readout{items: alertsettings.Default(), invalid: err}
	}
	return readout{items: s}
}

// WwdAlertsSettings reads the stored setting, off for a never-written key. A stored value that no longer reads is an error.
func (m *Module) WwdAlertsSettings() (alertsettings.Settings, error) {
	e, err := m.store.Get(KeyWwdAlerts)
	if err != nil {
		return alertsettings.Settings{}, err
	}
	if e.Value == nil {
		return alertsettings.Default(), nil
	}
	return normalizeWwdAlerts(e.Value)
}

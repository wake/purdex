package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/watchsettings"
)

// KeyWatch is the host_config row of the load watcher's setting (spec §12.2). The resources module reads it through
// watchsettings.Key. This row has the switch and the thresholds; W2a adds the delivery keys.
const KeyWatch = "watch"

// watchDefaultJSON is watchsettings.Default() as the GET answers it for a never-written key (emptyFor).
var watchDefaultJSON = func() string {
	b, err := json.Marshal(watchsettings.Default())
	if err != nil {
		panic(err) // a fixed struct of numbers and a bool
	}
	return string(b)
}()

// normalizeWatch validates a PUT body (and a stored value): an object of exactly the watch fields, each in its range. A
// misspelt or missing field is refused, so a save never means "left out = default".
func normalizeWatch(raw json.RawMessage) (watchsettings.Settings, error) {
	if err := rejectDuplicateKeys(raw); err != nil {
		return watchsettings.Settings{}, err
	}
	if firstByte(raw) != '{' {
		return watchsettings.Settings{}, errors.New("items must be a JSON object")
	}
	var in struct {
		Enabled          *bool    `json:"enabled"`
		XCores           *float64 `json:"x_cores"`
		YMB              *int     `json:"y_mb"`
		TSeconds         *int     `json:"t_s"`
		PressureLevel    *int     `json:"pressure_level"`
		PressureTSeconds *int     `json:"pressure_t_s"`
		PageRate         *float64 `json:"page_rate"`
		PageTSeconds     *int     `json:"page_t_s"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return watchsettings.Settings{}, fmt.Errorf("watch: %v", err)
	}
	if dec.More() {
		return watchsettings.Settings{}, errors.New("watch: trailing data after the object")
	}
	for name, missing := range map[string]bool{
		"enabled": in.Enabled == nil, "x_cores": in.XCores == nil, "y_mb": in.YMB == nil, "t_s": in.TSeconds == nil,
		"pressure_level": in.PressureLevel == nil, "pressure_t_s": in.PressureTSeconds == nil, "page_rate": in.PageRate == nil, "page_t_s": in.PageTSeconds == nil,
	} {
		if missing {
			return watchsettings.Settings{}, errors.New(name + " is required")
		}
	}
	s := watchsettings.Settings{
		Enabled: *in.Enabled, XCores: *in.XCores, YMB: *in.YMB, TSeconds: *in.TSeconds,
		PressureLevel: *in.PressureLevel, PressureTSeconds: *in.PressureTSeconds, PageRate: *in.PageRate, PageTSeconds: *in.PageTSeconds,
	}
	for _, r := range []struct {
		name      string
		v, lo, hi float64
	}{
		{"x_cores", s.XCores, 0.1, 256},
		{"y_mb", float64(s.YMB), 64, 1 << 20},
		{"t_s", float64(s.TSeconds), 10, 3600},
		{"pressure_level", float64(s.PressureLevel), 1, 4},
		{"pressure_t_s", float64(s.PressureTSeconds), 10, 3600},
		{"page_rate", s.PageRate, 1, 1e6},
		{"page_t_s", float64(s.PageTSeconds), 10, 3600},
	} {
		if !(r.v >= r.lo && r.v <= r.hi) { // written so that NaN fails
			return watchsettings.Settings{}, fmt.Errorf("%s must be between %g and %g", r.name, r.lo, r.hi)
		}
	}
	return s, nil
}

// readWatch is normalizeWatch's lenient twin (the GET's view): a value that does not read is invalid and answers the default
// items (never what its owner did not write as a setting: WatchSettings refuses it).
func readWatch(raw json.RawMessage) readout {
	s, err := normalizeWatch(raw)
	if err != nil {
		return readout{items: watchsettings.Default(), invalid: err}
	}
	return readout{items: s}
}

// WatchSettings reads the stored setting, the default (off) for a never-written key. A stored value that no longer reads is an
// error.
func (m *Module) WatchSettings() (watchsettings.Settings, error) {
	e, err := m.store.Get(KeyWatch)
	if err != nil {
		return watchsettings.Settings{}, err
	}
	if e.Value == nil {
		return watchsettings.Default(), nil
	}
	return normalizeWatch(e.Value)
}

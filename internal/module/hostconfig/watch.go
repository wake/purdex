package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/config"
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

// normalizeWatch validates a stored value (and is what the reader and the GET read it with): an object of exactly the watch fields
// (a misspelt one is refused), each in its range, and the switch and the seven detector fields present, so a row that does not read
// whole is invalid and the watcher stays off (watchsettings.Reader). The delivery fields (W2a) may be left out and then read as their
// defaults, so a row stored before they existed still reads. Rows are stored whole (the PUT stores what normalizeWatchOver made).
func normalizeWatch(raw json.RawMessage) (watchsettings.Settings, error) {
	return normalizeWatchOver(raw, watchsettings.Default(), false)
}

// normalizeWatchOver is normalizeWatch where the patrol numbers a body leaves out keep the values of stored (the row now), so a
// client that predates them and writes the row back does not reset what a newer client set. partial is a PUT body (9c-0, #3892): the
// seven detector fields too may be left out (an app that no longer shows them does not write them), and so keep the row's values;
// patrol_idle_min and auto_wake likewise. enabled is required either way.
func normalizeWatchOver(raw json.RawMessage, stored watchsettings.Settings, partial bool) (watchsettings.Settings, error) {
	if err := rejectDuplicateKeys(raw); err != nil {
		return watchsettings.Settings{}, err
	}
	if firstByte(raw) != '{' {
		return watchsettings.Settings{}, errors.New("items must be a JSON object")
	}
	// Before the decode: encoding/json turns an invalid byte in a string into U+FFFD, so a check made afterwards never sees it
	// and the command or prompt would be stored changed.
	if !utf8.Valid(raw) {
		return watchsettings.Settings{}, errors.New("watch: the body is not valid UTF-8")
	}
	var in struct {
		Enabled          *bool     `json:"enabled"`
		XCores           *float64  `json:"x_cores"`
		YMB              *int      `json:"y_mb"`
		TSeconds         *int      `json:"t_s"`
		PressureLevel    *int      `json:"pressure_level"`
		PressureTSeconds *int      `json:"pressure_t_s"`
		PageRate         *float64  `json:"page_rate"`
		PageTSeconds     *int      `json:"page_t_s"`
		Targets          *[]string `json:"targets"`
		Mode             *string   `json:"mode"`
		PatrolIdleMin    *int      `json:"patrol_idle_min"`
		PatrolPresentMin *int      `json:"patrol_present_min"`
		PatrolBusyMin    *int      `json:"patrol_busy_min"`
		PatrolCalmMin    *int      `json:"patrol_calm_min"`
		PatrolRestMin    *int      `json:"patrol_rest_min"`
		DiskLowGB        *int      `json:"disk_low_gb"`
		AutoWake         *bool     `json:"auto_wake"`
		Model            *string   `json:"model"` // the old spelling of agent_command (741–744)
		AgentCommand     *string   `json:"agent_command"`
		Workdir          *string   `json:"workdir"`
		StartPrompt      *string   `json:"start_prompt"`
		WakePrompt       *string   `json:"wake_prompt"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return watchsettings.Settings{}, fmt.Errorf("watch: %v", err)
	}
	if dec.More() {
		return watchsettings.Settings{}, errors.New("watch: trailing data after the object")
	}
	if in.Enabled == nil {
		return watchsettings.Settings{}, errors.New("enabled is required")
	}
	if !partial {
		for name, missing := range map[string]bool{
			"x_cores": in.XCores == nil, "y_mb": in.YMB == nil, "t_s": in.TSeconds == nil, "pressure_level": in.PressureLevel == nil,
			"pressure_t_s": in.PressureTSeconds == nil, "page_rate": in.PageRate == nil, "page_t_s": in.PageTSeconds == nil,
		} {
			if missing {
				return watchsettings.Settings{}, errors.New(name + " is required")
			}
		}
	}
	// A PUT may leave the detector fields out and a save then keeps what the row has. One that is sent as null is not "left out" (json
	// turns null into an absent pointer, so it is looked for here): it was refused as no value and still is.
	if err := rejectNullDetectors(raw); err != nil {
		return watchsettings.Settings{}, err
	}
	s := watchsettings.Settings{
		Enabled: *in.Enabled, XCores: stored.XCores, YMB: stored.YMB, TSeconds: stored.TSeconds,
		PressureLevel: stored.PressureLevel, PressureTSeconds: stored.PressureTSeconds, PageRate: stored.PageRate, PageTSeconds: stored.PageTSeconds,
	}
	if in.XCores != nil {
		s.XCores = *in.XCores
	}
	if in.YMB != nil {
		s.YMB = *in.YMB
	}
	if in.TSeconds != nil {
		s.TSeconds = *in.TSeconds
	}
	if in.PressureLevel != nil {
		s.PressureLevel = *in.PressureLevel
	}
	if in.PressureTSeconds != nil {
		s.PressureTSeconds = *in.PressureTSeconds
	}
	if in.PageRate != nil {
		s.PageRate = *in.PageRate
	}
	if in.PageTSeconds != nil {
		s.PageTSeconds = *in.PageTSeconds
	}
	def := watchsettings.Default()
	// patrol_idle_min and auto_wake left out keep the row's, like the other patrol numbers below (they used to fall back to the default,
	// which reset what a person had set whenever the writer did not know the field)
	s.Targets, s.Mode, s.PatrolIdleMin, s.AutoWake, s.AgentCommand, s.Workdir = def.Targets, def.Mode, stored.PatrolIdleMin, stored.AutoWake, def.AgentCommand, def.Workdir
	if in.Targets != nil {
		s.Targets = *in.Targets
	}
	if in.Mode != nil {
		s.Mode = *in.Mode
	}
	if in.PatrolIdleMin != nil {
		s.PatrolIdleMin = *in.PatrolIdleMin
	}
	// the patrol numbers an older client does not send keep their defaults (it read-modify-writes the row it knew)
	s.PatrolPresentMin, s.PatrolBusyMin, s.PatrolCalmMin, s.PatrolRestMin, s.DiskLowGB = stored.PatrolPresentMin, stored.PatrolBusyMin, stored.PatrolCalmMin, stored.PatrolRestMin, stored.DiskLowGB
	for _, f := range []struct {
		in  *int
		out *int
	}{{in.PatrolPresentMin, &s.PatrolPresentMin}, {in.PatrolBusyMin, &s.PatrolBusyMin}, {in.PatrolCalmMin, &s.PatrolCalmMin}, {in.PatrolRestMin, &s.PatrolRestMin}, {in.DiskLowGB, &s.DiskLowGB}} {
		if f.in != nil {
			*f.out = *f.in
		}
	}
	if in.AutoWake != nil {
		s.AutoWake = *in.AutoWake
	}
	switch {
	case in.Model != nil && in.AgentCommand != nil:
		return watchsettings.Settings{}, errors.New("model is the old spelling of agent_command: send one of them")
	case in.Model != nil: // an older client's read-modify-write, or a row of 741–744: the command it stood for
		if !watchModelRe.MatchString(*in.Model) {
			return watchsettings.Settings{}, errors.New("model must be a model name")
		}
		s.AgentCommand = watchsettings.LegacyModelCommand(*in.Model)
	case in.AgentCommand != nil:
		s.AgentCommand = *in.AgentCommand
	}
	if in.Workdir != nil {
		s.Workdir = *in.Workdir
	}
	if in.StartPrompt != nil {
		s.StartPrompt = *in.StartPrompt
	}
	if in.WakePrompt != nil {
		s.WakePrompt = *in.WakePrompt
	}
	if err := validateWatchDelivery(s); err != nil {
		return watchsettings.Settings{}, err
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
		{"patrol_idle_min", float64(s.PatrolIdleMin), 1, 24 * 60},
		{"patrol_present_min", float64(s.PatrolPresentMin), 1, 24 * 60},
		{"patrol_busy_min", float64(s.PatrolBusyMin), 1, 24 * 60},
		{"patrol_calm_min", float64(s.PatrolCalmMin), 1, 24 * 60},
		{"patrol_rest_min", float64(s.PatrolRestMin), 1, 24 * 60},
		{"disk_low_gb", float64(s.DiskLowGB), 1, 100000},
	} {
		if !(r.v >= r.lo && r.v <= r.hi) { // written so that NaN fails
			return watchsettings.Settings{}, fmt.Errorf("%s must be between %g and %g", r.name, r.lo, r.hi)
		}
	}
	return s, nil
}

// rejectNullDetectors refuses a detector field whose value is null: left out is allowed, null is not a value.
func rejectNullDetectors(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("watch: %v", err)
	}
	// encoding/json matches a field name without regard to case ("X_CORES" fills x_cores), so a key is compared the same way here
	for key, v := range fields {
		for _, name := range []string{"x_cores", "y_mb", "t_s", "pressure_level", "pressure_t_s", "page_rate", "page_t_s"} {
			if strings.EqualFold(key, name) && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				return errors.New(name + " must be a number or left out")
			}
		}
	}
	return nil
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

var (
	watchModelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\[\]-]{0,63}$`)
)

const (
	maxWatchTargets = 8
	maxWatchPrompt  = 4 << 10
	maxWatchCommand = 512
	maxWatchWorkdir = 1024
)

// validateWatchDelivery checks the delivery fields (spec §12.2): targets are 1–8 distinct host aliases, mode is patrol or
// resident, the agent command one line of at most 512 bytes, the workdir an absolute or ~/ path, the prompts at most 4 KB of
// text on one line (both are typed into a shell, so no control character, newline included, may end the line early).
func validateWatchDelivery(s watchsettings.Settings) error {
	if n := len(s.Targets); n < 1 || n > maxWatchTargets {
		return fmt.Errorf("targets must list 1 to %d host aliases", maxWatchTargets)
	}
	seen := map[string]bool{}
	for _, a := range s.Targets {
		if config.ValidateAlias(a, "") != nil { // the rule a peer host's alias is held to
			return fmt.Errorf("targets: %q is not a host alias", a)
		}
		k := strings.ToLower(a)
		if seen[k] {
			return fmt.Errorf("targets: %q is listed twice", a)
		}
		seen[k] = true
	}
	if s.Mode != watchsettings.ModePatrol && s.Mode != watchsettings.ModeResident {
		return errors.New("mode must be patrol or resident")
	}
	if s.AgentCommand == "" || len(s.AgentCommand) > maxWatchCommand || !utf8.ValidString(s.AgentCommand) || hasControl(s.AgentCommand, false) {
		return fmt.Errorf("agent_command must be one line of at most %d bytes without control characters", maxWatchCommand)
	}
	if s.Workdir != "" {
		if len(s.Workdir) > maxWatchWorkdir || !(strings.HasPrefix(s.Workdir, "/") || s.Workdir == "~" || strings.HasPrefix(s.Workdir, "~/")) || hasControl(s.Workdir, false) {
			return errors.New("workdir must be an absolute path or start with ~/")
		}
	}
	for name, p := range map[string]string{"start_prompt": s.StartPrompt, "wake_prompt": s.WakePrompt} {
		if len(p) > maxWatchPrompt || !utf8.ValidString(p) || hasControl(p, false) {
			return fmt.Errorf("%s must be one line of text of at most %d bytes", name, maxWatchPrompt)
		}
	}
	return nil
}

// hasControl: s holds a control character (newline and tab allowed when text).
func hasControl(s string, text bool) bool {
	for _, r := range s {
		if text && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

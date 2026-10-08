package resources

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"time"
)

// SettingsKey is the service-registry key under which the hostconfig module
// publishes itself as a SettingsReader. It lives here so that a reader needs
// no import of hostconfig.
const SettingsKey = "hostconfig.resources-settings"

// SettingsReader is what the resources module type-asserts on the registry
// value: the host setting `resources` with defaults applied. A stored value
// that no longer validates is an error, not a silent default.
type SettingsReader interface {
	ResourcesSettings() (Settings, error)
}

// ErrSettings is wrapped by every error Validate returns.
var ErrSettings = errors.New("invalid resources settings")

// Modes of the host setting `resources`.
const (
	ModeOff = "off" // sampler stopped; lease routes grant at once
	// ModeMeasure is declared in wire.go: sampler on, leases granted at once
	// and not recorded.
	ModeAdvise = "advise" // admission computed and recorded, always granted at once
	ModeLease  = "lease"  // real waiting (spec D-3)
)

// Default values and accepted ranges of the setting (plan Task 1.1).
const (
	DefaultMode          = ModeLease
	DefaultDeadlineS     = 300
	DefaultWarmupS       = 20
	DefaultFloorPct      = 50
	DefaultMaxHoldS      = 3600
	DefaultEWMAHalfLifeS = 15

	minDeadlineS, maxDeadlineS         = 10, 590
	minWarmupS, maxWarmupS             = 0, 300
	minFloorPct, maxFloorPct           = 0, 100
	minMaxHoldS, maxMaxHoldS           = 60, 86400
	minEWMAHalfLifeS, maxEWMAHalfLifeS = 5, 300
	minKindWeight, maxKindWeight       = 1, 100
	maxKinds                           = 32
)

// DefaultKinds are the built-in kinds and their weights (spec D-7).
var DefaultKinds = map[string]int{
	"test-full": 35,
	"build":     35,
	"test-pkg":  15,
	"lint-full": 10,
}

var kindName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// Settings is the host setting `resources`. Every field is optional: an
// unset one (empty string, nil map, nil pointer) reads as its default through
// Effective or the accessors. The numbers are pointers because 0 is a valid
// WarmupS and FloorPct, so that unset and 0 stay apart.
type Settings struct {
	// Mode is off, measure, advise or lease.
	Mode string `json:"mode,omitempty"`
	// Kinds maps a kind name to its weight, 1 to 100 (host percent). A kind
	// left out keeps its default; names other than the built-ins are allowed
	// (pdx lease run --kind <name>).
	Kinds map[string]int `json:"kinds,omitempty"`
	// DeadlineS is how long a request waits before it is granted anyway.
	DeadlineS *int `json:"deadline_s,omitempty"`
	// WarmupS is how long a lease is charged its full weight.
	WarmupS *int `json:"warmup_s,omitempty"`
	// FloorPct is the share of its weight a lease is always charged.
	FloorPct *int `json:"floor_pct,omitempty"`
	// MaxHoldS ends a lease that was never released.
	MaxHoldS *int `json:"max_hold_s,omitempty"`
	// EWMAHalfLifeS is the half-life of the measured-use average.
	EWMAHalfLifeS *int `json:"ewma_half_life_s,omitempty"`
}

func intp(n int) *int { return &n }

// DefaultSettings is Settings{}.Effective(): every field set.
func DefaultSettings() Settings { return Settings{}.Effective() }

// Validate reports the first field that is out of range, naming it. Unset
// fields are fine.
func (s Settings) Validate() error {
	switch s.Mode {
	case "", ModeOff, ModeMeasure, ModeAdvise, ModeLease:
	default:
		return fmt.Errorf("%w: mode must be off, measure, advise or lease", ErrSettings)
	}
	if len(s.Kinds) > maxKinds {
		return fmt.Errorf("%w: kinds has more than %d entries", ErrSettings, maxKinds)
	}
	for name, w := range s.Kinds {
		if !kindName.MatchString(name) {
			return fmt.Errorf("%w: kinds: invalid kind name %q (lowercase letters, digits and hyphens, at most 32)", ErrSettings, name)
		}
		if w < minKindWeight || w > maxKindWeight {
			return fmt.Errorf("%w: kinds.%s must be between %d and %d", ErrSettings, name, minKindWeight, maxKindWeight)
		}
	}
	for _, f := range []struct {
		name   string
		v      *int
		lo, hi int
	}{
		{"deadline_s", s.DeadlineS, minDeadlineS, maxDeadlineS},
		{"warmup_s", s.WarmupS, minWarmupS, maxWarmupS},
		{"floor_pct", s.FloorPct, minFloorPct, maxFloorPct},
		{"max_hold_s", s.MaxHoldS, minMaxHoldS, maxMaxHoldS},
		{"ewma_half_life_s", s.EWMAHalfLifeS, minEWMAHalfLifeS, maxEWMAHalfLifeS},
	} {
		if f.v != nil && (*f.v < f.lo || *f.v > f.hi) {
			return fmt.Errorf("%w: %s must be between %d and %d", ErrSettings, f.name, f.lo, f.hi)
		}
	}
	return nil
}

// Effective returns the settings with every unset field at its default and
// every out-of-range one clamped into its range, so a stored row edited by
// hand still reads as something usable. It never mutates s or shares its map.
func (s Settings) Effective() Settings {
	out := Settings{
		Mode:          s.mode(),
		Kinds:         maps.Clone(DefaultKinds),
		DeadlineS:     intp(clampInt(s.DeadlineS, DefaultDeadlineS, minDeadlineS, maxDeadlineS)),
		WarmupS:       intp(clampInt(s.WarmupS, DefaultWarmupS, minWarmupS, maxWarmupS)),
		FloorPct:      intp(clampInt(s.FloorPct, DefaultFloorPct, minFloorPct, maxFloorPct)),
		MaxHoldS:      intp(clampInt(s.MaxHoldS, DefaultMaxHoldS, minMaxHoldS, maxMaxHoldS)),
		EWMAHalfLifeS: intp(clampInt(s.EWMAHalfLifeS, DefaultEWMAHalfLifeS, minEWMAHalfLifeS, maxEWMAHalfLifeS)),
	}
	for name, w := range s.Kinds {
		out.Kinds[name] = min(max(w, minKindWeight), maxKindWeight)
	}
	return out
}

func (s Settings) mode() string {
	switch s.Mode {
	case ModeOff, ModeMeasure, ModeAdvise, ModeLease:
		return s.Mode
	}
	return DefaultMode
}

func clampInt(v *int, def, lo, hi int) int {
	if v == nil {
		return def
	}
	return min(max(*v, lo), hi)
}

// Deadline is how long a request waits before it is granted anyway.
func (s Settings) Deadline() time.Duration {
	return time.Duration(clampInt(s.DeadlineS, DefaultDeadlineS, minDeadlineS, maxDeadlineS)) * time.Second
}

// Warmup is how long a lease is charged its full weight.
func (s Settings) Warmup() time.Duration {
	return time.Duration(clampInt(s.WarmupS, DefaultWarmupS, minWarmupS, maxWarmupS)) * time.Second
}

// Floor is the share of its weight a lease is always charged, 0 to 1.
func (s Settings) Floor() float64 {
	return float64(clampInt(s.FloorPct, DefaultFloorPct, minFloorPct, maxFloorPct)) / 100
}

// MaxHold ends a lease that was never released.
func (s Settings) MaxHold() time.Duration {
	return time.Duration(clampInt(s.MaxHoldS, DefaultMaxHoldS, minMaxHoldS, maxMaxHoldS)) * time.Second
}

// HalfLife is the half-life of the measured-use average.
func (s Settings) HalfLife() time.Duration {
	return time.Duration(clampInt(s.EWMAHalfLifeS, DefaultEWMAHalfLifeS, minEWMAHalfLifeS, maxEWMAHalfLifeS)) * time.Second
}

// Weight is the weight of a kind: the setting's, else the built-in default;
// ok is false for a kind neither knows.
func (s Settings) Weight(kind string) (weight int, ok bool) {
	if w, set := s.Kinds[kind]; set {
		return min(max(w, minKindWeight), maxKindWeight), true
	}
	w, ok := DefaultKinds[kind]
	return w, ok
}

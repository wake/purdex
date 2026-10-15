package gate

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Source says where an effective setting came from.
type Source string

const (
	SrcDefault Source = "default" // the built-in value
	SrcConfig  Source = "config"  // set in config.toml
	SrcAuto    Source = "auto"    // computed from the host when the command started
)

// raw is a table of the config file with the bookkeeping every module shares: which keys were set (their Source) and
// what was refused. A value of the wrong type or out of range is not an error for the gate (a command is never refused
// over its settings): it keeps the default and leaves a warning to print.
type raw struct {
	m    map[string]any
	path string
	st   *rawState
}

type rawState struct {
	src  map[string]Source
	warn []string
}

func newRaw(m map[string]any) raw {
	if m == nil {
		m = map[string]any{}
	}
	return raw{m: m, st: &rawState{src: map[string]Source{}}}
}

// sub is the nested table `name`; a missing or non-table value is an empty table.
func (r raw) sub(name string) raw {
	child, _ := r.m[name].(map[string]any)
	if child == nil {
		child = map[string]any{}
	}
	return raw{m: child, path: r.path + name + ".", st: r.st}
}

func (r raw) set(key string, s Source) { r.st.src[r.path+key] = s }

func (r raw) warn(key, format string, a ...any) {
	r.st.warn = append(r.st.warn, fmt.Sprintf("%s%s: %s (default kept)", r.path, key, fmt.Sprintf(format, a...)))
}

// has says the key is in the table.
func (r raw) has(key string) bool { _, ok := r.m[key]; return ok }

func (r raw) Bool(key string, def bool) bool {
	r.set(key, SrcDefault)
	v, ok := r.m[key]
	if !ok {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		r.warn(key, "want true or false, got %v", v)
		return def
	}
	r.set(key, SrcConfig)
	return b
}

func (r raw) Int(key string, def, lo, hi int) int {
	r.set(key, SrcDefault)
	v, ok := r.m[key]
	if !ok {
		return def
	}
	n, ok := asInt(v)
	if !ok || n < lo || n > hi {
		r.warn(key, "want a whole number from %d to %d, got %v", lo, hi, v)
		return def
	}
	r.set(key, SrcConfig)
	return n
}

func (r raw) Float(key string, def, lo float64) float64 {
	r.set(key, SrcDefault)
	v, ok := r.m[key]
	if !ok {
		return def
	}
	f, ok := asFloat(v)
	if !ok || !finite(f) || f < lo {
		r.warn(key, "want a number of at least %v, got %v", lo, v)
		return def
	}
	r.set(key, SrcConfig)
	return f
}

// Enum is a string from a fixed set.
func (r raw) Enum(key, def string, allowed ...string) string {
	r.set(key, SrcDefault)
	v, ok := r.m[key]
	if !ok {
		return def
	}
	s, _ := v.(string)
	for _, a := range allowed {
		if s == a {
			r.set(key, SrcConfig)
			return s
		}
	}
	r.warn(key, "want one of %s, got %v", strings.Join(allowed, ", "), v)
	return def
}

// Name is a short identifier (a profile name): lowercase letters, digits, hyphen and underscore.
func (r raw) Name(key, def string) string {
	r.set(key, SrcDefault)
	v, ok := r.m[key]
	if !ok {
		return def
	}
	s, _ := v.(string)
	if !nameRe.MatchString(s) {
		r.warn(key, "want a name of lowercase letters, digits, - and _ (up to 32), got %v", v)
		return def
	}
	r.set(key, SrcConfig)
	return s
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func (r raw) Strings(key string, def []string) []string {
	r.set(key, SrcDefault)
	v, ok := r.m[key]
	if !ok {
		return def
	}
	list, ok := v.([]any)
	if !ok {
		r.warn(key, "want a list of strings, got %v", v)
		return def
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		s, ok := e.(string)
		if !ok {
			r.warn(key, "want a list of strings, got %v", v)
			return def
		}
		out = append(out, s)
	}
	r.set(key, SrcConfig)
	return out
}

// Param is a number that may be left to the gate: the string "auto" is computed from the host when a command starts.
type Param struct {
	Auto bool
	N    int
}

func (p Param) String() string {
	if p.Auto {
		return "auto"
	}
	return fmt.Sprint(p.N)
}

// IntOrAuto reads a whole number from lo to hi, or the string "auto".
func (r raw) IntOrAuto(key string, def Param, lo, hi int) Param {
	r.set(key, SrcDefault)
	v, ok := r.m[key]
	if !ok {
		return def
	}
	if s, isStr := v.(string); isStr && s == "auto" {
		r.set(key, SrcConfig)
		return Param{Auto: true}
	}
	n, ok := asInt(v)
	if !ok || n < lo || n > hi {
		r.warn(key, `want "auto" or a whole number from %d to %d, got %v`, lo, hi, v)
		return def
	}
	r.set(key, SrcConfig)
	return Param{N: n}
}

// FloatOrAuto reads a number of at least lo, or the string "auto".
func (r raw) FloatOrAuto(key string, def float64, defAuto bool, lo float64) (f float64, auto bool) {
	r.set(key, SrcDefault)
	v, ok := r.m[key]
	if !ok {
		return def, defAuto
	}
	if s, isStr := v.(string); isStr && s == "auto" {
		r.set(key, SrcConfig)
		return def, true
	}
	n, ok := asFloat(v)
	if !ok || !finite(n) || n < lo {
		r.warn(key, `want "auto" or a number of at least %v, got %v`, lo, v)
		return def, defAuto
	}
	r.set(key, SrcConfig)
	return n, false
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int64:
		return int(n), true
	case int:
		return n, true
	case float64:
		if n == math.Trunc(n) {
			return int(n), true
		}
	}
	return 0, false
}

// finite says the number is neither NaN nor an infinity (TOML can spell both: a NaN would pass every `< lo` test).
func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

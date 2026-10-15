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
	SrcProfile Source = "profile" // set by the active profile
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
	val  map[string]string // the effective value of every key that was read, as text
	warn []string
}

func newRaw(m map[string]any) raw {
	if m == nil {
		m = map[string]any{}
	}
	return raw{m: m, st: &rawState{src: map[string]Source{}, val: map[string]string{}}}
}

// sub is the nested table `name`; a missing value is an empty table, and one of the wrong kind is an empty table with a
// warning (the module keeps its defaults).
func (r raw) sub(name string) raw {
	child, isTable := r.m[name].(map[string]any)
	if v, present := r.m[name]; present && !isTable {
		r.warn(name, "want a table, got %v", v) // [[gate.slots]], slots = 3 …; the one list of tables, intercept's rule, is read by its module without sub
	}
	if child == nil {
		child = map[string]any{}
	}
	return raw{m: child, path: r.path + name + ".", st: r.st}
}

// fin records the effective value of a key and where it came from, and returns the value.
func fin[T any](r raw, key string, v T, s Source) T {
	r.st.src[r.path+key] = s
	r.st.val[r.path+key] = fmt.Sprint(v)
	return v
}

func (r raw) warn(key, format string, a ...any) {
	r.st.warn = append(r.st.warn, fmt.Sprintf("%s%s: %s (default kept)", r.path, key, fmt.Sprintf(format, a...)))
}

// has says the key is in the table.
func (r raw) has(key string) bool { _, ok := r.m[key]; return ok }

func (r raw) Bool(key string, def bool) bool {
	v, ok := r.m[key]
	if !ok {
		return fin(r, key, def, SrcDefault)
	}
	b, ok := v.(bool)
	if !ok {
		r.warn(key, "want true or false, got %v", v)
		return fin(r, key, def, SrcDefault)
	}
	return fin(r, key, b, SrcConfig)
}

func (r raw) Int(key string, def, lo, hi int) int {
	v, ok := r.m[key]
	if !ok {
		return fin(r, key, def, SrcDefault)
	}
	n, ok := asInt(v)
	if !ok || n < lo || n > hi {
		r.warn(key, "want a whole number from %d to %d, got %v", lo, hi, v)
		return fin(r, key, def, SrcDefault)
	}
	return fin(r, key, n, SrcConfig)
}

func (r raw) Float(key string, def, lo, hi float64) float64 {
	v, ok := r.m[key]
	if !ok {
		return fin(r, key, def, SrcDefault)
	}
	f, ok := asFloat(v)
	if !ok || !finite(f) || f < lo || f > hi {
		r.warn(key, "want a number from %v to %v, got %v", lo, hi, v)
		return fin(r, key, def, SrcDefault)
	}
	return fin(r, key, f, SrcConfig)
}

// Enum is a string from a fixed set.
func (r raw) Enum(key, def string, allowed ...string) string {
	v, ok := r.m[key]
	if !ok {
		return fin(r, key, def, SrcDefault)
	}
	s, _ := v.(string)
	for _, a := range allowed {
		if s == a {
			return fin(r, key, s, SrcConfig)
		}
	}
	r.warn(key, "want one of %s, got %v", strings.Join(allowed, ", "), v)
	return fin(r, key, def, SrcDefault)
}

// Name is a short identifier (a profile name): lowercase letters, digits, hyphen and underscore.
func (r raw) Name(key, def string) string {
	v, ok := r.m[key]
	if !ok {
		return fin(r, key, def, SrcDefault)
	}
	s, _ := v.(string)
	if !nameRe.MatchString(s) {
		r.warn(key, "want a name of lowercase letters, digits, - and _ (up to 32), got %v", v)
		return fin(r, key, def, SrcDefault)
	}
	return fin(r, key, s, SrcConfig)
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func (r raw) Strings(key string, def []string) []string {
	v, ok := r.m[key]
	if !ok {
		return finStrings(r, key, def, SrcDefault)
	}
	list, ok := v.([]any)
	if !ok {
		r.warn(key, "want a list of strings, got %v", v)
		return finStrings(r, key, def, SrcDefault)
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		s, ok := e.(string)
		if !ok {
			r.warn(key, "want a list of strings, got %v", v)
			return finStrings(r, key, def, SrcDefault)
		}
		out = append(out, s)
	}
	return finStrings(r, key, out, SrcConfig)
}

func finStrings(r raw, key string, v []string, s Source) []string {
	r.st.src[r.path+key], r.st.val[r.path+key] = s, strings.Join(v, ",")
	return v
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
	v, ok := r.m[key]
	if !ok {
		return fin(r, key, def, SrcDefault)
	}
	if s, isStr := v.(string); isStr && s == "auto" {
		return fin(r, key, Param{Auto: true}, SrcConfig)
	}
	n, ok := asInt(v)
	if !ok || n < lo || n > hi {
		r.warn(key, `want "auto" or a whole number from %d to %d, got %v`, lo, hi, v)
		return fin(r, key, def, SrcDefault)
	}
	return fin(r, key, Param{N: n}, SrcConfig)
}

// FloatOrAuto reads a number of at least lo, or the string "auto".
func (r raw) FloatOrAuto(key string, def float64, defAuto bool, lo, hi float64) (f float64, auto bool) {
	v, ok := r.m[key]
	if !ok {
		return finAuto(r, key, def, defAuto, SrcDefault)
	}
	if s, isStr := v.(string); isStr && s == "auto" {
		return finAuto(r, key, def, true, SrcConfig)
	}
	n, ok := asFloat(v)
	if !ok || !finite(n) || n < lo || n > hi {
		r.warn(key, `want "auto" or a number from %v to %v, got %v`, lo, hi, v)
		return finAuto(r, key, def, defAuto, SrcDefault)
	}
	return finAuto(r, key, n, false, SrcConfig)
}

// finAuto is fin for a number that may be "auto" (the value shown is then "auto").
func finAuto(r raw, key string, f float64, auto bool, s Source) (float64, bool) {
	if auto {
		fin(r, key, "auto", s)
	} else {
		fin(r, key, f, s)
	}
	return f, auto
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

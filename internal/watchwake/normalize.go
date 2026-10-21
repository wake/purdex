package watchwake

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"
)

// FieldError is one problem of a document. Path is rules.<rule>.<field>, with [i] for an array element; Code is what a client translates.
type FieldError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// The codes (spec section 5).
const (
	CodeMissingField   = "missing_field"
	CodeUnknownField   = "unknown_field"
	CodeBadType        = "bad_type"
	CodeOutOfRange     = "out_of_range"
	CodeBadTime        = "bad_time"
	CodeDuplicateTime  = "duplicate_time"
	CodeTooMany        = "too_many"
	CodeBadLevel       = "bad_level"
	CodeBadKind        = "bad_kind"
	CodePromptTooLong  = "prompt_too_long"
	CodePromptBadChar  = "prompt_bad_char"
	CodeBadTz          = "bad_tz"
	CodeBadVersion     = "bad_version"
	maxPromptBytes     = 2000
	maxTimes           = 24
	maxKinds           = 32
	maxIntervalMinutes = 1440
)

var (
	reTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	reKind = regexp.MustCompile(`^[a-z][a-z0-9_:-]{0,63}$`)
)

type walker struct{ errs []FieldError }

func (w *walker) add(path, code, format string, args ...any) {
	w.errs = append(w.errs, FieldError{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// object checks that v is an object, fills the fields it can find and reports the ones it cannot: missing ones in the order given, then
// unknown ones sorted. It returns nil when v is not an object.
func (w *walker) object(path string, v any, fields ...string) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		w.add(path, CodeBadType, "must be an object")
		return nil
	}
	for _, f := range fields {
		if _, has := m[f]; !has {
			w.add(join(path, f), CodeMissingField, "is required")
		}
	}
	var unknown []string
	for k := range m {
		if !slices.Contains(fields, k) {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		w.add(join(path, k), CodeUnknownField, "is not a field of this document")
	}
	return m
}

func (w *walker) boolean(path string, v any, ok bool) (out bool) {
	if !ok {
		return false
	}
	b, isBool := v.(bool)
	if !isBool {
		w.add(path, CodeBadType, "must be true or false")
	}
	return b
}

// integer reads a whole number in [lo, hi]; zero is also accepted when zeroOK.
func (w *walker) integer(path string, v any, ok bool, lo, hi int, zeroOK bool) int {
	if !ok {
		return 0
	}
	n, isNum := v.(json.Number)
	if !isNum {
		w.add(path, CodeBadType, "must be a whole number")
		return 0
	}
	i, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil {
		w.add(path, CodeBadType, "must be a whole number")
		return 0
	}
	if (zeroOK && i == 0) || (i >= int64(lo) && i <= int64(hi)) {
		return int(i)
	}
	if zeroOK {
		w.add(path, CodeOutOfRange, "must be 0 or between %d and %d", lo, hi)
	} else {
		w.add(path, CodeOutOfRange, "must be between %d and %d", lo, hi)
	}
	return 0
}

func (w *walker) prompt(path string, v any, ok bool) string {
	if !ok {
		return ""
	}
	s, isStr := v.(string)
	if !isStr {
		w.add(path, CodeBadType, "must be a string")
		return ""
	}
	if len(s) > maxPromptBytes {
		w.add(path, CodePromptTooLong, "is %d bytes; the limit is %d", len(s), maxPromptBytes)
	}
	if !utf8.ValidString(s) || badPromptChar(s) {
		w.add(path, CodePromptBadChar, "may hold line breaks and tabs, but no other control character")
	}
	return s
}

// badPromptChar says whether s has a control character other than a line feed or a tab (C0, DEL, C1, U+2028, U+2029).
func badPromptChar(s string) bool {
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, r == 0x2028, r == 0x2029:
			return true
		}
	}
	return false
}

// array returns the elements of an array, or reports bad_type.
func (w *walker) array(path string, v any, ok bool) ([]any, bool) {
	if !ok {
		return nil, false
	}
	a, isArr := v.([]any)
	if !isArr {
		w.add(path, CodeBadType, "must be an array")
	}
	return a, isArr
}

// Normalize reads a document strictly: every field required, none unknown, every value in range. It reports every problem at once; the
// settings it returns are meaningful only when there is none. Times, levels and kinds_off come back sorted (levels in the order push,
// digest, watch), so the same document always has the same etag.
func Normalize(raw []byte) (Settings, []FieldError) {
	if !utf8.Valid(raw) { // the decoder would swap a bad byte for U+FFFD and the document would be stored changed
		return Settings{}, []FieldError{{Path: "", Code: CodeBadType, Message: "is not valid UTF-8"}}
	}
	if hasUnpairedSurrogate(raw) { // \ud800 is valid UTF-8 on the wire and U+FFFD once decoded
		return Settings{}, []FieldError{{Path: "", Code: CodeBadType, Message: "has an unpaired surrogate escape"}}
	}
	dupes := duplicateKeys(raw)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return Settings{}, []FieldError{{Path: "", Code: CodeBadType, Message: "is not a JSON document"}}
	}
	if _, err := dec.Token(); err != io.EOF {
		return Settings{}, []FieldError{{Path: "", Code: CodeBadType, Message: "has data after the document"}}
	}
	w := &walker{}
	for _, p := range dupes {
		w.add(p, CodeBadType, "appears twice in its object")
	}
	var s Settings
	top := w.object("", root, "version", "enabled", "auto_start", "rules")
	if top == nil {
		return Settings{}, w.errs
	}
	get := func(m map[string]any, k string) (any, bool) { v, ok := m[k]; return v, ok }

	if v, ok := get(top, "version"); ok {
		if n, isNum := v.(json.Number); !isNum || n.String() != strconv.Itoa(Version) {
			w.add("version", CodeBadVersion, "this build reads version %d only", Version)
		}
		s.Version = Version
	}
	v, ok := get(top, "enabled")
	s.Enabled = w.boolean("enabled", v, ok)
	v, ok = get(top, "auto_start")
	s.AutoStart = w.boolean("auto_start", v, ok)

	if rv, ok := get(top, "rules"); ok {
		w.rules(&s.Rules, rv)
	}
	s.ensureLists()
	return s, w.errs
}

func (w *walker) rules(r *Rules, v any) {
	rules := w.object("rules", v, "patrol", "disk_low", "wwd_alert", "unattended_changed", "user_back")
	if rules == nil {
		return
	}
	if pv, ok := rules["patrol"]; ok {
		w.patrol(&r.Patrol, pv)
	}
	if dv, ok := rules["disk_low"]; ok {
		if m := w.object("rules.disk_low", dv, "enabled", "prompt", "below_gb"); m != nil {
			const p = "rules.disk_low"
			x, has := m["enabled"]
			r.DiskLow.Enabled = w.boolean(p+".enabled", x, has)
			x, has = m["prompt"]
			r.DiskLow.Prompt = w.prompt(p+".prompt", x, has)
			x, has = m["below_gb"]
			r.DiskLow.BelowGB = w.integer(p+".below_gb", x, has, 1, 100000, false)
		}
	}
	if av, ok := rules["wwd_alert"]; ok {
		w.wwdAlert(&r.WwdAlert, av)
	}
	if uv, ok := rules["unattended_changed"]; ok {
		if m := w.object("rules.unattended_changed", uv, "enabled", "prompt"); m != nil {
			const p = "rules.unattended_changed"
			x, has := m["enabled"]
			r.UnattendedChanged.Enabled = w.boolean(p+".enabled", x, has)
			x, has = m["prompt"]
			r.UnattendedChanged.Prompt = w.prompt(p+".prompt", x, has)
		}
	}
	if bv, ok := rules["user_back"]; ok {
		if m := w.object("rules.user_back", bv, "enabled", "prompt", "away_min"); m != nil {
			const p = "rules.user_back"
			x, has := m["enabled"]
			r.UserBack.Enabled = w.boolean(p+".enabled", x, has)
			x, has = m["prompt"]
			r.UserBack.Prompt = w.prompt(p+".prompt", x, has)
			x, has = m["away_min"]
			r.UserBack.AwayMin = w.integer(p+".away_min", x, has, 5, maxIntervalMinutes, false)
		}
	}
}

func (w *walker) patrol(p *Patrol, v any) {
	const path = "rules.patrol"
	m := w.object(path, v, "prompt", "interval_min", "max_gap_hours", "times", "tz")
	if m == nil {
		return
	}
	x, has := m["prompt"]
	p.Prompt = w.prompt(path+".prompt", x, has)
	if iv, ok := m["interval_min"]; ok {
		const ip = path + ".interval_min"
		if im := w.object(ip, iv, "resting", "present", "away", "busy", "calm"); im != nil {
			for _, f := range []struct {
				key string
				dst *int
			}{{"resting", &p.IntervalMin.Resting}, {"present", &p.IntervalMin.Present}, {"away", &p.IntervalMin.Away}, {"busy", &p.IntervalMin.Busy}, {"calm", &p.IntervalMin.Calm}} {
				x, has := im[f.key]
				*f.dst = w.integer(ip+"."+f.key, x, has, 1, maxIntervalMinutes, true)
			}
		}
	}
	x, has = m["max_gap_hours"]
	p.MaxGapHours = w.integer(path+".max_gap_hours", x, has, 1, 72, false) // no zero: the patrol cannot be switched off by a gap
	if tv, ok := m["times"]; ok {
		p.Times = w.times(path+".times", tv)
	}
	if zv, ok := m["tz"]; ok {
		s, isStr := zv.(string)
		switch {
		case !isStr:
			w.add(path+".tz", CodeBadType, "must be a string")
		case !validZone(s):
			w.add(path+".tz", CodeBadTz, "is not an IANA time zone this host knows")
		default:
			p.TZ = s
		}
	}
}

func validZone(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

func (w *walker) times(path string, v any) []string {
	a, ok := w.array(path, v, true)
	if !ok {
		return nil
	}
	if len(a) > maxTimes {
		w.add(path, CodeTooMany, "has %d entries; the limit is %d", len(a), maxTimes)
	}
	seen := map[string]bool{}
	var out []string
	for i, e := range a {
		ep := fmt.Sprintf("%s[%d]", path, i)
		s, isStr := e.(string)
		switch {
		case !isStr:
			w.add(ep, CodeBadType, "must be a string")
		case !reTime.MatchString(s):
			w.add(ep, CodeBadTime, "must be HH:MM, 24-hour, two digits each")
		case seen[s]:
			w.add(ep, CodeDuplicateTime, "is listed twice")
		default:
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func (w *walker) wwdAlert(a *WwdAlert, v any) {
	const path = "rules.wwd_alert"
	m := w.object(path, v, "enabled", "prompt", "levels", "kinds_off")
	if m == nil {
		return
	}
	x, has := m["enabled"]
	a.Enabled = w.boolean(path+".enabled", x, has)
	x, has = m["prompt"]
	a.Prompt = w.prompt(path+".prompt", x, has)

	if lv, ok := m["levels"]; ok {
		if arr, isArr := w.array(path+".levels", lv, true); isArr {
			seen := map[string]bool{}
			for i, e := range arr {
				ep := fmt.Sprintf("%s.levels[%d]", path, i)
				s, isStr := e.(string)
				switch {
				case !isStr || !slices.Contains(allLevels, s):
					w.add(ep, CodeBadLevel, "must be one of push, digest, watch")
				case seen[s]:
					w.add(ep, CodeBadLevel, "is listed twice")
				default:
					seen[s] = true
				}
			}
			for _, l := range allLevels { // the canonical order
				if seen[l] {
					a.Levels = append(a.Levels, l)
				}
			}
		}
	}
	if kv, ok := m["kinds_off"]; ok {
		if arr, isArr := w.array(path+".kinds_off", kv, true); isArr {
			if len(arr) > maxKinds {
				w.add(path+".kinds_off", CodeTooMany, "has %d entries; the limit is %d", len(arr), maxKinds)
			}
			seen := map[string]bool{}
			for i, e := range arr {
				ep := fmt.Sprintf("%s.kinds_off[%d]", path, i)
				s, isStr := e.(string)
				switch {
				case !isStr || !reKind.MatchString(s):
					w.add(ep, CodeBadKind, "must match ^[a-z][a-z0-9_:-]{0,63}$")
				case seen[s]:
					w.add(ep, CodeBadKind, "is listed twice")
				default:
					seen[s] = true
					a.KindsOff = append(a.KindsOff, s)
				}
			}
			sort.Strings(a.KindsOff)
		}
	}
}

// hasUnpairedSurrogate says whether a JSON string (a key or a value) holds a \uD800-\uDFFF escape that is not half of a high+low pair.
func hasUnpairedSurrogate(raw []byte) bool {
	hex4 := func(i int) (rune, bool) { // raw[i:i+4] as a code unit
		if i+4 > len(raw) {
			return 0, false
		}
		n, err := strconv.ParseUint(string(raw[i:i+4]), 16, 32)
		return rune(n), err == nil
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			inString = c == '"'
			continue
		}
		switch {
		case c == '"':
			inString = false
		case c == '\\' && i+1 < len(raw) && raw[i+1] == 'u':
			u, ok := hex4(i + 2)
			switch {
			case !ok: // not a valid escape: the decoder reports it
			case u >= 0xD800 && u <= 0xDBFF:
				if i+11 >= len(raw) || raw[i+6] != '\\' || raw[i+7] != 'u' {
					return true
				}
				if lo, ok := hex4(i + 8); !ok || lo < 0xDC00 || lo > 0xDFFF {
					return true
				}
				i += 11
				continue
			case u >= 0xDC00 && u <= 0xDFFF:
				return true
			}
			i += 5
		case c == '\\':
			i++ // the escaped character: an escaped quote or backslash must not end or confuse the string
		}
	}
	return false
}

// duplicateKeys is the paths of the object members that repeat a key of their object, at any depth (the second one is named), in document
// order. encoding/json keeps the last of two equal keys and says nothing, so a document could mean one thing here and another elsewhere.
// A document that does not parse has none to report: Normalize's own decode reports it.
func duplicateKeys(raw []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out []string
	var walk func(path string) bool
	walk = func(path string) bool {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		switch d, _ := tok.(json.Delim); d {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return false
				}
				key, _ := kt.(string)
				p := join(path, key)
				if seen[key] {
					out = append(out, p)
				}
				seen[key] = true
				if !walk(p) {
					return false
				}
			}
			_, err := dec.Token() // }
			return err == nil
		case '[':
			for i := 0; dec.More(); i++ {
				if !walk(fmt.Sprintf("%s[%d]", path, i)) {
					return false
				}
			}
			_, err := dec.Token() // ]
			return err == nil
		}
		return true
	}
	walk("")
	return out
}

// PeekVersion reads the version of a stored document without validating the rest, so a document of a newer build can be told from damage.
func PeekVersion(raw []byte) (int, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return 0, false
	}
	v, err := strconv.Atoi(string(bytes.TrimSpace(m["version"])))
	return v, err == nil
}

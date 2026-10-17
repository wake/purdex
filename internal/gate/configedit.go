package gate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
)

// `pdx gate set` edits config.toml as text, so the person's comments, order and other tables stay as they are; the new
// file is checked (it parses, the key is a setting the gate reads, the value is accepted) before it replaces the old one
// by rename, so a reader sees the whole old file or the whole new one.

var (
	keyRe       = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*){0,3}$`)
	bareWordRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	errNoSetKey = errors.New("a setting is written <module>.<key>=<value> (enabled and profile have no module)")
)

// editor is the text edit (a variable so a test can put a bad one in its place).
var editor = editTOML

// SetResult says what a set changed.
type SetResult struct {
	Key      string
	Profile  string // the profile table written to, "" for [gate] itself
	Old, New string // effective value before and after (of the active profile)
	NewSrc   Source
}

// Set writes key = value into the config file: under [gate], or under [gate.profiles.<profile>] when profile is not
// empty. The key is "module.key" (or "enabled", "profile"); value is TOML ("2", "true", "\"nice\"", "[\"go\"]"), and a
// bare word such as auto or nice is taken as a string.
func Set(path, profile, key, value string) (SetResult, error) {
	res := SetResult{Key: key, Profile: profile}
	if path == "" {
		return res, errors.New("no config path")
	}
	if !keyRe.MatchString(key) {
		return res, fmt.Errorf("%q is not a setting name: %w", key, errNoSetKey)
	}
	if profile != "" && (!nameRe.MatchString(profile) || !strings.Contains(key, ".")) {
		return res, fmt.Errorf("a profile holds module settings (<module>.<key>); %q in profile %q is not one", key, profile)
	}
	lit, err := tomlLiteral(value)
	if err != nil {
		return res, err
	}
	unlock, err := lockFile(path + ".lock")
	if err != nil {
		return res, err
	}
	defer unlock()

	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return res, err
	}
	res.Old = effectiveOf(string(old), profile, key)
	edited := editor(string(old), headerFor(profile, key), leafOf(key), lit)
	var top map[string]any
	if _, err := toml.Decode(edited, &top); err != nil {
		return res, fmt.Errorf("the file would not parse after the change (is %s set another way, e.g. a dotted key?): %w", key, err)
	}
	if !onlyChanged(string(old), edited, append(strings.Split(headerFor(profile, key), "."), leafOf(key))) {
		return res, fmt.Errorf("refused: the edit would have changed something besides %s (a layout this editor does not follow); change the file by hand", key)
	}
	// The value is checked on its own: with another profile active, that profile's value would hide a bad one.
	checkAs := profile
	if profile == "" {
		checkAs = "default"
	}
	cfg := configFor(top, checkAs)
	if _, known := cfg.Values[key]; !known {
		return res, fmt.Errorf("%q is not a setting the gate reads", key)
	}
	for _, w := range cfg.Warnings {
		if strings.HasPrefix(w, key+":") {
			return res, fmt.Errorf("refused: %s", w)
		}
	}
	active := configFor(top, "")
	if profile != "" {
		active = cfg
	}
	res.New, res.NewSrc = active.Values[key], active.Sources[key]
	if err := writeAtomic(path, []byte(edited)); err != nil {
		return res, err
	}
	return res, nil
}

// UseProfile makes name the active profile. The profile need not be defined yet (it then changes nothing); known says it is.
func UseProfile(path, name string) (known bool, err error) {
	if !nameRe.MatchString(name) {
		return false, fmt.Errorf("a profile name is lowercase letters, digits, - and _ (up to 32), got %q", name)
	}
	if _, err := Set(path, "", "profile", name); err != nil {
		return false, err
	}
	cfg, _ := Load(path)
	for _, p := range cfg.Profiles {
		if p == name {
			return true, nil
		}
	}
	return name == "default" || name == ReleaseProfile, nil
}

// configFor builds the config of a file's table as if profile (when not empty) were the active one.
func configFor(top map[string]any, profile string) Config {
	g, _ := top["gate"].(map[string]any)
	if profile != "" {
		cp := make(map[string]any, len(g)+1)
		for k, v := range g {
			cp[k] = v
		}
		cp["profile"] = profile
		g = cp
	}
	return FromTable(g)
}

func effectiveOf(text, profile, key string) string {
	var top map[string]any
	if _, err := toml.Decode(text, &top); err != nil {
		return ""
	}
	return configFor(top, profile).Values[key]
}

// onlyChanged says that, reading both texts as TOML, nothing differs except (possibly) the value at path. It is what
// makes a text edit safe: whatever the layout of the file, a set that moved another setting is refused.
func onlyChanged(before, after string, path []string) bool {
	var a, b map[string]any
	if _, err := toml.Decode(before, &a); err != nil {
		return false
	}
	if _, err := toml.Decode(after, &b); err != nil {
		return false
	}
	return sameExcept(a, b, path)
}

func sameExcept(a, b any, path []string) bool {
	if len(path) == 0 {
		return true
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if !aok || !bok {
		return false
	}
	for k, av := range am {
		bv, present := bm[k]
		if k == path[0] {
			if present && !sameExcept(av, bv, path[1:]) {
				return false
			}
			continue
		}
		if !present || !reflect.DeepEqual(av, bv) {
			return false
		}
	}
	for k := range bm {
		if _, present := am[k]; !present && k != path[0] {
			return false
		}
	}
	return true
}

func headerFor(profile, key string) string {
	parts := []string{"gate"}
	if profile != "" {
		parts = append(parts, "profiles", profile)
	}
	if i := strings.LastIndex(key, "."); i >= 0 {
		parts = append(parts, strings.Split(key[:i], ".")...)
	}
	return strings.Join(parts, ".")
}

func leafOf(key string) string { return key[strings.LastIndex(key, ".")+1:] }

// tomlLiteral turns what the person typed into a TOML value: a value that already is one is kept in canonical form, a
// bare word becomes a string.
func tomlLiteral(text string) (string, error) {
	text = strings.TrimSpace(text)
	var probe map[string]any
	if _, err := toml.Decode("v = "+text, &probe); err != nil {
		if bareWordRe.MatchString(text) {
			return strconv.Quote(text), nil
		}
		return "", fmt.Errorf("%q is not a TOML value (numbers, true/false, \"text\", [\"a\", \"b\"], or a bare word)", text)
	}
	return formatTOML(probe["v"]), nil
}

func formatTOML(v any) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = formatTOML(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprint(x)
	}
}

// editTOML sets leaf = lit in the table [header], in place if the key is there, else as the first line of the table;
// a table that is not in the file is appended.
func editTOML(text, header, leaf, lit string) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	keyLine := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(leaf) + `\s*=`)
	in, headerAt := false, -1
	inString := "" // the quote of a multi-line string we are inside: its lines are text, not headers or keys
	for i, l := range lines {
		if inString != "" {
			if strings.Contains(l, inString) {
				inString = ""
			}
			continue
		}
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "[") && !(in && keyLine.MatchString(l)) {
			code := beforeComment(l)
			for _, q := range []string{`"""`, `'''`} {
				if strings.Count(code, q)%2 == 1 {
					inString = q
				}
			}
		}
		if strings.HasPrefix(t, "[") {
			if in {
				break
			}
			if tableName(t) == header {
				in, headerAt = true, i
			}
			continue
		}
		if in && keyLine.MatchString(l) {
			end := valueEnd(lines, i)
			out := append([]string{}, lines[:i]...)
			out = append(out, leaf+" = "+lit)
			out = append(out, lines[end+1:]...)
			return strings.Join(out, "\n") + "\n"
		}
	}
	if headerAt >= 0 {
		out := append([]string{}, lines[:headerAt+1]...)
		out = append(out, leaf+" = "+lit)
		out = append(out, lines[headerAt+1:]...)
		return strings.Join(out, "\n") + "\n"
	}
	out := strings.Join(lines, "\n")
	if out != "" {
		out += "\n\n"
	}
	return out + "[" + header + "]\n" + leaf + " = " + lit + "\n"
}

// beforeComment is the line without its trailing comment: the part before a # that is not inside quotes.
func beforeComment(l string) string {
	quote := byte(0)
	for i := 0; i < len(l); i++ {
		c := l[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return l[:i]
		}
	}
	return l
}

// tableName is the dotted name of a `[a.b]` header line, whatever the spaces and quotes around its parts (`[ a . "b" ]`);
// "" when the line is not a plain table header (an array of tables, `[[a]]`, is not one).
func tableName(line string) string {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "[[") || !strings.HasPrefix(t, "[") {
		return ""
	}
	var name strings.Builder
	quote := byte(0)
	for i := 1; i < len(t); i++ {
		c := t[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '.' {
				return "\x00" // a quoted key that holds a dot is one key, not a path: it is never the table asked for
			} else {
				name.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ']':
			return name.String()
		case c == ' ' || c == '\t':
		default:
			name.WriteByte(c)
		}
	}
	return ""
}

// valueEnd is the index of the last line of the value that starts on line i: the same line, unless it opens an array or
// a multi-line string that goes on.
func valueEnd(lines []string, i int) int {
	_, v, _ := strings.Cut(lines[i], "=")
	v = strings.TrimSpace(v)
	for _, q := range []string{`"""`, `'''`} {
		if strings.HasPrefix(v, q) && strings.Count(v, q) < 2 {
			for j := i + 1; j < len(lines); j++ {
				if strings.Contains(lines[j], q) {
					return j
				}
			}
			return len(lines) - 1
		}
	}
	depth, quote := 0, byte(0)
	for j := i; j < len(lines); j++ {
		src := lines[j]
		if j == i {
			src = v
		}
		for k := 0; k < len(src); k++ {
			c := src[k]
			switch {
			case quote != 0:
				if c == '\\' && quote == '"' {
					k++
				} else if c == quote {
					quote = 0
				}
			case c == '"' || c == '\'':
				quote = c
			case c == '#':
				k = len(src)
			case c == '[':
				depth++
			case c == ']':
				depth--
			}
		}
		if depth <= 0 {
			return j
		}
	}
	return len(lines) - 1
}

func writeAtomic(path string, data []byte) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real // a config.toml that is a link keeps being a link: the file it points to is replaced
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil { // the data is on disk before the name points at it
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil { // and the rename itself survives a crash
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// lockFile serialises two `pdx gate set` so neither loses the other's change.
func lockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

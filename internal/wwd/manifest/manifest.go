// Package manifest parses and validates a repo's task manifest,
// .wwd/tasks.toml (wwd spec §6), renders a task's command from validated
// arguments, and computes the task definition hash used as part of the
// result-reuse key (spec §4.5).
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	DefaultTimeout      = 30 * time.Minute
	DefaultQueueTimeout = 2 * time.Hour
)

// EntryMode is what the hook does with an undeclared test or build command.
type EntryMode string

const (
	EntryAdvise  EntryMode = "advise"
	EntryRewrite EntryMode = "rewrite"
	EntryDeny    EntryMode = "deny"
)

// ArgsKind says what the caller's arguments mean.
type ArgsKind string

const (
	ArgsFiles    ArgsKind = "files"
	ArgsPackages ArgsKind = "packages"
	ArgsNone     ArgsKind = "none"
	ArgsPass     ArgsKind = "pass" // local-only tasks: arguments appended as given
)

var summaryKinds = map[string]bool{"go-json": true, "vitest-json": true, "junit": true, "plain": true}

// Manifest is a parsed .wwd/tasks.toml.
type Manifest struct {
	Entry Entry
	Tasks map[string]*Task
}

type Entry struct {
	Mode EntryMode
}

// Qualify describes the qualification run of a task (spec §7).
type Qualify struct {
	Args      []string
	Reference string
}

// LocalPolicy is the manifest's `local` field: whether the task may take the
// local fast lane, and for how many arguments.
type LocalPolicy struct {
	Allowed bool
	// Max is the largest argument count allowed; negative means unlimited.
	Max int
	// Kind is the argument kind a count limit applies to ("" when unlimited).
	Kind ArgsKind
}

// Permits reports whether a call with n arguments may use the fast lane.
func (p LocalPolicy) Permits(n int) bool {
	if !p.Allowed {
		return false
	}
	return p.Max < 0 || n <= p.Max
}

// Task is one declared task.
type Task struct {
	Name          string
	Run           string
	Cwd           string
	Args          ArgsKind
	Local         LocalPolicy
	Class         wire.JobClass
	Labels        []string
	Locks         []string
	Deps          []string
	Cacheable     bool
	Interruptible bool
	Timeout       time.Duration
	QueueTimeout  time.Duration
	// Env entries are "NAME" (allow from the caller's environment) or
	// "NAME=value" (fixed value).
	Env     []string
	DB      string
	Summary string
	Qualify *Qualify
}

type rawManifest struct {
	Entry struct {
		Mode string `toml:"mode"`
	} `toml:"entry"`
	Tasks map[string]rawTask `toml:"tasks"`
}

type rawTask struct {
	Run           string   `toml:"run"`
	Cwd           string   `toml:"cwd"`
	Args          string   `toml:"args"`
	Local         any      `toml:"local"`
	Class         string   `toml:"class"`
	Labels        []string `toml:"labels"`
	Locks         []string `toml:"locks"`
	Deps          []string `toml:"deps"`
	Cacheable     bool     `toml:"cacheable"`
	Interruptible bool     `toml:"interruptible"`
	Timeout       string   `toml:"timeout"`
	QueueTimeout  string   `toml:"queue_timeout"`
	Env           []string `toml:"env"`
	DB            string   `toml:"db"`
	Summary       string   `toml:"summary"`
	Qualify       *struct {
		Args      []string `toml:"args"`
		Reference string   `toml:"reference"`
	} `toml:"qualify"`
}

var (
	taskNameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	envNameRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	placeholderRe = regexp.MustCompile(`\{([a-z]+)\}`)
	localCountRe  = regexp.MustCompile(`^(files|packages)<=([0-9]+)$`)
	pkgPatternRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./@+-]*$`)
)

// Load reads and parses a manifest file.
func Load(file string) (*Manifest, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse parses and validates manifest text. Unknown keys are errors.
func Parse(src []byte) (*Manifest, error) {
	var raw rawManifest
	md, err := toml.Decode(string(src), &raw)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		keys := make([]string, len(un))
		for i, k := range un {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("manifest: unknown keys: %s", strings.Join(keys, ", "))
	}
	m := &Manifest{Entry: Entry{Mode: EntryAdvise}, Tasks: map[string]*Task{}}
	switch EntryMode(raw.Entry.Mode) {
	case "":
	case EntryAdvise, EntryRewrite, EntryDeny:
		m.Entry.Mode = EntryMode(raw.Entry.Mode)
	default:
		return nil, fmt.Errorf("manifest: entry.mode %q is not advise, rewrite or deny", raw.Entry.Mode)
	}
	for name, rt := range raw.Tasks {
		t, err := buildTask(name, rt)
		if err != nil {
			return nil, fmt.Errorf("manifest: task %q: %w", name, err)
		}
		m.Tasks[name] = t
	}
	return m, nil
}

func buildTask(name string, rt rawTask) (*Task, error) {
	if !taskNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid task name")
	}
	t := &Task{
		Name: name, Run: strings.TrimSpace(rt.Run), Cwd: rt.Cwd, Args: ArgsKind(rt.Args),
		Class: wire.ClassDev, Labels: rt.Labels, Locks: rt.Locks, Deps: rt.Deps,
		Cacheable: rt.Cacheable, Interruptible: rt.Interruptible, Env: rt.Env, DB: rt.DB,
		Summary: rt.Summary, Timeout: DefaultTimeout, QueueTimeout: DefaultQueueTimeout,
	}
	if t.Run == "" {
		return nil, fmt.Errorf("run is empty")
	}
	switch t.Args {
	case ArgsFiles, ArgsPackages, ArgsNone, ArgsPass:
	default:
		return nil, fmt.Errorf("args %q is not files, packages, none or pass", rt.Args)
	}
	if t.Cwd != "" {
		if err := checkRelPath(t.Cwd); err != nil {
			return nil, fmt.Errorf("cwd: %w", err)
		}
	}
	if rt.Class != "" {
		t.Class = wire.JobClass(rt.Class)
		if !t.Class.Valid() {
			return nil, fmt.Errorf("class %q is not dev, merge or release", rt.Class)
		}
	}
	if t.Summary == "" {
		t.Summary = "plain"
	}
	if !summaryKinds[t.Summary] {
		return nil, fmt.Errorf("summary %q is not go-json, vitest-json, junit or plain", t.Summary)
	}
	var err error
	if t.Timeout, err = parseDuration(rt.Timeout, DefaultTimeout); err != nil {
		return nil, fmt.Errorf("timeout: %w", err)
	}
	if t.QueueTimeout, err = parseDuration(rt.QueueTimeout, DefaultQueueTimeout); err != nil {
		return nil, fmt.Errorf("queue_timeout: %w", err)
	}
	if t.Local, err = parseLocal(rt.Local); err != nil {
		return nil, err
	}
	if t.Local.Kind != "" && t.Local.Kind != t.Args {
		return nil, fmt.Errorf("local count limit is on %s but args is %s", t.Local.Kind, t.Args)
	}
	if t.Args == ArgsPass && !t.Local.Allowed {
		return nil, fmt.Errorf("args = \"pass\" is for local-only tasks and needs local = true")
	}
	if t.DB != "" && t.DB != "per-job" {
		return nil, fmt.Errorf("db %q is not per-job", t.DB)
	}
	if err := checkPlaceholders(t); err != nil {
		return nil, err
	}
	envSeen := map[string]bool{}
	for _, e := range t.Env {
		k, _, _ := strings.Cut(e, "=")
		if !envNameRe.MatchString(k) {
			return nil, fmt.Errorf("env entry %q has an invalid name", e)
		}
		if envSeen[k] {
			return nil, fmt.Errorf("env name %q appears more than once", k)
		}
		envSeen[k] = true
	}
	if err := checkUnique("locks", t.Locks); err != nil {
		return nil, err
	}
	if rt.Qualify != nil {
		t.Qualify = &Qualify{Args: rt.Qualify.Args, Reference: rt.Qualify.Reference}
		if t.Qualify.Reference == "" {
			return nil, fmt.Errorf("qualify needs a reference host")
		}
	}
	return t, nil
}

func parseDuration(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q must be positive", s)
	}
	return d, nil
}

func parseLocal(v any) (LocalPolicy, error) {
	switch x := v.(type) {
	case nil:
		return LocalPolicy{}, nil
	case bool:
		return LocalPolicy{Allowed: x, Max: -1}, nil
	case string:
		if mm := localCountRe.FindStringSubmatch(x); mm != nil {
			n, err := strconv.Atoi(mm[2])
			if err != nil {
				return LocalPolicy{}, fmt.Errorf("local %q: %w", x, err)
			}
			return LocalPolicy{Allowed: true, Max: n, Kind: ArgsKind(mm[1])}, nil
		}
	}
	return LocalPolicy{}, fmt.Errorf("local must be true, false, \"files<=N\" or \"packages<=N\"")
}

// checkPlaceholderContext rejects a placeholder that sits inside quotes or is
// glued to other text: Render inserts single-quoted values, which are only
// safe as a whole unquoted shell word (optionally after "--opt=").
func checkPlaceholderContext(run string) error {
	var quote byte
	for i := 0; i < len(run); i++ {
		c := run[i]
		switch {
		case quote == '"' && c == '\\':
			i++ // inside double quotes a backslash escapes the next byte
		case quote != 0:
			if c == quote {
				quote = 0
			} else if loc := placeholderRe.FindStringIndex(run[i:]); c == '{' && loc != nil && loc[0] == 0 {
				return fmt.Errorf("placeholder %s is inside quotes", run[i:i+loc[1]])
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '\\':
			i++
		case c == '{':
			loc := placeholderRe.FindStringIndex(run[i:])
			if loc == nil || loc[0] != 0 {
				continue
			}
			end := i + loc[1]
			if i > 0 && run[i-1] != ' ' && run[i-1] != '\t' && run[i-1] != '=' {
				return fmt.Errorf("placeholder %s must be a separate word", run[i:end])
			}
			if end < len(run) && run[end] != ' ' && run[end] != '\t' {
				return fmt.Errorf("placeholder %s must be a separate word", run[i:end])
			}
		}
	}
	if quote != 0 {
		return fmt.Errorf("run has an unterminated quote")
	}
	return nil
}

func checkPlaceholders(t *Task) error {
	if err := checkPlaceholderContext(t.Run); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, mm := range placeholderRe.FindAllStringSubmatch(t.Run, -1) {
		seen[mm[1]] = true
		switch mm[1] {
		case "files":
			if t.Args != ArgsFiles {
				return fmt.Errorf("{files} needs args = \"files\"")
			}
		case "packages":
			if t.Args != ArgsPackages {
				return fmt.Errorf("{packages} needs args = \"packages\"")
			}
		case "report", "junit":
		default:
			return fmt.Errorf("unknown placeholder {%s}", mm[1])
		}
	}
	if t.Args == ArgsFiles && !seen["files"] {
		return fmt.Errorf("args = \"files\" needs a {files} placeholder in run")
	}
	if t.Args == ArgsPackages && !seen["packages"] {
		return fmt.Errorf("args = \"packages\" needs a {packages} placeholder in run")
	}
	if t.Summary == "junit" && !seen["junit"] {
		return fmt.Errorf("summary = \"junit\" needs a {junit} placeholder in run")
	}
	if t.Summary == "vitest-json" && !seen["report"] {
		return fmt.Errorf("summary = \"vitest-json\" needs a {report} placeholder in run")
	}
	return nil
}

func checkUnique(what string, xs []string) error {
	seen := map[string]bool{}
	for _, x := range xs {
		if x == "" || seen[x] {
			return fmt.Errorf("%s has an empty or duplicate entry %q", what, x)
		}
		seen[x] = true
	}
	return nil
}

// checkRelPath rejects absolute paths, NUL bytes and any ".." element.
func checkRelPath(p string) error {
	if p == "" || strings.ContainsRune(p, 0) {
		return fmt.Errorf("empty path or NUL byte")
	}
	if path.IsAbs(p) || strings.HasPrefix(p, "-") {
		return fmt.Errorf("%q must be relative and not start with '-'", p)
	}
	for _, el := range strings.Split(p, "/") {
		if el == ".." {
			return fmt.Errorf("%q must not contain '..'", p)
		}
	}
	return nil
}

// RenderEnv carries what Render needs from the workspace.
type RenderEnv struct {
	// Report and Junit are the output file paths for {report} and {junit}.
	Report, Junit string
	// Workspace is the job's workspace root; Report and Junit must be
	// inside it (spec §6).
	Workspace string
	// Exists reports whether a repo-root-relative path exists in the
	// snapshot. Required when the task takes files.
	Exists func(repoRel string) bool
}

// Render validates args against the task's args kind and returns the shell
// command with placeholders replaced. Every substituted value is
// single-quoted.
func (t *Task) Render(args []string, env RenderEnv) (string, error) {
	var list string
	switch t.Args {
	case ArgsNone:
		if len(args) > 0 {
			return "", fmt.Errorf("task %s takes no arguments", t.Name)
		}
	case ArgsFiles:
		if len(args) == 0 {
			return "", fmt.Errorf("task %s needs at least one file", t.Name)
		}
		if env.Exists == nil {
			return "", fmt.Errorf("task %s: no snapshot to check files against", t.Name)
		}
		for _, a := range args {
			if err := checkRelPath(a); err != nil {
				return "", fmt.Errorf("file %q: %w", a, err)
			}
			if !env.Exists(path.Join(t.Cwd, a)) {
				return "", fmt.Errorf("file %q is not in the snapshot", a)
			}
		}
		list = quoteAll(args)
	case ArgsPackages:
		if len(args) == 0 {
			return "", fmt.Errorf("task %s needs at least one package", t.Name)
		}
		for _, a := range args {
			if err := checkPackage(a); err != nil {
				return "", err
			}
		}
		list = quoteAll(args)
	case ArgsPass:
		list = quoteAll(args)
	}
	for ph, p := range map[string]string{"{report}": env.Report, "{junit}": env.Junit} {
		if !strings.Contains(t.Run, ph) {
			continue
		}
		if err := checkInside(env.Workspace, p); err != nil {
			return "", fmt.Errorf("%s: %w", ph, err)
		}
	}
	out := placeholderRe.ReplaceAllStringFunc(t.Run, func(ph string) string {
		switch ph {
		case "{files}", "{packages}":
			return list
		case "{report}":
			return shellQuote(env.Report)
		case "{junit}":
			return shellQuote(env.Junit)
		}
		return ph
	})
	if t.Args == ArgsPass && list != "" {
		out += " " + list
	}
	return out, nil
}

// checkInside requires p to be a non-empty absolute path strictly inside root.
func checkInside(root, p string) error {
	if root == "" || p == "" || strings.ContainsRune(p, 0) || !filepath.IsAbs(root) || !filepath.IsAbs(p) {
		return fmt.Errorf("output path %q needs an absolute path and a workspace root", p)
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("output path %q is outside the workspace", p)
	}
	return nil
}

func checkPackage(p string) error {
	if !pkgPatternRe.MatchString(p) && !strings.HasPrefix(p, "./") {
		return fmt.Errorf("package %q is not a package pattern", p)
	}
	if p == "" || strings.HasPrefix(p, "-") {
		return fmt.Errorf("package %q is not a package pattern", p)
	}
	rest := strings.TrimPrefix(p, "./")
	if rest != "" && !pkgPatternRe.MatchString(rest) {
		return fmt.Errorf("package %q is not a package pattern", p)
	}
	for _, el := range strings.Split(p, "/") {
		if el == ".." {
			return fmt.Errorf("package %q must not contain '..'", p)
		}
	}
	return nil
}

func quoteAll(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = shellQuote(x)
	}
	return strings.Join(q, " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DefinitionHash is the hex SHA-256 of the parts of the definition that
// decide what a run means: run, cwd, env (order-independent), args kind and
// summary (spec §4.5). Timeouts, class and labels are not part of it.
func (t *Task) DefinitionHash() string {
	env := append([]string(nil), t.Env...)
	sort.Strings(env)
	b, _ := json.Marshal(struct {
		Run     string   `json:"run"`
		Cwd     string   `json:"cwd"`
		Env     []string `json:"env"`
		Args    ArgsKind `json:"args"`
		Summary string   `json:"summary"`
	}{t.Run, t.Cwd, env, t.Args, t.Summary})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

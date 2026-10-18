// Package entry is the part of the hook (wwd spec §8.2) that knows the repo's task manifest: it recognises a test or build command,
// finds the declared task whose command shape it is, and says what the hook should do with it. The hook keeps no copy of these
// rules; it asks `pdx entry match`.
package entry

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/wwd/manifest"
)

// ManifestPath is the manifest's place in a repository.
const ManifestPath = ".wwd/tasks.toml"

// Action is what the hook does with one command.
type Action string

const (
	// ActionNone leaves the command alone: it is not a test or build command, there is no manifest, or the manifest does not read.
	ActionNone Action = "none"
	// ActionRewrite runs the command as `pdx test <Task> <Args…>`.
	ActionRewrite Action = "rewrite"
	// ActionAdvise lets the command run as it is and says which task to use.
	ActionAdvise Action = "advise"
	// ActionDeny refuses the command and says which task to use.
	ActionDeny Action = "deny"
)

// Result is the answer for one command.
type Result struct {
	Action Action `json:"action"`
	// Task and Args are the task a rewrite runs and its arguments (relative to the task's cwd, as `pdx test` takes them).
	Task string   `json:"task,omitempty"`
	Args []string `json:"args,omitempty"`
	// Mode is the manifest's [entry].mode when no task matched ("advise" when the manifest names none).
	Mode string `json:"mode,omitempty"`
	// Hint is the task nearest to the command, for the advice and the refusal.
	Hint string `json:"hint,omitempty"`
	// Reason says why nothing is done (Action none), or why a mode became advice.
	Reason string `json:"reason,omitempty"`
}

// Match decides what to do with the command argv, run in cwd.
func Match(cwd string, argv []string) Result {
	canon := normalize(argv)
	if !isFamily(canon) {
		return Result{Action: ActionNone}
	}
	root, m, err := findManifest(cwd)
	if err != nil {
		return Result{Action: ActionNone, Reason: err.Error()}
	}
	names := make([]string, 0, len(m.Tasks))
	for n := range m.Tasks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if args, ok := matchTask(root, cwd, canon, m.Tasks[n]); ok {
			return Result{Action: ActionRewrite, Task: n, Args: args}
		}
	}
	mode := string(m.Entry.Mode)
	if mode == "" {
		mode = string(manifest.EntryAdvise)
	}
	r := Result{Mode: mode, Hint: hintFor(m, names, canon)}
	switch manifest.EntryMode(mode) {
	case manifest.EntryDeny:
		r.Action = ActionDeny
	case manifest.EntryRewrite:
		// An undeclared command would run as an ad-hoc job (spec §8.3); that entry is not there yet, so it is advised.
		r.Action, r.Reason = ActionAdvise, "mode rewrite: the ad-hoc entry is not available yet"
	default:
		r.Action = ActionAdvise
	}
	return r
}

// findManifest looks for the repo's manifest in cwd and the directories above it; the first one found is the repo's.
func findManifest(cwd string) (root string, m *manifest.Manifest, err error) {
	dir := filepath.Clean(cwd)
	for {
		file := filepath.Join(dir, filepath.FromSlash(ManifestPath))
		if st, serr := os.Stat(file); serr == nil && st.Mode().IsRegular() {
			m, err = manifest.Load(file)
			if err != nil {
				return "", nil, fmt.Errorf("manifest %s: %v", file, err)
			}
			return dir, m, nil
		}
		up := filepath.Dir(dir)
		if up == dir {
			return "", nil, fmt.Errorf("no manifest (%s) above %s", ManifestPath, cwd)
		}
		dir = up
	}
}

// ---- command shapes ----

var pmBins = map[string]bool{"vitest": true, "jest": true, "tsc": true, "phpunit": true, "pest": true}

// normalize is the command as its tool sees it: the runners in front (npx, pnpm exec, yarn, bunx, a path into node_modules/.bin or
// vendor/bin) are taken off, and `pnpm run build` is `pnpm build`. The manifest's words are put through the same, so both sides
// speak the same way.
func normalize(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	w := append([]string(nil), argv...)
	w[0] = binName(w[0])
	switch w[0] {
	case "npx", "bunx":
		w = w[1:]
		for len(w) > 0 && strings.HasPrefix(w[0], "-") && w[0] != "--" {
			w = w[1:]
		}
	case "pnpm", "yarn", "npm", "bun":
		switch {
		case len(w) > 1 && w[1] == "exec" && w[0] != "yarn":
			w = w[2:]
		case len(w) > 1 && pmBins[w[1]]:
			w = w[1:]
		case len(w) > 2 && w[1] == "run":
			w = append([]string{w[0]}, w[2:]...)
		}
	}
	if len(w) > 0 {
		w[0] = binName(w[0])
	}
	if len(w) == 0 {
		return nil
	}
	return w
}

// binName is the name of a program as written: the path in front is dropped for the places tools live (node_modules/.bin, vendor/bin).
func binName(p string) string {
	base := path.Base(filepath.ToSlash(p))
	if strings.Contains(p, "/") {
		slash := filepath.ToSlash(p)
		if strings.Contains(slash, "node_modules/.bin/") || strings.Contains(slash, "vendor/bin/") {
			return base
		}
	}
	return p
}

// isFamily says whether a normalized command is a test or build tool call (spec §8.2).
func isFamily(c []string) bool {
	if len(c) == 0 {
		return false
	}
	second := ""
	if len(c) > 1 {
		second = c[1]
	}
	switch c[0] {
	case "vitest", "jest", "tsc", "phpunit", "pest", "xcodebuild":
		return true
	case "go":
		return second == "test" || second == "build" || second == "vet"
	case "swift":
		return second == "build" || second == "test"
	case "pnpm", "npm", "yarn", "bun":
		return second == "build" || second == "test" || second == "check"
	case "make":
		return second == "lint" || second == "test"
	case "composer":
		return second == "test"
	}
	return false
}

// matchTask says whether the command is the task's shape, and with which arguments. The words the task has are walked in order: a
// literal word must be there; a flag the task has may be left out (or given as the task has it); anything the task does not
// have, a flag in particular, is not the task. What is left after the task's words is the arguments, which must be what the task
// takes.
func matchTask(root, cwd string, cmd []string, t *manifest.Task) ([]string, bool) {
	var lits []string
	for _, w := range t.Shape() {
		if w.Kind == manifest.ShapeLiteral {
			lits = append(lits, w.Text)
		}
	}
	tw := normalize(lits)
	i := 0
	for _, w := range tw {
		if i < len(cmd) && cmd[i] == w {
			i++
			continue
		}
		if strings.HasPrefix(w, "-") {
			if name, _, ok := strings.Cut(w, "="); ok && i < len(cmd) && strings.HasPrefix(cmd[i], name+"=") {
				return nil, false // the same flag with another value
			}
			continue // the task's own flag, left out
		}
		return nil, false
	}
	rest := cmd[i:]
	taskDir := filepath.Join(root, filepath.FromSlash(t.Cwd))
	if !within(taskDir, cwd) {
		return nil, false
	}
	switch t.Args {
	case manifest.ArgsNone:
		if len(rest) > 0 {
			return nil, false
		}
		return nil, true
	case manifest.ArgsPass:
		return append([]string(nil), rest...), true
	}
	if len(rest) == 0 {
		return nil, false
	}
	var out []string
	for _, a := range rest {
		if a == "" || strings.HasPrefix(a, "-") {
			return nil, false
		}
		conv, ok := convertArg(t.Args, taskDir, cwd, a)
		if !ok {
			return nil, false
		}
		out = append(out, conv)
	}
	return out, true
}

// convertArg puts an argument as typed in cwd into the form `pdx test` takes: a file relative to the task's directory that is in
// the working tree, a package pattern relative to the task's directory (a pattern that is not a path is kept as it is).
func convertArg(kind manifest.ArgsKind, taskDir, cwd, a string) (string, bool) {
	if kind == manifest.ArgsPackages && !strings.HasPrefix(a, ".") && !filepath.IsAbs(a) {
		return a, true
	}
	abs := a
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.Clean(abs)
	if !within(taskDir, abs) {
		return "", false
	}
	rel, err := filepath.Rel(taskDir, abs)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	switch kind {
	case manifest.ArgsFiles:
		if st, err := os.Stat(abs); err != nil || !st.Mode().IsRegular() {
			return "", false
		}
		return rel, true
	}
	// a package pattern: keep the "./" form, and a trailing "/..." or "/"
	pat := rel
	if strings.HasSuffix(a, "/...") || a == "./..." {
		pat = strings.TrimSuffix(strings.TrimSuffix(rel, "/..."), "...")
		pat = strings.TrimSuffix(pat, "/")
		if pat == "." || pat == "" {
			return "./...", true
		}
		return "./" + pat + "/...", true
	}
	if rel == "." {
		return ".", true
	}
	out := "./" + rel
	if strings.HasSuffix(a, "/") {
		out += "/"
	}
	return out, true
}

// within says whether p is dir or below it.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// hintFor is the task nearest to the command: the one whose literal words agree with the command's the longest from the front.
func hintFor(m *manifest.Manifest, names []string, cmd []string) string {
	best, bestN := "", 0
	for _, n := range names {
		var lits []string
		for _, w := range m.Tasks[n].Shape() {
			if w.Kind == manifest.ShapeLiteral {
				lits = append(lits, w.Text)
			}
		}
		tw := normalize(lits)
		k := 0
		for k < len(tw) && k < len(cmd) && tw[k] == cmd[k] {
			k++
		}
		if k > bestN {
			best, bestN = n, k
		}
	}
	return best
}

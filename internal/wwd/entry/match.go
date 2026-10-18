package entry

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/wwd/manifest"
)

// matchTask says whether the command is the task's shape, and with which arguments.
//
// The task's words are walked in order. Its plain words (`go test`, `vitest run`, a trailing `./...`) must be the command's. Each
// flag of the task comes with the value words after it (`-p 3`): the group is either left out of the command, or given as the task
// has it; the same flag with another value is another command. A caller does not type the worker's output files, so a flag that
// carries one is the task's alone. What is left after the task's words is the arguments, which must be what the task takes.
func matchTask(root, cwd string, cmd []string, t *manifest.Task) ([]string, bool) {
	var lits, owned []string
	shape := t.Shape()
	for k, w := range shape {
		switch {
		case w.Kind == manifest.ShapeLiteral:
			lits = append(lits, w.Text)
			if k+1 < len(shape) && shape[k+1].Kind == manifest.ShapeOutput && shape[k+1].Text == "" {
				owned = append(owned, w.Text) // `-junitfile {junit}`: the flag and the value are the worker's
			}
		case w.Kind == manifest.ShapeOutput && w.Text != "":
			owned = append(owned, w.Text) // `--outputFile={report}`
		}
	}
	for _, c := range cmd {
		for _, o := range owned {
			if c == o || (strings.HasSuffix(o, "=") && strings.HasPrefix(c, o)) || strings.HasPrefix(c, o+"=") {
				return nil, false
			}
		}
	}
	tw := normalize(lits)
	i, k := 0, 0
	for k < len(tw) {
		if !strings.HasPrefix(tw[k], "-") { // a plain word of the task (go test, a trailing ./...): the command has it too
			if i >= len(cmd) || cmd[i] != tw[k] {
				return nil, false
			}
			i, k = i+1, k+1
			continue
		}
		j := k + 1
		if !strings.Contains(tw[k], "=") { // a flag with its value in the next word: `-p 3`
			for j < len(tw) && valueLike(tw[j]) {
				j++
			}
		}
		group := tw[k:j]
		switch {
		case i+len(group) <= len(cmd) && equal(cmd[i:i+len(group)], group):
			i += len(group)
		case i < len(cmd) && flagName(cmd[i]) == flagName(group[0]):
			return nil, false // the same flag with another value
		}
		k = j
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

// valueLike says whether a word after a flag can be its value: not another flag, and not a relative path or pattern (those are the
// task's plain words, like the trailing ./... of `go test -timeout 30m ./...`).
func valueLike(w string) bool {
	return w != "" && !strings.HasPrefix(w, "-") && !strings.HasPrefix(w, ".")
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// flagName is a flag without a value joined to it.
func flagName(w string) string {
	name, _, _ := strings.Cut(w, "=")
	return name
}

// convertArg puts an argument as typed in cwd into the form `pdx test` takes: a file relative to the task's directory that is in
// the working tree and has no symbolic link on the way (a queued job runs on a snapshot, which holds the link and not its target),
// a package pattern relative to the task's directory (a pattern that is not a path is kept as it is).
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
		if !plainFile(taskDir, abs, rel) {
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

// plainFile says whether abs is a regular file that lies where its path says: resolving every link on the way ends at the same
// place below the task's directory.
func plainFile(taskDir, abs, rel string) bool {
	realDir, err := filepath.EvalSymlinks(taskDir)
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return false
	}
	if r, err := filepath.Rel(realDir, real); err != nil || filepath.ToSlash(r) != rel { // outside, or moved by a link on the way
		return false
	}
	st, err := os.Stat(real)
	return err == nil && st.Mode().IsRegular()
}

// within says whether p is dir or below it.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

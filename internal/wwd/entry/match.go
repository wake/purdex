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
			for j < len(tw) && valueLike(tw[j], j == len(tw)-1) {
				j++
			}
		}
		group := tw[k:j]
		switch {
		case i+len(group) <= len(cmd) && equal(cmd[i:i+len(group)], group):
			i += len(group)
		case i < len(cmd) && flagName(cmd[i]) == flagName(group[0]):
			return nil, false // the same flag with another value
		case selectsWhatRuns[flagName(group[0])]:
			return nil, false // left out: the command would run more than the task does, and as the task it would run less
		}
		k = j
	}
	rest := cmd[i:]
	taskDir := filepath.Join(root, filepath.FromSlash(t.Cwd))
	if !within(taskDir, cwd) || !noLinks(root, taskDir) {
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
		conv, ok := convertArg(t.Args, root, taskDir, cwd, a)
		if !ok {
			return nil, false
		}
		out = append(out, conv)
	}
	return out, true
}

// selectsWhatRuns are the flags that choose which tests a command runs. Unlike `-p 3` or `-timeout 20m`, which only say how, leaving one out
// of the command is not the same command: `go test -tags e2e ./cmd/wwd/` runs the whole package, and read as a task that has `-run TestE2E`
// it would silently run only that (the task's flags are all that run once the command is a task).
var selectsWhatRuns = map[string]bool{"-run": true, "-skip": true, "-tags": true, "-short": true, "-bench": true, "-fuzz": true}

// valueLike says whether a word after a flag can be its value: not another flag, and, as the very last word of the task, not a
// relative path or pattern (that is the task's own word, like the trailing ./... of `go test -timeout 30m ./...`; anywhere else a
// value that starts with a dot, `--config ./x.ts`, is a value).
func valueLike(w string, last bool) bool {
	return w != "" && !strings.HasPrefix(w, "-") && !(last && strings.HasPrefix(w, "."))
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
func convertArg(kind manifest.ArgsKind, root, taskDir, cwd, a string) (string, bool) {
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
		if !plainFile(root, abs) {
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

// plainFile says whether abs is a regular file with no symbolic link on the way from the repository root: a queued job runs on a
// snapshot, which holds a link and not what it points at.
func plainFile(root, abs string) bool {
	if !noLinks(root, abs) {
		return false
	}
	st, err := os.Lstat(abs)
	return err == nil && st.Mode().IsRegular()
}

// noLinks says whether every part of p below root (p itself included) exists and is not a symbolic link.
func noLinks(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	cur := root
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, seg)
		st, err := os.Lstat(cur)
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

// within says whether p is dir or below it.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

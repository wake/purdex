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
	cmd = dropRunFlags(normalizeGoFlags(cmd))
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
		// a task that only ever runs here (go-pkg) is for packages by name: a pattern that expands (./internal/...) is not one package, and
		// run here it would be the suite of a tree on a light slot. It is the queued tasks' (go-changed, go-all).
		if t.Args == manifest.ArgsPackages && Recursive(a) && t.Local.Allowed && t.Local.Max < 0 {
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

// Recursive says whether a package argument is a pattern that expands to many packages (`./internal/...`, `...`).
func Recursive(a string) bool { return strings.Contains(a, "...") }

// wholeTree says whether a package argument is the whole tree of the repository.
func wholeTree(a string) bool { return a == "./..." || a == "..." }

// Flags of `go test` that only say how to run, never what: a command that has one the task does not write still is the task. They are
// dropped from the command before it is compared, and the task's own command runs (its values of -p and -timeout replace the typed ones;
// to keep your own, `pdx test --adhoc`). -race, -run, -skip, -tags, -short, -bench, -fuzz, -cover… change what runs or what it means: they
// stay in the command, and make it another one.
var (
	runFlagsAlone = map[string]bool{"-v": true, "-json": true, "-failfast": true}
	runFlagsValue = map[string]bool{"-count": true, "-p": true, "-parallel": true, "-timeout": true}
)

// normalizeGoFlags writes a `go test` command's `--flag` as `-flag`: Go reads them as the same flag, and every check here goes by the single
// dash name. A lone `--` ends the flags, and nothing from `-args` or `--` on is a flag of go (it is the test program's, or an argument).
func normalizeGoFlags(cmd []string) []string {
	if len(cmd) < 2 || cmd[0] != "go" || cmd[1] != "test" {
		return cmd
	}
	out := append([]string(nil), cmd[:2]...)
	for i := 2; i < len(cmd); i++ {
		w := cmd[i]
		if w == "-args" || w == "--" {
			return append(out, cmd[i:]...)
		}
		if strings.HasPrefix(w, "--") && len(w) > 2 && w[2] != '-' {
			w = w[1:]
		}
		out = append(out, w)
	}
	return out
}

// goValueFlags are the flags of `go test` (and the build flags it takes) that take a value, in the next word or joined with =: the word after
// one is its value whatever it looks like (`-run ./...` is a regexp).
var goValueFlags = map[string]bool{
	"-run": true, "-skip": true, "-tags": true, "-count": true, "-p": true, "-parallel": true, "-timeout": true, "-bench": true,
	"-benchtime": true, "-fuzz": true, "-fuzztime": true, "-cpu": true, "-coverprofile": true, "-covermode": true, "-coverpkg": true,
	"-o": true, "-exec": true, "-ldflags": true, "-gcflags": true, "-asmflags": true, "-gccgoflags": true, "-mod": true, "-modfile": true,
	"-overlay": true, "-pkgdir": true, "-vet": true, "-outputdir": true, "-blockprofile": true, "-cpuprofile": true, "-memprofile": true,
	"-mutexprofile": true, "-trace": true, "-shuffle": true, "-testlogfile": true, "-fuzzcachedir": true, "-fuzzminimizetime": true,
}

// dropRunFlags takes the flags above out of a `go test` command, also when the task writes the flag itself with its own value (go-all has
// -p 3 and -timeout 30m: `go test -p 4 ./...` is go-all, which runs its own). A value joined with = is one word; a flag that takes a value in
// the next word takes it.
func dropRunFlags(cmd []string) []string {
	if len(cmd) < 2 || cmd[0] != "go" || cmd[1] != "test" {
		return cmd
	}
	out := append([]string(nil), cmd[:2]...)
	for i := 2; i < len(cmd); i++ {
		name := flagName(cmd[i])
		switch {
		case cmd[i] == "-args" || cmd[i] == "--":
			return append(out, cmd[i:]...)
		case !(runFlagsAlone[name] || runFlagsValue[name]):
			out = append(out, cmd[i])
		case runFlagsValue[name] && !strings.Contains(cmd[i], "=") && i+1 < len(cmd) && !strings.HasPrefix(cmd[i+1], "-"):
			i++ // its value is the next word
		}
	}
	return out
}

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

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
	cmd = normalizeGoFlags(cmd) // before anything reads a flag: --junitfile is -junitfile
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
	for _, c := range beforeGoArgs(cmd) {
		for _, o := range owned {
			if c == o || (strings.HasSuffix(o, "=") && strings.HasPrefix(c, o)) || strings.HasPrefix(c, o+"=") {
				return nil, false
			}
		}
	}
	tw := normalize(lits)
	cmd = dropRunFlags(cmd)
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
		case !mayBeLeftOut(flagName(group[0]), tw, owned):
			return nil, false // left out: the command would run more than the task does (or otherwise than it), and as the task it would not
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

// mayBeLeftOut says whether a flag of the task's command may be missing from the typed command and the typed command still be the task. tw is
// the task's own words (its tool is the first one).
//
// A flag the task has and the user did not type changes what runs, unless it is known not to: `pnpm check` is not `pnpm check --fast
// --affected origin/main`, and `go test -tags e2e ./cmd/wwd/` is not the task that has `-run TestE2E` (as the task it would silently run only
// that). So the rule is the one way round: only a flag that says how to run, and not what, may be left out; any other flag of the task, a
// filter, a mode, a narrowing, is the command's to type, and without it the command is another one (it runs as typed, or as a task without
// the flag). A list of the flags that narrow was open the other way: every narrowing flag nobody listed was left out silently (#3814).
//
// What a flag means is the tool's: `-p` is the parallelism of `go test` and a package of cargo, `--run` is vitest's single run and a filter
// elsewhere. So the flags that say how are the known ones of the known tools (go test, vitest), and a task of any other tool has none: all its
// flags are the command's to type. The report file the worker writes (`{report}`, `{junit}`) is any tool's: a caller does not type it.
func mayBeLeftOut(name string, tw, owned []string) bool {
	for _, o := range owned {
		if flagName(o) == name {
			return true
		}
	}
	switch {
	case len(tw) >= 2 && tw[0] == "go" && tw[1] == "test":
		return runFlagsAlone[name] || runFlagsValue[name]
	case len(tw) >= 1 && tw[0] == "vitest":
		return vitestHowFlags[name]
	}
	return false
}

// vitestHowFlags are the flags of vitest that only say how to run or how to report: the reporter, the number of workers, the single run of a
// watcher (`vitest related --run`), quiet output. The output file is the worker's own (also in the task's owned flags).
var vitestHowFlags = map[string]bool{"--reporter": true, "--outputFile": true, "--maxWorkers": true, "--run": true, "--silent": true}

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

// normalizeGoFlags writes a `go test` command's `--flag` as `-flag`, and `-test.flag` as `-flag`: Go reads them as the same flag, and every
// check here goes by the single dash name. A lone `--` ends the flags, and nothing from `-args` or `--` on is a flag of go (it is the test program's, or an argument).
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
		// -test.run is -run: the test flags of `go help testflag` take the optional test. prefix. Nothing else does: -test.p is no -p, it is an
		// unknown flag for the test program, and must stay what it is (a command that fails is not one to turn into a task)
		if rest, ok := strings.CutPrefix(w, "-test."); ok && goTestFlags[flagName("-"+rest)] {
			w = "-" + rest
		}
		out = append(out, w)
	}
	return out
}

// goTestFlags are the flags that `go help testflag` lists, the ones that may be written with the test. prefix (-test.run for -run). Build
// flags such as -p, -tags and -race are not among them.
var goTestFlags = map[string]bool{
	"-benchmem": true, "-benchtime": true, "-bench": true, "-blockprofile": true, "-blockprofilerate": true, "-count": true,
	"-coverprofile": true, "-cpu": true, "-cpuprofile": true, "-failfast": true, "-fullpath": true, "-fuzz": true, "-fuzzminimizetime": true,
	"-fuzztime": true, "-json": true, "-list": true, "-memprofile": true, "-memprofilerate": true, "-mutexprofile": true,
	"-mutexprofilefraction": true, "-outputdir": true, "-parallel": true, "-run": true, "-short": true, "-shuffle": true, "-skip": true,
	"-testlogfile": true, "-timeout": true, "-trace": true, "-v": true,
}

// beforeGoArgs is the part of a `go test` command that is go's own flags and packages: what follows -args or a lone -- is for the test
// program (or an argument), and is not read as a flag of go, nor as the worker's output flag. Another command is returned whole.
func beforeGoArgs(cmd []string) []string {
	if len(cmd) < 2 || cmd[0] != "go" || cmd[1] != "test" {
		return cmd
	}
	for i := 2; i < len(cmd); i++ {
		if cmd[i] == "-args" || cmd[i] == "--" {
			return cmd[:i]
		}
	}
	return cmd
}

// goValueFlags are the flags of `go test` (and the build flags it takes) that take a value, in the next word or joined with =: the word after
// one is its value whatever it looks like (`-run ./...` is a regexp).
var goValueFlags = map[string]bool{
	"-run": true, "-skip": true, "-tags": true, "-count": true, "-p": true, "-parallel": true, "-timeout": true, "-bench": true,
	"-benchtime": true, "-fuzz": true, "-fuzztime": true, "-cpu": true, "-coverprofile": true, "-covermode": true, "-coverpkg": true,
	"-o": true, "-exec": true, "-ldflags": true, "-gcflags": true, "-asmflags": true, "-gccgoflags": true, "-mod": true, "-modfile": true,
	"-overlay": true, "-pkgdir": true, "-vet": true, "-outputdir": true, "-blockprofile": true, "-cpuprofile": true, "-memprofile": true,
	"-list": true, "-blockprofilerate": true, "-memprofilerate": true, "-mutexprofilefraction": true, "-run-timeout": true, "-testify.m": true,
	"-C": true, "-buildmode": true, "-buildvcs": true, "-compiler": true, "-installsuffix": true, "-pgo": true, "-toolexec": true,
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

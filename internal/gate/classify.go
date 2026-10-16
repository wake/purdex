package gate

import (
	"path/filepath"
	"strings"
)

// Class is what a command costs (spec §3): the pool it takes a slot from, or none.
type Class string

const (
	ClassNone  Class = "none"
	ClassLight Class = "light"
	ClassHeavy Class = "heavy"
)

// Valid says whether c names a pool.
func (c Class) Valid() bool { return c == ClassLight || c == ClassHeavy }

// goValueFlags are the go flags whose value is the next word; the ones that are not here are switches, so the word
// after them counts as a package.
var goValueFlags = map[string]bool{
	"-run": true, "-bench": true, "-count": true, "-timeout": true, "-tags": true, "-p": true, "-parallel": true,
	"-cpu": true, "-coverprofile": true, "-o": true, "-vet": true, "-covermode": true, "-coverpkg": true,
	"-skip": true, "-fuzz": true, "-exec": true, "-ldflags": true, "-gcflags": true, "-mod": true, "-modfile": true,
	"-overlay": true, "-pkgdir": true, "-blockprofile": true, "-cpuprofile": true, "-memprofile": true, "-trace": true,
	"-outputdir": true, "-shuffle": true, "-fuzztime": true, "-test.run": true, "-asmflags": true,
	"-C": true, "-list": true, "-vettool": true, "-toolexec": true, "-buildmode": true, "-compiler": true,
	"-installsuffix": true,
}

// vitestValueFlags are the vitest flags whose value is the next word.
var vitestValueFlags = map[string]bool{
	"-t": true, "--testNamePattern": true, "--project": true, "--maxWorkers": true, "--max-workers": true,
	"--reporter": true, "--config": true, "-c": true, "--root": true, "-r": true, "--dir": true, "--pool": true,
	"--shard": true, "--outputFile": true, "--environment": true, "--mode": true, "-m": true,
	"--coverage.reporter": true, "--coverage.provider": true, "--minWorkers": true, "--min-workers": true,
	"--retry": true, "--bail": true, "--testTimeout": true, "--hookTimeout": true, "--exclude": true,
	"--cache": true, "--poolOptions.threads.maxThreads": true, "--poolOptions.forks.maxForks": true,
	"--poolOptions.threads.minThreads": true, "--poolOptions.forks.minForks": true, "--browser.name": true,
}

// runnerValueOpts are the options of a package runner (pnpm, yarn, npm) whose value is the next word: the word after
// them is not the subcommand or the tool.
var runnerValueOpts = map[string]bool{
	"-C": true, "--dir": true, "--prefix": true, "--filter": true, "-F": true, "--workspace": true, "-w": true,
	"--package": true, "-p": true, "--cwd": true,
}

// scripts a package runner is taken to run in full: a script by one of these names is a whole-project run.
var heavyScripts = map[string]bool{
	"check": true, "test": true, "typecheck": true, "lint": true,
}

// packagingBases are the names that say a script packages or builds an output: build, package, pack, dist, bundle, and each of them
// with a suffix after a colon (build:mac, package:win:dev), and electron:build. Such a script is never held back (user 10-15),
// whichever way it is asked for (also `pnpm -r build`): it is none, not heavy, and not light either. It is put under a slot
// late, when it has run long (#2996). A script of an unknown name is not one: a dev server must not take a slot for ever.
var packagingBases = map[string]bool{"build": true, "package": true, "pack": true, "dist": true, "bundle": true}

// isPackagingScript says whether the script name is a packaging one.
func isPackagingScript(name string) bool {
	if name == "electron:build" || packagingBases[name] {
		return true
	}
	base, _, suffixed := strings.Cut(name, ":")
	return suffixed && packagingBases[base]
}

// Reason is why a command got its class (ClassifyReason): the two pools, and for a command the gate lets through the kind of
// letting. The load watcher reads it to tell a command that should have been gated from one that is allowed.
type Reason string

const (
	ReasonHeavy            Reason = "heavy"             // a whole run: takes a heavy slot
	ReasonLight            Reason = "light"             // a narrowed run: takes a light slot
	ReasonPackaging        Reason = "packaging"         // a build that writes its output: never held back (user 10-15)
	ReasonWatchMode        Reason = "watch_mode"        // a tool that watches and never ends (vitest watch, a dev server)
	ReasonKnownPassthrough Reason = "known_passthrough" // a known tool's command the gate lets through without judging its cost (go version, pnpm install, composer install…); a label only
	ReasonUnknown          Reason = "unknown"           // a tool or script the table does not know, or a command line it cannot read
)

// ClassifyReason is Classify with the reason for the class. The class is exactly Classify's.
func ClassifyReason(argv []string, e Env) (Class, Reason) {
	switch c := Classify(argv, e); c {
	case ClassHeavy:
		return c, ReasonHeavy
	case ClassLight:
		return c, ReasonLight
	default:
		return c, noneReason(argv, e)
	}
}

// Classify says how heavy a command is, from its argv (argv[0] may be a path). It is a pure function over the words
// the shims hand over: go, pnpm/npx/yarn/npm, vitest, tsc, make, composer and the PHP test runners phpunit and pest. Anything else is none and passes through. A
// command that cannot be read is none: the gate fails open on what it does not understand.
func Classify(argv []string, e Env) Class {
	if len(argv) == 0 {
		return ClassNone
	}
	args := argv[1:]
	switch filepath.Base(argv[0]) {
	case "go":
		return classifyGo(args)
	case "vitest":
		return classifyVitest(args, e)
	case "tsc":
		return classifyTsc(args)
	case "pnpm", "pnpx", "npx", "yarn", "npm", "bunx", "bun":
		return classifyRunner(filepath.Base(argv[0]), args, e)
	case "make":
		if len(args) == 1 && args[0] == "test" {
			return ClassHeavy
		}
	case "composer":
		return classifyComposer(args, e)
	case "phpunit", "pest":
		return classifyPhpunit(args)
	}
	return ClassNone
}

// goPackages reads the go flags and gives the package words, whether a compile-only (-c) was asked, and whether a
// pattern ends in "...". Words after -args belong to the test binary.
func goPackages(args []string) (pkgs []string, compileOnly bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-args" || a == "--args" {
			break
		}
		if strings.HasPrefix(a, "-") {
			name := "-" + strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			} else if goValueFlags[name] {
				i++
			}
			if name == "-c" {
				compileOnly = true
			}
			continue
		}
		pkgs = append(pkgs, a)
	}
	return pkgs, compileOnly
}

// goOutputFlag says whether the go flags name an output file or directory (-o x, -o=x): the command packages.
func goOutputFlag(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-args" || a == "--args" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := "-" + strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name = name[:eq]
		} else if goValueFlags[name] {
			if name == "-o" {
				return true
			}
			i++
			continue
		}
		if name == "-o" {
			return true
		}
	}
	return false
}

func wide(pkgs []string) bool {
	if len(pkgs) > 1 {
		return true
	}
	for _, p := range pkgs {
		if p == "..." || strings.HasSuffix(p, "/...") {
			return true
		}
	}
	return false
}

func classifyGo(args []string) Class {
	// `go -C dir test …` and `go -C=dir test …`: skip the directory flag to find the subcommand.
	for len(args) >= 1 && (args[0] == "-C" || strings.HasPrefix(args[0], "-C=")) {
		if args[0] == "-C" {
			if len(args) < 2 {
				return ClassNone
			}
			args = args[2:]
		} else {
			args = args[1:]
		}
	}
	if len(args) == 0 {
		return ClassNone
	}
	sub, rest := args[0], args[1:]
	pkgs, _ := goPackages(rest)
	switch sub {
	case "build":
		// Packaging is never held back: a build that writes its output (-o) and a build of one target are none. Only a build
		// of several packages or of the whole module (./...), the compile check that writes nothing, takes a slot.
		if goOutputFlag(rest) || (len(pkgs) <= 1 && !wide(pkgs)) {
			return ClassNone
		}
		return ClassHeavy
	case "test", "vet":
		if wide(pkgs) {
			return ClassHeavy
		}
		return ClassLight
	case "install":
		return ClassLight
	}
	return ClassNone // version, env, mod, list, fmt, run, tool, generate…
}

// classifyVitest reads vitest's words. Vitest watches (and never ends) only when told to (`watch`, `dev`, `--watch`,
// `-w`) or, with nothing said, for a person at a terminal outside CI; in an agent's shell, a script or CI it runs once. A
// run that names files or a filter is light, the rest is heavy. `--version`, `--help`, `list` and `init` run nothing.
func classifyVitest(args []string, e Env) Class {
	c, _ := vitestVerdict(args, e)
	return c
}

// vitestVerdict is classifyVitest with the reason of a none: watch_mode when vitest would watch (never ends), known_passthrough
// for the words that run no test.
func vitestVerdict(args []string, e Env) (Class, Reason) {
	sub := ""        // the first word that is a subcommand
	explicit := 0    // +1 run once (--no-watch, --watch=false), -1 watch (--watch, -w); the last one wins
	runFlag := false // --run / --run=true: vitest sets watch to false after everything else, whatever else was said
	narrowed := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--version" || a == "-v" || a == "--help" || a == "-h":
			return ClassNone, ReasonKnownPassthrough
		case a == "-w":
			explicit = -1
		case a == "--run" || a == "--run=true":
			runFlag = true
		case vitestBoolFlag(a) != 0:
			explicit = vitestBoolFlag(a)
		case a == "--changed" || a == "--related" || strings.HasPrefix(a, "--changed=") ||
			a == "-t" || a == "--testNamePattern" || a == "--project" || a == "--shard":
			narrowed = true
			if (a == "-t" || a == "--testNamePattern" || a == "--project" || a == "--shard") && i+1 < len(args) {
				i++
			}
		case strings.HasPrefix(a, "-"):
			name := a
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			} else if vitestValueFlags[name] {
				i++
			}
		case sub == "" && !narrowed && vitestSubcommands[a]:
			sub = a
		default:
			narrowed = true // a path or a filter
		}
	}
	switch sub {
	case "list", "init", "bench":
		return ClassNone, ReasonKnownPassthrough
	case "related":
		narrowed = true // `vitest related <files>`: the tests those files touch
	}
	// --run wins over `watch`, `dev` and --watch (vitest sets watch to false when options.run is true, after the
	// subcommand has set it), so it comes first; then a watch that was asked for; then what the place decides.
	once := runFlag || explicit > 0
	if sub == "run" {
		once = explicit >= 0 // vitest's run action sets options.run = !options.watch: `run --watch` watches, and --run cannot undo it
	}
	if !once && (sub == "watch" || sub == "dev" || explicit < 0) {
		return ClassNone, ReasonWatchMode
	}
	if !once && e.watchesByDefault() {
		return ClassNone, ReasonWatchMode // a person at a terminal: the default is watch
	}
	if narrowed {
		return ClassLight, ReasonLight
	}
	return ClassHeavy, ReasonHeavy
}

// vitestBoolFlag reads --watch with an optional =true / =false, and a bare --no-watch: +1 when the flag makes vitest run
// once, -1 when it makes it watch, 0 when it is some other flag.
func vitestBoolFlag(a string) int {
	name, val, hasVal := strings.Cut(a, "=")
	truth := !hasVal || val == "true"
	falsy := hasVal && val == "false"
	switch name {
	case "--watch":
		if truth {
			return -1
		}
		if falsy {
			return 1
		}
	case "--no-watch":
		// cac does not split `--no-*` at an `=`: `--no-watch=false` is an unknown option that changes nothing
		if !hasVal {
			return 1
		}
	}
	return 0
}

var vitestSubcommands = map[string]bool{"run": true, "watch": true, "dev": true, "related": true, "list": true, "init": true, "bench": true}

func classifyTsc(args []string) Class {
	build, project, files := false, false, false
	for _, a := range args {
		switch {
		case a == "-b" || a == "--build":
			build = true
		case a == "-p" || a == "--project" || strings.HasPrefix(a, "--project="):
			project = true
		case a == "--version" || a == "-v" || a == "--help" || a == "-h" || a == "--init" || a == "--showConfig":
			return ClassNone
		case !strings.HasPrefix(a, "-") && (strings.HasSuffix(a, ".ts") || strings.HasSuffix(a, ".tsx")):
			files = true
		}
	}
	switch {
	case build:
		return ClassHeavy // -b builds the project references: every project of the graph
	case project, files:
		return ClassLight // -p names one project's tsconfig (its own files only), or the files are named
	}
	return ClassHeavy // a bare tsc compiles the whole project
}

// classifyRunner reads `pnpm`, `npx`, `yarn`, `npm`: a script by a whole-project name is heavy, a recursive run is
// heavy, and a tool run through them is classified as that tool.
func classifyRunner(runner string, args []string, e Env) Class {
	recursive := false
	i := 0
	// options before the subcommand
	for ; i < len(args); i++ {
		a := args[i]
		if a == "-r" || a == "--recursive" {
			recursive = true
			continue
		}
		if !strings.HasPrefix(a, "-") {
			break
		}
		if runnerValueOpts[a] {
			i++ // the option's value
		}
	}
	if i >= len(args) {
		return ClassNone
	}
	word, rest := args[i], args[i+1:]
	if runner == "yarn" && word == "workspaces" && len(rest) > 0 && rest[0] == "foreach" {
		return yarnForeach(rest[1:])
	}
	switch word {
	case "install", "i", "add", "remove", "rm", "update", "up", "link", "unlink", "store", "config", "env", "dlx", "create",
		"init", "outdated", "list", "ls", "why", "audit", "publish", "pack", "root", "bin", "cache", "version", "help":
		return ClassNone
	case "exec", "x":
		if len(rest) == 0 {
			return ClassNone
		}
		return withRecursive(Classify(rest, e), recursive)
	case "run", "run-script":
		if len(rest) == 0 {
			return ClassNone
		}
		return scriptClass(rest[0], recursive)
	}
	if runner == "npx" || runner == "pnpx" || runner == "bunx" {
		return withRecursive(Classify(args[i:], e), recursive)
	}
	if isPackagingScript(word) {
		return ClassNone
	}
	if heavyScripts[word] {
		return ClassHeavy
	}
	// `pnpm vitest …`, `pnpm tsc -b`: the word is a tool in node_modules/.bin
	if c := Classify(args[i:], e); c != ClassNone {
		return withRecursive(c, recursive)
	}
	if recursive {
		return ClassHeavy
	}
	return ClassNone
}

// yarnForeach reads `yarn workspaces foreach <options> [run] <script|tool …>`: a run over the workspaces is heavy when
// it runs a whole-project script, or when it is asked to go through all of them (-A / --all / -R / --recursive).
func yarnForeach(args []string) Class {
	all := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-A" || a == "--all" || a == "-R" || a == "--recursive" || a == "-W" || a == "--worktree":
			all = true
		case a == "-p" || a == "--parallel" || a == "-i" || a == "--interlaced" || a == "-v" || a == "--verbose" || a == "-t" || a == "--topological" || a == "--topological-dev":
		case a == "--include" || a == "--exclude" || a == "-j" || a == "--jobs" || a == "--since":
			i++
		case strings.HasPrefix(a, "-"):
		case a == "run":
			if i+1 < len(args) && !isPackagingScript(args[i+1]) && (heavyScripts[args[i+1]] || all) {
				return ClassHeavy
			}
			return ClassNone
		default:
			if !isPackagingScript(a) && (heavyScripts[a] || all) {
				return ClassHeavy
			}
			return ClassNone
		}
	}
	return ClassNone
}

func scriptClass(name string, recursive bool) Class {
	if isPackagingScript(name) {
		return ClassNone
	}
	if heavyScripts[name] || recursive {
		return ClassHeavy
	}
	return ClassNone
}

func withRecursive(c Class, recursive bool) Class {
	if recursive && c != ClassNone {
		return ClassHeavy
	}
	return c
}

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
//
// The list is `go help testflag` and `go help build` read to the end (#3073: -benchtime was missing, so its value counted as a second
// package). A flag may also be written with the test binary's prefix, -test.benchtime: goFlagName strips it.
var goValueFlags = map[string]bool{
	// go build / go vet / go test (go help build)
	"-C": true, "-p": true, "-asmflags": true, "-buildmode": true, "-compiler": true, "-gccgoflags": true, "-gcflags": true,
	"-installsuffix": true, "-ldflags": true, "-mod": true, "-modfile": true, "-overlay": true, "-pgo": true, "-pkgdir": true,
	"-tags": true, "-toolexec": true, "-o": true, "-covermode": true, "-coverpkg": true, "-exec": true, "-vettool": true,
	// go test (go help testflag, and the flags the go command passes on to the test binary)
	"-bench": true, "-benchtime": true, "-blockprofile": true, "-blockprofilerate": true, "-count": true, "-coverprofile": true,
	"-cpu": true, "-cpuprofile": true, "-fuzz": true, "-fuzzcachedir": true, "-fuzzminimizetime": true, "-fuzztime": true,
	"-list": true, "-memprofile": true, "-memprofilerate": true, "-mutexprofile": true, "-mutexprofilefraction": true,
	"-outputdir": true, "-parallel": true, "-run": true, "-shuffle": true, "-skip": true, "-testlogfile": true, "-timeout": true,
	"-trace": true, "-vet": true,
}

// goFlagName is the name of a go flag word with one leading dash and without the test binary's prefix: "--benchtime=1x" and
// "-test.benchtime" are both "-benchtime".
func goFlagName(a string) string {
	name := "-" + strings.TrimLeft(a, "-")
	if eq := strings.IndexByte(name, '='); eq >= 0 {
		name = name[:eq]
	}
	return "-" + strings.TrimPrefix(strings.TrimPrefix(name, "-"), "test.")
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
	ReasonVitestRelated    Reason = "vitest_related"    // vitest related: the tests those files touch, which for a widely imported file is most of the suite (heavy)
	ReasonPackaging        Reason = "packaging"         // a build that writes its output: never held back (user 10-15)
	ReasonInfoOnly         Reason = "info_only"         // --help, -h, --version or the help subcommand: the tool prints a text and runs nothing (#3043)
	ReasonWatchMode        Reason = "watch_mode"        // a tool that watches and never ends (vitest watch, a dev server)
	ReasonKnownPassthrough Reason = "known_passthrough" // a known tool's command the gate lets through without judging its cost (go version, pnpm install, composer install…); a label only
	ReasonUnknown          Reason = "unknown"           // a tool or script the table does not know, or a command line it cannot read
)

// ClassifyReason is Classify with the reason for the class. The class is exactly Classify's.
func ClassifyReason(argv []string, e Env) (Class, Reason) {
	switch c := Classify(argv, e); c {
	case ClassHeavy:
		return c, heavyReason(argv, e)
	case ClassLight:
		return c, ReasonLight
	default:
		return c, noneReason(argv, e)
	}
}

// heavyReason is vitest_related for a `vitest related` that was made heavy because of what it follows, heavy for the rest.
func heavyReason(argv []string, e Env) Reason {
	if len(argv) == 0 {
		return ReasonHeavy
	}
	rest := argv[1:]
	switch filepath.Base(argv[0]) {
	case "vitest":
	case "pnpm", "pnpx", "npx", "yarn", "npm", "bunx", "bun":
		// the tool the runner runs: the first word that is not an option, after an optional exec / dlx
		i := 0
		for i < len(rest) && strings.HasPrefix(rest[i], "-") {
			i++
		}
		if i < len(rest) && (rest[i] == "exec" || rest[i] == "x" || rest[i] == "dlx") {
			i++
			for i < len(rest) && strings.HasPrefix(rest[i], "-") {
				i++
			}
		}
		if i >= len(rest) || filepath.Base(rest[i]) != "vitest" {
			return ReasonHeavy
		}
		rest = rest[i+1:]
	default:
		return ReasonHeavy
	}
	if _, r := vitestVerdict(rest, e); r == ReasonVitestRelated {
		return r
	}
	return ReasonHeavy
}

// infoTools are the tools whose own help or version a command can ask for.
var infoTools = map[string]bool{
	"go": true, "vitest": true, "tsc": true, "make": true, "composer": true, "phpunit": true, "pest": true, "claude": true,
	"vite": true, "electron-vite": true, "electron-builder": true, "swift": true, "swift-build": true, "swift-test": true,
}

// infoRunners are the package runners: their own help comes before the subcommand, and what follows a script's name is the script's.
var infoRunners = map[string]bool{"pnpm": true, "pnpx": true, "npx": true, "yarn": true, "npm": true, "bunx": true, "bun": true}

func isHelpWord(a string) bool { return a == "--help" || a == "-h" || a == "--version" }

// helpAsked reads the words a tool is given (after its own name): `--help`, `-h` or `--version` is a request for its help when it is not
// something else. A word after `--` or `-args` belongs to what the tool runs (`go test -args -h` runs the test binary). A help word right
// after an option that has no `=` may be that option's value (`go test -bench --help ./...` runs every test and benchmark; `phpunit
// --coverage-html --help` writes there): the tools take their values in many ways and the tables of them are never complete, so it is
// left to the gate to judge, and the command is classified as it was. `help` as the first word is the help subcommand of go and composer.
func helpAsked(tool string, words []string) bool {
	if len(words) > 0 && words[0] == "help" && (tool == "go" || tool == "composer") {
		return true
	}
	afterOption := false
	for _, a := range words {
		switch {
		case a == "--" || a == "-args" || a == "--args":
			return false
		case isHelpWord(a) && !afterOption:
			return true
		}
		afterOption = strings.HasPrefix(a, "-") && !strings.Contains(a, "=")
	}
	return false
}

// infoOnly says whether the command only asks a tool for its help or version (#3043): such a command runs nothing worth a slot, whatever
// the rest of the line says. For the package runners the help has to be the runner's own, or the tool it runs (`npx vitest --help`,
// `pnpm exec vitest --help`): a help word after the name of a script is passed to the script, which runs (`pnpm run build --help` builds).
func infoOnly(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	name := filepath.Base(argv[0])
	switch {
	case infoTools[name]:
		return helpAsked(name, argv[1:])
	case infoRunners[name]:
		return runnerInfoOnly(name, argv[1:])
	}
	return false
}

func runnerInfoOnly(name string, words []string) bool {
	i := 0
	for i < len(words) && strings.HasPrefix(words[i], "-") { // the runner's own options come first
		if isHelpWord(words[i]) {
			return true
		}
		if runnerValueOpts[words[i]] {
			i++
		}
		i++
	}
	if i >= len(words) {
		return false
	}
	toolAt := func(j int) bool { // words[j] is the tool the runner runs
		if j >= len(words) || !infoTools[filepath.Base(words[j])] {
			return false
		}
		return helpAsked(filepath.Base(words[j]), words[j+1:])
	}
	switch w := words[i]; {
	case w == "help":
		return name != "npx" && name != "pnpx" && name != "bunx"
	case name == "npx" || name == "pnpx" || name == "bunx":
		return toolAt(i)
	case w == "exec" || w == "x" || w == "dlx":
		j := i + 1
		for j < len(words) && strings.HasPrefix(words[j], "-") {
			if runnerValueOpts[words[j]] {
				j++
			}
			j++
		}
		return toolAt(j)
	case w == "run" || w == "run-script":
		// only a help word before the script's name is the runner's
		for _, a := range words[i+1:] {
			if !strings.HasPrefix(a, "-") {
				return false
			}
			if isHelpWord(a) {
				return true
			}
		}
	}
	return false
}

// Classify says how heavy a command is, from its argv (argv[0] may be a path). It is a pure function over the words
// the shims hand over: go, pnpm/npx/yarn/npm, vitest, tsc, make, composer and the PHP test runners phpunit and pest. Anything else is none and passes through. A
// command that cannot be read is none: the gate fails open on what it does not understand.
func Classify(argv []string, e Env) Class {
	if len(argv) == 0 {
		return ClassNone
	}
	if infoOnly(argv) {
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
		// The targets that run a whole-module go command: `make test` is go test ./..., `make lint` is gofmt and go vet ./... (#3089). Heavy from
		// the outside, so the go vet it starts passes as a nested command; held light, the outer run kept the light slot while the inner
		// one waited in the heavy queue, and every other light command waited behind it.
		if len(args) == 1 && (args[0] == "test" || args[0] == "lint") {
			return ClassHeavy
		}
	case "composer":
		return classifyComposer(args, e)
	case "phpunit", "pest":
		return classifyPhpunit(args)
	case "claude":
		return classifyClaude(args)
	case "xcodebuild":
		return classifyXcodebuild(args)
	case "swift":
		return classifySwift(args)
	case "swift-build", "swift-test":
		return classifySwiftDriver(args)
	}
	return ClassNone
}

// classifyClaude reads `claude plugin test`: the mods' test run starts a Claude Code engine and a test runner per file, so a
// whole run (no --file) is heavy and a run narrowed to files (--file, once or repeated) is light. No other claude command is
// held: the agent itself must never be. The gate sees it only through the mod's prefix (claude is not shimmed).
func classifyClaude(args []string) Class {
	if len(args) < 2 || args[0] != "plugin" || args[1] != "test" {
		return ClassNone
	}
	for _, a := range args[2:] {
		if a == "--" {
			break
		}
		if a == "--file" || strings.HasPrefix(a, "--file=") {
			return ClassLight
		}
	}
	return ClassHeavy
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
			name := goFlagName(a)
			if !strings.Contains(a, "=") && goValueFlags[name] {
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
		name := goFlagName(a)
		if !strings.Contains(a, "=") && goValueFlags[name] {
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

// maxLightPackages is how many packages a go test or go vet may name and still be a light run. Measured by the watcher (10-16, #3073):
// a run of up to two packages, alone or beside a vitest related, shows no more pressure than the related alone; one more package, or a
// pattern that ends in "...", is a whole-module run.
const maxLightPackages = 2

// ellipsis says whether a package word is a pattern over a whole tree ("...", "./...", "pkg/...").
func ellipsis(pkgs []string) bool {
	for _, p := range pkgs {
		if p == "..." || strings.HasSuffix(p, "/...") {
			return true
		}
	}
	return false
}

// wide says a go test or go vet names more than it runs light: a tree pattern or more than maxLightPackages packages.
func wide(pkgs []string) bool { return len(pkgs) > maxLightPackages || ellipsis(pkgs) }

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
		if goOutputFlag(rest) || (len(pkgs) <= 1 && !ellipsis(pkgs)) {
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
	related := false // the tests that import the named files, found through the import graph (#3024)
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
		case a == "--related":
			narrowed, related = true, true
		case a == "--changed" || strings.HasPrefix(a, "--changed=") ||
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
		related = true
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
	if related {
		// How much it runs depends on where the files sit in the import graph, not on how many are named: for a file many others
		// import it is close to the whole suite, which took 13 minutes on average on 10-16 beside two other vitests in the light
		// pool. One full-width run at a time is shorter than several that share the cores (D7).
		return ClassHeavy, ReasonVitestRelated
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

// tscWatches says whether tsc is told to watch (-w, --watch) and so never ends. tsc reads a boolean flag's next word when it is
// `true` or `false`, so `--watch false` runs once.
func tscWatches(args []string) bool {
	watch := false
	for i, a := range args {
		if a = strings.ToLower(a); a != "-w" && a != "--watch" { // tsc lowercases option names and boolean values
			continue
		}
		watch = true
		if i+1 < len(args) && strings.ToLower(args[i+1]) == "false" {
			watch = false
		}
	}
	return watch
}

func classifyTsc(args []string) Class {
	if tscWatches(args) {
		return ClassNone // watches and never ends: a slot would be held for good (#2989)
	}
	build, files := false, false
	for _, a := range args {
		switch {
		case a == "-b" || a == "--build":
			build = true
		case a == "--version" || a == "-v" || a == "--help" || a == "-h" || a == "--init" || a == "--showConfig":
			return ClassNone
		case !strings.HasPrefix(a, "-") && (strings.HasSuffix(a, ".ts") || strings.HasSuffix(a, ".tsx")):
			files = true
		}
	}
	switch {
	case build:
		return ClassHeavy // -b builds the project references: every project of the graph
	case files:
		return ClassLight // the files are named
	}
	// A bare tsc and `tsc -p <project>` compile a whole project, and from the words alone its size is not to be told: the SPA's
	// tsconfig.app.json is 1.24 GB of node, as much as tsc -b (the watcher, 10-17), so it is heavy. A project that is small is said to be
	// by a [[gate.intercept.rule]] (`tsc --noEmit -p tsconfig.node.json` -> light). (#3073; #2985 had made -p light.)
	return ClassHeavy
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

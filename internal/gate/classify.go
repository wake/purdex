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
	"check": true, "test": true, "typecheck": true, "lint": true, "build": true, "electron:build": true,
}

// Classify says how heavy a command is, from its argv (argv[0] may be a path). It is a pure function over the words
// the shims hand over: go, pnpm/npx/yarn/npm, vitest, tsc and make. Anything else is none and passes through. A
// command that cannot be read is none: the gate fails open on what it does not understand.
func Classify(argv []string) Class {
	if len(argv) == 0 {
		return ClassNone
	}
	args := argv[1:]
	switch filepath.Base(argv[0]) {
	case "go":
		return classifyGo(args)
	case "vitest":
		return classifyVitest(args)
	case "tsc":
		return classifyTsc(args)
	case "pnpm", "pnpx", "npx", "yarn", "npm", "bunx", "bun":
		return classifyRunner(filepath.Base(argv[0]), args)
	case "make":
		if len(args) == 1 && args[0] == "test" {
			return ClassHeavy
		}
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
	case "test", "vet", "build":
		if wide(pkgs) {
			return ClassHeavy
		}
		return ClassLight
	case "install":
		return ClassLight
	}
	return ClassNone // version, env, mod, list, fmt, run, tool, generate…
}

// vitestRun reads vitest's words: whether it is a run (or the default command), and whether a file or filter narrows
// it. `vitest watch` and `--version` are not runs the gate holds a slot for.
func classifyVitest(args []string) Class {
	run := false
	narrowed := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--version" || a == "-v" || a == "--help" || a == "-h":
			return ClassNone
		case a == "run":
			run = true
		case a == "related":
			run, narrowed = true, true // `vitest related <files>` runs once, on what those files touch
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
		default:
			narrowed = true // a path or a filter
		}
	}
	if !run {
		return ClassNone // watch mode or a bare `vitest`: long-lived, not held
	}
	if narrowed {
		return ClassLight
	}
	return ClassHeavy
}

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
	case build || project:
		return ClassHeavy
	case files:
		return ClassLight
	}
	return ClassHeavy // a bare tsc compiles the whole project
}

// classifyRunner reads `pnpm`, `npx`, `yarn`, `npm`: a script by a whole-project name is heavy, a recursive run is
// heavy, and a tool run through them is classified as that tool.
func classifyRunner(runner string, args []string) Class {
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
		return withRecursive(Classify(rest), recursive)
	case "run", "run-script":
		if len(rest) == 0 {
			return ClassNone
		}
		return scriptClass(rest[0], recursive)
	}
	if runner == "npx" || runner == "pnpx" || runner == "bunx" {
		return withRecursive(Classify(args[i:]), recursive)
	}
	if heavyScripts[word] {
		return ClassHeavy
	}
	// `pnpm vitest …`, `pnpm tsc -b`: the word is a tool in node_modules/.bin
	if c := Classify(args[i:]); c != ClassNone {
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
			if i+1 < len(args) && (heavyScripts[args[i+1]] || all) {
				return ClassHeavy
			}
			return ClassNone
		default:
			if heavyScripts[a] || all {
				return ClassHeavy
			}
			return ClassNone
		}
	}
	return ClassNone
}

func scriptClass(name string, recursive bool) Class {
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

package gate

import (
	"path/filepath"
	"strings"
)

// noneReason is why Classify said none for argv. It reads the same words Classify read; it is called only for a none.
func noneReason(argv []string, e Env) Reason {
	if len(argv) == 0 {
		return ReasonUnknown
	}
	args := argv[1:]
	switch name := filepath.Base(argv[0]); name {
	case "go":
		return goNoneReason(args)
	case "vitest":
		_, r := vitestVerdict(args, e)
		return r
	case "tsc", "phpunit", "pest":
		return ReasonKnownPassthrough // --version, --help, --init, --list-tests…: nothing is run
	case "make":
		// a target is the project's own: only the ones every Makefile means the same by are known to cost little
		switch i := firstWord(args); {
		case i >= 0 && args[i] == "build":
			return ReasonPackaging
		case i >= 0 && makeCheapTargets[args[i]]:
			return ReasonKnownPassthrough
		}
		return ReasonUnknown // `make` alone, `make lint`, `make integration`…
	case "composer":
		return composerNoneReason(args, e)
	case "pnpm", "pnpx", "npx", "yarn", "npm", "bunx", "bun":
		return runnerNoneReason(name, args, e)
	case "vite", "electron-vite":
		for _, a := range args {
			if a == "--version" || a == "-v" || a == "--help" || a == "-h" {
				return ReasonKnownPassthrough
			}
		}
		i := firstWord(args)
		switch {
		case i >= 0 && args[i] == "build":
			return ReasonPackaging
		case i < 0 || args[i] == "dev" || args[i] == "serve" || args[i] == "preview":
			return ReasonWatchMode // a server that runs until it is stopped (a bare `vite` is the dev server)
		}
		return ReasonUnknown // `vite optimize` and whatever comes next: not a word the table knows
	case "electron-builder":
		return ReasonPackaging
	}
	return ReasonUnknown
}

func goNoneReason(args []string) Reason {
	for len(args) >= 1 && (args[0] == "-C" || strings.HasPrefix(args[0], "-C=")) {
		if args[0] == "-C" {
			if len(args) < 2 {
				return ReasonUnknown // a line that cannot be read
			}
			args = args[2:]
		} else {
			args = args[1:]
		}
	}
	if len(args) == 0 {
		return ReasonKnownPassthrough // a bare `go` prints its usage
	}
	switch args[0] {
	case "build":
		return ReasonPackaging
	case "run", "generate", "tool":
		return ReasonUnknown // they run a program the table cannot judge
	}
	return ReasonKnownPassthrough // version, env, mod, list, fmt, …
}

func composerNoneReason(args []string, e Env) Reason {
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "-d" || a == "--working-dir" || a == "-f" || a == "--file" {
			i++
			continue
		}
		if strings.HasSuffix(a, ".json") && !strings.HasPrefix(a, "-") {
			continue
		}
		if !strings.HasPrefix(a, "-") {
			break
		}
	}
	if i >= len(args) {
		return ReasonKnownPassthrough
	}
	word, rest := args[i], args[i+1:]
	switch word {
	case "build":
		return ReasonPackaging
	case "run", "run-script", "run-scripts":
		j := firstWord(rest)
		switch {
		case j < 0:
			return ReasonKnownPassthrough // lists the scripts
		case rest[j] == "build":
			return ReasonPackaging
		}
		return ReasonUnknown // a script of the project's own
	case "exec":
		j := firstWord(rest)
		if j < 0 {
			return ReasonKnownPassthrough
		}
		tail := rest[j+1:]
		if len(tail) > 0 && tail[0] == "--" {
			tail = tail[1:]
		}
		return noneReason(append([]string{rest[j]}, tail...), e)
	}
	// composer runs a script of the project's by its bare name (`composer deploy`): only composer's own commands are known
	if composerBuiltins[word] {
		return ReasonKnownPassthrough // install, update, require, dump-autoload, validate, show, …
	}
	return ReasonUnknown
}

// makeCheapTargets are the make targets that do next to nothing in any project.
var makeCheapTargets = map[string]bool{"clean": true, "help": true, "version": true, "fmt": true, "format": true}

// composerBuiltins are composer's own commands that cost little (the ones that run a test script or a script by name are
// classified before this: test*, run, run-script, exec).
var composerBuiltins = map[string]bool{
	"about": true, "archive": true, "audit": true, "browse": true, "home": true, "bump": true, "check-platform-reqs": true,
	"clear-cache": true, "clearcache": true, "cc": true, "completion": true, "config": true, "create-project": true,
	"depends": true, "why": true, "diagnose": true, "dump-autoload": true, "dumpautoload": true, "fund": true,
	"global": true, "help": true, "init": true, "install": true, "i": true, "licenses": true, "list": true,
	"outdated": true, "prohibits": true, "why-not": true, "reinstall": true, "remove": true, "rm": true, "uninstall": true,
	"require": true, "r": true, "search": true, "self-update": true, "selfupdate": true, "show": true, "info": true,
	"status": true, "suggests": true, "update": true, "u": true, "upgrade": true, "validate": true,
}

// runnerNoneReason follows classifyRunner for a none: a packaging script, a package-manager command, or the tool or script it
// runs.
func runnerNoneReason(runner string, args []string, e Env) Reason {
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "-r" || a == "--recursive" {
			continue
		}
		if !strings.HasPrefix(a, "-") {
			break
		}
		if runnerValueOpts[a] {
			i++
		}
	}
	if i >= len(args) {
		return ReasonKnownPassthrough
	}
	word, rest := args[i], args[i+1:]
	if runner == "yarn" && word == "workspaces" && len(rest) > 0 && rest[0] == "foreach" {
		return yarnForeachNoneReason(rest[1:])
	}
	switch word {
	case "install", "i", "add", "remove", "rm", "update", "up", "link", "unlink", "store", "config", "env", "dlx", "create",
		"init", "outdated", "list", "ls", "why", "audit", "publish", "pack", "root", "bin", "cache", "version", "help":
		return ReasonKnownPassthrough
	case "exec", "x":
		if len(rest) == 0 {
			return ReasonKnownPassthrough
		}
		return noneReason(rest, e)
	case "run", "run-script":
		if len(rest) == 0 {
			return ReasonKnownPassthrough
		}
		if isPackagingScript(rest[0]) {
			return ReasonPackaging
		}
		return ReasonUnknown // a script of the project's own that the table does not call heavy
	}
	if runner == "npx" || runner == "pnpx" || runner == "bunx" {
		return noneReason(args[i:], e)
	}
	if isPackagingScript(word) {
		return ReasonPackaging
	}
	if r := noneReason(args[i:], e); r != ReasonUnknown {
		return r // `pnpm vitest --watch`, `pnpm tsc --version`: the tool's own reason
	}
	return ReasonUnknown // `pnpm <script>` the table does not know
}

func yarnForeachNoneReason(args []string) Reason {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--include" || a == "--exclude" || a == "-j" || a == "--jobs" || a == "--since":
			i++
		case strings.HasPrefix(a, "-"):
		case a == "run":
			if i+1 < len(args) && isPackagingScript(args[i+1]) {
				return ReasonPackaging
			}
			return ReasonUnknown
		default:
			if isPackagingScript(a) {
				return ReasonPackaging
			}
			return ReasonUnknown
		}
	}
	return ReasonUnknown
}

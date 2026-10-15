package gate

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The parallelism a gated command is held to (spec §5, module 3). The real load is slots × per-command parallelism, so
// each command gets a cap of its own when it did not give one; a limit the command (or its environment) already
// carries is the person's and is left alone. The numbers come from ParallelismConfig.Resolve.

// GoFlagsEnv returns env with `-p=<p>` added to GOFLAGS when GOFLAGS carries no -p. A -p on the command line wins
// over GOFLAGS, so this never overrides a command that gave its own. GOMAXPROCS is left alone.
func GoFlagsEnv(env []string, p int) []string {
	if p <= 0 {
		return env
	}
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GOFLAGS=") {
			out = append(out, kv)
			continue
		}
		found = true
		cur := strings.TrimPrefix(kv, "GOFLAGS=")
		if hasGoP(cur) {
			out = append(out, kv)
			continue
		}
		if strings.TrimSpace(cur) == "" {
			out = append(out, "GOFLAGS=-p="+strconv.Itoa(p))
		} else {
			out = append(out, "GOFLAGS="+cur+" -p="+strconv.Itoa(p))
		}
	}
	if !found {
		out = append(out, "GOFLAGS=-p="+strconv.Itoa(p))
	}
	return out
}

func hasGoP(flags string) bool {
	for _, w := range strings.Fields(flags) {
		w = strings.TrimLeft(w, "-")
		if w == "p" || strings.HasPrefix(w, "p=") {
			return true
		}
	}
	return false
}

var vitestLimit = regexp.MustCompile(`^--(maxWorkers|max-workers|poolOptions\..*(maxThreads|maxForks))(=|$)`)

// VitestArgs appends `--maxWorkers=<n>` to a vitest run (directly, or through npx / pnpm exec / pnpm vitest /
// yarn) that carries no worker limit. It reports whether it changed anything. Arguments after vitest's own `--` are the
// tests' or the script's, so the flag goes in front of them. A run that is not vitest is returned as it was.
func VitestArgs(argv []string, n int) ([]string, bool) {
	start := vitestAt(argv)
	if n <= 0 || start < 0 {
		return argv, false
	}
	at := len(argv)
	for i := start + 1; i < len(argv); i++ {
		if argv[i] == "--" {
			at = i // what follows belongs to the script or the tests: not a limit of vitest's, and the flag goes before it
			break
		}
	}
	for _, a := range argv[start:at] {
		if vitestLimit.MatchString(a) {
			return argv, false
		}
	}
	out := make([]string, 0, len(argv)+1)
	out = append(out, argv[:at]...)
	out = append(out, "--maxWorkers="+strconv.Itoa(n))
	out = append(out, argv[at:]...)
	return out, true
}

// vitestAt is the index of the word that is vitest in the command (`vitest …` itself, or the tool a runner names), or -1.
// A runner's own `--` before the tool (`npx --package vitest -- vitest run`) is not vitest's.
func vitestAt(argv []string) int {
	if len(argv) == 0 {
		return -1
	}
	if filepath.Base(argv[0]) == "vitest" {
		return 0
	}
	switch filepath.Base(argv[0]) {
	case "npx", "pnpx", "bunx", "pnpm", "yarn", "npm":
	default:
		return -1
	}
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "exec" || a == "x" || a == "dlx" || a == "run":
			continue
		case runnerValueOpts[a]:
			i++ // `--prefix spa`: the value is not the tool
			continue
		case strings.HasPrefix(a, "-"):
			continue
		}
		if filepath.Base(a) == "vitest" {
			return i
		}
		return -1
	}
	return -1
}

// PnpmArgs adds `--workspace-concurrency=<n>` right after `pnpm` for a recursive run that gives none.
func PnpmArgs(argv []string, n int) ([]string, bool) {
	if n <= 0 || len(argv) == 0 || filepath.Base(argv[0]) != "pnpm" {
		return argv, false
	}
	recursive := false
	for _, a := range argv[1:] {
		if a == "--" {
			break
		}
		if a == "-r" || a == "--recursive" {
			recursive = true
		}
		if a == "--workspace-concurrency" || strings.HasPrefix(a, "--workspace-concurrency=") {
			return argv, false
		}
	}
	if !recursive {
		return argv, false
	}
	out := make([]string, 0, len(argv)+1)
	out = append(out, argv[0], "--workspace-concurrency="+strconv.Itoa(n))
	out = append(out, argv[1:]...)
	return out, true
}

// Capped gives argv and env their caps (go's -p, vitest's workers, pnpm's workspace concurrency) and says what it put
// in, for the one line the gate prints. A zero number is no cap (the parallelism module is off).
func Capped(argv, env []string, goP, vitestWorkers, pnpmWorkspace int) (outArgv, outEnv []string, added []string) {
	outArgv, outEnv = argv, env
	if a, ok := VitestArgs(outArgv, vitestWorkers); ok {
		outArgv = a
		added = append(added, "--maxWorkers="+strconv.Itoa(vitestWorkers))
	}
	if a, ok := PnpmArgs(outArgv, pnpmWorkspace); ok {
		outArgv = a
		added = append(added, "--workspace-concurrency="+strconv.Itoa(pnpmWorkspace))
	}
	if e := GoFlagsEnv(outEnv, goP); !sameEnv(e, outEnv) {
		outEnv = e // every gated command gets it: a script under the gate may run go anywhere
		if len(argv) > 0 && filepath.Base(argv[0]) == "go" {
			added = append(added, "GOFLAGS -p="+strconv.Itoa(goP))
		}
	}
	return outArgv, outEnv, added
}

func sameEnv(a, b []string) bool {
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

package gate

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The parallelism a gated command is held to (spec §5). The real load is slots × per-command parallelism, so each
// command gets a cap of its own when it did not give one; a limit the command (or its environment) already carries
// is the person's and is left alone.

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
// yarn) that carries no worker limit. It reports whether it changed anything. Arguments after `--` or `-args` are
// the script's, so the flag goes in front of them. A run that is not vitest is returned as it was.
func VitestArgs(argv []string, n int) ([]string, bool) {
	if n <= 0 || !runsVitest(argv) {
		return argv, false
	}
	for _, a := range argv {
		if vitestLimit.MatchString(a) {
			return argv, false
		}
	}
	at := len(argv)
	for i, a := range argv {
		if a == "--" {
			at = i
			break
		}
	}
	out := make([]string, 0, len(argv)+1)
	out = append(out, argv[:at]...)
	out = append(out, "--maxWorkers="+strconv.Itoa(n))
	out = append(out, argv[at:]...)
	return out, true
}

// runsVitest says the command is vitest itself: `vitest …`, or a runner naming vitest as the tool.
func runsVitest(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	if filepath.Base(argv[0]) == "vitest" {
		return true
	}
	switch filepath.Base(argv[0]) {
	case "npx", "pnpx", "bunx", "pnpm", "yarn", "npm":
	default:
		return false
	}
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			return false
		case a == "exec" || a == "x" || a == "dlx" || a == "run":
			continue
		case strings.HasPrefix(a, "-"):
			continue
		}
		return a == "vitest"
	}
	return false
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

// Capped gives argv and env their caps for the class and says what it put in, for the one line the gate prints.
func Capped(argv, env []string, c Config) (outArgv, outEnv []string, added []string) {
	outArgv, outEnv = argv, env
	if a, ok := VitestArgs(outArgv, c.VitestWorkers); ok {
		outArgv = a
		added = append(added, "--maxWorkers="+strconv.Itoa(c.VitestWorkers))
	}
	if a, ok := PnpmArgs(outArgv, c.PnpmConcurrency); ok {
		outArgv = a
		added = append(added, "--workspace-concurrency="+strconv.Itoa(c.PnpmConcurrency))
	}
	if e := GoFlagsEnv(outEnv, c.GoP); !sameEnv(e, outEnv) {
		outEnv = e // every gated command gets it: a script under the gate may run go anywhere
		if len(argv) > 0 && filepath.Base(argv[0]) == "go" {
			added = append(added, "GOFLAGS -p="+strconv.Itoa(c.GoP))
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

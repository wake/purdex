package entry

import (
	"path"
	"path/filepath"
	"strings"
)

var pmBins = map[string]bool{"vitest": true, "jest": true, "tsc": true, "phpunit": true, "pest": true}

// Options of the runners that take a value in the next word, and the ones that hand the rest to a shell. Anything else that starts
// with a dash is taken to be on its own (a value joined with = is one word anyway).
var (
	npxValueOpts = map[string]bool{"-p": true, "--package": true, "-w": true, "--workspace": true, "--prefix": true, "--cache": true,
		"--registry": true, "--userconfig": true, "--loglevel": true}
	npxShellOpts     = map[string]bool{"-c": true, "--call": true}
	pnpmExecValueOpt = map[string]bool{"--filter": true, "-F": true, "--dir": true, "-C": true}
	pnpmExecShellOpt = map[string]bool{"-c": true, "--shell-mode": true}
)

// dropOptions takes a runner's own options off the front of w, `--` included, and gives what is left. ok is false when an option
// hands the rest to a shell: that is not a command to read.
func dropOptions(w []string, valueOpts, shellOpts map[string]bool) (rest []string, ok bool) {
	for len(w) > 0 && strings.HasPrefix(w[0], "-") {
		switch {
		case w[0] == "--":
			return w[1:], true
		case shellOpts[w[0]]:
			return nil, false
		case valueOpts[w[0]]:
			w = w[min(2, len(w)):]
		default:
			w = w[1:]
		}
	}
	return w, true
}

// toTool is what a runner runs, once its options are off. An option this package does not know that takes a value leaves the value
// where the program should be; when what is there is not a test or build command but one is further along, the command starts
// there (a runner is never a way round the entry).
func toTool(w []string) []string {
	if isFamily(normalizeBin(w)) {
		return w
	}
	for p := 1; p < len(w); p++ {
		if isFamily(normalizeBin(w[p:])) {
			return w[p:]
		}
	}
	return w
}

// normalizeBin is w with a tool path in front of it reduced to the tool's name.
func normalizeBin(w []string) []string {
	if len(w) == 0 {
		return nil
	}
	out := append([]string(nil), w...)
	out[0] = binName(out[0])
	return out
}

// normalize is the command as its tool sees it: the runners in front (npx, pnpm exec, yarn, bunx, a path into node_modules/.bin or
// vendor/bin) are taken off, and `pnpm run build` is `pnpm build`. The manifest's words are put through the same, so both sides
// speak the same way.
func normalize(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	w := append([]string(nil), argv...)
	w[0] = binName(w[0])
	switch w[0] {
	case "npx", "bunx":
		rest, ok := dropOptions(w[1:], npxValueOpts, npxShellOpts)
		if !ok {
			return w
		}
		w = toTool(rest)
	case "pnpm", "yarn", "npm", "bun":
		// the package manager's own options come before its subcommand (`pnpm --filter spa exec vitest`)
		if rest, ok := dropOptions(w[1:], pnpmExecValueOpt, nil); ok {
			w = append([]string{w[0]}, rest...)
		}
		switch {
		case len(w) > 1 && w[1] == "exec" && w[0] != "yarn":
			rest, ok := dropOptions(w[2:], pnpmExecValueOpt, pnpmExecShellOpt)
			if !ok {
				return w
			}
			w = toTool(rest)
		case len(w) > 1 && pmBins[w[1]]:
			w = w[1:]
		case len(w) > 2 && w[1] == "run":
			w = append([]string{w[0]}, w[2:]...)
		}
	}
	if len(w) > 0 {
		w[0] = binName(w[0])
	}
	if len(w) == 0 {
		return nil
	}
	return w
}

// binName is the name of a program as written: the path in front is dropped for the places tools live (node_modules/.bin, vendor/bin).
func binName(p string) string {
	if strings.Contains(p, "/") {
		slash := filepath.ToSlash(p)
		if strings.Contains(slash, "node_modules/.bin/") || strings.Contains(slash, "vendor/bin/") {
			return path.Base(slash)
		}
	}
	return p
}

// isFamily says whether a normalized command is a test or build tool call (spec §8.2).
func isFamily(c []string) bool {
	if len(c) == 0 {
		return false
	}
	second := ""
	if len(c) > 1 {
		second = c[1]
	}
	switch c[0] {
	case "vitest", "jest", "tsc", "phpunit", "pest", "xcodebuild":
		return true
	case "go":
		return second == "test" || second == "build" || second == "vet"
	case "swift":
		return second == "build" || second == "test"
	case "pnpm", "npm", "yarn", "bun":
		return second == "build" || second == "test" || second == "check"
	case "make":
		return second == "lint" || second == "test"
	case "composer":
		return second == "test"
	}
	return false
}

package entry

import (
	"path"
	"path/filepath"
	"strings"
)

var pmBins = map[string]bool{"vitest": true, "jest": true, "tsc": true, "phpunit": true, "pest": true}

// Options of the runners that take a value in the next word, and the ones that hand the rest to a shell. An option not listed may
// or may not take a value (a value joined with = is one word anyway): both readings are tried, see optionTails.
var (
	npxValueOpts = map[string]bool{"-p": true, "--package": true, "-w": true, "--workspace": true, "--prefix": true, "--cache": true,
		"--registry": true, "--userconfig": true, "--loglevel": true}
	npxShellOpts = map[string]bool{"-c": true, "--call": true}
	// pmValueOpts are the package managers' own options that take a value, before or after exec (pnpm, npm, yarn and bun).
	pmValueOpts = map[string]bool{"--filter": true, "-F": true, "--dir": true, "-C": true, "--cwd": true, "--prefix": true,
		"-w": true, "--workspace": true, "--workspace-root": true, "--registry": true, "--userconfig": true}
	pmShellOpts = map[string]bool{"-c": true, "--shell-mode": true}
)

// optionTails are the readings of what follows a runner's options: every way of taking the options off the front of w. A listed
// option that takes a value takes it; `--` ends the options; an option that hands the rest to a shell gives no reading (it is not a
// command to read); an option that is not listed is read both ways, on its own and with the next word as its value, so a runner
// can never hide the tool behind an option this package does not know.
func optionTails(w []string, valueOpts, shellOpts map[string]bool) [][]string {
	if len(w) == 0 || !strings.HasPrefix(w[0], "-") {
		return [][]string{w}
	}
	switch {
	case w[0] == "--":
		return [][]string{w[1:]}
	case shellOpts[w[0]]:
		return nil
	case valueOpts[w[0]]:
		return optionTails(w[min(2, len(w)):], valueOpts, shellOpts)
	}
	out := optionTails(w[1:], valueOpts, shellOpts)
	if len(w) > 1 && !strings.HasPrefix(w[1], "-") {
		out = append(out, optionTails(w[2:], valueOpts, shellOpts)...)
	}
	return out
}

// firstFamily is the first reading that, put through shape, is a test or build command; with none, the first reading as shape makes
// it (nil when there is no reading).
func firstFamily(tails [][]string, shape func([]string) []string) []string {
	for _, t := range tails {
		if c := shape(t); isFamily(c) {
			return c
		}
	}
	if len(tails) == 0 {
		return nil
	}
	return shape(tails[0])
}

// toolShape is a reading of what a runner runs: the tool, with a path in front of it reduced to its name.
func toolShape(w []string) []string {
	if len(w) == 0 {
		return nil
	}
	out := append([]string(nil), w...)
	out[0] = binName(out[0])
	return out
}

// pmSubcommand is a package manager's command once its own options are off: `exec` runs a tool (its options off too), a tool name
// right after the manager is that tool, and `run X` is `X`.
func pmSubcommand(pm string, w []string) []string {
	switch {
	case len(w) > 0 && w[0] == "exec" && pm != "yarn":
		return firstFamily(optionTails(w[1:], pmValueOpts, pmShellOpts), toolShape)
	case len(w) > 0 && pmBins[w[0]]:
		return toolShape(w)
	case len(w) > 1 && w[0] == "run":
		return append([]string{pm}, w[1:]...)
	}
	return append([]string{pm}, w...)
}

// normalize is the command as its tool sees it: the runners in front (npx, pnpm exec, yarn, bunx, a path into node_modules/.bin or
// vendor/bin) are taken off, and `pnpm run build` is `pnpm build`. The manifest's words are put through the same, so both sides
// speak the same way.
func normalize(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	first := binName(argv[0])
	switch first {
	case "npx", "bunx":
		tails := optionTails(argv[1:], npxValueOpts, npxShellOpts)
		if len(tails) == 0 {
			return toolShape(argv)
		}
		return firstFamily(tails, toolShape)
	case "pnpm", "yarn", "npm", "bun":
		tails := optionTails(argv[1:], pmValueOpts, nil)
		return firstFamily(tails, func(t []string) []string { return pmSubcommand(first, t) })
	}
	return toolShape(argv)
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

package gate

import "strings"

// PHP: composer and the test runners it starts (phpunit, pest). A project's integration suite runs for minutes, so a whole
// run is heavy; a run narrowed to a filter or to test files is light; install, update, require, dump-autoload, build and
// every other composer command are none (a build is never held back).

// phpunitValueFlags are the phpunit and pest options whose value is the next word (given as `--opt=value`, the value is in the
// same word). An option that is not here is a switch, so the word after it is a test path.
var phpunitValueFlags = map[string]bool{
	"--filter": true, "--testsuite": true, "--group": true, "--exclude-group": true, "--covers": true, "--uses": true,
	"-c": true, "--configuration": true, "--bootstrap": true, "--include-path": true, "-d": true,
	"--log-junit": true, "--log-teamcity": true, "--log-events-text": true, "--log-events-verbose-text": true,
	"--testdox-html": true, "--testdox-text": true, "--testdox-xml": true,
	"--coverage-clover": true, "--coverage-cobertura": true, "--coverage-crap4j": true, "--coverage-html": true,
	"--coverage-php": true, "--coverage-xml": true, "--order-by": true, "--random-order-seed": true,
	"--cache-result-file": true, "--cache-directory": true, "--test-suffix": true, "--default-time-limit": true,
	"--generate-baseline": true, "--use-baseline": true, "--min": true, "--shard": true, "--processes": true,
	// phpunit 10/11 and pest
	"--exclude-filter": true, "--exclude-testsuite": true, "--test-directory": true, "--requires-php-extension": true,
	"--issue": true, "--pr": true, "--pull-request": true, "--extension": true, "--coverage-filter": true, "--columns": true,
	"--coverage-text-file": true,
	// pest mutation testing (`--mutate`): classes and paths whose values contain slashes
	"--class": true, "--ignore": true, "--id": true, "--exactly": true,
}

// phpunitNoRun are the options that print something and run no test.
var phpunitNoRun = map[string]bool{
	"--version": true, "--help": true, "-h": true, "--list-tests": true, "--list-tests-xml": true, "--list-suites": true,
	"--list-groups": true, "--generate-configuration": true, "--migrate-configuration": true, "--check-version": true,
	"--atleast-version": true, "--list-test-files": true,
}

// classifyPhpunit reads phpunit's and pest's words: a filter or a test path narrows the run (light), anything else is a whole
// run (heavy), including --testsuite (a whole suite).
func classifyPhpunit(args []string) Class {
	narrowed := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, _, hasVal := strings.Cut(a, "=")
		switch {
		case phpunitNoRun[name]:
			return ClassNone
		case a == "--":
			// everything after it is a path
			for _, w := range args[i+1:] {
				if looksLikeTestPath(w) {
					narrowed = true
				}
			}
			i = len(args)
		case name == "--filter":
			narrowed = true
			if !hasVal {
				i++
			}
		case strings.HasPrefix(a, "-"):
			if !hasVal && phpunitValueFlags[name] {
				i++
			}
		default:
			// A test file or directory narrows the run. A word that does not look like a path is more likely the value of an option
			// this table does not know (the next release adds some), and then the run is as wide as it was: heavy is the safe side.
			if looksLikeTestPath(a) {
				narrowed = true
			}
		}
	}
	if narrowed {
		return ClassLight
	}
	return ClassHeavy
}

// looksLikeTestPath says whether a phpunit/pest word names a test file or directory: a .php file or anything with a slash.
func looksLikeTestPath(w string) bool {
	return strings.HasSuffix(w, ".php") || strings.ContainsAny(w, `/\`)
}

// isComposerTestScript says whether a composer script name is a test script: `test`, `test:integration`, `tests`, `Test`… The
// case does not matter here: a script name composer treats as another one is wrongly held back at worst, never let through.
func isComposerTestScript(name string) bool { return strings.HasPrefix(strings.ToLower(name), "test") }

// firstWord is the index of the first word of args that is not an option, or -1.
func firstWord(args []string) int {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			return i
		}
	}
	return -1
}

// afterScript classifies a composer test script by the words handed to it after `--` (composer passes them on to the script,
// which is usually phpunit): a filter or a test path in them narrows it; without them it is a whole run.
func afterScript(rest []string) Class {
	for i, a := range rest {
		if a == "--" {
			if c := classifyPhpunit(rest[i+1:]); c == ClassLight {
				return ClassLight
			}
			return ClassHeavy
		}
	}
	return ClassHeavy
}

// classifyComposer reads `composer`: a test script (`test`, `test:*`, also through `run` / `run-script`) is heavy, narrowed to
// light by a filter or test path after `--`; `composer exec <bin>` is that binary's own class; everything else is none.
func classifyComposer(args []string, e Env) Class {
	i := 0
	for ; i < len(args); i++ { // composer's own options before the command
		a := args[i]
		if a == "-d" || a == "--working-dir" || a == "-f" || a == "--file" {
			i++ // the option's value is the next word
			continue
		}
		if strings.HasSuffix(a, ".json") && !strings.HasPrefix(a, "-") {
			continue // a composer.json given to an option this table does not know: not the command (a test run must not slip past as "none")
		}
		if !strings.HasPrefix(a, "-") {
			break
		}
	}
	if i >= len(args) {
		return ClassNone
	}
	word, rest := args[i], args[i+1:]
	switch word {
	case "run", "run-script", "run-scripts":
		j := firstWord(rest)
		if j < 0 || !isComposerTestScript(rest[j]) {
			return ClassNone
		}
		return afterScript(rest[j+1:])
	case "exec":
		j := firstWord(rest)
		if j < 0 {
			return ClassNone
		}
		tail := rest[j+1:]
		if len(tail) > 0 && tail[0] == "--" {
			tail = tail[1:]
		}
		return Classify(append([]string{rest[j]}, tail...), e)
	}
	if isComposerTestScript(word) {
		return afterScript(rest)
	}
	return ClassNone // install, update, require, dump-autoload, build, validate, show, …
}

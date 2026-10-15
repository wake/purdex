package gate

import (
	"regexp"
	"strings"
)

// Env is what the classifier needs to know about the place a command runs in, because a few tools behave by it: vitest
// with no subcommand watches only for a person at a terminal, and runs once for an agent, a script and a CI job.
// Classify cannot guess these from the words, so the caller passes them (EnvOf).
type Env struct {
	StdinTTY bool // stdin is a terminal
	Agent    bool // the command runs for a coding agent (vitest's own test: std-env's isAgent)
	CI       bool // a CI environment (vitest's own test: std-env's isCI)
}

// agentVars are the environment variables that std-env 4 (the library vitest asks) reads as "an agent runs this":
// AI_AGENT with any value, and one of the vendor variables below. Three more are matched on a value (see agentValues).
var agentVars = []string{
	"AI_AGENT", "CLAUDECODE", "CLAUDE_CODE", "REPL_ID", "GEMINI_CLI", "CODEX_SANDBOX", "CODEX_THREAD_ID", "OPENCODE",
	"AUGMENT_AGENT", "GOOSE_PROVIDER", "CURSOR_AGENT",
}

var agentValues = map[string]*regexp.Regexp{
	"PATH":         regexp.MustCompile(`\.pi[\\/]agent`),
	"EDITOR":       regexp.MustCompile(`devin`),
	"TERM_PROGRAM": regexp.MustCompile(`kiro`),
}

// EnvOf reads the agent and CI marks from an environment (KEY=VALUE words) and takes the terminal fact from the caller.
func EnvOf(environ []string, stdinTTY bool) Env {
	vars := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	e := Env{StdinTTY: stdinTTY}
	for _, k := range agentVars {
		if vars[k] != "" {
			e.Agent = true
		}
	}
	for k, re := range agentValues {
		if v := vars[k]; v != "" && re.MatchString(v) {
			e.Agent = true
		}
	}
	e.CI = isCI(vars)
	return e
}

// isCI is std-env's isCI: the CI variable with any value, or else the first provider variable that is set (ciProviders,
// in std-env's order) unless that provider is one that is not CI (a local run on the platform).
func isCI(vars map[string]string) bool {
	if vars["CI"] != "" {
		return true
	}
	for _, p := range ciProviders {
		if vars[p.Var] != "" {
			return p.CI
		}
	}
	return false
}

// watchesByDefault says vitest, given no subcommand and no flag for it, would sit in watch mode: a person at a
// terminal, outside CI. Everywhere else it runs once.
func (e Env) watchesByDefault() bool { return !e.CI && e.StdinTTY && !e.Agent }

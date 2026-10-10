package nex

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// U18 (#1647): a handoff keeps the session's model and effort. The statusline's last reading of the session is the source
// (agent.ContextUsageReader — the hook payloads carry neither); this file turns it into the flags of a resume command and
// into the labels that carry it across a worker stint.

// Execution labels that carry the reading across a worker stint (written at the handoff, read at the take-back), so a daemon
// restart or the usage cache's eviction does not lose it.
const (
	handoffModelLabel  = "purdex.model"
	handoffEffortLabel = "purdex.effort"
)

// modelNameRE is nexen's rule for a model argument (store.ValidateModel, v0.21.0): an alias or a full id as Claude Code reports
// it — Anthropic ids, Bedrock ids (':' and inference-profile ARNs with '/'), Vertex ids ('@') — optionally ending in [1m]. It
// starts with an alphanumeric, so it is never a flag, and has no quote, space or shell character, so it is one argv word and safe
// single-quoted on a command line. team.ValidModel is narrower on purpose (it also guards the member launch line, U20) and is
// left alone; this module keeps its own guard aligned with the library it hands the value to.
var modelNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,159}(\[1m\])?$`)

func validModel(s string) bool { return modelNameRE.MatchString(s) }

// sessionReading is the model id and effort level of a session, "" for what is not known.
type sessionReading struct{ Model, Effort string }

// resumeIDRE finds the session id of a `--resume {id}` (or `-r {id}`, `--resume={id}`) in a resume template. The flags go
// right after it, so whatever the user's template puts behind it stays behind it.
var resumeIDRE = regexp.MustCompile(`(?:^|\s)(?:--resume|-r)(?:\s+|=)\{id\}`)

// applySessionFlags returns the resume template with the reading's flags after the session id of its `--resume {id}`. A template
// without one (codex, opencode, a wrapper that takes the id another way) is not ours to change. A flag the template already has
// stays the user's; a value that is not safe on a command line (validModel / team.ValidEffort) is not used.
func applySessionFlags(template string, r sessionReading) string {
	locs := resumeIDRE.FindAllStringIndex(template, 2)
	if len(locs) != 1 || !simpleCommand(template) {
		return template // none, two, or a shell structure in which the flags' command is a guess: the user's text stays
	}
	loc := locs[0]
	words := strings.Fields(template)
	var add string
	if r.Model != "" && validModel(r.Model) && !hasFlag(words, "--model") {
		add += " --model '" + r.Model + "'" // single-quoted: "[1m]" would glob unquoted (as the member launch line does)
	}
	if r.Effort != "" && team.ValidEffort(r.Effort) && !hasFlag(words, "--effort") {
		add += " --effort " + r.Effort
	}
	return template[:loc[1]] + add + template[loc[1]:]
}

// simpleCommand says whether the template is ONE simple command: words separated by blanks, optionally led by NAME=value
// assignments. Anything that gives the line a shell structure — a pipe, a list, a background, a redirection, a quote, an escape,
// a comment, a substitution, a group, a newline — or an option terminator makes "which command do the flags belong to, and does a
// --model further on belong to Claude" a guess, so those templates are not rewritten.
func simpleCommand(template string) bool {
	// {id} is the one brace the template may carry: take it out, then nothing with a meaning to the shell may be left
	if strings.ContainsAny(strings.ReplaceAll(template, "{id}", ""), "|&;<>()$`\\\"'#\n\r*?[]{}~!") {
		return false
	}
	return !slices.Contains(strings.Fields(template), "--")
}

func hasFlag(words []string, name string) bool {
	for _, w := range words {
		if w == name || strings.HasPrefix(w, name+"=") {
			return true
		}
	}
	return false
}

// readingOf is the session's reading: the execution's labels (JSON text, "" when there is no execution) first, then the
// statusline's last reading, each field on its own; only values that pass validModel / team.ValidEffort.
func (m *Module) readingOf(sid, labelsJSON string) sessionReading {
	var r sessionReading
	var labels map[string]string
	if labelsJSON != "" && json.Unmarshal([]byte(labelsJSON), &labels) == nil {
		if v := labels[handoffModelLabel]; validModel(v) {
			r.Model = v
		}
		if v := labels[handoffEffortLabel]; team.ValidEffort(v) {
			r.Effort = v
		}
	}
	if r.Model != "" && r.Effort != "" {
		return r
	}
	if reader, ok := m.owners.(agent.ContextUsageReader); ok {
		if u, found := reader.ContextUsage(sid); found {
			if r.Model == "" && validModel(u.ModelID) {
				r.Model = u.ModelID
			}
			if r.Effort == "" && team.ValidEffort(u.Effort) {
				r.Effort = u.Effort
			}
		}
	}
	return r
}

// handoffLabels are the labels of a handoff's execution: the binding to the session, and the reading when there is one.
func handoffLabels(code, sid string, r sessionReading) map[string]string {
	out := map[string]string{"source": "purdex", handoffSessionLabel: code, purdexSessionLabel: sid}
	for k, v := range r.labels() {
		out[k] = v
	}
	return out
}

// labels are the reading as execution labels (only what is known).
func (r sessionReading) labels() map[string]string {
	out := map[string]string{}
	if r.Model != "" {
		out[handoffModelLabel] = r.Model
	}
	if r.Effort != "" {
		out[handoffEffortLabel] = r.Effort
	}
	return out
}

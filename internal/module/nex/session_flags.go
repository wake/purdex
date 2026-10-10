package nex

import (
	"encoding/json"
	"regexp"
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

// sessionReading is the model id and effort level of a session, "" for what is not known.
type sessionReading struct{ Model, Effort string }

// resumeIDRE finds the session id of a `--resume {id}` (or `-r {id}`, `--resume={id}`) in a resume template. The flags go
// right after it, so whatever the user's template puts behind it stays behind it.
var resumeIDRE = regexp.MustCompile(`(?:^|\s)(?:--resume|-r)(?:\s+|=)\{id\}`)

// applySessionFlags returns the resume template with the reading's flags after the session id of its `--resume {id}`. A template
// without one (codex, opencode, a wrapper that takes the id another way) is not ours to change. A flag the template already has
// stays the user's; a value that is not safe on a command line (team.ValidModel / ValidEffort) is not used.
func applySessionFlags(template string, r sessionReading) string {
	loc := resumeIDRE.FindStringIndex(template)
	if loc == nil {
		return template
	}
	words := strings.Fields(template)
	var add string
	if r.Model != "" && team.ValidModel(r.Model) && !hasFlag(words, "--model") {
		add += " --model '" + r.Model + "'" // single-quoted: "[1m]" would glob unquoted (as the member launch line does)
	}
	if r.Effort != "" && team.ValidEffort(r.Effort) && !hasFlag(words, "--effort") {
		add += " --effort " + r.Effort
	}
	return template[:loc[1]] + add + template[loc[1]:]
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
// statusline's last reading, each field on its own; only values that pass team.ValidModel / ValidEffort.
func (m *Module) readingOf(sid, labelsJSON string) sessionReading {
	var r sessionReading
	var labels map[string]string
	if labelsJSON != "" && json.Unmarshal([]byte(labelsJSON), &labels) == nil {
		if v := labels[handoffModelLabel]; team.ValidModel(v) {
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
			if r.Model == "" && team.ValidModel(u.ModelID) {
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

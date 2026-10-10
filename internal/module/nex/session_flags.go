package nex

// U18 (#1647): a handoff keeps the session's model and effort. The statusline's last reading of the session is the source
// (agent.ContextUsageReader); this file turns it into the flags of a resume command. Scaffold: behaviour lands with the tests.

// Execution labels that carry the reading across a worker stint (written at the handoff, read at the take-back), so a daemon
// restart or the usage cache's eviction does not lose it.
const (
	handoffModelLabel  = "purdex.model"
	handoffEffortLabel = "purdex.effort"
)

// sessionReading is the model id and effort level of a session, "" for what is not known.
type sessionReading struct{ Model, Effort string }

// applySessionFlags returns the resume template with the reading's flags after the session id of its `--resume {id}`.
func applySessionFlags(template string, r sessionReading) string { return template }

// readingOf is the session's reading: the execution's labels (JSON text, "" when there is no execution) first, then the
// statusline's last reading, each field on its own; only values that pass team.ValidModel / ValidEffort.
func (m *Module) readingOf(sid, labelsJSON string) sessionReading { return sessionReading{} }

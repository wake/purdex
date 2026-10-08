package ccnorm

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/wake/purdex/internal/convmodel"
)

// systemRow: turn_duration ends a turn, local_command is a command that
// never reaches the model, compact_boundary is a compaction. Other subtypes
// (stop_hook_summary, api_error, …) are skipped and counted. isMeta is not
// consulted: Claude Code writes local_command rows with isMeta true.
func (n *Normalizer) systemRow(l *rawLine, off int64) {
	switch l.subtype {
	case "turn_duration":
		if len(n.turns) == 0 {
			n.skip("orphan_turn_duration")
			return
		}
		ti := len(n.turns) - 1
		tr := n.turns[ti]
		tr.duration, tr.durationAt = true, l.at
		n.attribute(ti, l.at)
	case "local_command":
		n.localCommand(l, off)
	default:
		n.skip("system:" + l.subtype)
	}
}

// localCommand reads a system/local_command row. Claude Code 2.1.289+ records
// a local command such as /model as two system rows instead of user rows,
// with the same tags in a top-level string content: a slash command, and its
// <local-command-stdout>. Anything else is skipped. As in
// prelude/classify.go:560-598, a command only counts when its
// <command-name> is closed and starts with '/', since a system row is written
// by the CLI and anything that does not read as a whole typed command is not
// taken for the person's words.
func (n *Normalizer) localCommand(l *rawLine, off int64) {
	text, ok := jsonString(l.Content)
	if !ok {
		n.skip("local_command:content")
		return
	}
	switch {
	case isSlashCommand(text):
		typed := slashText(text)
		if !tagClosed(text, "command-name") || !strings.HasPrefix(typed, "/") {
			n.skip("local_command:other")
			return
		}
		ti, ok := n.openTurn(l.uuid, l.at, off)
		if !ok {
			return
		}
		n.turns[ti].modelFree = true
		n.addUser(ti, l.uuid, l.at, convmodel.SourceSlash, nil, typed, nil, off)
		n.attribute(ti, l.at)
	case firstTag(text) == "local-command-stdout":
		v, _ := tagValue(text, "local-command-stdout")
		n.commandOutput(l, off, v, true)
	default:
		n.skip("local_command:other")
	}
}

// interruptMarker turns the "[Request interrupted by user…" row into an
// `interrupted` system item (the marker text is not a user item) and marks
// the turn: a marker ends it as interrupted even when a turn_duration
// follows (a refusal writes both).
func (n *Normalizer) interruptMarker(l *rawLine, off int64) {
	ti := n.ensureTurn(l.uuid, l.at, off)
	tr := n.turns[ti]
	tr.interrupted, tr.markerAt = true, l.at
	n.upsert(tr.t.ID, convmodel.Item{Type: convmodel.ItemSystem, System: &convmodel.System{
		ID: l.uuid, At: l.at, Kind: convmodel.SystemInterrupted,
	}}, off)
	n.attribute(ti, l.at)
}

// marshalNoEscape encodes v without HTML escaping, so detail text with
// '<' or '&' stays readable.
func marshalNoEscape(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil
	}
	return json.RawMessage(bytes.TrimSpace(b.Bytes()))
}

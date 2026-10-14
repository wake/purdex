package workbook

import (
	"bytes"
	"encoding/json"
	"strings"
)

// ModelEvent is one item of a journal job's `events` as the model wrote it (prompt document §1). Nothing here is
// trusted yet: ValidateJournal cuts, scrubs and filters it.
type ModelEvent struct {
	Kind        string
	By          string
	From        string
	To          string
	Text        string
	Items       []string
	DetailStart string
}

// ModelRoadmap is one item of `roadmap`: a status change the model asks for.
type ModelRoadmap struct {
	Feature, Stage, Status string
}

// JournalOutput is a journal job's answer after the strict parse. Dropped counts the items the parse refused to keep
// (unknown kind, a required field missing): a shape that is wrong anywhere is ErrFormat instead, never a drop.
type JournalOutput struct {
	Events      []ModelEvent
	ConfirmDone []int
	Roadmap     []ModelRoadmap
	Dropped     int
}

// rawEvent and rawRoadmap decode an item with the wrong type refused (a string where an array belongs fails the decode).
type rawEvent struct {
	Kind        string            `json:"kind"`
	By          string            `json:"by"`
	From        string            `json:"from"`
	To          string            `json:"to"`
	Text        string            `json:"text"`
	Items       []json.RawMessage `json:"items"`
	DetailStart string            `json:"detail_start"`
}

type rawRoadmap struct {
	Feature string `json:"feature"`
	Stage   string `json:"stage"`
	Status  string `json:"status"`
}

// journalArray returns the elements of a member that must be a JSON array (null is not one).
func journalArray(raw json.RawMessage) ([]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, ErrFormat
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, ErrFormat
	}
	return items, nil
}

// objectItem decodes one array element that must be an object without a repeated member (null decodes into a struct
// without an error, so the first byte is checked).
func objectItem(raw json.RawMessage, into any) error {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 || b[0] != '{' || hasDuplicateMember(string(b)) {
		return ErrFormat
	}
	if json.Unmarshal(b, into) != nil {
		return ErrFormat
	}
	return nil
}

// ParseJournalJSON reads a journal job's answer (prompt document §1, plan T-1b1.3). Like the v2 parser one code fence
// is allowed around the object and a repeated member name is refused; the members `events`, `confirm_done` and `roadmap`
// must all be there as arrays (extra top-level members are ignored, as in v2). A wrong type anywhere, a null or
// non-object item, a repeated member inside an item, a non-string confirm item or a non-integer confirm_done number is
// ErrFormat (the caller retries once). An item with an unknown kind, an unknown `by` on a request, or a missing
// text / feature / stage / status is dropped and counted in Dropped. The v2 ParseModelJSON is untouched.
func ParseJournalJSON(text string) (JournalOutput, error) {
	_, members, err := objectOf(text)
	if err != nil {
		return JournalOutput{}, err
	}
	var out JournalOutput
	rawEvents, rawDone, rawRoad := members["events"], members["confirm_done"], members["roadmap"]
	evs, err := journalArray(rawEvents)
	if err != nil {
		return JournalOutput{}, err
	}
	done, err := journalArray(rawDone)
	if err != nil {
		return JournalOutput{}, err
	}
	road, err := journalArray(rawRoad)
	if err != nil {
		return JournalOutput{}, err
	}
	for _, raw := range evs {
		var r rawEvent
		if err := objectItem(raw, &r); err != nil {
			return JournalOutput{}, err
		}
		ev := ModelEvent{Kind: r.Kind, By: r.By, From: r.From, To: r.To, Text: r.Text, DetailStart: r.DetailStart}
		for _, it := range r.Items {
			b := bytes.TrimSpace(it)
			var s string
			if len(b) == 0 || b[0] != '"' || json.Unmarshal(b, &s) != nil {
				return JournalOutput{}, ErrFormat
			}
			ev.Items = append(ev.Items, s)
		}
		if !knownEvent(ev) {
			out.Dropped++
			continue
		}
		out.Events = append(out.Events, ev)
	}
	for _, raw := range done {
		b := bytes.TrimSpace(raw)
		var n int
		if len(b) == 0 || b[0] == 'n' || json.Unmarshal(b, &n) != nil {
			return JournalOutput{}, ErrFormat
		}
		out.ConfirmDone = append(out.ConfirmDone, n)
	}
	for _, raw := range road {
		var r rawRoadmap
		if err := objectItem(raw, &r); err != nil {
			return JournalOutput{}, err
		}
		if strings.TrimSpace(r.Feature) == "" || strings.TrimSpace(r.Stage) == "" || strings.TrimSpace(r.Status) == "" {
			out.Dropped++
			continue
		}
		out.Roadmap = append(out.Roadmap, ModelRoadmap(r))
	}
	return out, nil
}

// knownEvent: a kind the model may write (request / comm / confirm), with the text it needs. Task kinds are never the
// model's (prompt document §1), so they are unknown here.
func knownEvent(ev ModelEvent) bool {
	if strings.TrimSpace(ev.Text) == "" {
		return false
	}
	switch ev.Kind {
	case "comm", "confirm":
		return true
	case "request":
		return ev.By == "user" || ev.By == "leader" || ev.By == "member"
	}
	return false
}

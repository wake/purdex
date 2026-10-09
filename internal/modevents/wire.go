package modevents

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

// WireVersion is the only batch version this daemon accepts.
const WireVersion = 1

// MaxEvents is the most events one batch may carry.
const MaxEvents = 500

// Batch is one POST /mod/v1/events body (spec §6.2).
type Batch struct {
	V          int    `json:"v"`
	Stream     string `json:"stream"`
	Agent      string `json:"agent"`
	CCVersion  string `json:"cc_version"`
	ModVersion string `json:"mod_version"`
	// DroppedTotal is cumulative for the stream (never reset by the mod);
	// the registry keeps the maximum it has seen.
	DroppedTotal int64 `json:"dropped_total"`
	// CWD and Interactive come with every batch from a U1-2a-1 mod, so
	// a daemon that restarts under a running stream learns both without
	// its session.start. An older mod sends neither (zero values).
	CWD         string  `json:"cwd"`
	Interactive bool    `json:"interactive"`
	Events      []Event `json:"events"`
	// Caps are the optional features this mod can run, e.g. "workbook.v2" (absent = none). Each batch restates them,
	// and the registry counts a capability as live for CapsFresh after the last batch that named it.
	Caps []string `json:"caps,omitempty"`
}

// Limits of Caps.
const (
	MaxCaps    = 8
	CodeBadCap = "bad_caps"
)

var capRe = regexp.MustCompile(`^[a-z][a-z0-9.]{0,31}$`)

// Event is one mod event. Seq is per stream and strictly increasing; At
// is the mod's Date.now() in ms (kept, never used for ordering). A decoded
// event always has a well-formed Type and a JSON object as Data.
type Event struct {
	Seq  int64           `json:"seq"`
	At   int64           `json:"at"`
	SID  string          `json:"sid"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// The v1 event types (spec §6.3).
const (
	TypeSessionStart  = "session.start"
	TypeSessionSwitch = "session.switch" // /clear or /resume: the sid changes, the stream goes on
	TypeSessionEnd    = "session.end"
	TypeTurnStart     = "turn.start"
	TypeTurnComplete  = "turn.complete"
	TypeToolCheck     = "tool.check"
	TypeToolStart     = "tool.start"
	TypeToolEnd       = "tool.end"
	TypeToolApproved  = "tool.approved" // a permission ask approved: its tool row started running
	TypeAgentSpawn    = "agent.spawn"
	TypeCompactStart  = "compact.start"
	TypeCompactEnd    = "compact.end"
	TypeUsage         = "usage"
	TypeBackground    = "background"
	TypeHeartbeat     = "heartbeat"
)

var knownTypes = []string{
	TypeSessionStart, TypeSessionSwitch, TypeSessionEnd,
	TypeTurnStart, TypeTurnComplete,
	TypeToolCheck, TypeToolStart, TypeToolEnd, TypeToolApproved,
	TypeAgentSpawn, TypeCompactStart, TypeCompactEnd,
	TypeUsage, TypeBackground, TypeHeartbeat,
}

var knownTypeSet = func() map[string]bool {
	m := make(map[string]bool, len(knownTypes))
	for _, t := range knownTypes {
		m[t] = true
	}
	return m
}()

// KnownTypes returns the v1 event types. Other types are accepted on the
// wire, counted under "unknown" and never delivered.
func KnownTypes() []string { return append([]string(nil), knownTypes...) }

// IsKnownType reports whether t is a v1 event type.
func IsKnownType(t string) bool { return knownTypeSet[t] }

// The 400 error codes of spec §6.2.
const (
	CodeBadJSON            = "bad_json"
	CodeUnsupportedVersion = "unsupported_version"
	CodeBadStream          = "bad_stream"
	CodeBadEvents          = "bad_events"
	CodeBadSeq             = "bad_seq"
	CodeBadSID             = "bad_sid"
	CodeBadEvent           = "bad_event"
)

// WireError is a rejected batch. Stream is set when the first JSON object
// decoded and its stream id is valid, so the rejection (trailing data
// included) can be counted on that stream; it is empty otherwise. Err is the underlying decode error, if any (a
// read past an http.MaxBytesReader limit surfaces through it).
type WireError struct {
	Code   string
	Stream string
	Err    error
}

func (e *WireError) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Err.Error()
	}
	return e.Code
}

func (e *WireError) Unwrap() error { return e.Err }

var (
	streamRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	sidRe       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	eventTypeRe = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
)

// ValidStream reports whether s is a well-formed stream id.
func ValidStream(s string) bool { return streamRe.MatchString(s) }

// DecodeBatch reads exactly one JSON object from r and validates it. Every
// error is a *WireError. Unknown fields, top-level or per event, are
// ignored.
func DecodeBatch(r io.Reader) (Batch, error) {
	dec := json.NewDecoder(r)
	var b Batch
	if err := dec.Decode(&b); err != nil {
		return Batch{}, &WireError{Code: CodeBadJSON, Err: err}
	}
	// The object decoded: take the stream now, so that every later
	// rejection, trailing data included, can be counted on it.
	stream := ""
	if ValidStream(b.Stream) {
		stream = b.Stream
	}
	// Anything after the object, other than whitespace, is a bad body.
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing data after the batch")
		}
		return Batch{}, &WireError{Code: CodeBadJSON, Stream: stream, Err: err}
	}

	fail := func(code string) (Batch, error) {
		return Batch{}, &WireError{Code: code, Stream: stream}
	}
	if b.V != WireVersion {
		return fail(CodeUnsupportedVersion)
	}
	if stream == "" {
		return fail(CodeBadStream)
	}
	if len(b.Events) == 0 || len(b.Events) > MaxEvents || b.DroppedTotal < 0 {
		return fail(CodeBadEvents)
	}
	var prev int64
	for _, e := range b.Events {
		if e.Seq <= prev {
			return fail(CodeBadSeq)
		}
		prev = e.Seq
	}
	for _, e := range b.Events {
		if !sidRe.MatchString(e.SID) {
			return fail(CodeBadSID)
		}
	}
	for _, e := range b.Events {
		if !eventTypeRe.MatchString(e.Type) || !isObject(e.Data) {
			return fail(CodeBadEvent)
		}
	}
	if len(b.Caps) > MaxCaps {
		return fail(CodeBadCap)
	}
	for _, c := range b.Caps {
		if !capRe.MatchString(c) {
			return fail(CodeBadCap)
		}
	}
	return b, nil
}

// isObject reports whether raw, already valid JSON, is an object. A
// missing member leaves raw empty; JSON null arrives as "null".
func isObject(raw json.RawMessage) bool {
	raw = bytes.TrimLeft(raw, " \t\r\n")
	return len(raw) > 0 && raw[0] == '{'
}

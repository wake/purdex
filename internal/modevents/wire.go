package modevents

import (
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
	DroppedTotal int64   `json:"dropped_total"`
	Events       []Event `json:"events"`
}

// Event is one mod event. Seq is per stream and strictly increasing; At
// is the mod's Date.now() in ms (kept, never used for ordering).
type Event struct {
	Seq  int64           `json:"seq"`
	At   int64           `json:"at"`
	SID  string          `json:"sid"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// The v1 event types (spec §6.3).
const (
	TypeSessionStart = "session.start"
	TypeSessionClear = "session.clear"
	TypeSessionEnd   = "session.end"
	TypeTurnStart    = "turn.start"
	TypeTurnComplete = "turn.complete"
	TypeToolCheck    = "tool.check"
	TypeToolStart    = "tool.start"
	TypeToolEnd      = "tool.end"
	TypeAgentSpawn   = "agent.spawn"
	TypeCompactStart = "compact.start"
	TypeCompactEnd   = "compact.end"
	TypeUsage        = "usage"
	TypeBackground   = "background"
	TypeHeartbeat    = "heartbeat"
)

var knownTypes = []string{
	TypeSessionStart, TypeSessionClear, TypeSessionEnd,
	TypeTurnStart, TypeTurnComplete,
	TypeToolCheck, TypeToolStart, TypeToolEnd,
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
)

// WireError is a rejected batch. Stream is set when the body parsed and
// its stream id is valid, so the rejection can be counted on that stream;
// it is empty otherwise. Err is the underlying decode error, if any (a
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
	streamRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	sidRe    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
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
	// Anything after the object, other than whitespace, is a bad body.
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing data after the batch")
		}
		return Batch{}, &WireError{Code: CodeBadJSON, Err: err}
	}

	stream := ""
	if ValidStream(b.Stream) {
		stream = b.Stream
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
	return b, nil
}

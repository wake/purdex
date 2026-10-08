// Package ccnorm turns a Claude Code transcript (the JSONL file Claude Code
// writes for a session) into the conversation model of package convmodel:
// turns, user messages, agent text, thinking and system items (spec
// 2026-10-08-interface-u1 §8.1, facts in §3 M-U1-7).
//
// A Normalizer covers one file from offset 0 and is fed complete lines with
// their byte offsets; it reports what each line changed, so a caller can
// serve increments. It does no I/O and reads no clock. It imports only the
// standard library and convmodel.
//
// The row rules are ported from the Nexen prelude classifier
// (lab.protype.tw/wake/nexen prelude/classify.go, v0.20.0) rather than
// imported; each port site cites the source lines.
package ccnorm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// object is a decoded JSON object whose values stay raw until a rule needs
// them. Keys are the decoded strings exactly as written: case matters and a
// repeated key overwrites, as with JSON.parse in the client (the reason
// prelude/classify.go:76-100 decodes into a map rather than into a struct).
type object map[string]json.RawMessage

func parseObject(raw json.RawMessage) (object, bool) {
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var o object
	if err := json.Unmarshal(raw, &o); err != nil || o == nil {
		return nil, false
	}
	return o, true
}

func (o object) get(k string) json.RawMessage { return o[k] }

// str is the string value of k, "" when absent or not a string.
func (o object) str(k string) string {
	s, _ := jsonString(o[k])
	return s
}

func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// jsonTrue is true only for the literal true (prelude's jsonTrue: "true" the
// string is not true).
func jsonTrue(raw json.RawMessage) bool {
	return string(raw) == "true"
}

// jsonInt reads a non-negative whole number.
func jsonInt(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || raw[0] == '"' {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil || math.IsNaN(f) || f < 0 || f > 1<<53 {
		return 0, false
	}
	return int64(f), true
}

// timestampMillis parses a row's ISO timestamp; 0 when absent or unparseable
// (prelude/classify.go:824).
func timestampMillis(raw json.RawMessage) int64 {
	s, ok := jsonString(raw)
	if !ok {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// rawLine holds the top-level members the normalizer reads, each nil when
// absent. Every field is raw so that one member of an unexpected type never
// drops a whole row.
type rawLine struct {
	Type               json.RawMessage // "type"
	Subtype            json.RawMessage // "subtype"
	UUID               json.RawMessage // "uuid"
	Timestamp          json.RawMessage // "timestamp"
	Message            json.RawMessage // "message"
	Content            json.RawMessage // "content": a system row's text
	Attachment         json.RawMessage // "attachment"
	CompactMetadata    json.RawMessage // "compactMetadata"
	IsSidechain        json.RawMessage // "isSidechain"
	IsMeta             json.RawMessage // "isMeta"
	IsCompactSummary   json.RawMessage // "isCompactSummary"
	IsAPIErrorMessage  json.RawMessage // "isApiErrorMessage"
	InterruptedMessage json.RawMessage // "interruptedMessageId"
	Origin             json.RawMessage // "origin"
	TurnOrigin         json.RawMessage // "turnOrigin"
	PromptSource       json.RawMessage // "promptSource"
	Entrypoint         json.RawMessage // "entrypoint"
	ThinkingDurationMS json.RawMessage // "thinkingDurationMs"
	Effort             json.RawMessage // "effort"
	PerTurnEffort      json.RawMessage // "perTurnEffort"
	Error              json.RawMessage // "error": an API error row's kind
	CustomTitle        json.RawMessage // "customTitle"
	AITitle            json.RawMessage // "aiTitle"
	ToolUseResult      json.RawMessage // "toolUseResult" (steps, U1-4c)
	DurationMS         json.RawMessage // "durationMs": turn_duration

	// Derived once by decodeLine.
	at              int64 // timestamp, ms; 0 when absent
	uuid            string
	typ, subtype    string
	meta, sidechain bool
	compactSummary  bool
	apiError        bool
	marker          bool // carries interruptedMessageId
}

// decodeLine reads line's top-level members by their exact keys. ok is false
// when line is not a JSON object.
func decodeLine(line []byte) (rawLine, bool) {
	o, ok := parseObject(bytes.TrimSpace(line))
	if !ok {
		return rawLine{}, false
	}
	l := rawLine{
		Type:               o["type"],
		Subtype:            o["subtype"],
		UUID:               o["uuid"],
		Timestamp:          o["timestamp"],
		Message:            o["message"],
		Content:            o["content"],
		Attachment:         o["attachment"],
		CompactMetadata:    o["compactMetadata"],
		IsSidechain:        o["isSidechain"],
		IsMeta:             o["isMeta"],
		IsCompactSummary:   o["isCompactSummary"],
		IsAPIErrorMessage:  o["isApiErrorMessage"],
		InterruptedMessage: o["interruptedMessageId"],
		Origin:             o["origin"],
		TurnOrigin:         o["turnOrigin"],
		PromptSource:       o["promptSource"],
		Entrypoint:         o["entrypoint"],
		ThinkingDurationMS: o["thinkingDurationMs"],
		Effort:             o["effort"],
		PerTurnEffort:      o["perTurnEffort"],
		Error:              o["error"],
		CustomTitle:        o["customTitle"],
		AITitle:            o["aiTitle"],
		ToolUseResult:      o["toolUseResult"],
		DurationMS:         o["durationMs"],
	}
	l.typ, _ = jsonString(l.Type)
	l.subtype, _ = jsonString(l.Subtype)
	l.uuid, _ = jsonString(l.UUID)
	l.at = timestampMillis(l.Timestamp)
	l.sidechain = jsonTrue(l.IsSidechain)
	l.meta = jsonTrue(l.IsMeta)
	l.compactSummary = jsonTrue(l.IsCompactSummary)
	l.apiError = jsonTrue(l.IsAPIErrorMessage)
	l.marker = len(l.InterruptedMessage) > 0 && string(l.InterruptedMessage) != "null"
	return l, true
}

func (l *rawLine) str(raw json.RawMessage) string {
	s, _ := jsonString(raw)
	return s
}

// block is one content block.
type block struct {
	typ  string
	text string // a text block's text, a thinking block's thinking
	obj  object
}

// contentBlocks reads a message content (or a queued prompt) as blocks: an
// array's objects, a string as one text block. An array element that is not
// an object is not a block. ok is false for any other shape
// (prelude/classify.go contentBlocks).
func contentBlocks(content json.RawMessage) ([]block, bool) {
	if s, ok := jsonString(content); ok {
		return []block{{typ: "text", text: s}}, true
	}
	if len(content) == 0 || content[0] != '[' {
		return nil, false
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(content, &elems); err != nil {
		return nil, false
	}
	blocks := make([]block, 0, len(elems))
	for _, e := range elems {
		o, ok := parseObject(e)
		if !ok {
			continue
		}
		b := block{typ: o.str("type"), obj: o}
		switch b.typ {
		case "text":
			b.text = o.str("text")
		case "thinking":
			b.text = o.str("thinking")
		}
		blocks = append(blocks, b)
	}
	return blocks, true
}

// joinText is the text of any content: a string as is, an array's text
// blocks joined by newlines (other blocks contribute nothing).
func joinText(blocks []block) string {
	var parts []string
	for _, b := range blocks {
		if b.typ == "text" {
			parts = append(parts, b.text)
		}
	}
	return strings.Join(parts, "\n")
}

func hasToolResult(blocks []block) bool {
	for _, b := range blocks {
		if b.typ == "tool_result" {
			return true
		}
	}
	return false
}

// sizedValue is a member of an image's source object read without keeping a
// long value: a short one stays raw (media_type), a string also has its
// base64 size measured in place (UnmarshalJSON is handed a slice of the
// input, so nothing is copied).
type sizedValue struct {
	raw  json.RawMessage // the value when it is short, else nil
	size int64           // base64Size of a string value
}

func (v *sizedValue) UnmarshalJSON(b []byte) error {
	if len(b) <= 1024 {
		v.raw = append(json.RawMessage(nil), b...)
	}
	if len(b) >= 2 && b[0] == '"' {
		v.size = base64Size(b[1 : len(b)-1])
	}
	return nil
}

// imageSize is the media type and decoded size of an image block's base64
// data (base64.StdEncoding.DecodedLen less the padding, on the length
// trimmed of white space). The data itself is never copied or kept, however
// it is escaped.
func imageSize(b block) (mediaType string, size int64) {
	src := b.obj.get("source")
	if len(src) == 0 || src[0] != '{' {
		return "", 0
	}
	var members map[string]sizedValue
	if err := json.Unmarshal(src, &members); err != nil {
		return "", 0
	}
	mediaType, _ = jsonString(members["media_type"].raw)
	return mediaType, members["data"].size // 0 unless data is a string
}

// base64Size measures the inside of a JSON string (the quotes off) as the
// string it decodes to would measure: white space at either end does not
// count, nor does '=' padding. It walks the escapes (\/ \n \uXXXX …) and
// keeps no decoded copy.
func base64Size(in []byte) int64 {
	var chars, ws, pad int
	for i := 0; i < len(in); {
		c, w := in[i], 1
		i++
		if c == '\\' && i < len(in) {
			e := in[i]
			i++
			switch e {
			case 'n':
				c = '\n'
			case 't':
				c = '\t'
			case 'r':
				c = '\r'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'u':
				r := rune(0)
				for j := 0; j < 4 && i < len(in); j++ {
					r = r<<4 | rune(hexVal(in[i]))
					i++
				}
				if r < utf8.RuneSelf {
					c = byte(r)
				} else {
					c, w = 0xff, max(utf8.RuneLen(r), 3) // not white space, not '='
				}
			default: // \" \\ \/
				c = e
			}
		}
		switch c {
		case ' ', '\t', '\n', '\v', '\f', '\r':
			if chars > 0 {
				ws++
			}
			continue
		}
		if ws > 0 { // white space inside the data, not at its end
			pad = 0
		}
		chars += ws + w
		ws = 0
		if c == '=' {
			pad++
		} else {
			pad = 0
		}
	}
	return int64(base64.StdEncoding.DecodedLen(chars)) - int64(min(pad, 2))
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 0
}

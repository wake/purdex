package ccnorm

import (
	"bytes"
	"encoding/json"
	"sort"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convmodel"
)

// maxInputDepth is how deep a tool input may nest before the rest is
// dropped (Claude Code's inputs are two or three levels deep). It is the
// cap Validate checks.
const maxInputDepth = convmodel.MaxInputDepth

// capInput bounds a tool input for storing (lead ruling D9): every string
// value at most 4 KiB (head), the whole at most 16 KiB, nesting at most
// maxInputDepth. cut reports that the stored input is not the complete tool
// input, whatever the reason (a string cut, the total cap, the depth cap, a
// dropped member); Validate cannot tell the reasons apart and does not try.
// The result is always a valid JSON object.
//
// The spec says a step input is an object. Anything else (a string, array,
// number, bool, null, or no input at all) is stored as {} and is not flagged:
// there was no object to cut. No wire change.
//
// A whole input over 16 KiB keeps its top-level members in sorted key order
// while they fit and fills the rest of the budget from the first member that
// does not (a string is cut, an array keeps the elements that fit), so a
// client still gets a value of the same shape.
func capInput(raw json.RawMessage) (out json.RawMessage, cut bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if len(raw) == 0 || raw[0] != '{' {
		return json.RawMessage(`{}`), false
	}
	if dec.Decode(&v) != nil {
		return json.RawMessage(`{}`), true // an object that cannot be read
	}
	v = capValue(v, 0, &cut)
	b := marshalNoEscape(v)
	if len(b) <= convmodel.MaxInput {
		return b, cut
	}
	b = fitJSON(v, convmodel.MaxInput)
	if b == nil || !json.Valid(b) {
		b = json.RawMessage(`{}`)
	}
	return b, true
}

// capValue caps every string in v in place and returns v.
func capValue(v any, depth int, cut *bool) any {
	switch x := v.(type) {
	case string:
		s, c := capText(x, convmodel.MaxInputString)
		*cut = *cut || c
		return s
	case []any:
		if depth >= maxInputDepth {
			*cut = true
			return nil
		}
		for i := range x {
			x[i] = capValue(x[i], depth+1, cut)
		}
	case map[string]any:
		if depth >= maxInputDepth {
			*cut = true
			return nil
		}
		for k, e := range x {
			x[k] = capValue(e, depth+1, cut)
		}
	}
	return v
}

// jsize is the encoded size of v, or limit+1 as soon as it is known to be
// larger than limit (so measuring costs at most about limit).
func jsize(v any, limit int) int {
	switch x := v.(type) {
	case string:
		return escSize(x, limit)
	case json.Number:
		return len(x)
	case bool:
		if x {
			return 4
		}
		return 5
	case nil:
		return 4
	case []any:
		n := 2
		for i, e := range x {
			if i > 0 {
				n++
			}
			if n += jsize(e, limit-n); n > limit {
				return limit + 1
			}
		}
		return n
	case map[string]any:
		n := 2
		i := 0
		for k, e := range x {
			if i > 0 {
				n++
			}
			i++
			if n += escSize(k, limit) + 1; n > limit {
				return limit + 1
			}
			if n += jsize(e, limit-n); n > limit {
				return limit + 1
			}
		}
		return n
	}
	return limit + 1
}

// escSize is the encoded size of s with its quotes, stopping past limit.
func escSize(s string, limit int) int {
	n := 2
	for _, r := range s {
		if n += runeSize(r); n > limit {
			return limit + 1
		}
	}
	return n
}

// runeSize is how many bytes encoding/json (without HTML escaping) writes
// for r inside a string.
func runeSize(r rune) int {
	switch {
	case r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '\t' || r == '\b' || r == '\f':
		return 2
	case r < 0x20 || r == 0x2028 || r == 0x2029:
		return 6
	}
	return utf8.RuneLen(r)
}

// fitString is the longest head of s whose encoding (with quotes) fits in
// budget bytes, on a rune boundary; ok is false when not even "" fits.
func fitString(s string, budget int) (string, bool) {
	if budget < 2 {
		return "", false
	}
	n, end := 2, 0
	for i, r := range s {
		if n += runeSize(r); n > budget {
			break
		}
		end = i + utf8.RuneLen(r)
	}
	return s[:end], true
}

// fitJSON encodes the head of v in at most budget bytes, as full as the
// structure allows; nil when nothing of v fits. The first member or element
// that does not fit whole is itself fitted, and nothing follows it.
func fitJSON(v any, budget int) json.RawMessage {
	switch x := v.(type) {
	case string:
		if s, ok := fitString(x, budget); ok {
			return marshalNoEscape(s)
		}
		return nil
	case []any:
		if budget < 2 {
			return nil
		}
		out := []byte{'['}
		for i, e := range x {
			room := budget - len(out) - 1 // the closing ]
			if i > 0 {
				room-- // the comma
			}
			enc, whole := fitMember(e, room)
			if enc == nil {
				break
			}
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, enc...)
			if !whole {
				break
			}
		}
		return append(out, ']')
	case map[string]any:
		if budget < 2 {
			return nil
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []byte{'{'}
		for i, k := range keys {
			room := budget - len(out) - 1 // the closing }
			if i > 0 {
				room--
			}
			ks := escSize(k, room)
			if ks+1 > room {
				break
			}
			enc, whole := fitMember(x[k], room-ks-1)
			if enc == nil {
				break
			}
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, marshalNoEscape(k)...)
			out = append(out, ':')
			out = append(out, enc...)
			if !whole {
				break
			}
		}
		return append(out, '}')
	}
	if jsize(v, budget) <= budget {
		return marshalNoEscape(v)
	}
	return nil
}

// fitMember encodes v whole when it fits in room, else its fitted head.
func fitMember(v any, room int) (enc json.RawMessage, whole bool) {
	if jsize(v, room) <= room {
		return marshalNoEscape(v), true
	}
	return fitJSON(v, room), false
}

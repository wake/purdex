package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Validator is implemented by types with enumerations or invariants to check.
type Validator interface{ Validate() error }

// Decode strictly decodes b into T: unknown fields and trailing data are
// rejected, and T's Validate (if any) must pass.
func Decode[T any](b []byte) (T, error) { return decode[T](b, true) }

// DecodeLenient is Decode for what a reader takes from somewhere that may be newer than it: a peer's answer or push, an event, a record
// read back from disk. A field this build does not know is skipped (the writer may be a later build that added one), at any depth;
// everything else is checked as Decode does — the framing (one JSON value, nothing after it), the types of the fields it knows and
// T's Validate, so a wrong value, a missing required field or an enumeration it does not know is still refused.
//
// Use Decode for a request body written by a person or a script (a PUT, a POST): there a field that is not known is a typo, and
// ignoring it silently is the worse outcome. Which of the two a call site takes is fixed by TestStrictDecodeOnlyReadsRequestBodies.
func DecodeLenient[T any](b []byte) (T, error) { return decode[T](b, false) }

func decode[T any](b []byte, strict bool) (T, error) {
	var v, zero T
	dec := json.NewDecoder(bytes.NewReader(b))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(&v); err != nil {
		return zero, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return zero, errors.New("wire: trailing data after JSON value")
	}
	if val, ok := any(v).(Validator); ok {
		if err := val.Validate(); err != nil {
			return zero, err
		}
	}
	return v, nil
}

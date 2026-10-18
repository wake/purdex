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
func Decode[T any](b []byte) (T, error) {
	var v, zero T
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
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

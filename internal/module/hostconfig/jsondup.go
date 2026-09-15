package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// rejectDuplicateKeys reports an error when raw is not exactly one JSON value
// or when any object in it repeats a member name. encoding/json silently keeps
// the last duplicate, so two parsers could disagree on what a body means.
// The walk is iterative (explicit stack) so deep nesting cannot blow the stack.
func rejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	// One entry per open container: an object's seen-key set, or nil for an array.
	var stack []map[string]struct{}
	expectKey := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if expectKey {
			if tok != json.Delim('}') {
				key, ok := tok.(string)
				if !ok {
					return errors.New("invalid JSON: object key is not a string")
				}
				keys := stack[len(stack)-1]
				if _, dup := keys[key]; dup {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				keys[key] = struct{}{}
				expectKey = false
				continue
			}
			stack = stack[:len(stack)-1]
		} else {
			switch tok {
			case json.Delim('{'):
				stack = append(stack, map[string]struct{}{})
				expectKey = true
				continue
			case json.Delim('['):
				stack = append(stack, nil)
				continue
			case json.Delim(']'):
				stack = stack[:len(stack)-1]
			}
		}
		// A value just completed (scalar or closed container).
		if len(stack) == 0 {
			break
		}
		expectKey = stack[len(stack)-1] != nil
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("invalid JSON: trailing data after top-level value")
	}
	return nil
}

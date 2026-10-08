package ccnorm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

// nested is n levels of single-member objects around the string "leaf":
// {"a":{"a":…"leaf"…}}.
func nested(n int) obj {
	var v any = "leaf"
	for range n {
		v = obj{"a": v}
	}
	return v.(obj)
}

// input_truncated means "the stored input is not the complete tool input"
// for any reason; conv() runs Conversation.Validate on every result.

func TestInput_DepthCapSetsTruncatedAndValidates(t *testing.T) {
	// 32 levels of objects (depth 0..31) with a string leaf fit whole
	s := oneStep(t, "Whatever", nested(32), nil)
	if s.InputTruncated || !strings.Contains(string(s.Input), `"leaf"`) {
		t.Errorf("32 levels: truncated=%v input=%s", s.InputTruncated, s.Input)
	}
	// 33 levels: the 33rd object is replaced by null
	s = oneStep(t, "Whatever", nested(33), nil)
	if !s.InputTruncated || strings.Contains(string(s.Input), `leaf`) || !strings.Contains(string(s.Input), `null`) {
		t.Errorf("33 levels: truncated=%v input=%s", s.InputTruncated, s.Input)
	}
	// and nesting through arrays
	var v any = "leaf"
	for range 40 {
		v = []any{v}
	}
	s = oneStep(t, "Whatever", obj{"deep": v}, nil)
	if !s.InputTruncated {
		t.Errorf("40 array levels: input_truncated not set: %s", s.Input)
	}
}

func TestInput_HugeKeyDropsMemberAndValidates(t *testing.T) {
	s := oneStep(t, "Whatever", obj{repeat("a", 20000): 1}, nil)
	if !s.InputTruncated || string(s.Input) != "{}" {
		t.Errorf("truncated=%v input=%.80s (%d bytes)", s.InputTruncated, s.Input, len(s.Input))
	}
	// members sorted before the huge key survive
	s = oneStep(t, "Whatever", obj{"a": "kept", repeat("z", 20000): 1}, nil)
	if !s.InputTruncated || decodeInput(t, s)["a"] != "kept" || len(decodeInput(t, s)) != 1 {
		t.Errorf("truncated=%v input=%.80s", s.InputTruncated, s.Input)
	}
}

func TestInput_NonStringScalarFirstMisfitValidates(t *testing.T) {
	for name, scalar := range map[string]any{"number": 1234567890123, "bool": false, "null": nil} {
		in := obj{"a0": repeat("p", 4096), "a1": repeat("q", 4096), "a2": repeat("r", 4096), "a3": ""}
		// pad a3 so that everything but the scalar is 2 bytes under the cap
		base, _ := json.Marshal(in)
		in["a3"] = repeat("s", convmodel.MaxInput-2-len(base))
		if b, _ := json.Marshal(in); len(b) != convmodel.MaxInput-2 {
			t.Fatalf("%s: setup is %d bytes", name, len(b))
		}
		in["z"] = scalar // sorts last and cannot fit
		s := oneStep(t, "Whatever", in, nil)
		m := decodeInput(t, s)
		if !s.InputTruncated {
			t.Errorf("%s: input_truncated not set", name)
		}
		if _, has := m["z"]; has || len(m) != 4 {
			t.Errorf("%s: %d keys, z kept=%v", name, len(m), has)
		}
		if len(s.Input) > convmodel.MaxInput {
			t.Errorf("%s: %d bytes", name, len(s.Input))
		}
	}
}

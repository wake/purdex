package ccnorm

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

// Row builders for tool steps. Fields and spellings are the ones measured in
// the CC 2.1.292 samples (f3 lines 62-77, c2 lines 97-98 / 148-149).

// toolCall is an assistant row holding one tool_use block.
func toolCall(uuid string, sec float64, id, name string, input obj, opts ...opt) []byte {
	return assistantRow(uuid, sec, "claude-opus-5-5", toolUseBlock(id, name, input), opts...)
}

// resultRow is a user row holding one tool_result block. content is a string
// or a []obj of blocks.
func resultRow(uuid string, sec float64, id string, content any, isErr bool, opts ...opt) []byte {
	b := obj{"type": "tool_result", "tool_use_id": id, "content": content}
	if isErr {
		b["is_error"] = true
	}
	o := common("user", uuid, sec)
	o["message"] = obj{"role": "user", "content": []obj{b}}
	o["sourceToolAssistantUUID"] = "asst-" + id
	return line(o, opts...)
}

func denialKind(k string) opt { return with("toolDenialKind", k) }
func toolUseResult(v any) opt { return with("toolUseResult", v) }
func imgObj(mt, b64 string) obj {
	return obj{"type": "image", "source": obj{"type": "base64", "media_type": mt, "data": b64}}
}

const refusal = "The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed."

// stepsIn returns the steps of turn i in order.
func stepsIn(t testing.TB, c convmodel.Conversation, i int) []*convmodel.Step {
	t.Helper()
	var out []*convmodel.Step
	for _, it := range itemsOf(t, c, i) {
		if it.Step != nil {
			out = append(out, it.Step)
		}
	}
	return out
}

// stepNamed finds a step anywhere in the conversation by id.
func stepNamed(t testing.TB, c convmodel.Conversation, id string) *convmodel.Step {
	t.Helper()
	for _, tr := range c.Turns {
		for _, it := range tr.Items {
			if it.Step != nil && it.Step.ID == id {
				return it.Step
			}
		}
	}
	t.Fatalf("no step %q\n%s", id, dump(c))
	return nil
}

// oneStep runs a prompt, one tool call and (when result != nil) its result,
// and returns the step. The turn is left open.
func oneStep(t testing.TB, name string, input obj, result []byte) *convmodel.Step {
	t.Helper()
	lines := [][]byte{userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", name, input)}
	if result != nil {
		lines = append(lines, result)
	}
	return stepNamed(t, conv(t, lines...), "toolu_1")
}

func repeat(s string, n int) string { return strings.Repeat(s, n) }

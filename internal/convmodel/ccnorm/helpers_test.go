package ccnorm

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convmodel"
)

// Row builders for the tests. Every helper takes field values as arguments
// and writes only fields and spellings measured in spec §3 M-U1-7 (checked
// against the CC 2.1.292 samples); none invents a field.

type obj = map[string]any

// opt changes a row after the helper built it.
type opt func(obj)

func with(k string, v any) opt { return func(o obj) { o[k] = v } }
func without(k string) opt     { return func(o obj) { delete(o, k) } }

const (
	sidA = "7e7f214b-c4e3-48cd-ab15-62a3471bd4fd"
	t0   = 1791378000000 // 2026-10-07T13:00:00Z in ms
)

// at is the ISO timestamp s seconds after t0 (the form rows carry).
func at(s float64) string {
	return time.UnixMilli(t0 + int64(s*1000)).UTC().Format("2006-01-02T15:04:05.000Z")
}

// ms is the model time of the same instant.
func ms(s float64) int64 { return t0 + int64(s*1000) }

func line(o obj, opts ...opt) []byte {
	for _, f := range opts {
		f(o)
	}
	b, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}
	return b
}

func common(typ, uuid string, sec float64) obj {
	return obj{
		"type": typ, "uuid": uuid, "timestamp": at(sec), "isSidechain": false,
		"userType": "external", "entrypoint": "cli", "cwd": "/work/x",
		"sessionId": sidA, "version": "2.1.292", "gitBranch": "HEAD",
	}
}

// userRow is a typed human prompt row (CC 2.1.292 shape, f2b line 58).
func userRow(uuid string, sec float64, text string, opts ...opt) []byte {
	o := common("user", uuid, sec)
	o["message"] = obj{"role": "user", "content": text}
	o["origin"] = obj{"kind": "human"}
	o["promptSource"] = "typed"
	o["turnOrigin"] = "human"
	o["turnPosition"] = obj{"promptIndex": 0, "turnIndex": 0}
	return line(o, opts...)
}

// oldUserRow is the pre-2.1.284 shape: no turnPosition, turnOrigin or origin.
func oldUserRow(uuid string, sec float64, text string, opts ...opt) []byte {
	return userRow(uuid, sec, text, append([]opt{
		without("turnPosition"), without("turnOrigin"), without("origin"), without("promptSource"),
	}, opts...)...)
}

func originKind(k string) opt   { return with("origin", obj{"kind": k}) }
func turnOrigin(k string) opt   { return with("turnOrigin", k) }
func promptSource(k string) opt { return with("promptSource", k) }
func isMeta() opt               { return with("isMeta", true) }
func sidechain() opt            { return with("isSidechain", true) }
func entrypoint(e string) opt   { return with("entrypoint", e) }

func blocksOf(blocks ...obj) opt {
	return func(o obj) { o["message"] = obj{"role": "user", "content": blocks} }
}

// assistantBlock is one assistant row holding one content block (M-U1-7).
func assistantRow(uuid string, sec float64, model string, block obj, opts ...opt) []byte {
	o := common("assistant", uuid, sec)
	o["message"] = obj{
		"model": model, "id": "msg_x", "type": "message", "role": "assistant",
		"content": []obj{block}, "stop_reason": "end_turn",
	}
	o["apiBlockIndex"] = 0
	o["effort"] = "medium"
	o["perTurnEffort"] = "medium"
	return line(o, opts...)
}

func textBlock(s string) obj     { return obj{"type": "text", "text": s} }
func thinkingBlock(s string) obj { return obj{"type": "thinking", "thinking": s, "signature": "sig"} }
func toolUseBlock(id, name string, input obj) obj {
	return obj{"type": "tool_use", "id": id, "name": name, "input": input}
}

func assistantText(uuid string, sec float64, text string, opts ...opt) []byte {
	return assistantRow(uuid, sec, "claude-opus-5-5", textBlock(text), opts...)
}

func assistantThinking(uuid string, sec float64, text string, durMS int, opts ...opt) []byte {
	o := []opt{}
	if durMS > 0 {
		o = append(o, with("thinkingDurationMs", durMS))
	}
	return assistantRow(uuid, sec, "claude-opus-5-5", thinkingBlock(text), append(o, opts...)...)
}

// multiBlockAssistant is an older-version row with several blocks in one row.
func multiBlockAssistant(uuid string, sec float64, blocks ...obj) []byte {
	o := common("assistant", uuid, sec)
	o["message"] = obj{"model": "claude-opus-5-5", "role": "assistant", "content": blocks}
	return line(o)
}

// apiErrorRow is the synthetic row of an API error (spec §3 M-U1-7).
func apiErrorRow(uuid string, sec float64, kind, text string) []byte {
	o := common("assistant", uuid, sec)
	o["message"] = obj{"model": "<synthetic>", "role": "assistant", "content": []obj{textBlock(text)}}
	o["isApiErrorMessage"] = true
	o["error"] = kind
	return line(o)
}

func toolResultRow(uuid string, sec float64, toolUseID, text string, opts ...opt) []byte {
	o := common("user", uuid, sec)
	o["message"] = obj{"role": "user", "content": []obj{{"type": "tool_result", "tool_use_id": toolUseID, "content": text}}}
	return line(o, opts...)
}

func turnDuration(uuid string, sec float64, durMS int, opts ...opt) []byte {
	o := common("system", uuid, sec)
	o["subtype"] = "turn_duration"
	o["durationMs"] = durMS
	o["isMeta"] = false
	return line(o, opts...)
}

func stopHookSummary(uuid string, sec float64) []byte {
	o := common("system", uuid, sec)
	o["subtype"] = "stop_hook_summary"
	o["hookCount"] = 1
	return line(o)
}

// interruptRow is the "[Request interrupted by user]" marker (f2b line 84).
func interruptRow(uuid string, sec float64, forToolUse bool) []byte {
	text := "[Request interrupted by user]"
	if forToolUse {
		text = "[Request interrupted by user for tool use]"
	}
	o := common("user", uuid, sec)
	o["message"] = obj{"role": "user", "content": []obj{textBlock(text)}}
	if !forToolUse {
		o["interruptedMessageId"] = "msg_x"
	}
	return line(o)
}

func compactBoundary(uuid string, sec float64, trigger string) []byte {
	o := common("system", uuid, sec)
	o["subtype"] = "compact_boundary"
	o["compactMetadata"] = obj{"trigger": trigger}
	return line(o)
}

func compactSummaryRow(uuid string, sec float64) []byte {
	return userRow(uuid, sec, "summary text", isMeta(), with("isCompactSummary", true), without("turnPosition"))
}

func localCommandRow(uuid string, sec float64, content string) []byte {
	o := common("system", uuid, sec)
	o["subtype"] = "local_command"
	o["content"] = content
	o["isMeta"] = true
	o["level"] = "info"
	return line(o)
}

func queuedCommand(uuid string, sec float64, prompt string, origin obj, mode string) []byte {
	o := common("attachment", uuid, sec)
	a := obj{"type": "queued_command", "prompt": prompt, "commandMode": mode}
	if origin != nil {
		a["origin"] = origin
	}
	o["attachment"] = a
	return line(o)
}

func queueOp(sec float64, op string) []byte {
	return line(obj{"type": "queue-operation", "operation": op, "timestamp": at(sec), "sessionId": sidA})
}

func aiTitle(s string) []byte {
	return line(obj{"type": "ai-title", "aiTitle": s, "sessionId": sidA})
}

func customTitle(s string) []byte {
	return line(obj{"type": "custom-title", "customTitle": s, "sessionId": sidA})
}

// feed feeds the lines with contiguous offsets from 0 and returns all
// changes; a feed error fails the test.
func feed(t testing.TB, n *Normalizer, lines ...[]byte) []Change {
	t.Helper()
	var all []Change
	for _, l := range lines {
		ch, err := n.Feed(n.Next(), l)
		if err != nil {
			t.Fatalf("Feed at %d: %v", n.Next(), err)
		}
		all = append(all, ch...)
	}
	return all
}

// norm is a fresh normalizer fed with lines.
func norm(t testing.TB, lines ...[]byte) *Normalizer {
	t.Helper()
	n := New(Options{SessionID: sidA})
	feed(t, n, lines...)
	return n
}

// conv normalizes lines (still live) and checks the result is well formed.
func conv(t testing.TB, lines ...[]byte) convmodel.Conversation {
	t.Helper()
	return validated(t, norm(t, lines...))
}

func validated(t testing.TB, n *Normalizer) convmodel.Conversation {
	t.Helper()
	c := n.Conversation()
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v\n%s", err, dump(c))
	}
	return c
}

func dump(c convmodel.Conversation) string {
	b, _ := json.MarshalIndent(c, "", " ")
	return string(b)
}

// itemsOf returns the items of turn i.
func itemsOf(t testing.TB, c convmodel.Conversation, i int) []convmodel.Item {
	t.Helper()
	if i >= len(c.Turns) {
		t.Fatalf("want turn %d, have %d turns\n%s", i, len(c.Turns), dump(c))
	}
	return c.Turns[i].Items
}

// sig is a compact description of an item for assertions.
func sig(it convmodel.Item) string {
	switch it.Type {
	case convmodel.ItemUser:
		return fmt.Sprintf("user:%s:%s", it.User.Source, it.User.Text)
	case convmodel.ItemAgentText:
		return "agent_text:" + it.AgentText.Markdown
	case convmodel.ItemThinking:
		return fmt.Sprintf("thinking:%q:%d", it.Thinking.Text, it.Thinking.DurationMS)
	case convmodel.ItemSystem:
		return fmt.Sprintf("system:%s", it.System.Kind)
	}
	return string(it.Type)
}

func sigs(items []convmodel.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = sig(it)
	}
	return out
}

func itemID(it convmodel.Item) string {
	switch it.Type {
	case convmodel.ItemUser:
		return it.User.ID
	case convmodel.ItemAgentText:
		return it.AgentText.ID
	case convmodel.ItemThinking:
		return it.Thinking.ID
	case convmodel.ItemStep:
		return it.Step.ID
	case convmodel.ItemSystem:
		return it.System.ID
	}
	return ""
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

package convmodel

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func i64(v int64) *int64 { return &v }
func intp(v int) *int    { return &v }

// sampleItems returns one fully populated item of each type.
func sampleItems() []Item {
	return []Item{
		{Type: ItemUser, User: &UserMessage{
			ID: "u1", At: 1791409527000, Text: "hi", Truncated: true, Source: SourcePeer,
			From:   &From{Kind: "peer", Name: "alice"},
			Images: []Image{{MediaType: "image/png", Bytes: 12}}, ClientMsgID: "c1",
		}},
		{Type: ItemAgentText, AgentText: &AgentText{ID: "a1", At: 2, Markdown: "# x", Truncated: true, Streaming: true}},
		{Type: ItemThinking, Thinking: &Thinking{ID: "t1", At: 3, Text: "hmm", DurationMS: 1500}},
		{Type: ItemStep, Step: &Step{
			ID: "s1", At: 4, Kind: StepEdit, Tool: "Edit", Status: StepDenied, Denial: "user-rejected",
			Summary: "a.go", StartedAt: 4, DurationMS: i64(20),
			Input:          json.RawMessage(`{"file_path":"/a/a.go"}`),
			InputTruncated: true, InputPartial: true,
			Output: &Output{Text: "ok", TotalLines: 1, TotalBytes: 2, Keep: "", Images: []Image{{MediaType: "image/png", Bytes: 3}}},
			Diff: &Diff{Path: "/a/a.go", Added: 1, Removed: 1, Exact: true, Truncated: true,
				Hunks: []Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []string{"-a", "+b"}}}},
			Command:  &Command{Text: "ls", Description: "list", ExitCode: intp(2), BackgroundTaskID: "bg1"},
			Subagent: &Subagent{AgentID: "ag1", Description: "d", Type: "Explore", Async: true},
			Children: []Item{{Type: ItemUser, User: &UserMessage{ID: "u2", At: 5, Text: "brief", Source: SourceTask}}},
		}},
		{Type: ItemSystem, System: &System{ID: "y1", At: 6, Kind: SystemCompacted, Detail: json.RawMessage(`{"trigger":"auto"}`)}},
	}
}

func TestItemJSON_RoundTripEveryType(t *testing.T) {
	for _, it := range sampleItems() {
		t.Run(string(it.Type), func(t *testing.T) {
			b, err := json.Marshal(it)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got Item
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, it) {
				t.Fatalf("round trip mismatch\n got %+v\nwant %+v\njson %s", got, it, b)
			}
		})
	}
}

func TestItemJSON_FlatShapeHasTypeDiscriminator(t *testing.T) {
	cases := []struct {
		item Item
		want string
	}{
		{Item{Type: ItemUser, User: &UserMessage{ID: "u1", At: 1, Text: "hi", Source: SourceUser}},
			`{"type":"user","id":"u1","at":1,"text":"hi","source":"user"}`},
		{Item{Type: ItemAgentText, AgentText: &AgentText{ID: "a1", At: 2, Markdown: "m"}},
			`{"type":"agent_text","id":"a1","at":2,"markdown":"m"}`},
		{Item{Type: ItemThinking, Thinking: &Thinking{ID: "t1", At: 3, DurationMS: 9}},
			`{"type":"thinking","id":"t1","at":3,"duration_ms":9}`},
		{Item{Type: ItemStep, Step: &Step{ID: "s1", At: 4, Kind: StepRead, Tool: "Read", Status: StepDone,
			Summary: "a.go", StartedAt: 4, Input: json.RawMessage(`{"file_path":"a.go"}`)}},
			`{"type":"step","id":"s1","at":4,"kind":"read","tool":"Read","status":"done","summary":"a.go","started_at":4,"input":{"file_path":"a.go"}}`},
		{Item{Type: ItemSystem, System: &System{ID: "y1", At: 6, Kind: SystemInterrupted}},
			`{"type":"system","id":"y1","at":6,"kind":"interrupted"}`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.item)
		if err != nil {
			t.Fatalf("%s: %v", c.item.Type, err)
		}
		if string(b) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.item.Type, b, c.want)
		}
	}
}

func TestItemJSON_MarshalRejectsZeroOrTwoVariants(t *testing.T) {
	u := &UserMessage{ID: "u"}
	bad := map[string]Item{
		"zero":            {Type: ItemUser},
		"empty type":      {},
		"two":             {Type: ItemUser, User: u, System: &System{ID: "y"}},
		"type mismatch":   {Type: ItemSystem, User: u},
		"unknown+variant": {Type: "future", User: u},
	}
	for name, it := range bad {
		if _, err := json.Marshal(it); err == nil {
			t.Errorf("%s: marshal succeeded, want error", name)
		}
	}
}

func TestItemJSON_UnknownTypeDecodesWithoutError(t *testing.T) {
	in := `{"type":"hologram","id":"h1","spin":3}`
	var it Item
	if err := json.Unmarshal([]byte(in), &it); err != nil {
		t.Fatalf("unknown type: %v", err)
	}
	if it.Type != "hologram" {
		t.Errorf("Type = %q, want raw string", it.Type)
	}
	if it.User != nil || it.AgentText != nil || it.Thinking != nil || it.Step != nil || it.System != nil {
		t.Errorf("variants must be nil: %+v", it)
	}
	// re-marshal keeps what was received
	b, err := json.Marshal(it)
	if err != nil || string(b) != in {
		t.Errorf("re-marshal = %s, %v; want %s", b, err, in)
	}

	// unknown enum values decode too, in a whole conversation
	doc := `{"key":{"host_id":"","provider":"claude","session_id":"s"},"provider":"claude","title":"",
	 "turns":[{"id":"t","index":0,"started_at":1,"outcome":"exploded","items":[
	  {"type":"user","id":"t","at":1,"text":"x","source":"telepathy"},
	  {"type":"step","id":"s","at":1,"kind":"teleport","tool":"X","status":"vaporized","summary":"","started_at":1,"input":{}},
	  {"type":"system","id":"y","at":1,"kind":"zoomed"}]}]}`
	var c Conversation
	if err := json.Unmarshal([]byte(doc), &c); err != nil {
		t.Fatalf("unknown enums: %v", err)
	}
	if c.Turns[0].Outcome != "exploded" || c.Turns[0].Items[0].User.Source != "telepathy" ||
		c.Turns[0].Items[1].Step.Kind != "teleport" || c.Turns[0].Items[2].System.Kind != "zoomed" {
		t.Errorf("unknown enum values not preserved: %+v", c.Turns[0])
	}
}

func TestConversationJSON_OmitsEmptyOptionals(t *testing.T) {
	c := Conversation{
		Key: Key{Provider: "claude", SessionID: "s"}, Provider: "claude",
		Usage: &Usage{Model: "m"},
		Turns: []Turn{{ID: "t", Index: 0, StartedAt: 1, Outcome: OutcomeRunning, Items: []Item{
			{Type: ItemUser, User: &UserMessage{ID: "t", At: 1, Text: "x", Source: SourceUser}},
			{Type: ItemAgentText, AgentText: &AgentText{ID: "a", At: 2, Markdown: "y"}},
			{Type: ItemStep, Step: &Step{ID: "s", At: 3, Kind: StepOther, Tool: "T", Status: StepRunning,
				Summary: "", StartedAt: 3, Input: json.RawMessage(`{}`)}},
		}}},
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"client_msg_id", "streaming", "input_partial", "children", "ended_at", "error",
		"truncated", "denial", "backend", "capabilities", "context", "rate_limits"} {
		if bytes.Contains(b, []byte(`"`+k+`"`)) {
			t.Errorf("key %q present, want omitted: %s", k, b)
		}
	}
}

func TestConversationJSON_EmptyArraysAreNotNull(t *testing.T) {
	b, err := json.Marshal(Conversation{Turns: nil})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"turns":[]`) {
		t.Errorf("nil turns = %s, want []", b)
	}
	b, _ = json.Marshal(Turn{ID: "t"})
	if !strings.Contains(string(b), `"items":[]`) {
		t.Errorf("nil items = %s, want []", b)
	}
}

func TestConversationJSON_OffsetsNotOnWire(t *testing.T) {
	c := Conversation{Turns: []Turn{{ID: "t", Offset: 4242, Items: []Item{
		{Type: ItemSystem, Offset: 7777, System: &System{ID: "y", At: 1, Kind: SystemInterrupted}},
	}}}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, bad := range []string{"4242", "7777", "ffset"} {
		if strings.Contains(s, bad) {
			t.Errorf("%q leaked onto the wire: %s", bad, s)
		}
	}
	var back Conversation
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Turns[0].Offset != 0 || back.Turns[0].Items[0].Offset != 0 {
		t.Errorf("offsets must not round-trip: %+v", back.Turns[0])
	}
}

func TestTimesAreIntegerMillis(t *testing.T) {
	end := int64(1791409552000)
	c := Conversation{Turns: []Turn{{ID: "t", StartedAt: 1791409527000, EndedAt: &end, Outcome: OutcomeDone, Items: []Item{
		{Type: ItemThinking, Thinking: &Thinking{ID: "k", At: 1791409528000, DurationMS: 1500}},
	}}}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"started_at":1791409527000`, `"ended_at":1791409552000`, `"at":1791409528000`, `"duration_ms":1500`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	// ended_at is a pointer: a zero time is still written
	zero := int64(0)
	b, _ = json.Marshal(Turn{ID: "t", EndedAt: &zero})
	if !strings.Contains(string(b), `"ended_at":0`) {
		t.Errorf("ended_at 0 dropped: %s", b)
	}
}

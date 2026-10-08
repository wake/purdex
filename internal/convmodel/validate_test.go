package convmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

// wellFormed is a valid two-turn conversation covering every item type. The
// first turn and its opening user item share the id "t1" on purpose.
func wellFormed() *Conversation {
	end := int64(20)
	return &Conversation{
		Key: Key{Provider: "claude", SessionID: "s"}, Provider: "claude",
		Turns: []Turn{
			{ID: "t1", Index: 0, StartedAt: 10, EndedAt: &end, Outcome: OutcomeDone, Items: []Item{
				{Type: ItemUser, User: &UserMessage{ID: "t1", At: 10, Text: "go", Source: SourceUser}},
				{Type: ItemThinking, Thinking: &Thinking{ID: "k1", At: 11, DurationMS: 5}},
				{Type: ItemStep, Step: &Step{ID: "s1", At: 12, Kind: StepExecute, Tool: "Bash", Status: StepDone,
					Summary: "ls", StartedAt: 12, Input: json.RawMessage(`{"command":"ls"}`),
					Output: &Output{Text: "a\nb", TotalLines: 2, TotalBytes: 3},
					Children: []Item{
						{Type: ItemUser, User: &UserMessage{ID: "c1", At: 12, Text: "brief", Source: SourceTask}},
					}}},
				{Type: ItemStep, Step: &Step{ID: "s2", At: 13, Kind: StepEdit, Tool: "Edit", Status: StepDenied,
					Denial: "user-rejected", Summary: "a.go", StartedAt: 13, Input: json.RawMessage(`{}`)}},
				{Type: ItemAgentText, AgentText: &AgentText{ID: "a1", At: 14, Markdown: "done"}},
				{Type: ItemSystem, System: &System{ID: "y1", At: 15, Kind: SystemCompacted}},
			}},
			{ID: "t2", Index: 1, StartedAt: 30, Outcome: OutcomeRunning, Items: []Item{
				{Type: ItemUser, User: &UserMessage{ID: "t2", At: 30, Text: "more", Source: SourceQueued}},
			}},
		},
	}
}

func step(c *Conversation, id string) *Step {
	for _, it := range c.Turns[0].Items {
		if it.Step != nil && it.Step.ID == id {
			return it.Step
		}
	}
	panic("no step " + id)
}

func rejects(t *testing.T, mutate func(c *Conversation), wantSub string) {
	t.Helper()
	c := wellFormed()
	mutate(c)
	err := c.Validate()
	if err == nil {
		t.Fatalf("Validate accepted a conversation that should fail with %q", wantSub)
	}
	if !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("Validate error = %q, want it to mention %q", err, wantSub)
	}
}

func TestValidate_AcceptsWellFormed(t *testing.T) {
	if err := wellFormed().Validate(); err != nil {
		t.Fatalf("well-formed conversation rejected: %v", err)
	}
	// an empty conversation is fine too
	if err := (&Conversation{}).Validate(); err != nil {
		t.Fatalf("empty conversation rejected: %v", err)
	}
}

func TestValidate_RejectsDuplicateTurnID(t *testing.T) {
	rejects(t, func(c *Conversation) { c.Turns[1].ID = "t1" }, "duplicate turn id")
}

func TestValidate_RejectsDuplicateItemID(t *testing.T) {
	rejects(t, func(c *Conversation) { c.Turns[1].Items[0].User.ID = "a1" }, "duplicate item id")
	// ids of step children count too
	rejects(t, func(c *Conversation) { step(c, "s1").Children[0].User.ID = "k1" }, "duplicate item id")
}

func TestValidate_RejectsEmptyID(t *testing.T) {
	rejects(t, func(c *Conversation) { c.Turns[0].ID = "" }, "empty turn id")
	rejects(t, func(c *Conversation) { c.Turns[0].Items[1].Thinking.ID = "" }, "empty item id")
}

func TestValidate_RejectsUnknownEnum(t *testing.T) {
	rejects(t, func(c *Conversation) { c.Turns[0].Outcome = "exploded" }, "unknown outcome")
	rejects(t, func(c *Conversation) { c.Turns[0].Items[0].User.Source = "telepathy" }, "unknown source")
	rejects(t, func(c *Conversation) { step(c, "s1").Kind = "teleport" }, "unknown step kind")
	rejects(t, func(c *Conversation) { step(c, "s1").Status = "vaporized" }, "unknown step status")
	rejects(t, func(c *Conversation) { c.Turns[0].Items[5].System.Kind = "zoomed" }, "unknown system kind")
	rejects(t, func(c *Conversation) {
		step(c, "s1").Output = &Output{Text: "a", TotalLines: 1, TotalBytes: 9, Truncated: true, Keep: "middle"}
	}, "unknown keep")
}

func TestValidate_RejectsBadItemShape(t *testing.T) {
	rejects(t, func(c *Conversation) { c.Turns[0].Items[1] = Item{Type: "future"} }, "item type")
	rejects(t, func(c *Conversation) { c.Turns[0].Items[1] = Item{Type: ItemThinking} }, "variant")
	rejects(t, func(c *Conversation) {
		c.Turns[0].Items[1].System = &System{ID: "z", Kind: SystemResumed}
	}, "variant")
}

func TestValidate_RejectsIndexMismatch(t *testing.T) {
	rejects(t, func(c *Conversation) { c.Turns[1].Index = 5 }, "index")
}

func TestValidate_RejectsEndedBeforeStarted(t *testing.T) {
	rejects(t, func(c *Conversation) { e := int64(9); c.Turns[0].EndedAt = &e }, "ended_at")
}

func TestValidate_RejectsRunningTurnNotLast(t *testing.T) {
	rejects(t, func(c *Conversation) { c.Turns[0].Outcome = OutcomeRunning }, "running")
}

func TestValidate_RejectsDeniedWithoutDenial(t *testing.T) {
	rejects(t, func(c *Conversation) { step(c, "s2").Denial = "" }, "denial")
}

func TestValidate_RejectsDenialWithoutDenied(t *testing.T) {
	rejects(t, func(c *Conversation) { step(c, "s1").Denial = "interrupted" }, "denial")
}

func TestValidate_RejectsOutputTruncatedMismatch(t *testing.T) {
	// total_bytes below the kept text
	rejects(t, func(c *Conversation) { step(c, "s1").Output.TotalBytes = 1 }, "total_bytes")
	// text shorter than the total but not flagged
	rejects(t, func(c *Conversation) { step(c, "s1").Output.TotalBytes = 99 }, "truncated")
	// flagged but nothing was cut
	rejects(t, func(c *Conversation) {
		o := step(c, "s1").Output
		o.Truncated, o.Keep = true, KeepHead
	}, "truncated")
}

func TestValidate_RejectsOutputKeepMismatch(t *testing.T) {
	// keep without truncation
	rejects(t, func(c *Conversation) { step(c, "s1").Output.Keep = KeepTail }, "keep")
	// truncation without keep
	rejects(t, func(c *Conversation) {
		o := step(c, "s1").Output
		o.TotalBytes, o.Truncated = 99, true
	}, "keep")
}

func TestValidate_AcceptsTruncatedOutputWithKeep(t *testing.T) {
	c := wellFormed()
	o := step(c, "s1").Output
	o.TotalBytes, o.Truncated, o.Keep = 99, true, KeepTail
	if err := c.Validate(); err != nil {
		t.Fatalf("truncated output with keep rejected: %v", err)
	}
}

func TestValidate_RejectsTruncatedFlagShorterThanCap(t *testing.T) {
	short := "tiny"
	rejects(t, func(c *Conversation) {
		u := c.Turns[0].Items[0].User
		u.Text, u.Truncated = short, true
	}, "user text")
	rejects(t, func(c *Conversation) {
		a := c.Turns[0].Items[4].AgentText
		a.Markdown, a.Truncated = short, true
	}, "agent_text")
	rejects(t, func(c *Conversation) {
		step(c, "s2").Diff = &Diff{Path: "a", Exact: true, Truncated: true,
			Hunks: []Hunk{{Lines: []string{"+x"}}}}
	}, "diff")
}

func TestValidate_InputTruncatedFlagAcceptsAnyCause(t *testing.T) {
	// the flag says the stored input is not the complete tool input; the
	// output alone cannot show why (a depth cut, a dropped member, a string
	// cut all leave a small object), so any object input may carry it
	for _, in := range []string{`{}`, `{"command":"ls"}`, `{"a":null}`, `{"command":"` + strings.Repeat("a", MaxInputString) + `"}`} {
		c := wellFormed()
		step(c, "s1").Input = json.RawMessage(in)
		step(c, "s1").InputTruncated = true
		if err := c.Validate(); err != nil {
			t.Errorf("input %.40s with the flag: %v", in, err)
		}
	}
}

func TestValidate_RejectsNonObjectInput(t *testing.T) {
	for _, in := range []string{``, `null`, `"s"`, `[1]`, `5`, `true`, `{`} {
		for _, flag := range []bool{false, true} {
			rejects(t, func(c *Conversation) {
				step(c, "s1").Input = json.RawMessage(in)
				step(c, "s1").InputTruncated = flag
			}, "input")
		}
	}
}

func TestValidate_RejectsUntruncatedInputOverCap(t *testing.T) {
	// not flagged => nothing in the stored input may exceed a cap
	rejects(t, func(c *Conversation) {
		step(c, "s1").Input = json.RawMessage(`{"a":{"b":["` + strings.Repeat("x", MaxInputString+1) + `"]}}`)
	}, "input")
	rejects(t, func(c *Conversation) {
		in := map[string]string{}
		for i := range 5 {
			in[strings.Repeat("k", i+1)] = strings.Repeat("v", MaxInputString)
		}
		b, _ := json.Marshal(in)
		step(c, "s1").Input = b // five strings at the string cap: over the whole cap
	}, "input")
	// exactly at the string cap is fine
	c := wellFormed()
	step(c, "s1").Input = json.RawMessage(`{"command":"` + strings.Repeat("a", MaxInputString) + `"}`)
	if err := c.Validate(); err != nil {
		t.Errorf("a string of exactly the cap: %v", err)
	}
}

func TestValidate_AcceptsTruncatedFlagAtCap(t *testing.T) {
	c := wellFormed()
	u := c.Turns[0].Items[0].User
	// cut on a UTF-8 boundary up to 3 bytes under the cap
	u.Text, u.Truncated = strings.Repeat("a", MaxText-3), true
	step(c, "s1").Input = json.RawMessage(`{"command":"` + strings.Repeat("a", MaxInputString) + `"}`)
	step(c, "s1").InputTruncated = true
	lines := make([]string, MaxDiffLines)
	step(c, "s2").Diff = &Diff{Path: "a", Exact: true, Truncated: true, Hunks: []Hunk{{Lines: lines}}}
	if err := c.Validate(); err != nil {
		t.Fatalf("truncated fields at the cap rejected: %v", err)
	}
}

func TestValidate_RejectsThinkingTruncatedShorterThanCap(t *testing.T) {
	rejects(t, func(c *Conversation) {
		th := c.Turns[0].Items[1].Thinking
		th.Text, th.Truncated = "tiny", true
	}, "thinking")
	// no text at all but flagged truncated
	rejects(t, func(c *Conversation) { c.Turns[0].Items[1].Thinking.Truncated = true }, "thinking")
	// one byte under the UTF-8 slack
	rejects(t, func(c *Conversation) {
		th := c.Turns[0].Items[1].Thinking
		th.Text, th.Truncated = strings.Repeat("a", MaxText-4), true
	}, "thinking")
}

func TestValidate_AcceptsThinkingTruncatedAtCap(t *testing.T) {
	for _, n := range []int{MaxText - 3, MaxText} {
		c := wellFormed()
		th := c.Turns[0].Items[1].Thinking
		th.Text, th.Truncated = strings.Repeat("a", n), true
		if err := c.Validate(); err != nil {
			t.Fatalf("truncated thinking of %d bytes rejected: %v", n, err)
		}
	}
}

func TestValidate_RejectsTypeVariantMismatch(t *testing.T) {
	variants := map[ItemType]func(*Item){
		ItemUser:      func(it *Item) { it.User = &UserMessage{ID: "m1", Source: SourceUser} },
		ItemAgentText: func(it *Item) { it.AgentText = &AgentText{ID: "m1"} },
		ItemThinking:  func(it *Item) { it.Thinking = &Thinking{ID: "m1"} },
		ItemStep: func(it *Item) {
			it.Step = &Step{ID: "m1", Kind: StepOther, Status: StepDone, Input: json.RawMessage(`{}`)}
		},
		ItemSystem: func(it *Item) { it.System = &System{ID: "m1", Kind: SystemResumed} },
	}
	types := []ItemType{ItemUser, ItemAgentText, ItemThinking, ItemStep, ItemSystem}

	check := func(name string, it Item) {
		t.Run(name, func(t *testing.T) {
			c := wellFormed()
			c.Turns[0].Items = []Item{it}
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Validate panicked: %v", r)
				}
			}()
			if err := c.Validate(); err == nil {
				t.Fatalf("Validate accepted %+v", it)
			}
		})
	}
	for _, typ := range types {
		check(string(typ)+"/zero", Item{Type: typ})
		for _, other := range types {
			if other == typ {
				continue
			}
			wrong := Item{Type: typ}
			variants[other](&wrong)
			check(string(typ)+"/holds-"+string(other), wrong)

			both := Item{Type: typ}
			variants[typ](&both)
			variants[other](&both)
			check(string(typ)+"/own-plus-"+string(other), both)
		}
	}
}

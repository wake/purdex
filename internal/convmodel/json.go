package convmodel

import (
	"encoding/json"
	"fmt"
)

// MarshalJSON writes turns as [] rather than null when there are none.
func (c Conversation) MarshalJSON() ([]byte, error) {
	type plain Conversation
	if c.Turns == nil {
		c.Turns = []Turn{}
	}
	return json.Marshal(plain(c))
}

// MarshalJSON writes items as [] rather than null when there are none.
func (t Turn) MarshalJSON() ([]byte, error) {
	type plain Turn
	if t.Items == nil {
		t.Items = []Item{}
	}
	return json.Marshal(plain(t))
}

// MarshalJSON writes the flat object {"type": …, <the variant's fields>}.
// A known Type needs exactly its own variant non-nil; an unknown Type with
// no variant (one that was decoded) is written back as received.
func (i Item) MarshalJSON() ([]byte, error) {
	var variant any
	var want ItemType
	n := 0
	for _, v := range []struct {
		set     bool
		typ     ItemType
		payload any
	}{
		{i.User != nil, ItemUser, i.User},
		{i.AgentText != nil, ItemAgentText, i.AgentText},
		{i.Thinking != nil, ItemThinking, i.Thinking},
		{i.Step != nil, ItemStep, i.Step},
		{i.System != nil, ItemSystem, i.System},
	} {
		if v.set {
			n++
			variant, want = v.payload, v.typ
		}
	}
	switch {
	case n == 0 && i.Type != "" && !i.Type.known():
		if len(i.raw) > 0 {
			return i.raw, nil
		}
		return json.Marshal(struct {
			Type ItemType `json:"type"`
		}{i.Type})
	case n != 1:
		return nil, fmt.Errorf("convmodel: item %q has %d variants, want exactly 1", i.Type, n)
	case want != i.Type:
		return nil, fmt.Errorf("convmodel: item type %q holds the %q variant", i.Type, want)
	}
	body, err := json.Marshal(variant)
	if err != nil {
		return nil, err
	}
	head, err := json.Marshal(i.Type)
	if err != nil {
		return nil, err
	}
	out := append([]byte(`{"type":`), head...)
	if len(body) > 2 { // a variant always has fields; guard anyway
		out = append(out, ',')
		out = append(out, body[1:]...)
		return out, nil
	}
	return append(out, '}'), nil
}

// UnmarshalJSON reads the flat form. An unknown type is not an error: the
// Item keeps the raw type string, no variant, and the received bytes.
func (i *Item) UnmarshalJSON(data []byte) error {
	var head struct {
		Type ItemType `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	*i = Item{Type: head.Type}
	var target any
	switch head.Type {
	case ItemUser:
		i.User = new(UserMessage)
		target = i.User
	case ItemAgentText:
		i.AgentText = new(AgentText)
		target = i.AgentText
	case ItemThinking:
		i.Thinking = new(Thinking)
		target = i.Thinking
	case ItemStep:
		i.Step = new(Step)
		target = i.Step
	case ItemSystem:
		i.System = new(System)
		target = i.System
	default:
		i.raw = append(json.RawMessage(nil), data...)
		return nil
	}
	return json.Unmarshal(data, target)
}

func (t ItemType) known() bool {
	switch t {
	case ItemUser, ItemAgentText, ItemThinking, ItemStep, ItemSystem:
		return true
	}
	return false
}

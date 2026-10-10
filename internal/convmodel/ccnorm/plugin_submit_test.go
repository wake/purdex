package ccnorm

import (
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

// U3-0b: a mod's $.prompt.submit({text, asUser: true}) is how the Apps send. Its transcript row carries origin
// {kind: "plugin", name, asUser: true} and promptSource / turnOrigin "system" (measured on Claude Code 2.1.296); without
// asUser the model reads the text framed ("The X plugin sent a message: …") and the row has no asUser.

func pluginRow(uuid string, sec float64, text string, asUser bool) []byte {
	o := obj{"kind": "plugin", "name": "purdex"}
	if asUser {
		o["asUser"] = true
	}
	return userRow(uuid, sec, text, with("origin", o), promptSource("system"), turnOrigin("system"))
}

// The person's words sent through the mod are a user item, so the App's local echo finds its message and the turn has a
// prompt. Mutation gate: drop the plugin case → the turn has no user item → red.
func TestPluginSubmit_AsUserIsAUserMessage(t *testing.T) {
	c := conv(t, pluginRow("u1", 1, "請幫我看一下", true), assistantText("a1", 2, "好"), turnDuration("d1", 3, 1))
	if len(c.Turns) != 1 {
		t.Fatalf("turns = %d\n%s", len(c.Turns), dump(c))
	}
	it := c.Turns[0].Items[0]
	if it.User == nil || it.User.Source != convmodel.SourceUser || it.User.Text != "請幫我看一下" || it.User.From != nil {
		t.Fatalf("first item = %+v", it)
	}
}

// A plugin's own framed prompt (the relay's write prompt, workbook-style nudges) is not the person's: still skipped and counted.
func TestPluginSubmit_FramedStaysSkipped(t *testing.T) {
	n := norm(t, userRow("u0", 0.5, "hi"), assistantText("a0", 0.7, "x"), turnDuration("d0", 0.8, 1),
		pluginRow("u1", 1, "The purdex plugin sent a message:\nwrite the handoff", false))
	if got := n.Stats().Skipped["origin:plugin"]; got != 1 {
		t.Fatalf("skipped = %v", n.Stats().Skipped)
	}
	if c := validated(t, n); len(c.Turns) != 1 {
		t.Fatalf("a framed plugin prompt opened a turn: %d", len(c.Turns))
	}
}

// asUser must be exactly true: a string or a false does not make the prompt the person's.
func TestPluginSubmit_OnlyABooleanTrue(t *testing.T) {
	for name, v := range map[string]any{"false": false, "string": "true", "zero": 0} {
		row := userRow("u1", 1, "x", with("origin", obj{"kind": "plugin", "name": "p", "asUser": v}), promptSource("system"), turnOrigin("system"))
		if n := norm(t, row); n.Stats().Skipped["origin:plugin"] != 1 {
			t.Errorf("asUser=%s was accepted: %v", name, n.Stats().Skipped)
		}
	}
}

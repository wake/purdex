package ccnorm

import (
	"strings"
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

// A plugin's own framed prompt (the relay's write prompt, nudges) is not the person's: a message from the plugin (#2396), in its
// own turn - see peer_marks_test.go for the body rules.
func TestPluginSubmit_FramedIsAMessageFromThePlugin(t *testing.T) {
	c := conv(t, userRow("u0", 0.5, "hi"), assistantText("a0", 0.7, "x"), turnDuration("d0", 0.8, 1),
		pluginRow("u1", 1, "The purdex plugin sent a message:\nwrite the handoff", false))
	if len(c.Turns) != 2 {
		t.Fatalf("turns = %d", len(c.Turns))
	}
	if u := c.Turns[1].Items[0].User; u == nil || u.Source != convmodel.SourcePeer || u.From == nil || u.From.Kind != "plugin" {
		t.Fatalf("user = %+v", u)
	}
}

// asUser must be exactly true: a string or a false does not make the prompt the person's.
func TestPluginSubmit_OnlyABooleanTrue(t *testing.T) {
	for name, v := range map[string]any{"false": false, "string": "true", "zero": 0} {
		row := userRow("u1", 1, "x", with("origin", obj{"kind": "plugin", "name": "p", "asUser": v}), promptSource("system"), turnOrigin("system"))
		u := firstUser(t, conv(t, row))
		if u.Source != convmodel.SourcePeer {
			t.Errorf("asUser=%s was taken for the person's own words: %+v", name, u)
		}
	}
}

// What $.session.append writes (Claude Code 2.1.319): isMeta, origin plugin, no promptSource / turnOrigin / turnPosition.
// The row is the agent's context only: it opens no turn and shows as no item. Mutation gate: drop the meta skip in user.go -> red.
func TestPluginAppend_IsNotATurn(t *testing.T) {
	const injected = "<injected by Purdex after compaction: not the user's words>"
	appended := func(uuid string, sec float64) []byte {
		return userRow(uuid, sec, injected, isMeta(), with("origin", obj{"kind": "plugin", "name": "purdex"}),
			without("promptSource"), without("turnOrigin"), without("turnPosition"))
	}
	between := conv(t,
		userRow("u1", 1, "first"), assistantText("a1", 2, "ok"), turnDuration("d1", 3, 1),
		appended("m1", 4),
		userRow("u2", 5, "second"), assistantText("a2", 6, "ok"), turnDuration("d2", 7, 1))
	afterCompact := conv(t,
		userRow("u1", 1, "first"), assistantText("a1", 2, "ok"), turnDuration("d1", 3, 1),
		compactBoundary("b1", 4, "manual"), compactSummaryRow("s1", 5),
		appended("m1", 6))
	for name, c := range map[string]convmodel.Conversation{"between turns": between, "after compaction": afterCompact} {
		if strings.Contains(dump(c), "injected by Purdex") {
			t.Errorf("%s: the appended text reached the conversation:\n%s", name, dump(c))
		}
	}
	if len(between.Turns) != 2 {
		t.Errorf("between turns: turns = %d, want 2\n%s", len(between.Turns), dump(between))
	}
	if n := len(afterCompact.Turns); n != 1 {
		t.Errorf("after compaction: turns = %d, want 1\n%s", n, dump(afterCompact))
	}
}

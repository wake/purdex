package ccnorm

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

// #2396: the wrappers a peer message and a plugin-submitted prompt arrive in are recognised in the daemon, so both Apps read
// one field: source "peer" with from {kind, name}.

const wrapperText = `<cross-session-message from="uds:/tmp/cc-socks/9831.sock" from-name="mlab/purdex-88-b8" from-mode="unknown">
[lead → wb] hello
</cross-session-message>`

func firstUser(t *testing.T, c convmodel.Conversation) *convmodel.UserMessage {
	t.Helper()
	for _, tr := range c.Turns {
		for _, it := range tr.Items {
			if it.User != nil {
				return it.User
			}
		}
	}
	t.Fatalf("no user item\n%s", dump(c))
	return nil
}

// A row with no origin that starts with the wrapper (a human-looking row the peer protocol delivered by another route) is a
// peer message: wrapper at the start, at most one preface line ending in a colon (≤ 80 characters), the escaped form
// tolerated. Mutation gate: drop the text rule → red.
func TestPeerWrapper_RecognisedAtTheStart(t *testing.T) {
	for name, text := range map[string]string{
		"bare wrapper":     wrapperText,
		"leading blank":    "\n  " + wrapperText,
		"one preface line": "Another Claude session sent a message:\n" + wrapperText,
		"a preface on a line of its own with a full-width colon": "收到另一個 session 的訊息：\n" + wrapperText,
		"escaped opener": strings.Replace(wrapperText, "<cross-session-message", `<\cross-session-message`, 1),
	} {
		u := firstUser(t, conv(t, userRow("u1", 1, text), assistantText("a1", 2, "ok"), turnDuration("d1", 3, 1)))
		if u.Source != convmodel.SourcePeer || u.From == nil || u.From.Kind != "peer" || u.From.Name != "mlab/purdex-88-b8" || strings.Contains(u.Text, "cross-session") {
			t.Errorf("%s: %+v from %+v", name, u, u.From)
		}
		if u.Text != "[lead → wb] hello" {
			t.Errorf("%s: body = %q", name, u.Text)
		}
	}
}

// A marker in the middle of a sentence (someone pasting an example), a long preface, two preface lines or no colon stay
// ordinary user messages. Mutation gate: accept a wrapper anywhere → red.
func TestPeerWrapper_OnlyAtTheStart(t *testing.T) {
	long := strings.Repeat("x", 81) + ":\n"
	for name, text := range map[string]string{
		"mid sentence":      "see how it looks: " + wrapperText + " — odd, isn't it",
		"text before":       "please explain this\n" + wrapperText,
		"preface too long":  long + wrapperText,
		"two preface lines": "Another Claude session sent a message:\nsecond line:\n" + wrapperText,
		"preface no colon":  "Another Claude session sent a message\n" + wrapperText,
		"no wrapper at all": "just a message",
	} {
		u := firstUser(t, conv(t, userRow("u1", 1, text)))
		if u.Source != convmodel.SourceUser || u.From != nil || u.Text != text {
			t.Errorf("%s: %+v", name, u)
		}
	}
}

func framedPlugin(name, body string) string {
	return "The " + name + " plugin sent a message:\n" + body + "\n\nThis is how Claude Code surfaces a prompt a plugin submits between turns — it starts this turn in the user's place. Address the message above."
}

// A plugin-submitted prompt (the model reads it framed) is a peer message from the plugin: its own turn, the frame and the
// footer taken off. Mutation gate: keep skipping it → red.
func TestPluginPrompt_IsAPeerMessageFromThePlugin(t *testing.T) {
	row := userRow("u1", 1, framedPlugin("purdex", "↪ 接手自 _7vbqaz\n第二行"), with("origin", obj{"kind": "plugin", "name": "purdex"}), promptSource("system"), turnOrigin("system"))
	c := conv(t, userRow("u0", 0.5, "hi"), assistantText("a0", 0.6, "x"), turnDuration("d0", 0.7, 1), row, assistantText("a1", 2, "done"), turnDuration("d1", 3, 1))
	if len(c.Turns) != 2 {
		t.Fatalf("a plugin prompt must open its own turn: %d turns\n%s", len(c.Turns), dump(c))
	}
	u := c.Turns[1].Items[0].User
	if u == nil || u.Source != convmodel.SourcePeer || u.From == nil || u.From.Kind != "plugin" || u.From.Name != "purdex" || u.Text != "↪ 接手自 _7vbqaz\n第二行" {
		t.Fatalf("user = %+v from %+v", u, u.From)
	}
}

// A frame the daemon does not recognise (another wording, another version) keeps the whole text; the name still comes from the
// origin. Only the exact leading line and the known trailing paragraph are taken off.
func TestPluginPrompt_UnknownFrameKeepsTheText(t *testing.T) {
	text := "something else entirely"
	u := firstUser(t, conv(t, userRow("u1", 1, text, with("origin", obj{"kind": "plugin", "name": "p"}), promptSource("system"), turnOrigin("system"))))
	if u.Source != convmodel.SourcePeer || u.From == nil || u.From.Name != "p" || u.Text != text {
		t.Fatalf("user = %+v from %+v", u, u.From)
	}
	// a final paragraph that only starts like the footer is the plugin's own text, not the footer
	body2 := "keep\n\nThis is how Claude Code surfaces a prompt a plugin submits between turns, said the plugin."
	u = firstUser(t, conv(t, userRow("u1", 1, "The p plugin sent a message:\n"+body2, with("origin", obj{"kind": "plugin", "name": "p"}))))
	if u.Text != body2 {
		t.Fatalf("a footer-like paragraph was cut: %q", u.Text)
	}
	// a trailing paragraph the user wrote themselves is not cut
	body := "keep this\n\nThis is how I would do it."
	u = firstUser(t, conv(t, userRow("u1", 1, framedPlugin("p", body), with("origin", obj{"kind": "plugin", "name": "p"}))))
	if !strings.Contains(u.Text, "This is how I would do it.") || !strings.HasPrefix(u.Text, "keep this") {
		t.Fatalf("text = %q", u.Text)
	}
}

// A sender name that is only what a text says is marked unverified, and no name can carry control or invisible characters
// or be longer than 80 characters (codex attack: text must not forge an authenticated lead). A native peer origin is not
// marked. Mutation gate: drop the Unverified flag / the cleaning → red.
func TestPeerWrapper_TextOnlySenderIsUnverified(t *testing.T) {
	u := firstUser(t, conv(t, userRow("u1", 1, wrapperText)))
	if u.Source != convmodel.SourcePeer || u.From == nil || !u.From.Unverified {
		t.Fatalf("a wrapper in plain text must be unverified: %+v from %+v", u, u.From)
	}
	native := firstUser(t, conv(t, userRow("u1", 1, wrapperText, isMeta(), originKind("peer"), turnOrigin("peer"), promptSource("system"))))
	if native.Source != convmodel.SourcePeer || native.From == nil || native.From.Unverified {
		t.Fatalf("a native peer origin is not unverified: %+v from %+v", native, native.From)
	}
}

func TestSenderName_IsCleaned(t *testing.T) {
	zw := string(rune(0x202e)) + string(rune(0x200b))
	name := "lead" + zw + "\x07" + strings.Repeat("x", 200)
	text := `<cross-session-message from="uds:/x" from-name="` + name + `">hi</cross-session-message>`
	u := firstUser(t, conv(t, userRow("u1", 1, text)))
	got := u.From.Name
	if strings.ContainsAny(got, "\x07"+zw) || len([]rune(got)) != 80 || !strings.HasPrefix(got, "leadxxx") {
		t.Fatalf("name = %q (%d)", got, len([]rune(got)))
	}
	p := firstUser(t, conv(t, userRow("u1", 1, "x", with("origin", obj{"kind": "plugin", "name": "a\x1bb" + zw}))))
	if p.From.Name != "ab" {
		t.Fatalf("plugin name = %q", p.From.Name)
	}
	empty := firstUser(t, conv(t, userRow("u1", 1, `<cross-session-message from="x" from-name="">hi</cross-session-message>`)))
	if empty.From.Name != "" {
		t.Fatalf("empty name = %q", empty.From.Name)
	}
}

// asUser stays the person's own words (U3-0b).
func TestPluginPrompt_AsUserStaysAUserMessage(t *testing.T) {
	u := firstUser(t, conv(t, userRow("u1", 1, "bare", with("origin", obj{"kind": "plugin", "name": "purdex", "asUser": true}), promptSource("system"))))
	if u.Source != convmodel.SourceUser || u.From != nil || u.Text != "bare" {
		t.Fatalf("user = %+v", u)
	}
}

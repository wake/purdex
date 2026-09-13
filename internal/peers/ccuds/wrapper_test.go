package ccuds

import "testing"

// TestWrapperFormat_Golden pins the byte layout measured on Claude Code
// 2.1.270 (spec §3.2): attributes in the fixed order from / from-name /
// from-mode, the text on its own line, the closing tag on the next.
func TestWrapperFormat_Golden(t *testing.T) {
	w := Wrapper{
		From:     "uds:/tmp/cc-socks/42603.sock",
		FromName: "purdex-47",
		FromMode: "bypass",
		Text:     "hello\nworld",
	}
	want := "<cross-session-message from=\"uds:/tmp/cc-socks/42603.sock\" from-name=\"purdex-47\" from-mode=\"bypass\">\nhello\nworld\n</cross-session-message>"
	if got := w.Format(); got != want {
		t.Fatalf("Format() =\n%q\nwant\n%q", got, want)
	}
}

func TestWrapperFormat_HopChainAfterFromMode(t *testing.T) {
	w := Wrapper{From: "uds:/a.sock", FromName: "n", FromMode: "prompting", HopChain: "deadbeef", Text: "t"}
	want := "<cross-session-message from=\"uds:/a.sock\" from-name=\"n\" from-mode=\"prompting\" hop-chain=\"deadbeef\">\nt\n</cross-session-message>"
	if got := w.Format(); got != want {
		t.Fatalf("Format() =\n%q\nwant\n%q", got, want)
	}
}

// TestWrapperFormat_EscapesAttributesNotText: attribute values are escaped
// for the four XML-significant characters; the text is emitted verbatim.
func TestWrapperFormat_EscapesAttributesNotText(t *testing.T) {
	w := Wrapper{From: `u"d<s>&`, FromName: `a&b`, FromMode: "bypass", Text: `<b>"raw" & unescaped</b>`}
	want := "<cross-session-message from=\"u&#34;d&lt;s&gt;&amp;\" from-name=\"a&amp;b\" from-mode=\"bypass\">\n<b>\"raw\" & unescaped</b>\n</cross-session-message>"
	if got := w.Format(); got != want {
		t.Fatalf("Format() =\n%q\nwant\n%q", got, want)
	}
}

func TestWrapperParse_RoundTripsEveryField(t *testing.T) {
	cases := []Wrapper{
		{From: "uds:/tmp/cc-socks/1.sock", FromName: "one", FromMode: "bypass", Text: "hi"},
		{From: "uds:/tmp/cc-socks/1.sock", FromName: "one", FromMode: "prompting", HopChain: "abc123", Text: "multi\nline\n\ntext"},
		{From: `q"uote&<>`, FromName: `n&m`, FromMode: "bypass", HopChain: `h"c`, Text: `<cross-session-message from="fake">nested</cross-session-message>`},
		{From: "uds:/x.sock", FromName: "", FromMode: "", Text: ""},
		{From: "uds:/x.sock", FromName: "e", FromMode: "bypass", Text: "\n"},
	}
	for i, in := range cases {
		got, ok := Parse(in.Format())
		if !ok {
			t.Fatalf("case %d: Parse(Format()) ok = false", i)
		}
		if got != in {
			t.Fatalf("case %d: round trip mismatch\n got %+v\nwant %+v", i, got, in)
		}
	}
}

func TestWrapperParse_AttributeOrderAndUnknownAttributes(t *testing.T) {
	in := "<cross-session-message hop-chain=\"ff\" from-mode=\"bypass\" x-future=\"ignored\" from-name=\"n&amp;m\" from=\"uds:/s.sock\">\nbody\n</cross-session-message>"
	got, ok := Parse(in)
	if !ok {
		t.Fatal("Parse ok = false")
	}
	want := Wrapper{From: "uds:/s.sock", FromName: "n&m", FromMode: "bypass", HopChain: "ff", Text: "body"}
	if got != want {
		t.Fatalf("Parse =\n%+v\nwant\n%+v", got, want)
	}
}

func TestWrapperParse_Rejects(t *testing.T) {
	cases := map[string]string{
		"plain text":         "just some text",
		"empty":              "",
		"open only":          "<cross-session-message from=\"a\">\nx",
		"close only":         "x\n</cross-session-message>",
		"wrong tag":          "<cross-session-messages from=\"a\">\nx\n</cross-session-message>",
		"unterminated open":  "<cross-session-message from=\"a\"\nx\n</cross-session-message>",
		"leading whitespace": " <cross-session-message from=\"a\">\nx\n</cross-session-message>",
	}
	for name, in := range cases {
		if w, ok := Parse(in); ok {
			t.Errorf("%s: Parse ok = true, got %+v", name, w)
		}
	}
}

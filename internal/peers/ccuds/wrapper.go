// Package ccuds isolates every piece of Purdex that touches Claude Code's
// undocumented peer protocol (measured on 2.1.270, spec §3): the
// cross-session-message wrapper, the NDJSON frame and its socket write,
// the registry files a virtual peer must create, and the VirtualPeer that
// binds a socket and registers itself. Nothing outside this package should
// know the byte layout of any of these.
package ccuds

import (
	"html"
	"regexp"
	"strings"
)

const (
	wrapperOpen  = "<cross-session-message"
	wrapperClose = "</cross-session-message>"
)

// Wrapper is the cross-session-message envelope the harness puts around a
// peer message's text (spec §3.2). HopChain is the loop-detection token the
// harness adds to replies; a relay carries it through unchanged.
type Wrapper struct {
	From     string // "uds:<sock>" reply address
	FromName string
	FromMode string // "bypass" | "prompting"
	HopChain string
	Text     string
}

// Format renders exactly
//
//	<cross-session-message from="…" from-name="…" from-mode="…"[ hop-chain="…"]>\n<text>\n</cross-session-message>
//
// Attribute values are escaped with html.EscapeString; the text is emitted
// verbatim. hop-chain is present only when non-empty.
func (w Wrapper) Format() string {
	var b strings.Builder
	b.WriteString(wrapperOpen)
	b.WriteString(` from="`)
	b.WriteString(html.EscapeString(w.From))
	b.WriteString(`" from-name="`)
	b.WriteString(html.EscapeString(w.FromName))
	b.WriteString(`" from-mode="`)
	b.WriteString(html.EscapeString(w.FromMode))
	b.WriteString(`"`)
	if w.HopChain != "" {
		b.WriteString(` hop-chain="`)
		b.WriteString(html.EscapeString(w.HopChain))
		b.WriteString(`"`)
	}
	b.WriteString(">\n")
	b.WriteString(w.Text)
	b.WriteString("\n")
	b.WriteString(wrapperClose)
	return b.String()
}

// attrPattern matches one name="value" attribute inside the opening tag.
// Values are html-escaped by Format so a '"' never appears inside one.
var attrPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9_-]*)="([^"]*)"`)

// Parse is the inverse of Format. ok is false unless content starts with
// the opening tag and ends with the closing tag. Attributes may appear in
// any order; unknown ones are ignored; values are html-unescaped. Text is
// everything between the opening tag's trailing "\n" and the closing tag's
// leading "\n" (a single missing newline on either side is tolerated).
func Parse(content string) (Wrapper, bool) {
	if !strings.HasPrefix(content, wrapperOpen) || !strings.HasSuffix(content, wrapperClose) {
		return Wrapper{}, false
	}
	rest := content[len(wrapperOpen):]
	if rest == "" || (rest[0] != ' ' && rest[0] != '>') {
		return Wrapper{}, false // a longer tag name, e.g. <cross-session-messages>
	}
	gt := strings.IndexByte(rest, '>')
	if gt < 0 {
		return Wrapper{}, false
	}
	body := rest[gt+1:]
	if len(body) < len(wrapperClose) {
		return Wrapper{}, false // the '>' found was inside the closing tag
	}
	body = body[:len(body)-len(wrapperClose)]

	var w Wrapper
	for _, m := range attrPattern.FindAllStringSubmatch(rest[:gt], -1) {
		v := html.UnescapeString(m[2])
		switch m[1] {
		case "from":
			w.From = v
		case "from-name":
			w.FromName = v
		case "from-mode":
			w.FromMode = v
		case "hop-chain":
			w.HopChain = v
		}
	}

	// Format emits ">\n" + text + "\n</…>"; with empty text the two
	// newlines are distinct ("\n\n"), so strip one from each side.
	body = strings.TrimPrefix(body, "\n")
	body = strings.TrimSuffix(body, "\n")
	w.Text = body
	return w, true
}

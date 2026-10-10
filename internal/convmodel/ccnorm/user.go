package ccnorm

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convmodel"
)

// The user-row rules are ported from the Nexen prelude classifier
// (lab.protype.tw/wake/nexen@v0.20.0 prelude/classify.go): the origin gate
// before any tag (:286-365), the slash rewrite, persisted-output unwrapping
// and the task summary (:411-512), queued_command attachments (:516-545).
// The target differs: prelude emits stream-json envelopes and notes, this
// package emits convmodel items, and a row that prelude shows as a note
// (bash output, command output) becomes a system command_output item here.

// sourceKind is what produced a prompt row: origin.kind when the row has an
// origin, else turnOrigin, else promptSource, with the two spellings of
// task-notification and auto-continuation made one. "" is a row with none of
// the three (an old row, typed by the user). An origin that is present but
// has no string kind is "invalid" — not "typed by the user"
// (prelude/classify.go:806-820).
func sourceKind(l *rawLine) string {
	if len(l.Origin) > 0 {
		o, ok := parseObject(l.Origin)
		if k := o.str("kind"); ok && k != "" {
			return normKind(k)
		}
		return "invalid"
	}
	if k := l.str(l.TurnOrigin); k != "" {
		return normKind(k)
	}
	return normKind(l.str(l.PromptSource))
}

// leadingPeerWrapper: the text starts with the peer wrapper - after white space and at most one preface line that ends in a
// colon and is at most 80 characters ("Another Claude session sent a message:") - in its plain or backslash-escaped form. A
// wrapper in the middle of a sentence, or after more text, is just something the person wrote.
func leadingPeerWrapper(text string) bool {
	t := strings.TrimLeftFunc(text, unicode.IsSpace)
	opens := func(s string) bool {
		return strings.HasPrefix(s, peerOpen) || strings.HasPrefix(s, `<\cross-session-message`)
	}
	if opens(t) {
		return true
	}
	line, rest, ok := strings.Cut(t, "\n")
	if !ok || utf8.RuneCountInString(line) > 80 {
		return false
	}
	line = strings.TrimRightFunc(line, unicode.IsSpace)
	if !strings.HasSuffix(line, ":") && !strings.HasSuffix(line, "：") {
		return false
	}
	return opens(strings.TrimLeftFunc(rest, unicode.IsSpace))
}

// pluginFooter is the paragraph Claude Code 2.1.296 appends to a framed plugin prompt. It is matched whole: a text that
// does not end in exactly this keeps its tail (another version may word it differently - then it stays in the message).
const pluginFooter = "This is how Claude Code surfaces a prompt a plugin submits between turns — it starts this turn in the user's place. Address the message above."

// pluginBody takes the frame off a plugin's prompt: the first line "The <name> plugin sent a message:" and the footer
// paragraph. A text in another shape is returned whole.
func pluginBody(text, name string) string {
	first, rest, ok := strings.Cut(text, "\n")
	if !ok || first != "The "+name+" plugin sent a message:" {
		return text
	}
	return strings.TrimSuffix(rest, "\n\n"+pluginFooter)
}

// pluginAsUser: the row's origin says a plugin submitted the text as the person's own (origin.asUser).
func pluginAsUser(l *rawLine) bool {
	o, ok := parseObject(l.Origin)
	return ok && jsonTrue(o.get("asUser"))
}

func normKind(k string) string { return strings.ReplaceAll(k, "_", "-") }

// humanKind reports whether a row of this kind was typed by a person (or
// sent through the SDK on their behalf): the default branch of the source
// table.
func humanKind(k string) bool {
	switch k {
	case "", "human", "sdk", "typed", "queued", "suggestion-accepted":
		return true
	}
	return false
}

// userRow classifies one `user` row. Order: compact summary and meta rows
// out, tool results (steps, U1-4c) set aside, the interrupt marker, then the
// origin gate, and only after it the tags of a human-typed text.
func (n *Normalizer) userRow(l *rawLine, off int64) {
	msg, _ := parseObject(l.Message)
	blocks, ok := contentBlocks(msg.get("content"))
	if !ok || len(blocks) == 0 {
		n.skip("content")
		return
	}
	blocks = n.capBlocks(blocks)
	if l.compactSummary {
		n.compactSummary(blocks, off)
		return
	}
	kind := sourceKind(l)
	// A meta row is skipped unless it is a peer message or a scheduled
	// wake-up (the census shows scheduled rows are isMeta with turnPosition;
	// the source table needs them reachable).
	if l.meta && kind != "peer" && kind != "scheduled" {
		n.skip("meta")
		return
	}
	if hasToolResult(blocks) {
		n.toolResultRow(l, blocks, off) // never a prompt, never opens a turn
		return
	}
	if isInterruptMarker(l, blocks) {
		n.interruptMarker(l, off)
		return
	}

	text := joinText(blocks)
	var (
		src  convmodel.Source
		from *convmodel.From
	)
	switch {
	case kind == "peer":
		body, name := peerBody(text)
		if name == "" {
			o, _ := parseObject(l.Origin)
			name = o.str("name")
		}
		src, text, from = convmodel.SourcePeer, body, &convmodel.From{Kind: "peer", Name: name}
	case kind == "task-notification":
		src, text = convmodel.SourceTask, taskText(text)
	case kind == "scheduled":
		src = convmodel.SourceScheduled
	case kind == "plugin" && pluginAsUser(l):
		// a mod's $.prompt.submit({text, asUser: true}): the person's own words sent on their behalf (U3-0b: the Apps'
		// submit goes this way). The text is bare.
		src = convmodel.SourceUser
	case kind == "plugin":
		// a plugin's own prompt, which the model reads framed ("The X plugin sent a message: …"): a message from the
		// plugin, not the person's (#2396). It opens its own turn; the frame and the footer are not part of the message.
		o, _ := parseObject(l.Origin)
		name := o.str("name")
		src, text, from = convmodel.SourcePeer, pluginBody(text, name), &convmodel.From{Kind: "plugin", Name: name}
	case humanKind(kind) && leadingPeerWrapper(text):
		// no peer origin on the row, but the text opens with the peer wrapper (#2396)
		body, name := peerBody(strings.Replace(text, `<\cross-session-message`, peerOpen, 1))
		src, text, from = convmodel.SourcePeer, body, &convmodel.From{Kind: "peer", Name: name}
	case humanKind(kind):
		var handled bool
		src, text, handled = n.humanTags(l, off, text)
		if handled {
			return
		}
	default:
		n.skipDyn("origin:" + kind)
		return
	}

	ti, ok := n.openTurn(l.uuid, l.at, off)
	if !ok {
		return
	}
	if n.sub && !n.briefDone {
		// a subagent's first prompt is the brief its parent gave it (ruling D7)
		n.briefDone = true
		src, from = convmodel.SourceTask, nil
	}
	n.addUser(ti, l.uuid, l.at, src, from, text, blocks, off)
	n.handoff(ti, l.str(l.Entrypoint), l.at, off)
	n.attribute(ti, l.at)
}

// humanTags reads the tags of a human-typed text. handled is true when the
// row was fully dealt with here (a continuation row, or one that is skipped);
// otherwise the returned source and text open a turn.
func (n *Normalizer) humanTags(l *rawLine, off int64, text string) (src convmodel.Source, out string, handled bool) {
	switch tag := firstTag(text); {
	case isSlashCommand(text):
		return convmodel.SourceSlash, slashText(text), false
	case tag == "bash-input":
		v, _ := tagValue(text, tag)
		return convmodel.SourceBash, v, false
	case tag == "bash-stdout" || tag == "bash-stderr":
		n.commandOutput(l, off, bashOutput(text), false)
		return "", "", true
	case tag == "local-command-stdout":
		v, _ := tagValue(text, tag)
		n.commandOutput(l, off, v, true)
		return "", "", true
	case tag == "local-command-caveat":
		n.skip("caveat")
		return "", "", true
	}
	if l.str(l.PromptSource) == "queued" {
		return convmodel.SourceQueued, text, false
	}
	return convmodel.SourceUser, text, false
}

// addUser adds a user item built from a row's content blocks.
func (n *Normalizer) addUser(ti int, id string, at int64, src convmodel.Source, from *convmodel.From, text string, blocks []block, off int64) {
	text, cut := capText(text, convmodel.MaxText)
	u := &convmodel.UserMessage{ID: id, At: at, Text: text, Truncated: cut, Source: src, From: from}
	for _, b := range blocks {
		if b.typ == "image" {
			mt, size := imageSize(b)
			u.Images = append(u.Images, convmodel.Image{MediaType: mt, Bytes: size})
		}
	}
	n.upsert(n.turns[ti].t.ID, convmodel.Item{Type: convmodel.ItemUser, User: u}, off)
}

// commandOutput adds a command_output system item to the current turn (a
// bash-mode turn's output, a local command's stdout). An empty output adds
// nothing. localCmd marks a turn without model work.
func (n *Normalizer) commandOutput(l *rawLine, off int64, text string, localCmd bool) {
	ti := n.ensureTurn(l.uuid, l.at, off)
	tr := n.turns[ti]
	if localCmd && !tr.hasModel {
		tr.modelFree = true
	}
	n.attribute(ti, l.at)
	if strings.TrimSpace(text) == "" {
		return
	}
	text, cut := capText(text, convmodel.MaxText)
	n.upsert(tr.t.ID, convmodel.Item{Type: convmodel.ItemSystem, System: &convmodel.System{
		ID: l.uuid, At: l.at, Kind: convmodel.SystemCommandOutput, Detail: textDetail(text, cut),
	}}, off)
}

// textDetail is the detail of a command_output item: {"text": …}, with
// "truncated": true when the text was cut.
func textDetail(text string, truncated bool) json.RawMessage {
	d := struct {
		Text      string `json:"text"`
		Truncated bool   `json:"truncated,omitempty"`
	}{text, truncated}
	return marshalNoEscape(d)
}

// attachmentRow keeps one attachment type, queued_command: a prompt sent
// while Claude was busy exists only as this attachment, so dropping it would
// drop something the person said. It is an item inside the running turn and
// never opens one (prelude/classify.go:516-545).
func (n *Normalizer) attachmentRow(l *rawLine, off int64) {
	a, ok := parseObject(l.Attachment)
	if !ok {
		n.skip("attachment:invalid")
		return
	}
	if typ := a.str("type"); typ != "queued_command" {
		n.skipDyn("attachment:" + typ)
		return
	}
	blocks, ok := contentBlocks(a.get("prompt"))
	if !ok || len(blocks) == 0 {
		n.skip("content")
		return
	}
	blocks = n.capBlocks(blocks)
	mode := a.str("commandMode")
	var kind string
	if len(a.get("origin")) > 0 {
		o, ok := parseObject(a.get("origin"))
		if kind = normKind(o.str("kind")); !ok || kind == "" {
			kind = "invalid"
		}
	} else if mode == "task-notification" {
		kind = "task-notification"
	} else {
		kind = "human"
	}

	text := joinText(blocks)
	var (
		src  convmodel.Source
		from *convmodel.From
	)
	switch kind {
	case "human":
		if mode != "" && mode != "prompt" {
			n.skipDyn("attachment:queued_command:" + mode)
			return
		}
		src = convmodel.SourceQueued
	case "peer":
		body, name := peerBody(text)
		if name == "" {
			o, _ := parseObject(a.get("origin"))
			name = o.str("name")
		}
		src, text, from = convmodel.SourcePeer, body, &convmodel.From{Kind: "peer", Name: name}
	case "task-notification":
		src, text = convmodel.SourceTask, taskText(text)
	default:
		n.skipDyn("origin:" + kind)
		return
	}
	ti := n.ensureTurn(l.uuid, l.at, off)
	n.addUser(ti, l.uuid, l.at, src, from, text, blocks, off)
	n.attribute(ti, l.at)
}

// isInterruptMarker: the "[Request interrupted by user…" text row Claude
// Code writes when the person presses Esc, or any row with an
// interruptedMessageId.
func isInterruptMarker(l *rawLine, blocks []block) bool {
	if l.marker {
		return true
	}
	return len(blocks) > 0 && blocks[0].typ == "text" && strings.HasPrefix(blocks[0].text, "[Request interrupted by user")
}

// ---- tags and text helpers, ported from prelude/classify.go ---------------

// capText keeps the head of s up to max bytes, cut on a UTF-8 boundary.
func capText(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// firstTag is the name of the tag text opens with, after leading whitespace;
// "" when it does not open with a bare tag (prelude/classify.go:761).
func firstTag(text string) string {
	t := strings.TrimLeftFunc(text, unicode.IsSpace)
	if len(t) < 3 || t[0] != '<' {
		return ""
	}
	for i := 1; i < len(t); i++ {
		c := t[i]
		switch {
		case c == '>':
			if i == 1 {
				return ""
			}
			return t[1:i]
		case c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		default:
			return ""
		}
	}
	return ""
}

// tagValue is the text between <name> and </name>; without a closing tag it
// runs to the end. ok is false when <name> does not occur
// (prelude/classify.go:784).
func tagValue(text, name string) (string, bool) {
	open := "<" + name + ">"
	i := strings.Index(text, open)
	if i < 0 {
		return "", false
	}
	rest := text[i+len(open):]
	if j := strings.Index(rest, "</"+name+">"); j >= 0 {
		return rest[:j], true
	}
	return rest, true
}

// tagClosed: the first <name> in text has a </name> somewhere after it.
func tagClosed(text, name string) bool {
	open := "<" + name + ">"
	i := strings.Index(text, open)
	return i >= 0 && strings.Contains(text[i+len(open):], "</"+name+">")
}

// isSlashCommand: text opens with one of the command tags and carries
// <command-name>. A lone <command-message> is not a command.
func isSlashCommand(text string) bool {
	switch firstTag(text) {
	case "command-name", "command-message", "command-args":
		return strings.Contains(text, "<command-name>")
	}
	return false
}

// slashText rewrites a slash-command row to what the person typed: the
// <command-name> value (it already starts with '/'), plus a space and
// <command-args> when that is not empty. Each tag is read where it stands;
// Claude Code does not always write them in the same order.
func slashText(text string) string {
	name, _ := tagValue(text, "command-name")
	args, _ := tagValue(text, "command-args")
	name, args = strings.TrimSpace(name), strings.TrimSpace(args)
	if args == "" {
		return name
	}
	return name + " " + args
}

// bashOutput is the stdout then the stderr of a <bash-stdout> row, each
// unwrapped from <persisted-output> and joined by a newline; an empty stream
// is left out.
func bashOutput(text string) string {
	var parts []string
	for _, tag := range [...]string{"bash-stdout", "bash-stderr"} {
		v, ok := tagValue(text, tag)
		if !ok {
			continue
		}
		if v = unwrapPersisted(v); strings.TrimSpace(v) != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "\n")
}

// unwrapPersisted: when an output is too large Claude Code saves it to a file
// and writes, inside the stream, a <persisted-output> block holding a notice
// and a preview. A stream that is that block whole is unwrapped, one layer;
// anything else comes back unchanged (prelude/classify.go:473-481).
func unwrapPersisted(s string) string {
	const open, closing = "<persisted-output>", "</persisted-output>"
	t := strings.TrimFunc(s, unicode.IsSpace)
	if len(t) < len(open)+len(closing) || !strings.HasPrefix(t, open) || !strings.HasSuffix(t, closing) {
		return s
	}
	inner := strings.TrimPrefix(t[len(open):len(t)-len(closing)], "\n")
	return strings.TrimSuffix(inner, "\n")
}

var anyTag = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9_-]*>`)

// taskText is a task notification's one-liner: the <summary> when there is
// one, else the text with every bare tag stripped (prelude/classify.go:493).
func taskText(text string) string {
	if v, ok := tagValue(text, "summary"); ok {
		return strings.TrimSpace(html.UnescapeString(v)) // the harness HTML-escapes it (&amp; &gt;)
	}
	return strings.TrimSpace(anyTag.ReplaceAllString(text, ""))
}

var fromNameAttr = regexp.MustCompile(`(?:^|\s)from-name="([^"]*)"`)

const peerOpen, peerClose = "<cross-session-message", "</cross-session-message>"

// peerBody is the message body of a peer message and the sender's name: the
// <cross-session-message from-name="…"> wrapper (internal/peers/ccuds)
// removed, one newline inside each end dropped, the attribute unescaped. A
// text without the wrapper is returned as is, with no name.
func peerBody(text string) (body, name string) {
	i := strings.Index(text, peerOpen)
	if i < 0 {
		return text, ""
	}
	j := strings.IndexByte(text[i:], '>')
	if j < 0 {
		return text, ""
	}
	j += i
	if m := fromNameAttr.FindStringSubmatch(text[i+len(peerOpen) : j]); m != nil {
		name = html.UnescapeString(m[1])
	}
	body = text[j+1:]
	if k := strings.LastIndex(body, peerClose); k >= 0 {
		body = body[:k]
	}
	body = strings.TrimSuffix(strings.TrimPrefix(body, "\n"), "\n")
	return body, name
}

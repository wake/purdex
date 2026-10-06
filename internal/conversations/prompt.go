package conversations

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// promptMaxBytes is the most of a first human prompt that is kept.
const promptMaxBytes = 500

// humanPrompt returns the text of l when l is a human prompt (§13.1 as
// amended, R-4-13): a user line that is not meta, sidechain or compact
// summary, whose message.content is a string, or an array with no
// tool_result block (its first text block's text), and whose text after
// TrimSpace is non-empty and does not start with '<' (command wrappers,
// task notifications, cross-session messages). The text is trimmed and cut
// to promptMaxBytes on a UTF-8 boundary.
func humanPrompt(l *headLine) (string, bool) {
	if l.Type != "user" || l.IsMeta || l.IsSidechain || l.IsCompactSummary {
		return "", false
	}
	text, ok := contentText(l.Message.Content)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" || text[0] == '<' {
		return "", false
	}
	return cutUTF8(text, promptMaxBytes), true
}

// contentText is a user message's text: the string itself, or the first
// text block of an array with no tool_result block.
func contentText(content json.RawMessage) (string, bool) {
	c := bytes.TrimLeft(content, " \t\r\n")
	if len(c) == 0 {
		return "", false
	}
	switch c[0] {
	case '"':
		var s string
		if json.Unmarshal(c, &s) != nil {
			return "", false
		}
		return s, true
	case '[':
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(c, &blocks) != nil {
			return "", false
		}
		text, found := "", false
		for _, b := range blocks {
			if b.Type == "tool_result" {
				return "", false
			}
			if !found && b.Type == "text" {
				text, found = b.Text, true
			}
		}
		return text, found
	}
	return "", false
}

// cutUTF8 cuts s to at most limit bytes without splitting a rune.
func cutUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	i := limit
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

package teammod

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// answersFit is the daemon's copy of the rule in the mod's answersFit (cmd/pdx/plugin/purdex/hooks/ask.js): a remote answer closes
// the terminal's dialog only if it answers exactly the questions asked — one non-empty string per question text and no other key.
// Checked at decide (#1845) so the phone is told at once; before, the daemon approved what the mod then refused, and the card
// showed an answer the terminal never took. cmd/pdx/plugin/purdex/hooks/ask_answers.fixture.js holds the cases both sides must agree on.
//
// questions is the row's payload `questions` verbatim. why is "" when the answers fit, else a short reason naming the question or
// key (clipped), safe to hand back to the client.
func answersFit(questions json.RawMessage, answers map[string]string) (fit bool, why string) {
	var qs []json.RawMessage
	if err := json.Unmarshal(questions, &qs); err != nil || len(qs) == 0 {
		return false, "this request has no questions to answer"
	}
	asked := make(map[string]bool, len(qs))
	for i, raw := range qs {
		var q struct {
			Question *string `json:"question"`
		}
		if err := json.Unmarshal(raw, &q); err != nil || q.Question == nil || *q.Question == "" {
			return false, fmt.Sprintf("question %d has no text, so it cannot be answered remotely", i+1)
		}
		asked[*q.Question] = true
	}
	for q := range asked {
		if _, ok := answers[q]; !ok {
			return false, fmt.Sprintf("hook.answers has no answer for the question %q", clip(q))
		}
	}
	for k, v := range answers {
		if !asked[k] {
			return false, fmt.Sprintf("hook.answers has a key that is not a question: %q", clip(k))
		}
		if strings.TrimFunc(v, isECMAWhitespace) == "" {
			return false, fmt.Sprintf("hook.answers has an empty answer for the question %q", clip(k))
		}
	}
	return true, ""
}

// isECMAWhitespace is what JavaScript's String.prototype.trim removes (WhiteSpace and LineTerminator of ECMAScript), which is not
// what Go's unicode.IsSpace says: U+FEFF counts here and U+0085 does not. The mod trims with JavaScript's, so the daemon must too.
func isECMAWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// clip shortens s for an error detail.
func clip(s string) string {
	const max = 80
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

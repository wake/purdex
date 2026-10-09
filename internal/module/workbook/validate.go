package workbook

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/wake/purdex/internal/redact"
)

// Limits of spec §5.4, in characters (runes, not bytes: the text is Chinese).
const (
	maxThing  = 16
	maxPush   = 40
	maxEntry  = 150
	maxStatus = 200
)

// ErrFormat: the model's answer is not the JSON object of spec §5.3 (after one fence is stripped).
var ErrFormat = errors.New("workbook: the answer is not the expected JSON")

// Summary is the model's answer.
type Summary struct {
	Skip      bool   `json:"skip"`
	Thing     string `json:"thing"`
	Push      string `json:"push"`
	Entry     string `json:"entry"`
	Status    string `json:"status"`
	ThingDone bool   `json:"thing_done"`
}

// ParseModelJSON reads the model's text as a Summary. One code fence around the object is allowed; anything else around
// it, a non-object, a field of the wrong type, or (unless it skips) an empty thing or entry is ErrFormat.
func ParseModelJSON(text string) (Summary, error) {
	body := stripFence(strings.TrimSpace(text))
	dec := json.NewDecoder(strings.NewReader(body))
	var s Summary
	if err := dec.Decode(&s); err != nil || dec.More() {
		return Summary{}, ErrFormat
	}
	if _, err := dec.Token(); err == nil { // nothing may follow the object
		return Summary{}, ErrFormat
	}
	if !s.Skip && (strings.TrimSpace(s.Thing) == "" || strings.TrimSpace(s.Entry) == "") {
		return Summary{}, ErrFormat
	}
	return s, nil
}

// stripFence removes one ```[lang] ... ``` wrapper. A text with two fences is left as it is (and so fails to parse).
func stripFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	rest := s[3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		return s
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasSuffix(rest, "```") {
		return s
	}
	rest = strings.TrimSpace(strings.TrimSuffix(rest, "```"))
	if strings.Contains(rest, "```") {
		return s
	}
	return rest
}

// Repair applies spec §5.4 to what the model said, in code: every string is redacted; thing is cut to 16; push over 40
// is cut at its last punctuation within 40, or dropped when there is none; status over 200 is cut at its last sentence
// end within 200. An entry over 150 is left whole and rewrite is true: the caller asks the model once more
// (RewritePrompt) and then applies CutEntry to whatever comes back.
func Repair(s Summary) (out Summary, rewrite bool) {
	out = s
	out.Thing = cutRunes(strings.TrimSpace(redact.String(s.Thing)), maxThing)
	out.Push = repairPush(strings.TrimSpace(redact.String(s.Push)))
	out.Entry = strings.TrimSpace(redact.String(s.Entry))
	out.Status = cutAtSentenceEnd(strings.TrimSpace(redact.String(s.Status)), maxStatus)
	return out, utf8.RuneCountInString(out.Entry) > maxEntry
}

// CutEntry is the last resort for an entry that is still over 150 after the rewrite: cut at the last sentence end within
// 150 (hard cut when there is none).
func CutEntry(s string) string {
	return cutAtSentenceEnd(strings.TrimSpace(redact.String(s)), maxEntry)
}

func repairPush(p string) string {
	if utf8.RuneCountInString(p) <= maxPush {
		return p
	}
	head := []rune(p)[:maxPush]
	for i := len(head) - 1; i >= 0; i-- {
		if isPunct(head[i]) {
			return strings.TrimSpace(string(head[:i]))
		}
	}
	return ""
}

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// cutAtSentenceEnd keeps s whole within n runes; otherwise it keeps up to and including the last sentence end within n,
// or the first n runes when there is none.
func cutAtSentenceEnd(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	head := r[:n]
	for i := len(head) - 1; i >= 0; i-- {
		if isSentenceEnd(head[i]) {
			return string(head[:i+1])
		}
	}
	return string(head)
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '。', '！', '？', '!', '?', '.', '\n':
		return true
	}
	return false
}

func isPunct(r rune) bool {
	if isSentenceEnd(r) {
		return true
	}
	switch r {
	case '，', '、', '；', '：', ',', ';', ':':
		return true
	}
	return false
}

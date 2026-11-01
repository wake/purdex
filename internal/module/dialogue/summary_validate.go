package dialogue

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/redact"
)

// Limits of a turn summary, in characters (runes, not bytes: the text is Chinese).
const (
	maxTitle   = 16
	maxPush    = 40
	maxSummary = 150

	// maxModelJSON bounds what is parsed (the object is a few hundred bytes).
	maxModelJSON = 64 << 10
)

// summaryOut is the model's answer to a turn-summary job.
type summaryOut struct {
	Skip    bool   `json:"skip"`
	Title   string `json:"title"`
	Push    string `json:"push"`
	Summary string `json:"summary"`
}

func formatErr(why string) error { return fmt.Errorf("%w: %s", errFormat, why) }

// parseSummary reads the model's text as a summaryOut: one JSON object (one code fence around it is allowed), the four
// fields all present and of the right type, no repeated or extra field, nothing before or after. Unless it skips, title
// and summary must not be empty. Anything else is errFormat.
func parseSummary(text string) (summaryOut, error) {
	if len(text) > maxModelJSON {
		return summaryOut{}, formatErr("too long")
	}
	body := stripFence(strings.TrimSpace(text))
	if hasDuplicateMember(body) {
		return summaryOut{}, formatErr("repeated field")
	}
	var members map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &members) != nil {
		return summaryOut{}, formatErr("not a JSON object")
	}
	for _, k := range []string{"skip", "title", "push", "summary"} {
		if _, ok := members[k]; !ok {
			return summaryOut{}, formatErr("missing " + k)
		}
	}
	if len(members) != 4 {
		return summaryOut{}, formatErr("extra field")
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	var s summaryOut
	if err := dec.Decode(&s); err != nil || dec.More() {
		return summaryOut{}, formatErr("wrong field type")
	}
	if !s.Skip && (strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Summary) == "") {
		return summaryOut{}, formatErr("empty title or summary")
	}
	return s, nil
}

// repairSummary applies spec §3 in code: every field is redacted, then cut to its limit at a word boundary. title is
// cut to 16 (no ellipsis); push over 40 is cut at its last punctuation or dropped when there is none; summary over 150
// is cut at the last sentence end within 150, or at a word with an ellipsis when there is none.
func repairSummary(o summaryOut) summaryOut {
	o.Title = cutAtWord(strings.TrimSpace(redact.String(o.Title)), maxTitle)
	o.Push = repairPush(strings.TrimSpace(redact.String(o.Push)))
	o.Summary = repairSummaryText(strings.TrimSpace(redact.String(o.Summary)))
	return o
}

func repairSummaryText(s string) string {
	r := []rune(s)
	if len(r) <= maxSummary {
		return s
	}
	for i := maxSummary - 1; i >= 0; i-- {
		if isSentenceEndAt(r, i) {
			return strings.TrimSpace(string(r[:i+1]))
		}
	}
	return cutAtWord(s, maxSummary) + "…"
}

// isWordRune: the characters of a run that must not be cut in the middle (a word, a path, a version, a PR number).
// CJK characters are not in it: between two of them is always a boundary.
func isWordRune(c rune) bool {
	return c < unicode.MaxASCII && (c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '.' || c == '/' || c == '-')
}

// cutAtWord keeps the first n runes of s. When the cut falls inside a run of ASCII letters, digits, '_', '.', '/' or '-',
// it backs off to the start of that run; when that leaves fewer than n/2 runes it cuts at n instead. Trailing space is
// trimmed.
func cutAtWord(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := n
	if isWordRune(r[n-1]) && isWordRune(r[n]) {
		start := n
		for start > 0 && isWordRune(r[start-1]) {
			start--
		}
		if start >= n/2 {
			cut = start
		}
	}
	return strings.TrimRight(string(r[:cut]), " \t\n")
}

// hasDuplicateMember reports whether the top-level object repeats a member name. The answer's values are all scalars,
// so a flat token walk is enough; anything that does not walk cleanly is left to the real decode to refuse.
func hasDuplicateMember(body string) bool {
	dec := json.NewDecoder(strings.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for dec.More() {
		k, err := dec.Token()
		key, ok := k.(string)
		if err != nil || !ok {
			return false
		}
		if seen[key] {
			return true
		}
		seen[key] = true
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return false
		}
	}
	return false
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

func repairPush(p string) string {
	if utf8.RuneCountInString(p) <= maxPush {
		return p
	}
	all := []rune(p)
	for i := maxPush - 1; i >= 0; i-- {
		if isPunctAt(all, i) {
			return strings.TrimSpace(string(all[:i]))
		}
	}
	return ""
}

// isSentenceEndAt: a Chinese or ASCII stop mark, or a newline; an ASCII '.' only when whitespace follows it, so
// "v1.2", "3.14" and "main.go" are not cut in the middle.
func isSentenceEndAt(r []rune, i int) bool {
	switch r[i] {
	case '。', '！', '？', '!', '?', '\n':
		return true
	case '.':
		return i+1 < len(r) && unicode.IsSpace(r[i+1])
	}
	return false
}

func isPunctAt(r []rune, i int) bool {
	if isSentenceEndAt(r, i) {
		return true
	}
	switch r[i] {
	case '，', '、', '；', '：', ',', ';', ':':
		return true
	}
	return false
}

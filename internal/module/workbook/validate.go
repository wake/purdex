package workbook

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/redact"
)

// Limits of spec §5.4, in characters (runes, not bytes: the text is Chinese).
const (
	maxThing  = 16
	maxPush   = 40
	maxEntry  = 150
	maxStatus = 200

	// maxModelJSON bounds what is parsed (the object is a few hundred bytes; the runner caps stdout well above this).
	maxModelJSON = 64 << 10
)

// ErrFormat: the model's answer is not the JSON object of spec §5.3 (after one fence is stripped).
var ErrFormat = errors.New("workbook: the answer is not the expected JSON")

// ModelTodos is the answer's todo changes as the model wrote them: numbers of the job's open todos (spec §5.3).
type ModelTodos struct {
	Done    []int     `json:"done"`
	Dropped []int     `json:"dropped"`
	Add     []TodoAdd `json:"add"`
}

// Summary is the model's answer.
type Summary struct {
	Skip      bool       `json:"skip"`
	Thing     string     `json:"thing"`
	Push      string     `json:"push"`
	Entry     string     `json:"entry"`
	Status    string     `json:"status"`
	ThingDone bool       `json:"thing_done"`
	Todos     ModelTodos `json:"todos"`
}

// RefreshResult is the answer to a refresh job (spec §5.6): {status, todos}. A push, a thing or an entry the model adds
// is not read.
type RefreshResult struct {
	Status string     `json:"status"`
	Todos  ModelTodos `json:"todos"`
}

// objectOf reads the text as one JSON object (one code fence allowed) and returns its members: the shared start of both
// answers. Anything else, or a repeated member name, is ErrFormat.
func objectOf(text string) (body string, members map[string]json.RawMessage, err error) {
	if len(text) > maxModelJSON {
		return "", nil, ErrFormat
	}
	body = stripFence(strings.TrimSpace(text))
	if hasDuplicateMember(body) { // two values for one field: which one counts would depend on the order
		return "", nil, ErrFormat
	}
	if json.Unmarshal([]byte(body), &members) != nil {
		return "", nil, ErrFormat
	}
	return body, members, nil
}

// checkTodos holds the todos member to its shape: an object, no repeated member in it or in an add, and the types of
// spec §5.3 (the decode itself refuses a wrong type).
func checkTodos(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' || hasDuplicateMember(string(raw)) {
		return ErrFormat
	}
	var sub map[string]json.RawMessage
	if json.Unmarshal(raw, &sub) != nil {
		return ErrFormat
	}
	if adds, ok := sub["add"]; ok {
		var items []json.RawMessage
		if json.Unmarshal(adds, &items) != nil {
			return ErrFormat
		}
		for _, it := range items {
			if hasDuplicateMember(string(it)) {
				return ErrFormat
			}
		}
	}
	var mt ModelTodos
	if json.Unmarshal(raw, &mt) != nil {
		return ErrFormat
	}
	return nil
}

// ParseModelJSON reads the model's text as a Summary. One code fence around the object is allowed; anything else around
// it, a non-object, a missing one of the seven fields (spec §5.3), a field of the wrong type, or (unless it skips) an
// empty thing or entry is ErrFormat. A skip keeps its todos.
func ParseModelJSON(text string) (Summary, error) {
	body, members, err := objectOf(text)
	if err != nil {
		return Summary{}, err
	}
	for _, k := range []string{"skip", "thing", "push", "entry", "status", "thing_done", "todos"} {
		if _, ok := members[k]; !ok {
			return Summary{}, ErrFormat // §5.3 names seven fields; a missing one is not "empty", it is a malformed answer
		}
	}
	if checkTodos(members["todos"]) != nil {
		return Summary{}, ErrFormat
	}
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

// ParseRefreshJSON reads a refresh job's answer: {status, todos}, both required, the status not empty.
func ParseRefreshJSON(text string) (RefreshResult, error) {
	body, members, err := objectOf(text)
	if err != nil {
		return RefreshResult{}, err
	}
	for _, k := range []string{"status", "todos"} {
		if _, ok := members[k]; !ok {
			return RefreshResult{}, ErrFormat
		}
	}
	if checkTodos(members["todos"]) != nil {
		return RefreshResult{}, ErrFormat
	}
	var r RefreshResult
	if err := json.Unmarshal([]byte(body), &r); err != nil || strings.TrimSpace(r.Status) == "" {
		return RefreshResult{}, ErrFormat
	}
	return r, nil
}

// RepairRefresh redacts the status and cuts it at the last sentence end within 200 (spec §5.4).
func RepairRefresh(r RefreshResult) RefreshResult {
	r.Status = cutAtSentenceEnd(strings.TrimSpace(redact.String(r.Status)), maxStatus)
	r.Todos = redactTodos(r.Todos)
	return r
}

func redactTodos(t ModelTodos) ModelTodos {
	out := ModelTodos{Done: t.Done, Dropped: t.Dropped}
	for _, a := range t.Add {
		out.Add = append(out.Add, TodoAdd{Title: redact.String(a.Title), Detail: redact.String(a.Detail)})
	}
	return out
}

// ResolveTodos reads the model's numbers through the job's n -> todo id map (plan D12): a number the job did not hand out
// is ignored.
func ResolveTodos(t ModelTodos, ids map[int]int64) TodoChanges {
	var ch TodoChanges
	for _, n := range t.Done {
		if id, ok := ids[n]; ok {
			ch.Done = append(ch.Done, id)
		}
	}
	for _, n := range t.Dropped {
		if id, ok := ids[n]; ok {
			ch.Dropped = append(ch.Dropped, id)
		}
	}
	ch.Adds = t.Add
	return ch
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
	out.Todos = redactTodos(s.Todos)
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
	all := []rune(p)
	for i := maxPush - 1; i >= 0; i-- {
		if isPunctAt(all, i) {
			return strings.TrimSpace(string(all[:i]))
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
	for i := n - 1; i >= 0; i-- {
		if isSentenceEndAt(r, i) {
			return string(r[:i+1])
		}
	}
	return string(r[:n])
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

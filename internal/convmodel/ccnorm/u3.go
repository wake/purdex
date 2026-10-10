package ccnorm

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/convmodel"
)

// U3-0 (plan D12): the question of an AskUserQuestion-shaped call and its answers, the range of a Read, the scope of a
// search, a created file and the summary of a compaction. Collie's parser (bridge/journal/claude.ts, tool-call.ts) is the
// reference; where this file differs the reason is in the case's comment. All additive: older clients ignore the members.

// questionOf reads the `questions` list of a tool input. The SHAPE is the rule, not the tool name: an entry needs a text and
// an `options` array whose entries are strings or {label, description}; a malformed entry is dropped, and a list with
// nothing valid left is not a question. At most MaxQuestions questions and MaxQuestionOptions options are kept, every
// string at most MaxInputString bytes (like the step input they come from).
func questionOf(in object) *convmodel.StepQuestion {
	blocks, ok := contentBlocks(in.get("questions"))
	if !ok {
		return nil
	}
	var out []convmodel.QuestionItem
	for _, b := range blocks {
		if len(out) == convmodel.MaxQuestions {
			break
		}
		var rawOpts []json.RawMessage
		if json.Unmarshal(b.obj.get("options"), &rawOpts) != nil {
			continue // no options array: not a question
		}
		text := trimCap(b.obj.str("question"))
		if text == "" {
			continue
		}
		q := convmodel.QuestionItem{
			Key: b.obj.str("question"), Question: text, Header: trimCap(b.obj.str("header")),
			Multiple: jsonTrue(b.obj.get("multiSelect")) || jsonTrue(b.obj.get("multiple")),
			Options:  []convmodel.QuestionOption{},
		}
		for _, raw := range rawOpts {
			if len(q.Options) == convmodel.MaxQuestionOptions {
				break
			}
			var label, desc string
			if s, ok := jsonString(raw); ok {
				label = s
			} else if o, ok := parseObject(raw); ok {
				label, desc = o.str("label"), o.str("description")
			}
			if label = trimCap(label); label != "" {
				q.Options = append(q.Options, convmodel.QuestionOption{Label: label, Description: trimCap(desc)})
			}
		}
		out = append(out, q)
	}
	if len(out) == 0 {
		return nil
	}
	return &convmodel.StepQuestion{Questions: out}
}

// trimCap is a blank-trimmed input string, capped like any string of the step input.
func trimCap(s string) string {
	s, _ = capText(strings.TrimSpace(s), convmodel.MaxInputString)
	return s
}

// withAnswers is q with the answers of a completed call, from toolUseResult.answers: an object keyed by the question's own
// text, one string each. A single-select answer (and any free text, "Other") is kept whole, capped like user text; a
// multi-select answer is ONE string joining the picks with ", " and quoting the ones that contain a comma, so it is read
// back by finding which option labels it names (none named: kept whole). A call whose answers do not cover every question
// has none (q unchanged). q is never modified: the step it came from may be shared.
func withAnswers(q *convmodel.StepQuestion, tur object) *convmodel.StepQuestion {
	given, ok := parseObject(tur.get("answers"))
	if !ok {
		return q
	}
	answers := make([][]string, 0, len(q.Questions))
	for _, it := range q.Questions {
		s, ok := jsonString(given.get(it.Key))
		if !ok {
			return q
		}
		picked := []string(nil)
		if it.Multiple {
			for _, o := range it.Options {
				if namesItem(s, o.Label) {
					picked = append(picked, o.Label)
				}
			}
		}
		if len(picked) == 0 {
			s, _ = capText(s, convmodel.MaxText)
			picked = []string{s}
		}
		answers = append(answers, picked)
	}
	return &convmodel.StepQuestion{Questions: q.Questions, Answers: answers}
}

// namesItem reports whether joined holds label as one whole item of a ", "-joined list, quoted or not.
func namesItem(joined, label string) bool {
	for _, cand := range [2]string{label, `"` + label + `"`} {
		for from := 0; ; {
			i := strings.Index(joined[from:], cand)
			if i < 0 {
				break
			}
			i += from
			end := i + len(cand)
			if (i == 0 || strings.HasSuffix(joined[:i], ", ")) && (end == len(joined) || strings.HasPrefix(joined[end:], ", ")) {
				return true
			}
			from = i + 1
		}
	}
	return false
}

// readRangeOf is the offset and limit a Read asked for: whole numbers of at least 1, as numbers or numeric text. A call
// that gave neither has no range. (Collie keeps a range only when both are given; here either is enough, since "from
// line N to the end" and "the first N lines" are both ranges.)
func readRangeOf(in object) *convmodel.ReadRange {
	r := convmodel.ReadRange{Offset: wholeNumber(in.get("offset")), Limit: wholeNumber(in.get("limit"))}
	if r.Offset == 0 && r.Limit == 0 {
		return nil
	}
	return &r
}

// wholeNumber is raw as an integer ≥ 1 (a JSON number, or a string holding one); 0 for anything else.
func wholeNumber(raw json.RawMessage) int {
	var f float64
	if s, ok := jsonString(raw); ok {
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return 0
		}
		f = v
	} else if json.Unmarshal(raw, &f) != nil {
		return 0
	}
	if f < 1 || f > math.MaxInt32 || f != math.Trunc(f) {
		return 0
	}
	return int(f)
}

// searchScopeOf is where a Grep / Glob / WebSearch looked: the path, else the glob, else "web" for the web search.
func searchScopeOf(tool string, in object) *convmodel.SearchScope {
	switch tool {
	case "Grep", "Glob":
		for _, k := range [...]string{"path", "glob"} {
			if s := strings.TrimSpace(in.str(k)); s != "" {
				w, _ := capText(s, convmodel.MaxInputString)
				return &convmodel.SearchScope{Where: w}
			}
		}
	case "WebSearch":
		return &convmodel.SearchScope{Where: "web"}
	}
	return nil
}

// createdDiff marks the diff of a Write as a created file when its result says so (toolUseResult.type "create"; Claude Code
// writes originalFile null there too). A Write of an empty file has no input diff, so one is made for it. d is not modified.
func createdDiff(d *convmodel.Diff, path string, tur object) *convmodel.Diff {
	if tur.str("type") != "create" {
		return d
	}
	if d == nil {
		if path == "" {
			path = tur.str("filePath")
		}
		p, _ := capText(path, maxDiffLine)
		return &convmodel.Diff{Path: p, Exact: true, Created: true}
	}
	c := *d
	c.Created = true
	return &c
}

// compactDetail is the detail of a `compacted` item.
type compactDetail struct {
	Trigger   string `json:"trigger,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (d compactDetail) json() json.RawMessage {
	if d == (compactDetail{}) {
		return nil
	}
	return marshalNoEscape(d)
}

// compactSummary gives the compaction the summary Claude Code wrote right after its boundary (the isCompactSummary row),
// capped like user text. A summary with no boundary before it, or a second one, stays skipped and counted.
func (n *Normalizer) compactSummary(blocks []block, off int64) {
	loc, ok := n.itemAt[n.compactID]
	if n.compactID == "" || !ok {
		n.skip("compact_summary")
		return
	}
	tr := n.turns[loc.turn]
	it := tr.t.Items[loc.item]
	text := strings.TrimSpace(joinText(blocks))
	n.compactID = ""
	if text == "" || it.System == nil {
		n.skip("compact_summary")
		return
	}
	var d compactDetail
	_ = json.Unmarshal(it.System.Detail, &d)
	d.Summary, d.Truncated = capText(text, convmodel.MaxText)
	sys := *it.System
	sys.Detail = d.json()
	n.upsert(tr.t.ID, convmodel.Item{Type: convmodel.ItemSystem, System: &sys}, off)
}

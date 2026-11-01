// Package dialogue writes a session's dialogue file: a plain-text record of what
// the person and the agent said, one file per segment (spec 2026-11-01-session-dialogue-log §2.2).
package dialogue

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/convmodel"
)

const truncatedMark = "…（截斷）"

// block is one result of renderItems: either text to append to the segment file, or a compaction boundary after which
// the writer starts a new segment.
//
// text has no leading or trailing blank line: it starts with its first line and ends with one "\n". Blank lines are
// only inserted between the elements inside it (a speech paragraph is set off by a blank line on both sides; single-line
// markers stay adjacent). The writer puts one blank line between consecutive text blocks.
type block struct {
	text    string
	compact bool   // a compaction boundary; text is empty
	trigger string // "auto" or "manual", set when compact
}

// element is one rendered piece before spacing: a paragraph (speech, may span lines) or a single-line marker.
type element struct {
	text string
	para bool
}

// renderItems renders a turn's items (possibly the second half of a turn) in order.
func renderItems(items []convmodel.Item) []block {
	var out []block
	var els []element
	flush := func() {
		if len(els) == 0 {
			return
		}
		out = append(out, block{text: layout(els)})
		els = nil
	}
	for _, it := range items {
		if it.Type == convmodel.ItemSystem && it.System != nil && it.System.Kind == convmodel.SystemCompacted {
			flush()
			out = append(out, block{compact: true, trigger: compactTrigger(it.System.Detail)})
			continue
		}
		els = append(els, renderItem(it)...)
	}
	flush()
	return out
}

// layout joins elements: one blank line next to a paragraph, none between two markers.
func layout(els []element) string {
	var sb strings.Builder
	for i, e := range els {
		if i > 0 && (e.para || els[i-1].para) {
			sb.WriteByte('\n')
		}
		sb.WriteString(e.text)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func renderItem(it convmodel.Item) []element {
	switch it.Type {
	case convmodel.ItemUser:
		if it.User != nil {
			return renderUser(it.User)
		}
	case convmodel.ItemAgentText:
		if a := it.AgentText; a != nil && strings.TrimSpace(a.Markdown) != "" {
			return []element{{text: "agent: " + speech(a.Markdown, a.Truncated), para: true}}
		}
	case convmodel.ItemStep:
		if it.Step != nil {
			return renderStep(it.Step)
		}
	}
	return nil // thinking, other system items, unknown types
}

func renderUser(u *convmodel.UserMessage) []element {
	var els []element
	switch u.Source {
	case convmodel.SourceSlash:
		if l := firstLine(u.Text); l != "" {
			els = append(els, element{text: "<slash | " + l + ">"})
		}
	case convmodel.SourceBash:
		if l := firstLine(u.Text); l != "" {
			els = append(els, element{text: "<shell | " + l + ">"})
		}
	case convmodel.SourceTask:
		if s := taskSummary(u.Text); s != "" {
			els = append(els, element{text: "<subagent done | " + s + ">"})
		}
	default:
		label := "user"
		switch u.Source {
		case convmodel.SourcePeer:
			name := "unknown"
			if u.From != nil && strings.TrimSpace(u.From.Name) != "" {
				name = u.From.Name
			}
			label = "peer(" + name + ")"
		case convmodel.SourceScheduled:
			label = "scheduled"
		}
		if strings.TrimSpace(u.Text) != "" || len(u.Images) > 0 {
			text := strings.TrimRight(label+": "+speech(u.Text, u.Truncated), " ")
			if len(u.Images) > 0 { // the image line belongs to the speech paragraph
				text += fmt.Sprintf("\n<image ×%d>", len(u.Images))
			}
			els = append(els, element{text: text, para: true})
		}
		return els
	}
	if len(u.Images) > 0 {
		els = append(els, element{text: fmt.Sprintf("<image ×%d>", len(u.Images))})
	}
	return els
}

func renderStep(s *convmodel.Step) []element {
	summary := s.Summary
	if q := s.Question; q != nil && len(q.Questions) > 0 {
		summary = q.Questions[0].Question
	}
	els := []element{{text: "<tool " + s.Tool + " | " + oneLine(summary) + ">"}}
	if q := s.Question; q != nil {
		var parts []string
		for _, a := range q.Answers {
			parts = append(parts, strings.Join(a, "、"))
		}
		if ans := strings.Join(parts, "；"); strings.Trim(ans, "、；") != "" {
			els = append(els, element{text: "<answer | " + oneLine(ans) + ">"})
		}
	}
	return els
}

// speech is the text kept whole (trailing newlines dropped), with the truncation mark when the model capped it.
func speech(text string, truncated bool) string {
	text = strings.TrimRight(text, "\r\n")
	if truncated {
		text += truncatedMark
	}
	return text
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// oneLine folds all whitespace runs, newlines included, into single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// taskSummary is the text inside <summary>…</summary> of a task notification, folded to one line; without the tag, the
// first non-blank line.
func taskSummary(text string) string {
	if i := strings.Index(text, "<summary>"); i >= 0 {
		rest := text[i+len("<summary>"):]
		if j := strings.Index(rest, "</summary>"); j >= 0 {
			if s := oneLine(rest[:j]); s != "" {
				return s
			}
		}
	}
	return firstLine(text)
}

// compactTrigger reads Detail.trigger; anything but "manual" is "auto".
func compactTrigger(detail json.RawMessage) string {
	var d struct {
		Trigger string `json:"trigger"`
	}
	if json.Unmarshal(detail, &d) == nil && d.Trigger == "manual" {
		return "manual"
	}
	return "auto"
}

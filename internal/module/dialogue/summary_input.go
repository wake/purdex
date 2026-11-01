package dialogue

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/redact"
)

// Limits of a turn-summary job's input (spec §3).
const (
	maxUserPrompt    = 1500
	maxAssistantText = 3000
	maxTools         = 20
)

type summaryTurnPart struct {
	UserPrompt    string   `json:"user_prompt"`
	AssistantText string   `json:"assistant_text"`
	Tools         []string `json:"tools"`
}

type summaryInput struct {
	RecentTitles []string        `json:"recent_titles"`
	Turn         summaryTurnPart `json:"turn"`
}

// buildSummaryPrompt is the JSON a turn-summary job sends: the session's last titles (oldest first) and the turn's text.
// Every string goes through redact.String before it is cut. When the transcript has no agent text for the turn and
// hookText (what the Stop hook carried) is not empty, assistant_text is the hook's text.
func buildSummaryPrompt(turn convmodel.Turn, recentTitles []string, hookText string) (string, error) {
	in := summaryInput{RecentTitles: []string{}, Turn: summaryTurnPart{Tools: []string{}}}
	for _, t := range recentTitles {
		in.RecentTitles = append(in.RecentTitles, redact.String(t))
	}
	var users []string
	var reply string
	for _, it := range turn.Items {
		switch {
		case it.User != nil:
			users = append(users, it.User.Text)
		case it.AgentText != nil && strings.TrimSpace(it.AgentText.Markdown) != "":
			reply = it.AgentText.Markdown // the session's last words of the turn
		case it.Step != nil && len(in.Turn.Tools) < maxTools:
			tool := it.Step.Tool
			if s := strings.TrimSpace(it.Step.Summary); s != "" {
				tool += ": " + s
			}
			in.Turn.Tools = append(in.Turn.Tools, redact.String(tool))
		}
	}
	if reply == "" && strings.TrimSpace(hookText) != "" {
		reply = hookText
	}
	in.Turn.UserPrompt = cutEllipsis(redact.String(strings.Join(users, "\n")), maxUserPrompt)
	in.Turn.AssistantText = cutEllipsis(redact.String(reply), maxAssistantText)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // "<a> & b" is sent as written
	if err := enc.Encode(in); err != nil {
		return "", fmt.Errorf("build turn-summary input: %w", err)
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// cutEllipsis keeps s whole within n runes; otherwise the first n-1 runes and an ellipsis.
func cutEllipsis(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// turnSummaryPrompt is the system prompt of a turn-summary job (plan appendix B-1).
//
//go:embed prompts/turn-summary-v1.txt
var turnSummaryPrompt string

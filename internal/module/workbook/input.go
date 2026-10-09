package workbook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/redact"
)

// Limits of a job's input (spec §5.2).
const (
	maxUserPrompt    = 1500
	maxAssistantText = 3000
	maxTools         = 20
	maxRecentEntries = 3
	maxPromptTodos   = 30
)

// TurnSource is everything the input of a turn job is built from, read at hand-out (plan D12): the previous output is
// already applied, so the status, the recent ok entries and the open todos are the current ones.
type TurnSource struct {
	PreviousStatus string
	Recent         []Entry // the conversation's last ok entries, oldest first; the newest three are sent
	Open           []Todo  // the conversation's open todos, oldest first; the first 30 are sent
	Turn           convmodel.Turn
}

// turnInput is the prompt JSON of spec §5.2.
type turnInput struct {
	PreviousStatus string        `json:"previous_status"`
	RecentEntries  []recentEntry `json:"recent_entries"`
	OpenTodos      []openTodo    `json:"open_todos"`
	DroppedTitles  []string      `json:"dropped_titles"` // always empty in v2: the todos are read-only (spec §2 item 11)
	Turn           turnPart      `json:"turn"`
}

type recentEntry struct {
	Thing string `json:"thing"`
	Entry string `json:"entry"`
}

type openTodo struct {
	N      int    `json:"n"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type turnPart struct {
	UserPrompt    string   `json:"user_prompt"`
	AssistantText string   `json:"assistant_text"`
	Tools         []string `json:"tools"`
}

// BuildTurnInput builds the prompt of a turn job and the job's map from the numbers it handed out (n, from 1, oldest
// todo first) to todo ids. Every string goes through redact.String before it is cut; the output is byte-stable for the
// same source.
func BuildTurnInput(src TurnSource) (prompt string, ids map[int]int64, err error) {
	in := turnInput{
		PreviousStatus: redact.String(src.PreviousStatus),
		RecentEntries:  []recentEntry{},
		OpenTodos:      []openTodo{},
		DroppedTitles:  []string{},
		Turn:           turnPart{Tools: []string{}},
	}
	recent := src.Recent
	if len(recent) > maxRecentEntries {
		recent = recent[len(recent)-maxRecentEntries:]
	}
	for _, e := range recent {
		in.RecentEntries = append(in.RecentEntries, recentEntry{Thing: redact.String(e.Thing), Entry: redact.String(e.Entry)})
	}
	in.OpenTodos, ids = numberTodos(src.Open)

	var users []string
	for _, it := range src.Turn.Items {
		switch {
		case it.User != nil:
			users = append(users, it.User.Text)
		case it.AgentText != nil && strings.TrimSpace(it.AgentText.Markdown) != "":
			in.Turn.AssistantText = it.AgentText.Markdown // the session's last words of the turn
		case it.Step != nil && len(in.Turn.Tools) < maxTools:
			tool := it.Step.Tool
			if s := strings.TrimSpace(it.Step.Summary); s != "" {
				tool += ": " + s
			}
			in.Turn.Tools = append(in.Turn.Tools, redact.String(tool))
		}
	}
	in.Turn.UserPrompt = cutEllipsis(redact.String(strings.Join(users, "\n")), maxUserPrompt)
	in.Turn.AssistantText = cutEllipsis(redact.String(in.Turn.AssistantText), maxAssistantText)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // "<a> & b" is sent as written
	if err := enc.Encode(in); err != nil {
		return "", nil, fmt.Errorf("build workbook input: %w", err)
	}
	return strings.TrimSuffix(buf.String(), "\n"), ids, nil
}

// numberTodos numbers the first 30 open todos from 1 for one job and returns the job's n -> id map.
func numberTodos(open []Todo) ([]openTodo, map[int]int64) {
	out := []openTodo{}
	ids := map[int]int64{}
	for i, t := range open {
		if i >= maxPromptTodos {
			break
		}
		out = append(out, openTodo{N: i + 1, Title: redact.String(t.Title), Detail: redact.String(t.Detail)})
		ids[i+1] = t.ID
	}
	return out, ids
}

// BuildRefreshInput fills the refresh prompt (spec §5.6) with the status and the numbered open todos; the dropped titles
// are [] in v2. It returns the job's n -> id map like BuildTurnInput.
func BuildRefreshInput(status string, open []Todo) (prompt string, ids map[int]int64) {
	todos, ids := numberTodos(open)
	list := "（沒有）"
	if len(todos) > 0 {
		var lines []string
		for _, t := range todos {
			line := fmt.Sprintf("%d：%s", t.N, t.Title)
			if t.Detail != "" {
				line += " — " + t.Detail
			}
			lines = append(lines, line)
		}
		list = strings.Join(lines, "\n")
	}
	r := strings.NewReplacer(
		"{{status}}", redact.String(status),
		"{{open_todos}}", list,
		"{{dropped_titles}}", "[]",
	)
	return r.Replace(RefreshPrompt), ids
}

// cutEllipsis keeps s within n runes: longer text is cut to n-1 runes and an ellipsis.
func cutEllipsis(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

package dialogue

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convmodel"
)

func sumUser(text string) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemUser, User: &convmodel.UserMessage{Text: text}}
}
func sumAgent(md string) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemAgentText, AgentText: &convmodel.AgentText{Markdown: md}}
}
func sumStep(tool, summary string) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemStep, Step: &convmodel.Step{Tool: tool, Summary: summary}}
}

type promptShape struct {
	RecentTitles []string `json:"recent_titles"`
	Turn         struct {
		UserPrompt    string   `json:"user_prompt"`
		AssistantText string   `json:"assistant_text"`
		Tools         []string `json:"tools"`
	} `json:"turn"`
}

func decodePrompt(t *testing.T, s string) promptShape {
	t.Helper()
	var p promptShape
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	return p
}

func TestBuildSummaryPrompt_Shape(t *testing.T) {
	turn := convmodel.Turn{Items: []convmodel.Item{
		sumUser("請修好 <a> & b"), sumUser("再補測試"),
		sumAgent("先說的"), sumStep("Bash", "go test"), sumStep("Read", ""), sumAgent("最後說的"),
	}}
	s, err := buildSummaryPrompt(turn, []string{"甲", "乙"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, "\\u003c") || strings.HasSuffix(s, "\n") {
		t.Fatalf("html escaped or trailing newline: %q", s)
	}
	p := decodePrompt(t, s)
	if len(p.RecentTitles) != 2 || p.RecentTitles[1] != "乙" {
		t.Fatalf("recent_titles = %v", p.RecentTitles)
	}
	if p.Turn.UserPrompt != "請修好 <a> & b\n再補測試" || p.Turn.AssistantText != "最後說的" {
		t.Fatalf("turn = %+v", p.Turn)
	}
	if len(p.Turn.Tools) != 2 || p.Turn.Tools[0] != "Bash: go test" || p.Turn.Tools[1] != "Read" {
		t.Fatalf("tools = %v", p.Turn.Tools)
	}
}

func TestBuildSummaryPrompt_EmptyListsAreArrays(t *testing.T) {
	s, err := buildSummaryPrompt(convmodel.Turn{Items: []convmodel.Item{sumUser("x")}}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `"recent_titles":[]`) || !strings.Contains(s, `"tools":[]`) {
		t.Fatalf("nil lists must be []: %s", s)
	}
}

func TestBuildSummaryPrompt_LimitsAndRedaction(t *testing.T) {
	secret := "sk-ant-api03-" + strings.Repeat("a", 40)
	items := []convmodel.Item{sumUser(strings.Repeat("問", 2000) + secret), sumAgent(strings.Repeat("答", 4000))}
	for i := 0; i < 30; i++ {
		items = append(items, sumStep("Bash", "ls"))
	}
	s, err := buildSummaryPrompt(convmodel.Turn{Items: items}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, secret) {
		t.Fatal("a secret reached the prompt")
	}
	p := decodePrompt(t, s)
	if n := utf8.RuneCountInString(p.Turn.UserPrompt); n != maxUserPrompt || !strings.HasSuffix(p.Turn.UserPrompt, "…") {
		t.Fatalf("user prompt %d runes", n)
	}
	if n := utf8.RuneCountInString(p.Turn.AssistantText); n != maxAssistantText || !strings.HasSuffix(p.Turn.AssistantText, "…") {
		t.Fatalf("assistant text %d runes", n)
	}
	if len(p.Turn.Tools) != maxTools {
		t.Fatalf("%d tools", len(p.Turn.Tools))
	}
}

func TestBuildSummaryPrompt_HookTextOnlyWhenThereIsNoAgentText(t *testing.T) {
	noReply := convmodel.Turn{Items: []convmodel.Item{sumUser("u"), sumStep("Bash", "x")}}
	p := decodePrompt(t, mustPrompt(t, noReply, "hook 說的"))
	if p.Turn.AssistantText != "hook 說的" {
		t.Fatalf("assistant_text = %q", p.Turn.AssistantText)
	}
	withReply := convmodel.Turn{Items: []convmodel.Item{sumUser("u"), sumAgent("transcript 說的")}}
	p = decodePrompt(t, mustPrompt(t, withReply, "hook 說的"))
	if p.Turn.AssistantText != "transcript 說的" {
		t.Fatalf("assistant_text = %q", p.Turn.AssistantText)
	}
	// the hook text is redacted and cut like any other
	secret := "sk-ant-api03-" + strings.Repeat("a", 40)
	p = decodePrompt(t, mustPrompt(t, noReply, secret+strings.Repeat("答", 4000)))
	if strings.Contains(p.Turn.AssistantText, secret) || utf8.RuneCountInString(p.Turn.AssistantText) != maxAssistantText {
		t.Fatalf("hook text: %d runes, secret=%v", utf8.RuneCountInString(p.Turn.AssistantText), strings.Contains(p.Turn.AssistantText, secret))
	}
}

func mustPrompt(t *testing.T, turn convmodel.Turn, hook string) string {
	t.Helper()
	s, err := buildSummaryPrompt(turn, nil, hook)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

package workbook

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

// WB-1b′-a: the job's input (spec §5.2, plan D12).

func userItem(text string) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemUser, User: &convmodel.UserMessage{ID: "u", Text: text, Source: convmodel.SourceUser}}
}
func agentItem(md string) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemAgentText, AgentText: &convmodel.AgentText{ID: "a", Markdown: md}}
}
func stepItem(tool, summary string) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemStep, Step: &convmodel.Step{ID: "s", Tool: tool, Summary: summary}}
}

type decoded struct {
	PreviousStatus string `json:"previous_status"`
	RecentEntries  []struct {
		Thing string `json:"thing"`
		Entry string `json:"entry"`
	} `json:"recent_entries"`
	OpenTodos []struct {
		N      int    `json:"n"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	} `json:"open_todos"`
	DroppedTitles []string `json:"dropped_titles"`
	Turn          struct {
		UserPrompt    string   `json:"user_prompt"`
		AssistantText string   `json:"assistant_text"`
		Tools         []string `json:"tools"`
	} `json:"turn"`
}

func parse(t *testing.T, s string) decoded {
	t.Helper()
	var d decoded
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatalf("not JSON: %v: %s", err, s)
	}
	return d
}

func todosOf(titles ...string) []Todo {
	var out []Todo
	for i, t := range titles {
		out = append(out, Todo{ID: int64(100 + i), Title: t, Detail: "細節" + t})
	}
	return out
}

func TestBuildTurnInput_ShapeAndNumbering(t *testing.T) {
	turn := convmodel.Turn{ID: "t1", Items: []convmodel.Item{
		userItem("請修好登入"), stepItem("Bash", "go test ./auth"), stepItem("Edit", "auth/login.go"), agentItem("先說的"), agentItem("修好了，測試全過。"),
	}}
	in := TurnSource{
		PreviousStatus: "在修登入",
		Recent:         []Entry{{Thing: "登入", Entry: "上一筆。"}, {Thing: "登入", Entry: "這一筆。"}},
		Open:           todosOf("甲", "乙", "丙"),
		Turn:           turn,
	}
	prompt, ids, err := BuildTurnInput(in)
	if err != nil {
		t.Fatal(err)
	}
	d := parse(t, prompt)
	if d.PreviousStatus != "在修登入" || len(d.RecentEntries) != 2 || d.RecentEntries[1].Entry != "這一筆。" {
		t.Fatalf("context = %+v", d)
	}
	if d.Turn.UserPrompt != "請修好登入" || d.Turn.AssistantText != "修好了，測試全過。" {
		t.Fatalf("turn = %+v (the assistant text is the session's last words)", d.Turn)
	}
	if strings.Join(d.Turn.Tools, ";") != "Bash: go test ./auth;Edit: auth/login.go" {
		t.Fatalf("tools = %v", d.Turn.Tools)
	}
	// open todos numbered from 1, oldest first, with the job's map n → id
	if len(d.OpenTodos) != 3 || d.OpenTodos[0].N != 1 || d.OpenTodos[0].Title != "甲" || d.OpenTodos[2].N != 3 || d.OpenTodos[2].Detail != "細節丙" {
		t.Fatalf("open_todos = %+v", d.OpenTodos)
	}
	if ids[1] != 100 || ids[2] != 101 || ids[3] != 102 || len(ids) != 3 {
		t.Fatalf("ids = %v", ids)
	}
	// dropped_titles is always an empty array in v2, never null or missing
	if d.DroppedTitles == nil || len(d.DroppedTitles) != 0 || !strings.Contains(prompt, `"dropped_titles":[]`) {
		t.Fatalf("dropped_titles = %v in %s", d.DroppedTitles, prompt)
	}
}

// An empty list is [] on the wire, never null: the measured prompt is sent as measured.
func TestBuildTurnInput_EmptyListsAreArrays(t *testing.T) {
	prompt, ids, err := BuildTurnInput(TurnSource{Turn: convmodel.Turn{Items: []convmodel.Item{userItem("嗨")}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"recent_entries":[]`, `"open_todos":[]`, `"dropped_titles":[]`, `"tools":[]`, `"previous_status":""`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("missing %s in %s", want, prompt)
		}
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v", ids)
	}
}

// 30 open todos at most, oldest first; the 31st is not in the prompt (the daemon would not close what it did not show).
func TestBuildTurnInput_AtMostThirtyOpenTodos(t *testing.T) {
	var names []string
	for i := 0; i < 40; i++ {
		names = append(names, fmt.Sprintf("項目%d", i))
	}
	prompt, ids, err := BuildTurnInput(TurnSource{Open: todosOf(names...), Turn: convmodel.Turn{}})
	if err != nil {
		t.Fatal(err)
	}
	d := parse(t, prompt)
	if len(d.OpenTodos) != 30 || d.OpenTodos[0].Title != "項目0" || d.OpenTodos[29].Title != "項目29" || len(ids) != 30 {
		t.Fatalf("open_todos = %d, first %q, ids %d", len(d.OpenTodos), d.OpenTodos[0].Title, len(ids))
	}
}

func TestBuildTurnInput_LimitsAndCuts(t *testing.T) {
	long := strings.Repeat("長", 2000)
	assistant := strings.Repeat("答", 4000)
	var items []convmodel.Item
	items = append(items, userItem(long), agentItem(assistant))
	for i := 0; i < 30; i++ {
		items = append(items, stepItem("Bash", fmt.Sprintf("cmd %d", i)))
	}
	prompt, _, _ := BuildTurnInput(TurnSource{Turn: convmodel.Turn{Items: items}})
	d := parse(t, prompt)
	if r := []rune(d.Turn.UserPrompt); len(r) != 1500 || r[1499] != '…' {
		t.Fatalf("user_prompt = %d runes, last %q", len(r), string(r[len(r)-1]))
	}
	if r := []rune(d.Turn.AssistantText); len(r) != 3000 || r[2999] != '…' {
		t.Fatalf("assistant_text = %d runes", len(r))
	}
	if len(d.Turn.Tools) != 20 || d.Turn.Tools[0] != "Bash: cmd 0" {
		t.Fatalf("tools = %d, first %q", len(d.Turn.Tools), d.Turn.Tools[0])
	}
	// text within the limit is untouched
	prompt, _, _ = BuildTurnInput(TurnSource{Turn: convmodel.Turn{Items: []convmodel.Item{userItem(strings.Repeat("短", 1500))}}})
	if parse(t, prompt).Turn.UserPrompt != strings.Repeat("短", 1500) {
		t.Fatal("a text at the limit was cut")
	}
}

// Every string that goes to the model is redacted, whichever field it sits in.
// Mutation gate: skip the redaction of one field → red.
func TestBuildTurnInput_RedactsEveryString(t *testing.T) {
	tok := "sk-ant-api03-AbCdEf123456"
	turn := convmodel.Turn{Items: []convmodel.Item{userItem("用 " + tok), stepItem("Bash", "curl -H 'Authorization: Bearer "+tok+"'"), agentItem("貼了 " + tok)}}
	in := TurnSource{
		PreviousStatus: "狀況 " + tok,
		Recent:         []Entry{{Thing: "事 " + tok, Entry: "紀錄 " + tok}},
		Open:           []Todo{{ID: 1, Title: "待辦 " + tok, Detail: "細節 " + tok}},
		Turn:           turn,
	}
	prompt, _, _ := BuildTurnInput(in)
	if strings.Contains(prompt, "AbCdEf") {
		t.Fatalf("a secret reached the prompt: %s", prompt)
	}
	if got := strings.Count(prompt, "[redacted]"); got < 8 {
		t.Fatalf("only %d redactions in %s", got, prompt)
	}
}

// Same input, same bytes: the golden string below is what the model is sent for this fixture.
func TestBuildTurnInput_ByteStable(t *testing.T) {
	in := TurnSource{
		PreviousStatus: "在修登入",
		Recent:         []Entry{{Thing: "登入", Entry: "改了 <a> & b。"}},
		Open:           todosOf("甲"),
		Turn:           convmodel.Turn{Items: []convmodel.Item{userItem("請修好"), stepItem("Bash", "go test"), agentItem("好了。")}},
	}
	want := `{"previous_status":"在修登入","recent_entries":[{"thing":"登入","entry":"改了 <a> & b。"}],` +
		`"open_todos":[{"n":1,"title":"甲","detail":"細節甲"}],"dropped_titles":[],` +
		`"turn":{"user_prompt":"請修好","assistant_text":"好了。","tools":["Bash: go test"]}}`
	for i := 0; i < 3; i++ {
		got, _, err := BuildTurnInput(in)
		if err != nil || got != want {
			t.Fatalf("got  %s\nwant %s\nerr %v", got, want, err)
		}
	}
}

func TestBuildTurnInput_OnlyTheLastThreeRecentEntries(t *testing.T) {
	in := TurnSource{Recent: []Entry{{Entry: "1"}, {Entry: "2"}, {Entry: "3"}, {Entry: "4"}, {Entry: "5"}}}
	d := parse(t, mustBuild(t, in))
	if len(d.RecentEntries) != 3 || d.RecentEntries[0].Entry != "3" || d.RecentEntries[2].Entry != "5" {
		t.Fatalf("recent = %+v", d.RecentEntries)
	}
}

func mustBuild(t *testing.T, in TurnSource) string {
	t.Helper()
	s, _, err := BuildTurnInput(in)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The refresh prompt is filled with the status and the numbered open todos; the dropped titles are [] in v2.
func TestBuildRefreshInput(t *testing.T) {
	prompt, ids := BuildRefreshInput("在修登入", todosOf("甲", "乙"))
	for _, want := range []string{"- 目前狀況：在修登入", "1：甲 — 細節甲", "2：乙 — 細節乙", "- 使用者刪掉的待辦（永遠不要再加回來）：[]"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("missing %q in\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "{{") {
		t.Errorf("an unfilled placeholder is left:\n%s", prompt)
	}
	if ids[1] != 100 || ids[2] != 101 {
		t.Fatalf("ids = %v", ids)
	}
	// no todos: the list says so rather than leaving a hole
	empty, ids := BuildRefreshInput("", nil)
	if !strings.Contains(empty, "（沒有）") || len(ids) != 0 || strings.Contains(empty, "{{") {
		t.Errorf("empty:\n%s", empty)
	}
}

func TestBuildRefreshInput_RedactsAndCaps(t *testing.T) {
	tok := "sk-ant-api03-AbCdEf123456"
	var names []string
	for i := 0; i < 35; i++ {
		names = append(names, fmt.Sprintf("項目%d", i))
	}
	todos := todosOf(names...)
	todos[0].Title = "看 " + tok
	prompt, ids := BuildRefreshInput("狀況 "+tok, todos)
	if strings.Contains(prompt, "AbCdEf") {
		t.Fatalf("a secret reached the refresh prompt")
	}
	if len(ids) != 30 || strings.Contains(prompt, "項目34") {
		t.Fatalf("ids %d / shows item 34: %v", len(ids), strings.Contains(prompt, "項目34"))
	}
}

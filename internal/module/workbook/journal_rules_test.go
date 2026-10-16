package workbook

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/journal"
)

func confirmEv(items []string, detailStart string) ModelEvent {
	return ModelEvent{Kind: "confirm", From: "88", To: "使用者", Text: "有事要決定。", Items: items, DetailStart: detailStart}
}

func okCtx() JournalContext {
	return JournalContext{UserPrompt: "請繼續", AssistantText: "進度報告。\n\n要你決定：1. 甲？2. 乙？"}
}

// A turn that starts with [pdx team] writes no events, whatever the model says; ticks and roadmap still go through.
// Mutation gate: drop the prefix check (trust the model) -> the events are written (red).
func TestValidateJournal_TeamNoticeWritesNoEvents(t *testing.T) {
	c := okCtx()
	c.UserPrompt = "  [pdx team] 成員 x 完成了任務"
	c.Open = []OpenItem{{N: 1, EntryID: "e1", Text: "甲"}}
	c.Rows = []RoadmapRow{{Feature: "f", Stage: "s"}}
	res := ValidateJournal(JournalOutput{
		Events:      []ModelEvent{{Kind: "comm", Text: "通知"}, confirmEv([]string{"丙"}, "")},
		ConfirmDone: []int{1},
		Roadmap:     []ModelRoadmap{{Feature: "f", Stage: "s", Status: "完成"}},
	}, c)
	if len(res.Events) != 0 {
		t.Fatalf("events = %+v", res.Events)
	}
	if len(res.Ticks) != 1 || len(res.Roadmap) != 1 {
		t.Errorf("ticks %v roadmap %v: the notice only silences the events", res.Ticks, res.Roadmap)
	}
	c.UserPrompt = "[report] 完成"
	if got := ValidateJournal(JournalOutput{Events: []ModelEvent{{Kind: "comm", Text: "通知"}}}, c); len(got.Events) != 1 {
		t.Errorf("only [pdx team] is silenced: %+v", got.Events)
	}
}

// A re-asked question is not written twice: normalised (trim, width fold, case, punctuation) equality with an open item.
// Mutation gate: compare the raw text (no normalisation) -> the re-asked item is written (red).
func TestValidateJournal_ConfirmDedupe(t *testing.T) {
	c := okCtx()
	c.Open = []OpenItem{
		{N: 1, EntryID: "e1", Text: "日誌檔要不要進 git？"},
		{N: 0, EntryID: "e0", Text: "OAuth 要不要？"}, // not sent to the model but still open
	}
	res := ValidateJournal(JournalOutput{Events: []ModelEvent{
		confirmEv([]string{"日誌檔要不要進git", "ＯＡｕｔｈ要不要", "新的問題"}, ""),
	}}, c)
	if len(res.Events) != 1 || len(res.Events[0].Items) != 1 || res.Events[0].Items[0] != "新的問題" {
		t.Fatalf("events = %+v", res.Events)
	}
	// every item already open: the confirm is not written
	res = ValidateJournal(JournalOutput{Events: []ModelEvent{confirmEv([]string{"日誌檔要不要進 Git！"}, "")}}, c)
	if len(res.Events) != 0 {
		t.Fatalf("a confirm left with no items was written: %+v", res.Events)
	}
	// the same question twice in one answer, and across two confirms of one answer
	res = ValidateJournal(JournalOutput{Events: []ModelEvent{
		confirmEv([]string{"問題一", "問題一"}, ""), confirmEv([]string{"問題一", "問題二"}, ""),
	}}, okCtx())
	if len(res.Events) != 2 || len(res.Events[0].Items) != 1 || len(res.Events[1].Items) != 1 || res.Events[1].Items[0] != "問題二" {
		t.Fatalf("events = %+v", res.Events)
	}
	// a question this very answer ticks may be asked again
	c.Open[0].N = 1
	res = ValidateJournal(JournalOutput{ConfirmDone: []int{1}, Events: []ModelEvent{confirmEv([]string{"日誌檔要不要進 git"}, "")}}, c)
	if len(res.Events) != 1 || len(res.Ticks) != 1 {
		t.Fatalf("res = %+v", res)
	}
}

// detail_start without the markdown marks of the reply is still found; the cut keeps the marks in front of the match.
// Mutation gate: exact match only -> falls back to the last 1500 runes (red).
func TestValidateJournal_DetailStartIgnoresMarkdown(t *testing.T) {
	reply := "前面是一大段進度。\n\n## **要你決定**：1. `日誌檔`要不要進 git？2. 按鈕？"
	c := JournalContext{AssistantText: strings.Repeat("填充。", 700) + reply}
	for name, start := range map[string]string{
		"without markup": "要你決定：1. 日誌檔要不要進 git",
		"with markup":    "## **要你決定**：1. `日誌檔`要不要進 git",
		"half markup":    "**要你決定：1. 日誌檔",
	} {
		res := ValidateJournal(JournalOutput{Events: []ModelEvent{confirmEv([]string{"甲"}, start)}}, c)
		if len(res.Events) != 1 {
			t.Fatalf("%s: %+v", name, res)
		}
		want := "## **要你決定**：1. `日誌檔`要不要進 git？2. 按鈕？"
		if res.Events[0].Detail != want {
			t.Errorf("%s: detail = %q, want %q", name, res.Events[0].Detail, want)
		}
	}
	// not found: the last 1500 runes
	res := ValidateJournal(JournalOutput{Events: []ModelEvent{confirmEv([]string{"甲"}, "完全不在回覆裡")}}, c)
	if n := len([]rune(res.Events[0].Detail)); n != fallbackDetail {
		t.Errorf("fallback detail has %d runes, want %d", n, fallbackDetail)
	}
	if !strings.HasSuffix(res.Events[0].Detail, "2. 按鈕？") {
		t.Error("fallback is not the tail of the reply")
	}
	// an empty detail_start, and a short reply
	res = ValidateJournal(JournalOutput{Events: []ModelEvent{confirmEv([]string{"甲"}, "")}}, okCtx())
	if res.Events[0].Detail != "進度報告。\n\n要你決定：1. 甲？2. 乙？" {
		t.Errorf("detail = %q", res.Events[0].Detail)
	}
}

func TestValidateJournal_DetailIsScrubbedAndCapped(t *testing.T) {
	long := "問：" + strings.Repeat("字", 5000)
	res := ValidateJournal(JournalOutput{Events: []ModelEvent{confirmEv([]string{"甲"}, "問：")}}, JournalContext{AssistantText: long})
	if n := len([]rune(res.Events[0].Detail)); n != maxDetail {
		t.Errorf("detail has %d runes, want %d", n, maxDetail)
	}
	// a secret in the cut text never reaches the detail, even when the caller did not scrub the reply
	secret := "sk-ant-api03-" + strings.Repeat("a1B2c3D4", 6)
	res = ValidateJournal(JournalOutput{Events: []ModelEvent{confirmEv([]string{"甲"}, "金鑰")}},
		JournalContext{AssistantText: "金鑰 " + secret + " 要不要換？"})
	if strings.Contains(res.Events[0].Detail, secret) || !strings.Contains(res.Events[0].Detail, "要不要換") {
		t.Errorf("detail = %q", res.Events[0].Detail)
	}
}

// Boundaries: text 80 / 81, item 30 / 31 runes, 10 / 11 items.
// Mutation gate: change each limit by one -> the matching case fails (red).
func TestValidateJournal_Limits(t *testing.T) {
	r := func(n int) string { return strings.Repeat("字", n) }
	res := ValidateJournal(JournalOutput{Events: []ModelEvent{
		{Kind: "comm", Text: r(80)}, {Kind: "comm", Text: r(81)},
		{Kind: "request", By: "user", Text: r(70) + "，" + r(10) + "尾"}, // 81 runes with a comma at 71
		{Kind: "comm", Text: r(70) + "。" + r(20)},
	}}, okCtx())
	got := []string{}
	for _, e := range res.Events {
		got = append(got, e.Text)
	}
	if len(got) != 4 || got[0] != r(80) || got[1] != r(80) || got[2] != r(70) || got[3] != r(70)+"。" {
		t.Fatalf("texts = %q", got)
	}
	items := func(n, size int) []string {
		var out []string
		for i := 0; i < n; i++ {
			out = append(out, string(rune('a'+i))+r(size-1))
		}
		return out
	}
	for _, c := range []struct{ n, size, wantN, wantSize int }{{10, 30, 10, 30}, {11, 31, 10, 30}, {1, 31, 1, 30}, {1, 30, 1, 30}} {
		res := ValidateJournal(JournalOutput{Events: []ModelEvent{confirmEv(items(c.n, c.size), "")}}, okCtx())
		if len(res.Events) != 1 || len(res.Events[0].Items) != c.wantN || len([]rune(res.Events[0].Items[0])) != c.wantSize {
			t.Errorf("%d items of %d runes: %+v", c.n, c.size, res.Events)
		}
	}
}

func TestValidateJournal_TicksAndRoadmap(t *testing.T) {
	c := okCtx()
	c.Open = []OpenItem{{N: 1, EntryID: "e1", ItemIndex: 0}, {N: 2, EntryID: "e1", ItemIndex: 1}, {N: 0, EntryID: "e0", ItemIndex: 0}}
	c.Rows = []RoadmapRow{{Feature: "工作簿日誌", Stage: "J-1", Status: "未開始"}}
	res := ValidateJournal(JournalOutput{
		ConfirmDone: []int{2, 9, 2, 0, -1},
		Roadmap: []ModelRoadmap{
			{Feature: "工作簿日誌", Stage: "J-1", Status: "進行中"},
			{Feature: "工作簿日誌", Stage: "J-9", Status: "完成"}, // a stage the job did not send
			{Feature: "別的", Stage: "J-1", Status: "完成"},
			{Feature: "工作簿日誌", Stage: "J-1", Status: "做完了"}, // not one of the five
		},
	}, c)
	if len(res.Ticks) != 1 || res.Ticks[0] != (ConfirmTick{EntryID: "e1", ItemIndex: 1}) {
		t.Errorf("ticks = %+v (unknown, repeated and not-sent numbers are ignored)", res.Ticks)
	}
	if len(res.Roadmap) != 1 || res.Roadmap[0].Status != "進行中" || res.Dropped != 3 {
		t.Errorf("roadmap = %+v dropped %d", res.Roadmap, res.Dropped)
	}
}

func TestValidateJournal_RequestKeepsByAndActor(t *testing.T) {
	res := ValidateJournal(JournalOutput{Events: []ModelEvent{
		{Kind: "request", By: "member", From: "53-pe", To: "88", Text: "請裁定。"},
		{Kind: "comm", From: "", To: "88", Text: "通知。"},
	}}, okCtx())
	if len(res.Events) != 2 || res.Events[0].Kind != journal.KindRequest || res.Events[0].By != journal.ByMember ||
		res.Events[0].Actor != "53-pe → 88" || res.Events[1].Actor != "88" {
		t.Fatalf("events = %+v", res.Events)
	}
}

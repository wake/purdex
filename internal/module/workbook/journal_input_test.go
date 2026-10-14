package workbook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/journal"
)

func discardLog(string, ...any) {}

func putConfirm(t *testing.T, dir, id string, at time.Time, done []bool, texts ...string) {
	t.Helper()
	e := journal.Entry{ID: id, Kind: journal.KindConfirm, At: at, Actor: "88 → 使用者", Text: "有事要決定。"}
	for i, s := range texts {
		e.Items = append(e.Items, journal.Item{Text: s, Done: i < len(done) && done[i]})
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := journal.Upsert(journal.MonthFile(dir, at), e); err != nil {
		t.Fatal(err)
	}
}

// asked counts the confirm entries that still have open items (a fully ticked one does not count); idx is the item's
// place in its entry, ticked items keep their place; the open list crosses month files in order.
// Mutation gate: idx counted among the open items only (not the entry's place) -> red.
func TestReadOpenItems_AskedAndIdx(t *testing.T) {
	dir := t.TempDir()
	d0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	d1 := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	putConfirm(t, dir, "c1", d0, []bool{true, true}, "全部勾過", "也勾過")
	putConfirm(t, dir, "c2", d0.Add(time.Hour), []bool{true, false, false}, "二-1", "二-2", "二-3")
	putConfirm(t, dir, "c3", d1, nil, "三-1")
	if err := journal.Upsert(journal.MonthFile(dir, d1), journal.Entry{ID: "x1", Kind: journal.KindComm, At: d1, Text: "不是 confirm"}); err != nil {
		t.Fatal(err)
	}
	got := ReadOpenItems(dir, discardLog)
	want := []OpenItem{
		{N: 1, Asked: 1, Idx: 2, EntryID: "c2", ItemIndex: 1, Text: "二-2"},
		{N: 2, Asked: 1, Idx: 3, EntryID: "c2", ItemIndex: 2, Text: "二-3"},
		{N: 3, Asked: 2, Idx: 1, EntryID: "c3", ItemIndex: 0, Text: "三-1"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if ReadOpenItems(filepath.Join(dir, "nope"), discardLog) != nil || ReadOpenItems("", discardLog) != nil {
		t.Error("a missing folder has no items")
	}
}

// At most 30 are sent, the newest ones; the older open items are kept for the re-ask check but get no number.
// Mutation gate: number the first 30 instead of the last 30 -> red.
func TestReadOpenItems_NewestThirtyAreNumbered(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 32; i++ {
		putConfirm(t, dir, fmt.Sprintf("c%02d", i), at.Add(time.Duration(i)*time.Minute), nil, fmt.Sprintf("問題%02d", i))
	}
	got := ReadOpenItems(dir, discardLog)
	if len(got) != 32 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].N != 0 || got[1].N != 0 || got[2].N != 1 || got[2].Text != "問題02" || got[31].N != 30 || got[31].Text != "問題31" {
		t.Errorf("numbering: %+v %+v %+v %+v", got[0], got[1], got[2], got[31])
	}
	// the n -> entry map of the sent ones is what a tick is resolved by
	res := ValidateJournal(JournalOutput{ConfirmDone: []int{1, 30, 31}}, JournalContext{Open: got})
	if len(res.Ticks) != 2 || res.Ticks[0].EntryID != "c02" || res.Ticks[1].EntryID != "c31" {
		t.Errorf("ticks = %+v", res.Ticks)
	}
}

const testRoadmap = "# Roadmap\n\n## 工作簿日誌\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n| J-1b | daemon 寫入 | 未開始 |\n| J-1c | 任務鏡像 | 未開始 |\n\n" +
	"## 名字池\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n| NP-1 | 名單 | 進行中 |\n\n## 別的\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n| ZZ-9 | x | 完成 |\n"

func writeFile(t *testing.T, name, text string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(f, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// The features the unit touched come first; the others follow; at most 40 rows. A missing file is empty and quiet, an
// unparsable one is empty and logged.
// Mutation gate: no touched-first ordering -> red; cap 41 -> red.
func TestReadRoadmapRows(t *testing.T) {
	f := writeFile(t, "roadmap.md", testRoadmap)
	got := ReadRoadmapRows(f, "NP-1 已經 merge", discardLog)
	if len(got) != 4 || got[0].Feature != "名字池" || got[0].Stage != "NP-1" || got[0].Status != "進行中" || got[0].Desc != "名單" ||
		got[1].Stage != "J-1b" || got[3].Stage != "ZZ-9" {
		t.Fatalf("rows = %+v", got)
	}
	if rows := ReadRoadmapRows(f, "工作簿日誌 的事", discardLog); rows[0].Feature != "工作簿日誌" || rows[2].Feature != "名字池" {
		t.Errorf("a feature named in the text is touched: %+v", rows)
	}
	var logs []string
	logf := func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	if rows := ReadRoadmapRows(filepath.Join(t.TempDir(), "roadmap.md"), "", logf); rows != nil || len(logs) != 0 {
		t.Errorf("missing file: rows %v logs %v", rows, logs)
	}
	if rows := ReadRoadmapRows(writeFile(t, "roadmap.md", "## F\n\n| 階段 | 說明 |\n| a | b |\n"), "", logf); rows != nil || len(logs) != 1 {
		t.Errorf("unparsable: rows %v logs %v", rows, logs)
	}
	var big strings.Builder
	big.WriteString("## 大\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n")
	for i := 0; i < 45; i++ {
		fmt.Fprintf(&big, "| S-%02d | d | 未開始 |\n", i)
	}
	if rows := ReadRoadmapRows(writeFile(t, "roadmap.md", big.String()), "", logf); len(rows) != 40 || rows[39].Stage != "S-39" {
		t.Errorf("cap: %d rows", len(rows))
	}
}

func testTurn() convmodel.Turn {
	return convmodel.Turn{ID: "t1", Outcome: convmodel.OutcomeDone, Items: []convmodel.Item{
		userItem("[88 → 1f] 請查 <a> & b"), agentItem("先說的"), agentItem("最後說的：要你決定 sk-ant-api03-" + strings.Repeat("a1B2c3D4", 6)),
	}}
}

// The prompt is the JSON of the prompt document §2: the same turn words the v2 job gets, the numbering of the seed, empty
// lists as [] and not null, secrets scrubbed, HTML left as written.
func TestBuildJournalInput(t *testing.T) {
	seed := JournalSeed{UnitRole: "leader", SelfName: "purdex-88-b8",
		Open: []OpenItem{{N: 0, Asked: 1, Idx: 1, EntryID: "old", Text: "太舊沒送"}, {N: 1, Asked: 2, Idx: 3, EntryID: "c3", Text: "日誌檔要不要進 git"}},
		Rows: []RoadmapRow{{Feature: "工作簿日誌", Stage: "J-1", Desc: "daemon", Status: "未開始"}}}
	prompt, ctx, err := BuildJournalInput(seed, testTurn())
	if err != nil {
		t.Fatal(err)
	}
	var in struct {
		UnitRole string `json:"unit_role"`
		SelfName string `json:"self_name"`
		Turn     struct {
			UserPrompt    string   `json:"user_prompt"`
			AssistantText string   `json:"assistant_text"`
			Tools         []string `json:"tools"`
		} `json:"turn"`
		OpenConfirms []map[string]any `json:"open_confirms"`
		RoadmapRows  []map[string]any `json:"roadmap_rows"`
	}
	if err := json.Unmarshal([]byte(prompt), &in); err != nil {
		t.Fatal(err)
	}
	if in.UnitRole != "leader" || in.SelfName != "purdex-88-b8" || in.Turn.UserPrompt != "[88 → 1f] 請查 <a> & b" ||
		!strings.HasPrefix(in.Turn.AssistantText, "最後說的：要你決定") || strings.Contains(prompt, "sk-ant-api03") || in.Turn.Tools == nil {
		t.Fatalf("input = %+v", in)
	}
	if !strings.Contains(prompt, "<a> & b") {
		t.Error("HTML was escaped")
	}
	if len(in.OpenConfirms) != 1 || in.OpenConfirms[0]["n"] != 1.0 || in.OpenConfirms[0]["asked"] != 2.0 || in.OpenConfirms[0]["idx"] != 3.0 ||
		in.OpenConfirms[0]["text"] != "日誌檔要不要進 git" || len(in.RoadmapRows) != 1 || in.RoadmapRows[0]["desc"] != "daemon" {
		t.Errorf("lists = %+v %+v", in.OpenConfirms, in.RoadmapRows)
	}
	// the v2 job sees exactly the same turn words
	v2, _, err := BuildTurnInput(TurnSource{Turn: testTurn()})
	if err != nil {
		t.Fatal(err)
	}
	var v2in struct{ Turn json.RawMessage }
	_ = json.Unmarshal([]byte(v2), &v2in)
	var j2 struct{ Turn json.RawMessage }
	_ = json.Unmarshal([]byte(prompt), &j2)
	if string(v2in.Turn) != string(j2.Turn) {
		t.Errorf("turn parts differ:\n%s\n%s", v2in.Turn, j2.Turn)
	}
	// the context: the whole reply (scrubbed), the open items with their map, the rows as sent
	if !strings.HasPrefix(ctx.AssistantText, "最後說的") || strings.Contains(ctx.AssistantText, "sk-ant-api03") ||
		ctx.UserPrompt != in.Turn.UserPrompt || len(ctx.Open) != 2 || ctx.Open[1].EntryID != "c3" || len(ctx.Rows) != 1 {
		t.Errorf("ctx = %+v", ctx)
	}
	// empty lists are arrays
	empty, _, _ := BuildJournalInput(JournalSeed{UnitRole: "solo"}, testTurn())
	if !strings.Contains(empty, `"open_confirms":[]`) || !strings.Contains(empty, `"roadmap_rows":[]`) {
		t.Errorf("empty = %s", empty)
	}
	again, _, _ := BuildJournalInput(seed, testTurn())
	if again != prompt {
		t.Error("not byte-stable")
	}
}

func TestUnitRoleOf(t *testing.T) {
	for seat, want := range map[string]string{"lead": "leader", "none": "solo", "member": "", "member_remote": "", "": ""} {
		if got, ok := unitRoleOf(seat); got != want || ok != (want != "") {
			t.Errorf("%q -> %q %v", seat, got, ok)
		}
	}
}

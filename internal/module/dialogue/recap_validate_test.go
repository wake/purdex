package dialogue

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

const goodRecap = `{"headline": "h", "done": ["a", "b"], "now": "n", "decided": ["d"], "waiting": ["w"], "next": "x"}`

// 1: parseRecap accepts exactly the six fields of the right types.
func TestParseRecap_Table(t *testing.T) {
	cases := []struct {
		name, in string
		ok       bool
	}{
		{"all six", goodRecap, true},
		{"code fence", "```json\n" + goodRecap + "\n```", true},
		{"empty lists", `{"headline":"h","done":[],"now":"","decided":[],"waiting":[],"next":""}`, true},
		{"missing next", `{"headline":"h","done":[],"now":"n","decided":[],"waiting":[]}`, false},
		{"missing done", `{"headline":"h","now":"n","decided":[],"waiting":[],"next":"x"}`, false},
		{"extra field", `{"headline":"h","done":[],"now":"n","decided":[],"waiting":[],"next":"x","more":1}`, false},
		{"repeated field", `{"headline":"h","headline":"g","done":[],"now":"n","decided":[],"waiting":[],"next":"x"}`, false},
		{"headline not a string", `{"headline":1,"done":[],"now":"n","decided":[],"waiting":[],"next":"x"}`, false},
		{"done not an array", `{"headline":"h","done":"a","now":"n","decided":[],"waiting":[],"next":"x"}`, false},
		{"done item not a string", `{"headline":"h","done":[1],"now":"n","decided":[],"waiting":[],"next":"x"}`, false},
		{"waiting null", `{"headline":"h","done":[],"now":"n","decided":[],"waiting":null,"next":"x"}`, false},
		{"not json", `hello`, false},
		{"trailing text", goodRecap + " tail", false},
		{"two fences", "```\n" + goodRecap + "\n```\n```\n" + goodRecap + "\n```", false},
	}
	for _, c := range cases {
		_, err := parseRecap(c.in)
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok && !errors.Is(err, errFormat) {
			t.Errorf("%s: err = %v, want errFormat", c.name, err)
		}
	}
	if got, _ := parseRecap(goodRecap); got.Headline != "h" || len(got.Done) != 2 || got.Now != "n" || got.Next != "x" {
		t.Fatalf("parsed = %+v", got)
	}
}

func TestRepairRecap_Limits(t *testing.T) {
	long := func(n int) string { return strings.Repeat("字", n) }
	o := repairRecap(recapOut{
		Headline: long(70), Now: long(90), Next: long(70),
		Done:    []string{long(50), "2", "3", "4", "5", "6", "7", "8"},
		Decided: []string{long(41), "", "  "},
		Waiting: []string{"", "ok"},
	})
	if n := len([]rune(o.Headline)); n != 60 {
		t.Errorf("headline %d", n)
	}
	if n := len([]rune(o.Now)); n != 80 {
		t.Errorf("now %d", n)
	}
	if n := len([]rune(o.Next)); n != 60 {
		t.Errorf("next %d", n)
	}
	if len(o.Done) != 6 || len([]rune(o.Done[0])) != 40 || o.Done[5] != "6" {
		t.Errorf("done = %v", o.Done)
	}
	if len(o.Decided) != 1 || len([]rune(o.Decided[0])) != 40 {
		t.Errorf("decided = %v", o.Decided)
	}
	if !reflect.DeepEqual(o.Waiting, []string{"ok"}) {
		t.Errorf("waiting = %v", o.Waiting)
	}
}

// Empty items are dropped before the first six are taken; secrets are redacted; a cut respects word boundaries.
func TestRepairRecap_DropsEmptiesFirstRedactsAndCutsAtWords(t *testing.T) {
	o := repairRecap(recapOut{Headline: "h", Done: []string{"", "1", "", "2", "3", "4", "5", "6", "7"}})
	if !reflect.DeepEqual(o.Done, []string{"1", "2", "3", "4", "5", "6"}) {
		t.Errorf("done = %v", o.Done)
	}
	o = repairRecap(recapOut{Headline: strings.Repeat("字", 50) + " internal/module/dialogue/recap.go 之後", Next: "key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789 end"})
	if strings.Contains(o.Next, "abcdefghijklmnop") {
		t.Errorf("secret kept: %q", o.Next)
	}
	if strings.Contains(o.Headline, "internal/mo") {
		t.Errorf("cut inside a path: %q", o.Headline)
	}
}

// 4: the block as it stands in the file.
func TestRecapBlock_Format(t *testing.T) {
	o := recapOut{Headline: "做完 T9", Done: []string{"摘要", "驗證"}, Now: "寫 T10", Decided: []string{"用 haiku"}, Waiting: []string{"等使用者"}, Next: "合併"}
	r := RecapRow{At: 1_800_000_000_000, FromNo: 3, ToNo: 12}
	want := "<recap " + localHM(r.At) + " | turns 3–12>\nheadline: 做完 T9\ndone:\n- 摘要\n- 驗證\nnow: 寫 T10\ndecided:\n- 用 haiku\nwaiting:\n- 等使用者\nnext: 合併\n"
	if got := recapText(r, o, false); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	carried := recapText(r, o, true)
	if !strings.HasPrefix(carried, "<carried recap "+localHM(r.At)+" | turns 3–12>\nheadline: 做完 T9\n") || strings.Contains(carried[1:], "<recap") {
		t.Fatalf("carried = %s", carried)
	}
	// empty lists and empty now/next leave their lines out
	got := recapText(r, recapOut{Headline: "h", Done: []string{"a"}}, false)
	if got != "<recap "+localHM(r.At)+" | turns 3–12>\nheadline: h\ndone:\n- a\n" {
		t.Fatalf("sparse = %q", got)
	}
}

// planCodeBlock returns the first code block after the plan's heading that starts with head.
func planCodeBlock(t *testing.T, head string) string {
	t.Helper()
	raw, err := os.ReadFile("../../../docs/plans/2026-11-01-session-dialogue-log-plan.md")
	if err != nil {
		t.Fatal(err)
	}
	plan := string(raw)
	i := strings.Index(plan, head)
	if i < 0 {
		t.Fatalf("%s not found in the plan", head)
	}
	rest := plan[i:]
	open := strings.Index(rest, "```\n")
	if open < 0 {
		t.Fatal("code block not found")
	}
	rest = rest[open+4:]
	end := strings.Index(rest, "\n```")
	if end < 0 {
		t.Fatal("code block is not closed")
	}
	return rest[:end]
}

// The embedded prompt is the plan's appendix B-2 code block, word for word.
func TestRecapPrompt_MatchesPlanAppendix(t *testing.T) {
	want := planCodeBlock(t, "### B-2 ")
	if strings.TrimRight(recapPrompt, "\n") != want {
		t.Fatalf("prompts/recap-v1.txt differs from plan appendix B-2")
	}
	if !strings.Contains(want, `{"headline": "…", "done": ["…"]`) {
		t.Fatal("extracted block is not the recap prompt")
	}
}

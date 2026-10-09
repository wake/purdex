package workbook

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func runes(s string) int { return utf8.RuneCountInString(s) }

func TestParseModelJSON(t *testing.T) {
	good := `{"skip":false,"thing":"P6-5 接力指令","push":"審查問題已修","entry":"做了一件事。理由。結果。","status":"等 lead 放行","thing_done":true}`
	for name, in := range map[string]string{
		"plain":           good,
		"fenced":          "```json\n" + good + "\n```",
		"fenced no lang":  "```\n" + good + "\n```",
		"padded":          "\n  " + good + "  \n",
		"fence with text": "```json\n" + good + "\n```\n",
	} {
		s, err := ParseModelJSON(in)
		if err != nil || s.Thing != "P6-5 接力指令" || !s.ThingDone || s.Skip || s.Status != "等 lead 放行" {
			t.Errorf("%s: %+v err=%v", name, s, err)
		}
	}
}

func TestParseModelJSON_FormatErrors(t *testing.T) {
	for name, in := range map[string]string{
		"empty":          "",
		"prose":          "好的，這是結果：",
		"truncated":      `{"skip":false,"thing":"x"`,
		"array":          `[1,2]`,
		"wrong type":     `{"skip":"no","thing":"x","entry":"e"}`,
		"no thing":       `{"skip":false,"thing":"","push":"p","entry":"e","status":"s"}`,
		"no entry":       `{"skip":false,"thing":"t","push":"p","entry":"","status":"s"}`,
		"two fences":     "```json\n{}\n```\n```json\n{}\n```",
		"trailing prose": `{"skip":false,"thing":"t","entry":"e"} 以上`,
	} {
		if _, err := ParseModelJSON(in); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v, want ErrFormat", name, err)
		}
	}
	// a skip needs nothing else
	if s, err := ParseModelJSON(`{"skip":true,"thing":"","push":"","entry":"","status":"前情","thing_done":false}`); err != nil || !s.Skip {
		t.Errorf("skip: %+v err=%v", s, err)
	}
}

func TestRepair_PushCutAtLastPunctuationOrDropped(t *testing.T) {
	long := "審查指出兩個問題都已經修好了，接著等 lead 放行之後就可以直接 merge 並且請 1f 排部署，順便更新文件"
	if runes(long) <= 40 {
		t.Fatal("fixture too short")
	}
	got, _ := Repair(Summary{Thing: "t", Push: long, Entry: "e。"})
	if got.Push != "審查指出兩個問題都已經修好了" || runes(got.Push) > 40 {
		t.Fatalf("push = %q", got.Push)
	}
	// no punctuation inside the first 40 → dropped
	got, _ = Repair(Summary{Thing: "t", Push: strings.Repeat("字", 50), Entry: "e。"})
	if got.Push != "" {
		t.Fatalf("push = %q, want dropped", got.Push)
	}
	// exactly 40 stays
	keep := strings.Repeat("字", 40)
	if got, _ = Repair(Summary{Thing: "t", Push: keep, Entry: "e。"}); got.Push != keep {
		t.Fatalf("a 40-rune push was changed: %q", got.Push)
	}
}

func TestRepair_ThingAndStatus(t *testing.T) {
	got, _ := Repair(Summary{Thing: strings.Repeat("事", 20), Entry: "e。", Status: strings.Repeat("甲。", 120)})
	if runes(got.Thing) != 16 {
		t.Fatalf("thing = %d runes", runes(got.Thing))
	}
	if runes(got.Status) > 200 || !strings.HasSuffix(got.Status, "。") {
		t.Fatalf("status = %d runes, %q", runes(got.Status), got.Status[len(got.Status)-6:])
	}
	// status with no sentence end inside 200 is cut hard at 200
	got, _ = Repair(Summary{Thing: "t", Entry: "e。", Status: strings.Repeat("字", 250)})
	if runes(got.Status) != 200 {
		t.Fatalf("hard cut = %d", runes(got.Status))
	}
}

func TestRepair_EntryOverLimitAsksForARewrite(t *testing.T) {
	long := strings.Repeat("這是一個很長的句子。", 20) // 200 runes
	got, rewrite := Repair(Summary{Thing: "t", Entry: long})
	if !rewrite || got.Entry != long {
		t.Fatalf("rewrite=%v, entry changed=%v", rewrite, got.Entry != long)
	}
	if _, rewrite = Repair(Summary{Thing: "t", Entry: strings.Repeat("字", 150)}); rewrite {
		t.Fatal("a 150-rune entry needs no rewrite")
	}
}

func TestCutEntry_LastSentenceEndWithin150(t *testing.T) {
	s := strings.Repeat("甲", 100) + "。" + strings.Repeat("乙", 100) + "。"
	got := CutEntry(s)
	if got != strings.Repeat("甲", 100)+"。" {
		t.Fatalf("cut = %q", got)
	}
	if got := CutEntry(strings.Repeat("字", 200)); runes(got) != 150 {
		t.Fatalf("no sentence end → hard cut at 150, got %d", runes(got))
	}
	if got := CutEntry("短。"); got != "短。" {
		t.Fatalf("short entry changed: %q", got)
	}
}

// Every output string goes through the redactor, a model that echoes a token included.
// Mutation gate: skip the redaction in Repair → red.
func TestRepair_RedactsEveryField(t *testing.T) {
	tok := "sk-ant-api03-AbCdEf123456"
	got, _ := Repair(Summary{Thing: "用 " + tok, Push: "Bearer " + tok, Entry: "貼了 " + tok + "。", Status: tok})
	for name, v := range map[string]string{"thing": got.Thing, "push": got.Push, "entry": got.Entry, "status": got.Status} {
		if strings.Contains(v, "AbCdEf") {
			t.Errorf("%s leaks the token: %q", name, v)
		}
	}
}

func TestRepair_ChineseSentenceEndsAndASCII(t *testing.T) {
	got, _ := Repair(Summary{Thing: "t", Entry: "e。", Status: strings.Repeat("a", 150) + "! " + strings.Repeat("b", 80)})
	if got.Status != strings.Repeat("a", 150)+"!" {
		t.Fatalf("status = %q", got.Status)
	}
}

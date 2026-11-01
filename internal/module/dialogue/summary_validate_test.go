package dialogue

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseSummary_Table(t *testing.T) {
	ok := `{"skip": false, "title": "標題", "push": "推播", "summary": "內文。"}`
	cases := []struct {
		name    string
		in      string
		want    summaryOut
		wantErr bool
	}{
		{"all four fields", ok, summaryOut{Title: "標題", Push: "推播", Summary: "內文。"}, false},
		{"surrounding whitespace", "\n  " + ok + "\n", summaryOut{Title: "標題", Push: "推播", Summary: "內文。"}, false},
		{"missing skip", `{"title":"a","push":"b","summary":"c"}`, summaryOut{}, true},
		{"missing title", `{"skip":false,"push":"b","summary":"c"}`, summaryOut{}, true},
		{"missing push", `{"skip":false,"title":"a","summary":"c"}`, summaryOut{}, true},
		{"missing summary", `{"skip":false,"title":"a","push":"b"}`, summaryOut{}, true},
		{"extra field", `{"skip":false,"title":"a","push":"b","summary":"c","status":"x"}`, summaryOut{}, true},
		{"duplicate field", `{"skip":false,"title":"a","title":"z","push":"b","summary":"c"}`, summaryOut{}, true},
		{"code fence", "```json\n" + ok + "\n```", summaryOut{Title: "標題", Push: "推播", Summary: "內文。"}, false},
		{"bare code fence", "```\n" + ok + "\n```", summaryOut{Title: "標題", Push: "推播", Summary: "內文。"}, false},
		{"two code fences", "```json\n" + ok + "\n```\n```json\n" + ok + "\n```", summaryOut{}, true},
		{"text after the object", ok + " 謝謝", summaryOut{}, true},
		{"two objects", ok + ok, summaryOut{}, true},
		{"text before the object", "好的：" + ok, summaryOut{}, true},
		{"skip with empty fields", `{"skip":true,"title":"","push":"","summary":""}`, summaryOut{Skip: true}, false},
		{"empty title", `{"skip":false,"title":"  ","push":"b","summary":"c"}`, summaryOut{}, true},
		{"empty summary", `{"skip":false,"title":"a","push":"b","summary":""}`, summaryOut{}, true},
		{"empty push is fine", `{"skip":false,"title":"a","push":"","summary":"c"}`, summaryOut{Title: "a", Summary: "c"}, false},
		{"skip is a string", `{"skip":"false","title":"a","push":"b","summary":"c"}`, summaryOut{}, true},
		{"title is a number", `{"skip":false,"title":1,"push":"b","summary":"c"}`, summaryOut{}, true},
		{"not an object", `["a"]`, summaryOut{}, true},
		{"not json", `好的`, summaryOut{}, true},
		{"empty", ``, summaryOut{}, true},
		{"too big", `{"skip":true,"title":"","push":"","summary":"` + strings.Repeat("x", maxModelJSON) + `"}`, summaryOut{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseSummary(c.in)
			if c.wantErr {
				if !errors.Is(err, errFormat) {
					t.Fatalf("err = %v, want errFormat", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestCutAtWord_Table(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"fits", "short title", 16, "short title"},
		{"english word backs off", "implement the parser fix", 15, "implement the"},
		{"cut lands on a space", "implement the parser", 13, "implement the"},
		{"path backs off", "update the file at internal/module/dialogue/queue.go", 30, "update the file at"},
		{"pr number backs off", "合併 PR 2222 完成", 9, "合併 PR"},
		{"cjk and english mixed", "完成修正parser錯誤", 7, "完成修正"},
		{"cjk is always a boundary", "中文字之間都算邊界", 4, "中文字之"},
		{"one long alnum run is cut hard", "abcdefghijklmnopqrstuvwxyz", 10, "abcdefghij"},
		{"back off would leave under half", "ab internal/module/dialogue", 12, "ab internal/"},
		{"dot dash underscore join a run", "do the thing foo_bar-baz.v2 now", 20, "do the thing"},
		{"empty", "", 5, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cutAtWord(c.in, c.n); got != c.want {
				t.Fatalf("cutAtWord(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
			}
		})
	}
}

func TestRepairSummary_Table(t *testing.T) {
	long := strings.Repeat("字", 200)
	cases := []struct {
		name string
		in   summaryOut
		want summaryOut
	}{
		{"within limits is untouched", summaryOut{Title: "標題", Push: "推播。", Summary: "一句。"}, summaryOut{Title: "標題", Push: "推播。", Summary: "一句。"}},
		{"title cut to 16 without an ellipsis", summaryOut{Title: strings.Repeat("字", 20), Summary: "x"}, summaryOut{Title: strings.Repeat("字", 16), Summary: "x"}},
		{"title cut at a word", summaryOut{Title: "alpha.650 deployment finished", Summary: "x"}, summaryOut{Title: "alpha.650", Summary: "x"}},
		{"push over 40 cut at the last punctuation", summaryOut{Title: "t", Push: strings.Repeat("字", 30) + "，" + strings.Repeat("字", 30), Summary: "x"},
			summaryOut{Title: "t", Push: strings.Repeat("字", 30), Summary: "x"}},
		{"push with no punctuation is dropped", summaryOut{Title: "t", Push: strings.Repeat("字", 50), Summary: "x"}, summaryOut{Title: "t", Summary: "x"}},
		{"summary cut at the last sentence end within 150", summaryOut{Title: "t", Summary: "第一句。" + strings.Repeat("字", 146) + "。" + long},
			summaryOut{Title: "t", Summary: "第一句。"}},
		{"summary sentence end exactly at 150", summaryOut{Title: "t", Summary: strings.Repeat("字", 149) + "。" + long}, summaryOut{Title: "t", Summary: strings.Repeat("字", 149) + "。"}},
		{"summary with no sentence end gets an ellipsis", summaryOut{Title: "t", Summary: long}, summaryOut{Title: "t", Summary: strings.Repeat("字", 149) + "…"}},
		{"summary with no sentence end cut at a word", summaryOut{Title: "t", Summary: strings.Repeat("字", 140) + " internal/module/dialogue/queue.go"},
			summaryOut{Title: "t", Summary: strings.Repeat("字", 140) + "…"}},
		{"skip keeps its flag", summaryOut{Skip: true}, summaryOut{Skip: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := repairSummary(c.in)
			if got != c.want {
				t.Fatalf("got %+v\nwant %+v", got, c.want)
			}
		})
	}
	// the limits themselves
	got := repairSummary(summaryOut{Title: long, Push: long, Summary: long})
	if utf8.RuneCountInString(got.Title) > 16 || utf8.RuneCountInString(got.Push) > 40 || utf8.RuneCountInString(got.Summary) > 151 {
		t.Fatalf("over the limits: %+v", got)
	}
}

func TestRepairSummary_Redacts(t *testing.T) {
	secret := "sk-ant-api03-" + strings.Repeat("a", 40)
	got := repairSummary(summaryOut{Title: "t " + secret, Push: "p " + secret, Summary: "s " + secret})
	for _, f := range []string{got.Title, got.Push, got.Summary} {
		if strings.Contains(f, secret) {
			t.Fatalf("a secret survived: %q", f)
		}
	}
}

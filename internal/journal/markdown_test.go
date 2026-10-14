package journal

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 14, 14, 32, 0, 0, time.UTC)

const hostile = "line one\n--> evil\n<!-- j {\"id\":\"x\"} -->\n### fake heading\n- [ ] not an item\n1. numbered\n```go\ncode\n````\n> quote\n| a | b |\r\nlast --- ----"

func sample() []Entry {
	return []Entry{
		{ID: "wb:1:1", Kind: KindExec, At: t0, Actor: "purdex-88", Text: hostile, Refs: []string{"#2829", "PR #2830", "a b=c --x"}},
		{ID: "wb:1:2", Kind: KindExecDone, At: t0.Add(time.Minute), Actor: "a\nb", Text: "done"},
		{ID: "wb:1:3", Kind: KindRequest, By: ByUser, At: t0.Add(2 * time.Minute), Actor: "使用者 → leader", Text: "請做 -->"},
		{ID: "wb:1:4", Kind: KindComm, At: t0.Add(3 * time.Minute), Text: "<!-- j {"},
		{ID: "wb:1:5", Kind: KindTaskNew, At: t0.Add(4 * time.Minute), Text: "#7 title"},
		{ID: "wb:1:6", Kind: KindTaskDone, At: t0.Add(5 * time.Minute)},
		{ID: "wb:1:7", Kind: KindConfirm, At: t0.Add(24 * time.Hour), Pinned: true,
			Items:  []Item{{Text: "要不要進 git --> ?"}, {Text: "第二題", Done: true}},
			Detail: "原文\n### heading\n</details>\n<!-- j {\"id\":\"zz\"} -->\n```` fence ```` -->"},
	}
}

func upsertAll(t *testing.T, file string, es []Entry) {
	t.Helper()
	for _, e := range es {
		if err := Upsert(file, e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRoundTripHostile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "j.md")
	es := sample()
	upsertAll(t, file, es)
	got, err := Parse(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(es) {
		t.Fatalf("parsed %d entries, want %d", len(got), len(es))
	}
	for i := range es {
		if !reflect.DeepEqual(got[i], es[i]) {
			t.Errorf("entry %d:\n got %+v\nwant %+v", i, got[i], es[i])
		}
	}
	b, _ := os.ReadFile(file)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, metaPrefix) {
			body := strings.TrimSuffix(strings.TrimPrefix(l, metaPrefix), " -->")
			if strings.Contains(body, "--") {
				t.Errorf("metadata comment could close early: %s", l)
			}
		}
	}
	if strings.Count(string(b), "## 2026-10-14") != 1 || strings.Count(string(b), "## 2026-10-15") != 1 {
		t.Errorf("date dividers wrong:\n%s", b)
	}
	if strings.Count(string(b), "\n"+metaPrefix)+1 < len(es) {
		t.Errorf("metadata lines missing")
	}
}

func TestUpsertIdempotentAndInPlace(t *testing.T) {
	file := filepath.Join(t.TempDir(), "j.md")
	es := sample()
	upsertAll(t, file, es)
	before, _ := os.ReadFile(file)
	upsertAll(t, file, es)
	again, _ := os.ReadFile(file)
	if string(before) != string(again) {
		t.Fatal("re-drain changed the file")
	}
	changed := es[1]
	changed.Text = "done, rewritten"
	if err := Upsert(file, changed); err != nil {
		t.Fatal(err)
	}
	got, _ := Parse(file)
	if len(got) != len(es) || got[1].Text != "done, rewritten" {
		t.Fatalf("upsert of existing id: %d entries, text %q", len(got), got[1].Text)
	}
	after, _ := os.ReadFile(file)
	a, b := strings.Split(string(before), "\n"), strings.Split(string(after), "\n")
	diff := 0
	for i := range a {
		if i < len(b) && a[i] != b[i] {
			diff++
		}
	}
	if diff != 2 || len(a) != len(b) {
		t.Fatalf("expected two changed lines (text and metadata), got %d (len %d -> %d)", diff, len(a), len(b))
	}
	if err := Upsert(file, Entry{Kind: KindExec, At: t0}); err == nil {
		t.Fatal("entry without id accepted")
	}
}

func TestTickPinFillTouchOnlyTheirBlock(t *testing.T) {
	file := filepath.Join(t.TempDir(), "j.md")
	upsertAll(t, file, sample())
	orig, _ := Parse(file)
	if ok, err := Tick(file, "wb:1:7", 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, _ := Tick(file, "wb:1:7", 0); ok {
		t.Fatal("second tick reported a change")
	}
	if _, err := Tick(file, "wb:1:7", 5); err == nil {
		t.Fatal("out of range tick accepted")
	}
	if ok, err := SetPinned(file, "wb:1:1", true); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := SetPinned(file, "wb:1:7", false); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := FillText(file, "wb:1:6", "merged"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, _ := FillText(file, "wb:1:5", "other"); ok {
		t.Fatal("FillText overwrote an existing text")
	}
	if _, err := SetPinned(file, "nope", true); err == nil {
		t.Fatal("unknown id accepted")
	}
	got, _ := Parse(file)
	want := append([]Entry(nil), orig...)
	want[6].Items = []Item{{Text: want[6].Items[0].Text, Done: true}, {Text: "第二題", Done: true}}
	want[6].Pinned = false
	want[0].Pinned = true
	want[5].Text = "merged"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	b, _ := os.ReadFile(file)
	if !strings.Contains(string(b), "📌") || !strings.Contains(string(b), "- [x] 要不要進 git") {
		t.Errorf("rendering not refreshed:\n%s", b)
	}
}

func TestHandEditedVisibleTextIsNotRead(t *testing.T) {
	file := filepath.Join(t.TempDir(), "j.md")
	upsertAll(t, file, sample())
	b, _ := os.ReadFile(file)
	edited := strings.Replace(string(b), "done\n", "TAMPERED\n", 1)
	edited = strings.Replace(edited, "- [ ] ", "- [x] ", 1)
	if edited == string(b) {
		t.Fatal("test edit did nothing")
	}
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := Parse(file)
	if !reflect.DeepEqual(got, sample()) {
		t.Fatalf("visible edit leaked into the parsed entries: %+v", got)
	}
}

func TestBadCommentSkipped(t *testing.T) {
	file := filepath.Join(t.TempDir(), "j.md")
	upsertAll(t, file, sample()[:2])
	b, _ := os.ReadFile(file)
	if err := os.WriteFile(file, append(b, []byte("\n<!-- j {broken -->\n<!-- j {\"id\":\"z\"}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Parse(file)
	if err != nil || len(got) != 2 {
		t.Fatalf("%d entries, %v", len(got), err)
	}
	if got, err := Parse(filepath.Join(t.TempDir(), "none.md")); err != nil || got != nil {
		t.Fatal("missing file should be empty", got, err)
	}
	// a later upsert still works around the damaged comments
	if err := Upsert(file, Entry{ID: "new", Kind: KindComm, At: t0, Text: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestMonthFile(t *testing.T) {
	if got := MonthFile("/d", t0); got != filepath.Join("/d", "2026-10.md") {
		t.Fatal(got)
	}
}

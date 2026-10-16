package journal

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const roadmapDoc = "# Roadmap\r\n\nintro | with pipe\n\n## Journal\n\n| 階段 | 說明 | 狀態 |\n|:--|---|--:|\n| J-0a  | team feed \\| done   |  完成  |\n|J-1a|package|未開始|\n| J-1b | no trailing pipe | 未開始\n\ntrailing prose\n\n## Name pool\n\n| 說明 | 階段 | 狀態 | extra |\n|---|---|---|---|\n| first col is desc | NP-1 | 進行中 | x |\n\n## Empty\n\nnothing here\n"

func writeRoadmap(t *testing.T, doc string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "roadmap.md")
	if err := os.WriteFile(f, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestParseRoadmap(t *testing.T) {
	got, err := ParseRoadmap(writeRoadmap(t, roadmapDoc))
	if err != nil {
		t.Fatal(err)
	}
	want := []Feature{
		{Name: "Journal", Stages: []Stage{{"J-0a", `team feed \| done`, "完成"}, {"J-1a", "package", "未開始"}, {"J-1b", "no trailing pipe", "未開始"}}},
		{Name: "Name pool", Stages: []Stage{{"NP-1", "first col is desc", "進行中"}}},
		{Name: "Empty"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestSetStatusChangesOnlyTheCell(t *testing.T) {
	f := writeRoadmap(t, roadmapDoc)
	cases := []struct{ feature, stage, old, status string }{
		{"Journal", "J-0a", "完成", "暫停"}, {"Journal", "J-1a", "未開始", "進行中"},
		{"Journal", "J-1b", "未開始", "審查中"}, {"Name pool", "NP-1", "進行中", "完成"},
	}
	for _, c := range cases {
		before, _ := os.ReadFile(f)
		ok, err := SetStatus(f, c.feature, c.stage, c.status)
		if err != nil || !ok {
			t.Fatalf("%v: %v %v", c, ok, err)
		}
		after, _ := os.ReadFile(f)
		a, b := strings.Split(string(before), "\n"), strings.Split(string(after), "\n")
		if len(a) != len(b) {
			t.Fatalf("line count changed")
		}
		diffs := 0
		for i := range a {
			if a[i] == b[i] {
				continue
			}
			diffs++
			// the old status text is replaced in place; padding and every other byte stay
			k := strings.LastIndex(a[i], c.old)
			if want := a[i][:k] + c.status + a[i][k+len(c.old):]; b[i] != want {
				t.Errorf("more than the cell changed:\n before %q\n after  %q\n want   %q", a[i], b[i], want)
			}
		}
		if diffs != 1 {
			t.Fatalf("%v changed %d lines", c, diffs)
		}
	}
	got, _ := os.ReadFile(f)
	for _, want := range []string{"|  暫停  |", "|J-1a|package|進行中|", "| 審查中\n"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestSetStatusRefusals(t *testing.T) {
	f := writeRoadmap(t, roadmapDoc)
	before, _ := os.ReadFile(f)
	if ok, err := SetStatus(f, "Journal", "J-1a", "亂寫"); ok || !errors.Is(err, ErrUnknownStatus) {
		t.Fatal(ok, err)
	}
	for _, c := range [][2]string{{"Nope", "J-1a"}, {"Journal", "J-9"}, {"Empty", "x"}} {
		if ok, err := SetStatus(f, c[0], c[1], "完成"); ok || err != nil {
			t.Fatalf("%v: %v %v", c, ok, err)
		}
	}
	if ok, err := SetStatus(f, "Journal", "J-0a", "完成"); ok || err != nil {
		t.Fatal("same status reported a change", ok, err)
	}
	after, _ := os.ReadFile(f)
	if string(before) != string(after) {
		t.Fatal("a refused change touched the file")
	}
}

func TestUnparsableTableLeavesFileUntouched(t *testing.T) {
	for name, doc := range map[string]string{
		"missing header column": "## F\n\n| 階段 | 狀態 |\n|---|---|\n| a | 未開始 |\n",
		"no separator":          "## F\n\n| 階段 | 說明 | 狀態 |\n| a | b | 未開始 |\n",
		"header at eof":         "## F\n\n| 階段 | 說明 | 狀態 |",
	} {
		f := writeRoadmap(t, doc)
		ok, err := SetStatus(f, "F", "a", "完成")
		if ok || !errors.Is(err, ErrUnparsable) {
			t.Errorf("%s: %v %v", name, ok, err)
		}
		if b, _ := os.ReadFile(f); string(b) != doc {
			t.Errorf("%s: file touched", name)
		}
		if _, err := ParseRoadmap(f); !errors.Is(err, ErrUnparsable) {
			t.Errorf("%s: parse err %v", name, err)
		}
	}
	if _, err := SetStatus(filepath.Join(t.TempDir(), "none.md"), "F", "a", "完成"); err == nil {
		t.Error("missing file accepted")
	}
}

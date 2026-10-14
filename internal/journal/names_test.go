package journal

import (
	"testing"
	"time"
)

func TestDisplayName(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"purdex-3f-adam", "Adam"},
		{"mlab/purdex-3f-adam", "Adam"},
		{"purdex-88-b8", "purdex-88-b8"},
		{"mlab/purdex-88-b8", "purdex-88-b8"},
		{"a/b/purdex-3f-adam", "Adam"},
		{"mlab/", ""},
		{"", ""},
		{"adam", "adam"}, // no dash: not a virtual name with a pool suffix
		{"mlab/_q34psn", "_q34psn"},
	} {
		if got := DisplayName(c.in); got != c.want {
			t.Errorf("DisplayName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRewritePeerPrefixes(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"[mlab/purdex-3f-adam → mlab/purdex-88-b8] 請查 mlab/purdex-3f-adam", "[Adam → purdex-88-b8] 請查 mlab/purdex-3f-adam"},
		{"[report merged mlab/purdex-3f-adam] 好了", "[report merged Adam] 好了"},
		{"[report merged T-3] ok", "[report merged T-3] ok"},
		{"[pdx team] member 接力完成：a → b", "[pdx team] member 接力完成：a → b"},
		{"intro [mlab/purdex-3f-adam → x] not a prefix", "intro [mlab/purdex-3f-adam → x] not a prefix"},
		{"[a b → c] x", "[a b → c] x"},
		{"[mlab/purdex-3f-adam → mlab/purdex-88-b8] one\n[mlab/purdex-88-b8 → mlab/purdex-3f-adam] two", "[Adam → purdex-88-b8] one\n[purdex-88-b8 → Adam] two"},
		{"[report merged feature/login] 完成", "[report merged feature/login] 完成"},
		{"[report https://host/path] x", "[report https://host/path] x"},
		{"[report merged a/b/purdex-3f-adam] x", "[report merged a/b/purdex-3f-adam] x"},
		{"[report merged mlab/purdex-88-b8] x", "[report merged mlab/purdex-88-b8] x"}, // not a pool name: kept as written
		{"[https://h/p → purdex-3f-adam] x", "[https://h/p → Adam] x"},
		{"[unclosed mlab/purdex-3f-adam", "[unclosed mlab/purdex-3f-adam"},
		{"", ""},
	} {
		if got := RewritePeerPrefixes(c.in); got != c.want {
			t.Errorf("RewritePeerPrefixes(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// A name ending in an ASCII letter or digit is spaced from 「請求」; a CJK name stays tight.
func TestEntryLabel_PersonNameSpacing(t *testing.T) {
	for name, want := range map[string]string{"Wake": "Wake 請求", "wake2": "wake2 請求", "王小明": "王小明請求", "小W": "小W 請求", "W小": "W小請求"} {
		if got := (Entry{Kind: KindRequest, By: ByUser, Person: name}).Label(); got != want {
			t.Errorf("%q = %q, want %q", name, got, want)
		}
	}
}

// The label of a request the person makes carries their name; the other labels and an unnamed person are unchanged, and
// the name survives a write and a parse.
func TestEntryLabel_PersonName(t *testing.T) {
	if got := (Entry{Kind: KindRequest, By: ByUser}).Label(); got != "使用者請求" {
		t.Errorf("unnamed = %q", got)
	}
	if got := (Entry{Kind: KindRequest, By: ByUser, Person: "Wake"}).Label(); got != "Wake 請求" {
		t.Errorf("named = %q", got)
	}
	if got := (Entry{Kind: KindRequest, By: ByLeader, Person: "Wake"}).Label(); got != "領導者請求" {
		t.Errorf("leader = %q", got)
	}
	file := t.TempDir() + "/2026-10.md"
	e := Entry{ID: "a", Kind: KindRequest, By: ByUser, Person: "Wake", At: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC), Text: "要求。"}
	if err := Upsert(file, e); err != nil {
		t.Fatal(err)
	}
	got, err := Parse(file)
	if err != nil || len(got) != 1 || got[0].Person != "Wake" {
		t.Fatalf("parse = %+v %v", got, err)
	}
}

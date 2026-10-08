package textwidth

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCellWidth(t *testing.T) {
	for _, c := range []struct {
		s    string
		want int
	}{{"abc", 3}, {"長", 2}, {"a長b", 4}, {"e\u0301", 1}, {"😀", 2}, {"🚀", 2}, {"👍🏽", 2}, {"", 0}} {
		if got := CellWidth(c.s); got != c.want {
			t.Errorf("CellWidth(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestCutWidth(t *testing.T) {
	if got := CutWidth("abcdef", 6); got != "abcdef" {
		t.Errorf("fits: %q", got)
	}
	if got := CutWidth("abcdefg", 6); got != "abcde…" {
		t.Errorf("ascii: %q", got)
	}
	got := CutWidth(strings.Repeat("長", 40), 10)
	if CellWidth(got) > 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("cjk: %q width %d", got, CellWidth(got))
	}
}

// Every row's later columns start at the same display column, whatever the
// widths of the cells before them.

// The shared fixture: the strings and the weights the daemon (this package)
// and the App (spa/src/lib/textwidth.ts) must both give. Two hand-written range
// tables cannot be proved the same, but they can be made to agree on these.
func TestWidthFixture(t *testing.T) {
	b, err := os.ReadFile("../../testdata/textwidth/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name  string `json:"name"`
		S     string `json:"s"`
		Width int    `json:"width"`
	}
	if err := json.Unmarshal(b, &cases); err != nil || len(cases) < 20 {
		t.Fatalf("fixture: %v (%d cases)", err, len(cases))
	}
	for _, c := range cases {
		if got := CellWidth(c.S); got != c.Width {
			t.Errorf("%s: CellWidth(%q) = %d, want %d", c.Name, c.S, got, c.Width)
		}
	}
}

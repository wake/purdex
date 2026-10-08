package main

import (
	"strings"
	"testing"
)

func TestCellWidth(t *testing.T) {
	for _, c := range []struct {
		s    string
		want int
	}{{"abc", 3}, {"長", 2}, {"a長b", 4}, {"e\u0301", 1}, {"😀", 2}, {"", 0}} {
		if got := cellWidth(c.s); got != c.want {
			t.Errorf("cellWidth(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestCutWidth(t *testing.T) {
	if got := cutWidth("abcdef", 6); got != "abcdef" {
		t.Errorf("fits: %q", got)
	}
	if got := cutWidth("abcdefg", 6); got != "abcde…" {
		t.Errorf("ascii: %q", got)
	}
	got := cutWidth(strings.Repeat("長", 40), 10)
	if cellWidth(got) > 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("cjk: %q width %d", got, cellWidth(got))
	}
}

// Every row's later columns start at the same display column, whatever the
// widths of the cells before them.
func TestAlignRows(t *testing.T) {
	var b strings.Builder
	alignRows(&b, [][]string{
		{"A", "TASK", "LAST"},
		{"x", strings.Repeat("長", 5), "1m"},
		{"y", "abc", "2m"},
	}, 2)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	col := func(line, last string) int { return cellWidth(line[:strings.Index(line, last)]) }
	h, r1, r2 := col(lines[0], "LAST"), col(lines[1], "1m"), col(lines[2], "2m")
	if h != r1 || h != r2 {
		t.Fatalf("last column starts at %d / %d / %d:\n%s", h, r1, r2, b.String())
	}
	if strings.HasSuffix(lines[2], " ") {
		t.Errorf("trailing padding on the last column: %q", lines[2])
	}
}

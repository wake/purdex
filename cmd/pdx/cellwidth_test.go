package main

import (
	"io"
	"strings"
	"testing"
)

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestAlignRowsReturnsWriteError(t *testing.T) {
	if err := alignRows(failWriter{}, [][]string{{"a", "b"}}, 2); err == nil {
		t.Fatal("write error swallowed")
	}
}

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

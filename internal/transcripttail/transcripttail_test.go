package transcripttail

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func rd(s string) *bytes.Reader { return bytes.NewReader([]byte(s)) }

func tail(t *testing.T, s string, n, max int) Result {
	t.Helper()
	r, err := Tail(rd(s), int64(len(s)), n, max)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	return r
}

func after(t *testing.T, s string, off int64, max int) Result {
	t.Helper()
	r, err := After(rd(s), int64(len(s)), off, max)
	if err != nil {
		t.Fatalf("After: %v", err)
	}
	return r
}

func eq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

func TestTailBasic(t *testing.T) {
	r := tail(t, "a\nbb\nccc\n", 2, 1<<20)
	eq(t, r.Lines, "bb", "ccc")
	if r.Start != 2 || r.End != 9 || r.More {
		t.Fatalf("%+v", r)
	}
	r = tail(t, "a\nbb\n", 10, 1<<20)
	eq(t, r.Lines, "a", "bb")
	if r.Start != 0 || r.End != 5 {
		t.Fatalf("%+v", r)
	}
}

func TestTailEmptyAndNoNewline(t *testing.T) {
	r := tail(t, "", 5, 1<<20)
	if len(r.Lines) != 0 || r.Start != 0 || r.End != 0 || r.More {
		t.Fatalf("%+v", r)
	}
	// only an unfinished line: nothing returned, end stays before it
	r = tail(t, "partial", 5, 1<<20)
	if len(r.Lines) != 0 || r.End != 0 {
		t.Fatalf("%+v", r)
	}
	// trailing unfinished segment is withheld
	r = tail(t, "a\nb\npart", 5, 1<<20)
	eq(t, r.Lines, "a", "b")
	if r.End != 4 {
		t.Fatalf("%+v", r)
	}
}

func TestTailMultibyteAcrossBlocks(t *testing.T) {
	// multibyte lines so that some straddle the 64 KB block boundary
	var sb strings.Builder
	var want []string
	for i := 0; sb.Len() < 3*blockSize; i++ {
		l := fmt.Sprintf("行%d-%s", i, strings.Repeat("中", 37))
		want = append(want, l)
		sb.WriteString(l + "\n")
	}
	s := sb.String()
	r := tail(t, s, len(want), 1<<30)
	eq(t, r.Lines, want...)
	r = tail(t, s, 5, 1<<30)
	eq(t, r.Lines, want[len(want)-5:]...)
	for _, l := range r.Lines {
		if !strings.HasPrefix(l, "行") {
			t.Fatalf("cut mid-rune: %q", l)
		}
	}
}

func TestTailByteCapKeepsNewest(t *testing.T) {
	s := "aaaa\nbbbb\ncccc\ndddd\n"
	r := tail(t, s, 100, 10) // room for two 5-byte lines
	eq(t, r.Lines, "cccc", "dddd")
	if r.Start != 10 || r.End != 20 || !r.More {
		t.Fatalf("%+v", r)
	}
}

func TestTailOversizeSingleLine(t *testing.T) {
	big := strings.Repeat("x", 100)
	s := "a\n" + big + "\n"
	r := tail(t, s, 5, 50)
	eq(t, r.Lines, big)
	if !r.More || r.Start != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestTailLineTooLarge(t *testing.T) {
	s := "a\n" + strings.Repeat("x", MaxLineBytes+10) + "\n"
	_, err := Tail(rd(s), int64(len(s)), 5, 2<<20)
	if !errors.Is(err, ErrLineTooLarge) {
		t.Fatalf("err = %v", err)
	}
	// an older oversized line does not fail when newer lines already answer
	s = strings.Repeat("x", MaxLineBytes+10) + "\nb\n"
	r, err := Tail(rd(s), int64(len(s)), 5, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, r.Lines, "b")
	if !r.More {
		t.Fatalf("%+v", r)
	}
}

func TestAfterBasic(t *testing.T) {
	s := "a\nbb\nccc\n"
	r := after(t, s, 2, 1<<20)
	eq(t, r.Lines, "bb", "ccc")
	if r.Start != 2 || r.End != 9 || r.More {
		t.Fatalf("%+v", r)
	}
	r = after(t, s, 9, 1<<20)
	if len(r.Lines) != 0 || r.Start != 9 || r.End != 9 {
		t.Fatalf("%+v", r)
	}
}

func TestAfterMidLineDropsHalfLine(t *testing.T) {
	s := "aaaa\nbb\ncc\n"
	r := after(t, s, 2, 1<<20)
	eq(t, r.Lines, "bb", "cc")
	if r.Start != 5 {
		t.Fatalf("start = %d", r.Start)
	}
}

func TestAfterWithholdsUnfinished(t *testing.T) {
	s := "a\nb\nunfin"
	r := after(t, s, 0, 1<<20)
	eq(t, r.Lines, "a", "b")
	if r.End != 4 {
		t.Fatalf("%+v", r)
	}
	// offset inside the unfinished line: nothing yet, end stays at line start
	r = after(t, s, 6, 1<<20)
	if len(r.Lines) != 0 || r.End != 4 || r.Start != 4 {
		t.Fatalf("%+v", r)
	}
}

func TestAfterByteCapMore(t *testing.T) {
	s := "aaaa\nbbbb\ncccc\n"
	r := after(t, s, 0, 10)
	eq(t, r.Lines, "aaaa", "bbbb")
	if !r.More || r.End != 10 {
		t.Fatalf("%+v", r)
	}
	r = after(t, s, r.End, 10)
	eq(t, r.Lines, "cccc")
	if r.More {
		t.Fatalf("%+v", r)
	}
}

func TestAfterOversizeLine(t *testing.T) {
	big := strings.Repeat("x", 100)
	s := big + "\nz\n"
	r := after(t, s, 0, 50)
	eq(t, r.Lines, big)
	if !r.More || r.End != 101 {
		t.Fatalf("%+v", r)
	}
	s = strings.Repeat("x", MaxLineBytes+10) + "\n"
	if _, err := After(rd(s), int64(len(s)), 0, 2<<20); !errors.Is(err, ErrLineTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

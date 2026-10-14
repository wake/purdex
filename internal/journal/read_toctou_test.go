package journal

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeInfo is a regular, small file as Stat sees it.
type fakeInfo struct{}

func (fakeInfo) Name() string       { return "2026-11.md" }
func (fakeInfo) Size() int64        { return 10 }
func (fakeInfo) Mode() os.FileMode  { return 0o600 }
func (fakeInfo) ModTime() time.Time { return time.Time{} }
func (fakeInfo) IsDir() bool        { return false }
func (fakeInfo) Sys() any           { return nil }

// endlessFile is what a month file can turn into between a check and the read: small by its Stat, endless by its Read.
type endlessFile struct{ read int64 }

func (e *endlessFile) Read(p []byte) (int, error) {
	if e.read > 10<<20 {
		return 0, errors.New("read past any cap")
	}
	e.read += int64(len(p))
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}
func (e *endlessFile) Close() error               { return nil }
func (e *endlessFile) Stat() (os.FileInfo, error) { return fakeInfo{}, nil }

// sizedFile is small by its Stat and n bytes of non-entry text by its Read.
type sizedFile struct{ n, done int }

func (s *sizedFile) Read(p []byte) (int, error) {
	if s.done >= s.n {
		return 0, io.EOF
	}
	k := min(len(p), s.n-s.done)
	for i := 0; i < k; i++ {
		p[i] = 'y'
	}
	s.done += k
	return k, nil
}
func (s *sizedFile) Close() error               { return nil }
func (s *sizedFile) Stat() (os.FileInfo, error) { return fakeInfo{}, nil }

func swapOpenMonth(t *testing.T, suffix string, replacement monthFile) {
	t.Helper()
	real := openMonth
	openMonth = func(path string) (monthFile, error) {
		if strings.HasSuffix(path, suffix) {
			return replacement, nil
		}
		return real(path)
	}
	t.Cleanup(func() { openMonth = real })
}

// The size limits are enforced on what is read, from the one descriptor that was checked: a month file that passes its check
// and then grows (or is swapped for something endless) is cut off at the cap, never read whole.
// Mutation gate: read the descriptor without a limit -> it is read past the cap (red).
func TestReadUnit_TheCapIsEnforcedOnWhatIsRead(t *testing.T) {
	withReadLimits(t, 1000, 1<<30)
	dir := unitDir(t,
		Entry{ID: "small", Kind: KindExec, At: at(time.October, 1, 1), Text: "t"},
		Entry{ID: "swapped", Kind: KindExec, At: at(time.November, 1, 1), Text: "t"},
	)
	endless := &endlessFile{}
	swapOpenMonth(t, "2026-11.md", endless)
	got, skipped, err := ReadUnit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "small" || len(skipped) != 1 || skipped[0].Name != "2026-11.md" {
		t.Fatalf("entries = %v, skipped = %+v", ids(got), skipped)
	}
	if endless.read > maxMonthBytes+1 {
		t.Fatalf("%d bytes were read from a month capped at %d", endless.read, maxMonthBytes)
	}
}

// The unit budget counts the bytes actually read, not the sizes Stat reported: a month that reads bigger than its Stat said
// takes the budget it really uses, and the older month that no longer fits is left out.
// Mutation gate: charge Stat's size to the budget -> the grown month costs 10 bytes and nothing is left out (red).
func TestReadUnit_TheUnitBudgetCountsBytesRead(t *testing.T) {
	dir := unitDir(t,
		Entry{ID: "old", Kind: KindExec, At: at(time.September, 1, 1), Text: "t"},
		Entry{ID: "grown", Kind: KindExec, At: at(time.November, 1, 1), Text: "t"},
	)
	st, err := os.Stat(MonthFile(dir, at(time.September, 1, 1)))
	if err != nil {
		t.Fatal(err)
	}
	// room for the 700 bytes the newest month really reads, and for the old month only if the 700 were counted as 10
	withReadLimits(t, 800, st.Size()+650)
	swapOpenMonth(t, "2026-11.md", &sizedFile{n: 700})
	got, skipped, err := ReadUnit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || skipped[0].Name != "2026-09.md" {
		t.Fatalf("entries = %v, skipped = %+v: the older month should have been left out of the budget", ids(got), skipped)
	}
}

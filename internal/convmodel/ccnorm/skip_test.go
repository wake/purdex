package ccnorm

import (
	"errors"
	"reflect"
	"testing"
)

func TestSkip_AdvancesPastALineTheCallerDidNotRead(t *testing.T) {
	n := New(Options{})
	feed(t, n, userRow("u1", 1, "hi"))
	next := n.Next()
	// a line of 123 bytes at next that was never read
	if err := n.Skip(next, 123); err != nil {
		t.Fatal(err)
	}
	if n.Next() != next+123+1 {
		t.Fatalf("Next = %d, want %d", n.Next(), next+124)
	}
	if got := n.Stats().Skipped["line:oversize"]; got != 1 {
		t.Errorf("Skipped[line:oversize] = %d, want 1", got)
	}
	if n.Stats().Lines != 2 {
		t.Errorf("Lines = %d, want 2", n.Stats().Lines)
	}
	// the row after it is accepted at the new offset
	if _, err := n.Feed(n.Next(), assistantText("a1", 2, "yo")); err != nil {
		t.Fatal(err)
	}
}

func TestSkip_ContiguityLikeFeed(t *testing.T) {
	n := New(Options{})
	feed(t, n, userRow("u1", 1, "hi"))
	next := n.Next()
	if err := n.Skip(next+5, 10); !errors.Is(err, ErrGap) {
		t.Fatalf("a gap: err = %v, want ErrGap", err)
	}
	if n.Next() != next || n.Stats().Skipped["line:oversize"] != 0 {
		t.Fatalf("a refused Skip changed state: next %d, stats %+v", n.Next(), n.Stats())
	}
	if err := n.Skip(0, 10); err != nil { // a replay is ignored, like Feed's
		t.Fatalf("replay: %v", err)
	}
	if n.Next() != next || n.Stats().Replayed != 1 {
		t.Fatalf("a replayed Skip moved Next (%d) or missed Replayed (%d)", n.Next(), n.Stats().Replayed)
	}
}

func TestHeader_TitleAndUsage(t *testing.T) {
	n := New(Options{})
	if title, usage := n.Header(); title != "" || usage != nil {
		t.Fatalf("empty normalizer header = %q %+v", title, usage)
	}
	feed(t, n, userRow("u1", 1, "hi"), assistantText("a1", 2, "yo"))
	title, usage := n.Header()
	c := n.Conversation()
	if title != c.Title || (usage == nil) != (c.Usage == nil) || (usage != nil && !reflect.DeepEqual(usage, c.Usage)) {
		t.Fatalf("Header %q %+v differs from Conversation %q %+v", title, usage, c.Title, c.Usage)
	}
}

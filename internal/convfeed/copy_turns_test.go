package convfeed

import "testing"

// The copy shares nothing with the entry: a caller may change it freely.
// Mutation gate: return the entry's own slice → red.
func TestCopyLastTurns_SharesNothingWithTheEntry(t *testing.T) {
	e := filled(t, 4)
	got := e.CopyLastTurns(2)
	if len(got) != 2 || got[0].Index != 2 || got[1].Index != 3 {
		t.Fatalf("turns = %+v", got)
	}
	if len(got[1].Items) == 0 {
		t.Fatal("no items copied")
	}
	want := e.CopyLastTurns(2)
	// scribble over everything the copy holds
	got[1].ID = "changed"
	got[1].Items[0].User.Text = "changed"
	if got[1].Items[1].AgentText != nil {
		got[1].Items[1].AgentText.Markdown = "changed"
	}
	got[1].Items = got[1].Items[:0]
	again := e.CopyLastTurns(2)
	for i := range want {
		if again[i].ID != want[i].ID || len(again[i].Items) != len(want[i].Items) {
			t.Fatalf("the entry moved: %+v vs %+v", again[i], want[i])
		}
		if again[i].Items[0].User == nil || again[i].Items[0].User.Text != want[i].Items[0].User.Text {
			t.Fatalf("an item's text moved: %+v", again[i].Items[0].User)
		}
	}
}

func TestCopyLastTurns_FewerThanAskedAndNone(t *testing.T) {
	if got := filled(t, 2).CopyLastTurns(5); len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got := NewEntry(sidA).CopyLastTurns(3); len(got) != 0 {
		t.Fatalf("empty entry gave %d turns", len(got))
	}
	if got := filled(t, 2).CopyLastTurns(0); len(got) != 0 {
		t.Fatalf("n=0 gave %d turns", len(got))
	}
}

func TestCopyTurnsFrom(t *testing.T) {
	e := filled(t, 4)
	all := e.CopyLastTurns(10)
	cases := []struct {
		name      string
		from      string
		wantFirst int
		wantLen   int
		found     bool
	}{
		{"middle", all[1].ID, 1, 3, true},
		{"last", all[3].ID, 3, 1, true},
		{"first", all[0].ID, 0, 4, true},
		{"unknown", "nope", 0, 4, false},
		{"empty", "", 0, 4, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, found := e.CopyTurnsFrom(c.from)
			if found != c.found || len(got) != c.wantLen || got[0].Index != c.wantFirst {
				t.Fatalf("found=%v len=%d first=%d, want %v/%d/%d", found, len(got), got[0].Index, c.found, c.wantLen, c.wantFirst)
			}
		})
	}
}

func TestCopyTurnsFrom_SharesNothingWithTheEntry(t *testing.T) {
	e := filled(t, 3)
	id := e.CopyLastTurns(3)[1].ID
	got, _ := e.CopyTurnsFrom(id)
	got[0].ID = "changed"
	got[0].Items[0].User.Text = "changed"
	got[0].Items = got[0].Items[:0]
	again, found := e.CopyTurnsFrom(id)
	if !found || again[0].ID != id || len(again[0].Items) == 0 || again[0].Items[0].User.Text == "changed" {
		t.Fatalf("the entry moved: %+v", again[0])
	}
}

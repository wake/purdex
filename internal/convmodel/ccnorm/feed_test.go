package ccnorm

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

func jsonOf(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFeed_FirstLineMustBeOffsetZero(t *testing.T) {
	n := New(Options{})
	ch, err := n.Feed(7, userRow("u1", 1, "hi"))
	if !errors.Is(err, ErrGap) {
		t.Fatalf("err = %v, want ErrGap", err)
	}
	if len(ch) != 0 || n.Next() != 0 || len(n.Conversation().Turns) != 0 || n.Stats().Lines != 0 {
		t.Fatalf("a refused first line changed state: changes=%v next=%d", ch, n.Next())
	}
	if _, err := n.Feed(0, userRow("u1", 1, "hi")); err != nil {
		t.Fatal(err)
	}
}

func TestFeed_GapRefusedStateUnchanged(t *testing.T) {
	n := norm(t, userRow("u1", 1, "hi"), assistantText("a1", 2, "yo"))
	before := jsonOf(t, n.Conversation())
	next, st := n.Next(), n.Stats()

	ch, err := n.Feed(next+5, assistantText("a2", 3, "skipped"))
	if !errors.Is(err, ErrGap) {
		t.Fatalf("err = %v, want ErrGap", err)
	}
	if len(ch) != 0 {
		t.Errorf("a refused line reported changes %v", ch)
	}
	if got := jsonOf(t, n.Conversation()); got != before {
		t.Errorf("a refused line changed the model:\n%s\n%s", before, got)
	}
	if n.Next() != next || !reflect.DeepEqual(n.Stats(), st) {
		t.Errorf("a refused line moved Next (%d→%d) or Stats", next, n.Next())
	}
	// the right offset still works
	feed(t, n, assistantText("a2", 3, "ok"))
	if got := len(itemsOf(t, n.Conversation(), 0)); got != 3 {
		t.Errorf("items = %d, want 3", got)
	}
}

func TestFeed_NextIsOffsetPlusLengthPlusOne(t *testing.T) {
	n := New(Options{})
	l := userRow("u1", 1, "hi")
	if _, err := n.Feed(0, l); err != nil {
		t.Fatal(err)
	}
	if want := int64(len(l)) + 1; n.Next() != want {
		t.Errorf("Next = %d, want %d", n.Next(), want)
	}
}

func TestFeed_ReplayedLineIgnored(t *testing.T) {
	lines := [][]byte{
		userRow("u1", 1, "hi"), assistantText("a1", 2, "one"), assistantText("a2", 3, "two"),
		assistantText("a3", 4, "three"), turnDuration("d1", 5, 4000),
	}
	n := New(Options{})
	offs := make([]int64, len(lines))
	for i, l := range lines {
		offs[i] = n.Next()
		if _, err := n.Feed(offs[i], l); err != nil {
			t.Fatal(err)
		}
	}
	before := jsonOf(t, n.Conversation())
	next := n.Next()

	// the same last line twice
	for i := 0; i < 2; i++ {
		ch, err := n.Feed(offs[4], lines[4])
		if err != nil || len(ch) != 0 {
			t.Fatalf("replay: changes=%v err=%v", ch, err)
		}
	}
	// an overlapping re-read of the last 3 lines (what the transcript API's
	// overlapping `after` reads give)
	for i := 2; i < 5; i++ {
		if ch, err := n.Feed(offs[i], lines[i]); err != nil || len(ch) != 0 {
			t.Fatalf("overlap replay %d: changes=%v err=%v", i, ch, err)
		}
	}
	if got := jsonOf(t, n.Conversation()); got != before {
		t.Errorf("replay changed the model")
	}
	if n.Next() != next {
		t.Errorf("Next moved on replay: %d → %d", next, n.Next())
	}
	if st := n.Stats(); st.Replayed != 5 || st.Lines != 5 {
		t.Errorf("Stats = %+v, want Replayed 5 and Lines 5 (replays are not counted as lines)", st)
	}
	// and new lines after a replay are taken
	feed(t, n, assistantText("a4", 6, "four"))
	if got := len(itemsOf(t, n.Conversation(), 0)); got != 5 {
		t.Errorf("items = %d, want 5", got)
	}
}

func TestFeed_ChangesReportNewAndChangedWithOffsets(t *testing.T) {
	n := New(Options{})
	off := func() int64 { return n.Next() }
	feedOne := func(l []byte) (int64, []Change) {
		o := off()
		ch, err := n.Feed(o, l)
		if err != nil {
			t.Fatal(err)
		}
		return o, ch
	}

	o1, ch := feedOne(userRow("u1", 1, "hi"))
	want := []Change{{"u1", "", o1}, {"u1", "u1", o1}}
	if !reflect.DeepEqual(ch, want) {
		t.Errorf("prompt row: %v, want %v", ch, want)
	}
	o2, ch := feedOne(assistantText("a1", 2, "yo"))
	if want := []Change{{"u1", "a1", o2}}; !reflect.DeepEqual(ch, want) {
		t.Errorf("assistant row: %v, want %v", ch, want)
	}
	// a row that is skipped changes nothing
	if _, ch = feedOne(line(obj{"type": "last-prompt", "lastPrompt": "x", "sessionId": sidA})); len(ch) != 0 {
		t.Errorf("metadata row reported %v", ch)
	}
	// turn_duration changes the turn (running → done, ended_at set)
	o4, ch := feedOne(turnDuration("d1", 3, 2000))
	if want := []Change{{"u1", "", o4}}; !reflect.DeepEqual(ch, want) {
		t.Errorf("turn_duration: %v, want %v", ch, want)
	}
	// the next prompt row opens a turn; the previous turn is already done, so
	// it is not reported again
	o5, ch := feedOne(userRow("u2", 4, "again"))
	if want := []Change{{"u2", "", o5}, {"u2", "u2", o5}}; !reflect.DeepEqual(ch, want) {
		t.Errorf("second prompt: %v, want %v", ch, want)
	}
}

func TestFeed_OpeningATurnClosesThePreviousOneAndReportsIt(t *testing.T) {
	n := New(Options{})
	feed(t, n, userRow("u1", 1, "hi"), assistantText("a1", 2, "yo")) // no turn_duration: still running
	if got := n.Conversation().Turns[0].Outcome; got != convmodel.OutcomeRunning {
		t.Fatalf("outcome = %q, want running", got)
	}
	o := n.Next()
	ch, err := n.Feed(o, userRow("u2", 3, "next"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{"u1", "", o}, {"u2", "", o}, {"u2", "u2", o}}
	if !reflect.DeepEqual(ch, want) {
		t.Errorf("changes = %v, want %v", ch, want)
	}
}

func TestRows_BadJSONCounted(t *testing.T) {
	bad := [][]byte{[]byte("not json"), []byte("[1]"), []byte(""), []byte("42"), []byte(`{"type":"user"`), []byte("null")}
	n := New(Options{})
	feed(t, n, bad...)
	feed(t, n, userRow("u1", 1, "hi"))
	st := n.Stats()
	if st.BadJSON != len(bad) {
		t.Errorf("BadJSON = %d, want %d", st.BadJSON, len(bad))
	}
	if st.Lines != len(bad)+1 {
		t.Errorf("Lines = %d, want %d", st.Lines, len(bad)+1)
	}
	if got := len(n.Conversation().Turns); got != 1 {
		t.Errorf("turns = %d, want 1 (a bad line never blocks the next)", got)
	}
}

func TestRows_NoUUIDSkipped(t *testing.T) {
	n := norm(t, userRow("u1", 1, "hi", without("uuid")))
	if len(n.Conversation().Turns) != 0 || n.Stats().Skipped["no_uuid"] != 1 {
		t.Errorf("a prompt row without uuid: turns=%d skipped=%v", len(n.Conversation().Turns), n.Stats().Skipped)
	}
}

func TestRows_MetadataTypesCountedNotApplied(t *testing.T) {
	n := norm(t,
		line(obj{"type": "last-prompt", "lastPrompt": "x"}),
		line(obj{"type": "mode", "mode": "normal"}),
		line(obj{"type": "brand-new-type", "uuid": "z"}),
		queueOp(1, "enqueue"),
	)
	sk := n.Stats().Skipped
	if sk["type:last-prompt"] != 1 || sk["type:mode"] != 1 || sk["type:brand-new-type"] != 1 || sk["type:queue-operation"] != 1 {
		t.Errorf("Skipped = %v", sk)
	}
	if len(n.Conversation().Turns) != 0 {
		t.Error("metadata rows opened a turn")
	}
}

func TestStats_ReturnsACopy(t *testing.T) {
	n := norm(t, line(obj{"type": "mode"}))
	n.Stats().Skipped["type:mode"] = 99
	if got := n.Stats().Skipped["type:mode"]; got != 1 {
		t.Errorf("mutating the returned Stats changed the counters (%d)", got)
	}
}

func TestStore_UpsertSameIDUpdatesInPlace(t *testing.T) {
	n := norm(t, userRow("u1", 1, "hi"), assistantText("a1", 2, "first"))
	n.flush()
	off := n.Next() + 100
	n.upsert("u1", convmodel.Item{
		Type:      convmodel.ItemAgentText,
		AgentText: &convmodel.AgentText{ID: "a1", At: ms(2), Markdown: "second"},
	}, off)
	items := itemsOf(t, n.Conversation(), 0)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (an id seen again is updated, not appended)", len(items))
	}
	if got := items[1].AgentText.Markdown; got != "second" {
		t.Errorf("markdown = %q, want the update", got)
	}
	if want := []Change{{"u1", "a1", off}}; !reflect.DeepEqual(n.flush(), want) {
		t.Errorf("changes after update did not match")
	}
	// unknown turn: nothing happens
	if n.upsert("nope", convmodel.Item{Type: convmodel.ItemAgentText, AgentText: &convmodel.AgentText{ID: "z"}}, off) {
		t.Error("upsert into an unknown turn reported success")
	}
}

func TestPosition_LateResultMovesUpdatedNotCreated(t *testing.T) {
	n := New(Options{})
	feed(t, n, userRow("u1", 1, "hi"))
	aOff := n.Next()
	feed(t, n, assistantText("a1", 2, "first"))
	uPos, _ := n.Position("u1", "")

	late := aOff + 5000
	n.upsert("u1", convmodel.Item{
		Type:      convmodel.ItemAgentText,
		AgentText: &convmodel.AgentText{ID: "a1", At: ms(2), Markdown: "later"},
	}, late)

	p, ok := n.Position("u1", "a1")
	if !ok || p.Created != aOff || p.Updated != late {
		t.Errorf("item position = %+v ok=%v, want Created %d Updated %d", p, ok, aOff, late)
	}
	tp, ok := n.Position("u1", "")
	if !ok || tp.Created != uPos.Created || tp.Updated != late {
		t.Errorf("turn position = %+v, want Created %d (unchanged) Updated %d", tp, uPos.Created, late)
	}
	if up, _ := n.Position("u1", "u1"); up.Updated != 0 || up.Created != 0 {
		t.Errorf("an untouched item moved: %+v", up)
	}
	if _, ok := n.Position("u1", "nope"); ok {
		t.Error("position of an unknown item")
	}
	if _, ok := n.Position("nope", ""); ok {
		t.Error("position of an unknown turn")
	}
}

func TestConversation_IsADeepCopy(t *testing.T) {
	n := norm(t, userRow("u1", 1, "hi"), assistantText("a1", 2, "yo"))
	c := n.Conversation()
	c.Turns[0].Items[1].AgentText.Markdown = "mutated"
	c.Turns[0].Items = nil
	c.Title = "mutated"
	again := n.Conversation()
	if len(again.Turns[0].Items) != 2 || again.Turns[0].Items[1].AgentText.Markdown != "yo" {
		t.Error("mutating a returned Conversation changed the normalizer")
	}
}

func TestConversation_KeyAndCapabilities(t *testing.T) {
	c := New(Options{SessionID: sidA}).Conversation()
	if c.Key.SessionID != sidA || c.Key.Provider != "claude" || c.Provider != "claude" || c.Key.HostID != "" {
		t.Errorf("key = %+v provider = %q", c.Key, c.Provider)
	}
	if c.Capabilities == nil || c.Capabilities.Source != "transcript" {
		t.Errorf("capabilities = %+v", c.Capabilities)
	}
	if c.Turns == nil {
		t.Error("an empty conversation must have turns [] not null")
	}
	if got := jsonOf(t, c); !strings.Contains(got, `"turns":[]`) {
		t.Errorf("json = %s", got)
	}
}

// A row whose uuid was already seen, arriving at a new offset (not a replay),
// updates its item in place: no duplicate, Created stays, Updated moves.
func TestFeed_SameUUIDAtNewOffsetUpdatesInPlace(t *testing.T) {
	n := New(Options{})
	feed(t, n, userRow("u1", 1, "hi"))
	aOff := n.Next()
	feed(t, n, assistantText("a1", 2, "draft"))
	bOff := n.Next()
	feed(t, n, assistantText("a1", 2, "final"))

	items := itemsOf(t, n.Conversation(), 0)
	if len(items) != 2 || items[1].AgentText.Markdown != "final" {
		t.Fatalf("items = %v, want the user item and one updated agent_text", sigs(items))
	}
	if p, _ := n.Position("u1", "a1"); p.Created != aOff || p.Updated != bOff {
		t.Errorf("position = %+v, want Created %d Updated %d", p, aOff, bOff)
	}
}

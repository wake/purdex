package convfeed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convmodel/ccnorm"
)

const fixtureRoot = "../../testdata/conversation/v1/cc-transcript"

func TestEntry_FixturesFromZeroMatchExpected(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join(fixtureRoot, "*", "input.jsonl"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no fixtures under %s (%v)", fixtureRoot, err)
	}
	for _, in := range dirs {
		name := filepath.Base(filepath.Dir(in))
		t.Run(name, func(t *testing.T) {
			input, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(filepath.Dir(in), "expected.json"))
			if err != nil {
				t.Fatal(err)
			}
			var want struct {
				Conversation struct {
					Turns []any `json:"turns"`
				} `json:"conversation"`
				Live bool `json:"live"`
			}
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			m := &memFile{}
			m.AppendRaw(input)
			e := NewEntry(sidA)
			refresh(t, e, src(m, "f1", want.Live))
			w := e.Window(100000, -1, everything)
			b, err := json.Marshal(w.Turns)
			if err != nil {
				t.Fatal(err)
			}
			var got []any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			if got == nil {
				got = []any{}
			}
			if !reflect.DeepEqual(got, want.Conversation.Turns) {
				t.Fatalf("turns differ from expected.json (%d turns, want %d)", len(got), len(want.Conversation.Turns))
			}
		})
	}
}

func TestEntry_AppendOneRow_OnlyItsChanges(t *testing.T) {
	m := newMem(idle(3)...)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))
	rev := e.Revision()
	m.Append(userRow("u3", 100, "one more"))
	r := refresh(t, e, src(m, "f1", false))
	if !r.Changed || r.Reset {
		t.Fatalf("refresh = %+v, want Changed, no Reset", r)
	}
	if e.Revision() != rev+1 {
		t.Fatalf("revision %d, want %d", e.Revision(), rev+1)
	}
	ch := e.ChangesSince(rev)
	// the new turn with its one item; the turn before it only has its header settled (its outcome), no item changed
	if len(ch) != 2 || len(ch[0].Items) != 0 || ch[0].Turn.Index != 2 || !ch[0].HeaderChanged ||
		len(ch[1].Items) != 1 || ch[1].Turn.Index != 3 || ch[1].Items[0].User == nil || ch[1].Items[0].User.ID != "u3" || !ch[1].HeaderChanged {
		t.Fatalf("changes = %+v, want turn 2's header and the new turn 3 with its one item", ch)
	}
	if got := e.ChangesSince(e.Revision()); len(got) != 0 {
		t.Errorf("nothing changed since the current revision, got %+v", got)
	}
	if r := refresh(t, e, src(m, "f1", false)); r.Changed || e.Revision() != rev+1 {
		t.Errorf("an unchanged file moved the revision: %+v rev %d", r, e.Revision())
	}
}

func TestEntry_LateToolResult_OlderTurnInChanges(t *testing.T) {
	m := newMem(
		userRow("u0", 0, "run it"),
		toolUseRow("a0", 1, "toolu_1", "sleep 5"),
		userRow("u1", 10, "next question"),
		assistantText("a1", 11, "answer"),
	)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))
	rev := e.Revision()
	m.Append(toolResultRow("r0", 12, "toolu_1", "done"))
	refresh(t, e, src(m, "f1", false))
	ch := e.ChangesSince(rev)
	if len(ch) == 0 || ch[0].Turn.Index != 0 {
		t.Fatalf("changes = %+v, want the older turn 0 (its step got a result)", ch)
	}
	var step bool
	for _, c := range ch {
		for _, it := range c.Items {
			if it.Step != nil && it.Step.ID == "toolu_1" {
				step = true
			}
		}
	}
	if !step {
		t.Fatalf("the step toolu_1 is not in the changes: %+v", ch)
	}
}

func TestEntry_SetLiveFalse_UnfinishedStep_InChangesWithoutGrowth(t *testing.T) {
	m := newMem(userRow("u0", 0, "run it"), toolUseRow("a0", 1, "toolu_1", "sleep 99"))
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", true))
	rev := e.Revision()
	size, _ := m.Size()
	r := refresh(t, e, src(m, "f1", false)) // the pane went away: no file growth
	if now, _ := m.Size(); now != size {
		t.Fatal("the test file grew")
	}
	if !r.Changed {
		t.Fatalf("closing a live turn is a change: %+v", r)
	}
	ch := e.ChangesSince(rev)
	if len(ch) != 1 || !ch[0].HeaderChanged || len(ch[0].Items) != 1 || ch[0].Items[0].Step == nil {
		t.Fatalf("changes = %+v, want the turn header and the unfinished step", ch)
	}
	if h := e.Header(); h.Live {
		t.Errorf("header.Live = true after SetLive(false)")
	}
}

func TestEntry_TitleRow_HeaderChangeBumpsRevision(t *testing.T) {
	m := newMem(idle(1)...)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))
	rev := e.Revision()
	if e.HeaderChangedSince(0) == false {
		// the model carried a usage (model / effort) from the first assistant row
		t.Log("header usage present from the first refresh")
	}
	m.Append(customTitle("My title"))
	refresh(t, e, src(m, "f1", false))
	if e.Revision() != rev+1 || !e.HeaderChangedSince(rev) || e.Header().Title != "My title" {
		t.Fatalf("rev %d (want %d), headerChanged %v, title %q", e.Revision(), rev+1, e.HeaderChangedSince(rev), e.Header().Title)
	}
	if ch := e.ChangesSince(rev); len(ch) != 0 {
		t.Errorf("a title row changes no turn, got %+v", ch)
	}
}

func TestEntry_OversizeLineSkippedWithoutBufferingIt(t *testing.T) {
	big := make([]byte, 9<<20) // a 9 MiB line
	for i := range big {
		big[i] = 'A'
	}
	prefix := []byte(`{"type":"user","blob":"`)
	m := &memFile{}
	m.Append(userRow("u0", 0, "before"))
	m.AppendRaw(append(prefix, big...))
	m.AppendRaw([]byte("\"}\n"))
	m.Append(userRow("u1", 5, "after"), assistantText("a1", 6, "ok"))

	e := NewEntry(sidA)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	refresh(t, e, src(m, "f1", false))
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 7<<20 {
		t.Fatalf("refresh allocated %d MiB for a 9 MiB line: it was buffered", got>>20)
	}
	w := e.Window(10, -1, everything)
	if len(w.Turns) != 2 || w.Turns[1].Items[0].User == nil || w.Turns[1].Items[0].User.ID != "u1" {
		t.Fatalf("turns after the oversize line = %+v, want both rows around it fed", w.Turns)
	}
	if got := e.norm.Stats().Skipped["line:oversize"]; got != 1 {
		t.Errorf("Skipped[line:oversize] = %d, want 1", got)
	}
}

func TestEntry_UnterminatedTailWaits(t *testing.T) {
	m := newMem(userRow("u0", 0, "hi"))
	tail := assistantText("a0", 1, "partial answer")
	m.AppendRaw(tail[:len(tail)/2])
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))
	if n := e.Window(10, -1, everything); len(n.Turns) != 1 || len(n.Turns[0].Items) != 1 {
		t.Fatalf("the half row was fed: %+v", n.Turns)
	}
	m.AppendRaw(tail[len(tail)/2:])
	m.AppendRaw([]byte("\n"))
	refresh(t, e, src(m, "f1", false))
	if n := e.Window(10, -1, everything); len(n.Turns[0].Items) != 2 {
		t.Fatalf("the completed row was not fed: %+v", n.Turns)
	}
}

func TestEntry_ShrinkStartsANewEpoch(t *testing.T) {
	m := newMem(idle(3)...)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))
	old := e.Epoch()
	m.Set(joinLines(idle(1)))
	r := refresh(t, e, src(m, "f1", false))
	if !r.Reset || e.Epoch() == old || turnsOf(e) != 1 {
		t.Fatalf("reset %v, epoch same %v, turns %d: want a new epoch re-read from zero", r.Reset, e.Epoch() == old, turnsOf(e))
	}
}

func TestEntry_ReplacedFileStartsANewEpoch(t *testing.T) {
	m := newMem(idle(2)...)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "inode-1", false))
	old := e.Epoch()
	m2 := newMem(idle(5)...)
	r := refresh(t, e, src(m2, "inode-2", false))
	if !r.Reset || e.Epoch() == old || turnsOf(e) != 5 {
		t.Fatalf("reset %v, turns %d: want a new epoch for the other file", r.Reset, turnsOf(e))
	}
}

func TestEntry_SameInodeRewriteToALargerSizeStartsANewEpoch(t *testing.T) {
	m := newMem(idle(2)...)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))
	old := e.Epoch()
	// the same inode rewritten with different earlier bytes and a larger size: neither shrink nor identity sees it
	other := idle(6)
	for i := range other[:2] {
		other[i] = append([]byte(nil), other[i]...)
		other[i][len(other[i])-2] = 'Z'
	}
	m.Set(joinLines(append([][]byte{userRow("uX", 0, "totally different start")}, other...)))
	r := refresh(t, e, src(m, "f1", false))
	if !r.Reset || e.Epoch() == old {
		t.Fatalf("a same-inode rewrite was not noticed: %+v", r)
	}
	w := e.Window(100, -1, everything)
	if len(w.Turns) < 6 || w.Turns[0].Items[0].User == nil || w.Turns[0].Items[0].User.ID != "uX" {
		t.Fatalf("the rewrite was not re-read from zero: %d turns, first %+v", len(w.Turns), w.Turns[0].Items[0])
	}
}

func TestEntry_FeedGapStartsANewEpochAndReReads(t *testing.T) {
	m := newMem(idle(2)...)
	e := NewEntry(sidA)
	gaps := 0
	e.feedHook = func(off int64, line []byte) ([]ccnorm.Change, error) {
		if gaps == 0 {
			gaps++
			return nil, ccnorm.ErrGap
		}
		return e.norm.Feed(off, line)
	}
	old := e.Epoch()
	r := refresh(t, e, src(m, "f1", false))
	if !r.Reset || e.Epoch() == old || turnsOf(e) != 2 {
		t.Fatalf("reset %v turns %d epoch same %v: a gap must restart and re-read", r.Reset, turnsOf(e), e.Epoch() == old)
	}
	// a gap that keeps happening is an error, not a loop
	e.feedHook = func(int64, []byte) ([]ccnorm.Change, error) { return nil, ccnorm.ErrGap }
	if _, err := e.Refresh(context.Background(), src(newMem(idle(1)...), "f2", false)); !errors.Is(err, ccnorm.ErrGap) {
		t.Fatalf("a repeating gap: err = %v, want ErrGap", err)
	}
}

func TestEntry_ContextCancelledBetweenChunks(t *testing.T) {
	var lines [][]byte
	pad := strings.Repeat("x", 100_000)
	for i := 0; i < 70; i++ { // ~7 MB: several 2 MiB chunks
		lines = append(lines, userRow("u"+string(rune('a'+i%26))+string(rune('a'+i/26)), float64(i), pad))
	}
	m := newMem(lines...)
	ctx, cancel := context.WithCancel(context.Background())
	m.onRead = func(n int) {
		if n == 2 {
			cancel()
		}
	}
	e := NewEntry(sidA)
	if _, err := e.Refresh(ctx, src(m, "f1", false)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	m.onRead = nil
	r := refresh(t, e, src(m, "f1", false)) // resumes where it stopped
	if r.Reset || turnsOf(e) != 70 {
		t.Fatalf("after the cancelled refresh: reset %v turns %d, want 70 without a reset", r.Reset, turnsOf(e))
	}
}

// A refresh cut short (cancelled, or a read error) after complete lines were fed must still leave a fingerprint of the
// offset it reached: a same-inode rewrite before the next refresh is a new epoch, not a model mixing two file versions.
func TestEntry_RewriteAfterAnInterruptedRefreshStartsANewEpoch(t *testing.T) {
	pad := strings.Repeat("x", 100_000)
	var lines [][]byte
	for i := 0; i < 70; i++ {
		lines = append(lines, userRow("u"+string(rune('a'+i%26))+string(rune('a'+i/26)), float64(i), pad))
	}
	m := newMem(lines...)
	ctx, cancel := context.WithCancel(context.Background())
	m.onRead = func(n int) {
		if n == 2 {
			cancel()
		}
	}
	e := NewEntry(sidA)
	if _, err := e.Refresh(ctx, src(m, "f1", false)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if e.norm.Next() == 0 {
		t.Fatal("setup: nothing was fed before the cancel")
	}
	old := e.Epoch()
	// the same inode rewritten, as large as before, with other bytes where the entry had read
	rewritten := make([][]byte, len(lines))
	for i := range lines {
		rewritten[i] = userRow("w"+string(rune('a'+i%26))+string(rune('a'+i/26)), float64(i), pad)
	}
	m.onRead = nil
	m.Set(joinLines(rewritten))
	r := refresh(t, e, src(m, "f1", false))
	if !r.Reset || e.Epoch() == old {
		t.Fatalf("a rewrite after an interrupted refresh was not noticed: %+v", r)
	}
	w := e.Window(1000, -1, everything)
	if len(w.Turns) != 70 || w.Turns[0].Items[0].User == nil || w.Turns[0].Items[0].User.ID != "waa" {
		t.Fatalf("the rewrite was not re-read from zero: %d turns", len(w.Turns))
	}
}

func TestParseCursor_StaleEpoch(t *testing.T) {
	e := NewEntry(sidA)
	epoch, rev, err := ParseCursor(e.Cursor())
	if err != nil || epoch != e.Epoch() || rev != 0 {
		t.Fatalf("ParseCursor(%q) = %q %d %v", e.Cursor(), epoch, rev, err)
	}
	e2 := NewEntry(sidA)
	if foreign, _, err := ParseCursor(e2.Cursor()); err != nil || foreign == e.Epoch() {
		t.Fatalf("two entries share an epoch: %q %v", foreign, err)
	}
	for _, bad := range []string{"", "abc", ":5", "abc:", "abc:-1", "ab c:1", "ABC:1", "abc:x"} {
		if _, _, err := ParseCursor(bad); !errors.Is(err, ErrBadCursor) {
			t.Errorf("ParseCursor(%q) err = %v, want ErrBadCursor", bad, err)
		}
	}
}

func TestEntry_ModelStaysValid(t *testing.T) {
	m := newMem(idle(4)...)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))
	c := convmodel.Conversation{Key: convmodel.Key{Provider: "claude", SessionID: sidA}, Provider: "claude", Turns: e.Window(100, -1, everything).Turns}
	if err := c.Validate(); err != nil {
		t.Fatalf("the windowed conversation does not validate: %v", err)
	}
}

// The resolver's status and backend are part of the header: a change bumps the revision (an increment or a WebSocket
// frame reports it) and two reads of one entry never mix two requests' statuses.
// A reading taken before the entry's current one never replaces it: a follower that read the light, then waited for
// the gate while another follower refreshed a newer reading, must not move the status backwards.
func TestEntry_AnOlderStatusReadingDoesNotReplaceANewerOne(t *testing.T) {
	m := newMem(idle(1)...)
	e := NewEntry(sidA)
	t0 := time.Unix(1000, 0)
	s := src(m, "f1", true)
	s.Status, s.Backend, s.StatusAt = "running", "terminal", t0.Add(2*time.Second)
	refresh(t, e, s)
	rev := e.Revision()

	s.Status, s.StatusAt = "idle", t0.Add(time.Second) // read earlier, applied later
	refresh(t, e, s)
	if e.Header().Status != "running" || e.Revision() != rev {
		t.Fatalf("an older reading moved the entry: %+v rev %d (was %d)", e.Header(), e.Revision(), rev)
	}

	s.Status, s.StatusAt = "waiting", t0.Add(3*time.Second)
	refresh(t, e, s)
	if e.Header().Status != "waiting" {
		t.Fatalf("a newer reading was refused: %+v", e.Header())
	}

	s.Status, s.StatusAt = "idle", time.Time{} // no timestamp: always applies (sources that do not carry one)
	refresh(t, e, s)
	if e.Header().Status != "idle" {
		t.Fatalf("an untimed source was refused: %+v", e.Header())
	}
}

func TestEntry_StatusAndBackendAreHeaderFields(t *testing.T) {
	m := newMem(idle(1)...)
	e := NewEntry(sidA)
	s := src(m, "f1", true)
	s.Status, s.Backend = "running", "terminal"
	refresh(t, e, s)
	if h := e.Header(); h.Status != "running" || h.Backend != "terminal" || !h.Live {
		t.Fatalf("header = %+v", h)
	}
	rev := e.Revision()

	refresh(t, e, s) // the same answer again: nothing moves
	if e.Revision() != rev {
		t.Fatalf("revision moved from %d to %d on an unchanged status", rev, e.Revision())
	}

	s.Status = "idle"
	r := refresh(t, e, s)
	if !r.Changed || e.Revision() != rev+1 || !e.HeaderChangedSince(rev) || e.Header().Status != "idle" {
		t.Fatalf("status change: %+v rev %d header %+v", r, e.Revision(), e.Header())
	}

	s.Status, s.Backend = "ended", ""
	refresh(t, e, s)
	if h := e.Header(); h.Status != "ended" || h.Backend != "" {
		t.Fatalf("header = %+v", h)
	}
}

package teammod

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// Plan T-3a2: a member's last turn, from the agent module's turn-end feed.

func (w *taskWorld) turn(sid, text string, at, seq int64) {
	w.t.Helper()
	w.m.onTurnEnd(agent.TurnEndEvent{SessionID: sid, Text: text, At: at, Seq: seq})
}

func (w *taskWorld) rowTurn(key string) (string, int64, int64) {
	w.t.Helper()
	var sum string
	var at, seq int64
	if err := w.m.store.db.QueryRow(`SELECT last_turn_summary, last_turn_at, last_turn_seq FROM team_members WHERE spawn_op = ?`, key).Scan(&sum, &at, &seq); err != nil {
		w.t.Fatal(err)
	}
	return sum, at, seq
}

func (w *taskWorld) taskTurn(id string) (string, int64) {
	w.t.Helper()
	_, d, e := w.showTask(leadInbox, id)
	if e.Error != "" || d.Task.LastTurn == nil {
		return "", 0
	}
	return d.Task.LastTurn.Summary, d.Task.LastTurn.At
}

func (w *taskWorld) start(id string) {
	w.t.Helper()
	if code, _, e := w.setStatus(leadInbox, id, team.TaskInProgress); code != http.StatusOK {
		w.t.Fatalf("start %s: %d %+v", id, code, e)
	}
}

// The member's in_progress task takes the turn; the member row does not.
func TestLastTurn_WritesTheMembersInProgressTask(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "own", nil)
	w.start(tk.ID)
	w.turn("sid-ma", "做完了。接著測試。", 100, 1)
	if sum, at := w.taskTurn(tk.ID); sum != "做完了。" || at != 100 {
		t.Fatalf("task last turn = %q @%d", sum, at)
	}
	if sum, at, _ := w.rowTurn(w.ma.SpawnOp); sum != "" || at != 0 {
		t.Fatalf("the member row took it too: %q @%d", sum, at)
	}
}

// Two in_progress tasks: the newest updated_at, then the highest seq.
func TestLastTurn_TieBreakNewestThenSeq(t *testing.T) {
	w := newTaskWorld(t)
	a := w.mustTask(leadInbox, w.ma.Ref, "a", nil)
	b := w.mustTask(leadInbox, w.ma.Ref, "b", nil)
	w.start(a.ID)
	w.start(b.ID)
	// the same updated_at on both: the higher seq (b) wins
	if _, err := w.m.store.db.Exec(`UPDATE tasks SET updated_at = 500 WHERE team_id = ?`, uid(1)); err != nil {
		t.Fatal(err)
	}
	w.turn("sid-ma", "one.", 100, 1)
	if sum, _ := w.taskTurn(b.ID); sum != "one." {
		t.Fatalf("b = %q", sum)
	}
	if sum, _ := w.taskTurn(a.ID); sum != "" {
		t.Fatalf("a = %q", sum)
	}
	// a newer updated_at on a beats the higher seq
	if _, err := w.m.store.db.Exec(`UPDATE tasks SET updated_at = 900 WHERE team_id = ? AND seq = ?`, uid(1), 1); err != nil {
		t.Fatal(err)
	}
	w.turn("sid-ma", "two.", 200, 2)
	if sum, _ := w.taskTurn(a.ID); sum != "two." {
		t.Fatalf("a = %q", sum)
	}
}

// No in_progress task (a pending one does not count): the member's own row.
func TestLastTurn_NoTaskWritesTheMemberRow(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "pending", nil)
	w.turn("sid-ma", "No task yet. Second sentence.", 100, 7)
	if sum, at, seq := w.rowTurn(w.ma.SpawnOp); sum != "No task yet." || at != 100 || seq != 7 {
		t.Fatalf("row = %q @%d #%d", sum, at, seq)
	}
	if sum, _ := w.taskTurn(tk.ID); sum != "" {
		t.Fatalf("the pending task took it: %q", sum)
	}
}

// A lead, a solo session and an unknown one write nothing.
// Mutation gate: skip the member check → red.
func TestLastTurn_LeadAndSoloWriteNothing(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "own", nil)
	w.start(tk.ID)
	for _, sid := range []string{"sid-1", "sid-n", "sid-nobody", "sid-mx"} { // lead, no role, unknown, another team's member
		w.turn(sid, "hello.", 100, 1)
	}
	if sum, _ := w.taskTurn(tk.ID); sum != "" {
		t.Fatalf("task took a stranger's turn: %q", sum)
	}
	var n int
	if err := w.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_members WHERE spawn_op = ? AND last_turn_at > 0`, w.ma.SpawnOp).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows with a turn: %d %v", n, err)
	}
}

// A killed or gone member, or a member of an ended team, is no member.
func TestLastTurn_OnlyActiveMembersOfLiveTeams(t *testing.T) {
	w := newTaskWorld(t)
	if _, err := w.m.store.db.Exec(`UPDATE team_members SET state = 'gone' WHERE spawn_op = ?`, w.mb.SpawnOp); err != nil {
		t.Fatal(err)
	}
	w.turn("sid-mb", "late.", 100, 1)
	if _, at, _ := w.rowTurn(w.mb.SpawnOp); at != 0 {
		t.Fatalf("a gone member took a turn @%d", at)
	}
}

// Delivered out of order, or twice: the newest (at, seq) stays. Same-ms stamps
// are ordered by seq. Mutation gate: guard on at only → the same-ms case is red.
func TestLastTurn_OlderEventNeverOverwritesNewer(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "own", nil)
	w.start(tk.ID)
	w.turn("sid-ma", "newer.", 100, 5)
	w.turn("sid-ma", "older.", 99, 9) // an earlier ms
	w.turn("sid-ma", "same ms lower seq.", 100, 4)
	if sum, at := w.taskTurn(tk.ID); sum != "newer." || at != 100 {
		t.Fatalf("task = %q @%d", sum, at)
	}
	w.turn("sid-ma", "same ms higher seq.", 100, 6)
	if sum, _ := w.taskTurn(tk.ID); sum != "same ms higher seq." {
		t.Fatalf("task = %q", sum)
	}
	w.turn("sid-ma", "same ms higher seq.", 100, 6) // a repeat: no change, no error
}

// A relay moves the member to a new session; its last turn stays.
// Mutation gate: reset last_turn_* in resetMemberUsage → red.
func TestLastTurn_SurvivesARelayOnTheMemberRow(t *testing.T) {
	w := newTaskWorld(t)
	w.turn("sid-ma", "before the relay.", 100, 1)
	if _, err := w.m.store.db.Exec(`UPDATE team_members SET session_id = 'sid-ma2', ref = '_new', updated_at = 5, `+resetMemberUsage+` WHERE spawn_op = ?`, w.ma.SpawnOp); err != nil {
		t.Fatal(err)
	}
	if sum, at, _ := w.rowTurn(w.ma.SpawnOp); sum != "before the relay." || at != 100 {
		t.Fatalf("after the relay: %q @%d", sum, at)
	}
}

// The relay's real statement (relay_store_report.go) does not touch last_turn_*.
func TestLastTurn_ResetMemberUsageDoesNotNameLastTurn(t *testing.T) {
	if strings.Contains(resetMemberUsage, "last_turn") {
		t.Fatalf("resetMemberUsage resets the last turn: %s", resetMemberUsage)
	}
}

func TestLastTurn_TextRule(t *testing.T) {
	long := strings.Repeat("字", 300)
	for name, tc := range map[string]struct{ in, want string }{
		"one line":                    {"完成。", "完成。"},
		"first sentence (CJK)":        {"第一句。第二句。", "第一句。"},
		"first sentence (Latin)":      {"Done with it. Then more.", "Done with it."},
		"two sentences, one line":     {"Why?  Because.", "Why?"},
		"a dot inside a word":         {"see v1.2 for details", "see v1.2 for details"},
		"no sentence end":             {"just words and no end", "just words and no end"},
		"the first non-empty line":    {"\n\n  \nsecond line. x\nthird", "second line."},
		"whitespace collapsed":        {"a \t b   c.", "a b c."},
		"over 200 runes, no end":      {long, strings.Repeat("字", 200) + "…"},
		"sentence end beyond 200":     {strings.Repeat("字", 250) + "。", strings.Repeat("字", 200) + "…"},
		"sentence end at 200 exactly": {strings.Repeat("字", 199) + "。later", strings.Repeat("字", 199) + "。"},
		"whitespace only":             {" \n\t ", ""},
		"empty":                       {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := lastTurnSummary(tc.in); got != tc.want {
				t.Errorf("lastTurnSummary(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Nothing (empty text) is not written, and an empty summary never blanks an older one.
func TestLastTurn_EmptyTextWritesNothing(t *testing.T) {
	w := newTaskWorld(t)
	w.turn("sid-ma", "kept.", 100, 1)
	w.turn("sid-ma", "  \n ", 200, 2)
	if sum, at, _ := w.rowTurn(w.ma.SpawnOp); sum != "kept." || at != 100 {
		t.Fatalf("row = %q @%d", sum, at)
	}
}

// The task write announces the roster; a member-row write does not need to.
func TestLastTurn_ATaskWriteSignalsTheRoster(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "own", nil)
	w.start(tk.ID)
	w.m.rosterSig = make(chan struct{}, 1)
	w.turn("sid-ma", "x.", 100, 1)
	select {
	case <-w.m.rosterSig:
	default:
		t.Fatal("no roster signal for a task write")
	}
}

// GET /api/team shows the member row's turn as LAST when it has no task, and the
// later of that and the task's when it only has a pending one.
func TestLastTurn_TheTeamViewShowsTheMemberRowsTurn(t *testing.T) {
	w := newTaskWorld(t)
	w.turn("sid-mb", "no task here.", 12345, 1)
	code, view, e := call[team.TeamView](w.fixture, http.MethodGet, "/api/team?origin_inbox="+leadInbox, nil)
	if code != 200 {
		t.Fatalf("%d %+v", code, e)
	}
	var got int64
	for _, mv := range view.Members {
		if mv.SpawnOp == w.mb.SpawnOp {
			got = mv.LastAt
		}
	}
	if got != 12345 {
		t.Fatalf("LastAt = %d", got)
	}
}

// A team.db written before T-3a2 gets the columns.
func TestLastTurn_OldDatabaseGainsTheColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE team_members (spawn_op TEXT PRIMARY KEY, team_id TEXT NOT NULL, host_id TEXT NOT NULL, session_id TEXT NOT NULL,
		ref TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL, tmux_session TEXT NOT NULL, tmux_id TEXT NOT NULL DEFAULT '',
		tmux_instance TEXT NOT NULL DEFAULT '', pane_id TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0, proc_start TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '', effort TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open an old team.db: %v", err)
	}
	defer s.Close()
	for _, col := range []string{"last_turn_summary", "last_turn_at", "last_turn_seq"} {
		if _, found, err := columnType(s.db, "team_members", col); err != nil || !found {
			t.Errorf("column %s missing (%v)", col, err)
		}
	}
}

// The module subscribes in Start and lets go in Stop.
type fakeTurnEnds struct {
	fn    func(agent.TurnEndEvent)
	unsub int
}

func (f *fakeTurnEnds) SubscribeTurnEnd(fn func(agent.TurnEndEvent)) func() {
	f.fn = fn
	return func() { f.unsub++ }
}

func TestLastTurn_SubscribesAndLetsGo(t *testing.T) {
	w := newTaskWorld(t)
	f := &fakeTurnEnds{}
	w.m.subscribeTurnEnd(f)
	if f.fn == nil || w.m.unsubTurnEnd == nil {
		t.Fatal("not subscribed")
	}
	f.fn(agent.TurnEndEvent{SessionID: "sid-ma", Text: "via the feed.", At: 100, Seq: 1})
	if sum, _, _ := w.rowTurn(w.ma.SpawnOp); sum != "via the feed." {
		t.Fatalf("row = %q", sum)
	}
	w.m.subscribeTurnEnd(struct{}{}) // a service without the method: no panic, no subscription change
	_ = w.m.Stop(nil)
	if f.unsub != 1 || w.m.unsubTurnEnd != nil {
		t.Fatalf("unsub = %d", f.unsub)
	}
}

// The target moves with the task's status; a late event must not reappear on the place that is empty
// (codex R1). Mutation gate: drop the cross-location check → red.
func TestLastTurn_ALateEventNeverReappearsWhenTheTargetMoves(t *testing.T) {
	w := newTaskWorld(t)
	w.turn("sid-ma", "newer, on the row.", 200, 2) // no task: the member row
	tk := w.mustTask(leadInbox, w.ma.Ref, "own", nil)
	w.start(tk.ID)
	w.turn("sid-ma", "older, late.", 100, 1) // now an in_progress task is the target
	if sum, _ := w.taskTurn(tk.ID); sum != "" {
		t.Fatalf("a late event became visible on the task: %q", sum)
	}
	w.turn("sid-ma", "newest.", 300, 3)
	if sum, _ := w.taskTurn(tk.ID); sum != "newest." {
		t.Fatalf("task = %q", sum)
	}
	// and back: the task completes, a late event must not move onto the row either
	if code, _, e := w.setStatus(leadInbox, tk.ID, team.TaskCompleted); code != http.StatusOK {
		t.Fatalf("%d %+v", code, e)
	}
	w.turn("sid-ma", "late again.", 250, 9)
	if sum, at, _ := w.rowTurn(w.ma.SpawnOp); at != 200 || sum != "newer, on the row." {
		t.Fatalf("row = %q @%d", sum, at)
	}
}

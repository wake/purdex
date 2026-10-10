package workbook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// WB-1b′-b: the engine's test kit.

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type fakeTurns struct {
	mu    sync.Mutex
	turns map[string][]convmodel.Turn
	err   map[string]error
	calls int
}

func (f *fakeTurns) LastTurns(_ context.Context, _, sid string, n int) ([]convmodel.Turn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := f.err[sid]; err != nil {
		return nil, err
	}
	ts := f.turns[sid]
	if len(ts) > n {
		ts = ts[len(ts)-n:]
	}
	return append([]convmodel.Turn(nil), ts...), nil
}

func (f *fakeTurns) set(sid string, ts ...convmodel.Turn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turns[sid] = ts
}

type fakeLineage map[string]string

func (l fakeLineage) RootSessionOf(sid string) (string, error) {
	if r, ok := l[sid]; ok {
		return r, nil
	}
	return sid, nil
}

type fakeSeats map[string]team.Seat

func (s fakeSeats) SeatOf(sid string) (team.Seat, error) { return s[sid], nil }

type kit struct {
	t       *testing.T
	e       *Engine
	st      *Store
	clock   *fakeClock
	turns   *fakeTurns
	lineage fakeLineage
	capable map[string]bool
	refresh []CapSession // the sessions whose mod announced workbook.refresh
	logs    *logSink
	afters  []func()
	lines   []string // push-line hook calls: "<entry>:<ready>"
}

type logSink struct {
	mu sync.Mutex
	s  []string
}

func (l *logSink) add(f string, a ...any) {
	l.mu.Lock()
	l.s = append(l.s, fmt.Sprintf(f, a...))
	l.mu.Unlock()
}
func (l *logSink) text() string { l.mu.Lock(); defer l.mu.Unlock(); return strings.Join(l.s, "\n") }

func newKit(t *testing.T) *kit {
	t.Helper()
	k := &kit{t: t, st: openTest(t), clock: &fakeClock{t: time.Unix(1_700_000_000, 0)}, lineage: fakeLineage{},
		turns: &fakeTurns{turns: map[string][]convmodel.Turn{}, err: map[string]error{}}, capable: map[string]bool{}, logs: &logSink{}}
	k.e = NewEngine(Deps{Store: k.st, Turns: k.turns, Lineage: k.lineage, HostID: "h1",
		Seats:           fakeSeats{"s1": {TeamID: "team-9", Role: team.SeatMember}},
		Capable:         func(sid string) bool { return k.capable[sid] },
		RefreshSessions: func() []CapSession { return k.refresh },
		Now:             k.clock.Now,
		After: func(_ time.Duration, f func()) func() bool {
			k.afters = append(k.afters, f)
			return func() bool { return true }
		},
		Logf: k.logs.add})
	k.e.SetPushLineHook(func(id int64, ready bool) { k.lines = append(k.lines, fmt.Sprintf("%d:%v", id, ready)) })
	return k
}

func endedTurn(id string, endedAt int64, text string) convmodel.Turn {
	return convmodel.Turn{ID: id, Outcome: convmodel.OutcomeDone, StartedAt: endedAt - 5, EndedAt: &endedAt,
		Items: []convmodel.Item{userItem("做 " + id), agentItem(text)}}
}

func runningTurn(id string) convmodel.Turn {
	return convmodel.Turn{ID: id, Outcome: convmodel.OutcomeRunning, Items: []convmodel.Item{userItem("go"), agentItem("working")}}
}

func (k *kit) event(sid string, at int64) {
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: sid, Text: "hook text", At: at, Seq: at})
}

func (k *kit) entries(conv string) []Entry {
	k.t.Helper()
	rows, err := k.st.Conversation(conv, 50, 0)
	if err != nil {
		k.t.Fatal(err)
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

func (k *kit) next(stream, sid string) (Job, bool) {
	return k.e.Next(context.Background(), stream, sid, 0)
}

func mustNext(t *testing.T, k *kit, stream, sid string) Job {
	t.Helper()
	j, ok := k.next(stream, sid)
	if !ok {
		t.Fatalf("no job for %s", sid)
	}
	return j
}

// answerJSON is a valid seven-field answer.
func answerJSON(thing, push, entry, status string, todos string) Result {
	if todos == "" {
		todos = `{"done":[],"dropped":[],"add":[]}`
	}
	b, _ := json.Marshal(map[string]any{"skip": false, "thing": thing, "push": push, "entry": entry, "status": status, "thing_done": false})
	text := strings.TrimSuffix(string(b), "}") + `,"todos":` + todos + "}"
	return Result{Answered: true, Text: text, Usage: Usage{In: 100, Out: 20, CacheRead: 50}, LatencyMS: 900}
}

func longEntry() string {
	return strings.Repeat("這是一句不長不短剛好三十五個字的句子用來湊數足夠長的文字喔喔喔喔喔喔。", 6)
}

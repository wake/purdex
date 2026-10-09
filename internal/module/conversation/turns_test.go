package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
)

var _ convturns.Reader = (*Module)(nil)

// The last n turns, oldest first, with their ids, outcomes and items.
func TestLastTurns_OldestFirstWithIDsAndItems(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(6))
	turns, err := e.mod.LastTurns(context.Background(), "claude", sid, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 4 {
		t.Fatalf("turns = %d, want 4", len(turns))
	}
	for i := 1; i < len(turns); i++ {
		if turns[i].Index != turns[i-1].Index+1 {
			t.Fatalf("not consecutive oldest first: %d then %d", turns[i-1].Index, turns[i].Index)
		}
	}
	last := turns[3]
	if last.ID == "" || last.Outcome != convmodel.OutcomeDone || last.EndedAt == nil {
		t.Fatalf("last = %+v", last)
	}
	var user, agent string
	for _, it := range last.Items {
		if it.User != nil {
			user = it.User.Text
		}
		if it.AgentText != nil {
			agent = it.AgentText.Markdown
		}
	}
	if user != "question 5" || agent != "answer 5" {
		t.Fatalf("items = %q / %q", user, agent)
	}
	// the ids are the ones the HTTP snapshot shows (stable across reads of one transcript)
	again, _ := e.mod.LastTurns(context.Background(), "claude", sid, 4)
	for i := range turns {
		if turns[i].ID != again[i].ID {
			t.Fatalf("turn id %d moved between reads: %q then %q", i, turns[i].ID, again[i].ID)
		}
	}
}

// A turn that has not ended is reported running; the caller drops it.
func TestLastTurns_ReportsARunningTurn(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(2) + userRow("u9", 50, "still working") + "\n")
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1}} // a live pane: the open turn is running
	turns, err := e.mod.LastTurns(context.Background(), "claude", sid, 3)
	if err != nil || len(turns) == 0 {
		t.Fatalf("turns=%d err=%v", len(turns), err)
	}
	if got := turns[len(turns)-1]; got.Outcome != convmodel.OutcomeRunning || got.EndedAt != nil {
		t.Fatalf("last = %+v, want running", got)
	}
}

func TestLastTurns_NotFound(t *testing.T) {
	e := newEnv(t)
	if _, err := e.mod.LastTurns(context.Background(), "claude", sid, 3); !errors.Is(err, convfeed.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestLastTurns_BusyWhenEveryEntryIsInUse(t *testing.T) {
	e := newEnv(t)
	e.mod.cache = convfeed.NewCache(convfeed.CacheOptions{Max: 1})
	_, release, err := e.mod.cache.Acquire(context.Background(), "someone-else")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := e.mod.LastTurns(context.Background(), "claude", sid, 3); !errors.Is(err, convfeed.ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

// What a caller does to the answer never reaches the cache: the HTTP snapshot and the next read still show the original.
func TestLastTurns_TheAnswerIsACopy(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(3))
	first, err := e.mod.LastTurns(context.Background(), "claude", sid, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first: %d %v", len(first), err)
	}
	for _, it := range first[1].Items {
		if it.User != nil {
			it.User.Text = "scribbled"
		}
		if it.AgentText != nil {
			it.AgentText.Markdown = "scribbled"
		}
	}
	first[1].Items = nil
	second, _ := e.mod.LastTurns(context.Background(), "claude", sid, 2)
	var texts []string
	for _, it := range second[1].Items {
		if it.User != nil {
			texts = append(texts, it.User.Text)
		}
		if it.AgentText != nil {
			texts = append(texts, it.AgentText.Markdown)
		}
	}
	if len(texts) != 2 || texts[0] != "question 2" || texts[1] != "answer 2" {
		t.Fatalf("the cache was changed through the answer: %v", texts)
	}
	if body := e.get("/api/conversations/claude/" + sid).Body.String(); !strings.Contains(body, "question 2") || strings.Contains(body, "scribbled") {
		t.Fatalf("the HTTP snapshot was changed: %s", body)
	}
}

func TestLastTurns_OtherProviderAndBadCount(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(1))
	if _, err := e.mod.LastTurns(context.Background(), "codex", sid, 3); !errors.Is(err, convturns.ErrUnsupportedProvider) {
		t.Fatalf("codex err = %v", err)
	}
	if _, err := e.mod.LastTurns(context.Background(), "claude", sid, 0); err == nil {
		t.Fatal("n = 0 must be an error")
	}
}

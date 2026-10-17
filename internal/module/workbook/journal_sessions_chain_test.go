package workbook

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

type errPred struct{}

func (errPred) PredecessorOf(string) (string, int64, bool, error) {
	return "", 0, false, errors.New("db down")
}

func (j *journalEnv) expect503(urls ...string) {
	j.t.Helper()
	for _, u := range urls {
		if code, _ := j.request(http.MethodGet, u, nil); code != http.StatusServiceUnavailable {
			j.t.Errorf("%s = %d, want 503", u, code)
		}
	}
}

const bothRoutes = "/api/journal/sessions?session_id=s3"

// A reader that fails is an error, not the end of the chain. Mutation gate: treat a read error as "no predecessor" -> 200 (red).
func TestJournalSessions_ReaderErrorIs503(t *testing.T) {
	for name, reg := range map[string]func(j *journalEnv){
		"relay": func(j *journalEnv) {
			j.m.core.Registry.Register(team.PredecessorReaderKey, team.PredecessorReader(errPred{}))
			j.m.core.Registry.Register(agent.PredecessorReaderKey, agent.PredecessorReader(fakePred{}))
		},
		"clear": func(j *journalEnv) {
			j.m.core.Registry.Register(team.PredecessorReaderKey, team.PredecessorReader(fakePred{}))
			j.m.core.Registry.Register(agent.PredecessorReaderKey, agent.PredecessorReader(errPred{}))
		},
	} {
		t.Run(name, func(t *testing.T) {
			j := newJournalAPI(t)
			j.roots.root["s3"] = "s1"
			j.unit("solo:s1", "A")
			reg(j)
			j.expect503(bothRoutes, "/api/journal?session_id=s3", "/api/journal?session_id=s3&page=s3")
		})
	}
}

// A loop and a chain over the limit are errors, not a root. Mutation gate: break out of the loop silently -> 200 (red).
func TestJournalSessions_LoopAndTooLongAre503(t *testing.T) {
	j := newJournalAPI(t)
	j.roots.root["s3"] = "s1"
	j.unit("solo:s1", "A")
	j.preds(map[string][2]any{"s3": {"s2", int64(3)}, "s2": {"s1", int64(2)}, "s1": {"s3", int64(1)}}, nil)
	j.expect503(bothRoutes, "/api/journal?session_id=s3")

	j.preds(map[string][2]any{"s3": {"s2", int64(3)}, "s2": {"s1", int64(2)}, "s1": {"s0", int64(1)}}, nil)
	old := maxChain
	t.Cleanup(func() { maxChain = old })
	maxChain = 2 // s3 <- s2 <- s1 <- s0 needs three links
	j.expect503(bothRoutes, "/api/journal?session_id=s3")
	maxChain = 3
	if code, ss := j.sessions(bothRoutes); code != http.StatusOK || len(ss) != 4 {
		t.Fatalf("at the limit: %d %+v", code, ss)
	}
}

// An entry written by a session that is not on the chain stays off every page. Mutation gate: attribute it by time -> it shows
// on a page (red).
func TestJournalSessions_AnEntryFromOutsideTheChainIsOnNoPage(t *testing.T) {
	j := newJournalAPI(t)
	j.roots.root["s2"] = "s1"
	u := j.unit("solo:s1", "A")
	j.preds(map[string][2]any{"s2": {"s1", int64(5000)}}, nil)
	j.write(u,
		journal.Entry{ID: "ghost", Kind: journal.KindComm, At: time.UnixMilli(1000), SID: "elsewhere"},
		journal.Entry{ID: "mine", Kind: journal.KindComm, At: time.UnixMilli(1100), SID: "s1"},
	)
	for _, page := range []string{"s1", "s2"} {
		_, p := j.pageOf("/api/journal?session_id=s2&page=" + page)
		if slices.Contains(ids(p.Entries), "ghost") {
			t.Errorf("page %s shows the orphan: %v", page, ids(p.Entries))
		}
	}
	if _, p := j.pageOf("/api/journal?session_id=s2&page=s1"); !slices.Equal(ids(p.Entries), []string{"mine"}) {
		t.Errorf("page s1 = %v", ids(p.Entries))
	}
}

// With a predecessor reader missing the chain is flagged incomplete; with both it is not. Mutation gate: never set it -> red.
func TestJournalSessions_IncompleteWhenAReaderIsMissing(t *testing.T) {
	j := newJournalAPI(t)
	j.unit("solo:s1", "A")
	read := func() map[string]any {
		code, body := j.request(http.MethodGet, "/api/journal/sessions?session_id=s1", nil)
		if code != http.StatusOK {
			t.Fatalf("%d", code)
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := read(); m["incomplete"] != true {
		t.Errorf("no readers: %v", m)
	}
	j.preds(nil, nil)
	if m := read(); m["incomplete"] != nil {
		t.Errorf("both readers: %v", m)
	}
}

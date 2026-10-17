package workbook

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// fakePred answers PredecessorOf from a map: session -> {prev, at}.
type fakePred struct{ m map[string][2]any }

func (f fakePred) PredecessorOf(sid string) (string, int64, bool, error) {
	v, ok := f.m[sid]
	if !ok {
		return "", 0, false, nil
	}
	return v[0].(string), v[1].(int64), true, nil
}

type jSession struct {
	SessionID string `json:"session_id"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
	Via       string `json:"via"`
	Current   bool   `json:"current"`
}

func (j *journalEnv) sessions(url string) (int, []jSession) {
	j.t.Helper()
	code, body := j.request(http.MethodGet, url, nil)
	var out struct {
		Sessions []jSession `json:"sessions"`
	}
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			j.t.Fatal(err)
		}
	}
	return code, out.Sessions
}

func (j *journalEnv) preds(relay, clear map[string][2]any) {
	j.m.core.Registry.Register(team.PredecessorReaderKey, team.PredecessorReader(fakePred{relay}))
	j.m.core.Registry.Register(agent.PredecessorReaderKey, agent.PredecessorReader(fakePred{clear}))
}

func (j *journalEnv) pageOf(url string) (int, struct {
	jPage
	Page      string `json:"page"`
	Truncated bool   `json:"truncated"`
}) {
	j.t.Helper()
	code, body := j.request(http.MethodGet, url, nil)
	var p struct {
		jPage
		Page      string `json:"page"`
		Truncated bool   `json:"truncated"`
	}
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &p); err != nil {
			j.t.Fatal(err)
		}
	}
	return code, p
}

// The chain is oldest first over both links. Mutation gate: drop the /clear reader -> s1 is missing (red).
func TestJournalSessions_ChainOverRelayAndClear(t *testing.T) {
	j := newJournalAPI(t)
	j.roots.root["s3"] = "s1"
	j.unit("solo:s1", "A")
	j.preds(map[string][2]any{"s3": {"s2", int64(2000)}}, map[string][2]any{"s2": {"s1", int64(1000)}})
	code, ss := j.sessions("/api/journal/sessions?session_id=s3")
	if code != http.StatusOK || len(ss) != 3 {
		t.Fatalf("%d %+v", code, ss)
	}
	want := []jSession{{"s1", 0, 1000, "root", false}, {"s2", 1000, 2000, "clear", false}, {"s3", 2000, 0, "relay", true}}
	if !slices.Equal(ss, want) {
		t.Fatalf("got %+v want %+v", ss, want)
	}
}

// When both readers answer for the same session at the same instant, the relay's record comes first (wins).
// Mutation gate: consult the clear reader first -> via "clear" (red).
func TestJournalSessions_RelayBeforeClearAtTheSameInstant(t *testing.T) {
	j := newJournalAPI(t)
	j.roots.root["s2"] = "s1"
	j.unit("solo:s1", "A")
	j.preds(map[string][2]any{"s2": {"s1", int64(500)}}, map[string][2]any{"s2": {"sX", int64(500)}})
	_, ss := j.sessions("/api/journal/sessions?session_id=s2")
	if len(ss) != 2 || ss[0].SessionID != "s1" || ss[1].Via != "relay" {
		t.Fatalf("%+v", ss)
	}
}

// Entries are attributed: sid first, then wb_entries for turn:/ev:, else by span; page returns only that session.
// Mutation gate: ignore `page` -> every session's entries (red).
func TestJournalSessions_PagesAttributeEntries(t *testing.T) {
	j := newJournalAPI(t)
	j.roots.root["s2"] = "s1"
	u := j.unit("solo:s1", "A")
	j.preds(map[string][2]any{"s2": {"s1", int64(5000)}}, nil)
	e := pending("s1", "s1", "t1", 100)
	e.TeamID, e.Role = "", team.SeatNone
	id := mustInsert(t, j.store, e)
	ms := func(n int64) time.Time { return time.UnixMilli(n) }
	j.write(u,
		journal.Entry{ID: fmt.Sprintf("turn:%d", id), Kind: journal.KindExec, At: ms(9000), Text: "old turn, by wb_entries (time says s2)"},
		journal.Entry{ID: "bySpan1", Kind: journal.KindComm, At: ms(1000), Text: "span s1"},
		journal.Entry{ID: "bySpan2", Kind: journal.KindComm, At: ms(6000), Text: "span s2"},
		journal.Entry{ID: "stamped", Kind: journal.KindComm, At: ms(1500), SID: "s2", Text: "sid wins over span"},
		journal.Entry{ID: "ask", Kind: journal.KindConfirm, At: ms(1200), Items: []journal.Item{{Text: "?"}}},
	)
	_, p := j.pageOf("/api/journal?session_id=s2&page=s1")
	if got := ids(p.Entries); !slices.Equal(got, []string{"bySpan1", "ask", fmt.Sprintf("turn:%d", id)}) || p.Page != "s1" {
		t.Fatalf("page s1 = %v (%s)", got, p.Page)
	}
	_, cur := j.pageOf("/api/journal?session_id=s2")
	if got := ids(cur.Entries); !slices.Equal(got, []string{"stamped", "bySpan2"}) || cur.Page != "s2" {
		t.Fatalf("current = %v (%s)", got, cur.Page)
	}
	// an older page still carries the whole unit's confirms
	if len(cur.Confirms) != 1 || cur.OpenConfirms != 1 || len(p.Confirms) != 1 {
		t.Fatalf("confirms %v %d", ids(cur.Confirms), cur.OpenConfirms)
	}
	if code, _ := j.pageOf("/api/journal?session_id=s2&page=nope"); code != http.StatusNotFound {
		t.Fatalf("unknown page = %d", code)
	}
	// older Apps: before / limit keep the old paging
	_, old := j.pageOf("/api/journal?session_id=s2&limit=2")
	if len(old.Entries) != 2 || !old.More || old.Page != "" {
		t.Fatalf("legacy = %v more=%v", ids(old.Entries), old.More)
	}
}

// Mutation gate: drop the cap -> 1001 entries (red).
func TestJournalSessions_PageCapTruncates(t *testing.T) {
	j := newJournalAPI(t)
	u := j.unit("solo:s1", "A")
	var es []journal.Entry
	for i := 0; i < pageEntryCap+5; i++ {
		es = append(es, journal.Entry{ID: fmt.Sprintf("e%05d", i), Kind: journal.KindComm, At: time.UnixMilli(int64(1000 + i))})
	}
	j.write(u, es...)
	_, p := j.pageOf("/api/journal?session_id=s1")
	if len(p.Entries) != pageEntryCap || !p.Truncated || p.Entries[len(p.Entries)-1].ID != "e01004" {
		t.Fatalf("%d truncated=%v", len(p.Entries), p.Truncated)
	}
}

// Mutation gate: drop the capability line -> red.
func TestWire_InfoAnnouncesJournalSessionsForTheRealModule(t *testing.T) {
	k := newWire(t, nil)
	k.c.AddModule(k.m)
	mux := http.NewServeMux()
	k.c.RegisterCoreRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
	var body struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(body.Capabilities, "journal.sessions.v1") {
		t.Errorf("capabilities = %v", body.Capabilities)
	}
	var _ *core.Core = k.c
}

// New entries carry the leader session id. Mutation gate: drop SID in the exec entry -> red.
func TestDrain_NewEntriesCarryTheLeaderSession(t *testing.T) {
	k := newDrainKit(t)
	id := k.turn("c", "t1", team.SeatNone, TodoChanges{})
	k.okEntry(id, "x。", false)
	k.drain()
	e, ok := k.byID("solo:c", "turn:"+itoa(id))
	if !ok || e.SID != "c-s" {
		t.Fatalf("%+v %v", e, ok)
	}
}

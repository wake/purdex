package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

type membersBody struct {
	Members []struct {
		Ref       string `json:"ref"`
		SessionID string `json:"session_id"`
		Title     string `json:"title"`
		Task      *struct {
			Number string `json:"number"`
			Title  string `json:"title"`
			State  string `json:"state"`
		} `json:"task"`
		Last *struct {
			At   int64  `json:"at"`
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"last"`
	} `json:"members"`
}

// membersEnv is the journal API environment with a team feed wired as the journal run's.
func membersEnv(t *testing.T, feed *fakeFeed) *journalEnv {
	t.Helper()
	j := newJournalAPI(t)
	j.m.mu.Lock()
	j.m.journalRun = &journalRun{feed: feed}
	j.m.mu.Unlock()
	j.seats.seats["lead-s"] = team.Seat{Role: team.SeatLead, TeamID: "t1"}
	j.seats.seats["mem-s"] = team.Seat{Role: team.SeatMember, TeamID: "t1"}
	return j
}

func getMembers(t *testing.T, j *journalEnv, sid string) (int, membersBody) {
	t.Helper()
	w := j.get("/api/journal/members?session_id=" + sid)
	var b membersBody
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatalf("body %s: %v", w.Body.String(), err)
		}
	}
	return w.Code, b
}

// The wire: each current member with its current task and its newest report; the newest by created_at, a tie to the greater
// id; the first line of the summary is the text; a member with neither has nulls; another team's reports are not used.
// Mutations: take the first report seen instead of the newest → last is the old one; break ties by the smaller id → the
// tie case reads the wrong report; drop the TeamID filter → the other team's newer report shows.
func TestJournalMembers_WireShape(t *testing.T) {
	feed := &fakeFeed{
		members: map[string][]team.MemberInfo{"t1": {
			{MemberKey: "k1", Ref: "_aaaaaa", SessionID: "s-1", Title: "one", State: team.MemberActive},
			{MemberKey: "k2", Ref: "_bbbbbb", SessionID: "s-2", Title: "two", State: team.MemberActive},
			{MemberKey: "k3", Ref: "_cccccc", SessionID: "s-3", Title: "three", State: team.MemberActive},
		}},
		current: map[string]team.MemberTaskInfo{"t1/k1": {Seq: 4, Subject: "build it", Status: team.TaskInProgress}},
		reports: []team.ReportRow{
			{RowID: 1, TeamID: "t1", MemberKey: "k1", ID: "r-old", Kind: "progress", Summary: "old", CreatedAt: 100},
			{RowID: 2, TeamID: "t1", MemberKey: "k1", ID: "r-new", Kind: "ready", Summary: "PR is up\nsecond line", CreatedAt: 300},
			{RowID: 3, TeamID: "t1", MemberKey: "k1", ID: "r-mid", Kind: "progress", Summary: "mid", CreatedAt: 200},
			{RowID: 4, TeamID: "t1", MemberKey: "k2", ID: "r-a", Kind: "progress", Summary: "tie a", CreatedAt: 500},
			{RowID: 5, TeamID: "t1", MemberKey: "k2", ID: "r-b", Kind: "blocked", Summary: "tie b", CreatedAt: 500},
			{RowID: 6, TeamID: "t2", MemberKey: "k1", ID: "r-other", Kind: "done", Summary: "other team", CreatedAt: 900},
		},
	}
	j := membersEnv(t, feed)
	code, b := getMembers(t, j, "lead-s")
	if code != 200 || len(b.Members) != 3 {
		t.Fatalf("code %d members %+v", code, b.Members)
	}
	m1, m2, m3 := b.Members[0], b.Members[1], b.Members[2]
	if m1.Ref != "_aaaaaa" || m1.SessionID != "s-1" || m1.Title != "one" {
		t.Errorf("m1 identity = %+v", m1)
	}
	if m1.Task == nil || m1.Task.Number != "4" || m1.Task.Title != "build it" || m1.Task.State != "in_progress" {
		t.Errorf("m1 task = %+v", m1.Task)
	}
	if m1.Last == nil || m1.Last.At != 300 || m1.Last.Kind != "ready" || m1.Last.Text != "PR is up" {
		t.Errorf("m1 last = %+v, want the newest report, first line", m1.Last)
	}
	if m2.Last == nil || m2.Last.Kind != "blocked" || m2.Last.Text != "tie b" || m2.Task != nil {
		t.Errorf("m2 = %+v, want the greater id on a tie and no task", m2)
	}
	if m3.Task != nil || m3.Last != nil {
		t.Errorf("m3 = %+v, want nulls", m3)
	}
	// a member of the team reads the same list
	if code, b2 := getMembers(t, j, "mem-s"); code != 200 || len(b2.Members) != 3 {
		t.Errorf("member read: %d %+v", code, b2.Members)
	}
}

func TestJournalMembers_Errors(t *testing.T) {
	feed := &fakeFeed{members: map[string][]team.MemberInfo{"t1": {{MemberKey: "k1", Ref: "_aaaaaa", SessionID: "s-1"}}}}
	j := membersEnv(t, feed)
	if w := j.get("/api/journal/members"); w.Code != http.StatusBadRequest {
		t.Errorf("no session: %d", w.Code)
	}
	// a solo session has no members
	if code, b := getMembers(t, j, "solo-s"); code != 200 || b.Members == nil || len(b.Members) != 0 {
		t.Errorf("solo: %d %+v", code, b)
	}
	j.seats.seats["remote-s"] = team.Seat{Role: team.SeatMemberRemote, LeadHostID: "h2", LeadSessionID: "ls"}
	if w := j.get("/api/journal/members?session_id=remote-s"); w.Code != http.StatusConflict {
		t.Errorf("remote member: %d", w.Code)
	}
	feed.err = errors.New("boom")
	if w := j.get("/api/journal/members?session_id=lead-s"); w.Code != http.StatusInternalServerError {
		t.Errorf("feed error: %d, want 500 (never an empty answer)", w.Code)
	}
	// no team feed: unavailable
	j.m.mu.Lock()
	j.m.journalRun = &journalRun{}
	j.m.mu.Unlock()
	if w := j.get("/api/journal/members?session_id=lead-s"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no feed: %d", w.Code)
	}
}

// journal.members.v1 in /api/info with the real workbook module registered into core: declared while the journal runs with
// a team feed, not without one. Mutation: drop the `journal_members` key from Status → not declared and the test goes red.
func TestJournalMembers_CapabilityWithTheRealModule(t *testing.T) {
	caps := func(withFeed bool) []any {
		cfg := &config.Config{DataDir: t.TempDir(), HostID: "h1:abc"}
		c := core.New(core.CoreDeps{Config: cfg})
		c.Registry.Register(peersmod.OriginResolverKey, &fakeOrigins{})
		if withFeed {
			c.Registry.Register(team.JournalFeedKey, &fakeFeed{teams: map[string]team.TeamInfo{}})
		}
		m := New()
		c.AddModule(m)
		if err := m.Init(c); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { m.Stop(context.Background()); m.Close() })
		if err := m.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		c.RegisterCoreRoutes(mux)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/info", nil))
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body["capabilities"].([]any)
	}
	has := func(l []any, s string) bool {
		for _, v := range l {
			if v == s {
				return true
			}
		}
		return false
	}
	with := caps(true)
	if !has(with, "journal.v1") || !has(with, "journal.members.v1") {
		t.Errorf("with a feed: %v, want journal.v1 and journal.members.v1", with)
	}
	without := caps(false)
	if !has(without, "journal.v1") || has(without, "journal.members.v1") {
		t.Errorf("without a feed: %v, want journal.v1 only", without)
	}
}

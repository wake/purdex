package workbook

import (
	"errors"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// withLead wires a team feed that names lead as the team's current lead, for the tests where a member reads a page.
func withLead(j *journalEnv, teamID, lead string) {
	j.m.mu.Lock()
	j.m.journalRun = &journalRun{feed: &fakeFeed{teams: map[string]team.TeamInfo{teamID: {TeamID: teamID, LeadSessionID: lead}}}, cancel: func() {}}
	j.m.mu.Unlock()
}

// errInfoFeed is a feed whose TeamInfo can fail.
type errInfoFeed struct {
	*fakeFeed
	err error
}

func (f errInfoFeed) TeamInfo(id string) (team.TeamInfo, bool, error) {
	if f.err != nil {
		return team.TeamInfo{}, false, f.err
	}
	return f.fakeFeed.TeamInfo(id)
}

// A member reads the leader's chain, never its own: when the feed cannot name the current lead (error, unknown team, blank
// lead, no feed) the answer is 503, for the sessions and the page routes; the lead itself may stand in for the feed.
// Mutation gate: fall back to the reader for everyone -> the member gets 200 (red).
func TestJournalSessions_MemberNeedsAReliableLeader(t *testing.T) {
	cases := map[string]errInfoFeed{
		"error":   {fakeFeed: &fakeFeed{teams: map[string]team.TeamInfo{"t1": {TeamID: "t1", LeadSessionID: "lead-s"}}}, err: errors.New("boom")},
		"unknown": {fakeFeed: &fakeFeed{teams: map[string]team.TeamInfo{}}},
		"blank":   {fakeFeed: &fakeFeed{teams: map[string]team.TeamInfo{"t1": {TeamID: "t1"}}}},
	}
	for name, feed := range cases {
		t.Run(name, func(t *testing.T) {
			j := membersEnv(t, &fakeFeed{})
			j.m.journalRun.feed = feed
			j.unit("team:t1", "T")
			for _, url := range []string{"/api/journal/sessions?session_id=mem-s", "/api/journal?session_id=mem-s"} {
				if code, _ := j.request(http.MethodGet, url, nil); code != http.StatusServiceUnavailable {
					t.Errorf("%s: member got %d, want 503", url, code)
				}
			}
			if code, ss := j.sessions("/api/journal/sessions?session_id=lead-s"); code != http.StatusOK || len(ss) != 1 || ss[0].SessionID != "lead-s" {
				t.Errorf("the lead itself: %d %+v", code, ss)
			}
		})
	}
	// normal: the member gets the lead's chain
	j := membersEnv(t, &fakeFeed{teams: map[string]team.TeamInfo{"t1": {TeamID: "t1", LeadSessionID: "lead-2"}}})
	j.unit("team:t1", "T")
	j.preds(map[string][2]any{"lead-2": {"lead-s", int64(900)}}, nil)
	code, ss := j.sessions("/api/journal/sessions?session_id=mem-s")
	if code != http.StatusOK || len(ss) != 2 || ss[1].SessionID != "lead-2" || !ss[1].Current {
		t.Fatalf("member, normal: %d %+v", code, ss)
	}
}

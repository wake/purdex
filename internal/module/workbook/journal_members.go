package workbook

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// GET /api/journal/members (capability journal.members.v1): the current members of the reader's team, each with the task it
// is on and its last report to the leader. The journal's own request / comm entries carry no member identity, so they are
// not a source: only the team's reports are.

type journalMemberTaskJSON struct {
	Number string `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"` // pending | in_progress
}

type journalMemberLastJSON struct {
	At   int64  `json:"at"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type journalMemberJSON struct {
	Ref       string                 `json:"ref"`
	SessionID string                 `json:"session_id"`
	Title     string                 `json:"title"`
	Task      *journalMemberTaskJSON `json:"task"`
	Last      *journalMemberLastJSON `json:"last"`
}

const reportPage = 500

// teamFeed is the journal's team feed, nil while the journal is off or the daemon has none.
func (m *Module) teamFeed() team.JournalFeed {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.journalRun == nil {
		return nil
	}
	return m.journalRun.feed
}

// lastReports is the newest report of each member of the team: the later created_at wins, a tie goes to the greater id.
func lastReports(feed team.JournalFeed, teamID string) (map[string]team.ReportRow, error) {
	out := map[string]team.ReportRow{}
	var after int64
	for {
		page, err := feed.ReportsAfter(after, 0, reportPage)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		for _, r := range page {
			if r.TeamID != teamID {
				continue
			}
			if cur, ok := out[r.MemberKey]; !ok || r.CreatedAt > cur.CreatedAt || (r.CreatedAt == cur.CreatedAt && r.ID > cur.ID) {
				out[r.MemberKey] = r
			}
		}
		after = page[len(page)-1].RowID
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

// handleJournalMembers: GET /api/journal/members?session_id=<reader> → {members: […]}. The reader is a lead or a member of a
// live team (a solo session has no members: []); 409 remote_member for a member of a team led on another host, as for the
// other journal routes.
func (m *Module) handleJournalMembers(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	feed := m.teamFeed()
	if feed == nil || m.journalDisabled() {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	seat, err := m.seatOf(sessionID)
	if err != nil {
		log.Printf("[workbook] journal: seat of %s: %v", sessionID, err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	switch seat.Role {
	case team.SeatMemberRemote:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "remote_member", "lead_host_id": seat.LeadHostID, "lead_session_id": seat.LeadSessionID})
		return
	case team.SeatLead, team.SeatMember:
	default:
		seat.TeamID = ""
	}
	out := []journalMemberJSON{}
	if seat.TeamID == "" {
		writeJSON(w, http.StatusOK, map[string]any{"members": out})
		return
	}
	members, err := feed.Members(seat.TeamID)
	if err == nil {
		var last map[string]team.ReportRow
		if last, err = lastReports(feed, seat.TeamID); err == nil {
			for _, mi := range members {
				jm := journalMemberJSON{Ref: mi.Ref, SessionID: mi.SessionID, Title: mi.Title}
				cur, ok, cerr := feed.CurrentTask(seat.TeamID, mi.MemberKey)
				if cerr != nil {
					err = cerr
					break
				}
				if ok {
					jm.Task = &journalMemberTaskJSON{Number: strconv.Itoa(cur.Seq), Title: cur.Subject, State: string(cur.Status)}
				}
				if rep, ok := last[mi.MemberKey]; ok {
					text := firstLine(rep.Summary)
					if text == "" {
						text = firstLine(rep.Body)
					}
					jm.Last = &journalMemberLastJSON{At: rep.CreatedAt, Kind: rep.Kind, Text: text}
				}
				out = append(out, jm)
			}
		}
	}
	if err != nil {
		log.Printf("[workbook] journal: members of team %s: %v", seat.TeamID, err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

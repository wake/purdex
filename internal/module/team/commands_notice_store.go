// internal/module/team/commands_notice_store.go
package teammod

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// oweNoticeIn writes the notice a command owes its member, in the command's own transaction (spec §4.4). One
// per (member, kind, causing command), so a re-applied transaction cannot double it. The two strings are the
// snapshot the notice is about — the lead address and team name as THIS event left them, cleaned and bounded —
// so a later lead_moved cannot change what an earlier notice says; delivery (X3d) fills M's fixed template
// from them.
func oweNoticeIn(tx dbtx, mk, kind, causeID, leadAddress, teamName string, at int64) error {
	_, err := tx.Exec(`INSERT INTO remote_notices (mk, kind, cause_id, lead_address, team_name, state, attempts, next_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?) ON CONFLICT (mk, kind, cause_id) DO NOTHING`,
		mk, kind, causeID, cleanNoticeField(leadAddress), cleanNoticeField(teamName), noticeOwed, at, at, at)
	if err != nil {
		return fmt.Errorf("owe %s notice to %s: %w", kind, mk, err)
	}
	return nil
}

// cleanNoticeField is s as a notice may carry it: control characters dropped, cut to 64 bytes on a rune boundary.
func cleanNoticeField(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) <= 64 {
		return s
	}
	cut := 64
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// splitTmux splits a registry "<session>:@<win>.%<pane>" into its session name and pane id; either may be "".
func splitTmux(t string) (session, pane string) {
	if i := strings.IndexByte(t, ':'); i >= 0 {
		session = t[:i]
	} else {
		return t, ""
	}
	if j := strings.LastIndexByte(t, '.'); j >= 0 && strings.HasPrefix(t[j+1:], "%") {
		pane = t[j+1:]
	}
	return session, pane
}

// remoteNoticeRow is one remote_notices row.
type remoteNoticeRow struct {
	ID          int64
	MK          string
	Kind        string
	CauseID     string
	LeadAddress string
	TeamName    string
	State       string
	Attempts    int
	NextAt      int64
}

// RemoteNotices lists the notices owed to (or sent to) the member mk, oldest first.
func (s *Store) RemoteNotices(mk string) ([]remoteNoticeRow, error) {
	rows, err := s.db.Query(`SELECT id, mk, kind, cause_id, lead_address, team_name, state, attempts, next_at FROM remote_notices WHERE mk = ? ORDER BY id`, mk)
	if err != nil {
		return nil, fmt.Errorf("remote notices %s: %w", mk, err)
	}
	defer rows.Close()
	out := []remoteNoticeRow{}
	for rows.Next() {
		var n remoteNoticeRow
		if err := rows.Scan(&n.ID, &n.MK, &n.Kind, &n.CauseID, &n.LeadAddress, &n.TeamName, &n.State, &n.Attempts, &n.NextAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// PanePlacement is what one `list-panes -a` pass says about a pane: the pid of
// its process and the name of the session that owns it, i.e. what
// ActivePanePID and PaneSessionName answer one tmux round trip at a time.
type PanePlacement struct {
	PID         string
	SessionName string
	// Ambiguous: the pane is linked into several sessions and the listing
	// cannot say which one PaneSessionName would answer with (see
	// buildPanePlacements). SessionName is empty; ask PaneSessionName for it.
	Ambiguous bool
}

// listPanePlacementsFormat separates its fields with a space, not a TAB (tmux
// without a UTF-8 client locale rewrites a TAB in -F output to "_", alpha.340),
// and puts the session name last: a name may hold spaces, the other three
// fields cannot, so the row is split into at most four parts.
const listPanePlacementsFormat = "#{pane_id} #{pane_pid} #{session_activity} #{session_name}"

// paneRow is one row of that listing. A pane linked into several sessions
// (link-window) has one row per session.
type paneRow struct {
	PaneID      string
	PID         string
	Activity    int64 // session_activity, unix seconds
	SessionName string
}

// parsePaneRows parses list-panes output in listPanePlacementsFormat. Blank
// lines are skipped; any other line that is not "%N pid activity name" fails
// the whole listing, like parsePaneLocations: a row skipped would leave its
// pane out of the answer, and the caller would read that as "no such pane".
func parsePaneRows(out []byte) ([]paneRow, error) {
	var rows []paneRow
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, " ", 4)
		if len(fields) != 4 || !tmuxPaneIDRe.MatchString(fields[0]) || !panePIDRe.MatchString(fields[1]) {
			return nil, fmt.Errorf(`malformed row %s, want "%%N pid activity name"`, quoteRow(line))
		}
		activity, err := strconv.ParseInt(fields[2], 10, 64)
		name := strings.TrimSpace(fields[3])
		if err != nil || name == "" {
			return nil, fmt.Errorf(`malformed row %s, want "%%N pid activity name"`, quoteRow(line))
		}
		rows = append(rows, paneRow{PaneID: fields[0], PID: fields[1], Activity: activity, SessionName: name})
	}
	return rows, nil
}

// buildPanePlacements folds the rows into one placement per pane.
//
// A pane linked into several sessions is listed once per session, while
// `display-message -t %N '#{session_name}'` (PaneSessionName) answers with ONE
// of them: tmux's cmd_find_best_session takes the session with the newest
// activity time, and the first one it meets on a tie. Checked on tmux 3.6a
// against an isolated server: creating the sessions apart, attaching a client
// to one of them later, and sessions named so that the alphabetical order
// disagrees with the creation order all moved the answer exactly as
// session_activity predicts, and the row order of the listing predicted
// nothing. So:
//
//   - one session name across the pane's rows: that name;
//   - several, one of them with the newest session_activity: that one;
//   - several tied on the newest session_activity: Ambiguous. tmux compares
//     microseconds and the format prints seconds, so the listing cannot
//     decide; the caller asks PaneSessionName about that pane, which is
//     exactly what it did before the listing existed.
func buildPanePlacements(rows []paneRow) map[string]PanePlacement {
	type best struct {
		pid       string
		name      string
		activity  int64
		ambiguous bool
	}
	byPane := make(map[string]*best, len(rows))
	for _, r := range rows {
		b, ok := byPane[r.PaneID]
		if !ok {
			byPane[r.PaneID] = &best{pid: r.PID, name: r.SessionName, activity: r.Activity}
			continue
		}
		switch {
		case r.Activity > b.activity:
			b.name, b.activity, b.ambiguous = r.SessionName, r.Activity, false
		case r.Activity == b.activity && r.SessionName != b.name:
			b.ambiguous = true
		}
	}
	out := make(map[string]PanePlacement, len(byPane))
	for id, b := range byPane {
		if b.ambiguous {
			out[id] = PanePlacement{PID: b.pid, Ambiguous: true}
			continue
		}
		out[id] = PanePlacement{PID: b.pid, SessionName: b.name}
	}
	return out
}

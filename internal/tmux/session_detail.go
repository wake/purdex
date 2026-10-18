package tmux

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// SessionDetail is one session as a single list-sessions answer gives it (#3316): the session, the fields of its active pane that a
// session list shows, and the tmux server's instance ("<pid>:<start_time>", the string config.GetTmuxInstance reads with a
// display-message of its own).
type SessionDetail struct {
	TmuxSession
	Pane     TmuxPaneMetadata
	Instance string
}

// DetailLister is the optional capability of an Executor that can read every session, its active pane and the server instance in
// ONE tmux invocation. Before it a session list cost 2 + N forks (list-sessions, the instance, and a display-message per session);
// with it, one. ok is false when the answer cannot be trusted (see parseSessionDetails): the caller reads the old way, which is
// slower and always right. The same "no server / no sessions is an empty list" rule as ListSessions.
type DetailLister interface {
	ListSessionsDetailed(ctx context.Context) (details []SessionDetail, ok bool, err error)
}

// detailSep separates the fields of the detailed answer. It is printable ASCII on purpose: tmux sanitises a -F answer by the
// locale of its client, and where there is no UTF-8 locale (LANG, LC_ALL and LC_CTYPE all unset: a daemon started from the Finder)
// it turns a TAB, and any other control character, into "_" — which would leave one field per line (the failure of 2026-09-14).
// Printable ASCII passes through every locale (the non-ASCII text of the fields is kept by -u, see ListSessionsDetailed). It cannot be told apart from text in a free-text field, so the parser checks the
// field count, that the ids sit in their slots, and that no session appears twice, and refuses the whole answer otherwise.
const detailSep = "|~|"

// detailFormat: session_id, session_created, instance, window_id, pane_id, session_name, pane_title, window_name,
// pane_current_command, and last session_path (a raw path: the last field absorbs the rest of the line, as in ListSessions).
const detailFormat = "#{session_id}" + detailSep + "#{session_created}" + detailSep + "#{pid}:#{start_time}" + detailSep +
	"#{window_id}" + detailSep + "#{pane_id}" + detailSep + "#{session_name}" + detailSep + "#{pane_title}" + detailSep +
	"#{window_name}" + detailSep + "#{pane_current_command}" + detailSep + "#{session_path}"

const detailFieldCount = 10

var tmuxInstanceRe = regexp.MustCompile(`^[0-9]+:[0-9]+$`)

// ListSessionsDetailed runs list-sessions with -u: without it tmux sanitises the answer by the locale of its client, and where there is
// no UTF-8 locale it rewrites every non-ASCII character of a name, title or path to "_" (a session called 工作 would be listed as "__"
// and could no longer be addressed). -u makes tmux write UTF-8 whatever the locale, and the printable separator survives either way.
func (r *RealExecutor) ListSessionsDetailed(ctx context.Context) ([]SessionDetail, bool, error) {
	out, err := boundedRead(ctx, "tmux.read.ListSessionsDetailed", "-u", "list-sessions", "-F", detailFormat).Output()
	if err != nil {
		if cerr := readCtxErr(ctx, "tmux list-sessions", err); cerr != nil {
			return nil, false, cerr
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if IsNoServer(stderr) || strings.Contains(stderr, "no sessions") {
				return nil, true, nil
			}
		}
		return nil, false, fmt.Errorf("tmux list-sessions: %w", err)
	}
	details, ok := parseSessionDetails(string(out))
	return details, ok, nil
}

// parseSessionDetails parses the detailed list-sessions answer. ok is false — and the caller reads the old way — when any line does
// not have exactly the expected fields with every id in its slot (a locale that rewrote the separators, a free-text field that
// held the separator, a name or title with a newline in it), or when a session id comes twice. Never a half-parsed list.
func parseSessionDetails(out string) ([]SessionDetail, bool) {
	if strings.TrimSpace(out) == "" {
		return nil, true
	}
	var details []SessionDetail
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		parts := strings.SplitN(line, detailSep, detailFieldCount)
		if len(parts) != detailFieldCount ||
			strings.Contains(parts[detailFieldCount-1], detailSep) || // an earlier field held the separator and pushed the rest into the path
			!tmuxSessionIDRe.MatchString(parts[0]) ||
			!tmuxInstanceRe.MatchString(parts[2]) ||
			!tmuxWindowIDRe.MatchString(parts[3]) ||
			!tmuxPaneIDRe.MatchString(parts[4]) ||
			seen[parts[0]] {
			log.Printf("tmux list-sessions: the detailed answer cannot be trusted (e.g. %q); reading the sessions one by one", truncate(line, 120))
			return nil, false
		}
		seen[parts[0]] = true
		created, _ := strconv.ParseInt(parts[1], 10, 64) // unreadable = 0 = unknown
		details = append(details, SessionDetail{
			TmuxSession: TmuxSession{ID: parts[0], Name: parts[5], Created: created, Cwd: parts[9]},
			Pane: TmuxPaneMetadata{
				SessionID:          parts[0],
				SessionName:        parts[5],
				WindowID:           parts[3],
				PaneID:             parts[4],
				PaneTitle:          sanitizeTmuxMetadata(parts[6]),
				WindowName:         sanitizeTmuxMetadata(parts[7]),
				PaneCurrentCommand: sanitizeTmuxMetadata(parts[8]),
			},
			Instance: parts[2],
		})
	}
	return details, true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

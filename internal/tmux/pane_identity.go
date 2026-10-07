// internal/tmux/pane_identity.go — a session that carries a tag from birth,
// and one read that answers who a pane belongs to.
//
// A caller that creates a session and must later prove it is still the one
// it created (lead-team spawn ownership, P4-5 review H3/H4) cannot rely on
// the name, which anyone may reuse, nor on ids, which a restarted server
// mints again. It tags the session with a user option in the create itself,
// and reads back the server's generation, the pane's session and pane ids,
// the tag and the pane's directory in ONE invocation: one server's answer.
package tmux

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// PaneIdentity is one tmux invocation's answer about a pane: the server's
// generation ("<pid>:<start_time>", as GetTmuxInstance reads it), the
// pane's session id ("$N") and pane id ("%N"), the value of one session
// user option ("" when unset) and the pane's current directory.
type PaneIdentity struct {
	Instance, SessionID, PaneID, Tag, Cwd string
}

// A user option name is interpolated into a format, so it is `@` and word
// characters only.
var userOptionPattern = regexp.MustCompile(`^@[A-Za-z0-9_-]+$`)

var paneIDPattern = regexp.MustCompile(`^%[0-9]+$`)

func paneIdentityArgs(target, option string) ([]string, error) {
	if !userOptionPattern.MatchString(option) {
		return nil, fmt.Errorf("tmux: %q is not a user option name", option)
	}
	return []string{"display-message", "-p", "-t", target,
		"#{pid}:#{start_time} #{session_id} #{pane_id} [#{" + option + "}] #{pane_current_path}"}, nil
}

// parsePaneIdentity reads paneIdentityArgs' line. A target tmux cannot find
// prints an empty line with exit 0 (measured, tmux 3.6a), so anything short
// of every field, well formed, is an error. The directory is last: it may
// hold spaces.
func parsePaneIdentity(out string) (PaneIdentity, error) {
	f := strings.SplitN(strings.TrimRight(out, "\n"), " ", 5)
	if len(f) != 5 || !instancePattern.MatchString(f[0]) || !sessionIDPattern.MatchString(f[1]) ||
		!paneIDPattern.MatchString(f[2]) || len(f[3]) < 2 || f[3][0] != '[' || f[3][len(f[3])-1] != ']' {
		return PaneIdentity{}, fmt.Errorf("tmux: unreadable pane identity %q", out)
	}
	return PaneIdentity{Instance: f[0], SessionID: f[1], PaneID: f[2], Tag: f[3][1 : len(f[3])-1], Cwd: f[4]}, nil
}

// newSessionTaggedArgs is new-session followed, in the same command list,
// by set-option of the session user option: without -t it applies to the
// session just created and to no other. new-session prints (-P) the id and
// the server generation of the session it made, also when the set-option
// after it fails (exit 1); when new-session itself fails it prints nothing
// (all measured, tmux 3.6a). The value must be inert in a tmux command (the
// generation's character set).
func newSessionTaggedArgs(name, cwd, option, value string) ([]string, error) {
	if !userOptionPattern.MatchString(option) || !instancePattern.MatchString(value) {
		return nil, fmt.Errorf("tmux: option %q = %q cannot tag a session", option, value)
	}
	return []string{"new-session", "-d", "-s", name, "-c", cwd, "-P", "-F", "#{session_id} #{pid}:#{start_time}",
		";", "set-option", option, value}, nil
}

// parseCreatedSession reads new-session's -P line; "" for anything else.
func parseCreatedSession(out string) (sessionID, instance string) {
	f := strings.Fields(out)
	if len(f) != 2 || !sessionIDPattern.MatchString(f[0]) || !instancePattern.MatchString(f[1]) {
		return "", ""
	}
	return f[0], f[1]
}

// NewSessionTaggedContext is NewSessionContext that also sets the new
// session's user option to value, in the same tmux invocation. It returns
// the id and generation of the session its new-session made, as that
// command printed them, even with an error: a set-option that failed after
// it leaves that session behind, and only the caller can remove it by id.
func (r *RealExecutor) NewSessionTaggedContext(ctx context.Context, name, cwd, option, value string) (string, string, error) {
	args, err := newSessionTaggedArgs(name, cwd, option, value)
	if err != nil {
		return "", "", err
	}
	out, err := boundedRead(ctx, args...).Output()
	id, inst := parseCreatedSession(string(out))
	if err != nil {
		if cerr := readCtxErr(ctx, "tmux new-session", err); cerr != nil {
			return id, inst, cerr
		}
		return id, inst, err
	}
	return id, inst, nil
}

// PaneIdentity reads target's identity and the session user option in one
// tmux invocation; target is a pane id or a session target such as "=name:".
func (r *RealExecutor) PaneIdentity(ctx context.Context, target, option string) (PaneIdentity, error) {
	args, err := paneIdentityArgs(target, option)
	if err != nil {
		return PaneIdentity{}, err
	}
	out, err := boundedRead(ctx, args...).Output()
	if err != nil {
		if cerr := readCtxErr(ctx, "tmux display-message", err); cerr != nil {
			return PaneIdentity{}, cerr
		}
		return PaneIdentity{}, fmt.Errorf("tmux display-message: %w", err)
	}
	return parsePaneIdentity(string(out))
}

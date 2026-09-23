package session

// CreateSession is the create path of POST /api/sessions as a Go call
// (exec-to-terminal spec §4.1 step 6): the nex module makes a fresh tmux
// session for an execution it takes to a terminal, through the same name
// rule, cwd resolution and HasSession→NewSession→ListSessions→SetMeta
// critical section handleCreate runs. The typed error says which stage
// failed and — what the caller needs most — whether a tmux session was
// left behind (SessionAlive), so it can report or kill it.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/wake/purdex/internal/store"
)

// CreateStage names the step of CreateSession that failed.
type CreateStage string

const (
	CreateStageInvalidName CreateStage = "invalid_name" // name fails ValidSessionName
	CreateStageInvalidCwd  CreateStage = "invalid_cwd"  // resolveCwd refused the directory
	CreateStageExists      CreateStage = "exists"       // HasSession(name) was already true
	CreateStageNewSession  CreateStage = "new_session"  // tmux new-session failed; nothing exists
	CreateStageList        CreateStage = "list"         // tmux list-sessions failed, or the new session was not in it
	// The tmux generation read after list-sessions differs from the one read
	// before new-session: the server the session was created on has been
	// replaced, and the session died with it. Nothing of ours exists.
	CreateStageGenerationChanged CreateStage = "generation_changed"
	CreateStageEncode            CreateStage = "encode" // the new session's id could not be encoded to a code
	CreateStageMeta              CreateStage = "meta"   // SetMeta failed
)

// Sentinels for errors.Is on a *CreateError; the stages after new_session
// have no sentinel because the caller acts on SessionAlive, not on which
// follow-up step failed.
var (
	ErrInvalidSessionName = errors.New("invalid session name: must match ^[a-zA-Z0-9_-]+$")
	ErrInvalidCwd         = errors.New("invalid cwd")
	ErrSessionExists      = errors.New("session already exists")
)

// CreateError is what CreateSession returns on every failure. Err is the
// underlying cause (the sentinel for the first three stages, the tmux /
// store / codec error for the rest) and is what Unwrap exposes.
type CreateError struct {
	Stage CreateStage
	Name  string
	Err   error
}

func (e *CreateError) Error() string {
	return "creating session " + e.Name + " (" + string(e.Stage) + "): " + e.Err.Error()
}

func (e *CreateError) Unwrap() error { return e.Err }

// Is matches the stage's sentinel, so errors.Is(err, ErrInvalidCwd) holds
// while Err keeps the underlying reason (the text the HTTP handler shows).
func (e *CreateError) Is(target error) bool {
	switch e.Stage {
	case CreateStageInvalidName:
		return target == ErrInvalidSessionName
	case CreateStageInvalidCwd:
		return target == ErrInvalidCwd
	case CreateStageExists:
		return target == ErrSessionExists
	}
	return false
}

// SessionAlive reports whether `tmux new-session` succeeded before the
// failure AND the session is still there: it exists but has no meta row
// and no code was returned. A caller that wanted an atomic create decides
// what to do with it. generation_changed is false: new-session succeeded,
// but on a server that has since been replaced.
func (e *CreateError) SessionAlive() bool {
	switch e.Stage {
	case CreateStageList, CreateStageEncode, CreateStageMeta:
		return true
	}
	return false
}

// ValidSessionName reports whether name is one POST /api/sessions accepts.
func ValidSessionName(name string) bool {
	return name != "" && nameRegex.MatchString(name)
}

// SessionExists reports whether a tmux session of that name is live.
func (m *SessionModule) SessionExists(name string) bool {
	return m.tmux.HasSession(name)
}

// ValidateCwd reports whether cwd would be accepted by CreateSession: the
// same resolution (trim, `~`, absolute, exists, is a directory) that runs
// there, without creating anything. The error wraps ErrInvalidCwd.
func (m *SessionModule) ValidateCwd(cwd string) error {
	if _, err := resolveCwd(cwd, os.UserHomeDir); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidCwd, err)
	}
	return nil
}

// CreateSession validates name and cwd, then creates the tmux session in
// cwd and records its meta (mode `terminal`), returning the same
// SessionInfo POST /api/sessions answers with. On failure the error is a
// *CreateError; see SessionAlive for what may have been left behind.
func (m *SessionModule) CreateSession(name, cwd string) (*SessionInfo, error) {
	fail := func(stage CreateStage, err error) (*SessionInfo, error) {
		return nil, &CreateError{Stage: stage, Name: name, Err: err}
	}

	if !ValidSessionName(name) {
		return fail(CreateStageInvalidName, ErrInvalidSessionName)
	}

	// tmux neither expands ~ nor fails on an unusable -c: it silently starts
	// the session in $HOME. Resolve here so the pane really lands where the
	// caller asked, and so the recorded cwd matches the pane's directory.
	resolved, err := resolveCwd(cwd, os.UserHomeDir)
	if err != nil {
		return fail(CreateStageInvalidCwd, err)
	}
	cwd = resolved

	// Serialize the HasSession→NewSession→SetMeta critical section so two
	// concurrent creates with the same name can't both slip past the
	// duplicate check. Input validation stays outside the lock.
	m.createMu.Lock()
	defer m.createMu.Unlock()

	if m.tmux.HasSession(name) {
		return fail(CreateStageExists, ErrSessionExists)
	}

	// The generation is sampled on both sides of new-session + list-sessions
	// and must agree: the caller gets a session id and the generation it is
	// valid in, and a server that restarted in between would hand it an id
	// minted by the old server with a stamp read from the new one — a pair
	// that names a stranger's session on the new server. An empty sample
	// before the create is "no server running": new-session starts one, and
	// the sample read after it is that server's, which is the one the
	// session lives on.
	before := m.TmuxInstance()

	if err := m.tmux.NewSession(name, cwd); err != nil {
		return fail(CreateStageNewSession, err)
	}

	// Find the newly created session to get its tmux ID. The read is bounded
	// like every session-list read (#1293): a hung tmux must not hold the
	// create critical section (createMu) forever.
	listCtx, cancel := context.WithTimeout(context.Background(), listReadTimeout)
	sessions, err := m.tmux.ListSessions(listCtx)
	cancel()
	if err != nil {
		return fail(CreateStageList, err)
	}

	after := m.TmuxInstance()
	if before != "" && after != before {
		return fail(CreateStageGenerationChanged, fmt.Errorf("tmux server restarted during create (generation %s → %s)", before, after))
	}
	// The stamp is the pre-create sample; only when there was no server to
	// sample is it the one new-session started.
	instance := before
	if before == "" {
		instance = after
	}

	for _, s := range sessions {
		if s.Name != name {
			continue
		}
		code, err := EncodeSessionID(s.ID)
		if err != nil {
			return fail(CreateStageEncode, err)
		}

		// `s.Cwd` is tmux's own `#{session_path}` — the directory the
		// session is actually in — so it, not the request, is what gets
		// recorded. resolveCwd stat'd the directory a moment ago, but tmux
		// ran after that: if it vanished in between, tmux silently started
		// the session in $HOME, and recording the request would have the
		// daemon report a directory the session is not in. This also puts
		// create on the same footing as list/get, which already source Cwd
		// from tmux.
		//
		// A mismatch is logged, not fatal. Killing the session on mismatch
		// was considered and rejected: `session_path` comes from getcwd(),
		// which canonicalises symlinks and filesystem case (/tmp →
		// /private/tmp on macOS), so a string comparison produces false
		// mismatches — and killing a live session on a false positive is
		// far worse than the rare race it would guard.
		//
		// That same canonicalisation is why the warning is gated on
		// sameDirectory (os.SameFile) rather than on the string compare
		// alone: every create through a symlinked path or a /tmp request
		// comes back spelled differently while being the very same
		// directory, and warning on those would bury the one case the
		// warning is for — the requested directory vanished and tmux
		// silently fell back to $HOME.
		if s.Cwd != cwd && !sameDirectory(s.Cwd, cwd) {
			log.Printf("session: tmux did not honour the requested directory for %q: requested %q, session is in %q", name, cwd, s.Cwd)
		}

		if err := m.meta.SetMeta(s.ID, store.SessionMeta{
			TmuxID: s.ID,
			Mode:   "terminal",
			Cwd:    s.Cwd,
		}); err != nil {
			return fail(CreateStageMeta, err)
		}

		m.invalidateNameCache()
		m.invalidateListCache()

		return &SessionInfo{
			Code:   code,
			TmuxID: s.ID,
			Name:   s.Name,
			Exists: true,
			Mode:   "terminal",
			Cwd:    s.Cwd,
			// Built by hand rather than via ListSessions, so it needs its
			// own stamp. The rebuild engine re-points a pane using the
			// generation carried here (spec §4.8 step 4); leaving it empty
			// would give the rebuilt pane an unknown generation until the
			// next sessions broadcast. The value is the pre-create sample
			// (checked equal to the post-list one above), not a fresh read.
			TmuxInstance: instance,
		}, nil
	}

	return fail(CreateStageList, errors.New("session created but not found"))
}

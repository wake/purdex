package conversation

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/promptq"
)

// The services and timings of POST /api/sessions/{code}/submit (K-1a-2, spec docs/specs/2026-10-11-keystroke-submit-spec.md §3).

// TypedQueue is the part of promptq.Queue the route drives.
type TypedQueue interface {
	Lookup(clientMsgID string) (promptq.Result, bool)
	SubmitFill(ctx context.Context, sessionID, clientMsgID, text string) (promptq.Result, error)
	ClearFilled(ctx context.Context, clientMsgID string) (promptq.Result, error)
	ClearIf(ctx context.Context, sessionID, text string) (promptq.Result, error)
	MarkEntering(id string) error
	MarkEntered(id string) error
	MarkEnterRefused(id string) error
	MarkUnknown(id string) error
	MarkGaveUp(id string) error
	Read(ctx context.Context, sessionID string) (promptq.Result, error)
	HasOwner(sessionID string) bool
	NoOwnerFor(sessionID string) time.Duration
}

// SessionLookup finds a tmux session by code (session.SessionProvider).
type SessionLookup interface {
	GetSession(code string) (*session.SessionInfo, error)
}

// PaneKeySender is the guarded Enter / typing (tmux.RealExecutor).
type PaneKeySender interface {
	SendKeysIfInstanceAndMode(sessionID, expectedInstance, paneID string, keys ...string) error
}

type typedDeps struct {
	q        TypedQueue
	sessions SessionLookup
	owners   agent.OwnerResolver
	panes    OwnerSource // every confirmed live pane of a session id: the pane-identity check
	keys     PaneKeySender
}

// typedTuning: zero values are the spec's; tests shorten them.
type typedTuning struct {
	lockWait, poll, finaliseFor, keysAfter time.Duration
	recallSettle                           time.Duration // recall: how long the TUI gets to draw what Up popped before the read repeats
	now                                    func() time.Time
}

const (
	enterFreshness = 1500 * time.Millisecond // C1: the fill result may be at most this old when Enter goes out
	cleanupTimeout = promptq.DefaultWait + 2*time.Second
)

func (m *Module) tuning() typedTuning {
	t := m.tune
	if t.lockWait <= 0 {
		t.lockWait = 2 * time.Second
	}
	if t.poll <= 0 {
		t.poll = 25 * time.Millisecond
	}
	if t.finaliseFor <= 0 {
		t.finaliseFor = promptq.DefaultHandTimeout + 5*time.Second
	}
	if t.keysAfter <= 0 {
		t.keysAfter = 10 * time.Second
	}
	if t.recallSettle <= 0 {
		t.recallSettle = 150 * time.Millisecond
	}
	if t.now == nil {
		t.now = time.Now
	}
	return t
}

// typedDeps: the injected set (tests), else the daemon's services.
func (m *Module) typedDeps() *typedDeps {
	if m.td != nil {
		return m.td
	}
	if m.core == nil || m.core.Registry == nil {
		return nil
	}
	d := &typedDeps{}
	var ok bool
	if svc, found := m.core.Registry.Get(promptq.Key); found {
		d.q, ok = svc.(TypedQueue)
	}
	if !ok {
		return nil
	}
	svc, found := m.core.Registry.Get(session.RegistryKey)
	if !found {
		return nil
	}
	if d.sessions, ok = svc.(SessionLookup); !ok {
		return nil
	}
	if svc, found = m.core.Registry.Get(agent.OwnerResolverKey); !found {
		return nil
	}
	if d.owners, ok = svc.(agent.OwnerResolver); !ok {
		return nil
	}
	if svc, found = m.core.Registry.Get(agentKey); !found {
		return nil
	}
	if d.panes, ok = svc.(OwnerSource); !ok {
		return nil
	}
	if d.keys, ok = m.core.Tmux.(PaneKeySender); !ok {
		return nil
	}
	return d
}

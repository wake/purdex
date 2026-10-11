package agent

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/session"
)

// NA-0b (no-agent launch panel spec §2.3): a root SessionStart tells the session module the launch it was waiting for worked, so
// the claim on the session is released; nothing else does.

type releasingProvider struct {
	session.SessionProvider
	mu    sync.Mutex
	names []string
	done  chan struct{} // closed by the first release (the call is made off the hook path)
}

func (p *releasingProvider) ReleaseLaunchClaim(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.names = append(p.names, name)
	close(p.done)
}

func (p *releasingProvider) released() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.names...)
}

func withReleaser(m *Module) *releasingProvider {
	p := &releasingProvider{SessionProvider: m.sessions, done: make(chan struct{})}
	m.sessions = p
	return p
}

func TestLaunchRelease_RootSessionStartReleasesTheSessionsClaim(t *testing.T) {
	m := newSessionStartTestModule(t)
	p := withReleaser(m)
	if rec := postRootSessionStart(t, m, `{"session_id":"S","cwd":"/w","source":"startup"}`); rec.Code != http.StatusOK {
		t.Fatalf("hook answered %d", rec.Code)
	}
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the release never came")
	}
	if got := p.released(); len(got) != 1 || got[0] != "work" {
		t.Fatalf("released = %v, want [work]", got)
	}
}

// A child's SessionStart (a subagent of a running agent) is not the launch's agent starting; neither is any other event.
func TestLaunchRelease_ChildSessionStartAndOtherEventsReleaseNothing(t *testing.T) {
	m := newSessionStartTestModule(t)
	p := withReleaser(m)
	if rec := postChildSessionStart(t, m, `{"session_id":"C","cwd":"/w","source":"startup"}`); rec.Code != http.StatusOK {
		t.Fatalf("hook answered %d", rec.Code)
	}
	time.Sleep(100 * time.Millisecond) // the call, if any, is asynchronous
	if got := p.released(); len(got) != 0 {
		t.Fatalf("a child SessionStart released %v", got)
	}

	m2 := newSessionStartTestModule(t)
	p2 := withReleaser(m2)
	if rec := postRootPrompt(t, m2, `{"session_id":"S"}`); rec.Code != http.StatusOK {
		t.Fatalf("hook answered %d", rec.Code)
	}
	time.Sleep(100 * time.Millisecond)
	if got := p2.released(); len(got) != 0 {
		t.Fatalf("a prompt released %v", got)
	}
}

// The registry key the session module asks is the agent module's own (one constant), and its spelling is pinned.
func TestLaunchRelease_AgentRunningKeyIsPinned(t *testing.T) {
	if AgentRunningKey != "agent.running" {
		t.Fatalf("AgentRunningKey = %q", AgentRunningKey)
	}
}

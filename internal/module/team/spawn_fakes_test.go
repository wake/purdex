package teammod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/hostconfig"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// spawnFakes are the spawn runner's seams in every fixture (Init needs them).
type spawnFakes struct {
	tmux     *tmux.FakeExecutor
	sessions *fakeSessions
	frames   *fakeFrames
	teamCfg  *fakeTeamCfg
	so       *spawnOrigins // newSpawnFixture only
}

func (f *fixture) registerSpawnFakes() {
	f.tmux = tmux.NewFakeExecutor()
	f.tmux.SetInstance("4242:1700000000")
	f.core.Tmux = f.tmux
	f.sessions = &fakeSessions{tm: f.tmux}
	f.frames = &fakeFrames{}
	f.teamCfg = &fakeTeamCfg{s: hostconfig.DefaultTeamSettings}
	f.core.Registry.Register(session.RegistryKey, f.sessions)
	f.core.Registry.Register(agent.TerminalSessionsKey, f.frames)
	f.core.Registry.Register(hostconfig.TeamSettingsKey, f.teamCfg)
}

// fakeSessions is the session module's create path over the fixture's
// FakeExecutor: session $N gets the active pane %N.
type fakeSessions struct {
	tm        *tmux.FakeExecutor
	mu        sync.Mutex
	creates   int
	createErr error
}

func (s *fakeSessions) SessionExists(name string) bool { return s.tm.HasSession(name) }
func (s *fakeSessions) TmuxInstance() string           { return s.tm.Instance() }

func (s *fakeSessions) ValidateCwd(cwd string) error {
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return fmt.Errorf("%w: %s", session.ErrInvalidCwd, cwd)
	}
	return nil
}

func (s *fakeSessions) CreateSession(name, cwd string) (*session.SessionInfo, error) {
	s.mu.Lock()
	s.creates++
	err := s.createErr
	s.mu.Unlock()
	if err == nil && s.tm.HasSession(name) {
		err = session.ErrSessionExists
	}
	if err != nil {
		return nil, err
	}
	_ = s.tm.NewSession(name, cwd)
	list, _ := s.tm.ListSessions(context.Background())
	id := list[len(list)-1].ID
	s.tm.SetActivePaneMetadata(name, tmux.TmuxPaneMetadata{SessionID: id, SessionName: name, PaneID: "%" + id[1:]})
	return &session.SessionInfo{Name: name, TmuxID: id, TmuxInstance: s.tm.Instance(), Cwd: cwd}, nil
}

func (s *fakeSessions) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creates
}

// fakeFrames is the agent module's live frames.
type fakeFrames struct {
	mu   sync.Mutex
	list []agent.TerminalSession
}

func (f *fakeFrames) LiveSessions(context.Context, string) ([]agent.TerminalSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.TerminalSession(nil), f.list...), nil
}

type fakeTeamCfg struct {
	s   hostconfig.TeamSettings
	err error
}

func (c *fakeTeamCfg) TeamSettings() (hostconfig.TeamSettings, error) { return c.s, c.err }

func (f *fakeTitles) Claim(sid, label string, _ time.Time) (store.PeerLabel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = append(f.claims, [2]string{sid, label})
	return store.PeerLabel{SessionID: sid, Label: label}, nil
}

// spawnOrigins is the fixture's resolver plus addresses and the member
// sessions that registered (register).
type spawnOrigins struct {
	*fakeOrigins
	mu      sync.Mutex
	members map[string]team.Origin
}

func (s *spawnOrigins) ResolveOrigin(inbox string) (team.Origin, bool, error) {
	o, ok, err := s.fakeOrigins.ResolveOrigin(inbox)
	o.Address = "mlab/" + o.Name
	return o, ok, err
}

func (s *spawnOrigins) ResolveOriginBySession(sid string) (team.Origin, bool, error) {
	s.mu.Lock()
	o, ok := s.members[sid]
	s.mu.Unlock()
	if ok {
		return o, true, nil
	}
	return s.fakeOrigins.ResolveOriginBySession(sid)
}

// spawnID is the i-th spawn op id: a UUID v4 whose tmux name tm-<i:08x>00
// differs from every other i's.
func spawnID(i int) string { return fmt.Sprintf("%08x-00aa-4bbb-8ccc-000000000000", i) }

// newSpawnFixture is a fixture whose session sid-1 (/tmp/10.sock) leads team
// uid(1) with one granted root (a temp dir, symlinks evaluated) and the
// given limit; the registration poll advances the clock instead of sleeping.
func newSpawnFixture(t *testing.T, maxMembers int) (*fixture, string) {
	f := newFixture(t)
	f.so = &spawnOrigins{fakeOrigins: f.origins, members: map[string]team.Origin{}}
	f.core.Registry.Register(peersmod.OriginResolverKey, f.so) // a second Module's Init takes it too
	f.m.origins = f.so
	f.m.spawnSleep = f.fastSleep
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := team.Grant{MaxMembers: maxMembers, Roots: []string{root}}
	if _, _, _, err := f.m.store.Create(openApproval(uid(1), "sid-1", 1), "seed"); err != nil {
		t.Fatal(err)
	}
	if _, won, err := f.m.store.CloseLeadApproved(uid(1), approveClose(1, g), leadTeam(uid(1), "sid-1", "_abc123", g, 1)); err != nil || !won {
		t.Fatalf("seed team: won=%v err=%v", won, err)
	}
	return f, root
}

func (f *fixture) fastSleep(context.Context, time.Duration) {
	f.clock.Add(250)
	time.Sleep(time.Millisecond)
}

// register makes sid's Claude Code come up on pane: a verified frame there
// and a live registry entry.
func (f *fixture) register(pane, sid string) {
	f.frames.mu.Lock()
	f.frames.list = append(f.frames.list, agent.TerminalSession{PaneID: pane, SessionID: sid, AgentType: "cc", Verified: true})
	f.frames.mu.Unlock()
	f.so.mu.Lock()
	f.so.members[sid] = team.Origin{SessionID: sid, Ref: ipeers.RefID(sid), PID: 31, ProcStart: "p31", Address: "mlab/" + ipeers.RefID(sid)}
	f.so.mu.Unlock()
}

// spawn posts a spawn of id from sid-1 into cwd; edit changes the body.
func (f *fixture) spawn(id, cwd string, edit func(*team.SpawnRequest)) (int, team.SpawnOp, team.APIError) {
	f.t.Helper()
	req := team.SpawnRequest{ID: id, OriginInbox: "/tmp/10.sock", Cwd: cwd}
	if edit != nil {
		edit(&req)
	}
	code, body := f.do(http.MethodPost, "/api/team/spawns", req)
	var op team.SpawnOp
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &op); err != nil {
			f.t.Fatalf("decode spawn op: %v; %s", err, body)
		}
		return code, op, team.APIError{}
	}
	return code, op, decodeErr(f.t, body)
}

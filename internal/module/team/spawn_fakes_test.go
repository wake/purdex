package teammod

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// spawnFakes are the spawn runner's seams in every fixture (Init needs them).
type spawnFakes struct {
	tmux     *tmux.FakeExecutor
	sessions *fakeSessions
	teamCfg  *fakeTeamCfg
}

func (f *fixture) registerSpawnFakes() {
	f.tmux = tmux.NewFakeExecutor()
	f.tmux.SetInstance("4242:1700000000")
	f.core.Tmux = f.tmux
	f.sessions = &fakeSessions{tm: f.tmux}
	f.teamCfg = &fakeTeamCfg{s: hostconfig.DefaultTeamSettings}
	f.core.Registry.Register(session.RegistryKey, f.sessions)
	f.core.Registry.Register(hostconfig.TeamSettingsKey, f.teamCfg)
}

// fakeSessions is the session module's tagged create over the fixture's
// FakeExecutor: session $N gets the active pane %N, whose directory is the
// cwd as the kernel resolves it at the create (symlinks followed).
// beforeCreate, when set, runs first (a test swaps the directory there).
type fakeSessions struct {
	tm           *tmux.FakeExecutor
	mu           sync.Mutex
	creates      int
	createErr    error
	beforeCreate func()
}

func (s *fakeSessions) SessionExists(name string) bool { return s.tm.HasSession(name) }

func (s *fakeSessions) ValidateCwd(cwd string) error {
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return fmt.Errorf("%w: %s", session.ErrInvalidCwd, cwd)
	}
	return nil
}

func (s *fakeSessions) CreateSessionTagged(name, cwd string, tag session.SessionTag) (*session.SessionInfo, error) {
	s.mu.Lock()
	s.creates++
	err, before := s.createErr, s.beforeCreate
	s.mu.Unlock()
	if before != nil {
		before()
	}
	if err == nil && s.tm.HasSession(name) {
		err = session.ErrSessionExists
	}
	if err != nil {
		return nil, err
	}
	id, _, _ := s.tm.NewSessionTaggedContext(context.Background(), name, cwd, tag.Option, tag.Value)
	s.tm.SetActivePaneMetadata(name, tmux.TmuxPaneMetadata{SessionID: id, SessionName: name, PaneID: "%" + id[1:]})
	resolved, _ := filepath.EvalSymlinks(cwd)
	s.tm.SetPaneCwd("%"+id[1:], resolved)
	return &session.SessionInfo{Name: name, TmuxID: id, TmuxInstance: s.tm.Instance(), Cwd: cwd}, nil
}

func (s *fakeSessions) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creates
}

type fakeTeamCfg struct {
	s   hostconfig.TeamSettings
	err error
}

func (c *fakeTeamCfg) TeamSettings() (hostconfig.TeamSettings, error) { return c.s, c.err }

// spawnID is the i-th spawn op id: a UUID v4 named tm-<i:08x>00.
func spawnID(i int) string { return fmt.Sprintf("%08x-00aa-4bbb-8ccc-000000000000", i) }

// newSpawnFixture is a fixture whose session sid-1 (/tmp/10.sock) leads team
// uid(1) with one granted root (a temp dir, symlinks evaluated) and the
// given limit.
func newSpawnFixture(t *testing.T, maxMembers int) (*fixture, string) {
	f := newFixture(t)
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

// acceptOp stores op i of team uid(1) from sid-1 into cwd as the POST would
// (edit changes it before the insert) and returns its id.
func (f *fixture) acceptOp(i int, cwd string, edit func(*spawnRow)) string {
	f.t.Helper()
	id := spawnID(i)
	name, _ := team.SpawnTmuxName(id)
	at := f.clock.Load()
	op := spawnRow{ID: id, TeamID: uid(1), HostID: "h:1", OriginSessionID: "sid-1", Cwd: cwd, TmuxName: name,
		Step: team.StepAccepted, State: team.SpawnRunning, CreatedAt: at, UpdatedAt: at}
	if edit != nil {
		edit(&op)
	}
	mustCreateSpawn(f.t, f.m.store, op)
	return id
}

// runOp accepts op i, runs it to where its runner stops and returns it.
func (f *fixture) runOp(i int, cwd string, edit func(*spawnRow)) spawnRow {
	f.t.Helper()
	id := f.acceptOp(i, cwd, edit)
	f.m.startSpawn(id)
	f.m.spawnWG.Wait()
	op, _, err := f.m.store.GetSpawnOp(id)
	if err != nil {
		f.t.Fatal(err)
	}
	return op
}

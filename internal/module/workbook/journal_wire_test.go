package workbook

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/journal"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// fakeOrigins is the peers module's origin resolver: the live sessions by id.
type fakeOrigins struct {
	mu   sync.Mutex
	live map[string]team.Origin
	err  error
}

func (f *fakeOrigins) ResolveOriginBySession(sid string) (team.Origin, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.live[sid]
	if f.err != nil {
		return o, ok, f.err // a caller must not trust the origin that comes with an error
	}
	return o, ok, nil
}

func (f *fakeOrigins) set(o team.Origin) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.live == nil {
		f.live = map[string]team.Origin{}
	}
	f.live[o.SessionID] = o
}

// wireKit is a started workbook module over a fake origin resolver and a fake team feed.
type wireKit struct {
	m       *Module
	c       *core.Core
	origins *fakeOrigins
	feed    *fakeFeed
}

// newWire makes a module through Init, with the journal switch as given (nil = the config says nothing), and starts it.
func newWire(t *testing.T, enabled *bool) *wireKit { return newWireFeed(t, enabled, nil, false) }

// newWireFeed is newWire with the team feed the module is given (nil: a plain fake) and, with start false, not started.
func newWireFeed(t *testing.T, enabled *bool, feed team.JournalFeed, noStart bool) *wireKit {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir(), HostID: "h1:abc"}
	if enabled != nil {
		cfg.Journal = &config.JournalConfig{Enabled: enabled}
	}
	c := core.New(core.CoreDeps{Config: cfg})
	k := &wireKit{c: c, origins: &fakeOrigins{}, feed: &fakeFeed{teams: map[string]team.TeamInfo{}}}
	if feed == nil {
		feed = k.feed
	}
	c.Registry.Register(peersmod.OriginResolverKey, k.origins)
	c.Registry.Register(team.JournalFeedKey, feed)
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()); m.Close() })
	k.m = m
	if !noStart {
		if err := m.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return k
}

func boolp(b bool) *bool { return &b }

// The peers module's resolver is what the wiring talks to: if it loses either method the journal would silently not wire (or
// read the registry once per team), so the build says so.
var (
	_ originSource = (*peersmod.OriginResolver)(nil)
	_ liveLister   = (*peersmod.OriginResolver)(nil)
)

func (k *wireKit) journalStatus() bool {
	v, _ := k.m.Status()["journal"].(bool)
	return v
}

// With the switch on (or absent) the journal is wired: the engine has a source, the loops exist, and the module says so.
func TestWire_OnByDefault(t *testing.T) {
	for name, enabled := range map[string]*bool{"absent": nil, "explicit": boolp(true)} {
		k := newWire(t, enabled)
		k.m.mu.Lock()
		run, eng := k.m.journalRun, k.m.engine
		k.m.mu.Unlock()
		if run == nil || run.drainer == nil || run.mirror == nil || run.git == nil {
			t.Fatalf("%s: the journal run is %+v, want a drainer, a task mirror and a git loop", name, run)
		}
		if eng == nil || eng.d.Journal == nil {
			t.Fatalf("%s: the engine has no journal source: the second model call would never be made", name)
		}
		if !k.journalStatus() {
			t.Errorf("%s: Status()[journal] = false, journal.v1 would not be announced", name)
		}
		if k.m.journalDisabled() {
			t.Errorf("%s: the API is switched off", name)
		}
	}
}

// With `enabled = false` nothing of the journal exists: no source for the engine (so a turn queues no second model call), no
// drainer, task mirror or git loop, no journal.v1, and the four routes answer 503.
// Mutation gate: ignore the switch in prepareJournal -> red.
func TestWire_OffStartsNothing(t *testing.T) {
	k := newWire(t, boolp(false))
	k.m.mu.Lock()
	run, eng := k.m.journalRun, k.m.engine
	k.m.mu.Unlock()
	if run != nil {
		t.Fatalf("the journal run exists with the journal off: %+v", run)
	}
	if eng == nil {
		t.Fatal("the engine itself must still run: the workbook has a life without the journal")
	}
	if eng.d.Journal != nil {
		t.Fatal("the engine has a journal source: every turn would queue the second model call")
	}
	if eng.d.Store != nil && eng.d.Store.journalOn.Load() {
		t.Fatal("the store writes outbox rows with the journal off")
	}
	// a lead's turn gets no journal job
	if j := eng.journalAfter(&job{entryID: 1}); j != nil {
		t.Fatalf("a journal job is queued with the journal off: %+v", j)
	}
	if k.journalStatus() {
		t.Error("Status()[journal] = true: journal.v1 would be announced")
	}
	if !k.m.journalDisabled() {
		t.Error("the API is not switched off")
	}
	if st := k.m.Status(); st["ready"] != true {
		t.Errorf("the workbook module must stay ready with the journal off: %v", st)
	}
	// no directory of the git loop is made either
	if _, err := os.Stat(filepath.Join(k.dataDir(), "journal")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the git loop's folder exists: %v", err)
	}
}

func (k *wireKit) dataDir() string {
	k.c.CfgMu.RLock()
	defer k.c.CfgMu.RUnlock()
	return k.c.Cfg.DataDir
}

// A daemon without the peers module's resolver cannot find a session's folder: the journal is left unwired (and says so),
// the workbook is not hurt.
func TestWire_WithoutTheOriginResolverTheJournalIsNotWired(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()); m.Close() })
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	run, eng := m.journalRun, m.engine
	m.mu.Unlock()
	if run != nil || eng == nil || eng.d.Journal != nil {
		t.Fatalf("run %v, engine %v: want no run, an engine without a journal source", run, eng)
	}
	if m.Status()["journal"] != false || m.Status()["ready"] != true {
		t.Fatalf("status = %v", m.Status())
	}
	if m.journalDisabled() {
		t.Error("not wired is not switched off: the API answers its usual 404s")
	}
}

// A daemon without the team feed still writes the journal; only the teams' tasks are not mirrored.
func TestWire_WithoutTheTeamFeedThereIsNoTaskMirror(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	c.Registry.Register(peersmod.OriginResolverKey, &fakeOrigins{})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()); m.Close() })
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	run := m.journalRun
	m.mu.Unlock()
	if run == nil || run.drainer == nil || run.mirror != nil {
		t.Fatalf("run = %+v, want a drainer and no mirror", run)
	}
}

// The four journal routes answer 503 with the journal off, and 200/404 as usual with it on.
// Mutation gate: drop the journalDisabled check in resolveJournal -> red.
func TestWire_OffMakesEveryJournalRouteUnavailable(t *testing.T) {
	e := newJournalAPI(t)
	e.m.journalOff = true
	for _, r := range []struct{ method, url string }{
		{"GET", "/api/journal?session_id=s1"},
		{"GET", "/api/journal/tasks?session_id=s1"},
		{"GET", "/api/journal/roadmap?session_id=s1"},
		{"PUT", "/api/journal/pin"},
	} {
		var body any
		if r.method == "PUT" {
			body = map[string]any{"session_id": "s1", "entry_id": "x", "pinned": true}
		}
		code, out := e.request(r.method, r.url, body)
		if code != http.StatusServiceUnavailable || !strings.Contains(string(out), "unavailable") {
			t.Errorf("%s %s = %d %s, want 503 unavailable", r.method, r.url, code, out)
		}
	}
	e.m.journalOff = false
	if code, _ := e.request("GET", "/api/journal?session_id=s1", nil); code != http.StatusNotFound {
		t.Errorf("with the journal on and no unit: %d, want 404", code)
	}
}

// blockingFeed holds the task mirror inside its first read until release is closed, so a test can tell whether Stop waits
// for a loop that is in the middle of its work.
type blockingFeed struct {
	*fakeFeed
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingFeed) ReportsAfter(after, since int64, limit int) ([]team.ReportRow, error) {
	b.once.Do(func() {
		close(b.entered)
		<-b.release
	})
	return b.fakeFeed.ReportsAfter(after, since, limit)
}

// Stop ends the loops and returns only when they have ended: the store is released after it. The mirror is held inside a
// read of the feed (which ignores the context), so Stop must be seen to wait for it.
// Mutation gate: do not wait, or do not cancel, in journalRun.stop; do not call it from Module.Stop; never start the
// task mirror -> red.
func TestWire_StopWaitsForTheLoopsToEnd(t *testing.T) {
	bf := &blockingFeed{fakeFeed: &fakeFeed{teams: map[string]team.TeamInfo{}}, entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(bf.release) }) }
	defer release()
	k := newWireFeed(t, nil, bf, false)
	k.m.mu.Lock()
	run := k.m.journalRun
	k.m.mu.Unlock()
	select {
	case <-bf.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the task mirror never started")
	}
	done := make(chan struct{})
	go func() { k.m.Stop(context.Background()); close(done) }()
	select {
	case <-done:
		t.Fatal("Stop returned while the task mirror was still inside its work")
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return after the loops were released")
	}
	if run.runCtx.Err() == nil {
		t.Fatal("Stop left the loops' context running")
	}
	if k.journalStatus() {
		t.Error("Status()[journal] still true after Stop")
	}
}

// What prepareJournal hands the engine and the loops, looked at without starting anything.
func TestWire_PrepareConnectsTheParts(t *testing.T) {
	k := newWireFeed(t, nil, nil, true)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[journal]\nperson_name = \"Wake\"\npush_projects = [\"github.com/wake/purdex\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k.c.CfgMu.Lock()
	k.c.CfgPath = cfgPath
	k.c.Cfg.Journal = &config.JournalConfig{PersonName: "Wake"}
	k.c.CfgMu.Unlock()
	k.origins.set(team.Origin{SessionID: "s1", Address: "mlab/purdex-3f-adam"})

	var d Deps
	run := k.m.prepareJournal(k.m.live(), &d)
	if run == nil {
		t.Fatal("prepareJournal returned no run")
	}
	defer run.stop()
	if d.Journal == nil || d.OnJournalResult == nil {
		t.Fatalf("deps = %+v: the engine gets no source or no result hook", d)
	}
	// a result of a journal job asks the drainer for a pass
	d.OnJournalResult(JournalOutcome{})
	if len(run.drainer.kick) != 1 {
		t.Error("a journal result did not kick the drainer")
	}
	// the drainer names sessions and the person
	if got := run.drainer.actor("s1"); got != "Adam" {
		t.Errorf("actor(s1) = %q, want Adam", got)
	}
	if got := run.drainer.person(); got != "Wake" {
		t.Errorf("person() = %q, want Wake", got)
	}
	// the mirror mirrors the teams whose lead is a live session here, and asks the drainer for a pass
	if run.mirror.local("s1") != true || run.mirror.local("gone") != false {
		t.Error("the task mirror's notion of local is not 'the lead's session is live here'")
	}
	if run.mirror.kick == nil {
		t.Error("the task mirror cannot kick the drainer")
	}
	// the git loop keeps its worktrees under the data dir and reads push_projects from the file as it is now
	if want := filepath.Join(k.dataDir(), "journal"); run.git.Dir != want {
		t.Errorf("git dir = %q, want %q", run.git.Dir, want)
	}
	if got, err := run.git.PushProjects(); err != nil || len(got) != 1 || got[0] != "github.com/wake/purdex" {
		t.Errorf("push_projects = %v, %v", got, err)
	}
	if err := os.WriteFile(cfgPath, []byte("[journal]\npush_projects = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := run.git.PushProjects(); len(got) != 0 {
		t.Errorf("push_projects after the edit = %v, want none", got)
	}
}

// A worktree path that is not absolute is refused: the unit would be registered with a place the guards reject.
func TestJournalProjects_ARelativeJournalFolderIsRefused(t *testing.T) {
	k := newProjectKit(t)
	cwd, _ := filepath.EvalSymlinks(t.TempDir())
	k.origins.set(team.Origin{SessionID: "s1", Cwd: cwd})
	k.projects.git.Dir = "relative-journal-dir"
	t.Chdir(t.TempDir())
	if p, err := k.projects.Project(UnitRef{Key: "solo:c1", Kind: UnitSolo, SessionID: "s1"}); err == nil {
		t.Fatalf("project %+v, want an error", p)
	}
}

// ---- the source ----

func TestJournalSource_UnitFilesAndSelfName(t *testing.T) {
	e := newAPI(t)
	origins := &fakeOrigins{}
	origins.set(team.Origin{SessionID: "s1", Address: "mlab/purdex-3f-adam"})
	src := journalSource{st: e.store, origins: origins}

	ent := Entry{Role: team.SeatNone, ConvKey: "c1"}
	if _, ok, err := src.UnitFiles(ent); err != nil || ok {
		t.Fatalf("a unit nothing was written to: ok %v, err %v", ok, err)
	}
	wt := t.TempDir()
	u, err := e.store.RegisterUnit(UnitRow{Key: "solo:c1", Kind: UnitSolo, ProjectID: "p1", WtPath: wt, Label: "purdex"})
	if err != nil {
		t.Fatal(err)
	}
	files, ok, err := src.UnitFiles(ent)
	if err != nil || !ok {
		t.Fatalf("registered unit: ok %v, err %v", ok, err)
	}
	h := unitHome(u)
	if files.JournalDir != h.journalDir || files.Roadmap != h.roadmapFile {
		t.Errorf("files = %+v, want %s and %s (the folders the drainer writes)", files, h.journalDir, h.roadmapFile)
	}
	// a lead's team unit
	if _, err := e.store.RegisterUnit(UnitRow{Key: "team:t1", Kind: UnitTeam, ProjectID: "p1", WtPath: wt, Label: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if f, ok, _ := src.UnitFiles(Entry{Role: team.SeatLead, TeamID: "t1", ConvKey: "c1"}); !ok || !strings.Contains(f.JournalDir, "alpha-") {
		t.Errorf("lead of team t1: %+v, %v", f, ok)
	}
	// a member has no unit of its own: no files
	if _, ok, _ := src.UnitFiles(Entry{Role: team.SeatMember, TeamID: "t1", ConvKey: "c2"}); ok {
		t.Error("a member got files")
	}
	// a symlinked folder on the way is an error, not a path
	if err := os.MkdirAll(filepath.Join(wt, ".purdex"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(wt, ".purdex", "journal")); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := src.UnitFiles(ent); err == nil || ok {
		t.Errorf("a symlinked journal folder: ok %v, err %v, want an error", ok, err)
	}

	if got := src.SelfName("s1"); got != "mlab/purdex-3f-adam" {
		t.Errorf("SelfName(live) = %q", got)
	}
	if got := src.SelfName("gone"); got != "" {
		t.Errorf("SelfName(not live) = %q, want empty", got)
	}
	origins.err = errors.New("registry unreadable")
	if got := src.SelfName("s1"); got != "" {
		t.Errorf("SelfName(registry error) = %q, want empty", got)
	}
}

// ---- the unit's project ----

type projectKit struct {
	projects *journalProjects
	origins  *fakeOrigins
	feed     *fakeFeed
	dir      string // the Git's folder
}

func newProjectKit(t *testing.T) *projectKit {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	k := &projectKit{origins: &fakeOrigins{}, feed: &fakeFeed{teams: map[string]team.TeamInfo{}}, dir: filepath.Join(dir, "journal")}
	k.projects = &journalProjects{ctx: context.Background(), git: &journal.Git{Dir: k.dir, Logf: t.Logf}, origins: k.origins, feed: k.feed,
		convOf: func(sid string) string { return "conv-of-" + sid }}
	return k
}

// gitRepo makes a repository with one commit and returns its (symlink-free) path.
func gitRepo(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "myrepo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--allow-empty", "-m", "x"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out.String())
		}
	}
	return root
}

// A solo session's unit lives in the journal worktree of the project its cwd is in; the registered path is the worktree's
// root (the folder that holds .purdex).
func TestJournalProjects_SoloUsesTheSessionsOwnCwd(t *testing.T) {
	k := newProjectKit(t)
	repo := gitRepo(t)
	sub := filepath.Join(repo, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	k.origins.set(team.Origin{SessionID: "s1", Cwd: sub})
	want, err := journal.ProjectOf(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	p, err := k.projects.Project(UnitRef{Key: "solo:c1", Kind: UnitSolo, ConvKey: "c1", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ProjectID != want.ID {
		t.Errorf("ProjectID = %q, want %q (the repository's)", p.ProjectID, want.ID)
	}
	if p.WtPath != journal.WorktreeDir(k.dir, want) || !filepath.IsAbs(p.WtPath) {
		t.Errorf("WtPath = %q, want %q", p.WtPath, journal.WorktreeDir(k.dir, want))
	}
	if _, err := os.Stat(filepath.Join(p.WtPath, ".purdex")); err != nil {
		t.Errorf("the .purdex folder is not there: %v", err)
	}
	if p.Label != "myrepo" || p.LeadConv != "c1" {
		t.Errorf("label %q, lead conv %q, want myrepo and c1", p.Label, p.LeadConv)
	}
}

// A team's unit is the lead's project (the session of the row is the lead's), named by the team's label, owned by the lead's
// conversation.
func TestJournalProjects_TeamUsesTheLeadsCwdAndLabel(t *testing.T) {
	k := newProjectKit(t)
	repo := gitRepo(t)
	k.origins.set(team.Origin{SessionID: "lead1", Cwd: repo})
	k.origins.set(team.Origin{SessionID: "member1", Cwd: t.TempDir()}) // elsewhere: must not matter
	k.feed.teams["t1"] = team.TeamInfo{TeamID: "t1", Label: "Alpha team", LeadSessionID: "lead1"}
	want, _ := journal.ProjectOf(context.Background(), repo)

	p, err := k.projects.Project(UnitRef{Key: "team:t1", Kind: UnitTeam, TeamID: "t1", SessionID: "lead1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ProjectID != want.ID {
		t.Errorf("ProjectID = %q, want the lead's repository %q", p.ProjectID, want.ID)
	}
	if p.Label != "Alpha team" || p.LeadConv != "conv-of-lead1" {
		t.Errorf("label %q, lead conv %q", p.Label, p.LeadConv)
	}
	// a label the task mirror knows wins
	p, err = k.projects.Project(UnitRef{Key: "team:t1", Kind: UnitTeam, TeamID: "t1", SessionID: "lead1", Label: "Mirror label"})
	if err != nil || p.Label != "Mirror label" {
		t.Errorf("label %q, err %v, want Mirror label", p.Label, err)
	}
	// an unknown team: a name that is still a name
	p, err = k.projects.Project(UnitRef{Key: "team:t9", Kind: UnitTeam, TeamID: "t9", SessionID: "lead1"})
	if err != nil || p.Label != "team" {
		t.Errorf("unknown team: label %q, err %v, want team", p.Label, err)
	}
}

// A folder that is not a repository still gets a journal (files only, never committed), in the same place.
func TestJournalProjects_AFolderWithoutGitKeepsFilesOnly(t *testing.T) {
	k := newProjectKit(t)
	cwd, _ := filepath.EvalSymlinks(t.TempDir())
	k.origins.set(team.Origin{SessionID: "s1", Cwd: cwd})
	p, err := k.projects.Project(UnitRef{Key: "solo:c1", Kind: UnitSolo, ConvKey: "c1", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.WtPath, k.dir) || p.ProjectID == "" || p.Label != filepath.Base(cwd) {
		t.Errorf("project = %+v", p)
	}
}

// A session the registry does not list, an empty cwd and an unreadable registry are errors: the drainer leaves the row
// undone and tries again; nothing is guessed.
func TestJournalProjects_ASessionThatCannotBeFoundIsAnErrorNotAGuess(t *testing.T) {
	k := newProjectKit(t)
	k.origins.set(team.Origin{SessionID: "nocwd"})
	for name, sid := range map[string]string{"not live": "gone", "no cwd": "nocwd"} {
		if p, err := k.projects.Project(UnitRef{Key: "solo:c1", Kind: UnitSolo, SessionID: sid}); err == nil {
			t.Errorf("%s: project %+v, want an error", name, p)
		}
	}
	k.origins.set(team.Origin{SessionID: "s1", Cwd: t.TempDir()}) // listed, but the read that found it failed
	k.origins.err = errors.New("registry unreadable")
	if _, err := k.projects.Project(UnitRef{Key: "solo:c1", Kind: UnitSolo, SessionID: "s1"}); err == nil {
		t.Error("an unreadable registry gave a project")
	}
	if entries, _ := os.ReadDir(k.dir); len(entries) != 0 {
		t.Errorf("a worktree was made for a session that could not be found: %v", entries)
	}
}

// listingOrigins is a fakeOrigins that can also list its live sessions, counting how often it is read.
type listingOrigins struct {
	*fakeOrigins
	mu    sync.Mutex
	lists int
}

func (l *listingOrigins) ListLiveOrigins() ([]team.Origin, error) {
	l.mu.Lock()
	l.lists++
	l.mu.Unlock()
	l.fakeOrigins.mu.Lock()
	defer l.fakeOrigins.mu.Unlock()
	if l.fakeOrigins.err != nil {
		return nil, l.fakeOrigins.err
	}
	var out []team.Origin
	for _, o := range l.fakeOrigins.live {
		out = append(out, o)
	}
	return out, nil
}

func (l *listingOrigins) reads() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lists
}

// The task mirror asks about the lead of every team on every tick; the registry is read once per liveTTL for all of them, not
// once per question, and a session that appears (or goes) is seen after the TTL.
// Mutation gate: no cache (read at every question) -> red; never refresh -> red; a read error answered true -> red.
func TestLiveSessions_OneRegistryReadServesManyQuestions(t *testing.T) {
	src := &listingOrigins{fakeOrigins: &fakeOrigins{}}
	src.set(team.Origin{SessionID: "a"})
	src.set(team.Origin{SessionID: "b"})
	now := time.Unix(1_700_000_000, 0)
	ls := newLiveSessions(src)
	ls.now = func() time.Time { return now }

	for i := 0; i < 50; i++ {
		if !ls.has("a") || !ls.has("b") || ls.has("gone") {
			t.Fatal("a live session was not found, or a session that is not live was")
		}
	}
	if n := src.reads(); n != 1 {
		t.Fatalf("%d registry reads for 150 questions, want 1", n)
	}
	src.set(team.Origin{SessionID: "c"})
	if ls.has("c") {
		t.Error("a session that appeared was seen before the TTL ended")
	}
	now = now.Add(liveTTL)
	if !ls.has("c") || src.reads() != 2 {
		t.Errorf("after the TTL: has(c) false or %d reads, want true and 2", src.reads())
	}
	// an unreadable registry answers "not live here" and is read again by the next question
	src.err = errors.New("registry unreadable")
	now = now.Add(liveTTL)
	if ls.has("a") {
		t.Error("an unreadable registry answered live")
	}
	src.err = nil
	if !ls.has("a") {
		t.Error("the registry was not read again after the error")
	}
}

// A source that cannot list is asked one session at a time (the same answers, no cache).
func TestLiveSessions_ASourceWithoutAListIsAskedPerSession(t *testing.T) {
	src := &fakeOrigins{}
	src.set(team.Origin{SessionID: "a"})
	ls := newLiveSessions(src)
	if !ls.has("a") || ls.has("gone") {
		t.Fatal("per-session lookup gave the wrong answer")
	}
	src.err = errors.New("registry unreadable")
	if ls.has("a") {
		t.Error("an unreadable registry answered live")
	}
}

// The whole path in one: a finished turn of a live solo session ends up as an exec entry in the project's journal worktree
// under the Git folder, the App is told, and the git loop commits it on purdex/journal (the main checkout is untouched). The
// drainer, the unit's project, the notification and the git loop are the production ones.
// Mutation gate: do not start the drainer or the git loop (journalRun.start) -> red; do not hand the drainer SetNotify -> red.
func TestWire_AFinishedTurnIsWrittenToTheProjectsJournalAndAnnounced(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	cwd := gitRepo(t)
	k := newWireFeed(t, nil, nil, true)
	k.m.gitInterval = 100 * time.Millisecond
	if err := k.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	k.origins.set(team.Origin{SessionID: "c1-s", Cwd: cwd, Address: "mlab/purdex-3f-adam"})
	var mu sync.Mutex
	var sent []string
	k.m.mu.Lock()
	k.m.broadcast = func(typ, value string) {
		mu.Lock()
		sent = append(sent, typ+" "+value)
		mu.Unlock()
	}
	run := k.m.journalRun
	k.m.mu.Unlock()

	st := k.m.live()
	e := pending("c1", "c1-s", "t1", 1_700_000_000_000)
	e.Role = team.SeatNone
	id := mustInsert(t, st, e)
	if _, ok, err := st.SetPushLineV2(id, PushLineV2{Thing: "事", Push: "推", Status: "狀況", By: ClosedByModel}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := st.Finish(id, StateOK, "", Output{Thing: "事", Push: "推", Entry: "做完了一件事"}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	run.drainer.Kick()

	var text string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && text == "" {
		files, _ := filepath.Glob(filepath.Join(k.dataDir(), "journal", "*", "wt", ".purdex", "journal", "*", "*.md"))
		for _, f := range files {
			if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "做完了一件事") {
				text = string(b)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if text == "" {
		t.Fatal("the entry was not written to the project's journal worktree")
	}
	if !strings.Contains(text, "Adam") {
		t.Errorf("the entry does not carry the session's display name:\n%s", text)
	}
	changed := func() string {
		mu.Lock()
		defer mu.Unlock()
		for _, s := range sent {
			if strings.HasPrefix(s, journalChangedType+" ") {
				return s
			}
		}
		return ""
	}
	for time.Now().Before(deadline) && changed() == "" {
		time.Sleep(20 * time.Millisecond)
	}
	if got := changed(); !strings.Contains(got, `"what":"entries"`) || !strings.Contains(got, `"id":"c1"`) {
		t.Errorf("journal.changed = %q, want one for the entries of solo c1", got)
	}
	// the git loop commits the journal folder on its branch
	var wts []string
	for time.Now().Before(deadline) {
		wts, _ = filepath.Glob(filepath.Join(k.dataDir(), "journal", "*", "wt"))
		if len(wts) == 1 {
			out, err := exec.Command("git", "-C", wts[0], "log", "--format=%s", journal.Branch).Output()
			if err == nil && strings.Contains(string(out), "journal: ") {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(wts) != 1 {
		t.Fatalf("journal worktrees = %v, want one", wts)
	}
	out, err := exec.Command("git", "-C", wts[0], "log", "--format=%s", journal.Branch).Output()
	if err != nil || !strings.Contains(string(out), "journal: ") {
		t.Errorf("the journal branch has no journal commit: %q, %v", out, err)
	}
	if st, _ := exec.Command("git", "-C", cwd, "status", "--porcelain").Output(); len(strings.TrimSpace(string(st))) != 0 {
		t.Errorf("the main checkout changed: %s", st)
	}
}

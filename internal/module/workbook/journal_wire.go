package workbook

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/journal"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// The journal's production wiring (plan J-1e (c)): what startEngine builds when `[journal] enabled` is not false - the
// journal job's source, the drainer that writes the outbox to the unit's files, the task mirror, and the git loop that
// commits them. With the switch off none of it exists: no journal job (so no second model call after a turn), no
// goroutine, no capability, and the API answers 503.

// originSource is the part of the peers module's OriginResolver the journal needs: a live session's registry entry (its
// cwd, and its address for the display name).
type originSource interface {
	ResolveOriginBySession(sessionID string) (team.Origin, bool, error)
}

// journalSource implements JournalSource over the unit registry and the live sessions.
type journalSource struct {
	st      *Store
	origins originSource
}

// UnitFiles is where the entry's unit keeps its files; ok is false while the unit has none (nothing written yet) and for
// a seat that has no unit. A folder of the unit's home that is a symlink is an error: the job then runs with empty lists.
func (s journalSource) UnitFiles(e Entry) (JournalFiles, bool, error) {
	key, _, ok := UnitKeyOf(e.Role, e.TeamID, e.ConvKey)
	if !ok {
		return JournalFiles{}, false, nil
	}
	u, found, err := s.st.Unit(key)
	if err != nil || !found {
		return JournalFiles{}, false, err
	}
	h := unitHome(u)
	if err := h.verify(); err != nil {
		return JournalFiles{}, false, err
	}
	return JournalFiles{JournalDir: h.journalDir, Roadmap: h.roadmapFile}, true, nil
}

// SelfName is the session's pdx address (`<host>/<virtual name>`; the journal shows its display name), "" when the session
// is not live.
func (s journalSource) SelfName(sessionID string) string {
	return addressOf(s.origins, sessionID)
}

func addressOf(o originSource, sessionID string) string {
	origin, ok, err := o.ResolveOriginBySession(sessionID)
	if err != nil || !ok {
		return ""
	}
	return origin.Address
}

// journalProjects implements UnitProjects: the unit's project is the one the lead's (or the solo session's) cwd is in, and
// its files live in that project's journal worktree (journal.Git).
type journalProjects struct {
	ctx     context.Context // ends with the module: a worktree being made stops
	git     *journal.Git
	origins originSource
	feed    team.JournalFeed              // names a team unit's folder; nil: the unit is called "team"
	convOf  func(sessionID string) string // the conversation of a session (its relay chain's root)
}

func (p *journalProjects) Project(ref UnitRef) (UnitProject, error) {
	o, ok, err := p.origins.ResolveOriginBySession(ref.SessionID)
	if err != nil {
		return UnitProject{}, fmt.Errorf("find session %s: %w", ref.SessionID, err)
	}
	if !ok || strings.TrimSpace(o.Cwd) == "" {
		return UnitProject{}, fmt.Errorf("session %s is not live or has no working directory", ref.SessionID) // retried: the row stays undone
	}
	proj, err := journal.ProjectOf(p.ctx, o.Cwd)
	if err != nil {
		return UnitProject{}, fmt.Errorf("project of %s: %w", o.Cwd, err)
	}
	var wt string
	if err := p.git.With(p.ctx, proj, func(purdexDir string) error {
		wt = filepath.Dir(purdexDir)
		return nil
	}); err != nil {
		return UnitProject{}, fmt.Errorf("journal worktree of project %s: %w", proj.ID, err)
	}
	if !filepath.IsAbs(wt) {
		return UnitProject{}, fmt.Errorf("journal worktree %q is not an absolute path", wt)
	}
	up := UnitProject{ProjectID: proj.ID, WtPath: wt, Label: ref.Label, LeadConv: ref.ConvKey}
	if ref.Kind == UnitTeam {
		if up.LeadConv == "" && p.convOf != nil {
			up.LeadConv = p.convOf(ref.SessionID)
		}
		if up.Label == "" && p.feed != nil {
			if info, found, err := p.feed.TeamInfo(ref.TeamID); err == nil && found {
				up.Label = info.Label
			}
		}
		if up.Label == "" {
			up.Label = "team"
		}
	} else if up.Label == "" {
		up.Label = filepath.Base(proj.Root)
	}
	return up, nil
}

// journalRun is what the module started for the journal, ended by stop.
type journalRun struct {
	runCtx  context.Context // shared by every loop; stop cancels it
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	drainer *Drainer
	mirror  *TaskMirror // nil: the daemon has no team feed
	git     *journal.Git
}

func (r *journalRun) start() {
	r.goRun(r.drainer.Run)
	if r.mirror != nil {
		r.goRun(r.mirror.Run)
	}
	r.goRun(r.git.Run)
}

func (r *journalRun) goRun(fn func(context.Context)) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		fn(r.runCtx)
	}()
}

// stop ends the loops and waits for them: nothing writes to the store or the files after it returns.
func (r *journalRun) stop() {
	r.cancel()
	r.wg.Wait()
}

// prepareJournal builds the journal's parts into d (the source and the result hook the engine needs) and returns the run
// that starts the loops, or nil when the journal is off or the daemon lacks what it needs (logged). It starts nothing.
func (m *Module) prepareJournal(st *Store, d *Deps) *journalRun {
	m.core.CfgMu.RLock()
	enabled := m.core.Cfg.JournalEnabled()
	dataDir := m.core.Cfg.DataDir
	m.core.CfgMu.RUnlock()
	if !enabled {
		m.mu.Lock()
		m.journalOff = true
		m.mu.Unlock()
		log.Printf("[workbook] journal: off ([journal] enabled = false): no journal job, no drainer, no task mirror, no git loop")
		return nil
	}
	reg := m.core.Registry
	if reg == nil {
		return nil
	}
	svc, ok := reg.Get(peersmod.OriginResolverKey)
	origins, isOrigins := svc.(originSource)
	if !ok || !isOrigins {
		log.Printf("[workbook] journal: not wired: service %q is missing or has no ResolveOriginBySession", peersmod.OriginResolverKey)
		return nil
	}
	var feed team.JournalFeed
	if fs, ok := reg.Get(team.JournalFeedKey); ok {
		feed, _ = fs.(team.JournalFeed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	g := &journal.Git{
		Dir:          filepath.Join(dataDir, "journal"),
		PushProjects: func() ([]string, error) { return config.ReadJournalPushProjects(m.core.CfgPath) },
		Logf:         log.Printf,
	}
	projects := &journalProjects{ctx: ctx, git: g, origins: origins, feed: feed, convOf: func(sid string) string {
		if conv, err := m.convKeyOf(sid); err == nil {
			return conv
		}
		return sid
	}}
	dr := NewDrainer(st, projects, log.Printf)
	dr.SetNames(func(sid string) string { return addressOf(origins, sid) }, m.personName)
	dr.SetNotify(m.announceJournal)
	run := &journalRun{cancel: cancel, runCtx: ctx, drainer: dr, git: g}
	if feed != nil {
		local := func(leadSessionID string) bool {
			_, ok, err := origins.ResolveOriginBySession(leadSessionID)
			return err == nil && ok
		}
		run.mirror = NewTaskMirror(st, feed, local, dr.Kick, log.Printf)
	} else {
		log.Printf("[workbook] journal: no team feed: the team tasks are not mirrored")
	}
	d.Journal = journalSource{st: st, origins: origins}
	d.OnJournalResult = func(JournalOutcome) { dr.Kick() }
	return run
}

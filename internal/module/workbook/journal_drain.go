package workbook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/wake/purdex/internal/journal"
)

// UnitRef says whose journal a row belongs to. SessionID is the session of the entry being drained: for a team unit
// that is the lead's session (only a lead's turns write to a team journal).
type UnitRef struct {
	Key, Kind, TeamID, ConvKey, SessionID string
	Label                                 string // a team's label (the task mirror knows it); "" for a solo unit
}

// UnitProject is where a unit's project keeps its journal: the root of the registered worktree (J-1d builds it) and
// the label that names the unit's folder.
type UnitProject struct {
	ProjectID string
	WtPath    string
	Label     string
	LeadConv  string
}

// UnitProjects resolves a unit's project, once, at its first write: the lead's cwd for a team, the session's own for a
// solo unit. It is injected - J-1d wires it to the git worktree; until then nothing in production starts a drainer.
type UnitProjects interface {
	Project(ref UnitRef) (UnitProject, error)
}

const (
	drainBatch    = 50
	maxPasses     = 20
	drainEvery    = 5 * time.Second
	retryBase     = 5 * time.Second
	retryMax      = 5 * time.Minute
	errLogEvery   = time.Hour
	slugMaxRunes  = 40
	slugHashBytes = 3
)

// Drainer writes the journal outbox to the unit's files. A row is written only once its entry is no longer pending
// (an `ok` entry also writes its exec line, the final text); the files are written idempotently by entry id, so a
// row written twice (a crash before it was marked done) changes nothing. A file error leaves the row undone, retried
// with a growing delay; it never touches the workbook entry.
type Drainer struct {
	st       *Store
	projects UnitProjects
	logf     func(string, ...any)
	now      func() time.Time

	mu      sync.Mutex // guards projMu, retry and taskRow
	projMu  map[string]*sync.Mutex
	retry   map[int64]*retryState
	taskRow map[string]int64 // unit key -> the newest task row whose list its tasks file shows
	kick    chan struct{}

	markDone func(id int64) error // test seam: nil = Store.MarkOutboxDone

	selfName   func(sessionID string) string // the session's virtual name (SetNames); nil: entries carry no actor
	personName func() string                 // the person's name (SetNames); nil: the default
}

type retryState struct {
	fails   int
	next    time.Time
	lastLog time.Time
}

// NewDrainer makes a drainer over the store. Start it with Run.
func NewDrainer(st *Store, projects UnitProjects, logf func(string, ...any)) *Drainer {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Drainer{st: st, projects: projects, logf: logf, now: time.Now, projMu: map[string]*sync.Mutex{},
		retry: map[int64]*retryState{}, taskRow: map[string]int64{}, kick: make(chan struct{}, 1)}
}

// SetNames tells the drainer how to name the session an entry belongs to (its virtual name; the drainer writes the display
// name) and the person (config [journal] person_name, read at each write). Either may be nil. Call it before Run.
func (d *Drainer) SetNames(selfName func(sessionID string) string, personName func() string) {
	d.selfName, d.personName = selfName, personName
}

// actor is the display name of the session an entry belongs to, or "" when it cannot be named.
func (d *Drainer) actor(sessionID string) string {
	if d.selfName == nil {
		return ""
	}
	return journal.DisplayName(d.selfName(sessionID))
}

// person is the person's name for a request they make.
func (d *Drainer) person() string {
	if d.personName != nil {
		if n := strings.TrimSpace(d.personName()); n != "" {
			return n
		}
	}
	return journal.DefaultPersonName
}

// Kick asks for a pass soon (after a result was applied).
func (d *Drainer) Kick() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// Run drains at start, on every Kick and every few seconds, until ctx ends.
func (d *Drainer) Run(ctx context.Context) {
	t := time.NewTicker(drainEvery)
	defer t.Stop()
	for {
		if _, err := d.Drain(); err != nil {
			d.logf("[workbook] journal drain: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-d.kick:
		}
	}
}

// Drain writes what is ready (repeating while a pass writes something: a journal row waits for its turn row) and
// reports how many rows it wrote. A row that fails is skipped until its retry time; the error
// is logged (once an hour per row) and the pass goes on with the others.
func (d *Drainer) Drain() (int, error) {
	n := 0
	for pass := 0; pass < maxPasses; pass++ { // a journal row becomes ready when its turn row is done: go on while a pass writes
		wrote, tried, err := d.pass()
		n += wrote
		if err != nil || tried == 0 { // a failed row enters its delay, so the next pass reaches rows further down
			return n, err
		}
	}
	return n, nil
}

// pass tries one batch: how many rows it wrote and how many it tried.
func (d *Drainer) pass() (wrote, tried int, err error) {
	items, err := d.st.ReadyOutbox(drainBatch, d.waiting())
	if err != nil {
		return 0, 0, err
	}
	n := 0
	for _, it := range items {
		if !d.due(it.ID) {
			continue
		}
		tried++
		if err := d.writeRow(it); err != nil {
			d.failed(it, err)
			continue
		}
		mark := d.st.MarkOutboxDone
		if d.markDone != nil {
			mark = d.markDone
		}
		if err := mark(it.ID); err != nil {
			d.failed(it, err)
			continue
		}
		d.mu.Lock()
		delete(d.retry, it.ID)
		d.mu.Unlock()
		n++
	}
	return n, tried, nil
}

// waiting lists the rows that are in their retry delay.
func (d *Drainer) waiting() []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	var ids []int64
	for id, r := range d.retry {
		if now.Before(r.next) {
			ids = append(ids, id)
		}
	}
	return ids
}

func (d *Drainer) due(id int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.retry[id]
	return r == nil || !d.now().Before(r.next)
}

func (d *Drainer) failed(it OutboxItem, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.retry[it.ID]
	if r == nil {
		r = &retryState{}
		d.retry[it.ID] = r
	}
	r.fails++
	wait := retryBase << min(r.fails-1, 10)
	if wait > retryMax {
		wait = retryMax
	}
	now := d.now()
	r.next = now.Add(wait)
	if r.lastLog.IsZero() || now.Sub(r.lastLog) >= errLogEvery {
		r.lastLog = now
		d.logf("[workbook] journal outbox row %d (%s, entry %d) is not written yet (attempt %d): %v", it.ID, it.Kind, it.EntryID, r.fails, err)
	}
}

func (d *Drainer) projectLock(id string) *sync.Mutex {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.projMu[id]
	if m == nil {
		m = &sync.Mutex{}
		d.projMu[id] = m
	}
	return m
}

// home is where one unit's files are.
type home struct {
	journalDir, tasksFile, roadmapFile string
	solo                               bool
	convKey                            string
}

func (d *Drainer) writeRow(it OutboxItem) error {
	if it.Kind == OutboxTask { // belongs to no workbook entry
		return d.writeTaskRow(it)
	}
	ent, err := d.st.Entry(it.EntryID)
	if err != nil {
		return err
	}
	if ent.State == StatePending {
		return nil // not reached: ReadyOutbox filters; kept so a direct caller can never write early
	}
	reg, err := d.unit(it, ent)
	if err != nil {
		return err
	}
	lock := d.projectLock(reg.ProjectID)
	lock.Lock()
	defer lock.Unlock()
	slug := unitSlug(reg.Label, reg.Key)
	h := home{
		journalDir:  filepath.Join(reg.WtPath, ".purdex", "journal", slug),
		tasksFile:   filepath.Join(reg.WtPath, ".purdex", "tasks", slug+".md"),
		roadmapFile: filepath.Join(reg.WtPath, ".purdex", "roadmap.md"),
		solo:        reg.Kind == UnitSolo, convKey: ent.ConvKey,
	}
	switch it.Kind {
	case OutboxTurn:
		var p turnPayload
		if err := json.Unmarshal([]byte(it.Payload), &p); err != nil {
			return fmt.Errorf("decode outbox row %d: %w", it.ID, err) // stays undone: a binary that can decode it may come back
		}
		return d.writeTurn(h, ent, p)
	case OutboxJournal:
		var p journalPayload
		if err := json.Unmarshal([]byte(it.Payload), &p); err != nil {
			return fmt.Errorf("decode outbox row %d: %w", it.ID, err) // stays undone: a binary that can decode it may come back
		}
		return d.writeJournal(h, ent, p)
	}
	// A kind this binary does not know (a newer daemon wrote it, then this one was rolled back) stays undone, retried
	// and logged, so the upgraded binary can write it.
	return fmt.Errorf("unknown outbox kind %q", it.Kind)
}

// unit finds the unit's registration, making it (and asking the resolver for its project) on the first write.
func (d *Drainer) unit(it OutboxItem, ent Entry) (UnitRow, error) {
	_, kind, _ := UnitKeyOf(ent.Role, ent.TeamID, ent.ConvKey)
	return d.unitFor(it.UnitKey, UnitRef{Key: it.UnitKey, Kind: kind, TeamID: ent.TeamID, ConvKey: ent.ConvKey, SessionID: ent.SessionID})
}

// unitFor is unit for a row that names its unit itself (the task mirror's rows have no entry to read it from).
func (d *Drainer) unitFor(key string, ref UnitRef) (UnitRow, error) {
	if u, ok, err := d.st.Unit(key); err != nil || ok {
		return u, err
	}
	kind := ref.Kind
	p, err := d.projects.Project(ref)
	if err != nil {
		return UnitRow{}, fmt.Errorf("resolve the unit's project: %w", err)
	}
	if p.ProjectID == "" || p.WtPath == "" {
		return UnitRow{}, errors.New("resolve the unit's project: empty project")
	}
	return d.st.RegisterUnit(UnitRow{Key: key, Kind: kind, ProjectID: p.ProjectID, WtPath: p.WtPath, Label: p.Label, LeadConv: p.LeadConv})
}

func at(ms int64) time.Time {
	if ms <= 0 {
		return time.Now()
	}
	return time.UnixMilli(ms)
}

// putNew writes an entry that is not in its month file yet. A replay (a crash after the write, before the row was marked
// done) must not overwrite what happened to the entry since: a pin set or cleared by hand, an item ticked.
func (h home) putNew(e journal.Entry) error {
	file := journal.MonthFile(h.journalDir, e.At)
	have, err := journal.Parse(file)
	if err != nil {
		return err
	}
	for _, x := range have {
		if x.ID == e.ID {
			return nil
		}
	}
	return journal.Upsert(file, e)
}

// writeTurn writes a turn row: the solo task entries and tasks file, then the exec entry (only for an ok entry).
func (d *Drainer) writeTurn(h home, ent Entry, p turnPayload) error {
	when := at(ent.TurnAt)
	actor := d.actor(ent.SessionID)
	if h.solo {
		for _, t := range p.Todos {
			e := journal.Entry{ID: fmt.Sprintf("todo:%d:new", t.ID), Kind: journal.KindTaskNew, At: when, Actor: actor, Text: fmt.Sprintf("#%d %s", t.Seq, t.Title)}
			switch t.State {
			case TodoOpen:
			case TodoDone:
				e.ID, e.Kind = fmt.Sprintf("todo:%d:done", t.ID), journal.KindTaskDone
			default: // dropped: no entry, the task file shows it
				continue
			}
			if err := h.putNew(e); err != nil {
				return err
			}
		}
		if err := d.writeTasks(h); err != nil {
			return err
		}
	}
	if ent.Kind == KindTurn && ent.State == StateOK && strings.TrimSpace(ent.Entry) != "" { // a refresh's line is not a turn's account
		kind := journal.KindExec
		if ent.ThingDone {
			kind = journal.KindExecDone
		}
		if err := h.putNew(journal.Entry{ID: fmt.Sprintf("turn:%d", ent.ID), Kind: kind, At: when, Actor: actor, Text: strings.TrimSpace(ent.Entry)}); err != nil {
			return err
		}
	}
	return nil
}

// writeTasks rewrites the solo unit's tasks file from the conversation's todos (all of them, so a change by a refresh
// shows too). A conversation with no todo and no file writes nothing.
func (d *Drainer) writeTasks(h home) error {
	todos, err := d.st.ConvTodos(h.convKey)
	if err != nil {
		return err
	}
	if len(todos) == 0 {
		if _, serr := os.Stat(h.tasksFile); errors.Is(serr, os.ErrNotExist) {
			return nil
		} else if serr != nil {
			return serr // not "there is no file": retry instead of leaving a stale one
		}
	}
	tasks := make([]journal.Task, 0, len(todos))
	for _, t := range todos {
		st := journal.TaskTodo
		switch t.State {
		case TodoDone:
			st = journal.TaskDone
		case TodoDropped:
			st = journal.TaskDropped
		}
		tasks = append(tasks, journal.Task{Key: fmt.Sprintf("#%d", t.Seq), Title: t.Title, State: st})
	}
	return journal.WriteTasks(h.tasksFile, tasks)
}

// writeJournal writes a journal row: the events, then the ticks, then the roadmap statuses.
func (d *Drainer) writeJournal(h home, ent Entry, p journalPayload) error {
	when := at(ent.TurnAt)
	for i, ev := range p.Events {
		e := journal.Entry{ID: fmt.Sprintf("ev:%d:%d", ent.ID, i), Kind: journal.Kind(ev.Kind), By: journal.By(ev.By), At: when, Actor: ev.Actor, Text: ev.Text, Detail: ev.Detail}
		for _, it := range ev.Items {
			e.Items = append(e.Items, journal.Item{Text: it})
		}
		if e.By == journal.ByUser {
			e.Person = p.Person
			if e.Person == "" { // a row written before the job's name was kept
				e.Person = d.person()
			}
		}
		e.Pinned = e.Kind == journal.KindConfirm
		if err := h.putNew(e); err != nil {
			return err
		}
	}
	for _, t := range p.Ticks {
		if err := d.tick(h, t); err != nil {
			return err
		}
	}
	for _, r := range p.Roadmap {
		_, err := journal.SetStatus(h.roadmapFile, r.Feature, r.Stage, r.Status)
		switch {
		case err == nil:
		case errors.Is(err, os.ErrNotExist), errors.Is(err, journal.ErrUnknownStatus):
			d.logf("[workbook] journal roadmap: %s / %s not set: %v", r.Feature, r.Stage, err) // nothing to set, retrying cannot help
		default:
			return err
		}
	}
	return nil
}

// tick marks one confirm item answered, in whichever month file holds the entry; a confirm whose items are all
// answered is unpinned. An entry or item that is not there (edited away by hand) is skipped: retrying cannot make it.
func (d *Drainer) tick(h home, t payloadTick) error {
	files, err := filepath.Glob(filepath.Join(h.journalDir, "*.md"))
	if err != nil {
		return err
	}
	for i := len(files) - 1; i >= 0; i-- { // newest month first
		list, err := journal.Parse(files[i])
		if err != nil {
			return err
		}
		for _, e := range list {
			if e.ID != t.Entry {
				continue
			}
			if t.Idx < 0 || t.Idx >= len(e.Items) {
				d.logf("[workbook] journal tick: entry %s has no item %d", t.Entry, t.Idx)
				return nil
			}
			if _, err := journal.Tick(files[i], t.Entry, t.Idx); err != nil {
				return err
			}
			e.Items[t.Idx].Done = true
			all := true
			for _, it := range e.Items {
				all = all && it.Done
			}
			if all {
				_, err = journal.SetPinned(files[i], t.Entry, false)
			}
			return err
		}
	}
	d.logf("[workbook] journal tick: entry %s not found", t.Entry)
	return nil
}

// unitSlug names a unit's folder: the label made safe for a file name plus a short hash of the unit key, so two units
// with one label never share a folder.
func unitSlug(label, key string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(label) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if r := []rune(s); len(r) > slugMaxRunes {
		s = strings.Trim(string(r[:slugMaxRunes]), "-")
	}
	if s == "" {
		s = "unit"
	}
	sum := sha256.Sum256([]byte(key))
	return s + "-" + hex.EncodeToString(sum[:slugHashBytes])
}

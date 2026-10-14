package workbook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

	mu     sync.Mutex // guards projMu and retry
	projMu map[string]*sync.Mutex
	retry  map[int64]*retryState
	kick   chan struct{}

	markDone func(id int64) error // test seam: nil = Store.MarkOutboxDone
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
		retry: map[int64]*retryState{}, kick: make(chan struct{}, 1)}
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
			return d.badPayload(it, err)
		}
		return d.writeTurn(h, ent, p)
	case OutboxJournal:
		var p journalPayload
		if err := json.Unmarshal([]byte(it.Payload), &p); err != nil {
			return d.badPayload(it, err)
		}
		return d.writeJournal(h, ent, p)
	}
	// A kind this binary does not know (a newer daemon wrote it, then this one was rolled back) stays undone, retried
	// and logged, so the upgraded binary can write it; only a payload that cannot be decoded is dropped.
	return fmt.Errorf("unknown outbox kind %q", it.Kind)
}

// badPayload logs a row that can never be written and lets it count as done: retrying would only repeat the failure.
func (d *Drainer) badPayload(it OutboxItem, err error) error {
	d.logf("[workbook] journal outbox row %d is dropped: %v", it.ID, err)
	return nil
}

// unit finds the unit's registration, making it (and asking the resolver for its project) on the first write.
func (d *Drainer) unit(it OutboxItem, ent Entry) (UnitRow, error) {
	if u, ok, err := d.st.Unit(it.UnitKey); err != nil || ok {
		return u, err
	}
	_, kind, _ := UnitKeyOf(ent.Role, ent.TeamID, ent.ConvKey)
	p, err := d.projects.Project(UnitRef{Key: it.UnitKey, Kind: kind, TeamID: ent.TeamID, ConvKey: ent.ConvKey, SessionID: ent.SessionID})
	if err != nil {
		return UnitRow{}, fmt.Errorf("resolve the unit's project: %w", err)
	}
	if p.ProjectID == "" || p.WtPath == "" {
		return UnitRow{}, errors.New("resolve the unit's project: empty project")
	}
	return d.st.RegisterUnit(UnitRow{Key: it.UnitKey, Kind: kind, ProjectID: p.ProjectID, WtPath: p.WtPath, Label: p.Label, LeadConv: p.LeadConv})
}

func at(ms int64) time.Time {
	if ms <= 0 {
		return time.Now()
	}
	return time.UnixMilli(ms)
}

func (h home) put(e journal.Entry) error {
	return journal.Upsert(journal.MonthFile(h.journalDir, e.At), e)
}

// writeTurn writes a turn row: the exec entry (only for an ok entry).
func (d *Drainer) writeTurn(h home, ent Entry, p turnPayload) error {
	when := at(ent.TurnAt)
	if ent.Kind == KindTurn && ent.State == StateOK && strings.TrimSpace(ent.Entry) != "" { // a refresh's line is not a turn's account
		kind := journal.KindExec
		if ent.ThingDone {
			kind = journal.KindExecDone
		}
		if err := h.put(journal.Entry{ID: fmt.Sprintf("turn:%d", ent.ID), Kind: kind, At: when, Text: strings.TrimSpace(ent.Entry)}); err != nil {
			return err
		}
	}
	return nil
}

// writeJournal writes a journal row: the events.
func (d *Drainer) writeJournal(h home, ent Entry, p journalPayload) error {
	when := at(ent.TurnAt)
	for i, ev := range p.Events {
		e := journal.Entry{ID: fmt.Sprintf("ev:%d:%d", ent.ID, i), Kind: journal.Kind(ev.Kind), By: journal.By(ev.By), At: when, Actor: ev.Actor, Text: ev.Text, Detail: ev.Detail}
		for _, it := range ev.Items {
			e.Items = append(e.Items, journal.Item{Text: it})
		}
		e.Pinned = e.Kind == journal.KindConfirm
		if err := h.put(e); err != nil {
			return err
		}
	}
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

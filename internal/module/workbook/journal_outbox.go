package workbook

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// Outbox row kinds. A `turn` row is written in the transaction that applies a turn job's result (the todo changes of
// that transaction); a `journal` row in the transaction that records a journal job's result (events, ticks, roadmap).
const (
	OutboxTurn    = "turn"
	OutboxJournal = "journal"
)

// Unit kinds of wb_journal_units.
const (
	UnitTeam = "team"
	UnitSolo = "solo"
)

// EnableJournalOutbox turns the outbox on: from now on the result transactions of lead and solo entries also insert
// their outbox rows. NewEngine does it when a journal source is wired; with none, nothing here ever runs and the
// store behaves exactly as before.
func (s *Store) EnableJournalOutbox() { s.journalOn.Store(true) }

// UnitKeyOf is the journal unit a turn belongs to: its team while it leads one, else its own conversation. ok is false
// for a seat that writes no journal (a member, a remote member, an unrecorded seat).
func UnitKeyOf(role, teamID, convKey string) (key, kind string, ok bool) {
	switch role {
	case team.SeatLead:
		if teamID != "" {
			return UnitTeam + ":" + teamID, UnitTeam, true
		}
		return UnitSolo + ":" + convKey, UnitSolo, true
	case team.SeatNone:
		return UnitSolo + ":" + convKey, UnitSolo, true
	}
	return "", "", false
}

// turnPayload is a `turn` row: the todos the transaction changed (state open = added, done / dropped = closed).
type turnPayload struct {
	Todos []payloadTodo `json:"todos,omitempty"`
}

type payloadTodo struct {
	ID    int64  `json:"id"`
	Seq   int64  `json:"seq"`
	Title string `json:"title"`
	State string `json:"state"`
}

// journalPayload is a `journal` row: a validated journal result, ready to be written.
type journalPayload struct {
	Ver     int              `json:"ver"`              // JournalPromptVersion
	Person  string           `json:"person,omitempty"` // the person_name the job ran with
	Events  []payloadEvent   `json:"events,omitempty"`
	Ticks   []payloadTick    `json:"ticks,omitempty"`
	Roadmap []payloadRoadmap `json:"roadmap,omitempty"`
}

type payloadEvent struct {
	Kind   string   `json:"kind"`
	By     string   `json:"by,omitempty"`
	Actor  string   `json:"actor,omitempty"`
	Text   string   `json:"text"`
	Items  []string `json:"items,omitempty"`
	Detail string   `json:"detail,omitempty"`
}

type payloadTick struct {
	Entry string `json:"entry"`
	Idx   int    `json:"idx"`
}

type payloadRoadmap struct {
	Feature string `json:"feature"`
	Stage   string `json:"stage"`
	Status  string `json:"status"`
}

// outboxEntry is what an outbox insert needs to know about a turn or refresh entry (a refresh changes todos too).
func outboxEntry(tx execer, entryID int64) (conv, unitKey string, ok bool, err error) {
	var role, teamID string
	err = tx.QueryRow(`SELECT conv_key, COALESCE(role, ''), COALESCE(team_id, '') FROM wb_entries WHERE id = ? AND kind IN ('turn', 'refresh')`, entryID).
		Scan(&conv, &role, &teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("read workbook entry %d: %w", entryID, err)
	}
	unitKey, _, ok = UnitKeyOf(role, teamID, conv)
	return conv, unitKey, ok, nil
}

func insertOutbox(tx execer, entryID int64, unitKey, kind string, payload any, now int64) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("journal outbox: %w", err)
	}
	// One row per (entry, kind): a repeat of the same result is not a second row.
	if _, err := tx.Exec(`INSERT INTO wb_journal_outbox (entry_id, unit_key, kind, payload, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (entry_id, kind) DO NOTHING`, entryID, unitKey, kind, string(b), now); err != nil {
		return fmt.Errorf("journal outbox: %w", err)
	}
	return nil
}

// turnOutbox is called inside the transaction of SetPushLineV2 / FinishSkippedV2 / FinishRefresh (the last only when it
// changed a todo), after the todo changes: a lead's or
// a solo session's entry gets its `turn` row there, or the whole transaction fails and nothing of it is kept.
func (s *Store) turnOutbox(tx execer, entryID int64, res TodoResult, now int64) error {
	if !s.journalOn.Load() {
		return nil
	}
	_, unitKey, ok, err := outboxEntry(tx, entryID)
	if err != nil || !ok {
		return err
	}
	var p turnPayload
	for _, t := range res.Changed {
		p.Todos = append(p.Todos, payloadTodo{ID: t.ID, Seq: t.Seq, Title: t.Title, State: t.State})
	}
	return insertOutbox(tx, entryID, unitKey, OutboxTurn, p, now)
}

// RecordJournalResult stores a journal job's validated result as one `journal` outbox row, in one transaction. A
// result with nothing in it, an entry that writes no journal, and a result recorded twice for one entry store nothing.
// With the outbox off it does nothing.
func (s *Store) RecordJournalResult(out JournalOutcome) error {
	if !s.journalOn.Load() {
		return nil
	}
	p := journalPayload{Ver: out.PromptVer, Person: out.Person}
	for _, ev := range out.Result.Events {
		p.Events = append(p.Events, payloadEvent{Kind: string(ev.Kind), By: string(ev.By), Actor: ev.Actor, Text: ev.Text, Items: ev.Items, Detail: ev.Detail})
	}
	for _, t := range out.Result.Ticks {
		p.Ticks = append(p.Ticks, payloadTick{Entry: t.EntryID, Idx: t.ItemIndex})
	}
	for _, r := range out.Result.Roadmap {
		p.Roadmap = append(p.Roadmap, payloadRoadmap{Feature: r.Feature, Stage: r.Stage, Status: r.Status})
	}
	if len(p.Events) == 0 && len(p.Ticks) == 0 && len(p.Roadmap) == 0 {
		return nil
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	now := s.now()
	return s.inTx(func(tx execer) error {
		_, unitKey, ok, err := outboxEntry(tx, out.EntryID)
		if err != nil || !ok {
			return err
		}
		return insertOutbox(tx, out.EntryID, unitKey, OutboxJournal, p, now)
	})
}

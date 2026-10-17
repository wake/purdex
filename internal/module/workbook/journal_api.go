package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/team"
)

// The journal API (plan J-1e, capability journal.v1): a session asks for its unit's entries, tasks and roadmap, and checks an
// entry. The daemon decides the unit from the session's seat, so the App only ever sends the session of the tab it shows.

// journalChangedType is the host event sent after a write to a unit's files.
const journalChangedType = "journal.changed"

// JournalChange says what a write changed in a unit's files: What is one of "entries", "tasks" or "roadmap".
type JournalChange struct {
	UnitKey, ProjectID, What string
}

type journalUnitJSON struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

type journalProjectJSON struct {
	Name      string `json:"name"`
	RemoteURL string `json:"remote_url,omitempty"`
}

type journalItemJSON struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// journalEntryJSON is an entry as the App's strict parser reads it: actor and text are always strings, the lists are never
// null, the time is in milliseconds.
type journalEntryJSON struct {
	ID     string            `json:"id"`
	Kind   string            `json:"kind"`
	By     string            `json:"by,omitempty"`
	At     int64             `json:"at"`
	Actor  string            `json:"actor"`
	Text   string            `json:"text"`
	Refs   []string          `json:"refs"`
	Items  []journalItemJSON `json:"items"`
	Detail string            `json:"detail,omitempty"`
}

type journalPageJSON struct {
	Unit         journalUnitJSON    `json:"unit"`
	Project      journalProjectJSON `json:"project"`
	PersonName   string             `json:"person_name"`
	Entries      []journalEntryJSON `json:"entries"`
	Confirms     []journalEntryJSON `json:"confirms"`
	More         bool               `json:"more"`
	OpenConfirms int                `json:"open_confirms"`
}

type journalTaskJSON struct {
	Number string   `json:"number"`
	Title  string   `json:"title"`
	State  string   `json:"state"`
	Owner  string   `json:"owner,omitempty"`
	Refs   []string `json:"refs"`
}

type journalStageJSON struct {
	Stage       string `json:"stage"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type journalFeatureJSON struct {
	Name   string             `json:"name"`
	Stages []journalStageJSON `json:"stages"`
}

type journalChangedJSON struct {
	Unit    journalUnitRefJSON `json:"unit"`
	Project string             `json:"project"`
	What    string             `json:"what"`
}

type journalUnitRefJSON struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func entryWireJournal(e journal.Entry) journalEntryJSON {
	out := journalEntryJSON{ID: e.ID, Kind: string(e.Kind), By: string(e.By), At: e.At.UnixMilli(), Actor: e.Actor, Text: e.Text,
		Refs: []string{}, Items: []journalItemJSON{}, Detail: e.Detail}
	out.Refs = append(out.Refs, e.Refs...)
	for _, it := range e.Items {
		out.Items = append(out.Items, journalItemJSON{Text: it.Text, Done: it.Done})
	}
	return out
}

func entriesWire(es []journal.Entry) []journalEntryJSON {
	out := make([]journalEntryJSON, 0, len(es))
	for _, e := range es {
		out = append(out, entryWireJournal(e))
	}
	return out
}

// journalTarget is the unit a request is about.
type journalTarget struct {
	unit     UnitRow
	kind, id string
}

// seatOf is the session's seat; a daemon without the team module has everyone in no team.
func (m *Module) seatOf(sessionID string) (team.Seat, error) {
	if m.core == nil || m.core.Registry == nil {
		return team.Seat{Role: team.SeatNone}, nil
	}
	svc, ok := m.core.Registry.Get(team.SeatReaderKey)
	if !ok {
		return team.Seat{Role: team.SeatNone}, nil
	}
	r, ok := svc.(team.SeatReader)
	if !ok {
		return team.Seat{Role: team.SeatNone}, nil
	}
	return r.SeatOf(sessionID)
}

// resolveJournal finds the unit a session reads and answers the request itself when there is none: 400 without a session, 503
// while the module is off or the journal is switched off ([journal] enabled = false), 409 (with the lead's host and session) for a member of a team led on another host, 404 when the
// unit has not written anything yet. A lead or a member of a live team reads its team's unit (which lives in the lead's
// project, wherever the member works); anyone else reads the unit of its own conversation.
func (m *Module) resolveJournal(w http.ResponseWriter, sessionID string) (journalTarget, bool) {
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request")
		return journalTarget{}, false
	}
	st := m.live()
	if st == nil || m.journalDisabled() { // module off, or [journal] enabled = false
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return journalTarget{}, false
	}
	seat, err := m.seatOf(sessionID)
	if err != nil {
		log.Printf("[workbook] journal: seat of %s: %v", sessionID, err)
		writeError(w, http.StatusInternalServerError, "internal")
		return journalTarget{}, false
	}
	key := ""
	switch seat.Role {
	case team.SeatMemberRemote:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "remote_member", "lead_host_id": seat.LeadHostID, "lead_session_id": seat.LeadSessionID})
		return journalTarget{}, false
	case team.SeatLead, team.SeatMember:
		if seat.TeamID != "" {
			key = UnitTeam + ":" + seat.TeamID
		}
	}
	if key == "" {
		conv, err := m.convKeyOf(sessionID)
		if err != nil {
			log.Printf("[workbook] journal: conversation of %s: %v", sessionID, err)
			writeError(w, http.StatusInternalServerError, "internal")
			return journalTarget{}, false
		}
		key = UnitSolo + ":" + conv
	}
	unit, ok, err := st.Unit(key)
	if err != nil {
		log.Printf("[workbook] journal: unit %s: %v", key, err)
		writeError(w, http.StatusInternalServerError, "internal")
		return journalTarget{}, false
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no_journal")
		return journalTarget{}, false
	}
	if err := unitHome(unit).verify(); err != nil { // a folder of the unit's home that is a symlink would send a read elsewhere
		log.Printf("[workbook] journal: unit %s: %v", key, err)
		writeError(w, http.StatusInternalServerError, "internal")
		return journalTarget{}, false
	}
	kind, id, _ := strings.Cut(key, ":")
	return journalTarget{unit: unit, kind: kind, id: id}, true
}

// projectName is the project's name for the header: its repository's folder, else the unit's label.
func (m *Module) projectName(ctx context.Context, u UnitRow) string {
	if m.projectNameFn != nil {
		return m.projectNameFn(ctx, u)
	}
	if p, err := journal.ProjectOf(ctx, u.WtPath); err == nil && p.CommonDir != "" {
		if b := filepath.Base(p.Root); b != "" && b != "." && b != string(filepath.Separator) {
			return b
		}
	}
	return u.Label
}

// remoteURL is the https link of the project's origin ("" when there is none).
func (m *Module) remoteURL(ctx context.Context, dir string) string {
	if m.remoteURLFn != nil {
		return m.remoteURLFn(ctx, dir)
	}
	return journal.RemoteURL(ctx, dir)
}

func (m *Module) personName() string {
	if m.core == nil || m.core.Cfg == nil {
		return journal.DefaultPersonName
	}
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return m.core.Cfg.JournalPersonName()
}

// logSkipped says once per file and reason that a month file was left out of a unit; the page loads again on every change.
func (m *Module) logSkipped(unitKey string, skipped []journal.Skipped) {
	m.skipMu.Lock()
	defer m.skipMu.Unlock()
	if m.skipLogged == nil || len(m.skipLogged) > 256 {
		m.skipLogged = map[string]bool{}
	}
	for _, s := range skipped {
		k := unitKey + "/" + s.Name + ": " + s.Reason
		if !m.skipLogged[k] {
			m.skipLogged[k] = true
			log.Printf("[workbook] journal %s: month file %s left out: %s", unitKey, s.Name, s.Reason)
		}
	}
}

// handleJournal: GET /api/journal?session_id=&before=&limit= → the newest `limit` entries before the cursor (oldest first by
// (at, id)), the open 待確認 entries of the whole unit (confirms), whether there are older entries, and the number of open questions of the whole unit.
func (m *Module) handleJournal(w http.ResponseWriter, r *http.Request) {
	tgt, ok := m.resolveJournal(w, r.URL.Query().Get("session_id"))
	if !ok {
		return
	}
	limit, ok := intQuery(w, r, "limit", journal.DefaultPageLimit, 1, journal.MaxPageLimit)
	if !ok {
		return
	}
	all, skipped, err := journal.ReadUnit(unitHome(tgt.unit).journalDir)
	if err != nil {
		log.Printf("[workbook] journal: read %s: %v", tgt.unit.Key, err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	m.logSkipped(tgt.unit.Key, skipped)
	page, err := journal.PageBefore(all, r.URL.Query().Get("before"), int(limit))
	if errors.Is(err, journal.ErrBadCursor) {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err != nil {
		log.Printf("[workbook] journal: page %s: %v", tgt.unit.Key, err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	ctx := r.Context()
	writeJSON(w, http.StatusOK, journalPageJSON{
		Unit:         journalUnitJSON{Kind: tgt.kind, ID: tgt.id, Label: tgt.unit.Label},
		Project:      journalProjectJSON{Name: m.projectName(ctx, tgt.unit), RemoteURL: m.remoteURL(ctx, tgt.unit.WtPath)},
		PersonName:   m.personName(),
		Entries:      entriesWire(page.Entries),
		Confirms:     entriesWire(journal.Confirms(all)),
		More:         page.More,
		OpenConfirms: journal.OpenConfirmItems(all),
	})
}

// sideFile reads tasks.md or roadmap.md of a unit with the guard of a month file; a file that is not there, or that the guard
// refuses, is no content (logged when it is the latter).
func (m *Module) sideFile(unitKey, path string) (string, bool) {
	text, err := journal.ReadFileBounded(path, journal.MaxSideFileBytes)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.logSkipped(unitKey, []journal.Skipped{{Name: filepath.Base(path), Reason: err.Error()}})
		}
		return "", false
	}
	return text, true
}

// taskState is the App's word for a task's state: a task not yet done is open whether it is started or not.
func taskState(s journal.TaskState) string {
	switch s {
	case journal.TaskDone:
		return "done"
	case journal.TaskDropped:
		return "dropped"
	}
	return "open"
}

// handleJournalTasks: GET /api/journal/tasks?session_id= → {tasks: […]}, the unit's tasks file in file order.
func (m *Module) handleJournalTasks(w http.ResponseWriter, r *http.Request) {
	tgt, ok := m.resolveJournal(w, r.URL.Query().Get("session_id"))
	if !ok {
		return
	}
	out := []journalTaskJSON{}
	path := unitHome(tgt.unit).tasksFile
	if text, ok := m.sideFile(tgt.unit.Key, path); ok {
		for _, t := range journal.ParseTasksText(path, text) {
			out = append(out, journalTaskJSON{Number: strings.TrimPrefix(t.Key, "#"), Title: t.Title, State: taskState(t.State),
				Owner: t.Owner, Refs: append([]string{}, t.Refs...)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

// handleJournalRoadmap: GET /api/journal/roadmap?session_id= → {features: […]}, the project's roadmap.md.
func (m *Module) handleJournalRoadmap(w http.ResponseWriter, r *http.Request) {
	tgt, ok := m.resolveJournal(w, r.URL.Query().Get("session_id"))
	if !ok {
		return
	}
	out := []journalFeatureJSON{}
	if text, ok := m.sideFile(tgt.unit.Key, unitHome(tgt.unit).roadmapFile); ok {
		features, err := journal.ParseRoadmapText(text)
		if err != nil {
			m.logSkipped(tgt.unit.Key, []journal.Skipped{{Name: "roadmap.md", Reason: err.Error()}})
		}
		for _, f := range features {
			jf := journalFeatureJSON{Name: f.Name, Stages: []journalStageJSON{}}
			for _, s := range f.Stages {
				jf.Stages = append(jf.Stages, journalStageJSON{Stage: s.Name, Description: s.Desc, Status: s.Status})
			}
			out = append(out, jf)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"features": out})
}

type journalConfirmBody struct {
	SessionID string `json:"session_id"`
	EntryID   string `json:"entry_id"`
	Idx       *int   `json:"idx"`
	Done      *bool  `json:"done"`
}

// handleJournalConfirm: PUT /api/journal/confirm {session_id, entry_id, idx, done} → 204. A person checks or unchecks item idx
// of a 待確認 entry; the item is marked manual so Haiku never changes it. The entry is found in whichever month file holds it;
// journal.changed (entries) is sent only if the file changed. 404: no such entry or item; 400: the entry is not a confirm.
func (m *Module) handleJournalConfirm(w http.ResponseWriter, r *http.Request) {
	var body journalConfirmBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.EntryID == "" || body.Idx == nil || body.Done == nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	tgt, ok := m.resolveJournal(w, body.SessionID)
	if !ok {
		return
	}
	changed, err := journal.SetItemIn(unitHome(tgt.unit).journalDir, body.EntryID, *body.Idx, *body.Done)
	switch {
	case errors.Is(err, journal.ErrNotFound), errors.Is(err, journal.ErrItemRange):
		writeError(w, http.StatusNotFound, "not_found")
		return
	case errors.Is(err, journal.ErrNotConfirm):
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	case err != nil:
		log.Printf("[workbook] journal: confirm %s in %s: %v", body.EntryID, tgt.unit.Key, err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	if changed {
		m.announceJournal(JournalChange{UnitKey: tgt.unit.Key, ProjectID: tgt.unit.ProjectID, What: "entries"})
	}
	w.WriteHeader(http.StatusNoContent)
}

// announceJournal sends journal.changed for a write to a unit's files: the unit as {kind, id}, its project, and what changed.
func (m *Module) announceJournal(c JournalChange) {
	m.mu.Lock()
	send := m.broadcast
	m.mu.Unlock()
	if send == nil {
		return
	}
	kind, id, _ := strings.Cut(c.UnitKey, ":")
	b, err := json.Marshal(journalChangedJSON{Unit: journalUnitRefJSON{Kind: kind, ID: id}, Project: c.ProjectID, What: c.What})
	if err != nil {
		log.Printf("[workbook] journal event: %v", err)
		return
	}
	send(journalChangedType, string(b))
}

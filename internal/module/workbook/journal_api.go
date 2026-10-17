package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	// Pinned: TRANSITION ONLY. True for the entries of confirms[], false for the rest, for the SPA that still reads the
	// pinned band. Removed by a PR of its own once the SPA's JV-3 is out.
	Pinned bool `json:"pinned"`
}

type journalPageJSON struct {
	Unit       journalUnitJSON    `json:"unit"`
	Project    journalProjectJSON `json:"project"`
	PersonName string             `json:"person_name"`
	Entries    []journalEntryJSON `json:"entries"`
	Confirms   []journalEntryJSON `json:"confirms"`
	// Pinned: TRANSITION ONLY, the same entries as Confirms, for the SPA before JV-3 (it reads pinned[] as its 待確認 band).
	// Removed by a PR of its own once JV-3 is out.
	Pinned       []journalEntryJSON `json:"pinned"`
	More         bool               `json:"more"`
	OpenConfirms int                `json:"open_confirms"`
	// Page and Truncated are set on a session page only (journal.sessions.v1): the leader session the entries belong to, and
	// whether the page held more than the cap (the newest are kept).
	Page      string `json:"page,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
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

// confirmsWire is the entries of confirms[] (and, in the transition, pinned[]): every one flagged pinned.
func confirmsWire(es []journal.Entry) []journalEntryJSON {
	out := entriesWire(es)
	for i := range out {
		out[i].Pinned = true
	}
	return out
}

// journalTarget is the unit a request is about.
type journalTarget struct {
	unit     UnitRow
	kind, id string
	role     string // the reader's seat role
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
	return journalTarget{unit: unit, kind: kind, id: id, role: seat.Role}, true
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
	q := r.URL.Query()
	// A session page (journal.sessions.v1): `page` names the leader session; a request with neither `page` nor the older
	// Apps' `before` / `limit` is the current session. The older paging below stays for the Apps that send them.
	sessionPage := q.Has("page") || (!q.Has("before") && !q.Has("limit"))
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
	if sessionPage {
		m.writeSessionPage(w, r, tgt, all)
		return
	}
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
	writeJSON(w, http.StatusOK, m.pageJSON(r, tgt, all, page.Entries, page.More))
}

// pageJSON is the answer for a window of the unit's entries: the window, and the whole unit's confirms and open_confirms.
func (m *Module) pageJSON(r *http.Request, tgt journalTarget, all, window []journal.Entry, more bool) journalPageJSON {
	ctx := r.Context()
	confirms := confirmsWire(journal.Confirms(all))
	inBand := map[string]bool{}
	for _, c := range confirms {
		inBand[c.ID] = true
	}
	pageEntries := entriesWire(window)
	for i := range pageEntries {
		pageEntries[i].Pinned = inBand[pageEntries[i].ID]
	}
	return journalPageJSON{
		Unit:         journalUnitJSON{Kind: tgt.kind, ID: tgt.id, Label: tgt.unit.Label},
		Project:      journalProjectJSON{Name: m.projectName(ctx, tgt.unit), RemoteURL: m.remoteURL(ctx, tgt.unit.WtPath)},
		PersonName:   m.personName(),
		Entries:      pageEntries,
		Confirms:     confirms,
		Pinned:       confirms,
		More:         more,
		OpenConfirms: journal.OpenConfirmItems(all),
	}
}

// writeSessionPage answers GET /api/journal?session_id=&page=<leader session>: that session's entries oldest first (at most
// pageEntryCap, the newest kept, with truncated), and the whole unit's confirms. No page: the leader's current session; a
// page that is not in the leader's chain is 404.
func (m *Module) writeSessionPage(w http.ResponseWriter, r *http.Request, tgt journalTarget, all []journal.Entry) {
	st := m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	reader := r.URL.Query().Get("session_id")
	leader, err := m.leaderSession(tgt, reader)
	if err != nil {
		log.Printf("[workbook] journal: leader of %s: %v", tgt.unit.Key, err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable", "detail": err.Error()})
		return
	}
	idx := newSessionIndex(m.sessionChain(leader))
	want := len(idx.chain) - 1
	if page := r.URL.Query().Get("page"); page != "" {
		p, found := idx.pos[page]
		if !found {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		want = p
	}
	owner, err := attribute(st, idx, all)
	if err != nil {
		log.Printf("[workbook] journal: attribute %s: %v", tgt.unit.Key, err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	var window []journal.Entry
	for i, e := range all {
		if owner[i] == want {
			window = append(window, e)
		}
	}
	truncated := len(window) > pageEntryCap
	if truncated {
		window = window[len(window)-pageEntryCap:]
	}
	out := m.pageJSON(r, tgt, all, window, false)
	out.Page, out.Truncated = idx.chain[want].sid, truncated
	writeJSON(w, http.StatusOK, out)
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
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	// Exactly one object: a second Decode must find the end (trailing tokens, garbage or a tail past the limit are a 400).
	if err := dec.Decode(&body); err != nil || dec.Decode(&struct{}{}) != io.EOF || body.EntryID == "" || body.Idx == nil || body.Done == nil {
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

// handleJournalPinGone: PUT /api/journal/pin -> 410. Pinning was removed (journal v5); the text is what an App from before
// that shows, instead of a 404. TRANSITION ONLY: removed with pinned[] once the SPA's JV-3 is out.
func (m *Module) handleJournalPinGone(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusGone, map[string]string{"error": "pin_removed", "detail": "釘選已移除，待確認會自動列在最上面"})
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

package workbook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/team"
)

// journalSeats is the team module's seat reader: a map from session to seat; absent = none.
type journalSeats struct {
	mu    sync.Mutex
	seats map[string]team.Seat
	err   error
}

func (f *journalSeats) SeatOf(sid string) (team.Seat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return team.Seat{}, f.err
	}
	if s, ok := f.seats[sid]; ok {
		return s, nil
	}
	return team.Seat{Role: team.SeatNone}, nil
}

type journalEnv struct {
	*apiEnv
	wt    string
	seats *journalSeats
}

// newJournalAPI is the workbook API test environment plus a seat reader, the person's name and fixed answers for the
// project's name and remote (a test that wants the real ones clears the two functions).
func newJournalAPI(t *testing.T) *journalEnv {
	t.Helper()
	e := newAPI(t)
	seats := &journalSeats{seats: map[string]team.Seat{}}
	e.m.core.Registry.Register(team.SeatReaderKey, team.SeatReader(seats))
	e.m.core.Cfg.Journal = &config.JournalConfig{PersonName: "Wake"}
	e.m.remoteURLFn = func(context.Context, string) string { return "https://github.com/wake/purdex" }
	e.m.projectNameFn = func(context.Context, UnitRow) string { return "purdex" }
	return &journalEnv{apiEnv: e, wt: t.TempDir(), seats: seats}
}

func (j *journalEnv) unit(key, label string) UnitRow {
	j.t.Helper()
	kind := UnitSolo
	if strings.HasPrefix(key, UnitTeam+":") {
		kind = UnitTeam
	}
	u, err := j.store.RegisterUnit(UnitRow{Key: key, Kind: kind, ProjectID: "p1", WtPath: j.wt, Label: label})
	if err != nil {
		j.t.Fatal(err)
	}
	return u
}

// write puts entries into the unit's month files in the order given, as a drainer would.
func (j *journalEnv) write(u UnitRow, es ...journal.Entry) {
	j.t.Helper()
	for _, e := range es {
		if err := journal.Upsert(journal.MonthFile(unitHome(u).journalDir, e.At), e); err != nil {
			j.t.Fatal(err)
		}
	}
}

func (j *journalEnv) request(method, url string, body any) (int, []byte) {
	j.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			j.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	w := httptest.NewRecorder()
	j.mux.ServeHTTP(w, httptest.NewRequest(method, url, rd))
	return w.Code, w.Body.Bytes()
}

type jItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type jEntry struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	By     string   `json:"by"`
	At     int64    `json:"at"`
	Actor  string   `json:"actor"`
	Text   string   `json:"text"`
	Refs   []string `json:"refs"`
	Items  []jItem  `json:"items"`
	Detail string   `json:"detail"`
	Pinned bool     `json:"pinned"`
}

type jPage struct {
	Unit struct {
		Kind  string `json:"kind"`
		ID    string `json:"id"`
		Label string `json:"label"`
	} `json:"unit"`
	Project struct {
		Name      string `json:"name"`
		RemoteURL string `json:"remote_url"`
	} `json:"project"`
	PersonName   string   `json:"person_name"`
	Entries      []jEntry `json:"entries"`
	Pinned       []jEntry `json:"pinned"`
	Confirms     []jEntry `json:"confirms"`
	More         bool     `json:"more"`
	OpenConfirms int      `json:"open_confirms"`
}

func (j *journalEnv) page(url string) (int, jPage, string) {
	j.t.Helper()
	code, body := j.request(http.MethodGet, url, nil)
	var p jPage
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &p); err != nil {
			j.t.Fatalf("GET %s: %v\n%s", url, err, body)
		}
	}
	return code, p, string(body)
}

func ids(es []jEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func tm(month time.Month, day, hour int) time.Time {
	return time.Date(2026, month, day, hour, 0, 0, 0, time.UTC)
}

// A solo session reads its own conversation's journal: every entry oldest first by (at, id) whatever order the files hold
// them in, with the wire shape the App parses strictly (times in milliseconds, lists that are never null, the unit, the
// project, the person's name), the open 待確認 entries (confirms) newest first and the number of open questions.
// Mutation gate: the order the files hold -> the late-written early entry is last (red); at in seconds -> the number is wrong
// (red); a nil list left null -> the App refuses the row (red); the unit's id is the key with its prefix (red).
func TestJournalAPI_ASoloSessionReadsItsConversation(t *testing.T) {
	j := newJournalAPI(t)
	j.roots.root["s2"] = "conv-1" // the session is a later link of the conversation
	u := j.unit("solo:conv-1", "專案 A")
	j.write(u,
		journal.Entry{ID: "late", Kind: journal.KindExec, At: tm(time.November, 3, 10), Actor: "Kenji", Text: "written first, happened last", Refs: []string{"#2829"}},
		journal.Entry{ID: "early", Kind: journal.KindRequest, By: journal.ByUser, At: tm(time.November, 3, 9), Actor: "Wake → Kenji", Text: "written second"},
		journal.Entry{ID: "ask", Kind: journal.KindConfirm, At: tm(time.October, 30, 8), Actor: "Kenji → Wake", Text: "要不要",
			Items: []journal.Item{{Text: "甲?"}, {Text: "乙?", Done: true}, {Text: "丙?"}}, Detail: "原文"},
	)
	code, p, raw := j.page("/api/journal?session_id=s2")
	if code != http.StatusOK {
		t.Fatalf("code %d: %s", code, raw)
	}
	if want := []string{"ask", "early", "late"}; !slices.Equal(ids(p.Entries), want) {
		t.Fatalf("entries = %v, want %v", ids(p.Entries), want)
	}
	if p.Unit.Kind != "solo" || p.Unit.ID != "conv-1" || p.Unit.Label != "專案 A" {
		t.Errorf("unit = %+v", p.Unit)
	}
	if p.Project.Name != "purdex" || p.Project.RemoteURL != "https://github.com/wake/purdex" || p.PersonName != "Wake" {
		t.Errorf("project = %+v, person_name = %q", p.Project, p.PersonName)
	}
	if p.More || p.OpenConfirms != 2 {
		t.Errorf("more = %v, open_confirms = %d, want false and 2", p.More, p.OpenConfirms)
	}
	if !slices.Equal(ids(p.Confirms), []string{"ask"}) {
		t.Errorf("confirms = %v", ids(p.Confirms))
	}
	// the confirms band and the open questions cover the whole unit, not the page: a page of one entry still carries both
	_, one, _ := j.page("/api/journal?session_id=s2&limit=1")
	if !slices.Equal(ids(one.Entries), []string{"late"}) || !one.More || !slices.Equal(ids(one.Confirms), []string{"ask"}) || one.OpenConfirms != 2 {
		t.Errorf("a page of one: entries %v more=%v confirms %v open_confirms=%d", ids(one.Entries), one.More, ids(one.Confirms), one.OpenConfirms)
	}
	var late jEntry
	for _, e := range p.Entries {
		if e.ID == "late" {
			late = e
		}
	}
	if late.At != tm(time.November, 3, 10).UnixMilli() || late.Kind != "exec" || late.Actor != "Kenji" || !slices.Equal(late.Refs, []string{"#2829"}) {
		t.Errorf("late = %+v", late)
	}
	for _, e := range p.Entries {
		if e.ID == "ask" && (len(e.Items) != 3 || !e.Items[1].Done || e.Items[0].Done || e.Detail != "原文") {
			t.Errorf("ask = %+v", e)
		}
	}
	// the strict parser of the App refuses null where it expects a list, and a missing actor or text
	for _, must := range []string{`"refs":[]`, `"items":[]`} {
		if !strings.Contains(raw, must) {
			t.Errorf("the answer lacks %s:\n%s", must, raw)
		}
	}
	if strings.Contains(raw, `null`) {
		t.Errorf("the answer has a null:\n%s", raw)
	}
}

// An entry with no actor and no text (a task_done whose closing line has not come) still carries both, as empty strings: the
// App refuses a row where either is missing.
// Mutation gate: omit empty actor / text -> the App drops the row (red).
func TestJournalAPI_AnEntryWithoutActorOrTextStillHasBoth(t *testing.T) {
	j := newJournalAPI(t)
	u := j.unit("solo:s1", "專案 A")
	j.write(u, journal.Entry{ID: "bare", Kind: journal.KindTaskDone, At: tm(time.November, 3, 9)})
	_, p, raw := j.page("/api/journal?session_id=s1")
	if len(p.Entries) != 1 || !strings.Contains(raw, `"actor":""`) || !strings.Contains(raw, `"text":""`) {
		t.Fatalf("entries %v\n%s", ids(p.Entries), raw)
	}
}

// The person's name is the configured one, or the default when there is none: the App highlights it wherever it appears.
// Mutation gate: a name written into the code (red); the default missing (red).
func TestJournalAPI_PersonNameFollowsTheConfig(t *testing.T) {
	j := newJournalAPI(t)
	j.unit("solo:s1", "專案 A")
	j.m.core.Cfg.Journal = &config.JournalConfig{PersonName: "Hana"}
	if _, p, _ := j.page("/api/journal?session_id=s1"); p.PersonName != "Hana" {
		t.Errorf("person_name = %q, want Hana", p.PersonName)
	}
	j.m.core.Cfg.Journal = nil
	if _, p, _ := j.page("/api/journal?session_id=s1"); p.PersonName != "使用者" {
		t.Errorf("person_name = %q, want the default", p.PersonName)
	}
}

// A unit with no entries yet answers empty lists, not null.
func TestJournalAPI_AnEmptyUnitAnswersEmptyLists(t *testing.T) {
	j := newJournalAPI(t)
	j.unit("solo:s1", "專案 A")
	code, p, raw := j.page("/api/journal?session_id=s1")
	if code != http.StatusOK || len(p.Entries) != 0 || len(p.Confirms) != 0 || p.More || p.OpenConfirms != 0 {
		t.Fatalf("code %d, page %+v", code, p)
	}
	for _, must := range []string{`"entries":[]`, `"confirms":[]`} {
		if !strings.Contains(raw, must) {
			t.Errorf("the answer lacks %s:\n%s", must, raw)
		}
	}
}

// Pages are cut at the entry the cursor names, in the (at, id) order: ids and times run opposite ways here, so a cut by id
// alone would lose or repeat entries. `more` says there is something older; a cursor that names nothing, is malformed or has
// a limit out of range is a 400.
// Mutation gate: cut by id alone (red); `more` always false (red); an unknown cursor pages from the end (red).
func TestJournalAPI_PagingFollowsTheOrderNotTheIDs(t *testing.T) {
	j := newJournalAPI(t)
	u := j.unit("solo:s1", "專案 A")
	var es []journal.Entry
	for i, id := range []string{"g", "f", "e", "d", "c", "b", "a"} { // the ids descend while time ascends, across two months
		at := tm(time.October, 30, 10).Add(time.Duration(i) * 12 * time.Hour)
		es = append(es, journal.Entry{ID: id, Kind: journal.KindExec, At: at, Actor: "K", Text: "t"})
	}
	j.write(u, es...)
	_, newest, _ := j.page("/api/journal?session_id=s1&limit=3")
	if !slices.Equal(ids(newest.Entries), []string{"c", "b", "a"}) || !newest.More {
		t.Fatalf("newest page = %v more=%v", ids(newest.Entries), newest.More)
	}
	cursor := journal.CursorOf(journal.Entry{ID: "c", At: tm(time.October, 30, 10).Add(4 * 12 * time.Hour)})
	_, mid, _ := j.page("/api/journal?session_id=s1&limit=3&before=" + cursor)
	if !slices.Equal(ids(mid.Entries), []string{"f", "e", "d"}) || !mid.More {
		t.Fatalf("page before c = %v more=%v", ids(mid.Entries), mid.More)
	}
	cursor = journal.CursorOf(journal.Entry{ID: "f", At: tm(time.October, 30, 10).Add(1 * 12 * time.Hour)})
	_, last, _ := j.page("/api/journal?session_id=s1&limit=3&before=" + cursor)
	if !slices.Equal(ids(last.Entries), []string{"g"}) || last.More {
		t.Fatalf("last page = %v more=%v", ids(last.Entries), last.More)
	}
	for _, bad := range []string{"&before=nonsense", "&before=2026-11:nobody", "&before=2026-13:a", "&limit=0", "&limit=201", "&limit=x"} {
		code, body := j.request(http.MethodGet, "/api/journal?session_id=s1"+bad, nil)
		if code != http.StatusBadRequest || !strings.Contains(string(body), `"bad_request"`) {
			t.Errorf("%s: code %d, body %s", bad, code, body)
		}
	}
}

// Which unit a session reads is decided by the daemon from the session's seat: a member reads its team's journal, which sits
// in the lead's project and not where the member works; a lead of a live team reads the team's too, whichever of the lead's
// sessions asks (a relay keeps the unit); an ended team's lead is no lead any more and reads its own conversation.
// Mutation gate: a member resolves to its own conversation -> 404 (red); a lead resolves to its conversation (red); the unit
// is looked up by the session instead of the team -> a relayed lead finds none (red).
func TestJournalAPI_TheSeatDecidesTheUnit(t *testing.T) {
	j := newJournalAPI(t)
	withLead(j, "t1", "lead-new")
	team1 := j.unit("team:t1", "日誌")
	j.write(team1, journal.Entry{ID: "team-entry", Kind: journal.KindExec, At: tm(time.November, 1, 1), Actor: "A", Text: "t"})
	solo := j.unit("solo:old-lead", "專案 A")
	j.write(solo, journal.Entry{ID: "solo-entry", Kind: journal.KindExec, At: tm(time.October, 1, 1), Actor: "A", Text: "t"})
	j.seats.seats["member-1"] = team.Seat{TeamID: "t1", Role: team.SeatMember}
	j.seats.seats["lead-new"] = team.Seat{TeamID: "t1", Role: team.SeatLead} // the lead after a relay: another session, the same team
	j.roots.root["lead-new"] = "old-lead"
	// "lead-ended" has no seat any more (SeatOf answers none for an ended team) and is the old-lead conversation again
	j.roots.root["lead-ended"] = "old-lead"
	for session, want := range map[string]struct{ unit, entry string }{
		"member-1":   {"team:t1", "team-entry"},
		"lead-new":   {"team:t1", "team-entry"},
		"lead-ended": {"solo:old-lead", "solo-entry"},
	} {
		code, p, raw := j.page("/api/journal?session_id=" + session)
		if code != http.StatusOK {
			t.Errorf("%s: code %d %s", session, code, raw)
			continue
		}
		kind, id, _ := strings.Cut(want.unit, ":")
		if p.Unit.Kind != kind || p.Unit.ID != id || len(p.Entries) != 1 || p.Entries[0].ID != want.entry {
			t.Errorf("%s: unit %+v entries %v, want %s / %s", session, p.Unit, ids(p.Entries), want.unit, want.entry)
		}
	}
}

// A member of a team led on another host is told where to ask, and the same for the other three routes.
// Mutation gate: a 404 instead -> the App cannot follow the lead (red); the lead's session omitted (red).
func TestJournalAPI_ARemoteMemberIsToldWhereTheLeadIs(t *testing.T) {
	j := newJournalAPI(t)
	j.seats.seats["rm"] = team.Seat{TeamID: "t-L", Role: team.SeatMemberRemote, LeadHostID: "host-L", LeadSessionID: "lead-sid"}
	for _, c := range []struct{ method, url string }{
		{http.MethodGet, "/api/journal?session_id=rm"},
		{http.MethodGet, "/api/journal/tasks?session_id=rm"},
		{http.MethodGet, "/api/journal/roadmap?session_id=rm"},
	} {
		code, body := j.request(c.method, c.url, nil)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if code != http.StatusConflict || got["lead_host_id"] != "host-L" || got["lead_session_id"] != "lead-sid" {
			t.Errorf("%s %s: code %d, body %s", c.method, c.url, code, body)
		}
	}
	code, body := j.request(http.MethodPut, "/api/journal/confirm", map[string]any{"session_id": "rm", "entry_id": "x", "idx": 0, "done": true})
	if code != http.StatusConflict || !strings.Contains(string(body), `"lead_host_id":"host-L"`) {
		t.Errorf("confirm: code %d, body %s", code, body)
	}
}

// No unit yet is a 404 (not an empty journal, so the App can tell an older daemon from a quiet unit); a missing session is a 400;
// a store that cannot answer is a 500 and a module that is off is a 503.
func TestJournalAPI_TheErrorsAreTheirOwn(t *testing.T) {
	j := newJournalAPI(t)
	if code, body := j.request(http.MethodGet, "/api/journal?session_id=nobody", nil); code != http.StatusNotFound || !strings.Contains(string(body), `"no_journal"`) {
		t.Errorf("no unit: code %d body %s", code, body)
	}
	if code, body := j.request(http.MethodGet, "/api/journal", nil); code != http.StatusBadRequest || !strings.Contains(string(body), `"bad_request"`) {
		t.Errorf("no session: code %d body %s", code, body)
	}
	j.seats.err = errors.New("team store closed")
	if code, _ := j.request(http.MethodGet, "/api/journal?session_id=x", nil); code != http.StatusInternalServerError {
		t.Errorf("seat error: code %d", code)
	}
	j.seats.err = nil
	j.roots.err = errors.New("lineage unavailable")
	if code, _ := j.request(http.MethodGet, "/api/journal?session_id=x", nil); code != http.StatusInternalServerError {
		t.Errorf("lineage error: code %d", code)
	}
	j.roots.err = nil
	j.m.Stop(context.Background()) // the module is off
	if code, body := j.request(http.MethodGet, "/api/journal?session_id=x", nil); code != http.StatusServiceUnavailable || !strings.Contains(string(body), `"unavailable"`) {
		t.Errorf("module off: code %d body %s", code, body)
	}
}

// The folder holds a month file that must not be read (a symlink): the unit still answers with its other months.
func TestJournalAPI_ASkippedMonthDoesNotStopTheAnswer(t *testing.T) {
	j := newJournalAPI(t)
	u := j.unit("solo:s1", "專案 A")
	j.write(u, journal.Entry{ID: "ok", Kind: journal.KindExec, At: tm(time.November, 1, 1), Actor: "A", Text: "t"})
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := journal.Upsert(elsewhere, journal.Entry{ID: "linked", Kind: journal.KindExec, At: tm(time.October, 1, 1), Text: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(unitHome(u).journalDir, "2026-10.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	code, p, raw := j.page("/api/journal?session_id=s1")
	if code != http.StatusOK || !slices.Equal(ids(p.Entries), []string{"ok"}) {
		t.Fatalf("code %d entries %v\n%s", code, ids(p.Entries), raw)
	}
}

// With nothing replaced, the project's name and link come from the real repository: its folder's name and its origin as https.
func TestJournalAPI_TheProjectComesFromTheRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	j := newJournalAPI(t)
	j.m.remoteURLFn, j.m.projectNameFn = nil, nil // the real ones
	repo := filepath.Join(t.TempDir(), "myrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:wake/myrepo.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	j.wt = repo
	j.unit("solo:s1", "專案 A")
	_, p, raw := j.page("/api/journal?session_id=s1")
	if p.Project.Name != "myrepo" || p.Project.RemoteURL != "https://github.com/wake/myrepo" {
		t.Fatalf("project = %+v\n%s", p.Project, raw)
	}
	// a folder that is no repository: the name falls back to the unit's label and there is no link
	j2 := newJournalAPI(t)
	j2.m.remoteURLFn, j2.m.projectNameFn = nil, nil
	j2.unit("solo:s1", "專案 A")
	_, p2, raw2 := j2.page("/api/journal?session_id=s1")
	if p2.Project.Name != "專案 A" || p2.Project.RemoteURL != "" || strings.Contains(raw2, "remote_url") {
		t.Fatalf("project = %+v\n%s", p2.Project, raw2)
	}
}

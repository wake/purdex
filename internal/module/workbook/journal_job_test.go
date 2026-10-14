package workbook

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/team"
)

type fakeJournal struct {
	files JournalFiles
	ok    bool
}

func (f fakeJournal) UnitFiles(Entry) (JournalFiles, bool, error) { return f.files, f.ok, nil }
func (f fakeJournal) SelfName(sid string) string                  { return "name-" + sid }

const journalAnswer = `{"events":[{"kind":"comm","from":"1f","to":"88","text":"1f 通知已部署。"}],"confirm_done":[],"roadmap":[]}`

const skipAnswer = `{"skip":true,"thing":"","push":"","entry":"","status":"","thing_done":false,"todos":{"done":[],"dropped":[],"add":[]}}`

// journalKit is a kit whose session s1 sits in seat role, with a journal source wired and one ended turn taken in.
func journalKit(t *testing.T, role string, ids ...string) *kit {
	t.Helper()
	k := kitWith(t, "s1")
	k.seats["s1"] = team.Seat{TeamID: "team-9", Role: role}
	k.e.d.Journal = fakeJournal{}
	for i, id := range ids {
		k.turns.set("s1", endedTurn(id, int64(100*(i+1)), "做了 "+id))
		k.event("s1", int64(1000*(i+1)))
	}
	return k
}

func answer(text string) Result {
	return Result{Answered: true, Text: text, Usage: Usage{In: 10, Out: 5}, LatencyMS: 700}
}

func entryState(k *kit, id int64) Entry {
	k.t.Helper()
	e, err := k.st.Entry(id)
	if err != nil {
		k.t.Fatal(err)
	}
	return e
}

// A lead and a solo (none) turn queue one journal job after the turn job; a member, a remote member, an unrecorded seat
// and an engine without a journal source queue none.
// Mutation gate: queue for every seat (drop the unitRoleOf check) -> the member cases get a job (red); drop the nil
// Journal check -> the last case panics or gets a job (red).
func TestJournalJob_OnlyLeadAndSoloQueueOne(t *testing.T) {
	for _, c := range []struct {
		role string
		want bool
	}{{team.SeatLead, true}, {team.SeatNone, true}, {team.SeatMember, false}, {team.SeatMemberRemote, false}, {"", false}} {
		k := journalKit(t, c.role, "a")
		turn := mustNext(t, k, "m", "s1")
		if turn.Kind != JobTurn {
			t.Fatalf("%s: first job kind %s", c.role, turn.Kind)
		}
		k.finish("m", turn, answerJSON("事", "推", "紀錄", "狀態", ""))
		j, ok := k.next("m", "s1")
		if ok != c.want || (ok && j.Kind != JobJournal) {
			t.Errorf("%q: journal job = %v (%+v), want %v", c.role, ok, j, c.want)
		}
		if ok {
			k.finish("m", j, answer(journalAnswer))
			if _, again := k.next("m", "s1"); again {
				t.Errorf("%q: a second job", c.role)
			}
		}
	}
	k := journalKit(t, team.SeatLead, "a")
	k.e.d.Journal = nil
	k.finish("m", mustNext(t, k, "m", "s1"), answerJSON("事", "推", "紀錄", "狀態", ""))
	if _, ok := k.next("m", "s1"); ok {
		t.Error("a journal job without a journal source")
	}
}

// On the wire the job is a turn, with its own system text, a 60 s timeout, haiku / low; the lease keeps the real kind.
// Mutation gate: send kind journal -> red; timeout 30 000 -> red; the turn system prompt -> red.
func TestJournalJob_WireIsATurnAndLeaseKeepsItsKind(t *testing.T) {
	k := journalKit(t, team.SeatLead, "a")
	k.finish("m", mustNext(t, k, "m", "s1"), answerJSON("事", "推", "紀錄", "狀態", ""))
	j := mustNext(t, k, "m", "s1")
	if j.Kind != JobJournal {
		t.Fatalf("kind = %q", j.Kind)
	}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Complete struct {
			Model     string `json:"model"`
			Effort    string `json:"effort"`
			MaxTokens int    `json:"max_tokens"`
			TimeoutMS int    `json:"timeout_ms"`
			System    []struct {
				Text  string `json:"text"`
				Cache bool   `json:"cache"`
			} `json:"system"`
			Prompt string `json:"prompt"`
		} `json:"complete"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	c := wire.Complete
	if wire.Kind != "turn" || c.Model != "haiku" || c.Effort != "low" || c.MaxTokens != 4096 || c.TimeoutMS != 60_000 ||
		len(c.System) != 1 || c.System[0].Text != JournalPrompt || !c.System[0].Cache || !strings.HasPrefix(c.Prompt, `{"unit_role":"leader","self_name":"name-s1"`) {
		t.Fatalf("wire = %s", b)
	}
	if !strings.HasPrefix(JournalPrompt, "你是日誌記錄員") || strings.HasSuffix(JournalPrompt, "\n") || JournalPromptVersion != 1 {
		t.Error("the embedded prompt is not the journal prompt")
	}
	k.e.qmu.Lock()
	l := k.e.leases[j.ID]
	k.e.qmu.Unlock()
	if l == nil || l.job.kind != JobJournal || l.timeoutMS != 60_000 {
		t.Fatalf("lease = %+v", l)
	}
	// its lease outlives a v2 call's: 41 s on, a 30 s lease would be gone, this one is not
	k.clock.Add(41 * time.Second)
	k.e.reap()
	if _, err := k.e.Result("m", j.ID, answer(journalAnswer)); err != nil {
		t.Errorf("the journal lease ran out at the v2 time: %v", err)
	}
}

// The journal job runs after the re-write, and the entry is final (re-written text) by then.
// Mutation gate: drop `then:` from the re-write job -> no journal job (red).
func TestJournalJob_RunsAfterTheRewrite(t *testing.T) {
	k := journalKit(t, team.SeatNone, "a")
	turn := mustNext(t, k, "m", "s1")
	k.finish("m", turn, answerJSON("事", "推", longEntry(), "狀態", ""))
	rw := mustNext(t, k, "m", "s1")
	if rw.Kind != JobRewrite {
		t.Fatalf("kind = %s, want the re-write first", rw.Kind)
	}
	if _, ok := k.next("m", "s1"); ok {
		t.Fatal("a job while the re-write is leased")
	}
	k.finish("m", rw, answer("改寫後的紀錄。"))
	id := k.entries("s1")[0].ID
	if e := entryState(k, id); e.State != StateOK || e.Entry != "改寫後的紀錄。" {
		t.Fatalf("entry at the journal job's start = %+v", e)
	}
	if j := mustNext(t, k, "m", "s1"); j.Kind != JobJournal {
		t.Fatalf("kind = %s", j.Kind)
	}
}

// A re-write that is never run (its lease runs out) still lets the journal job follow.
func TestJournalJob_FollowsALostRewrite(t *testing.T) {
	k := journalKit(t, team.SeatLead, "a")
	k.finish("m", mustNext(t, k, "m", "s1"), answerJSON("事", "推", longEntry(), "狀態", ""))
	rw := mustNext(t, k, "m", "s1")
	k.clock.Add(time.Minute)
	k.e.reap()
	if j, ok := k.next("m", "s1"); !ok || j.Kind != JobJournal {
		t.Fatalf("after a lost re-write: %+v %v", j, ok)
	}
	if _, err := k.e.Result("m", rw.ID, answer("遲到")); err == nil {
		t.Error("the lost re-write took a result")
	}
}

// A skipped turn writes no exec entry but its journal job still runs. A failed turn job has no journal job.
// Mutation gate: no journalAfter on the skip branch -> red; journalAfter on the failure branch -> red.
func TestJournalJob_SkippedTurnStillQueuesOne_FailedTurnDoesNot(t *testing.T) {
	k := journalKit(t, team.SeatLead, "a")
	k.finish("m", mustNext(t, k, "m", "s1"), answer(skipAnswer))
	if j, ok := k.next("m", "s1"); !ok || j.Kind != JobJournal {
		t.Fatalf("after a skip: %+v %v", j, ok)
	}
	k = journalKit(t, team.SeatLead, "a")
	k.finish("m", mustNext(t, k, "m", "s1"), Result{Reason: "api-error", Status: 500, Error: "overloaded"})
	if _, ok := k.next("m", "s1"); ok {
		t.Error("a journal job after a failed turn job")
	}
}

// The journal job holds the conversation's place: the next turn's job waits behind it.
func TestJournalJob_HoldsTheConversationsPlace(t *testing.T) {
	k := journalKit(t, team.SeatLead, "a", "b")
	first := mustNext(t, k, "m", "s1")
	k.finish("m", first, answerJSON("事", "推", "紀錄", "狀態", ""))
	j := mustNext(t, k, "m", "s1")
	if j.Kind != JobJournal {
		t.Fatalf("kind = %s, want the journal job before the second turn", j.Kind)
	}
	k.finish("m", j, answer(journalAnswer))
	if next := mustNext(t, k, "m", "s1"); next.Kind != JobTurn {
		t.Fatalf("kind = %s", next.Kind)
	}
}

// Whatever happens to the journal job, the turn entry stays as the turn job left it and no push-line signal fires.
// Mutation gate: let finishUnrun fail the entry for a journal job -> a signal fires (red).
func TestJournalJob_FailureLeavesTheTurnEntryAlone(t *testing.T) {
	results := map[string]Result{
		"api error":   {Reason: "api-error", Status: 500, Error: "overloaded"},
		"aborted":     {Reason: "aborted", LatencyMS: 61_000},
		"refused":     {Reason: "refused"},
		"garbage":     answer("好的，我來寫日誌"),
		"wrong shape": answer(`{"events":"x","confirm_done":[],"roadmap":[]}`),
	}
	for name, res := range results {
		k := journalKit(t, team.SeatLead, "a")
		k.finish("m", mustNext(t, k, "m", "s1"), answerJSON("事", "推", "紀錄", "狀態", ""))
		id := k.entries("s1")[0].ID
		before := entryState(k, id)
		lines := len(k.lines)
		j := mustNext(t, k, "m", "s1")
		k.finish("m", j, res)
		if name == "garbage" || name == "wrong shape" { // one format retry, then nothing
			j2 := mustNext(t, k, "m", "s1")
			if j2.Kind != JobJournal || j2.ID == j.ID {
				t.Fatalf("%s: retry = %+v", name, j2)
			}
			k.finish("m", j2, res)
		}
		if _, ok := k.next("m", "s1"); ok {
			t.Errorf("%s: another job", name)
		}
		after := entryState(k, id)
		if after != before || len(k.lines) != lines {
			t.Errorf("%s: the turn entry changed\nbefore %+v\nafter  %+v\nsignals %v", name, before, after, k.lines[lines:])
		}
	}
	// a journal job whose lease runs out
	k := journalKit(t, team.SeatLead, "a")
	k.finish("m", mustNext(t, k, "m", "s1"), answerJSON("事", "推", "紀錄", "狀態", ""))
	id := k.entries("s1")[0].ID
	before := entryState(k, id)
	lines := len(k.lines)
	mustNext(t, k, "m", "s1")
	k.clock.Add(2 * time.Minute)
	k.e.reap()
	if after := entryState(k, id); after != before || len(k.lines) != lines {
		t.Errorf("a lost journal job changed the entry: %+v, signals %v", after, k.lines[lines:])
	}
}

// A valid answer goes through the daemon rules to the seam; a [pdx team] turn gets no events whatever the model says.
// Mutation gate: skip ValidateJournal (hand the parse to the seam) -> the team turn keeps its event (red).
func TestJournalJob_ResultReachesTheSeamThroughTheRules(t *testing.T) {
	var got []JournalOutcome
	k := journalKit(t, team.SeatLead)
	k.e.d.OnJournalResult = func(o JournalOutcome) { got = append(got, o) }
	t1 := convmodel.Turn{ID: "t1", Outcome: convmodel.OutcomeDone, StartedAt: 95, EndedAt: ptr(int64(100)),
		Items: []convmodel.Item{userItem("[pdx team] 成員 x 已完成"), agentItem("收到")}}
	t2 := convmodel.Turn{ID: "t2", Outcome: convmodel.OutcomeDone, StartedAt: 195, EndedAt: ptr(int64(200)),
		Items: []convmodel.Item{userItem("[1f → 88] 請查"), agentItem("好")}}
	for i, turns := range [][]convmodel.Turn{{t1}, {t1, t2}} {
		k.turns.set("s1", turns...)
		k.event("s1", int64(1000*(i+1)))
		k.finish("m", mustNext(t, k, "m", "s1"), answerJSON("事", "推", "紀錄", "狀態", ""))
		k.finish("m", mustNext(t, k, "m", "s1"), answer(journalAnswer))
	}
	if len(got) != 2 {
		t.Fatalf("outcomes = %d", len(got))
	}
	if len(got[0].Result.Events) != 0 || got[0].Result.Dropped != 1 {
		t.Errorf("[pdx team] turn: %+v", got[0].Result)
	}
	if len(got[1].Result.Events) != 1 || got[1].Result.Events[0].Actor != "1f → 88" || got[1].PromptVer != 1 || got[1].EntryID == 0 || got[1].ConvKey != "s1" {
		t.Errorf("second turn: %+v", got[1])
	}
	if !strings.Contains(k.logs.text(), "journal job of entry") {
		t.Error("the result was not logged")
	}
}

func ptr[T any](v T) *T { return &v }

// The shipped journal prompt is the measured block of the prompt document, byte for byte. The document lives on the
// spec branch until it merges, so the check runs where the file is.
func TestJournalPrompt_EqualsTheDocumentBlock(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/specs/2026-10-14-workbook-journal-prompt-v3.md")
	if err != nil {
		t.Skip("the prompt document is not in this checkout")
	}
	m := regexp.MustCompile("(?s)```text\n(.*?)\n```").FindSubmatch(doc)
	if m == nil || string(m[1]) != JournalPrompt {
		t.Error("JournalPrompt differs from the first text block of the prompt document")
	}
}

package workbook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/team"
)

type namedJournal struct {
	fakeJournal
	self string
}

func (n namedJournal) SelfName(string) string { return n.self }

// The journal job names the person and the session by display names and reads peers by theirs; the turn job that ran
// just before it on the same turn keeps the addresses.
// Mutation gate: send the raw virtual name -> self_name red; skip RewritePeerPrefixes -> user_prompt red; rewrite in the
// shared turn text (before the turn job's copy) -> the turn job's prompt red; ignore Deps.PersonName -> person_name red.
func TestJournalJob_NamesAndPrefixRewriteOnlyInTheJournalCopy(t *testing.T) {
	for _, c := range []struct {
		name       string
		person     func() string
		wantPerson string
	}{
		{"configured", func() string { return "Wake" }, "Wake"},
		{"default when the func is nil", nil, "使用者"},
		{"default when the value is blank", func() string { return " " }, "使用者"},
	} {
		k := journalKit(t, team.SeatLead)
		k.e.d.Journal = namedJournal{self: "mlab/purdex-3f-adam"}
		k.e.d.PersonName = c.person
		ended := int64(100)
		k.turns.set("s1", convmodel.Turn{ID: "t1", Outcome: convmodel.OutcomeDone, StartedAt: 95, EndedAt: &ended,
			Items: []convmodel.Item{userItem("[mlab/purdex-88-b8 → mlab/purdex-3f-adam] 請查"), agentItem("好")}})
		k.event("s1", 1000)
		tj := mustNext(t, k, "m", "s1")
		if tj.Kind != JobTurn || !strings.Contains(tj.Complete.Prompt, "[mlab/purdex-88-b8 → mlab/purdex-3f-adam] 請查") {
			t.Fatalf("%s: the turn job lost the addresses: %q", c.name, tj.Complete.Prompt)
		}
		k.finish("m", tj, answerJSON("事", "推", "紀錄", "狀態", ""))
		j := mustNext(t, k, "m", "s1")
		var in struct {
			PersonName string `json:"person_name"`
			SelfName   string `json:"self_name"`
			Turn       struct {
				UserPrompt string `json:"user_prompt"`
			} `json:"turn"`
		}
		if err := json.Unmarshal([]byte(j.Complete.Prompt), &in); err != nil {
			t.Fatal(err)
		}
		if in.PersonName != c.wantPerson || in.SelfName != "Adam" || in.Turn.UserPrompt != "[purdex-88-b8 → Adam] 請查" {
			t.Errorf("%s: input = %+v", c.name, in)
		}
	}
}

// The default is spelled twice (the config package cannot import the journal package); they must agree.
func TestJournalPersonName_DefaultsAgree(t *testing.T) {
	if journal.DefaultPersonName != config.DefaultJournalPersonName {
		t.Fatalf("journal %q != config %q", journal.DefaultPersonName, config.DefaultJournalPersonName)
	}
}

// The drainer names the session on its exec and task entries by display name and writes the person's name on a request
// they make; with no names wired it writes no actor and the default label.
// Mutation gate: skip DisplayName -> the raw address on the entries (red); drop the Person assignment -> the label stays
// 使用者 (red); ignore personName -> the default (red).
func TestDrain_ActorsAreDisplayNamesAndThePersonIsNamed(t *testing.T) {
	k := newDrainKit(t)
	k.d.SetNames(func(sid string) string { return "mlab/purdex-3f-adam" }, func() string { return "Wake" })
	id := k.turn("c", "t1", team.SeatNone, adds("寫測試"))
	k.okEntry(id, "做完了。", false)
	out := JournalOutcome{EntryID: id, PromptVer: 1, Result: JournalResult{Events: []JournalEvent{
		{Kind: journal.KindRequest, By: journal.ByUser, Actor: "Wake → Adam", Text: "Wake 要求查。"},
		{Kind: journal.KindRequest, By: journal.ByLeader, Actor: "Adam → 1f", Text: "派工。"},
	}}}
	if err := k.st.RecordJournalResult(out); err != nil {
		t.Fatal(err)
	}
	k.drain()
	byKind := map[journal.Kind]journal.Entry{}
	for _, e := range k.entries("solo:c") {
		byKind[e.Kind] = e
	}
	for _, kind := range []journal.Kind{journal.KindExec, journal.KindTaskNew} {
		if byKind[kind].Actor != "Adam" {
			t.Errorf("%s actor = %q, want Adam", kind, byKind[kind].Actor)
		}
	}
	u, _ := k.byID("solo:c", "ev:"+itoa(id)+":0")
	l, _ := k.byID("solo:c", "ev:"+itoa(id)+":1")
	if u.Person != "Wake" || u.Label() != "Wake請求" || l.Person != "" || l.Label() != "領導者請求" {
		t.Errorf("user request %+v (%s), leader request %+v (%s)", u, u.Label(), l, l.Label())
	}

	k2 := newDrainKit(t) // nothing wired
	id2 := k2.turn("c", "t1", team.SeatNone, TodoChanges{})
	k2.okEntry(id2, "紀錄。", false)
	if err := k2.st.RecordJournalResult(JournalOutcome{EntryID: id2, PromptVer: 1, Result: JournalResult{Events: []JournalEvent{{Kind: journal.KindRequest, By: journal.ByUser, Text: "要求。"}}}}); err != nil {
		t.Fatal(err)
	}
	k2.drain()
	x, _ := k2.byID("solo:c", "turn:"+itoa(id2))
	r, _ := k2.byID("solo:c", "ev:"+itoa(id2)+":0")
	if x.Actor != "" || r.Label() != "使用者請求" {
		t.Errorf("unwired: exec actor %q, label %q", x.Actor, r.Label())
	}
}

// The engine of a running daemon reads the person's name from the live config: a configured name reaches the journal
// job, and a reload applies without a restart.
// Mutation gate: drop the Deps.PersonName wiring in startEngine -> red.
func TestModule_EngineReadsPersonNameFromTheLiveConfig(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), Journal: &config.JournalConfig{PersonName: "Wake"}}
	c := core.New(core.CoreDeps{Config: cfg})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	m.mu.Lock()
	eng := m.engine
	m.mu.Unlock()
	if eng == nil || eng.personName() != "Wake" {
		t.Fatalf("engine = %v", eng)
	}
	c.CfgMu.Lock()
	c.Cfg.Journal = &config.JournalConfig{PersonName: "Other"}
	c.CfgMu.Unlock()
	if got := eng.personName(); got != "Other" {
		t.Errorf("after a reload: %q", got)
	}
}

// A journal row keeps the person_name its job ran with: a config reload between the job and the drain does not change
// the label of the events the job wrote about the old name.
// Mutation gate: read d.person() at drain time instead of p.Person -> red.
func TestDrain_PersonNameComesFromTheJobNotTheLiveConfig(t *testing.T) {
	k := newDrainKit(t)
	name := "New"
	k.d.SetNames(nil, func() string { return name })
	id := k.turn("c", "t1", team.SeatNone, TodoChanges{})
	k.okEntry(id, "紀錄。", false)
	out := JournalOutcome{EntryID: id, PromptVer: 1, Person: "Old", Result: JournalResult{Events: []JournalEvent{
		{Kind: journal.KindRequest, By: journal.ByUser, Text: "Old 要求。"}}}}
	if err := k.st.RecordJournalResult(out); err != nil {
		t.Fatal(err)
	}
	k.drain()
	e, ok := k.byID("solo:c", "ev:"+itoa(id)+":0")
	if !ok || e.Label() != "Old請求" {
		t.Errorf("entry %+v ok=%v label %q", e, ok, e.Label())
	}
}

// The name a job ran with reaches the stored outcome.
func TestJournalJob_OutcomeCarriesThePersonName(t *testing.T) {
	k := journalKit(t, team.SeatLead, "a")
	k.e.d.PersonName = func() string { return "Wake" }
	var got JournalOutcome
	k.e.d.OnJournalResult = func(o JournalOutcome) { got = o }
	k.finish("m", mustNext(t, k, "m", "s1"), answerJSON("事", "推", "紀錄", "狀態", ""))
	j := mustNext(t, k, "m", "s1")
	k.e.d.PersonName = func() string { return "Changed" } // a reload while the job runs
	k.finish("m", j, answer(journalAnswer))
	if got.Person != "Wake" {
		t.Errorf("outcome person = %q", got.Person)
	}
}

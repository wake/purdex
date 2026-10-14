package workbook

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/convmodel"
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

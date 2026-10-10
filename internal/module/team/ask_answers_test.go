package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #1845: decide checks a hook_ask's answers against the row's questions with the rule of the mod's answersFit (hooks/ask.js): one
// non-empty string per question text, no other key. hooks/ask_answers.fixture.js (inside the plugin folder, where `claude plugin test` can import it) is shared with ask.test.ts, so the two sides cannot
// drift apart unnoticed.

type answersCase struct {
	Name      string          `json:"name"`
	Questions json.RawMessage `json:"questions"`
	Answers   json.RawMessage `json:"answers"`
	Fit       bool            `json:"fit"`
}

func loadAnswersCases(t *testing.T) []answersCase {
	t.Helper()
	raw, err := os.ReadFile("../../../cmd/pdx/plugin/purdex/hooks/ask_answers.fixture.js")
	if err != nil {
		t.Fatal(err)
	}
	// the file is a JS module (`claude plugin test` imports only code files): comment lines, then `export default <JSON>`
	_, body, found := strings.Cut(string(raw), "export default ")
	if !found {
		t.Fatal("fixture: no `export default`")
	}
	var file struct {
		Cases []answersCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &file); err != nil || len(file.Cases) == 0 {
		t.Fatalf("fixture: %v (%d cases)", err, len(file.Cases))
	}
	return file.Cases
}

// beginWithQuestions opens a hook_ask for sid-1 with exactly these questions and returns its id.
func (f *fixture) beginWithQuestions(toolUse string, questions json.RawMessage) string {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/ask/begin", team.AskBeginRequest{SessionID: "sid-1", ToolUseID: toolUse, Kind: team.KindHookAsk,
		Payload: json.RawMessage(`{"questions":` + string(questions) + `}`)})
	var out team.AskBeginResponse
	if code != http.StatusCreated || json.Unmarshal(body, &out) != nil || out.ID == "" {
		f.t.Fatalf("begin: %d %s", code, body)
	}
	return out.ID
}

func (f *fixture) decideRawAnswers(id string, answers json.RawMessage) (int, []byte) {
	f.t.Helper()
	return f.decideRaw(id, `{"decision":"approve","hook":{"answers":`+string(answers)+`},"client":{"kind":"app","label":"Purdex iOS @ phone"}}`)
}

// Every case of the shared fixture: 200 and approved when the answers fit, 400 bad_request (the row still open) when not.
// Mutation gates: no validation → the misfits are approved; only the count of keys; extra keys allowed; Go's TrimSpace instead of
// ECMAScript's whitespace → the BOM and NEL cases (red).
func TestDecideHookAsk_AnswersFollowTheModsRule_SharedFixture(t *testing.T) {
	for i, c := range loadAnswersCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			f := newFixture(t)
			id := f.beginWithQuestions(fmt.Sprintf("toolu_fx%d", i), c.Questions)
			code, body := f.decideRawAnswers(id, c.Answers)
			a, _, _ := f.m.store.Get(id)
			if c.Fit {
				if code != http.StatusOK || a.State != team.StateApproved {
					t.Fatalf("fits, but %d %s (row %s)", code, body, a.State)
				}
				return
			}
			if code != http.StatusBadRequest || decodeErr(t, body).Error != team.ErrBadRequest || a.State != team.StateOpen {
				t.Fatalf("does not fit, but %d %s (row %s), want 400 bad_request and the row still open", code, body, a.State)
			}
		})
	}
}

// The 400 says which question is wrong, so the phone can show it.
func TestDecideHookAsk_TheDetailNamesTheProblem(t *testing.T) {
	two := json.RawMessage(`[{"question":"紅還是藍？","header":"a","options":[],"multiSelect":false},{"question":"要不要加辣？","header":"b","options":[],"multiSelect":false}]`)
	for name, tc := range map[string]struct {
		answers string
		want    string
	}{
		"missing": {`{"紅還是藍？":"紅"}`, "要不要加辣？"},
		"unknown": {`{"紅還是藍？":"紅","要不要加辣？":"要","別的":"x"}`, "別的"},
		"empty":   {`{"紅還是藍？":"紅","要不要加辣？":"  "}`, "要不要加辣？"},
		"no key":  {`{}`, "hook.answers"},
	} {
		f := newFixture(t)
		id := f.beginWithQuestions("toolu_d", two)
		code, body := f.decideRawAnswers(id, json.RawMessage(tc.answers))
		if e := decodeErr(t, body); code != http.StatusBadRequest || e.Error != team.ErrBadRequest || !strings.Contains(e.Detail, tc.want) {
			t.Errorf("%s: %d %s, want 400 with %q in the detail", name, code, body, tc.want)
		}
	}
}

// Not touched: a denied hook_ask (the chat reply), a hook_permission, a lead request.
func TestDecideHookAsk_OtherDecisionsAreUnchanged(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_chat")
	if code, body := f.decideRaw(id, `{"decision":"deny","hook":{"message":"聊聊"},"client":{"kind":"app","label":"phone"}}`); code != http.StatusOK {
		t.Fatalf("chat reply = %d %s", code, body)
	}
	pid := f.askBeginPermission("toolu_perm")
	if code, body := f.decideRaw(pid, `{"decision":"approve","client":{"kind":"app","label":"phone"}}`); code != http.StatusOK {
		t.Fatalf("hook_permission approve = %d %s", code, body)
	}
	lead := f.create(uid(3))
	if code, body := f.decide(lead.ID, "approve"); code != http.StatusOK {
		t.Fatalf("lead approve = %d %s", code, body)
	}
}

// codex attack: Go decodes a lone UTF-16 surrogate (\ud800) to U+FFFD, JavaScript keeps it as it is. Two different questions
// \ud800 and \ud801 would fold into ONE Go key, and the daemon would approve one answer for two questions that the mod's Set still
// counts as two. A text that cannot be told apart exactly is not matched (the stricter direction: a 400, never an approval the
// mod refuses). Not in the shared fixture: the mod would accept a genuine U+FFFD. Mutation gate: drop the check → red.
func TestDecideHookAsk_ATextTheDecoderCannotKeepApartIsRefused(t *testing.T) {
	for name, tc := range map[string]struct{ questions, answers string }{
		"two different lone surrogates": {`[{"question":"\ud800","header":"a"},{"question":"\ud801","header":"b"}]`, `{"\ud800":"x"}`},
		"one lone surrogate":            {`[{"question":"\ud800","header":"a"}]`, `{"\ud800":"x"}`},
		"a literal U+FFFD":              {"[{\"question\":\"a�b\",\"header\":\"a\"}]", "{\"a�b\":\"x\"}"},
	} {
		f := newFixture(t)
		id := f.beginWithQuestions("toolu_s", json.RawMessage(tc.questions))
		code, body := f.decideRawAnswers(id, json.RawMessage(tc.answers))
		if a, _, _ := f.m.store.Get(id); code != http.StatusBadRequest || a.State != team.StateOpen {
			t.Errorf("%s: %d %s (row %s), want 400 and the row open", name, code, body, a.State)
		}
	}
}

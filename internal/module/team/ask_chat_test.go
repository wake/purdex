package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// A hook_ask can be denied with the person's reply ("chat about this"): the
// message is trimmed and kept; printable text plus \n and \t only, 1–4000
// runes after trimming; never together with answers.
func TestDecide_HookAskDenyCarriesTheReply(t *testing.T) {
	f := newFixture(t)
	decide := func(id string, h *team.HookDecision) (int, []byte) {
		return f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide", team.DecideRequest{Decision: "deny", Hook: h, Client: appClient()})
	}
	accept := func(name, in, want string) {
		t.Helper()
		id := f.askBegin("toolu_chat_" + name)
		f.events()
		if code, body := decide(id, &team.HookDecision{Message: in}); code != http.StatusOK {
			t.Fatalf("%s: decide = %d %s", name, code, body)
		}
		a, _, _ := f.m.store.Get(id)
		if a.State != team.StateDenied || a.Hook == nil || a.Hook.Message != want || a.Hook.Answers != nil || a.Hook.Behavior != "" {
			t.Fatalf("%s: row = %+v hook=%+v, want denied with exactly message %q", name, a, a.Hook, want)
		}
		evs := f.events()
		if len(evs) != 1 || evs[0].Op != "closed" || evs[0].Approval.State != team.StateDenied || evs[0].Approval.Hook.Message != want {
			t.Fatalf("%s: events = %+v", name, evs)
		}
	}
	accept("plain", "  先別選，我想問一下  \n", "先別選，我想問一下")
	accept("nltab", "第一行\n\t第二行，含 tab\n第三行", "第一行\n\t第二行，含 tab\n第三行")
	accept("max", strings.Repeat("字", 4000), strings.Repeat("字", 4000))

	reject := func(name string, h *team.HookDecision) {
		t.Helper()
		id := f.askBegin("toolu_bad_" + name)
		code, body := decide(id, h)
		if code != http.StatusBadRequest || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Errorf("%s: %d %s, want 400 bad_request", name, code, body)
		}
		if a, _, _ := f.m.store.Get(id); a.State != team.StateOpen {
			t.Errorf("%s: row = %s, want still open", name, a.State)
		}
	}
	reject("missing", nil)
	reject("empty", &team.HookDecision{})
	reject("blank", &team.HookDecision{Message: " \n\t "})
	reject("toolong", &team.HookDecision{Message: strings.Repeat("字", 4001)})
	reject("cr", &team.HookDecision{Message: "a\r\nb"})
	reject("nul", &team.HookDecision{Message: "a\x00b"})
	reject("esc", &team.HookDecision{Message: "a\x1bb"})
	reject("answers", &team.HookDecision{Message: "hi", Answers: map[string]string{"紅還是藍？": "藍"}})
}

// The chat reply is an answer like any other: a second decide loses, the
// terminal's own answer still stands over it, and a terminal_only row stays
// read-only.
func TestDecide_HookAskDenyRaces(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_chat_r")
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide", team.DecideRequest{Decision: "deny", Hook: &team.HookDecision{Message: "hi"}, Client: appClient()}); code != 200 {
		t.Fatalf("deny = %d %s", code, body)
	}
	code, body := f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide", team.DecideRequest{Decision: "deny", Hook: &team.HookDecision{Message: "again"}, Client: appClient()})
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrAlreadyDecided {
		t.Fatalf("second decide = %d %s", code, body)
	}
	code, body = f.do(http.MethodPost, "/api/ask/report/"+id,
		team.AskReportRequest{State: team.StateAnsweredLocal, Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "紅"}}})
	if a := decodeApproval(t, body); code != 200 || a.State != team.StateTerminalOverride {
		t.Fatalf("late terminal answer = %d %s, want terminal_override", code, body)
	}
	payload, _ := hookPayloadFor(team.KindHookAsk, "toolu_chat_ro", json.RawMessage(`{"questions":`+askQuestions+`}`), true)
	f.m.createMu.Lock()
	ro, err := f.m.openHookRow(fixtureOrigins["/tmp/10.sock"], team.KindHookAsk, payload, true)
	f.m.createMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+ro.ID+"/decide", team.DecideRequest{Decision: "deny", Hook: &team.HookDecision{Message: "hi"}, Client: appClient()})
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrTerminalOnly {
		t.Fatalf("deny terminal_only = %d %s", code, body)
	}
}

// Wait: a denied hook_ask is answered_remote with its hook (the reply); the
// other bodies are unchanged. Mutation gate: askWaitOf without the hook_ask
// denied case ⇒ closed{denied} ⇒ red.
func TestAskWait_ChatReplyIsAnsweredRemoteWithTheMessage(t *testing.T) {
	f := newFixture(t)
	msg := "先別選\n\t我想問：為什麼？"
	id := f.askBegin("toolu_chat_w")
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide", team.DecideRequest{Decision: "deny", Hook: &team.HookDecision{Message: msg}, Client: appClient()}); code != 200 {
		t.Fatalf("deny = %d %s", code, body)
	}
	code, body := f.do(http.MethodGet, "/api/ask/wait/"+id+"?wait=25", nil)
	w := decodeWait(t, body)
	if code != 200 || w.State != team.AskAnsweredRemote || w.Hook == nil || w.Hook.Message != msg || w.Hook.Answers != nil || w.Reason != "" {
		t.Fatalf("wait = %d %s", code, body)
	}
	// Unchanged: an approved hook_ask, and a dismissed one.
	ok := f.askBegin("toolu_chat_w2")
	f.do(http.MethodPost, "/api/team/approvals/"+ok+"/decide", team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"q": "a"}}, Client: appClient()})
	_, body = f.do(http.MethodGet, "/api/ask/wait/"+ok, nil)
	if w := decodeWait(t, body); w.State != team.AskAnsweredRemote || w.Hook == nil || w.Hook.Answers["q"] != "a" {
		t.Fatalf("approved wait = %s", body)
	}
	dis := f.askBegin("toolu_chat_w3")
	f.do(http.MethodPost, "/api/ask/report/"+dis, team.AskReportRequest{State: team.StateDismissed})
	_, body = f.do(http.MethodGet, "/api/ask/wait/"+dis, nil)
	if w := decodeWait(t, body); w.State != team.AskClosed || w.Reason != "dismissed" {
		t.Fatalf("dismissed wait = %s", body)
	}
}

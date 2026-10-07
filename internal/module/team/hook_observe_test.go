package teammod

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func preAsk(sid, toolUse string) team.HookDecideRequest {
	return team.HookDecideRequest{Agent: "cc", Event: "PreToolUse", SessionID: sid, ToolName: "AskUserQuestion", ToolUseID: toolUse,
		ToolInput: json.RawMessage(`{"questions":` + askQuestions + `}`)}
}

func (f *fixture) flagPath(agent, sid string) string {
	return filepath.Join(f.core.Cfg.DataDir, HookAsksDir, agent, sid)
}

func (f *fixture) flagExists(sid string) bool {
	_, err := os.Stat(f.flagPath("cc", sid))
	return err == nil
}

func permReq(sid, input string) team.HookDecideRequest {
	return team.HookDecideRequest{Agent: "cc", Event: "PermissionRequest", SessionID: sid, ToolName: "Bash", ToolInput: json.RawMessage(input)}
}

// Without the mod, a PreToolUse/AskUserQuestion opens a terminal_only row
// and writes the flag; the matching PostToolUse closes it as answered_local
// with CC's answers and removes the flag.
func TestObserve_TerminalOnlyOpensAndClosesOnPostToolUse(t *testing.T) {
	f := newFixture(t)
	f.m.observeHookEvent(preAsk("sid-1", "toolu_a"))
	rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1")
	if len(rows) != 1 || rows[0].Kind != team.KindHookAsk || !isTerminalOnly(rows[0]) || rows[0].LeaseUntil != team.NoExpiryAt {
		t.Fatalf("rows = %+v", rows)
	}
	if fi, err := os.Stat(f.flagPath("cc", "sid-1")); err != nil || fi.Mode().Perm() != 0o600 || fi.Size() != 0 {
		t.Fatalf("flag = %v %v, want empty 0600", fi, err)
	}
	if di, err := os.Stat(filepath.Dir(f.flagPath("cc", "sid-1"))); err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("flag dir = %v %v, want 0700", di, err)
	}
	if evs := f.events(); len(evs) != 1 || evs[0].Op != "opened" {
		t.Fatalf("events = %+v", evs)
	}
	f.m.observeHookEvent(preAsk("sid-1", "toolu_a"))
	if rows, _ = f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 1 {
		t.Fatalf("duplicate open: %d rows", len(rows))
	}
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PostToolUse", SessionID: "sid-1", ToolName: "AskUserQuestion", ToolUseID: "toolu_a",
		Raw: json.RawMessage(`{"tool_response":{"questions":[],"answers":{"紅還是藍？":"紅"}}}`)})
	a, _, _ := f.m.store.Get(rows[0].ID)
	if a.State != team.StateAnsweredLocal || a.Hook == nil || a.Hook.Answers["紅還是藍？"] != "紅" || a.DecidedBy == nil || a.DecidedBy.Kind != team.ClientKindTerminal {
		t.Fatalf("after PostToolUse = %+v hook=%+v", a, a.Hook)
	}
	if f.flagExists("sid-1") {
		t.Fatal("flag must go with the last terminal_only row")
	}
}

// PostToolUse without answers, and a Failure even with some, dismiss a hook_ask.
func TestObserve_AskWithoutAnswersIsDismissed(t *testing.T) {
	f := newFixture(t)
	f.m.observeHookEvent(preAsk("sid-1", "toolu_a"))
	f.m.observeHookEvent(preAsk("sid-1", "toolu_b"))
	f.events()
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PostToolUse", SessionID: "sid-1", ToolName: "AskUserQuestion", ToolUseID: "toolu_a"})
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PostToolUseFailure", SessionID: "sid-1", ToolName: "AskUserQuestion", ToolUseID: "toolu_b",
		Raw: json.RawMessage(`{"tool_response":{"answers":{"q":"a"}}}`)})
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
	dismissed := 0
	for _, ev := range f.events() {
		if ev.Op == "closed" && ev.Approval.State == team.StateDismissed {
			dismissed++
		}
	}
	if dismissed != 2 || f.flagExists("sid-1") {
		t.Fatalf("dismissed=%d flag=%v", dismissed, f.flagExists("sid-1"))
	}
}

// A mod-present session gets no terminal_only row; so does a host with no
// remote responder; a Stop closes everything of the session as dismissed.
func TestObserve_ModPresentOrNoRespondersOpensNothing_StopDismissesAll(t *testing.T) {
	f := newFixture(t)
	if code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-1", ModVersion: "1", Agent: "cc"}); code != http.StatusOK {
		t.Fatalf("hello: %d %s", code, body)
	}
	f.m.observeHookEvent(preAsk("sid-1", "toolu_b"))
	f.m.observeHookEvent(permReq("sid-1", `{"command":"ls"}`))
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 0 || f.flagExists("sid-1") {
		t.Fatalf("mod present: rows = %+v flag=%v", rows, f.flagExists("sid-1"))
	}
	f.core.Events.RemoveTestSubscriber(f.sub)
	f.m.observeHookEvent(preAsk("sid-2", "toolu_c"))
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-2"); len(rows) != 0 || f.flagExists("sid-2") {
		t.Fatalf("no responders: rows = %+v", rows)
	}
	f.sub = f.core.Events.AddTestSubscriber()
	f.m.observeHookEvent(preAsk("sid-2", "toolu_c"))
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PermissionRequest", SessionID: "sid-2", ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"ls"}`), Raw: json.RawMessage(`{"permission_suggestions":[{"type":"addRules"}]}`)})
	rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-2")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	perm := rows[0]
	if perm.Kind != team.KindHookPermission {
		perm = rows[1]
	}
	var pp team.HookPermissionPayload
	if json.Unmarshal(perm.Payload, &pp) != nil || pp.ToolName != "Bash" || string(pp.Suggestions) != `[{"type":"addRules"}]` || !pp.TerminalOnly {
		t.Fatalf("permission payload = %s", perm.Payload)
	}
	f.events()
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "Stop", SessionID: "sid-2"})
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-2"); len(rows) != 0 {
		t.Fatalf("after Stop: rows = %+v", rows)
	}
	closed := 0
	for _, ev := range f.events() {
		if ev.Op == "closed" && ev.Approval.State == team.StateDismissed {
			closed++
		}
	}
	if closed != 2 || f.flagExists("sid-2") {
		t.Fatalf("closed=%d flag=%v", closed, f.flagExists("sid-2"))
	}
}

// UserPromptSubmit and SessionEnd close everything too.
func TestObserve_UserPromptSubmitAndSessionEndDismissAll(t *testing.T) {
	for _, ev := range []string{"UserPromptSubmit", "SessionEnd"} {
		f := newFixture(t)
		f.m.observeHookEvent(preAsk("sid-1", "toolu_a"))
		f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: ev, SessionID: "sid-1"})
		if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 0 || f.flagExists("sid-1") {
			t.Fatalf("%s: rows=%+v flag=%v", ev, rows, f.flagExists("sid-1"))
		}
	}
}

// A daemon that is stopping opens nothing.
func TestObserve_StoppingOpensNothing(t *testing.T) {
	f := newFixture(t)
	if err := f.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.m.observeHookEvent(preAsk("sid-1", "toolu_a"))
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 0 || f.flagExists("sid-1") {
		t.Fatalf("stopping: rows=%+v flag=%v", rows, f.flagExists("sid-1"))
	}
}

// An open row for the tool_use_id (the mod's, answerable) is left alone.
func TestObserve_ExistingRowForToolUseOpensNothing(t *testing.T) {
	f := newFixture(t)
	f.askBegin("toolu_m")
	f.m.observeHookEvent(preAsk("sid-1", "toolu_m"))
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 0 || f.flagExists("sid-1") {
		t.Fatalf("rows=%+v flag=%v", rows, f.flagExists("sid-1"))
	}
}

// A permission row closes when its tool's PostToolUse arrives (the user
// pressed Yes), by tool_name since PermissionRequest carries no tool_use_id;
// a re-fired prompt for the same call (whitespace differs) opens nothing.
func TestObserve_PermissionRowClosesOnItsToolsPostToolUse(t *testing.T) {
	f := newFixture(t)
	f.m.observeHookEvent(permReq("sid-1", `{"command":"ls"}`))
	f.m.observeHookEvent(permReq("sid-1", `{ "command": "ls" }`))
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 1 {
		t.Fatalf("a re-fired PermissionRequest must not open a second row: %d rows", len(rows))
	}
	f.m.observeHookEvent(permReq("sid-1", `{"command":"rm x"}`))
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 2 {
		t.Fatalf("a different input is another prompt: %d rows, want 2", len(rows))
	}
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PostToolUse", SessionID: "sid-1", ToolName: "Edit", ToolUseID: "toolu_x"})
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 2 {
		t.Fatalf("another tool's PostToolUse must not close them: %d rows", len(rows))
	}
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PostToolUse", SessionID: "sid-1", ToolName: "Bash", ToolUseID: "toolu_y"})
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 0 || f.flagExists("sid-1") {
		t.Fatalf("Bash's PostToolUse must close them and the flag: %d rows", len(rows))
	}
}

// The flag path is one plain element per part: an agent or session id that
// is an alias (../x, a/b, ., ..) writes nothing and removes nothing outside.
func TestObserve_FlagPathRejectsAliases(t *testing.T) {
	f := newFixture(t)
	victim := filepath.Join(f.core.Cfg.DataDir, "victim")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"cc", "../victim"}, {"../" + HookAsksDir + "/cc", "sid-1"}, {"cc", "a/b"}, {"cc", ".."}, {"cc", "."}, {"", "sid-1"}, {"cc", `a\b`}, {"cc", ""}} {
		if p := f.m.askFlagPath(c[0], c[1]); p != "" {
			t.Errorf("askFlagPath(%q,%q) = %q, want rejected", c[0], c[1], p)
		}
		f.m.setAskFlag(c[0], c[1], true)
		f.m.setAskFlag(c[0], c[1], false)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("victim removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.core.Cfg.DataDir, HookAsksDir)); err == nil {
		t.Fatal("a rejected path must create nothing")
	}
	if p := f.m.askFlagPath("cc", "sid-1"); p != f.flagPath("cc", "sid-1") {
		t.Fatalf("good path = %q", p)
	}
}

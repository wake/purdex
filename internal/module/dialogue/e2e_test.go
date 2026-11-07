package dialogue

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/convmodel/ccnorm/scrub"
	"github.com/wake/purdex/internal/core"
	agentmod "github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/conversation"
	"github.com/wake/purdex/internal/team"
)

// The test lives in package dialogue (not an external package) because it reaches the intake's onSettled hook; the real
// conversation module is imported without a cycle (conversation does not import dialogue).

type e2eOwners struct{}

func (e2eOwners) ConfirmedOwners(context.Context, string) ([]agentmod.PaneOwner, error) {
	return nil, nil
}

type e2eFeed struct{}

func (e2eFeed) SubscribeSession(string, func(string, team.Approval)) ([]team.Approval, func(), error) {
	return nil, func() {}, nil
}
func (e2eFeed) HoldResponder() func() { return func() {} }

// nonMeta drops the header and boundary lines of a segment, leaving what was said.
func nonMeta(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "<prev:") || strings.HasPrefix(l, "<source:") || strings.HasPrefix(l, "<next:") || strings.HasPrefix(l, "<compacted |") {
			continue
		}
		out = append(out, l)
	}
	// a dropped boundary line can leave one extra blank line
	return strings.Trim(strings.ReplaceAll(strings.Join(out, "\n"), "\n\n\n", "\n\n"), "\n")
}

func TestEndToEndDialogueFileFromRealConversationModule(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home) // the conversation module resolves transcripts under $HOME/.claude/projects
	sid := scrub.FixtureSessionID
	raw, err := os.ReadFile(filepath.Join(repoRoot, "testdata/conversation/v1/cc-transcript/compact/input.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(home, ".claude", "projects", "-work-fixture", sid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join(repoRoot, "testdata/dialogue/compact.md"))
	if err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	c := core.New(core.CoreDeps{Config: &config.Config{HostID: "h1:abc", DataDir: dataDir}})
	c.Registry.Register("agent.module", e2eOwners{})
	c.Registry.Register(team.ApprovalFeedKey, e2eFeed{})
	sub := &fakeSub{}
	c.Registry.Register(agentmod.TerminalSessionsKey, sub)

	conv := conversation.New()
	if err := conv.Init(c); err != nil {
		t.Fatal(err)
	}
	if err := conv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conv.Stop(context.Background()) })
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()); m.Close() })
	if st := m.Status(); st["ready"] != true {
		t.Fatalf("status = %v", st)
	}
	settled := make(chan struct{}, 8)
	m.intake.d.onSettled = func(string, int64) { settled <- struct{}{} } // set before the first event: no reader yet
	waitSettled := func() {
		t.Helper()
		select {
		case <-settled:
		case <-time.After(10 * time.Second):
			t.Fatal("the event did not settle")
		}
	}

	ev := agentmod.TurnEndEvent{SessionID: sid, At: time.Now().Add(time.Hour).UnixMilli(), Seq: 1}
	sub.emit(ev)
	waitSettled()

	seg1 := filepath.Join(dataDir, "dialogue", sid, "001.md")
	seg2 := filepath.Join(dataDir, "dialogue", sid, "002.md")
	b1, err1 := os.ReadFile(seg1)
	b2, err2 := os.ReadFile(seg2)
	if err1 != nil || err2 != nil {
		t.Fatalf("segments: %v %v", err1, err2)
	}
	s1, s2 := string(b1), string(b2)
	if first := strings.SplitN(s1, "\n", 2)[0]; first != "<prev: none>" {
		t.Fatalf("001 first line = %q", first)
	}
	if first := strings.SplitN(s2, "\n", 2)[0]; first != "<prev: "+seg1+">" {
		t.Fatalf("002 first line = %q, want prev %s", first, seg1)
	}
	for i, s := range []string{s1, s2} {
		if !strings.Contains(s, "\n<source: "+transcript+">\n") {
			t.Fatalf("segment %d has no <source: %s>:\n%s", i+1, transcript, s)
		}
	}
	if !strings.Contains(s1, "<compacted | manual>\n<next: "+seg2+">") {
		t.Fatalf("001 does not end with the boundary and the next link:\n%s", s1)
	}
	got := nonMeta(s1) + "\n\n" + nonMeta(s2)
	if want := nonMeta(string(golden)); got != want {
		t.Fatalf("joined segments differ from testdata/dialogue/compact.md\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// the same event again changes nothing
	sub.emit(ev)
	waitSettled()
	a1, _ := os.ReadFile(seg1)
	a2, _ := os.ReadFile(seg2)
	if string(a1) != s1 || string(a2) != s2 {
		t.Fatalf("a repeated event changed the files:\n%s\n---\n%s", a1, a2)
	}
}

// The row $.session.append puts into a transcript (Claude Code 2.1.319: isMeta, origin plugin, no promptSource / turnOrigin)
// must leave no trace in the dialogue file and must not move the turn numbers. Mutation gate: drop the meta skip in
// ccnorm/user.go -> the text lands in the file -> red.
func TestPluginAppend_DialogueFileHasNoTrace(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	sid := scrub.FixtureSessionID
	raw, err := os.ReadFile(filepath.Join(repoRoot, "testdata/conversation/v1/cc-transcript/compact/input.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(home, ".claude", "projects", "-work-fixture", sid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	c := core.New(core.CoreDeps{Config: &config.Config{HostID: "h1:abc", DataDir: dataDir}})
	c.Registry.Register("agent.module", e2eOwners{})
	c.Registry.Register(team.ApprovalFeedKey, e2eFeed{})
	sub := &fakeSub{}
	c.Registry.Register(agentmod.TerminalSessionsKey, sub)
	conv := conversation.New()
	if err := conv.Init(c); err != nil {
		t.Fatal(err)
	}
	if err := conv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conv.Stop(context.Background()) })
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()); m.Close() })
	settled := make(chan struct{}, 8)
	m.intake.d.onSettled = func(string, int64) { settled <- struct{}{} }
	waitSettled := func() {
		t.Helper()
		select {
		case <-settled:
		case <-time.After(10 * time.Second):
			t.Fatal("the event did not settle")
		}
	}

	sub.emit(agentmod.TurnEndEvent{SessionID: sid, At: time.Now().Add(time.Hour).UnixMilli(), Seq: 1})
	waitSettled()
	before, _, err := m.store.Session(sid)
	if err != nil {
		t.Fatal(err)
	}
	read := func() string {
		a, _ := os.ReadFile(filepath.Join(dataDir, "dialogue", sid, "001.md"))
		b, _ := os.ReadFile(filepath.Join(dataDir, "dialogue", sid, "002.md"))
		return string(a) + "\x00" + string(b)
	}
	filesBefore := read()

	const injected = "<injected by Purdex after compaction: not the user's words>"
	row := `{"parentUuid":"e2da8562-bb01-41fe-8a55-932a0f816274","isSidechain":false,"promptId":"0858945e-4142-449d-a24e-aa79d6c23ccd","type":"user",` +
		`"message":{"role":"user","content":[{"type":"text","text":"` + injected + `"}]},"isMeta":true,"uuid":"68299c98-6ceb-4f10-b38f-e5c6194fa6eb",` +
		`"timestamp":"2099-01-01T00:00:00.000Z","origin":{"kind":"plugin","name":"purdex"},"userType":"external"}` + "\n"
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(row); err != nil {
		t.Fatal(err)
	}
	f.Close()

	sub.emit(agentmod.TurnEndEvent{SessionID: sid, At: time.Now().Add(2 * time.Hour).UnixMilli(), Seq: 2})
	waitSettled()
	after, _, err := m.store.Session(sid)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(); strings.Contains(got, "injected by Purdex") || got != filesBefore {
		t.Fatalf("the appended row left a trace in the dialogue file:\n%s", got)
	}
	if after.NextTurnNo != before.NextTurnNo {
		t.Fatalf("next_turn_no moved: %d -> %d", before.NextTurnNo, after.NextTurnNo)
	}
}

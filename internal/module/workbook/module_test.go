package workbook

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/module/agent"
)

func TestModule_NameAndDependencies(t *testing.T) {
	m := New()
	if m.Name() != "workbook" {
		t.Fatalf("name = %q", m.Name())
	}
	got := map[string]bool{}
	for _, d := range m.Dependencies() {
		got[d] = true
	}
	for _, want := range []string{"agent", "team", "conversation", "hostconfig"} {
		if !got[want] {
			t.Errorf("missing dependency %q in %v", want, m.Dependencies())
		}
	}
}

func TestModule_InitOpensTheStoreOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}})
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	if st := m.Status(); st["ready"] != true || st["init_error"] != "" {
		t.Fatalf("status = %v", st)
	}
	fi, err := os.Stat(filepath.Join(dir, "workbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("workbook.db is %o", fi.Mode().Perm())
	}
}

// A store that cannot be opened must not stop the daemon: the module records why and stays off.
// Mutation gate: return the error from Init → red.
func TestModule_InitSoftFailsOnABrokenDataDir(t *testing.T) {
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: filepath.Join(t.TempDir(), "missing", "dir")}})
	if err := m.Init(c); err != nil {
		t.Fatalf("Init returned %v, want nil (soft fail)", err)
	}
	st := m.Status()
	if st["ready"] != false || st["init_error"] == "" {
		t.Fatalf("status = %v", st)
	}
	if m.live() != nil {
		t.Fatal("a soft-failed module handed out a store")
	}
}

// If the leftover entries cannot be settled the module must not claim to be ready (codex attack).
// Mutation gate: log and return nil without disabling → red.
func TestModule_StartDisablesItselfWhenFailPendingFails(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	m.live().db.Close() // the next statement fails
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start = %v, want nil (soft)", err)
	}
	st := m.Status()
	if st["ready"] != false || st["init_error"] == "" {
		t.Fatalf("status = %v", st)
	}
	if m.live() != nil {
		t.Fatal("a disabled module handed out a store")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after disable = %v", err)
	}
}

// Start writes the prompts the runner will read, owner-only, and a stale file is replaced.
// Mutation gate: drop the WritePromptFiles call from Start → red.
func TestModule_StartWritesThePromptFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "workbook"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workbook", "prompt-v1.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(m.prompts.System)
	if err != nil || string(got) != SystemPrompt {
		t.Fatalf("system prompt file: err=%v, equal=%v", err, string(got) == SystemPrompt)
	}
	fi, _ := os.Stat(m.prompts.System)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", fi.Mode().Perm())
	}
	if st := m.Status(); st["ready"] != true {
		t.Fatalf("status = %v", st)
	}
}

type fakeSessions struct {
	fn    func(agent.TurnEndEvent)
	unsub int
}

func (f *fakeSessions) SubscribeTurnEnd(fn func(agent.TurnEndEvent)) func() {
	f.fn = fn
	return func() { f.unsub++ }
}

// Start subscribes the engine to the agent module's turn ends (a hook-only text is enough: no transcript reader is
// registered), and Stop unsubscribes before the store closes. With no mod capable the turn is skipped:no_mod.
// Mutation gate: drop the subscribe, or the unsubscribe, → red.
func TestModule_StartSubscribesTheEngineAndStopLetsGo(t *testing.T) {
	dir := t.TempDir()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}})
	sessions := &fakeSessions{}
	c.Registry.Register(agent.TerminalSessionsKey, sessions)
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sessions.fn == nil || m.Jobs() == nil {
		t.Fatal("not subscribed")
	}
	sessions.fn(agent.TurnEndEvent{SessionID: "s1", Text: "做完了", At: 5000, Seq: 1})
	rows, err := m.live().Conversation("s1", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].State != StateSkipped || rows[0].Reason != ReasonNoMod {
		t.Fatalf("rows = %+v err=%v", rows, err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sessions.unsub != 1 || m.Jobs() != nil {
		t.Fatalf("unsub = %d, jobs = %v", sessions.unsub, m.Jobs())
	}
}

// The whole path through the registry services: a mod that announced workbook.v2 on its stream is capable, its session's
// turn becomes a job the socket service hands out, and the result finishes the entry; a session with no fresh
// announcement is skipped:no_mod (plan D11).
// Mutation gate: drop the Capable wiring, or the JobsKey registration → red.
func TestModule_TheModSocketPathEndToEnd(t *testing.T) {
	sid := "0f8e2c1a-1b2c-4d3e-8f90-a1b2c3d4e5f6"
	other := "11111111-1111-4111-8111-111111111111"
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	sessions := &fakeSessions{}
	c.Registry.Register(agent.TerminalSessionsKey, sessions)
	modReg := modevents.NewRegistry(time.Now)
	c.Registry.Register("modevents", modReg)
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := modReg.Apply(modevents.Batch{V: 1, Stream: "stream-aaaa", Agent: "cc", Caps: []string{CapV2},
		Events: []modevents.Event{{Seq: 1, SID: sid, Type: "heartbeat", Data: []byte(`{}`)}}}); err != nil {
		t.Fatal(err)
	}
	svc, ok := c.Registry.Get(JobsKey)
	if !ok {
		t.Fatal("no job service registered")
	}
	jobs := svc.(modevents.WorkbookService)
	sessions.fn(agent.TurnEndEvent{SessionID: other, Text: "沒有 mod", At: 1000, Seq: 1})
	sessions.fn(agent.TurnEndEvent{SessionID: sid, Text: "做完了", At: 2000, Seq: 2})
	if !jobs.JobWaiting(sid) || jobs.JobWaiting(other) {
		t.Fatal("only the capable session is told a job waits")
	}
	job, ok := jobs.NextJob(context.Background(), "stream-aaaa", sid, 0)
	if !ok {
		t.Fatal("no job")
	}
	id := job.(Job).ID
	if _, err := jobs.JobResult("stream-bbbb", modevents.WorkbookResult{JobID: id}); err != modevents.ErrNotLeased {
		t.Fatalf("another stream: %v", err)
	}
	text := `{"skip":false,"thing":"事","push":"推","entry":"句。","status":"狀","thing_done":false,"todos":{"done":[],"dropped":[],"add":[]}}`
	if _, err := jobs.JobResult("stream-aaaa", modevents.WorkbookResult{JobID: id, Answered: true, Text: text,
		Usage: modevents.WorkbookUsage{Input: 9}, LatencyMS: 5}); err != nil {
		t.Fatal(err)
	}
	rows, _ := m.live().Conversation(sid, 10, 0)
	if len(rows) != 1 || rows[0].State != StateOK || rows[0].Thing != "事" || rows[0].UsageIn != 9 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows, _ := m.live().Conversation(other, 10, 0); len(rows) != 1 || rows[0].Reason != ReasonNoMod {
		t.Fatalf("other = %+v", rows)
	}
	m.Stop(context.Background())
	if _, err := jobs.JobResult("stream-aaaa", modevents.WorkbookResult{JobID: id}); err != modevents.ErrNotLeased || jobs.JobWaiting(sid) {
		t.Fatal("a stopped module hands nothing out")
	}
}

// A restart settles what a crash left pending. Mutation gate: drop the FailPending call in Start → red.
func TestModule_StartFailsLeftoverPending(t *testing.T) {
	dir := t.TempDir()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	id := mustInsert(t, m.live(), pending("c", "s1", "t1", 1))
	m.Stop(context.Background())

	m2 := New()
	if err := m2.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m2.Stop(context.Background()) })
	if err := m2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e, _ := m2.live().Entry(id); e.State != StateFailed || e.Reason != ReasonStopped {
		t.Fatalf("entry = %+v", e)
	}
}

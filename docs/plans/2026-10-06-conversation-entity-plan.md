# Conversation entity Implementation Plan (P1a–P2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Claude Code conversation is one thing in one state (terminal / worker / dormant / gone). A worker has one "exit", leaves the live lists when it exits, and can be rebuilt as a terminal or a worker.

**Architecture:**
- **Daemon (Go).** The agent module answers two questions: "is session S live in a terminal?" (frames) and "a SessionStart just happened" (a subscription). The nex module uses both:
  - it finds S's live workers by paging the embedded Nexen store;
  - it runs one `exitWorker` (terminate + archive, under a lease it holds or borrows);
  - it guards every transfer with an owner check plus a per-session-id lock `sid:<S>`. That lock doubles as the in-flight marker that keeps the manual-resume handler (Q1) away from Purdex's own resumes.
- **SPA.** The list fetch follows Nexen's cursor. A derived selector keeps live rows, one per entity. "退出" replaces terminate in worker-facing UI. Ended worker panes become a rebuild screen with a terminal / worker choice. New Tab and Settings → Worker get the live / exited lists.

**Tech Stack:** Go 1.26 (net/http, embedded Nexen v0.16.1 `lab.protype.tw/wake/nexen`), sqlite (modernc); React 19, Zustand 5, Vitest 4 + Testing Library, Tailwind 4, Phosphor Icons.

**Spec:** `docs/specs/2026-10-06-conversation-entity-spec.md`. Read §2 (user decisions), §4–§9, and every block marked **統籌核准的推導（2026-10-06）** (D1–D16) before any task. Those blocks are binding: they are the coordinator-approved derivations this plan implements.

## Global Constraints

- Worktree: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/conv-entity`. Prefix **every** Bash command with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/conv-entity && ` (Go) or `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/conv-entity/spa && ` (SPA). Edit and Write paths are absolute and include `.claude/worktrees/conv-entity/`.
- Commit with `git commit --only <files>`, one commit per task. Message style: `feat(daemon): …` / `feat(spa): …` / `test(…)`. End every commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Go tests: `go test ./internal/module/nex/... ./internal/module/agent/... ./internal/store/... ./internal/agent/...`. Also run `go vet ./...` and `gofmt -l internal cmd` (must print nothing). Full suite: `go test ./...`.
- SPA: `npx vitest run <file>`; full suite `npx vitest run`; lint `pnpm run lint`; types `npx tsc --noEmit -p tsconfig.app.json` (a bare `npx tsc --noEmit` checks nothing here).
- Nexen stays at **v0.16.1** for the whole plan. No `go.mod` change. Nexen calls go through `engine_iface.go` only (`imports_test.go` enforces the boundary).
- "Live" (spec §4.2): `archived_at == 0 && state != terminated`. `failed` / `rejected` are live until exited. **One helper per side** decides it: Go `isLiveExecution`, TS `isLiveRow`. No other code re-derives it.
- "For S": `session_id == S || resume_session_id == S`. Entity key (D9): `session_id || resume_session_id || id`.
- Lock keys in `session.HandoffLocks`: session code (existing), `exec:<id>` (existing, `takeToTerminalLockKey`), and `sid:<S>` (new, `sidLockKey`). They are always `TryLock`, never blocking. A failed `TryLock` answers 409, except in the Q1 handler, which skips.
- Pdx principals (D4): `pdx:<hostID>` or `pdx:<hostID>/<client>`, with `hostID = m.opts.Config.HostID`. The daemon borrows a lease only from a pdx principal. Any other holder → 409 `held_by {principal}`, and nothing is changed.
- Daemon error bodies use the existing `writeHandoffError(w, status, code, msg, detail)` shape. New codes: `session_owned {owner: "terminal"|"worker", session_id, execution_id?, tmux_pane_id?}`, `transfer_in_progress`, `owner_check_failed`, `terminate_failed`, `terminate_contended`, `exit_failed`, `replace_mismatch`.
- User-visible copy lives in `spa/src/locales/en.json` and `spa/src/locales/zh-TW.json` (both, or `locale-completeness.test.ts` fails), with `{{var}}` interpolation. New keys go under `worker.exit.*`, `worker.ended.*`, `worker.rebuild.*`, `newtab.workers.*`, `settings.worker.tabs.*`. zh-TW copy is given in each task; en copy is a faithful translation.
- Icons: Phosphor only.
- PR size: each PR ≤ 800 changed lines and ≤ 20 files (`git diff --stat origin/main...HEAD | tail -1`). When a PR runs over, split at a task boundary and say so in the PR body.
- **Never restart the mlab daemon.** Deploys are batched by the coordinator (D14). Do not run `pdx stop` / `pdx start` / `pdx restart`.

## PR map

| PR | Tasks | Side |
|---|---|---|
| P1a-1 | 1–3 | daemon / agent module |
| P1a-2 | 4–6 | daemon / nex: live workers, control lease, exit |
| P1a-3 | 7–9 | daemon / nex: owner check, take-to-terminal, take-back |
| P1a-4 | 10–11 | daemon / nex: handoff, manual resume (Q1) |
| P1b-1 | 12–14 | SPA: paging, live entities, dot colours |
| P1b-2 | 15–18 | SPA: exit API, header, rows, Q1 toast |
| P1c-1 | 19 | daemon: `worker-rebuild` |
| P1c-2 | 20–22 | SPA: rebuild screen, worker ended screens, failed handoff |
| P1c-3 | 23 | SPA: the terminal pane's mode choice |
| P2-1 | 24–25 | SPA: New Tab switch, Settings → Worker tab registry |
| P2-2 | 26 | SPA: Exited tab |

Each PR is merged after codex R1 + R2, then bumped. The P1a / P1c-1 daemon PRs do not deploy (D14).

## Review Focus

1. **A late SessionStart hook from a session that a failed take-to-terminal just killed.** Expected: the worker is **not** exited. D3 re-checks for a live terminal frame before acting. Owner: Task 11 ("no live frame → no exit", "unverified frame only → no exit").
2. **Another device's tab holds the control lease** while this one exits or takes the worker to a terminal. Expected: a pdx holder's lease is borrowed and the action succeeds; a non-pdx holder (a Ploom agent token) gets 409 `held_by`, and no terminate, archive or session create happens. Owner: Task 5 (`takeControl` tests), Task 6 (endpoint), Task 8 (take-to-terminal).
3. **Take to terminal clicked twice, or retried after a 200.** Expected: the second call never types a second `claude --resume`. While the first holds `exec:<id>` / `sid:<S>`, it gets 409 `transfer_in_progress` / `takeback_in_progress`. After the first succeeded, it gets 409 `session_owned {owner: "terminal"}`. Owner: Task 8.
4. **The transfer's own resume fires a SessionStart.** Expected: during the transfer, the `sid:<S>` lock makes the Q1 handler skip it. After the transfer exited the worker, the handler finds no live worker and does nothing. Owner: Task 11 ("sid lock held → nothing"), Task 8 (the worker is exited before the lock is released).
5. **More than 500 non-archived executions on a host, or a server that returns the same cursor twice.** Expected: the newest live stints are listed, and the fetch stops after the stuck page or after 20 pages; it never loops. Owner: Task 12.

---

## Phase P1a-1 — agent module: transcript path, terminal lookup, SessionStart subscription

### Task 1: `transcript_path` on frames, the provenance envelope and `/provenance`

**Files:**
- Modify: `internal/agent/cc/status.go:22` (`DetailStrings` keys)
- Modify: `internal/agent/identity.go` (add `ExtractTranscriptPath`)
- Modify: `internal/store/frames.go`:
  - `Frame` struct (:16-35)
  - `addFrameIdentityColumns` (:114-133)
  - every `SELECT … session_id, cwd` list (`GetByIdentity` :283, `FindByPanePID` :301, `ListByPane` :321, `ListAll` :337, and the one inside `UpsertIfUnchanged`)
  - `scanFrame` (:637)
  - new method `SetTranscriptPath`
- Modify: `internal/module/agent/frame_ops.go:124-152` (`recordSessionIdentity`)
- Modify: `internal/module/agent/provenance.go:13-43` (`Provenance`, `buildProvenance`)
- Modify: `internal/module/agent/pane_owner.go:18-27` (`PaneOwner.TranscriptPath`, plus the site that fills `PaneOwner` from a frame)
- Modify: `internal/module/agent/provenance_handler.go:24-41` (response field)
- Test: `internal/store/frames_test.go`, `internal/module/agent/provenance_test.go`, `internal/module/agent/provenance_handler_test.go`, `internal/module/agent/identity_write_test.go`

**Interfaces:**
- Produces:
  - `agentpkg.ExtractTranscriptPath(raw json.RawMessage) string`
  - `store.Frame.TranscriptPath string`
  - `(*FramesStore).SetTranscriptPath(frameID, path string, seq int64) error`
  - `Provenance.TranscriptPath string` (json `transcript_path,omitempty`)
  - `PaneOwner.TranscriptPath string`
  - `provenanceResponse.TranscriptPath` (json `transcript_path,omitempty`)

- [ ] **Step 1: Write the failing tests**

```go
// internal/store/frames_test.go
func TestFrames_TranscriptPathColumnMigratesAndRoundTrips(t *testing.T) {
	s := openTestFrames(t) // the file's existing helper that opens an in-memory store
	f := seedTestFrame(t, s, "%1", 100, "t0") // existing seeding helper; returns the stored Frame
	if err := s.UpdateSessionIdentity(f.FrameID, "sess-1", "/w", 5); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTranscriptPath(f.FrameID, "/h/.claude/projects/-w/sess-1.jsonl", 5); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetByIdentity("%1", 100, "t0")
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.TranscriptPath != "/h/.claude/projects/-w/sess-1.jsonl" {
		t.Fatalf("TranscriptPath = %q", got.TranscriptPath)
	}
}

func TestFrames_SetTranscriptPathRefusesOlderSeq(t *testing.T) {
	s := openTestFrames(t)
	f := seedTestFrame(t, s, "%1", 100, "t0")
	_ = s.UpdateSessionIdentity(f.FrameID, "sess-2", "/w", 9)
	err := s.SetTranscriptPath(f.FrameID, "/old.jsonl", 8)
	if !errors.Is(err, ErrIdentityOutOfOrder) {
		t.Fatalf("err = %v, want ErrIdentityOutOfOrder", err)
	}
}
```

Use the helper names `frames_test.go` already has. If they differ, adapt only the helper calls, not the assertions. Also add one case to the migration test that opens a DB created without the column (the pattern `addFrameIdentityColumns` tests already use) and asserts `transcript_path` exists afterwards.

```go
// internal/module/agent/provenance_test.go — next to TestProvenance_RootSessionStart_EmitsEnvelope (:88)
func TestProvenance_CarriesTranscriptPath(t *testing.T) {
	m := newProvenanceTestModule(t, "inst-1")
	req := rootSessionStartReq(t, `{"session_id":"s1","cwd":"/w","source":"startup","transcript_path":"/t/s1.jsonl"}`)
	norm := m.buildNormalizedForTest(t, req)
	prov := norm.Detail["pdx_provenance"].(Provenance)
	if prov.TranscriptPath != "/t/s1.jsonl" {
		t.Fatalf("TranscriptPath = %q", prov.TranscriptPath)
	}
}
```

Build `rootSessionStartReq` from whatever `TestProvenance_RootSessionStart_EmitsEnvelope` builds inline. The provenance test providers carry Detail via the real cc `DetailStrings`; if this fixture uses a fake provider, extend that fake's Detail keys too.

```go
// internal/module/agent/provenance_handler_test.go
func TestProvenanceHandler_ReturnsTranscriptPath(t *testing.T) {
	// seed a root frame with identity + transcript path for the session's pane
	// (newProvenanceQueryModule + attachPane + seedIdentityFrame, then SetTranscriptPath)
	// GET /api/sessions/{code}/provenance → body.transcript_path == "/t/s1.jsonl"
}

// internal/module/agent/identity_write_test.go
func TestIdentityWrite_StoresTranscriptPathFromHook(t *testing.T) {
	// postIdentityEvent with raw_event {"session_id":"s1","cwd":"/w","transcript_path":"/t/s1.jsonl"}
	// → frames row for the sender has TranscriptPath "/t/s1.jsonl"
}
```

Write these two in full, following the neighbouring tests in the same files.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/store/ ./internal/module/agent/ -run 'TranscriptPath'`
Expected: compile errors (`SetTranscriptPath` undefined, `TranscriptPath` unknown field).

- [ ] **Step 3: Implement**

```go
// internal/agent/identity.go — next to ExtractSessionIdentity
// ExtractTranscriptPath returns the hook payload's top-level transcript_path
// (Claude Code sends it on every hook event), or "" when absent or not a string.
func ExtractTranscriptPath(raw json.RawMessage) string {
	var p struct {
		TranscriptPath string `json:"transcript_path"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return ""
	}
	return p.TranscriptPath
}
```

```go
// internal/store/frames.go
// In addFrameIdentityColumns' list:
{"transcript_path", `TEXT NOT NULL DEFAULT ''`},

// Frame: after Cwd
// TranscriptPath is the agent's own transcript file as its hooks report it.
// Written only by SetTranscriptPath, under the same identity_seq order as
// UpdateSessionIdentity.
TranscriptPath string

// Every SELECT list: "session_id, cwd" becomes "session_id, cwd, transcript_path".
// scanFrame: add &frame.TranscriptPath after &frame.Cwd.

// SetTranscriptPath records the transcript path for a frame. It follows
// UpdateSessionIdentity's ordering rule: equal or newer seq applies, an older
// one returns ErrIdentityOutOfOrder, and a missing frame returns sql.ErrNoRows.
func (s *FramesStore) SetTranscriptPath(frameID, path string, seq int64) error {
	if path == "" {
		return nil
	}
	res, err := s.db.Exec(`UPDATE agent_frames SET transcript_path = ?, identity_seq = ?
		WHERE frame_id = ? AND identity_seq <= ?`, path, seq, frameID, seq)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return s.identityMissOrStale(frameID) // see below
	}
	return nil
}
```

`UpdateSessionIdentity`'s tail already tells "missing" (`sql.ErrNoRows`) from "stale" (`ErrIdentityOutOfOrder`) after `affected == 0`. Extract that tail into `identityMissOrStale(frameID string) error` and call it from both methods. This is a pure extraction: `UpdateSessionIdentity`'s behaviour must not change.

```go
// internal/module/agent/frame_ops.go — recordSessionIdentity, after the
// UpdateSessionIdentity call returned nil (inside the same function):
if tp := agentpkg.ExtractTranscriptPath(req.RawEvent); tp != "" {
	if err := m.frames.SetTranscriptPath(frameID, tp, req.identitySeq); err != nil &&
		!errors.Is(err, sql.ErrNoRows) && !errors.Is(err, store.ErrIdentityOutOfOrder) {
		log.Printf("[agent] transcript_path_write_failed: frame=%s pane=%s err=%v", frameID, req.TmuxPaneID, err)
	}
}
```

Restructure the existing `if err := …UpdateSessionIdentity…; err != nil { … return … }` so that the success path falls through to this block. All three error branches keep returning as today.

```go
// internal/agent/cc/status.go:22
Detail: agent.DetailStrings(raw, "session_id", "cwd", "transcript_path"),

// internal/module/agent/provenance.go — Provenance, after Cwd:
TranscriptPath string `json:"transcript_path,omitempty"`
// buildProvenance:
TranscriptPath: strFromDetail(result.Detail, "transcript_path"),
```

Add `TranscriptPath` to `PaneOwner`, fill it wherever `PaneOwner` is built from a `store.Frame` (`frame.TranscriptPath`), and add `TranscriptPath string \`json:"transcript_path,omitempty"\`` to `provenanceResponse`, filled from the owner.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/store/ ./internal/module/agent/ ./internal/agent/...`
Expected: PASS, including every pre-existing test.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/agent/identity.go internal/agent/cc/status.go internal/store/frames.go internal/store/frames_test.go internal/module/agent/frame_ops.go internal/module/agent/provenance.go internal/module/agent/pane_owner.go internal/module/agent/provenance_handler.go internal/module/agent/provenance_test.go internal/module/agent/provenance_handler_test.go internal/module/agent/identity_write_test.go -m "feat(daemon): record the agent's transcript_path on its frame and provenance

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 2: Terminal lookup by session id (`LiveBySessionID`)

**Files:**
- Create: `internal/module/agent/terminal_sessions.go`
- Create: `internal/module/agent/terminal_sessions_test.go`
- Modify: `internal/store/frames.go` (add `ListRootsBySessionID`)
- Modify: `internal/module/agent/module.go:214-258` (register the service)
- Test: `internal/store/frames_test.go` (append)

**Interfaces:**
- Produces:

```go
const TerminalSessionsKey = "agent.terminal-sessions"

// TerminalSession is one root agent run recorded for a session id.
type TerminalSession struct {
	FrameID, PaneID, AgentType, SessionID, Cwd, TranscriptPath string
	// Verified is true when the pid is alive AND its start time matched the
	// recorded one. False means "alive, start time unreadable": an owner check
	// counts it (conservative), the Q1 handler does not act on it alone.
	Verified bool
}

type TerminalSessions interface {
	LiveBySessionID(ctx context.Context, agentType, sessionID string) ([]TerminalSession, error)
	SubscribeSessionStart(fn func(SessionStartEvent)) (unsubscribe func()) // Task 3
}
```

- `(*FramesStore).ListRootsBySessionID(sessionID string) ([]Frame, error)`: rows with `session_id = ? AND parent_frame_id IS NULL`, ordered by `started_at ASC`; an empty `sessionID` returns nil.

- [ ] **Step 1: Write the failing tests**

```go
// internal/module/agent/terminal_sessions_test.go
package agent

func TestLiveBySessionID(t *testing.T) {
	m := newTestModule(t) // in-memory frames, stubbed process seams (handler_test.go:67-98)
	live := seedRootWithIdentity(t, m, "%1", 101, "st-101", "cc", "S", "/w")  // exit_test.go helper shape
	dead := seedRootWithIdentity(t, m, "%2", 102, "st-102", "cc", "S", "/w")
	reused := seedRootWithIdentity(t, m, "%3", 103, "st-103", "cc", "S", "/w")
	unreadable := seedRootWithIdentity(t, m, "%4", 104, "st-104", "cc", "S", "/w")
	_ = seedRootWithIdentity(t, m, "%5", 105, "st-105", "codex", "S", "/w") // other agent type
	_ = seedRootWithIdentity(t, m, "%6", 106, "st-106", "cc", "OTHER", "/w")
	child := seedChildWithIdentity(t, m, live.FrameID, "%1", 107, "st-107", "cc", "S")

	withLivePids(t, 101, 103, 104, 105, 106, 107) // 102 is dead
	withStartTimes(t, map[int]string{101: "st-101", 103: "st-OTHER", 105: "st-105", 106: "st-106", 107: "st-107"},
		map[int]error{104: errors.New("ps failed")})

	got, err := m.LiveBySessionID(context.Background(), "cc", "S")
	if err != nil {
		t.Fatal(err)
	}
	byFrame := map[string]TerminalSession{}
	for _, g := range got {
		byFrame[g.FrameID] = g
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2 (live verified + unreadable): %+v", len(got), got)
	}
	if !byFrame[live.FrameID].Verified {
		t.Errorf("live frame not verified")
	}
	if g, ok := byFrame[unreadable.FrameID]; !ok || g.Verified {
		t.Errorf("unreadable start time: want present and unverified, got %+v ok=%v", g, ok)
	}
	for _, f := range []string{dead.FrameID, reused.FrameID, child.FrameID} {
		if _, ok := byFrame[f]; ok {
			t.Errorf("frame %s must not be returned", f)
		}
	}
}

func TestLiveBySessionID_EmptySessionIDReturnsNothing(t *testing.T) {
	m := newTestModule(t)
	got, err := m.LiveBySessionID(context.Background(), "cc", "")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}
```

Helpers:
- `seedRootWithIdentity` / `withLivePids` exist (`exit_test.go:54`, `ancestor_test.go:37`). Adapt their argument order.
- If `seedChildWithIdentity` or `withStartTimes` do not exist, add them at the bottom of this test file. `withStartTimes` overrides `processStartTimeFn` (`verify.go:24`) and restores it with `t.Cleanup`.

Add one `frames_test.go` case for `ListRootsBySessionID`: it returns roots only, excludes children and other session ids, and returns nil for `""`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/module/agent/ ./internal/store/ -run 'LiveBySessionID|ListRootsBySessionID'`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

```go
// internal/module/agent/terminal_sessions.go
package agent

import "context"

// TerminalSessionsKey names the service the nex module uses for the
// conversation-entity owner check (spec §4.2 "terminal": a live Purdex frame
// with session_id = S) and for the manual-resume trigger (Q1, D3).
const TerminalSessionsKey = "agent.terminal-sessions"

// (TerminalSession and TerminalSessions as in Interfaces above.)

// LiveBySessionID returns the root frames recorded for sessionID whose
// process is still the recorded one. A dead pid or a start-time mismatch is
// a stale row the sweep will clear; it is not an owner. An unreadable start
// time is kept with Verified=false (see TerminalSession).
func (m *Module) LiveBySessionID(ctx context.Context, agentType, sessionID string) ([]TerminalSession, error) {
	if m == nil || m.frames == nil || sessionID == "" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	frames, err := m.frames.ListRootsBySessionID(sessionID)
	if err != nil {
		return nil, err
	}
	var out []TerminalSession
	for _, f := range frames {
		if agentType != "" && f.AgentType != agentType {
			continue
		}
		if !isPidAliveFn(f.PID) {
			continue
		}
		verified := false
		if st, err := processStartTimeFn(f.PID); err == nil {
			if st != f.ProcessStartTime {
				continue
			}
			verified = true
		}
		out = append(out, TerminalSession{
			FrameID: f.FrameID, PaneID: f.PaneID, AgentType: f.AgentType, SessionID: f.SessionID,
			Cwd: f.Cwd, TranscriptPath: f.TranscriptPath, Verified: verified,
		})
	}
	return out, nil
}
```

Check `processStartTimeFn`'s real signature at `verify.go:24` / `probe.ProcessStartTime`, and mirror how `sweep.go:59-85` compares it. If the start time is not a string there, compare the same way the sweep does.

```go
// internal/store/frames.go
// ListRootsBySessionID returns the top-level frames whose recorded session id
// is sessionID (conversation entity spec §4.2). No index: agent_frames holds
// live runs only.
func (s *FramesStore) ListRootsBySessionID(sessionID string) ([]Frame, error) {
	if sessionID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		       parent_frame_id, subagents_json, status, started_at, last_seen_at, verified,
		       session_id, cwd, transcript_path
		FROM agent_frames
		WHERE session_id = ? AND (parent_frame_id IS NULL OR parent_frame_id = '')
		ORDER BY started_at ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFrames(rows)
}
```

In `module.go` Init, next to `OwnerResolverKey`: `c.Registry.Register(TerminalSessionsKey, TerminalSessions(m))`. This only compiles once Task 3 adds `SubscribeSessionStart`. Add a temporary method stub in this task, `func (m *Module) SubscribeSessionStart(func(SessionStartEvent)) func() { return func() {} }` with an empty `SessionStartEvent struct{}`. Task 3 replaces both.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/module/agent/ ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/agent/terminal_sessions.go internal/module/agent/terminal_sessions_test.go internal/store/frames.go internal/store/frames_test.go internal/module/agent/module.go -m "feat(daemon): look up live terminal runs by Claude session id

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 3: SessionStart subscription

**Files:**
- Modify: `internal/module/agent/terminal_sessions.go` (`SessionStartEvent`, hub, `SubscribeSessionStart`)
- Modify: `internal/module/agent/module.go` (Module field `sessionStarts sessionStartHub`)
- Modify: `internal/module/agent/handler.go` (publish after `attachProvenance`, ~:622)
- Test: `internal/module/agent/terminal_sessions_test.go` (append)

**Interfaces:**
- Produces:

```go
// SessionStartEvent is one SessionStart that applyFrameEvent granted a
// provenance envelope: a verified, top-level agent run that now records
// SessionID on its frame.
type SessionStartEvent struct {
	AgentType      string
	SessionID      string
	Source         string // the hook's "source": "startup" | "resume" | "clear" | …
	TmuxSession    string // the hook's tmux session name
	TmuxPaneID     string
	FrameID        string
	Cwd            string
	TranscriptPath string
}

func (m *Module) SubscribeSessionStart(fn func(SessionStartEvent)) (unsubscribe func())
```

- Delivery contract: each subscriber runs on its own goroutine, with a recover, after the frame write. The hook response never waits for a subscriber.

- [ ] **Step 1: Write the failing tests**

```go
func TestSessionStartSubscription_DeliversGrantedStart(t *testing.T) {
	m := newProvenanceTestModule(t, "inst-1")
	got := make(chan SessionStartEvent, 4)
	unsub := m.SubscribeSessionStart(func(ev SessionStartEvent) { got <- ev })
	defer unsub()

	postRootSessionStart(t, m, `{"session_id":"S","cwd":"/w","source":"resume","transcript_path":"/t/S.jsonl"}`)

	select {
	case ev := <-got:
		if ev.SessionID != "S" || ev.Source != "resume" || ev.TranscriptPath != "/t/S.jsonl" || ev.FrameID == "" || ev.AgentType != "cc" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no SessionStartEvent")
	}
}

func TestSessionStartSubscription_NoEventWithoutEnvelope(t *testing.T) {
	m := newProvenanceTestModule(t, "inst-1")
	got := make(chan SessionStartEvent, 4)
	defer m.SubscribeSessionStart(func(ev SessionStartEvent) { got <- ev })()

	postRootSessionStart(t, m, `{"session_id":"S","cwd":"/w","source":"compact"}`) // compact_ignored
	postChildSessionStart(t, m, `{"session_id":"S2","cwd":"/w","source":"startup"}`) // has a parent frame
	postRootPrompt(t, m, `{"session_id":"S","cwd":"/w"}`)                          // not a SessionStart
	postRootPrompt(t, m, `{"session_id":"S","cwd":"/w","source":"resume"}`)        // a non-SessionStart that carries source

	select {
	case ev := <-got:
		t.Fatalf("unexpected event %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSessionStartSubscription_UnsubscribeAndPanicIsolation(t *testing.T) {
	m := newProvenanceTestModule(t, "inst-1")
	got := make(chan SessionStartEvent, 4)
	unsubPanic := m.SubscribeSessionStart(func(SessionStartEvent) { panic("boom") })
	defer unsubPanic()
	unsub := m.SubscribeSessionStart(func(ev SessionStartEvent) { got <- ev })

	rec := postRootSessionStart(t, m, `{"session_id":"S","cwd":"/w","source":"startup"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("hook answered %d", rec.Code)
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("healthy subscriber starved by a panicking one")
	}
	unsub()
	postRootSessionStart(t, m, `{"session_id":"S3","cwd":"/w","source":"startup"}`)
	select {
	case ev := <-got:
		t.Fatalf("delivered after unsubscribe: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}
```

Build `postRootSessionStart` / `postChildSessionStart` / `postRootPrompt` on `postIdentityEvent` (`identity_write_test.go:588-610`) with the process-tree helpers that make the sender a verified root (or a child of a seeded root). They return the `*httptest.ResponseRecorder`. Put them in this test file.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/module/agent/ -run SessionStartSubscription`
Expected: FAIL (no event is delivered by the stub).

- [ ] **Step 3: Implement**

```go
// internal/module/agent/terminal_sessions.go
type sessionStartHub struct {
	mu   sync.Mutex
	next int
	subs map[int]func(SessionStartEvent)
}

func (h *sessionStartHub) subscribe(fn func(SessionStartEvent)) func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[int]func(SessionStartEvent){}
	}
	id := h.next
	h.next++
	h.subs[id] = fn
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs, id)
	}
}

func (h *sessionStartHub) publish(ev SessionStartEvent) {
	h.mu.Lock()
	fns := make([]func(SessionStartEvent), 0, len(h.subs))
	for _, fn := range h.subs {
		fns = append(fns, fn)
	}
	h.mu.Unlock()
	for _, fn := range fns {
		go func(fn func(SessionStartEvent)) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[agent] session_start subscriber panic: %v", r)
				}
			}()
			fn(ev)
		}(fn)
	}
}

func (m *Module) SubscribeSessionStart(fn func(SessionStartEvent)) func() {
	return m.sessionStarts.subscribe(fn)
}

// sessionStartEventFrom builds the event from the granted envelope plus the
// two fields the envelope does not carry (source, tmux session name).
func sessionStartEventFrom(req EventRequest, prov Provenance) SessionStartEvent {
	var raw struct {
		Source string `json:"source"`
	}
	_ = json.Unmarshal(req.RawEvent, &raw)
	return SessionStartEvent{
		AgentType: prov.AgentType, SessionID: prov.SessionID, Source: raw.Source,
		TmuxSession: req.TmuxSession, TmuxPaneID: prov.TmuxPaneID, FrameID: prov.FrameID,
		Cwd: prov.Cwd, TranscriptPath: prov.TranscriptPath,
	}
}
```

Remove Task 2's stub. In `handler.go`, right after `attachProvenance(&normalized, frameMeta)`:

```go
	// Conversation entity (spec §4.3, D3): a granted SessionStart is the
	// moment "S is in a terminal" becomes true; subscribers (the nex module's
	// manual-resume handler) run off the hook path.
	// The lifecycle check is redundant today (frame_ops.go:1116 grants an
	// envelope only on SessionStart) and kept on purpose: Q1 must never fire
	// on any other event, whatever a later change does to the grant site.
	if lifecycle == agentpkg.LifecycleSessionStart && frameMeta.Provenance != nil && frameMeta.Provenance.SessionID != "" {
		m.sessionStarts.publish(sessionStartEventFrom(req, *frameMeta.Provenance))
	}
```

Check the field name: the provenance pointer travels in `FrameTraceMeta` as whatever `attachProvenance` reads (`provenance.go:47`). Use that exact field.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/module/agent/` then `go test -race ./internal/module/agent/ -run SessionStart`
Expected: PASS, no race.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/agent/terminal_sessions.go internal/module/agent/terminal_sessions_test.go internal/module/agent/module.go internal/module/agent/handler.go -m "feat(daemon): publish granted SessionStarts to in-process subscribers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

**PR P1a-1:** open it with `gh pr create` (title `feat(daemon): conversation entity P1a-1 — terminal lookup, SessionStart subscription, transcript_path`). The body lists Tasks 1–3 and links the spec. Then run codex R1 + R2.

---
## Phase P1a-2 — nex: live workers for S, control lease, exit

### Task 4: Engine seam (`Terminate`, `List`) and `liveWorkersFor`

**Files:**
- Modify: `internal/module/nex/engine_iface.go:17-34`
- Create: `internal/module/nex/owners.go`
- Create: `internal/module/nex/owners_test.go`
- Modify: `internal/module/nex/handoff_fakes_test.go` (`fakeNexService.Terminate`, `fakeNexStore.List`)
- Modify: `internal/module/nex/imports_test.go` only if it lists method names; do not loosen the package boundary.

**Interfaces:**
- Produces:

```go
// engine_iface.go
type nexService interface {
	Delegate(ctx context.Context, req execution.Request) (execution.Result, error)
	AcquireLease(ctx context.Context, executionID, principalID string) (store.Lease, error)
	ReleaseLease(ctx context.Context, executionID, leaseID, principalID string) error
	Interrupt(ctx context.Context, req execution.InterruptRequest) (execution.InterruptResult, error)
	Archive(ctx context.Context, req execution.ArchiveRequest) error
	Terminate(ctx context.Context, req execution.TerminateRequest) error
	RenewLease(ctx context.Context, executionID, leaseID, principalID string) (store.Lease, error) // execution/lease.go:78
}
type nexStore interface {
	Get(ctx context.Context, id string) (store.Execution, error)
	List(ctx context.Context, opts store.ListOptions) (store.ListPage, error)
}

// owners.go
const (
	ownerScanPageSize = 500
	ownerScanMaxPages = 20
)
var errOwnerScanTruncated = errors.New("nex: owner scan hit its page cap")
func isLiveExecution(e store.Execution) bool          // ArchivedAt == 0 && State != StateTerminated
func executionIsFor(e store.Execution, sid string) bool // sid != "" && (SessionID == sid || ResumeSessionID == sid)
// liveWorkersFor returns S's live executions, newest first (CreatedAt desc, then ID desc).
// On errOwnerScanTruncated it still returns what it found.
func (m *Module) liveWorkersFor(parent context.Context, sid string) ([]store.Execution, error)
```

- Fakes (test-only):
  - `fakeNexService` gains `Terminate` with `terminateCalls []execution.TerminateRequest`, `terminateErr error` and `onTerminate func(execution.TerminateRequest)`. `onTerminate` lets a test flip the fake store's row to `terminated`.
  - `fakeNexService` gains `RenewLease` with `renewCalls []struct{ExecutionID, LeaseID, PrincipalID string}` and `renewErr error` (Task 5 consumes it).
  - `fakeNexStore` gains `listRows []store.Execution` and `listErr error`. Its `List` honours `IncludeArchived`, `Cursor` (id >) and `Limit`, returns rows in id order, and sets `NextCursor` to the last returned id only when more remain. Also add `listCalls int`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/module/nex/owners_test.go
package nex

func row(id, state string, archived bool, sid, resume string, created int64) store.Execution {
	e := store.Execution{ID: id, State: store.State(state), SessionID: sid, ResumeSessionID: resume, CreatedAt: created}
	if archived {
		e.ArchivedAt = 1
	}
	return e
}

func TestIsLiveExecution(t *testing.T) {
	cases := map[string]struct {
		e    store.Execution
		want bool
	}{
		"idle":                {row("a", "idle", false, "", "", 1), true},
		"running":             {row("a", "running", false, "", "", 1), true},
		"queued":              {row("a", "queued", false, "", "", 1), true},
		"failed":              {row("a", "failed", false, "", "", 1), true},
		"rejected":            {row("a", "rejected", false, "", "", 1), true},
		"terminated":          {row("a", "terminated", false, "", "", 1), false},
		"idle archived":       {row("a", "idle", true, "", "", 1), false},
		"terminated archived": {row("a", "terminated", true, "", "", 1), false},
	}
	for name, c := range cases {
		if got := isLiveExecution(c.e); got != c.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

func TestLiveWorkersFor_PagesFiltersAndOrders(t *testing.T) {
	env := newHandoffEnv(t) // existing fixture; exposes the fake store
	var rows []store.Execution
	for i := 0; i < ownerScanPageSize+3; i++ { // forces a second page
		rows = append(rows, row(fmt.Sprintf("01%04d", i), "terminated", false, "OTHER", "", int64(i)))
	}
	rows = append(rows,
		row("09a", "idle", false, "S", "", 100),
		row("09b", "rejected", false, "", "S", 200), // matched by resume_session_id
		row("09c", "terminated", false, "S", "", 300),
		row("09d", "idle", true, "S", "", 400), // archived: List never returns it
	)
	env.store.listRows = rows

	got, err := env.m.liveWorkersFor(context.Background(), "S")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, e := range got {
		ids = append(ids, e.ID)
	}
	if !reflect.DeepEqual(ids, []string{"09b", "09a"}) {
		t.Fatalf("ids = %v, want [09b 09a] (newest first, live only)", ids)
	}
	if env.store.listCalls < 2 {
		t.Fatalf("listCalls = %d, want ≥ 2 (cursor followed)", env.store.listCalls)
	}
}

func TestLiveWorkersFor_CapAndErrors(t *testing.T) {
	env := newHandoffEnv(t)
	env.store.listRows = make([]store.Execution, ownerScanPageSize*ownerScanMaxPages+1)
	for i := range env.store.listRows {
		env.store.listRows[i] = row(fmt.Sprintf("%06d", i), "idle", false, "S", "", int64(i))
	}
	got, err := env.m.liveWorkersFor(context.Background(), "S")
	if !errors.Is(err, errOwnerScanTruncated) || len(got) != ownerScanPageSize*ownerScanMaxPages {
		t.Fatalf("got %d, err %v", len(got), err)
	}

	env.store.listRows, env.store.listErr = nil, errors.New("db down")
	if _, err := env.m.liveWorkersFor(context.Background(), "S"); err == nil {
		t.Fatal("want the store error")
	}
	if got, err := env.m.liveWorkersFor(context.Background(), ""); err != nil || got != nil {
		t.Fatalf("empty sid: %v %v", got, err)
	}
}
```

The env field names (`env.m`, `env.store`) follow `newHandoffEnv` (`handoff_fakes_test.go:485-538`) and `takebackEnv` (`takeback_test.go:34`). If the fake store lives on `takebackEnv`, use that fixture.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/module/nex/ -run 'IsLiveExecution|LiveWorkersFor'`
Expected: compile failure (undefined).

- [ ] **Step 3: Implement**

```go
// internal/module/nex/owners.go
package nex

// Conversation-entity ownership (spec §4.2, D1): which executions are S's
// live worker stints.

func isLiveExecution(e store.Execution) bool {
	return e.ArchivedAt == 0 && e.State != store.StateTerminated
}

func executionIsFor(e store.Execution, sid string) bool {
	return sid != "" && (e.SessionID == sid || e.ResumeSessionID == sid)
}

func (m *Module) liveWorkersFor(parent context.Context, sid string) ([]store.Execution, error) {
	if sid == "" {
		return nil, nil
	}
	var out []store.Execution
	cursor := ""
	for page := 0; page < ownerScanMaxPages; page++ {
		ctx, cancel := detachedContext(parent, m.engineOpTimeout)
		res, err := m.sys.store.List(ctx, store.ListOptions{Cursor: cursor, Limit: ownerScanPageSize})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("nex: listing executions: %w", err)
		}
		for _, e := range res.Items {
			if executionIsFor(e, sid) && isLiveExecution(e) {
				out = append(out, e)
			}
		}
		if res.NextCursor == "" || res.NextCursor == cursor {
			sortNewestFirst(out)
			return out, nil
		}
		cursor = res.NextCursor
	}
	sortNewestFirst(out)
	return out, errOwnerScanTruncated
}

func sortNewestFirst(es []store.Execution) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].CreatedAt != es[j].CreatedAt {
			return es[i].CreatedAt > es[j].CreatedAt
		}
		return es[i].ID > es[j].ID
	})
}
```

Widen `engine_iface.go` as above, and keep the `var _ nexService = (*execution.Service)(nil)` assertions. They prove `Terminate` / `List` match v0.16.1 verbatim.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/module/nex/`
Expected: PASS (the import-boundary and deps tests included).

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/nex/engine_iface.go internal/module/nex/owners.go internal/module/nex/owners_test.go internal/module/nex/handoff_fakes_test.go -m "feat(daemon): find a Claude session's live worker stints

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 5: Control lease and `exitWorker`

**Files:**
- Create: `internal/module/nex/control.go`
- Create: `internal/module/nex/exit.go` (the exit logic; Task 6 adds the handler to the same file)
- Create: `internal/module/nex/exit_test.go`
- Modify: `internal/module/nex/handoff.go:20-35, 76-98` (`defaultEngineTerminateTimeout`, field default)
- Modify: `internal/module/nex/module.go` (field `engineTerminateTimeout time.Duration`)
- Modify: `internal/module/nex/takeback.go` (add `terminateExecution` next to the other engine wrappers, ~:350)

**Interfaces:**
- Produces:

```go
// control.go
type control struct {
	LeaseID, PrincipalID string
	release              func() // releases a lease this call acquired; no-op for a caller's or a borrowed lease
}
func noRelease() {}
func (m *Module) isPdxPrincipal(p string) bool // p == "pdx:"+HostID || prefix "pdx:"+HostID+"/"
// takeControl: caller lease → acquired lease → borrowed pdx holder's lease; non-pdx holder → 409 held_by {principal}.
func (m *Module) takeControl(parent context.Context, execID, callerLease, principal string) (control, *handoffError)
// renewControl extends ctl's lease to a full TTL right before a transfer's resume, so the send fence (D5) outlives
// create + resume (≤ rollbackWait 15 s, far under the 120 s TTL). A lease that expired or changed hands since
// takeControl (ErrLeaseExpired / ErrLeaseMismatch / ErrLeaseRequired) is re-taken once with takeControl(…, "", principal);
// the old ctl is released first. Returns the control to use from here on.
func (m *Module) renewControl(parent context.Context, execID string, ctl control, principal string) (control, *handoffError)
var nowMs = func() int64 { return time.Now().UnixMilli() } // test seam

// exit.go
type exitOutcome struct {
	Terminated, Archived bool
	State                store.State
}
func (o exitOutcome) Exited() bool { return o.Terminated || o.Archived }
// exitWorker runs spec §5 for one execution. ctl == nil: the call takes (and releases) its own control when it needs one.
func (m *Module) exitWorker(parent context.Context, exec store.Execution, ctl *control, principal string) (exitOutcome, *handoffError)

// takeback.go
func (m *Module) terminateExecution(parent context.Context, req execution.TerminateRequest) error // under engineTerminateTimeout
```

- Steps by state (spec §5, D4):

  | Row | Terminate | Archive | Result |
  |---|---|---|---|
  | `running` | under control | always attempted (D4: a failed terminate does not block it) | Nexen refuses to archive a row that is still running; then the **terminate** error is returned. A turn that ended meanwhile archives, and the row counts as exited |
  | `idle` / `queued` | under control | always (blocks writes even if terminate failed) | exited if either worked |
  | `failed` / `rejected` | — | yes | |
  | `terminated`, unarchived | — | yes (the D16 retry) | |
  | archived and terminated | — | — | `{true, true}` and no engine call |
  | archived, `idle` (legacy take-to-terminal shape) | under control | — | |

  Further rules:
  - A non-pdx lease holder → 409 `held_by` before anything changes.
  - Terminate's `store.ErrExecutionTerminal` (it ended on its own) → re-read and archive.
  - Error codes: `execution.ErrInterruptUnconfirmed` → 504 `interrupt_unconfirmed`; `execution.ErrTerminateContended` → 409 `terminate_contended`; any other terminate error → 500 `terminate_failed`; an archive error with nothing terminated → 500 `archive_failed`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/module/nex/exit_test.go
package nex

const pdxOther = "pdx:" + testHostID + "/other-tab" // testHostID: the HostID newHandoffEnv configures

func TestTakeControl(t *testing.T) {
	t.Run("caller lease is used as is", func(t *testing.T) {
		env := newHandoffEnv(t)
		ctl, herr := env.m.takeControl(context.Background(), "E1", "L-caller", "pdx:"+testHostID)
		if herr != nil || ctl.LeaseID != "L-caller" {
			t.Fatalf("%+v %v", ctl, herr)
		}
		ctl.release()
		if len(env.svc.releaseCalls) != 0 {
			t.Fatal("a caller's lease must never be released")
		}
	})
	t.Run("acquires and releases its own", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.acquireLeaseResult = store.Lease{ID: "L-own"}
		ctl, herr := env.m.takeControl(context.Background(), "E1", "", "pdx:"+testHostID)
		if herr != nil || ctl.LeaseID != "L-own" {
			t.Fatalf("%+v %v", ctl, herr)
		}
		ctl.release()
		if len(env.svc.releaseCalls) != 1 {
			t.Fatal("own lease not released")
		}
	})
	t.Run("borrows a pdx holder's lease", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.acquireLeaseErr = store.ErrLeaseHeld
		env.store.script(store.Execution{ID: "E1", State: store.StateIdle, LeaseID: "L-b", LeasePrincipalID: pdxOther, LeaseExpiresAt: nowMs() + 60_000})
		ctl, herr := env.m.takeControl(context.Background(), "E1", "", "pdx:"+testHostID)
		if herr != nil || ctl.LeaseID != "L-b" || ctl.PrincipalID != pdxOther {
			t.Fatalf("%+v %v", ctl, herr)
		}
		ctl.release()
		if len(env.svc.releaseCalls) != 0 {
			t.Fatal("a borrowed lease must never be released")
		}
	})
	t.Run("refuses a non-pdx holder", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.acquireLeaseErr = store.ErrLeaseHeld
		env.store.script(store.Execution{ID: "E1", State: store.StateIdle, LeaseID: "L-p", LeasePrincipalID: "ploom:agent-7", LeaseExpiresAt: nowMs() + 60_000})
		_, herr := env.m.takeControl(context.Background(), "E1", "", "pdx:"+testHostID)
		if herr == nil || herr.status != http.StatusConflict || herr.code != "held_by" || herr.detail["principal"] != "ploom:agent-7" {
			t.Fatalf("herr = %+v", herr)
		}
	})
	t.Run("renewControl extends the lease under its holder", func(t *testing.T) {
		env := newHandoffEnv(t)
		ctl := control{LeaseID: "L-b", PrincipalID: pdxOther, release: noRelease}
		got, herr := env.m.renewControl(context.Background(), "E1", ctl, "pdx:"+testHostID)
		if herr != nil || got.LeaseID != "L-b" || len(env.svc.renewCalls) != 1 || env.svc.renewCalls[0].PrincipalID != pdxOther {
			t.Fatalf("%+v %v %+v", got, herr, env.svc.renewCalls)
		}
	})
	t.Run("renewControl re-takes an expired lease once", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.renewErr = store.ErrLeaseExpired
		env.svc.acquireLeaseResult = store.Lease{ID: "L-new"}
		released := false
		got, herr := env.m.renewControl(context.Background(), "E1", control{LeaseID: "L-old", PrincipalID: "pdx:" + testHostID, release: func() { released = true }}, "pdx:"+testHostID)
		if herr != nil || got.LeaseID != "L-new" || !released {
			t.Fatalf("%+v %v released=%v", got, herr, released)
		}
	})
	t.Run("a pdx principal of another host is not ours", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.acquireLeaseErr = store.ErrLeaseHeld
		env.store.script(store.Execution{ID: "E1", LeaseID: "L-x", LeasePrincipalID: "pdx:" + testHostID + "x", LeaseExpiresAt: nowMs() + 60_000})
		if _, herr := env.m.takeControl(context.Background(), "E1", "", "pdx:"+testHostID); herr == nil || herr.code != "held_by" {
			t.Fatalf("herr = %+v", herr)
		}
	})
}

func TestExitWorker_ByState(t *testing.T) {
	cases := []struct {
		name               string
		row                store.Execution
		wantTerminateCalls int
		wantArchiveCalls   int
		want               exitOutcome
	}{
		{"idle", store.Execution{ID: "E", State: store.StateIdle}, 1, 1, exitOutcome{true, true, store.StateTerminated}},
		{"running", store.Execution{ID: "E", State: store.StateRunning}, 1, 1, exitOutcome{true, true, store.StateTerminated}},
		{"queued", store.Execution{ID: "E", State: store.StateQueued}, 1, 1, exitOutcome{true, true, store.StateTerminated}},
		{"failed", store.Execution{ID: "E", State: store.StateFailed}, 0, 1, exitOutcome{false, true, store.StateFailed}},
		{"rejected", store.Execution{ID: "E", State: store.StateRejected}, 0, 1, exitOutcome{false, true, store.StateRejected}},
		{"terminated unarchived", store.Execution{ID: "E", State: store.StateTerminated}, 0, 1, exitOutcome{true, true, store.StateTerminated}},
		{"already exited", store.Execution{ID: "E", State: store.StateTerminated, ArchivedAt: 9}, 0, 0, exitOutcome{true, true, store.StateTerminated}},
		{"legacy idle+archived", store.Execution{ID: "E", State: store.StateIdle, ArchivedAt: 9}, 1, 0, exitOutcome{true, true, store.StateTerminated}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newHandoffEnv(t)
			env.svc.acquireLeaseResult = store.Lease{ID: "L-own"}
			out, herr := env.m.exitWorker(context.Background(), c.row, nil, "pdx:"+testHostID)
			if herr != nil {
				t.Fatalf("herr = %+v", herr)
			}
			if out != c.want {
				t.Errorf("out = %+v, want %+v", out, c.want)
			}
			if got := len(env.svc.terminateCalls); got != c.wantTerminateCalls {
				t.Errorf("terminate calls = %d", got)
			}
			if got := len(env.svc.ArchiveCalls()); got != c.wantArchiveCalls {
				t.Errorf("archive calls = %d", got)
			}
			if c.wantTerminateCalls == 1 && env.svc.terminateCalls[0].LeaseID != "L-own" {
				t.Errorf("terminate lease = %q", env.svc.terminateCalls[0].LeaseID)
			}
		})
	}
}

func TestExitWorker_Failures(t *testing.T) {
	t.Run("idle: terminate fails, archive still blocks writes", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.terminateErr = errors.New("boom")
		out, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, nil, "pdx:"+testHostID)
		if herr != nil || !out.Exited() || out.Terminated || !out.Archived {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
	})
	t.Run("running: terminate fails, archive still attempted; refused while running → the terminate error", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.terminateErr = execution.ErrInterruptUnconfirmed
		env.svc.archiveErr = execution.ErrArchiveWhileRunning
		_, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateRunning}, nil, "pdx:"+testHostID)
		if herr == nil || herr.status != http.StatusGatewayTimeout || herr.code != "interrupt_unconfirmed" {
			t.Fatalf("herr = %+v", herr)
		}
		if len(env.svc.ArchiveCalls()) != 1 {
			t.Fatal("D4: a failed terminate must not skip the archive attempt")
		}
	})
	t.Run("running: terminate fails but the turn ended meanwhile → archived, exited", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.terminateErr = execution.ErrTerminateContended
		out, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateRunning}, nil, "pdx:"+testHostID)
		if herr != nil || !out.Exited() || !out.Archived || out.Terminated {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
	})
	t.Run("terminated but archive fails → still exited", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.archiveErr = errors.New("db busy")
		out, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, nil, "pdx:"+testHostID)
		if herr != nil || !out.Exited() || out.Archived {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
	})
	t.Run("failed row, archive fails → archive_failed", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.archiveErr = errors.New("db busy")
		_, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateFailed}, nil, "pdx:"+testHostID)
		if herr == nil || herr.code != "archive_failed" {
			t.Fatalf("herr = %+v", herr)
		}
	})
	t.Run("non-pdx holder → held_by, nothing changed", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.acquireLeaseErr = store.ErrLeaseHeld
		env.store.script(store.Execution{ID: "E", State: store.StateIdle, LeaseID: "L-p", LeasePrincipalID: "ploom:agent-7", LeaseExpiresAt: nowMs() + 60_000})
		_, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, nil, "pdx:"+testHostID)
		if herr == nil || herr.code != "held_by" || len(env.svc.terminateCalls) != 0 || len(env.svc.ArchiveCalls()) != 0 {
			t.Fatalf("herr=%+v terminate=%d archive=%d", herr, len(env.svc.terminateCalls), len(env.svc.ArchiveCalls()))
		}
	})
	t.Run("ended on its own meanwhile → re-read and archive", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.terminateErr = store.ErrExecutionTerminal
		env.store.script(store.Execution{ID: "E", State: store.StateFailed})
		out, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, nil, "pdx:"+testHostID)
		if herr != nil || out.State != store.StateFailed || !out.Archived || out.Terminated {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
	})
	t.Run("given control is used and not released", func(t *testing.T) {
		env := newHandoffEnv(t)
		released := false
		ctl := &control{LeaseID: "L-t", PrincipalID: "pdx:" + testHostID, release: func() { released = true }}
		if _, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, ctl, "pdx:"+testHostID); herr != nil {
			t.Fatal(herr)
		}
		if env.svc.terminateCalls[0].LeaseID != "L-t" || released || len(env.svc.acquireCalls) != 0 {
			t.Fatal("exitWorker must act under the given control without acquiring or releasing")
		}
	})
}
```

Knob names (`acquireLeaseResult`, `acquireLeaseErr`, `archiveErr`, `releaseCalls`, `acquireCalls`, `store.script`) must match what `fakeNexService` / `fakeNexStore` already call them. Add any knob that is missing to the fakes in this task. `testHostID` is the HostID `newHandoffEnv` puts in `opts.Config`; define the constant if the fixture inlines it.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/module/nex/ -run 'TakeControl|ExitWorker'`
Expected: compile failure.

- [ ] **Step 3: Implement**

```go
// internal/module/nex/control.go
package nex

// The lease an orchestration acts under (conversation entity D4/D5).
// Nexen fences every write — send, interrupt, terminate — behind one
// control lease per execution, and an open worker pane keeps renewing its
// own. The daemon mints every pdx principal, so it may act under a pdx
// holder's lease; it never overrides anyone else's.

type control struct {
	LeaseID, PrincipalID string
	release              func()
}

func noRelease() {}

var nowMs = func() int64 { return time.Now().UnixMilli() }

func (m *Module) isPdxPrincipal(p string) bool {
	base := "pdx:" + m.opts.Config.HostID
	return p == base || strings.HasPrefix(p, base+"/")
}

func (m *Module) takeControl(parent context.Context, execID, callerLease, principal string) (control, *handoffError) {
	if callerLease != "" {
		return control{LeaseID: callerLease, PrincipalID: principal, release: noRelease}, nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		lease, err := m.acquireLease(parent, execID, principal)
		if err == nil {
			id := lease.ID
			return control{LeaseID: id, PrincipalID: principal, release: func() {
				if err := m.releaseLease(parent, execID, id, principal); err != nil {
					m.logf("nex: releasing lease %s on %s: %v", id, execID, err)
				}
			}}, nil
		}
		if !errors.Is(err, store.ErrLeaseHeld) {
			return control{}, &handoffError{http.StatusInternalServerError, "lease_error", "acquiring lease: " + err.Error(), nil}
		}
		row, gerr := m.getExecution(parent, execID)
		if gerr != nil {
			return control{}, &handoffError{http.StatusInternalServerError, "store_error", "re-reading execution: " + gerr.Error(), nil}
		}
		if row.LeaseID == "" || row.LeaseExpiresAt <= nowMs() {
			continue // released or expired between the two reads
		}
		if !m.isPdxPrincipal(row.LeasePrincipalID) {
			return control{}, &handoffError{http.StatusConflict, "held_by", "execution lease is held by " + row.LeasePrincipalID,
				map[string]any{"principal": row.LeasePrincipalID}}
		}
		return control{LeaseID: row.LeaseID, PrincipalID: row.LeasePrincipalID, release: noRelease}, nil
	}
	return control{}, &handoffError{http.StatusConflict, "held_by", "execution lease kept changing hands", nil}
}

func (m *Module) renewControl(parent context.Context, execID string, ctl control, principal string) (control, *handoffError) {
	ctx, cancel := detachedContext(parent, m.engineOpTimeout)
	_, err := m.sys.service.RenewLease(ctx, execID, ctl.LeaseID, ctl.PrincipalID)
	cancel()
	if err == nil {
		return ctl, nil
	}
	if !errors.Is(err, store.ErrLeaseExpired) && !errors.Is(err, store.ErrLeaseMismatch) && !errors.Is(err, store.ErrLeaseRequired) {
		return ctl, &handoffError{http.StatusInternalServerError, "lease_error", "renewing lease: " + err.Error(), nil}
	}
	ctl.release()
	return m.takeControl(parent, execID, "", principal)
}

// ctlPtr hands exitWorker the transfer's control, or nil when the transfer
// held none (an ended row): exitWorker then takes its own if it needs one.
func ctlPtr(c control) *control {
	if c.LeaseID == "" {
		return nil
	}
	return &c
}
```

```go
// internal/module/nex/exit.go
package nex

// Exit — the one worker action (conversation entity spec §5, D4, D16).

func needsTerminate(s store.State) bool {
	return s == store.StateQueued || s == store.StateRunning || s == store.StateIdle
}

func (m *Module) exitWorker(parent context.Context, exec store.Execution, ctl *control, principal string) (exitOutcome, *handoffError) {
	out := exitOutcome{Terminated: exec.State == store.StateTerminated, Archived: exec.ArchivedAt != 0, State: exec.State}
	var termErr *handoffError // a failed terminate; reported only if the archive cannot stand in for it
	if needsTerminate(exec.State) {
		c := ctl
		if c == nil {
			own, herr := m.takeControl(parent, exec.ID, "", principal)
			if herr != nil {
				if herr.code == "held_by" {
					return out, herr // D4: a non-pdx holder is never overridden — nothing changes
				}
				termErr = herr
				m.logf("nex: exit %s: no control (%s); archiving only", exec.ID, herr.msg)
			} else {
				defer own.release()
				c = &own
			}
		}
		if c != nil {
			err := m.terminateExecution(parent, execution.TerminateRequest{ExecutionID: exec.ID, LeaseID: c.LeaseID, PrincipalID: c.PrincipalID})
			switch {
			case err == nil:
				out.Terminated, out.State = true, store.StateTerminated
			case errors.Is(err, store.ErrExecutionTerminal):
				if fresh, gerr := m.getExecution(parent, exec.ID); gerr == nil {
					out.State = fresh.State
					out.Terminated = fresh.State == store.StateTerminated
					out.Archived = out.Archived || fresh.ArchivedAt != 0
				}
			default:
				termErr = terminateError(err)
				m.logf("nex: exit %s: terminate: %v; archiving anyway (D4)", exec.ID, err)
			}
		}
	}
	if !out.Archived {
		err := m.archiveExecution(parent, execution.ArchiveRequest{ExecutionID: exec.ID, PrincipalID: principal, Archived: true})
		switch {
		case err == nil:
			out.Archived = true
		case out.Terminated:
			m.logf("nex: exit %s: archive after terminate: %v (exited; retried on the next exit)", exec.ID, err)
		case termErr != nil:
			// Typically archive_while_running: the turn the terminate could not
			// stop is still running. The terminate's reason is the useful one.
			return out, termErr
		default:
			return out, &handoffError{http.StatusInternalServerError, "archive_failed", "archiving execution: " + err.Error(),
				map[string]any{"execution_id": exec.ID}}
		}
	}
	return out, nil
}

func terminateError(err error) *handoffError {
	switch {
	case errors.Is(err, execution.ErrInterruptUnconfirmed):
		return &handoffError{http.StatusGatewayTimeout, "interrupt_unconfirmed", "interrupt not confirmed: " + err.Error(), nil}
	case errors.Is(err, execution.ErrTerminateContended):
		return &handoffError{http.StatusConflict, "terminate_contended", err.Error(), nil}
	default:
		return &handoffError{http.StatusInternalServerError, "terminate_failed", "terminating execution: " + err.Error(), nil}
	}
}
```

In `handoff.go`, add `defaultEngineTerminateTimeout = 3*defaultEngineInterruptTimeout + defaultEngineOpTimeout`, because Terminate loops stall → interrupt → write up to 3 times. Add the `engineTerminateTimeout` field and its `applyHandoffDefaults` branch. In `takeback.go`:

```go
func (m *Module) terminateExecution(parent context.Context, req execution.TerminateRequest) error {
	ctx, cancel := detachedContext(parent, m.engineTerminateTimeout)
	defer cancel()
	return m.sys.service.Terminate(ctx, req)
}
```

Note: the legacy idle+archived case reaches terminate and skips the archive, because `out.Archived` starts true. That matches the table.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/module/nex/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/nex/control.go internal/module/nex/exit.go internal/module/nex/exit_test.go internal/module/nex/handoff.go internal/module/nex/module.go internal/module/nex/takeback.go internal/module/nex/handoff_fakes_test.go -m "feat(daemon): exit a worker — terminate and archive under a held or borrowed lease

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 6: `POST /api/nex/executions/{id}/exit`

**Files:**
- Modify: `internal/module/nex/exit.go` (handler)
- Modify: `internal/module/nex/module.go:306-318` (`RegisterRoutes`)
- Test: `internal/module/nex/exit_test.go` (append)

**Interfaces:**
- Produces:
  - `POST /api/nex/executions/{id}/exit`. The body `{"lease_id"?: string}` may be empty.
  - 200 → `{"exited": bool, "terminated": bool, "archived": bool, "state": string}`.
  - Errors: 503 `nex_unavailable`; 400 `malformed_body`; 409 `transfer_in_progress` (the `exec:<id>` lock is held); 404 `execution_not_found`; 500 `store_error`; plus `exitWorker`'s codes.
  - Mounted whether or not the engine assembled, like take-to-terminal.

- [ ] **Step 1: Write the failing tests**

```go
func TestExitEndpoint(t *testing.T) {
	t.Run("idle → 200 exited", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.store.script(store.Execution{ID: "E1", State: store.StateIdle})
		env.svc.acquireLeaseResult = store.Lease{ID: "L-own"}
		rec := env.post(t, "/api/nex/executions/E1/exit", ``) // empty body
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["exited"] != true || body["terminated"] != true || body["archived"] != true || body["state"] != "terminated" {
			t.Fatalf("body = %v", body)
		}
	})
	t.Run("caller lease is used", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.store.script(store.Execution{ID: "E1", State: store.StateIdle})
		rec := env.post(t, "/api/nex/executions/E1/exit", `{"lease_id":"L-mine"}`)
		if rec.Code != 200 || env.svc.terminateCalls[0].LeaseID != "L-mine" || len(env.svc.acquireCalls) != 0 {
			t.Fatalf("%d calls=%+v", rec.Code, env.svc.terminateCalls)
		}
	})
	t.Run("lock held → 409 transfer_in_progress", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.m.locks.TryLock(takeToTerminalLockKey("E1"))
		rec := env.post(t, "/api/nex/executions/E1/exit", ``)
		assertErrorCode(t, rec, 409, "transfer_in_progress")
	})
	t.Run("missing → 404", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.store.scriptErr(store.ErrNotFound)
		assertErrorCode(t, env.post(t, "/api/nex/executions/NOPE/exit", ``), 404, "execution_not_found")
	})
	t.Run("idempotent second call makes no engine call", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.store.script(store.Execution{ID: "E1", State: store.StateTerminated, ArchivedAt: 5})
		rec := env.post(t, "/api/nex/executions/E1/exit", ``)
		if rec.Code != 200 || len(env.svc.terminateCalls)+len(env.svc.ArchiveCalls()) != 0 {
			t.Fatalf("%d", rec.Code)
		}
	})
	t.Run("engine unavailable → 503", func(t *testing.T) {
		env := newUnavailableEnv(t) // the fixture take-to-terminal's 503 test uses
		assertErrorCode(t, env.post(t, "/api/nex/executions/E1/exit", ``), 503, "nex_unavailable")
	})
}
```

Use the fixture's real request helper and error assertion (whatever `take_to_terminal_test.go` uses to POST and to check `code`). The helper names above are placeholders for those exact functions.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/module/nex/ -run TestExitEndpoint`
Expected: FAIL (404 from the mux, no route).

- [ ] **Step 3: Implement**

```go
// internal/module/nex/exit.go
type exitRequest struct {
	LeaseID string `json:"lease_id,omitempty"`
}

// handleExitWorker: POST /api/nex/executions/{id}/exit (spec §5, D4).
func (m *Module) handleExitWorker(w http.ResponseWriter, r *http.Request) {
	execID := r.PathValue("id")
	if m.sys.service == nil || m.sys.store == nil || m.opts.Config == nil {
		msg := "nex engine unavailable"
		if m.initErr != nil {
			msg = m.initErr.Error()
		}
		writeHandoffError(w, http.StatusServiceUnavailable, "nex_unavailable", msg, nil)
		return
	}
	principal, err := m.principal(r)
	if err != nil {
		writeHandoffError(w, http.StatusInternalServerError, "principal_unresolved", err.Error(), nil)
		return
	}
	var body exitRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeHandoffError(w, http.StatusBadRequest, "malformed_body", "invalid request body: "+err.Error(), nil)
		return
	}
	lockKey := takeToTerminalLockKey(execID)
	if !m.locks.TryLock(lockKey) {
		writeHandoffError(w, http.StatusConflict, "transfer_in_progress", "this worker is being moved or exited already", nil)
		return
	}
	defer m.locks.Unlock(lockKey)

	exec, err := m.getExecution(r.Context(), execID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeHandoffError(w, http.StatusNotFound, "execution_not_found", "execution not found", nil)
			return
		}
		writeHandoffError(w, http.StatusInternalServerError, "store_error", "reading execution: "+err.Error(), nil)
		return
	}
	var ctl *control
	if body.LeaseID != "" {
		ctl = &control{LeaseID: body.LeaseID, PrincipalID: principal, release: noRelease}
	}
	out, herr := m.exitWorker(r.Context(), exec, ctl, principal)
	if herr != nil {
		herr.write(w)
		return
	}
	m.logf("nex: exit %s → terminated=%v archived=%v (%s)", execID, out.Terminated, out.Archived, out.State)
	writeJSON(w, http.StatusOK, map[string]any{
		"exited": out.Exited(), "terminated": out.Terminated, "archived": out.Archived, "state": string(out.State),
	})
}
```

In `RegisterRoutes`, next to take-to-terminal: `mux.HandleFunc("POST "+RoutePrefix+"/executions/{id}/exit", m.handleExitWorker)`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/module/nex/ && go vet ./internal/module/nex/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/nex/exit.go internal/module/nex/exit_test.go internal/module/nex/module.go -m "feat(daemon): POST /api/nex/executions/{id}/exit

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

**PR P1a-2:** `feat(daemon): conversation entity P1a-2 — exit a worker`. Then codex R1 + R2.

---

## Phase P1a-3 — nex: owner check, take-to-terminal, take-back

### Task 7: `sid:<S>` lock key, `TerminalSessions` provider, `checkOwners`

**Files:**
- Modify: `internal/module/nex/owners.go` (`sidLockKey`, `checkOwners`)
- Modify: `internal/module/nex/module.go` (field `terminals agent.TerminalSessions`; `resolveProviders` resolves `agent.TerminalSessionsKey`)
- Modify: `internal/module/nex/handoff_fakes_test.go` (`stubTerminals`, wired by `newHandoffEnv`)
- Modify: `internal/module/nex/deps_test.go:44-82` (register a stub under `agent.TerminalSessionsKey`, plus a "missing service" case)
- Test: `internal/module/nex/owners_test.go` (append)

**Interfaces:**
- Consumes: `agent.TerminalSessions`, `agent.TerminalSession`, `agent.SessionStartEvent`, `agent.TerminalSessionsKey` (Tasks 2–3); `liveWorkersFor` (Task 4).
- Produces:

```go
func sidLockKey(sid string) string { return "sid:" + sid }
// checkOwners: nil when nothing but the transferred owner holds S. allowExec / allowPane name that owner ("" = none).
// 409 session_owned {owner: "terminal", session_id, tmux_pane_id} | {owner: "worker", session_id, execution_id, state};
// 503 owner_check_failed when either lookup errs (a truncated worker scan counts as an error).
func (m *Module) checkOwners(parent context.Context, sid, allowExec, allowPane string) *handoffError

// test fake
type stubTerminals struct {
	mu         sync.Mutex
	live       map[string][]agent.TerminalSession // by session id
	err        error
	subscribed func(agent.SessionStartEvent)
}
```

- [ ] **Step 1: Write the failing tests**

```go
func TestCheckOwners(t *testing.T) {
	ts := func(pane string, verified bool) agent.TerminalSession {
		return agent.TerminalSession{FrameID: "f" + pane, PaneID: pane, AgentType: "cc", SessionID: "S", Verified: verified}
	}
	t.Run("free", func(t *testing.T) {
		env := newHandoffEnv(t)
		if herr := env.m.checkOwners(context.Background(), "S", "", ""); herr != nil {
			t.Fatal(herr)
		}
	})
	t.Run("terminal elsewhere (verified or not)", func(t *testing.T) {
		for _, v := range []bool{true, false} {
			env := newHandoffEnv(t)
			env.terminals.live = map[string][]agent.TerminalSession{"S": {ts("%9", v)}}
			herr := env.m.checkOwners(context.Background(), "S", "", "")
			if herr == nil || herr.status != 409 || herr.code != "session_owned" || herr.detail["owner"] != "terminal" || herr.detail["tmux_pane_id"] != "%9" {
				t.Fatalf("verified=%v herr=%+v", v, herr)
			}
		}
	})
	t.Run("terminal in the allowed pane", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.terminals.live = map[string][]agent.TerminalSession{"S": {ts("%1", true)}}
		if herr := env.m.checkOwners(context.Background(), "S", "", "%1"); herr != nil {
			t.Fatal(herr)
		}
	})
	t.Run("live worker other than the allowed one", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.store.listRows = []store.Execution{row("E1", "idle", false, "S", "", 1), row("E2", "failed", false, "S", "", 2)}
		herr := env.m.checkOwners(context.Background(), "S", "E1", "")
		if herr == nil || herr.detail["owner"] != "worker" || herr.detail["execution_id"] != "E2" {
			t.Fatalf("herr = %+v", herr)
		}
		env.store.listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
		if herr := env.m.checkOwners(context.Background(), "S", "E1", ""); herr != nil {
			t.Fatal(herr)
		}
	})
	t.Run("lookup errors → 503", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.terminals.err = errors.New("db")
		if herr := env.m.checkOwners(context.Background(), "S", "", ""); herr == nil || herr.status != 503 || herr.code != "owner_check_failed" {
			t.Fatalf("herr = %+v", herr)
		}
		env.terminals.err = nil
		env.store.listErr = errors.New("db")
		if herr := env.m.checkOwners(context.Background(), "S", "", ""); herr == nil || herr.code != "owner_check_failed" {
			t.Fatalf("herr = %+v", herr)
		}
	})
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/module/nex/ -run TestCheckOwners`
Expected: compile failure.

- [ ] **Step 3: Implement**

```go
// internal/module/nex/owners.go
func sidLockKey(sid string) string { return "sid:" + sid }

func (m *Module) checkOwners(parent context.Context, sid, allowExec, allowPane string) *handoffError {
	ctx, cancel := detachedContext(parent, m.engineOpTimeout)
	terms, err := m.terminals.LiveBySessionID(ctx, "cc", sid)
	cancel()
	if err != nil {
		return &handoffError{http.StatusServiceUnavailable, "owner_check_failed", "checking terminal owners: " + err.Error(), map[string]any{"session_id": sid}}
	}
	for _, t := range terms {
		if allowPane != "" && t.PaneID == allowPane {
			continue
		}
		return &handoffError{http.StatusConflict, "session_owned", "this conversation is open in a terminal",
			map[string]any{"owner": "terminal", "session_id": sid, "tmux_pane_id": t.PaneID}}
	}
	workers, err := m.liveWorkersFor(parent, sid)
	if err != nil {
		return &handoffError{http.StatusServiceUnavailable, "owner_check_failed", "checking worker owners: " + err.Error(), map[string]any{"session_id": sid}}
	}
	for _, e := range workers {
		if e.ID == allowExec {
			continue
		}
		return &handoffError{http.StatusConflict, "session_owned", "this conversation already has a live worker",
			map[string]any{"owner": "worker", "session_id": sid, "execution_id": e.ID, "state": string(e.State)}}
	}
	return nil
}
```

`resolveProviders` resolves `agent.TerminalSessionsKey` into `m.terminals` with the same error shape as the other providers. Add `stubTerminals` (implementing `LiveBySessionID` from `live`/`err`, and `SubscribeSessionStart` storing `fn` in `subscribed` and returning an unsubscribe that nils it). Wire it in `newHandoffEnv` as `env.terminals`. Register it in `deps_test.go`'s registry setup, and add one case asserting Init fails with a clear message when it is missing.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/module/nex/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/nex/owners.go internal/module/nex/owners_test.go internal/module/nex/module.go internal/module/nex/handoff_fakes_test.go internal/module/nex/deps_test.go -m "feat(daemon): owner check for a Claude session (terminal or live worker)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 8: Take-to-terminal — owner check, held lease, resume first, then exit (D5, D6)

**Files:**
- Modify: `internal/module/nex/take_to_terminal.go` (handler steps 3–8 and the file comment)
- Modify: `internal/module/nex/takeback.go:36-38, 217-298` (`settled` gains `rejected`; `settleForResume` takes a `control` and no longer acquires or releases)
- Modify: `internal/module/nex/take_to_terminal_test.go` (rewrite the rollback tests at :466-554; add the cases below)
- Modify: `internal/module/nex/settle_test.go` (new `settleForResume` signature)

**Interfaces:**
- Consumes: `takeControl`, `exitWorker`, `checkOwners`, `sidLockKey`, `isLiveExecution`.
- Produces:
  - `func (m *Module) settleForResume(parent context.Context, exec store.Execution, ctl control) (store.Execution, string, *handoffError)`, which interrupts a running execution under `ctl`, re-reads it, and returns `(exec, sid)`. Error codes are unchanged except that `held_by` / `lease_error` move to `takeControl`.
  - Take-to-terminal 200 → `{session, session_id, archived, exited, exit_error?}`. `exit_error` is present only when the resume succeeded but the worker could not exit (terminate and archive both failed).
  - New 409s: `no_session_id` (now before any lock or preflight), `transfer_in_progress`, `session_owned`.
  - `execution_archived` is **no longer** returned: an exited execution is accepted (D6).
  - A resume failure detail no longer carries `unarchived`; it carries `exited: false`.

The new step order, which the handler comment must also state:
1. engine, then principal;
2. `exec:<id>` lock;
3. body;
4. read the row, then the provider check;
5. `sid` (409 `no_session_id`), then `queued` → 409 `execution_not_settled`;
6. `sid:<S>` lock (409 `transfer_in_progress`);
7. `checkOwners(sid, allowExec = exec.ID if live else "", "")`;
8. name / cwd preflights;
9. if the row is live and `running` or `idle`: `takeControl`, deferring `release`, then `settleForResume`, then `renewControl` (a full TTL for the fence);
10. create the session;
11. resume (on failure kill the session; nothing is exited);
12. if the row was live, `exitWorker(exec, &ctl or nil)`;
13. 200.

- [ ] **Step 1: Write the failing tests**

```go
// take_to_terminal_test.go — new / rewritten cases (use ttEnv / ttExec / reviveCCAfterKeysAt / assertNoSession)
func TestTakeToTerminal_ResumeThenExit_Idle(t *testing.T) {
	env := newTTEnv(t)
	env.store.script(ttExec(store.StateIdle)) // session id "S", cwd ok
	env.svc.acquireLeaseResult = store.Lease{ID: "L-t"}
	env.reviveCCAfterKeys()
	rec := env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// order: keys typed before terminate; terminate under the held lease; archive after.
	if env.keysSentAt().After(env.svc.terminateAt()) {
		t.Fatal("worker exited before the terminal resumed")
	}
	if env.svc.terminateCalls[0].LeaseID != "L-t" || len(env.svc.ArchiveCalls()) != 1 {
		t.Fatalf("terminate=%+v archive=%d", env.svc.terminateCalls, len(env.svc.ArchiveCalls()))
	}
	body := decode(t, rec)
	if body["exited"] != true || body["archived"] != true {
		t.Fatalf("body = %v", body)
	}
	if len(env.svc.releaseCalls) != 1 {
		t.Fatal("the transfer's own lease must be released after the exit")
	}
}

func TestTakeToTerminal_ResumeFails_NothingExited(t *testing.T) {
	env := newTTEnv(t)
	env.store.script(ttExec(store.StateIdle))
	// CC never comes up → cc_start_timeout
	rec := env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`)
	assertErrorCode(t, rec, 504, "cc_start_timeout")
	if len(env.svc.terminateCalls) != 0 || len(env.svc.ArchiveCalls()) != 0 {
		t.Fatal("a failed resume must exit nothing")
	}
	body := decode(t, rec)
	if body["exited"] != false || body["session_killed"] != true {
		t.Fatalf("body = %v", body)
	}
	if _, ok := body["unarchived"]; ok {
		t.Fatal("unarchived is gone")
	}
}

func TestTakeToTerminal_Running_InterruptsUnderControlThenExits(t *testing.T) {
	env := newTTEnv(t)
	scriptRunningThenIdle(env) // existing helper: Get → running, then idle after interrupt
	env.svc.acquireLeaseResult = store.Lease{ID: "L-t"}
	env.reviveCCAfterKeys()
	rec := env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`)
	if rec.Code != 200 || env.svc.interruptCalls[0].LeaseID != "L-t" || env.svc.terminateCalls[0].LeaseID != "L-t" {
		t.Fatalf("%d", rec.Code)
	}
}

func TestTakeToTerminal_ExitedExecutionIsRebuiltWithoutExit(t *testing.T) { // D6
	env := newTTEnv(t)
	e := ttExec(store.StateTerminated)
	e.ArchivedAt = 7
	env.store.script(e)
	env.reviveCCAfterKeys()
	rec := env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`)
	if rec.Code != 200 || len(env.svc.terminateCalls)+len(env.svc.ArchiveCalls())+len(env.svc.acquireCalls)+len(env.svc.interruptCalls) != 0 {
		t.Fatalf("%d: an exited execution needs no control and no exit", rec.Code)
	}
	if decode(t, rec)["exited"] != true {
		t.Fatal("exited must stay true")
	}
}

func TestTakeToTerminal_RejectedIsArchivedAfterResume(t *testing.T) { // D6
	env := newTTEnv(t)
	e := ttExec(store.StateRejected)
	e.SessionID, e.ResumeSessionID = "", "S"
	env.store.script(e)
	env.reviveCCAfterKeys()
	rec := env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`)
	if rec.Code != 200 || len(env.svc.terminateCalls) != 0 || len(env.svc.ArchiveCalls()) != 1 {
		t.Fatalf("%d", rec.Code)
	}
	if !strings.Contains(env.rawKeysText(), "claude --resume S") {
		t.Fatal("resume must use resume_session_id when session_id is empty")
	}
}

func TestTakeToTerminal_OwnerConflicts(t *testing.T) {
	t.Run("already in a terminal (second click after success)", func(t *testing.T) {
		env := newTTEnv(t)
		e := ttExec(store.StateTerminated)
		e.ArchivedAt = 7
		env.store.script(e)
		env.terminals.live = map[string][]agent.TerminalSession{"S": {{PaneID: "%4", SessionID: "S", AgentType: "cc", Verified: true}}}
		rec := env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`)
		assertErrorCode(t, rec, 409, "session_owned")
		assertNoSession(t, env, "proj-1")
	})
	t.Run("another live worker for S", func(t *testing.T) {
		env := newTTEnv(t)
		env.store.script(ttExec(store.StateIdle))
		env.store.listRows = []store.Execution{row("E1", "idle", false, "S", "", 1), row("E9", "idle", false, "S", "", 2)}
		assertErrorCode(t, env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`), 409, "session_owned")
	})
	t.Run("sid lock held", func(t *testing.T) {
		env := newTTEnv(t)
		env.store.script(ttExec(store.StateIdle))
		env.m.locks.TryLock(sidLockKey("S"))
		assertErrorCode(t, env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`), 409, "transfer_in_progress")
	})
	t.Run("non-pdx holder → held_by before any session", func(t *testing.T) {
		env := newTTEnv(t)
		e := ttExec(store.StateIdle)
		e.LeaseID, e.LeasePrincipalID, e.LeaseExpiresAt = "L-p", "ploom:agent-7", nowMs()+60_000
		env.store.script(e, e)
		env.svc.acquireLeaseErr = store.ErrLeaseHeld
		assertErrorCode(t, env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`), 409, "held_by")
		assertNoSession(t, env, "proj-1")
	})
	t.Run("pdx holder's lease is borrowed", func(t *testing.T) {
		env := newTTEnv(t)
		e := ttExec(store.StateIdle)
		e.LeaseID, e.LeasePrincipalID, e.LeaseExpiresAt = "L-b", "pdx:"+testHostID+"/tab2", nowMs()+60_000
		env.store.script(e, e)
		env.svc.acquireLeaseErr = store.ErrLeaseHeld
		env.reviveCCAfterKeys()
		rec := env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`)
		if rec.Code != 200 || env.svc.terminateCalls[0].LeaseID != "L-b" || len(env.svc.releaseCalls) != 0 {
			t.Fatalf("%d", rec.Code)
		}
	})
}
```

The env helper names (`newTTEnv`, `postTakeToTerminal`, `keysSentAt`, `terminateAt`, `decode`) are placeholders for what `take_to_terminal_test.go:37-110` already provides. Add the timestamp recorders to the fakes if they are missing; the "resume before exit" order is the point of Task 8. Delete the old "archive before resume" and "unarchive on failure" tests. Their replacements are `ResumeThenExit_Idle` and `ResumeFails_NothingExited`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/module/nex/ -run TestTakeToTerminal`
Expected: FAIL (the old order archives first; exited and rejected are refused).

- [ ] **Step 3: Implement**

Rewrite `settleForResume`:

```go
// settleForResume brings an already-read execution to a state a terminal may
// resume from: a running one is interrupted under ctl and re-read; the result
// must be settled and carry a session id. The caller holds ctl for the whole
// transfer (conversation entity D5) and releases it after the exit.
func (m *Module) settleForResume(parent context.Context, exec store.Execution, ctl control) (store.Execution, string, *handoffError) {
	if exec.State == store.StateRunning {
		_, err := m.interruptExecution(parent, execution.InterruptRequest{ExecutionID: exec.ID, LeaseID: ctl.LeaseID, PrincipalID: ctl.PrincipalID})
		switch {
		case err == nil, errors.Is(err, execution.ErrNoLiveTurn):
		case errors.Is(err, execution.ErrInterruptUnconfirmed):
			return exec, "", &handoffError{http.StatusGatewayTimeout, "interrupt_unconfirmed", "interrupt not confirmed: " + err.Error(), nil}
		default:
			return exec, "", &handoffError{http.StatusInternalServerError, "interrupt_failed", "interrupting execution: " + err.Error(), nil}
		}
		var err2 error
		if exec, err2 = m.getExecution(parent, exec.ID); err2 != nil {
			return exec, "", &handoffError{http.StatusInternalServerError, "store_error", "re-reading execution: " + err2.Error(), nil}
		}
	}
	if !settled(exec.State) {
		return exec, "", &handoffError{http.StatusConflict, "execution_not_settled", "execution is " + string(exec.State) + ", not settled",
			map[string]any{"state": string(exec.State)}}
	}
	sid := firstNonEmpty(exec.SessionID, exec.ResumeSessionID)
	if sid == "" {
		return exec, "", &handoffError{http.StatusConflict, "no_session_id", "execution has no Claude Code session id to resume", nil}
	}
	return exec, sid, nil
}

func settled(s store.State) bool {
	return s == store.StateIdle || s == store.StateFailed || s == store.StateTerminated || s == store.StateRejected
}
```

Take-to-terminal, from the row onward (replacing today's steps 3–8):

```go
	if exec.Provider != "claude" { /* unchanged 409 provider_unsupported */ }
	sid := firstNonEmpty(exec.SessionID, exec.ResumeSessionID)
	if sid == "" {
		writeHandoffError(w, http.StatusConflict, "no_session_id", "execution has no Claude Code session id to resume", nil)
		return
	}
	if exec.State == store.StateQueued {
		writeHandoffError(w, http.StatusConflict, "execution_not_settled", "execution is queued, not settled", map[string]any{"state": "queued"})
		return
	}
	// Step 3b (D2): the conversation's lock — every Purdex transfer of S holds
	// it, and the manual-resume handler skips S while it is held.
	sidKey := sidLockKey(sid)
	if !m.locks.TryLock(sidKey) {
		writeHandoffError(w, http.StatusConflict, "transfer_in_progress", "this conversation is being moved already", map[string]any{"session_id": sid})
		return
	}
	defer m.locks.Unlock(sidKey)
	// Step 3c (§4.3, D6): S may move only if nothing but this execution owns
	// it. An exited execution owns nothing; a second click after a success
	// finds S in the terminal it just reached and stops here.
	wasLive := isLiveExecution(exec)
	allow := ""
	if wasLive {
		allow = exec.ID
	}
	if herr := m.checkOwners(parent, sid, allow, ""); herr != nil {
		herr.write(w)
		return
	}

	// Step 4: preflights (unchanged).

	// Step 5 (D5): control for the whole transfer. Holding the lease is the
	// fence that replaced "archive first": nobody can send into the worker
	// between the resume and the exit. Ended rows take no sends and need none.
	ctl := control{release: noRelease}
	if wasLive && (exec.State == store.StateRunning || exec.State == store.StateIdle) {
		var herr *handoffError
		if ctl, herr = m.takeControl(parent, execID, body.LeaseID, principal); herr != nil {
			herr.write(w)
			return
		}
		defer func() { ctl.release() }() // ctl may be replaced by renewControl below
		if exec, _, herr = m.settleForResume(parent, exec, ctl); herr != nil {
			herr.write(w)
			return
		}
		// A full TTL from here: the fence must outlive create + resume, even
		// when the lease was borrowed with seconds left (plan review #2).
		if ctl, herr = m.renewControl(parent, execID, ctl, principal); herr != nil {
			herr.write(w)
			return
		}
	}

	// Step 6: create the session (unchanged).

	// Step 7: resume. On failure the session is killed and nothing is exited.
	if herr := m.resumeInWindow(info, info.TmuxInstance, body.ResumeCommand, sid); herr != nil {
		killed := m.killCreatedSession(execID, info, herr.code)
		if herr.detail == nil {
			herr.detail = map[string]any{}
		}
		herr.detail["session_name"] = name
		herr.detail["session_killed"] = killed
		herr.detail["exited"] = false
		herr.write(w)
		return
	}

	// Step 8 (§4.3): the terminal owns S now; the worker it was exits.
	// The resume succeeded, so the answer is 200 either way: the SPA must
	// swap the pane to the terminal that now runs S. A worker that could not
	// exit (terminate AND archive failed) is reported as exited:false plus
	// exit_error, and the SPA tells the user to exit it by hand (Task 16).
	resp := map[string]any{"session": info, "session_id": sid, "archived": exec.ArchivedAt != 0, "exited": !wasLive}
	if wasLive {
		out, herr := m.exitWorker(parent, exec, ctlPtr(ctl), principal)
		if herr != nil {
			m.logf("nex: take-to-terminal %s: exiting the worker after the resume: %s (%s)", execID, herr.code, herr.msg)
			resp["exit_error"] = herr.code
		}
		resp["exited"], resp["archived"] = out.Exited(), out.Archived || exec.ArchivedAt != 0
	}
	m.logf("nex: take-to-terminal %s → session %s/%s (session %s, exited=%v)", execID, info.Code, info.Name, sid, resp["exited"])
	writeJSON(w, http.StatusOK, resp)
```

Rewrite the file comment at the top to the new order (resume first, exit after; the held lease plus the locks are the fence; exited / rejected accepted). Remove the `execution_archived` refusal and the `archive_failed` step. Keep the session-create error handling exactly as it is.

**Take-back's call site in this task (interim; Task 9 replaces it).** The new `settleForResume` signature breaks `handleNexTakeback`, so adapt it here in a way that keeps today's observable behaviour byte for byte: a lease only when the row is `running`, released on the way out, and no renew:

```go
	ctl := control{release: noRelease}
	if exec.State == store.StateRunning {
		var herr *handoffError
		if ctl, herr = m.takeControl(parent, execID, body.LeaseID, principal); herr != nil {
			herr.write(w)
			return
		}
		defer ctl.release()
	}
	exec, sid, herr := m.settleForResume(parent, exec, ctl)
```

Existing `takeback_test.go` cases must pass **unchanged** in this commit. That is the proof the interim adaptation altered nothing. Two expected differences, both in the lease path:
- the `held_by` detail now comes from `takeControl` (`principal` is the re-read holder);
- a `running` row whose lease another **pdx** client holds is now borrowed instead of refused (D4/D5).

Adjust only assertions that pinned exactly those.

Also add these two take-to-terminal cases (plan review #2, #5):

```go
func TestTakeToTerminal_RenewsTheFenceBeforeTheResume(t *testing.T) {
	env := newTTEnv(t)
	env.store.script(ttExec(store.StateIdle))
	env.svc.acquireLeaseResult = store.Lease{ID: "L-t"}
	env.reviveCCAfterKeys()
	rec := env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`)
	if rec.Code != 200 || len(env.svc.renewCalls) != 1 || env.svc.renewCalls[0].LeaseID != "L-t" {
		t.Fatalf("%d renew=%+v", rec.Code, env.svc.renewCalls)
	}
	if env.svc.renewAt().After(env.keysSentAt()) {
		t.Fatal("the lease must be renewed before the resume keys go out")
	}
}

func TestTakeToTerminal_ResumeOKButExitFails_Reports200WithExitError(t *testing.T) {
	env := newTTEnv(t)
	env.store.script(ttExec(store.StateIdle))
	env.svc.terminateErr = errors.New("engine wedged")
	env.svc.archiveErr = errors.New("db locked")
	env.reviveCCAfterKeys()
	rec := env.postTakeToTerminal(t, "E1", `{"session_name":"proj-1","resume_command":"claude --resume {id}"}`)
	body := decode(t, rec)
	if rec.Code != 200 || body["exited"] != false || body["exit_error"] != "terminate_failed" || body["session"] == nil {
		t.Fatalf("%d %v", rec.Code, body)
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/module/nex/`
Expected: PASS, with `takeback_test.go` untouched apart from the noted `held_by` detail.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/nex/take_to_terminal.go internal/module/nex/takeback.go internal/module/nex/take_to_terminal_test.go internal/module/nex/settle_test.go -m "feat(daemon): take-to-terminal resumes first, then exits the worker; accepts exited workers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 9: Take-back — owner check, held lease, exit after the resume

**Files:**
- Modify: `internal/module/nex/takeback.go:59-206` (`handleNexTakeback`)
- Modify: `internal/module/nex/takeback_test.go`

**Interfaces:**
- Consumes: as Task 8.
- Produces:
  - Take-back 200 → `{session_id, archived, exited, exit_error?}` (same meaning as take-to-terminal).
  - New 409s: `no_session_id` (before the locks), `transfer_in_progress`, `session_owned`, `execution_archived` (an execution that is no longer live; the SPA never offers take-back for one).
  - The order after `boundToSession`: `exec:<id>` lock → `sid:<S>` lock → not live → 409 `execution_archived` → `checkOwners(sid, exec.ID, "")` → `takeControl` (if running or idle; defer release) → `settleForResume` → `renewControl` → last `cc_already_running` look → resume → `exitWorker` (a failure → 200 with `exited:false`, `exit_error`).

- [ ] **Step 1: Write the failing tests**

```go
func TestTakeback_ResumeThenExit(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.script(boundExec(store.StateIdle))
	env.svc.acquireLeaseResult = store.Lease{ID: "L-b"}
	env.reviveCCAfterKeys()
	rec := env.postTakeback(t)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if env.svc.terminateCalls[0].LeaseID != "L-b" || len(env.svc.ArchiveCalls()) != 1 {
		t.Fatal("take-back must terminate under its lease and archive")
	}
	body := decode(t, rec)
	if body["exited"] != true || body["archived"] != true {
		t.Fatalf("body = %v", body)
	}
}

func TestTakeback_ResumeFailsExitsNothing(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.script(boundExec(store.StateIdle))
	rec := env.postTakeback(t) // CC never starts
	assertErrorCode(t, rec, 504, "cc_start_timeout")
	assertUntouched(t, env) // existing helper: no terminate, no archive
}

func TestTakeback_RefusesExitedAndOwned(t *testing.T) {
	t.Run("exited", func(t *testing.T) {
		env := newTakebackEnv(t)
		e := boundExec(store.StateTerminated)
		e.ArchivedAt = 3
		env.store.script(e)
		assertErrorCode(t, env.postTakeback(t), 409, "execution_archived")
	})
	t.Run("another pane runs S", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.script(boundExec(store.StateIdle))
		env.terminals.live = map[string][]agent.TerminalSession{hoSessionID: {{PaneID: "%7", SessionID: hoSessionID, AgentType: "cc", Verified: true}}}
		assertErrorCode(t, env.postTakeback(t), 409, "session_owned")
	})
	t.Run("sid lock held", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.script(boundExec(store.StateIdle))
		env.m.locks.TryLock(sidLockKey(hoSessionID))
		assertErrorCode(t, env.postTakeback(t), 409, "transfer_in_progress")
	})
}
```

Adapt the existing archive assertions in `takeback_test.go` (it expected archive without terminate): a successful take-back now terminates and archives.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/module/nex/ -run TestTakeback`
Expected: FAIL.

- [ ] **Step 3: Implement** the order in Interfaces, with the same comments style as Task 8. Replace the final archive block with:

```go
	out, herr := m.exitWorker(parent, exec, ctlPtr(ctl), principal)
	resp := map[string]any{"session_id": sid, "archived": out.Archived, "exited": out.Exited()}
	if herr != nil {
		m.logf("nex: takeback %s: exiting %s after the resume: %s (%s)", code, execID, herr.code, herr.msg)
		resp["exit_error"] = herr.code
	}
	m.logf("nex: takeback %s ← execution %s (session %s, exited=%v)", code, execID, sid, out.Exited())
	writeJSON(w, http.StatusOK, resp)
```

(`ctlPtr` and `renewControl` come from Task 5.) Add a take-back case mirroring `TestTakeToTerminal_ResumeOKButExitFails_Reports200WithExitError`, and one asserting that `renewCalls` happens before the keys.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/module/nex/ && go vet ./internal/module/nex/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/nex/takeback.go internal/module/nex/takeback_test.go -m "feat(daemon): take-back exits the worker after the terminal resumes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

**PR P1a-3:** `feat(daemon): conversation entity P1a-3 — owner check, take-to-terminal and take-back exit the worker`. Then codex R1 + R2.

---

## Phase P1a-4 — nex: handoff owner check, manual resume (Q1)

### Task 10: Handoff — owner check, `sid` lock, rejected-execution handling (D7)

**Files:**
- Modify: `internal/module/nex/handoff.go:164-260`
- Modify: `internal/module/nex/handoff_test.go` (or the file holding handoff handler tests)

**Interfaces:**
- Produces:
  - After `resolveHandoffOwner` (sid = `owner.SessionID`): `sid:<S>` lock (409 `transfer_in_progress`), then `checkOwners(sid, "", owner.TmuxPaneID)` (409 `session_owned` / 503 `owner_check_failed`). Both run **before** liveness, `stopCC` or any key.
  - `delegate_rejected` detail gains `execution_id` (when the engine created a row) and `exited` (bool, only when rolled back).

- [ ] **Step 1: Write the failing tests**

```go
func TestHandoff_RefusesWhenWorkerOwnsSession(t *testing.T) {
	env := newHandoffEnv(t)
	env.store.listRows = []store.Execution{row("E7", "idle", false, hoSessionID, "", 1)}
	rec := env.postHandoff(t, `{"expected_tmux_instance":"`+hoInstance+`"}`)
	assertErrorCode(t, rec, 409, "session_owned")
	if body := decode(t, rec); body["owner"] != "worker" || body["execution_id"] != "E7" {
		t.Fatalf("body = %v", body)
	}
	if env.ccOps.interrupts+env.ccOps.exits != 0 || len(env.svc.delegateCalls) != 0 {
		t.Fatal("CC must be untouched")
	}
}

func TestHandoff_TerminalOwnerChecks(t *testing.T) {
	t.Run("another pane runs S → refused", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.terminals.live = map[string][]agent.TerminalSession{hoSessionID: {{PaneID: "%99", SessionID: hoSessionID, AgentType: "cc", Verified: true}}}
		assertErrorCode(t, env.postHandoff(t, `{"expected_tmux_instance":"`+hoInstance+`"}`), 409, "session_owned")
	})
	t.Run("its own pane is the owner being transferred → allowed", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.terminals.live = map[string][]agent.TerminalSession{hoSessionID: {{PaneID: env.ownerPaneID(), SessionID: hoSessionID, AgentType: "cc", Verified: true}}}
		if rec := env.postHandoff(t, `{"expected_tmux_instance":"`+hoInstance+`"}`); rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	})
	t.Run("sid lock held", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.m.locks.TryLock(sidLockKey(hoSessionID))
		assertErrorCode(t, env.postHandoff(t, `{"expected_tmux_instance":"`+hoInstance+`"}`), 409, "transfer_in_progress")
	})
}

func TestHandoff_RejectedRolledBackExitsTheRow(t *testing.T) {
	env := newHandoffEnv(t)
	env.svc.delegateResult = execution.Result{ID: "R1", State: store.StateRejected, RejectReason: "session_expired"}
	env.store.script(store.Execution{ID: "R1", State: store.StateRejected, ResumeSessionID: hoSessionID})
	env.reviveCCAfterKeys() // the rollback brings CC back
	rec := env.postHandoff(t, `{"expected_tmux_instance":"`+hoInstance+`","rollback_command":"claude --resume {id}"}`)
	assertErrorCode(t, rec, 409, "delegate_rejected")
	body := decode(t, rec)
	if body["rolled_back"] != true || body["execution_id"] != "R1" || body["exited"] != true || len(env.svc.ArchiveCalls()) != 1 {
		t.Fatalf("body = %v archive=%d", body, len(env.svc.ArchiveCalls()))
	}
}

func TestHandoff_RejectedNotRolledBackKeepsTheRow(t *testing.T) {
	env := newHandoffEnv(t)
	env.svc.delegateResult = execution.Result{ID: "R1", State: store.StateRejected, RejectReason: "session_expired"}
	rec := env.postHandoff(t, `{"expected_tmux_instance":"`+hoInstance+`"}`) // no rollback_command
	body := decode(t, rec)
	if body["rolled_back"] != false || body["execution_id"] != "R1" || len(env.svc.ArchiveCalls()) != 0 {
		t.Fatalf("body = %v", body)
	}
	if _, ok := body["exited"]; ok {
		t.Fatal("exited is only reported when rolled back")
	}
}
```

Adapt the names (`postHandoff`, `ccOps.interrupts`, `ownerPaneID`, `delegateResult`) to the existing handoff fixture. `ownerPaneID` is the `TmuxPaneID` that `stubOwnerResolver` returns.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/module/nex/ -run TestHandoff_`
Expected: FAIL.

- [ ] **Step 3: Implement**

Insert right after the identity step (`resolveHandoffOwner`), before the liveness probe:

```go
	// Conversation entity (§4.3, D2, D7): S moves only if this pane's CC is
	// its one owner. The sid lock also tells the manual-resume handler that
	// a rollback resume below is ours.
	sidKey := sidLockKey(owner.SessionID)
	if !m.locks.TryLock(sidKey) {
		writeHandoffError(w, http.StatusConflict, "transfer_in_progress", "this conversation is being moved already",
			map[string]any{"session_id": owner.SessionID})
		return
	}
	defer m.locks.Unlock(sidKey)
	if herr := m.checkOwners(r.Context(), owner.SessionID, "", owner.TmuxPaneID); herr != nil {
		herr.write(w)
		return
	}
```

In the rejection branch:

```go
		if result.ID != "" {
			extra["execution_id"] = result.ID
		}
		rolled := m.rollbackHandoff(sess, expected, body.RollbackCommand, owner.SessionID, target)
		extra["rolled_back"] = rolled
		// r.Context() is safe here: every engine call wraps it in detachedContext
		// (context.WithoutCancel, handoff.go:42-44), so a client that hangs up
		// cannot cut this cleanup short.
		// D7: rolled back → the terminal owns S again, so the rejected row
		// exits (one state). Not rolled back → it stays as the start-failed
		// worker the SPA shows in the pane.
		if rolled && result.ID != "" {
			exited := false
			if rej, gerr := m.getExecution(r.Context(), result.ID); gerr == nil {
				out, herr := m.exitWorker(r.Context(), rej, nil, principal)
				exited = herr == nil && out.Exited()
			} else {
				m.logf("nex: handoff %s: reading rejected execution %s: %v", code, result.ID, gerr)
			}
			extra["exited"] = exited
		}
```

Check `owner.TmuxPaneID`'s real field name on `agent.PaneOwner` (`pane_owner.go:18-27`: `TmuxPaneID`).

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/module/nex/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/nex/handoff.go internal/module/nex/handoff_test.go -m "feat(daemon): handoff checks the session's owner; a rolled-back rejection exits its row

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 11: Manual resume exits the worker (Q1, D3)

**Files:**
- Create: `internal/module/nex/manual_resume.go`
- Create: `internal/module/nex/manual_resume_test.go`
- Modify: `internal/module/nex/module.go` (`Start` subscribes, `Stop` unsubscribes; field `unsubscribeStarts func()`)

**Interfaces:**
- Consumes: `agent.TerminalSessions.SubscribeSessionStart`, `LiveBySessionID`; `liveWorkersFor`, `exitWorker`, `sidLockKey`, `takeToTerminalLockKey`.
- Produces:
  - `func (m *Module) onSessionStart(ev agent.SessionStartEvent)`, synchronous (the hub already runs it on its own goroutine).
  - `func (m *Module) internalPrincipal() string` → `"pdx:" + HostID`.
  - The host event, via `m.core.Events.Broadcast(ev.TmuxSession, "nex-worker-exited", value)`, where `value` is the JSON text `{"execution_id","session_id","reason":"manual_resume","tmux_session"}`, sent once per exited worker.

- [ ] **Step 1: Write the failing tests**

```go
// internal/module/nex/manual_resume_test.go
package nex

func ev(source string) agent.SessionStartEvent {
	return agent.SessionStartEvent{AgentType: "cc", SessionID: "S", Source: source, TmuxSession: "proj-2", TmuxPaneID: "%4", FrameID: "F"}
}

func liveTerminal(env *handoffEnv, verified bool) {
	env.terminals.live = map[string][]agent.TerminalSession{"S": {{FrameID: "F", PaneID: "%4", SessionID: "S", AgentType: "cc", Verified: verified}}}
}

func TestManualResume_ExitsLiveWorkersAndBroadcasts(t *testing.T) {
	env := newHandoffEnv(t)
	sub := env.captureHostEvents(t) // core.Events.AddTestSubscriber wrapper; returns decoded HostEvents
	liveTerminal(env, true)
	env.store.listRows = []store.Execution{row("E1", "idle", false, "S", "", 1), row("E2", "running", false, "", "S", 2)}
	env.svc.acquireLeaseResult = store.Lease{ID: "L-d"}

	env.m.onSessionStart(ev("resume"))

	if len(env.svc.terminateCalls) != 2 || len(env.svc.ArchiveCalls()) != 2 {
		t.Fatalf("terminate=%d archive=%d", len(env.svc.terminateCalls), len(env.svc.ArchiveCalls()))
	}
	got := sub.events("nex-worker-exited")
	if len(got) != 2 {
		t.Fatalf("events = %v", got)
	}
	var v map[string]string
	_ = json.Unmarshal([]byte(got[0].Value), &v)
	if v["reason"] != "manual_resume" || v["session_id"] != "S" || v["tmux_session"] != "proj-2" || v["execution_id"] == "" {
		t.Fatalf("value = %v", v)
	}
	if env.svc.acquireCalls[0].PrincipalID != "pdx:"+testHostID {
		t.Fatal("the daemon acts as the bare host principal")
	}
}

func TestManualResume_DoesNothingWhen(t *testing.T) {
	cases := map[string]func(env *handoffEnv) agent.SessionStartEvent{
		"source is clear":            func(env *handoffEnv) agent.SessionStartEvent { liveTerminal(env, true); return ev("clear") },
		"source is compact":          func(env *handoffEnv) agent.SessionStartEvent { liveTerminal(env, true); return ev("compact") },
		"not cc":                     func(env *handoffEnv) agent.SessionStartEvent { liveTerminal(env, true); e := ev("resume"); e.AgentType = "codex"; return e },
		"a Purdex transfer holds S":  func(env *handoffEnv) agent.SessionStartEvent { liveTerminal(env, true); env.m.locks.TryLock(sidLockKey("S")); return ev("resume") },
		"the terminal is gone":       func(env *handoffEnv) agent.SessionStartEvent { return ev("resume") },
		"only an unverified frame":   func(env *handoffEnv) agent.SessionStartEvent { liveTerminal(env, false); return ev("resume") },
		"no live worker":             func(env *handoffEnv) agent.SessionStartEvent { liveTerminal(env, true); env.store.listRows = nil; return ev("startup") },
		"engine unavailable":         func(env *handoffEnv) agent.SessionStartEvent { liveTerminal(env, true); env.m.sys = engine{}; return ev("resume") },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			env := newHandoffEnv(t)
			env.store.listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
			e := setup(env)
			env.m.onSessionStart(e)
			if len(env.svc.terminateCalls)+len(env.svc.ArchiveCalls()) != 0 {
				t.Fatal("must not exit")
			}
		})
	}
}

func TestManualResume_SkipsAWorkerBeingMovedAndReportsOnlySuccesses(t *testing.T) {
	env := newHandoffEnv(t)
	sub := env.captureHostEvents(t)
	liveTerminal(env, true)
	env.store.listRows = []store.Execution{row("E1", "idle", false, "S", "", 1), row("E2", "failed", false, "S", "", 2)}
	env.m.locks.TryLock(takeToTerminalLockKey("E1")) // E1 is mid-exit elsewhere
	env.svc.archiveErr = errors.New("db busy")        // E2's archive fails → not exited
	env.m.onSessionStart(ev("resume"))
	if len(sub.events("nex-worker-exited")) != 0 {
		t.Fatal("no event for a worker that did not exit")
	}
}

func TestManualResume_TruncatedScanStillExitsWhatItFound(t *testing.T) {
	env := newHandoffEnv(t)
	liveTerminal(env, true)
	rows := make([]store.Execution, ownerScanPageSize*ownerScanMaxPages+1)
	for i := range rows {
		rows[i] = row(fmt.Sprintf("%06d", i), "terminated", false, "OTHER", "", int64(i))
	}
	rows[0] = row("000000", "idle", false, "S", "", 0) // on page 1
	env.store.listRows = rows
	env.m.onSessionStart(ev("resume"))
	if len(env.svc.ArchiveCalls()) != 1 {
		t.Fatal("the worker found before the cap must still exit")
	}
}

func TestManualResume_StartSubscribesStopUnsubscribes(t *testing.T) {
	env := newHandoffEnv(t)
	if err := env.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if env.terminals.subscribed == nil {
		t.Fatal("Start must subscribe")
	}
	_ = env.m.Stop(context.Background())
	if env.terminals.subscribed != nil {
		t.Fatal("Stop must unsubscribe")
	}
}
```

`captureHostEvents` wraps `core.Events.AddTestSubscriber` (see `probe_intent_dispatcher_integration_test.go:34` in the agent package for the pattern). If `newHandoffEnv` has no `core.Core`, give it one with `Events: core.NewEventsBroadcaster()` (use the real constructor name). `Start` must still be a no-op when `initErr` is set.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/module/nex/ -run TestManualResume`
Expected: compile failure.

- [ ] **Step 3: Implement**

```go
// internal/module/nex/manual_resume.go
package nex

// Manual resume exits the worker (conversation entity Q1, D3): a terminal
// that resumes a session a live worker holds takes the conversation over,
// and the worker exits — unless the resume is one of Purdex's own
// transfers, which hold the session's sid lock for their whole run.

const manualResumeTimeout = 90 * time.Second // > one exit's terminate budget

func (m *Module) internalPrincipal() string { return "pdx:" + m.opts.Config.HostID }

func (m *Module) onSessionStart(ev agent.SessionStartEvent) {
	if m.sys.service == nil || m.sys.store == nil || m.opts.Config == nil {
		return
	}
	if ev.AgentType != "cc" || ev.SessionID == "" || (ev.Source != "startup" && ev.Source != "resume") {
		return
	}
	key := sidLockKey(ev.SessionID)
	if !m.locks.TryLock(key) {
		return // a Purdex transfer of S is in flight, or another handler has S
	}
	defer m.locks.Unlock(key)

	ctx, cancel := context.WithTimeout(context.Background(), manualResumeTimeout)
	defer cancel()
	// Act only on a terminal that is verifiably running S right now: a late
	// hook from a session a failed transfer already killed must not exit the
	// worker that transfer left alone.
	terms, err := m.terminals.LiveBySessionID(ctx, "cc", ev.SessionID)
	if err != nil {
		m.logf("nex: manual resume %s: terminal lookup: %v", ev.SessionID, err)
		return
	}
	verified := false
	for _, t := range terms {
		verified = verified || t.Verified
	}
	if !verified {
		return
	}
	workers, err := m.liveWorkersFor(ctx, ev.SessionID)
	if err != nil {
		// Unlike an owner check (which refuses a transfer on a partial scan),
		// the terminal has ALREADY taken S over here: exiting the workers
		// found is strictly better than exiting none. Logged loudly; the
		// 10 000-row cap is far beyond any real host.
		m.logf("nex: manual resume %s: worker scan: %v (acting on %d found)", ev.SessionID, err, len(workers))
	}
	principal := m.internalPrincipal()
	for _, w := range workers {
		lk := takeToTerminalLockKey(w.ID)
		if !m.locks.TryLock(lk) {
			m.logf("nex: manual resume %s: %s is busy; left to its mover", ev.SessionID, w.ID)
			continue
		}
		out, herr := m.exitWorker(ctx, w, nil, principal)
		m.locks.Unlock(lk)
		if herr != nil || !out.Exited() {
			m.logf("nex: manual resume %s: exiting %s failed: %v", ev.SessionID, w.ID, herr)
			continue
		}
		m.logf("nex: manual resume %s in %s → worker %s exited", ev.SessionID, ev.TmuxSession, w.ID)
		m.broadcastWorkerExited(w.ID, ev)
	}
}

func (m *Module) broadcastWorkerExited(execID string, ev agent.SessionStartEvent) {
	if m.core == nil || m.core.Events == nil {
		return
	}
	value, _ := json.Marshal(map[string]string{
		"execution_id": execID, "session_id": ev.SessionID, "reason": "manual_resume", "tmux_session": ev.TmuxSession,
	})
	m.core.Events.Broadcast(ev.TmuxSession, "nex-worker-exited", string(value))
}
```

In `Start`, after the `initErr` early return: `if m.terminals != nil { m.unsubscribeStarts = m.terminals.SubscribeSessionStart(m.onSessionStart) }`. In `Stop`, first: `if m.unsubscribeStarts != nil { m.unsubscribeStarts(); m.unsubscribeStarts = nil }`. The unsubscribe must run even when the engine never assembled, so put it before the `shutdown == nil` early return.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/module/nex/ && go test -race ./internal/module/nex/ -run 'ManualResume|TakeToTerminal|Takeback|Handoff' && go vet ./...`
Expected: PASS, no race.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/nex/manual_resume.go internal/module/nex/manual_resume_test.go internal/module/nex/module.go -m "feat(daemon): a manual resume in a terminal exits the session's live worker

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

**PR P1a-4:** `feat(daemon): conversation entity P1a-4 — handoff owner check, manual resume exits the worker`. Then codex R1 + R2. When P1a-1…P1a-4 are merged, report "daemon deployable (P1a)" to the coordinator. Do not restart.

---
## Phase P1b-1 — SPA lists: cursor paging, live entities, one dot mapping

### Task 12: Follow Nexen's cursor (`listAllExecutions`)

**Files:**
- Modify: `spa/src/lib/nex/validate-executions.ts:116-126` (`SanitizedExecutionsPage.nextCursor`)
- Create: `spa/src/lib/nex/list-all-executions.ts`
- Create: `spa/src/lib/nex/list-all-executions.test.ts`
- Modify: `spa/src/lib/nex/execution-list-effects.ts:98-122` (`fetch`)
- Test: `spa/src/lib/nex/validate-executions.test.ts` (append), `spa/src/stores/useExecutionListStore.test.ts` (adapt mocks expecting `limit: 100`)

**Interfaces:**
- Produces:

```ts
// validate-executions.ts
export interface SanitizedExecutionsPage { items: ExecutionSummary[]; dropped: number; nextCursor: string } // '' = last page or malformed
// list-all-executions.ts
export const LIST_PAGE_LIMIT = 500
export const LIST_MAX_PAGES = 20
export interface ListAllResult { items: ExecutionSummary[]; dropped: number; truncated: boolean }
/** Pages `listExecutions` until next_cursor is '' (or repeats, or LIST_MAX_PAGES). Resolves null as soon as `isCurrent()` is false after a page. */
export function listAllExecutions(hostId: string, opts: { includeArchived: boolean }, isCurrent?: () => boolean): Promise<ListAllResult | null>
```

- [ ] **Step 1: Write the failing tests**

```ts
// spa/src/lib/nex/list-all-executions.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import * as api from './nex-api'
import { listAllExecutions, LIST_PAGE_LIMIT, LIST_MAX_PAGES } from './list-all-executions'

vi.mock('./nex-api', () => ({ listExecutions: vi.fn() }))
const row = (id: string) => ({ id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dir', brief: '', labels: {}, created_at: 1, updated_at: 1, duration_ms: null, event_count: 0, observers: 0, archived: false })

describe('listAllExecutions', () => {
  beforeEach(() => vi.mocked(api.listExecutions).mockReset())

  it('concatenates pages in order and passes the cursor and limit', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a'), row('b')], next_cursor: 'b' })
      .mockResolvedValueOnce({ items: [row('c')], next_cursor: '' })
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(r!.items.map((i) => i.id)).toEqual(['a', 'b', 'c'])
    expect(r!.truncated).toBe(false)
    expect(api.listExecutions).toHaveBeenNthCalledWith(1, 'h1', { includeArchived: false, limit: LIST_PAGE_LIMIT })
    expect(api.listExecutions).toHaveBeenNthCalledWith(2, 'h1', { includeArchived: false, limit: LIST_PAGE_LIMIT, cursor: 'b' })
  })

  it('stops on a cursor that repeats (never loops) and marks it truncated', async () => {
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [row('a')], next_cursor: 'a' })
    const r = await listAllExecutions('h1', { includeArchived: true })
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
    expect(r!.truncated).toBe(true)
  })

  it('stops after LIST_MAX_PAGES', async () => {
    let n = 0
    vi.mocked(api.listExecutions).mockImplementation(async () => { n += 1; return { items: [row(`r${n}`)], next_cursor: `r${n}` } })
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(api.listExecutions).toHaveBeenCalledTimes(LIST_MAX_PAGES)
    expect(r!.truncated).toBe(true)
  })

  it('resolves null and asks for nothing more once the caller is stale', async () => {
    let current = true
    vi.mocked(api.listExecutions).mockImplementationOnce(async () => { current = false; return { items: [row('a')], next_cursor: 'a' } })
    expect(await listAllExecutions('h1', { includeArchived: false }, () => current)).toBeNull()
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
  })

  it('counts malformed rows and keeps going', async () => {
    vi.mocked(api.listExecutions).mockResolvedValueOnce({ items: [row('a'), { nope: 1 }], next_cursor: '' } as never)
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(r!.items).toHaveLength(1)
    expect(r!.dropped).toBe(1)
  })
})
```

Append to `validate-executions.test.ts`: `sanitizeExecutionsPage({items: [], next_cursor: 'x'}).nextCursor === 'x'`; a non-string or missing `next_cursor` gives `''`; a malformed body gives `''`.

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/lib/nex/list-all-executions.test.ts src/lib/nex/validate-executions.test.ts`
Expected: FAIL (module not found; `nextCursor` undefined).

- [ ] **Step 3: Implement**

```ts
// spa/src/lib/nex/list-all-executions.ts — conversation entity spec §9 / D9:
// follow Nexen's cursor (ids ascending, oldest first) so the newest rows are
// never cut off; bounded, and a repeated cursor ends the walk.
import { listExecutions } from './nex-api'
import { sanitizeExecutionsPage } from './validate-executions'
import type { ExecutionSummary } from './types'

export const LIST_PAGE_LIMIT = 500
export const LIST_MAX_PAGES = 20

export interface ListAllResult { items: ExecutionSummary[]; dropped: number; truncated: boolean }

export async function listAllExecutions(
  hostId: string,
  opts: { includeArchived: boolean },
  isCurrent: () => boolean = () => true,
): Promise<ListAllResult | null> {
  const items: ExecutionSummary[] = []
  let dropped = 0
  let cursor = ''
  for (let page = 0; page < LIST_MAX_PAGES; page += 1) {
    const raw = await listExecutions(hostId, { includeArchived: opts.includeArchived, limit: LIST_PAGE_LIMIT, ...(cursor ? { cursor } : {}) })
    if (!isCurrent()) return null
    const p = sanitizeExecutionsPage(raw)
    items.push(...p.items)
    dropped += p.dropped
    if (p.nextCursor === '') return { items, dropped, truncated: false }
    if (p.nextCursor === cursor) return { items, dropped, truncated: true }
    cursor = p.nextCursor
  }
  return { items, dropped, truncated: true }
}
```

In `execution-list-effects.ts` `fetch`, replace the single `listExecutions(...)` with `listAllExecutions(hostId, { includeArchived: false }, stillCurrent)`. Then:
- `null` → return (a stale fetch commits nothing);
- otherwise commit `items` as today and warn on `dropped > 0`;
- `truncated` → `console.warn('nex: executions list truncated', { hostId })`;
- errors take the existing catch path.

`sanitizeExecutionsPage` adds `nextCursor: typeof raw.next_cursor === 'string' ? raw.next_cursor : ''`, and `''` on the malformed path.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/lib/nex src/stores/useExecutionListStore.test.ts src/hooks/useHostExecutions.test.ts`
Expected: PASS (update mocks that pinned `{ includeArchived: false, limit: 100 }` to the new shape).

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/nex/validate-executions.ts spa/src/lib/nex/validate-executions.test.ts spa/src/lib/nex/list-all-executions.ts spa/src/lib/nex/list-all-executions.test.ts spa/src/lib/nex/execution-list-effects.ts spa/src/stores/useExecutionListStore.test.ts -m "feat(spa): follow Nexen's cursor when listing executions

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 13: Live entities — one row per conversation (`liveEntityRows`)

**Files:**
- Create: `spa/src/lib/nex/live-workers.ts`
- Create: `spa/src/lib/nex/live-workers.test.ts`
- Modify: `spa/src/components/executions/ExecutionsView.tsx:44-46` (rows come from `liveEntityRows(items)`)
- Test: `spa/src/components/executions/ExecutionsView.test.tsx` (append)

**Interfaces:**
- Produces:

```ts
export function isLiveRow(row: ExecutionSummary): boolean        // !row.archived && row.state !== 'terminated'
export function entityKeyOf(row: ExecutionSummary): string       // row.session_id || row.resume_session_id || row.id
/** Live rows only, one per entity (the latest stint: larger created_at, then larger id), in input order. */
export function liveEntityRows(items: readonly ExecutionSummary[]): ExecutionSummary[]
```

- Rule: the shared store keeps raw `items`. The admin table, `useTabDisplay`, `worker-summary` and `useWorkerAgentProjection` read them unchanged. Only list UIs call `liveEntityRows`.

- [ ] **Step 1: Write the failing tests**

```ts
// spa/src/lib/nex/live-workers.test.ts
import { describe, it, expect } from 'vitest'
import { isLiveRow, entityKeyOf, liveEntityRows } from './live-workers'
import type { ExecutionSummary } from './types'

const r = (o: Partial<ExecutionSummary> & { id: string }): ExecutionSummary => ({
  state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dir', brief: '', labels: {},
  created_at: 1, updated_at: 1, duration_ms: null, event_count: 0, observers: 0, archived: false, ...o,
})

describe('live workers', () => {
  it('isLiveRow follows spec §4.2', () => {
    expect(isLiveRow(r({ id: 'a', state: 'idle' }))).toBe(true)
    expect(isLiveRow(r({ id: 'a', state: 'failed' }))).toBe(true)
    expect(isLiveRow(r({ id: 'a', state: 'rejected' }))).toBe(true)
    expect(isLiveRow(r({ id: 'a', state: 'terminated' }))).toBe(false)
    expect(isLiveRow(r({ id: 'a', state: 'idle', archived: true }))).toBe(false)
  })

  it('entityKeyOf prefers session_id, then resume_session_id, then id', () => {
    expect(entityKeyOf(r({ id: 'a', session_id: 'S', resume_session_id: 'R' }))).toBe('S')
    expect(entityKeyOf(r({ id: 'a', resume_session_id: 'R' }))).toBe('R')
    expect(entityKeyOf(r({ id: 'a' }))).toBe('a')
  })

  it('keeps one live row per entity — the latest stint — in input order', () => {
    const rows = [
      r({ id: 'old', session_id: 'S', created_at: 10 }),
      r({ id: 'x', session_id: 'X', created_at: 11 }),
      r({ id: 'new', resume_session_id: 'S', created_at: 20 }), // before turn 1: only resume id
      r({ id: 'dead', session_id: 'D', state: 'terminated', created_at: 30 }),
      r({ id: 'gone', session_id: 'G', archived: true, created_at: 31 }),
    ]
    expect(liveEntityRows(rows).map((x) => x.id)).toEqual(['x', 'new'])
  })

  it('breaks a created_at tie by the larger id', () => {
    const rows = [r({ id: '01B', session_id: 'S', created_at: 5 }), r({ id: '01A', session_id: 'S', created_at: 5 })]
    expect(liveEntityRows(rows).map((x) => x.id)).toEqual(['01B'])
  })
})
```

Append to `ExecutionsView.test.tsx`: a store holding a terminated row, an archived row and two stints of one session renders exactly one row (the newer stint). An items list whose live rows are empty shows the existing empty state (`executions-empty`, or whatever testid the view uses).

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/lib/nex/live-workers.test.ts src/components/executions/ExecutionsView.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

```ts
// spa/src/lib/nex/live-workers.ts — conversation entity spec §4.2 / §9 / D9.
import type { ExecutionSummary } from './types'

export const isLiveRow = (row: ExecutionSummary): boolean => !row.archived && row.state !== 'terminated'
export const entityKeyOf = (row: ExecutionSummary): string => row.session_id || row.resume_session_id || row.id

const newer = (a: ExecutionSummary, b: ExecutionSummary): boolean =>
  a.created_at !== b.created_at ? a.created_at > b.created_at : a.id > b.id

export function liveEntityRows(items: readonly ExecutionSummary[]): ExecutionSummary[] {
  const best = new Map<string, ExecutionSummary>()
  for (const row of items) {
    if (!isLiveRow(row)) continue
    const key = entityKeyOf(row)
    const cur = best.get(key)
    if (!cur || newer(row, cur)) best.set(key, row)
  }
  const keep = new Set(best.values())
  return items.filter((row) => keep.has(row))
}
```

In `ExecutionsView.tsx`: `const live = useMemo(() => liveEntityRows(items), [items])`, then `groupBySource(live)`. The empty-state branch tests `live.length === 0`.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/lib/nex src/components/executions`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/nex/live-workers.ts spa/src/lib/nex/live-workers.test.ts spa/src/components/executions/ExecutionsView.tsx spa/src/components/executions/ExecutionsView.test.tsx -m "feat(spa): worker lists show live conversations only, one row each

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 14: One state → dot colour mapping (D8)

**Files:**
- Modify: `spa/src/lib/nex/state-dot.ts`
- Modify: `spa/src/components/execution/ExecutionHeader.tsx:64-67, 213` (drop the local `STATE_DOT`, use `STATE_DOT_CLASSES`)
- Test: `spa/src/lib/nex/state-dot.test.ts` (create), `spa/src/components/execution/ExecutionHeader.test.tsx`, `spa/src/components/executions/ExecutionRowCompact.test.tsx` (update colour expectations)

**Interfaces:**
- Produces: `STATE_DOT_CLASSES` = `{ running: 'bg-status-success', queued: 'bg-status-warning', idle: 'bg-text-muted', failed: 'bg-status-error', rejected: 'bg-status-error', terminated: 'bg-text-muted' }`, plus `stateDotClass(state: string): string`, which falls back to `'bg-text-muted'`.

- [ ] **Step 1: Write the failing tests**

```ts
// spa/src/lib/nex/state-dot.test.ts
import { describe, it, expect } from 'vitest'
import { STATE_DOT_CLASSES, stateDotClass } from './state-dot'

describe('state dot (D8: same colours as the terminal agent badge)', () => {
  it('maps every state', () => {
    expect(STATE_DOT_CLASSES).toEqual({
      running: 'bg-status-success', queued: 'bg-status-warning', idle: 'bg-text-muted',
      failed: 'bg-status-error', rejected: 'bg-status-error', terminated: 'bg-text-muted',
    })
  })
  it('falls back to muted for an unknown state', () => {
    expect(stateDotClass('weird')).toBe('bg-text-muted')
  })
})
```

In `ExecutionHeader.test.tsx`, add a test that the header dot for `terminated` has `bg-text-muted` and for `idle` has `bg-text-muted`. In `ExecutionRowCompact.test.tsx`, change any `bg-amber-400` / `bg-green-400` expectation to the new classes.

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/lib/nex/state-dot.test.ts src/components/execution/ExecutionHeader.test.tsx src/components/executions/ExecutionRowCompact.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

```ts
// spa/src/lib/nex/state-dot.ts — the one execution state → dot colour map
// (conversation entity D8): the list rows, the Nex admin table and the pane
// header all read it, and it matches the terminal agent badge (running green,
// idle grey, error red).
export const STATE_DOT_CLASSES: Record<string, string> = {
  running: 'bg-status-success',
  queued: 'bg-status-warning',
  idle: 'bg-text-muted',
  failed: 'bg-status-error',
  rejected: 'bg-status-error',
  terminated: 'bg-text-muted',
}
export const stateDotClass = (state: string): string => STATE_DOT_CLASSES[state] ?? 'bg-text-muted'
```

Replace `STATE_DOT_CLASSES[x] ?? 'bg-text-muted'` call sites (row, admin row, header) with `stateDotClass(x)`.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/lib/nex src/components/execution src/components/executions src/components/hosts/nex && pnpm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/nex/state-dot.ts spa/src/lib/nex/state-dot.test.ts spa/src/components/execution/ExecutionHeader.tsx spa/src/components/execution/ExecutionHeader.test.tsx spa/src/components/executions/ExecutionRowCompact.tsx spa/src/components/executions/ExecutionRowCompact.test.tsx spa/src/components/hosts/nex/NexExecutionRow.tsx -m "feat(spa): one state colour map for worker lists and the pane header

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

**PR P1b-1:** `feat(spa): conversation entity P1b-1 — live worker lists`. Then codex R1 + R2. After it merges, run `pnpm run build` on the main checkout once to confirm (the HMR dev server picks it up).

---

## Phase P1b-2 — SPA: exit action and the manual-resume toast

### Task 15: Exit API client and `exitWorker` flow

**Files:**
- Modify: `spa/src/lib/nex/handoff-api.ts` (`nexExitWorker`, `NexExitWorkerResult`)
- Create: `spa/src/lib/nex/exit-worker.ts`
- Create: `spa/src/lib/nex/exit-worker.test.ts`
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Produces:

```ts
// handoff-api.ts
export interface NexExitWorkerResult { exited: boolean; terminated: boolean; archived: boolean; state: string }
export function nexExitWorker(hostId: string, executionId: string, body: { lease_id?: string }): Promise<NexExitWorkerResult>
// exit-worker.ts
export interface ExitWorkerArgs { hostId: string; executionId: string; leaseId?: string; forgetLease?: () => void }
/** single-flight `exit:${hostId}:${executionId}`; refetches the host's list after; throws HandoffApiError. */
export function exitWorker(args: ExitWorkerArgs): Promise<NexExitWorkerResult>
export function exitErrorMessage(err: unknown, t: T): string
```

- Copy (zh-TW; give en equivalents):
  - `worker.exit.button`: 退出
  - `worker.exit.confirm_title`: 退出 worker？
  - `worker.exit.confirm_running`: 這一輪會被中斷。
  - `worker.exit.held_by`: 被 {{principal}} 控制中，無法退出。
  - `worker.exit.failed`: 退出失敗：{{reason}}
  - `worker.exit.manual_resume`: {{name}} 已在終端機接續，worker 已退出

- [ ] **Step 1: Write the failing tests**

```ts
// spa/src/lib/nex/exit-worker.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import * as api from './handoff-api'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { exitWorker, exitErrorMessage } from './exit-worker'
import { HandoffApiError } from './handoff-api'
import { useI18nStore } from '../../stores/useI18nStore'

vi.mock('./handoff-api', async (orig) => ({ ...(await orig<typeof import('./handoff-api')>()), nexExitWorker: vi.fn() }))

describe('exitWorker', () => {
  beforeEach(() => {
    vi.mocked(api.nexExitWorker).mockReset()
    vi.spyOn(useExecutionListStore.getState(), 'refetch').mockImplementation(() => {})
  })

  it('posts the lease, forgets it and refetches the list', async () => {
    vi.mocked(api.nexExitWorker).mockResolvedValue({ exited: true, terminated: true, archived: true, state: 'terminated' })
    const forgetLease = vi.fn()
    const r = await exitWorker({ hostId: 'h1', executionId: 'E1', leaseId: 'L1', forgetLease })
    expect(api.nexExitWorker).toHaveBeenCalledWith('h1', 'E1', { lease_id: 'L1' })
    expect(r.exited).toBe(true)
    expect(forgetLease).toHaveBeenCalled()
    expect(useExecutionListStore.getState().refetch).toHaveBeenCalledWith('h1')
  })

  it('collapses a double click into one request', async () => {
    let resolve!: (v: api.NexExitWorkerResult) => void
    vi.mocked(api.nexExitWorker).mockReturnValue(new Promise((r) => { resolve = r }))
    const a = exitWorker({ hostId: 'h1', executionId: 'E1' })
    const b = exitWorker({ hostId: 'h1', executionId: 'E1' })
    resolve({ exited: true, terminated: false, archived: true, state: 'failed' })
    await Promise.all([a, b])
    expect(api.nexExitWorker).toHaveBeenCalledTimes(1)
  })

  it('names the holder on held_by', () => {
    const t = useI18nStore.getState().t
    const msg = exitErrorMessage(new HandoffApiError(409, 'held_by', { principal: 'ploom:agent-7' }), t)
    expect(msg).toContain('ploom:agent-7')
  })
})
```

The real `refetch` signature lives in `useExecutionListStore` (:18-42); call it the way `ExecutionsView`'s retry does. Use the `singleFlight` helper that `handoff.ts` already uses. If it is module-private there, move it to `lib/nex/single-flight.ts` in this task. That is a pure move, so prove it with a byte comparison of the declaration.

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/lib/nex/exit-worker.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement** `nexExitWorker` with the existing `postJson` (`postJson<NexExitWorkerResult>(hostId, \`/api/nex/executions/${encodeURIComponent(executionId)}/exit\`, body.lease_id ? { lease_id: body.lease_id } : {})`). Then implement `exitWorker` and `exitErrorMessage`:
- `held_by` → `worker.exit.held_by` with `principal: err.body.principal ?? '?'`;
- any other `HandoffApiError` → `worker.exit.failed` with `reason: err.message || err.code`;
- anything else → `worker.exit.failed` with `String(err)`.

Add the locale keys to both files.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/lib/nex src/locales`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/nex/handoff-api.ts spa/src/lib/nex/exit-worker.ts spa/src/lib/nex/exit-worker.test.ts spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): exit-worker client and flow

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

(Add `spa/src/lib/nex/single-flight.ts` and `spa/src/lib/nex/handoff.ts` to `--only` if the helper moved.)

### Task 16: Pane header — "退出" replaces terminate

**Files:**
- Modify: `spa/src/components/execution/ExecutionHeader.tsx` (props, wide button, overflow item)
- Modify: `spa/src/components/execution/ExecutionView.tsx:520-545` (wiring, confirm dialog)
- Modify: `spa/src/hooks/useExecutionActions.ts:149-153` (remove `handleTerminate`; the admin table keeps its own)
- Test: `spa/src/components/execution/ExecutionHeader.test.tsx`, `spa/src/components/execution/ExecutionView.test.tsx`, `spa/src/hooks/useExecutionActions.test.ts`

**Interfaces:**
- Consumes: `exitWorker`, `exitErrorMessage` (Task 15); `isLiveRow` (Task 13).
- Produces: `ExecutionHeaderProps` loses `onTerminate` and gains:
  - `onExit: () => void`;
  - `exitDisabled: boolean`.

  `busy` keeps gating interrupt only. testids `header-exit` (wide) and `overflow-exit` (overflow) replace the terminate ones.
- Behaviour in `ExecutionView`:
  - Exit is enabled iff `summary` is live (`isLiveRow`) and no take-back or exit is in flight. So `failed` / `rejected` can exit.
  - `running` → a `ConfirmDialog` (`testIdPrefix="exit"`, title `worker.exit.confirm_title`, body `worker.exit.confirm_running`, confirm label `worker.exit.button`).
  - Any other live state → exits at once.
  - Errors go to `useUndoToast.getState().show(exitErrorMessage(err, t))`.
  - The lease id is `exec.lease?.leaseId`, as `runTakeBack` reads it; `forgetLease` is `lease.forget`.

- [ ] **Step 1: Write the failing tests**

```tsx
// ExecutionHeader.test.tsx (replace the terminate cases)
it('offers 退出 and calls onExit; no terminate control remains', async () => {
  const onExit = vi.fn()
  renderHeader({ summary: summaryOf({ state: 'idle' }), onExit, exitDisabled: false })
  await userEvent.click(screen.getByTestId('header-exit'))
  expect(onExit).toHaveBeenCalledTimes(1)
  expect(screen.queryByText(/terminate|終止/i)).toBeNull()
})
it('disables 退出 when exitDisabled', () => {
  renderHeader({ summary: summaryOf({ state: 'terminated' }), onExit: vi.fn(), exitDisabled: true })
  expect(screen.getByTestId('header-exit')).toBeDisabled()
})

// ExecutionView.test.tsx
it('exits an idle worker without a confirm and passes the held lease', async () => {
  seedExecution({ state: 'idle', lease: { leaseId: 'L1' } })
  vi.mocked(exitWorker).mockResolvedValue({ exited: true, terminated: true, archived: true, state: 'terminated' })
  renderView()
  await userEvent.click(screen.getByTestId('header-exit'))
  expect(exitWorker).toHaveBeenCalledWith(expect.objectContaining({ hostId: H, executionId: E, leaseId: 'L1' }))
  expect(screen.queryByTestId('exit-confirm')).toBeNull()
})
it('asks before exiting a running worker', async () => {
  seedExecution({ state: 'running' })
  renderView()
  await userEvent.click(screen.getByTestId('header-exit'))
  expect(exitWorker).not.toHaveBeenCalled()
  await userEvent.click(screen.getByTestId('exit-confirm'))
  expect(exitWorker).toHaveBeenCalledTimes(1)
})
it('lets a failed worker exit', async () => {
  seedExecution({ state: 'failed' })
  renderView()
  expect(screen.getByTestId('header-exit')).toBeEnabled()
})
it('shows the held_by message on refusal', async () => {
  seedExecution({ state: 'idle' })
  vi.mocked(exitWorker).mockRejectedValue(new HandoffApiError(409, 'held_by', { principal: 'ploom:agent-7' }))
  renderView()
  await userEvent.click(screen.getByTestId('header-exit'))
  await waitFor(() => expect(useUndoToast.getState().toast?.message).toContain('ploom:agent-7'))
})
```

Use `ExecutionView.test.tsx`'s existing render and seed helpers, and mock `../../lib/nex/exit-worker`. The confirm testid follows `ConfirmDialog`'s `testIdPrefix` convention (check what `${prefix}-confirm` is called in `ConfirmDialog.tsx`).

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/components/execution`
Expected: FAIL.

- [ ] **Step 3: Implement.** In `ExecutionHeader`:
- remove the terminate two-click state and `TERMINATE_CONFIRM_MS`, unless another importer uses it (`rg TERMINATE_CONFIRM_MS`);
- render `<button data-testid="header-exit" disabled={exitDisabled} onClick={onExit} className={`${ACTION} text-status-error`}><SignOut size={12} /> {t('worker.exit.button')}</button>` in the wide actions, with the same `overflow-exit` item.

In `ExecutionView`:
- add `exitBusy` state and `confirmExit` state;
- add `const exitable = !!st.summary && isLiveRow(st.summary)`;
- pass `exitDisabled={!exitable || exitBusy || takeBackBusy}`;
- `onExit` → `st.summary.state === 'running' ? setConfirmExit(true) : void runExit()`;
- `runExit` sets busy, calls `exitWorker({ hostId, executionId, leaseId: exec?.lease?.leaseId, forgetLease: lease.forget })`, toasts on error, and clears busy in `finally`.

Remove `handleTerminate` from `useExecutionActions` and its test.

Also in `ExecutionView.runTakeBack` (plan review #5): when `takeBack` / `takeToTerminal` resolves with `result.exited === false`, call `useUndoToast.getState().show(t('worker.exit.after_transfer_failed'), undefined, undefined, { persistent: true })`. The terminal took over, but the worker is still live. Add the copy to both locales (zh-TW: `worker.exit.after_transfer_failed` 已在終端機接續，但 worker 未能退出，請到清單手動退出). Add `exited?: boolean; exit_error?: string` to `NexTakeToTerminalResult` and `NexTakebackResult` in `handoff-api.ts`. Test: a mocked `takeToTerminal` resolving `{ result: { …, exited: false }, swapped: true }` shows the notice; `exited: true` shows none.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/components/execution src/hooks && npx tsc --noEmit -p tsconfig.app.json`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/components/execution/ExecutionHeader.tsx spa/src/components/execution/ExecutionHeader.test.tsx spa/src/components/execution/ExecutionView.tsx spa/src/components/execution/ExecutionView.test.tsx spa/src/hooks/useExecutionActions.ts spa/src/hooks/useExecutionActions.test.ts spa/src/lib/nex/handoff-api.ts spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): worker pane header offers 退出 instead of terminate

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 17: List rows — "退出" row action

**Files:**
- Modify: `spa/src/components/executions/ExecutionRowCompact.tsx` (optional `onExit`, wrapper)
- Modify: `spa/src/components/executions/ExecutionsView.tsx` (pass `onExit`; own the running confirm)
- Test: `spa/src/components/executions/ExecutionRowCompact.test.tsx`, `spa/src/components/executions/ExecutionsView.test.tsx`

**Interfaces:**
- Produces: the `ExecutionRowCompact` prop `onExit?: () => void`. When present, the row renders inside `<div className="group relative flex items-center">`. The existing button or list item comes first, then a sibling `<button data-testid="executions-row-exit" aria-label={t('worker.exit.button')}>`, shown on `group-hover` and `focus-within` and always reachable by keyboard. There are no nested buttons.
- `ExecutionsView`:
  - passes `onExit` only for rows on a shown host (`shown`);
  - `running` → the same `ConfirmDialog` (`testIdPrefix="exit"`), else exits at once;
  - calls `exitWorker({ hostId, executionId })` with no lease (the daemon acquires or borrows, D4);
  - errors → toast `exitErrorMessage`.

- [ ] **Step 1: Write the failing tests**

```tsx
// ExecutionRowCompact.test.tsx
it('renders an exit action beside the open button, not inside it', async () => {
  const onOpen = vi.fn(), onExit = vi.fn()
  render(<ExecutionRowCompact row={rowOf({ state: 'idle' })} daemonHostId={null} now={0} onOpen={onOpen} onExit={onExit} />)
  const exit = screen.getByTestId('executions-row-exit')
  expect(exit.closest('button[data-testid="executions-row"]')).toBeNull()
  await userEvent.click(exit)
  expect(onExit).toHaveBeenCalledTimes(1)
  expect(onOpen).not.toHaveBeenCalled()
})
it('has no exit action without onExit', () => {
  render(<ExecutionRowCompact row={rowOf({})} daemonHostId={null} now={0} />)
  expect(screen.queryByTestId('executions-row-exit')).toBeNull()
})

// ExecutionsView.test.tsx
it('exits an idle row directly and confirms a running one', async () => {
  seedList([rowOf({ id: 'I', state: 'idle', session_id: 'S1' }), rowOf({ id: 'R', state: 'running', session_id: 'S2' })])
  vi.mocked(exitWorker).mockResolvedValue({ exited: true, terminated: true, archived: true, state: 'terminated' })
  renderView()
  const [idleExit, runningExit] = screen.getAllByTestId('executions-row-exit')
  await userEvent.click(idleExit)
  expect(exitWorker).toHaveBeenCalledWith({ hostId: A, executionId: 'I' })
  await userEvent.click(runningExit)
  expect(exitWorker).toHaveBeenCalledTimes(1)
  await userEvent.click(screen.getByTestId('exit-confirm'))
  expect(exitWorker).toHaveBeenLastCalledWith({ hostId: A, executionId: 'R' })
})
```

(Use the row testid the component actually renders. If it differs from `executions-row`, match it.)

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/components/executions`
Expected: FAIL.

- [ ] **Step 3: Implement** as described. Use the `SignOut` icon at size 12, `text-text-muted hover:text-status-error`, `opacity-0 group-hover:opacity-100 focus:opacity-100`.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/components/executions && pnpm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/components/executions/ExecutionRowCompact.tsx spa/src/components/executions/ExecutionRowCompact.test.tsx spa/src/components/executions/ExecutionsView.tsx spa/src/components/executions/ExecutionsView.test.tsx -m "feat(spa): exit a worker from its list row

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 18: Toast when a manual resume exited a worker (Q1)

**Files:**
- Modify: `spa/src/hooks/useMultiHostEventWs.ts:195-215` (handle `nex-worker-exited`)
- Create: `spa/src/lib/nex/worker-label.ts` + `spa/src/lib/nex/worker-label.test.ts` (`workerLabel(row)`: `session_title?.text` → first line of brief → id)
- Create: `spa/src/lib/nex/worker-exited-event.ts` (parse + name lookup; keeps the hook thin)
- Create: `spa/src/lib/nex/worker-exited-event.test.ts`
- Test: `spa/src/hooks/useMultiHostEventWs.test.ts` (append, or the hook's existing test file)

**Interfaces:**
- Produces:

```ts
export interface WorkerExitedEvent { executionId: string; sessionId: string; reason: string; tmuxSession: string }
export function parseWorkerExited(value: unknown): WorkerExitedEvent | null // JSON text or object; requires string execution_id
/** session_title.text (types.ts:58 — an object `{text, source}`, never a string), else the first line of brief, from the host's list store row; else the tmux session name. */
export function workerExitedName(hostId: string, ev: WorkerExitedEvent): string
export function handleWorkerExited(hostId: string, value: unknown): void // toast worker.exit.manual_resume + refetch(hostId)
```

- [ ] **Step 1: Write the failing tests**

```ts
// spa/src/lib/nex/worker-exited-event.test.ts
describe('nex-worker-exited', () => {
  it('parses the daemon value (JSON text)', () => {
    expect(parseWorkerExited('{"execution_id":"E1","session_id":"S","reason":"manual_resume","tmux_session":"proj-2"}'))
      .toEqual({ executionId: 'E1', sessionId: 'S', reason: 'manual_resume', tmuxSession: 'proj-2' })
    expect(parseWorkerExited('nope')).toBeNull()
    expect(parseWorkerExited('{"session_id":"S"}')).toBeNull()
  })
  it('names the worker by its list row, else the tmux session', () => {
    useExecutionListStore.setState({ byHost: { h1: listCacheOf([rowOf({ id: 'E1', session_title: { text: '修 bug', source: 'ai' }, brief: 'x' })]) } })
    expect(workerExitedName('h1', ev('E1'))).toBe('修 bug')
    expect(workerExitedName('h1', ev('E9'))).toBe('proj-2')
  })
  it('toasts and refetches', () => {
    const show = vi.spyOn(useUndoToast.getState(), 'show')
    handleWorkerExited('h1', '{"execution_id":"E9","session_id":"S","reason":"manual_resume","tmux_session":"proj-2"}')
    expect(show).toHaveBeenCalledWith('proj-2 已在終端機接續，worker 已退出')
  })
})
```

The test locale is zh-TW only if `test-setup.ts` makes it so; otherwise assert against `t('worker.exit.manual_resume', { name: 'proj-2' })`.

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/lib/nex/worker-exited-event.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement.** In the hook, before the `handoff`/`relay` fall-through comment, add: `if (event.type === 'nex-worker-exited') { handleWorkerExited(hostId, event.value); return }`. `session_title` is `{ text: string; source: string } | undefined` (`types.ts:58`); read `row.session_title?.text`. Put the label rule in one helper, `workerLabel(row): string` (session_title.text → first line of brief → id) in `lib/nex/worker-label.ts`, with its own test. Task 26 reuses it.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/lib/nex src/hooks && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/nex/worker-label.ts spa/src/lib/nex/worker-label.test.ts spa/src/lib/nex/worker-exited-event.ts spa/src/lib/nex/worker-exited-event.test.ts spa/src/hooks/useMultiHostEventWs.ts spa/src/hooks/useMultiHostEventWs.test.ts -m "feat(spa): toast when a terminal resume exited a worker

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

**PR P1b-2:** `feat(spa): conversation entity P1b-2 — 退出 and the manual-resume toast`. Then codex R1 + R2.

---
## Phase P1c-1 — daemon: `POST /api/nex/worker-rebuild` (D11)

### Task 19: Worker rebuild endpoint

**Files:**
- Create: `internal/module/nex/worker_rebuild.go`
- Create: `internal/module/nex/worker_rebuild_test.go`
- Modify: `internal/module/nex/module.go` (`RegisterRoutes`)

**Interfaces:**
- Consumes: `sidLockKey`, `takeToTerminalLockKey`, `checkOwners`, `exitWorker`, `executionIsFor`, `isLiveExecution`; `handoffProfile` and the `sandbox.UsableProfiles` check from `handoff.go:147-151`.
- Produces:
  - `POST /api/nex/worker-rebuild` with body `{session_id: string, cwd: string, profile?: string, replace_execution_id?: string}`.
  - 200 → `{execution_id, state, effective_profile, reject_reason?}`. A `rejected` state is data, not an error, as in Nexen.
  - Errors:
    - 503 `nex_unavailable`;
    - 409 `handoff_unsupported`;
    - 400 `malformed_body` / `missing_session_id` / `missing_cwd`;
    - 409 `cwd_missing`;
    - 409 `transfer_in_progress` (sid or replace lock);
    - 404 `execution_not_found` (the replaced row);
    - 409 `replace_mismatch` (the replaced row is not for S);
    - `checkOwners` codes;
    - `exitWorker` codes, with `detail.step = "exit_replaced"`;
    - 500 `delegate_failed`.
  - Delegate request: `Brief: "(rebuilt as worker)"` (`rebuildBrief`; P3b removes it), `SandboxProfile: body.Profile || handoffProfile`, a cwd mount, `Origin: "purdex://host/<hostID>/rebuild"`, `Labels: {"source": "purdex"}` plus `"rebuild_of": <replace id>` when one is given, and `ResumeSessionID: S`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/module/nex/worker_rebuild_test.go
func TestWorkerRebuild(t *testing.T) {
	post := func(env *handoffEnv, body string) *httptest.ResponseRecorder {
		return env.post(t, "/api/nex/worker-rebuild", body)
	}
	t.Run("fresh stint for a free session", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.delegateResult = execution.Result{ID: "N1", State: store.StateRunning, EffectiveProfile: "handoff"}
		rec := post(env, `{"session_id":"S","cwd":"/w"}`)
		if rec.Code != 200 || decode(t, rec)["execution_id"] != "N1" {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		req := env.svc.delegateCalls[0]
		if req.ResumeSessionID != "S" || req.Brief != rebuildBrief || req.SandboxProfile != "handoff" || req.Mounts[0].Path != "/w" || req.Labels["source"] != "purdex" {
			t.Fatalf("req = %+v", req)
		}
		if _, ok := req.Labels["rebuild_of"]; ok {
			t.Fatal("no rebuild_of without a replaced execution")
		}
	})
	t.Run("replacing a failed stint exits it first", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.store.script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: "S"})
		env.store.listRows = []store.Execution{row("F1", "failed", false, "S", "", 1)}
		env.svc.delegateResult = execution.Result{ID: "N1", State: store.StateRunning}
		rec := post(env, `{"session_id":"S","cwd":"/w","profile":"readonly","replace_execution_id":"F1"}`)
		if rec.Code != 200 || len(env.svc.ArchiveCalls()) != 1 || env.svc.delegateCalls[0].SandboxProfile != "readonly" || env.svc.delegateCalls[0].Labels["rebuild_of"] != "F1" {
			t.Fatalf("%d archive=%d", rec.Code, len(env.svc.ArchiveCalls()))
		}
		if env.svc.archiveAt().After(env.svc.delegateAt()) {
			t.Fatal("the replaced stint must exit before the new one starts")
		}
	})
	t.Run("refusals", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.terminals.live = map[string][]agent.TerminalSession{"S": {{PaneID: "%3", SessionID: "S", AgentType: "cc", Verified: true}}}
		assertErrorCode(t, post(env, `{"session_id":"S","cwd":"/w"}`), 409, "session_owned")

		env = newHandoffEnv(t)
		env.store.listRows = []store.Execution{row("L1", "idle", false, "S", "", 1)}
		assertErrorCode(t, post(env, `{"session_id":"S","cwd":"/w"}`), 409, "session_owned")

		env = newHandoffEnv(t)
		env.store.script(store.Execution{ID: "X1", State: store.StateFailed, SessionID: "OTHER"})
		assertErrorCode(t, post(env, `{"session_id":"S","cwd":"/w","replace_execution_id":"X1"}`), 409, "replace_mismatch")

		env = newHandoffEnv(t)
		env.m.locks.TryLock(sidLockKey("S"))
		assertErrorCode(t, post(env, `{"session_id":"S","cwd":"/w"}`), 409, "transfer_in_progress")

		env = newHandoffEnv(t)
		assertErrorCode(t, post(env, `{"cwd":"/w"}`), 400, "missing_session_id")
		assertErrorCode(t, post(env, `{"session_id":"S"}`), 400, "missing_cwd")

		// The replaced row is mid-exit / mid-transfer elsewhere: refused before any exit or delegate.
		env = newHandoffEnv(t)
		env.store.script(store.Execution{ID: "F1", State: store.StateFailed, SessionID: "S"})
		env.m.locks.TryLock(takeToTerminalLockKey("F1"))
		assertErrorCode(t, post(env, `{"session_id":"S","cwd":"/w","replace_execution_id":"F1"}`), 409, "transfer_in_progress")
		if len(env.svc.ArchiveCalls())+len(env.svc.delegateCalls) != 0 {
			t.Fatal("no exit and no delegate while the replaced row is locked")
		}
	})
	t.Run("rejected is data", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.delegateResult = execution.Result{ID: "R1", State: store.StateRejected, RejectReason: "session_expired"}
		rec := post(env, `{"session_id":"S","cwd":"/w"}`)
		if b := decode(t, rec); rec.Code != 200 || b["state"] != "rejected" || b["reject_reason"] != "session_expired" {
			t.Fatalf("%d %v", rec.Code, b)
		}
	})
	t.Run("delegate error → 500", func(t *testing.T) {
		env := newHandoffEnv(t)
		env.svc.delegateErr = errors.New("spawn failed")
		assertErrorCode(t, post(env, `{"session_id":"S","cwd":"/w"}`), 500, "delegate_failed")
	})
}
```

The cwd validation goes through `m.sessions.ValidateCwd`. Make the fixture accept `/w`, which is what the take-to-terminal tests configure. Add `archiveAt` / `delegateAt` recorders to the fakes if they are missing.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/module/nex/ -run TestWorkerRebuild`
Expected: FAIL (404, no route).

- [ ] **Step 3: Implement** `handleWorkerRebuild`. Follow the step order in Interfaces and the handler idiom of `handleTakeToTerminal`: detached bounded contexts, `writeHandoffError`, and `m.logf` on success. The sid lock comes **before** the replace lock: transfers take `sid` → `exec`, except take-to-terminal, which takes `exec` → `sid`. All are `TryLock`, so they cannot deadlock. Register it in `RegisterRoutes`: `mux.HandleFunc("POST "+RoutePrefix+"/worker-rebuild", m.handleWorkerRebuild)`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/module/nex/ && go vet ./internal/module/nex/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/nex/worker_rebuild.go internal/module/nex/worker_rebuild_test.go internal/module/nex/module.go internal/module/nex/handoff_fakes_test.go -m "feat(daemon): POST /api/nex/worker-rebuild — a new worker stint for a session

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

**PR P1c-1:** `feat(daemon): conversation entity P1c-1 — worker rebuild endpoint`. Then codex R1 + R2. Report "daemon deployable (P1a + P1c-1)" to the coordinator; the SPA P1c-2 needs this daemon live to be tested by hand.

---

## Phase P1c-2 — SPA: rebuild screen, worker ended screens, failed handoff

### Task 20: `RebuildScreen` layout and `RebuildModeChoice`

**Files:**
- Create: `spa/src/components/RebuildScreen.tsx`
- Create: `spa/src/components/RebuildModeChoice.tsx`
- Create: `spa/src/components/RebuildModeChoice.test.tsx`
- Modify: `spa/src/components/TerminatedPane.tsx` (render through `RebuildScreen`; a pure layout move)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`
- Test: `spa/src/components/TerminatedPane.test.tsx` (must pass unchanged)

**Interfaces:**
- Produces:

```tsx
export interface RebuildScreenProps {
  icon: ReactNode; title: string; description?: string
  detail?: ReactNode          // e.g. the failure reason line
  closeLabel: string; onClose: () => void
  children: ReactNode          // the rebuild block(s)
  testId?: string
}
export type RebuildMode = 'terminal' | 'worker'
export interface RebuildModeChoiceProps {
  value: RebuildMode; onChange: (m: RebuildMode) => void
  terminalAvailable: boolean; workerAvailable: boolean
  workerUnavailableHint?: string
}
```

- `RebuildModeChoice` renders `role="radiogroup"` with two `role="radio"` buttons, `rebuild-mode-terminal` and `rebuild-mode-worker`, showing `aria-checked`. An unavailable option is disabled and its title carries the hint.
- Copy (zh-TW):
  - `worker.rebuild.mode_label`: 重建為
  - `worker.rebuild.terminal`: 終端機
  - `worker.rebuild.worker`: Worker
  - `worker.rebuild.worker_unavailable`: 這台主機的 Nexen 尚未就緒，或不知道這段對話的 session id／工作目錄
  - `worker.rebuild.button`: 重建
  - `worker.rebuild.failed`: 重建失敗：{{reason}}
  - `worker.rebuild.owned_terminal`: 這段對話已在終端機中開啟
  - `worker.rebuild.owned_worker`: 這段對話已有一個進行中的 worker

- [ ] **Step 1: Write the failing tests**

```tsx
// spa/src/components/RebuildModeChoice.test.tsx
it('marks the preselected mode and switches', async () => {
  const onChange = vi.fn()
  render(<RebuildModeChoice value="worker" onChange={onChange} terminalAvailable workerAvailable />)
  expect(screen.getByTestId('rebuild-mode-worker')).toHaveAttribute('aria-checked', 'true')
  await userEvent.click(screen.getByTestId('rebuild-mode-terminal'))
  expect(onChange).toHaveBeenCalledWith('terminal')
})
it('disables an unavailable worker option with its hint', () => {
  render(<RebuildModeChoice value="terminal" onChange={vi.fn()} terminalAvailable workerAvailable={false} workerUnavailableHint="nope" />)
  const w = screen.getByTestId('rebuild-mode-worker')
  expect(w).toBeDisabled()
  expect(w).toHaveAttribute('title', 'nope')
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/components/RebuildModeChoice.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement.** `RebuildScreen` is `TerminatedPane`'s outer markup (`TerminatedPane.tsx:66-91`) lifted out: icon, title, description, detail, close button, children. `TerminatedPane` then renders `<RebuildScreen icon={<SmileySad …/>} …>{RebuildActionSet}{SessionPickerList}</RebuildScreen>` with identical classes, so its tests stay green untouched.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/components/RebuildModeChoice.test.tsx src/components/TerminatedPane.test.tsx src/locales`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/components/RebuildScreen.tsx spa/src/components/RebuildModeChoice.tsx spa/src/components/RebuildModeChoice.test.tsx spa/src/components/TerminatedPane.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): shared rebuild screen layout and the terminal / worker choice

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 21: Worker ended screens — exited and start failed (§6, §7)

**Files:**
- Modify: `spa/src/lib/nex/handoff-api.ts` (`nexWorkerRebuild`)
- Create: `spa/src/lib/nex/worker-rebuild.ts`
- Create: `spa/src/lib/nex/worker-rebuild.test.ts`
- Create: `spa/src/components/execution/WorkerEndedPane.tsx`
- Create: `spa/src/components/execution/WorkerEndedPane.test.tsx`
- Modify: `spa/src/components/execution/ExecutionView.tsx` (render `WorkerEndedPane` once the summary is ended)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Consumes: `RebuildScreen`, `RebuildModeChoice` (Task 20); `isLiveRow` (Task 13); `takeToTerminal` (`lib/nex/handoff.ts:255`); `executionContentFor` (`handoff.ts:90`); `selectHandoffReady` (`useNexHostStore`); `singleFlight`.
- Produces:

```ts
// handoff-api.ts
export interface NexWorkerRebuildRequest { session_id: string; cwd: string; profile?: string; replace_execution_id?: string }
export interface NexWorkerRebuildResult { execution_id: string; state: string; effective_profile?: string; reject_reason?: string }
export function nexWorkerRebuild(hostId: string, body: NexWorkerRebuildRequest): Promise<NexWorkerRebuildResult>
// worker-rebuild.ts
export interface RebuildAsWorkerArgs {
  hostId: string; sessionId: string; cwd: string; profile?: string; replaceExecutionId?: string
  tabId: string; paneId: string
  /** CAS on the pane's current content (trySetPaneContent). */
  expect: (c: PaneContent) => boolean
}
/** single-flight `rebuild:${hostId}:${paneId}`; on 200 swaps the pane to the new execution (even when rejected — it then shows start failed). */
export function rebuildAsWorker(args: RebuildAsWorkerArgs): Promise<{ result: NexWorkerRebuildResult; swapped: boolean }>
export function rebuildErrorMessage(err: unknown, t: T): string // session_owned → owned_terminal / owned_worker; else worker.rebuild.failed
// WorkerEndedPane.tsx
export type WorkerEndedKind = 'exited' | 'failed'
export function workerEndedKind(s: ExecutionSummary | null): WorkerEndedKind | null // !live → exited; live failed/rejected → failed; else null
export function WorkerEndedPane(props: { hostId: string; executionId: string; summary: ExecutionSummary; tabId: string; paneId: string }): JSX.Element
```

- Behaviour:
  - Preselection is `worker` (D12).
  - Terminal is available iff `summary.provider === 'claude'` and `sid = session_id || resume_session_id` and `summary.cwd`.
  - Worker is available iff `selectHandoffReady(hostId)` and `sid` and `cwd`.
  - **Terminal** → `takeToTerminal({ hostId, executionId, cwd, tabId, paneId, forgetLease: () => {} })`. The daemon accepts exited and rejected executions (D6).
  - **Worker** → `rebuildAsWorker({ …, profile: summary.effective_profile, replaceExecutionId: isLiveRow(summary) ? executionId : undefined, expect: (c) => c.kind === 'execution' && c.executionId === executionId && (c.host ?? hostId) === hostId })`.
  - Errors show inline, under the button, as `worker-rebuild-error`.
- Copy (zh-TW):
  - `worker.ended.exited_title`: 這個 worker 已退出
  - `worker.ended.exited_desc`: 可以在終端機或新的 worker 接續這段對話。
  - `worker.ended.failed_title`: 啟動失敗
  - `worker.ended.reason`: 原因：{{reason}}
  - `worker.ended.close_tab`: 關閉分頁

- [ ] **Step 1: Write the failing tests**

```tsx
// spa/src/components/execution/WorkerEndedPane.test.tsx
vi.mock('../../lib/nex/handoff', async (o) => ({ ...(await o<typeof import('../../lib/nex/handoff')>()), takeToTerminal: vi.fn() }))
vi.mock('../../lib/nex/worker-rebuild', async (o) => ({ ...(await o<typeof import('../../lib/nex/worker-rebuild')>()), rebuildAsWorker: vi.fn() }))

describe('workerEndedKind', () => {
  it('classifies', () => {
    expect(workerEndedKind(sum({ state: 'terminated' }))).toBe('exited')
    expect(workerEndedKind(sum({ state: 'idle', archived: true }))).toBe('exited')
    expect(workerEndedKind(sum({ state: 'failed' }))).toBe('failed')
    expect(workerEndedKind(sum({ state: 'rejected' }))).toBe('failed')
    expect(workerEndedKind(sum({ state: 'idle' }))).toBeNull()
    expect(workerEndedKind(null)).toBeNull()
  })
})

describe('WorkerEndedPane', () => {
  beforeEach(() => useNexHostStore.setState({ byHost: { [H]: handoffReadyEntry } }))

  it('exited: worker preselected; rebuild as worker replaces nothing', async () => {
    renderPane(sum({ state: 'terminated', archived: true, session_id: 'S', cwd: '/w', effective_profile: 'handoff' }))
    expect(screen.getByText('這個 worker 已退出')).toBeInTheDocument()
    expect(screen.getByTestId('rebuild-mode-worker')).toHaveAttribute('aria-checked', 'true')
    await userEvent.click(screen.getByTestId('worker-rebuild'))
    expect(rebuildAsWorker).toHaveBeenCalledWith(expect.objectContaining({ hostId: H, sessionId: 'S', cwd: '/w', profile: 'handoff', replaceExecutionId: undefined }))
  })

  it('failed: shows the reason and replaces the failed stint', async () => {
    renderPane(sum({ state: 'rejected', reject_reason: 'session_expired', resume_session_id: 'S', cwd: '/w' }))
    expect(screen.getByText('啟動失敗')).toBeInTheDocument()
    expect(screen.getByText(/session_expired/)).toBeInTheDocument()
    await userEvent.click(screen.getByTestId('worker-rebuild'))
    expect(rebuildAsWorker).toHaveBeenCalledWith(expect.objectContaining({ sessionId: 'S', replaceExecutionId: E }))
  })

  it('terminal choice takes it to a new terminal', async () => {
    renderPane(sum({ state: 'terminated', archived: true, session_id: 'S', cwd: '/w' }))
    await userEvent.click(screen.getByTestId('rebuild-mode-terminal'))
    await userEvent.click(screen.getByTestId('worker-rebuild'))
    expect(takeToTerminal).toHaveBeenCalledWith(expect.objectContaining({ hostId: H, executionId: E, cwd: '/w' }))
  })

  it('without nex ready the worker option is disabled and terminal is preselected', () => {
    useNexHostStore.setState({ byHost: { [H]: notReadyEntry } })
    renderPane(sum({ state: 'terminated', archived: true, session_id: 'S', cwd: '/w' }))
    expect(screen.getByTestId('rebuild-mode-worker')).toBeDisabled()
    expect(screen.getByTestId('rebuild-mode-terminal')).toHaveAttribute('aria-checked', 'true')
  })

  it('shows an owner refusal inline', async () => {
    vi.mocked(rebuildAsWorker).mockRejectedValue(new HandoffApiError(409, 'session_owned', { owner: 'terminal' }))
    renderPane(sum({ state: 'terminated', archived: true, session_id: 'S', cwd: '/w' }))
    await userEvent.click(screen.getByTestId('worker-rebuild'))
    expect(await screen.findByTestId('worker-rebuild-error')).toHaveTextContent('這段對話已在終端機中開啟')
  })
})
```

`worker-rebuild.test.ts`:
- `rebuildAsWorker` posts the body (omitting absent optionals) and swaps the pane with the CAS;
- a double call is one request;
- `swapped: false` when the CAS fails;
- `rebuildErrorMessage` maps `session_owned` with owner `worker` / `terminal`, and anything else to `failed`.

Add to `ExecutionView.test.tsx`: a terminated + archived summary renders `WorkerEndedPane` (mock it), and so does a `failed` one. A live idle one renders the transcript as before.

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/components/execution src/lib/nex/worker-rebuild.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement.** In `ExecutionView`, right after the `problem` early return: `const endedKind = workerEndedKind(st.summary); if (endedKind && st.summary) return <WorkerEndedPane hostId={hostId} executionId={executionId} summary={st.summary} tabId={tabId} paneId={paneId} />`. Hooks above the early returns stay unconditional; check the component has no hook after this line, and move the line below the last hook if needed. If the preselected mode becomes unavailable, `WorkerEndedPane` falls back to the available one. With neither available, the button is disabled and the description says why (`worker.rebuild.worker_unavailable`).

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/components src/lib/nex && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/nex/handoff-api.ts spa/src/lib/nex/worker-rebuild.ts spa/src/lib/nex/worker-rebuild.test.ts spa/src/components/execution/WorkerEndedPane.tsx spa/src/components/execution/WorkerEndedPane.test.tsx spa/src/components/execution/ExecutionView.tsx spa/src/components/execution/ExecutionView.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): exited and start-failed worker panes with terminal / worker rebuild

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 22: A rejected handoff that was not rolled back opens its start-failed pane (D7)

**Files:**
- Modify: `spa/src/lib/nex/handoff.ts:134-158` (`handToNex`)
- Modify: `spa/src/lib/nex/handoff-api.ts` (the `delegate_rejected` body type gains `execution_id?`, `exited?`)
- Test: `spa/src/lib/nex/handoff.test.ts`

**Interfaces:**
- Produces: on `HandoffApiError` with `code === 'delegate_rejected'`, `body.rolled_back !== true` and a string `body.execution_id`, `handToNex` swaps the pane to `executionContentFor(hostId, body.execution_id)` with the same CAS it uses on success. It then **rethrows**, so the dialog still shows the error. A rolled-back rejection leaves the pane alone, as today.

- [ ] **Step 1: Write the failing tests**

```ts
it('a rejection that was not rolled back turns the pane into that worker', async () => {
  vi.mocked(nexHandoff).mockRejectedValue(new HandoffApiError(409, 'delegate_rejected', { rolled_back: false, execution_id: 'R1', reject_reason: 'x' }))
  await expect(handToNex(args)).rejects.toMatchObject({ code: 'delegate_rejected' })
  expect(paneContent()).toMatchObject({ kind: 'execution', executionId: 'R1', host: H })
})
it('a rolled-back rejection leaves the terminal pane', async () => {
  vi.mocked(nexHandoff).mockRejectedValue(new HandoffApiError(409, 'delegate_rejected', { rolled_back: true, execution_id: 'R1', exited: true }))
  await expect(handToNex(args)).rejects.toBeTruthy()
  expect(paneContent()).toMatchObject({ kind: 'tmux-session' })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/lib/nex/handoff.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement.** Wrap the `nexHandoff` call in `try/catch` inside the single-flight body, do the swap in the catch under the conditions above, and rethrow.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/lib/nex src/components/HandoffConfirmDialog.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/nex/handoff.ts spa/src/lib/nex/handoff-api.ts spa/src/lib/nex/handoff.test.ts -m "feat(spa): a rejected, un-rolled-back handoff shows its start-failed worker pane

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

**PR P1c-2:** `feat(spa): conversation entity P1c-2 — worker ended screens and rebuild`. Then codex R1 + R2.

---

## Phase P1c-3 — SPA: a closed terminal pane can be rebuilt as a worker (Q4)

### Task 23: Mode choice on `TerminatedPane`

**Files:**
- Modify: `spa/src/components/TerminatedPane.tsx`
- Test: `spa/src/components/TerminatedPane.test.tsx` (append)

**Interfaces:**
- Consumes: `RebuildModeChoice`, `rebuildAsWorker`, `rebuildErrorMessage`, `selectHandoffReady`.
- Behaviour:
  - `sid = record.agent?.type === 'cc' ? record.agent.sessionId : undefined`.
  - The choice is shown only when `selectHandoffReady(hostId)` and `sid` and `record.cwd`, preselecting terminal (D12). Otherwise the screen is exactly as today.
  - `terminal` shows today's `RebuildActionSet` and `SessionPickerList`.
  - `worker` replaces them with a block showing the cwd and a `terminated-rebuild-worker` button. The button calls `rebuildAsWorker({ hostId, sessionId: sid, cwd: record.cwd, tabId, paneId, expect: (c) => c.kind === 'tmux-session' && c.hostId === content.hostId && c.sessionCode === content.sessionCode })`. Errors show inline (`terminated-rebuild-error`).
  - `useNexHostStore.getState().ensure(hostId)` runs on mount.

- [ ] **Step 1: Write the failing tests**

```tsx
it('offers worker rebuild when nex is ready and the record knows the session', async () => {
  useNexHostStore.setState({ byHost: { [H]: handoffReadyEntry } })
  renderTerminated({ rebuild: { sessionName: 'p1', tmuxInstance: 'i', cwd: '/w', agent: { type: 'cc', sessionId: 'S', updatedAt: 1 }, capturedAt: 1 } })
  expect(screen.getByTestId('rebuild-mode-terminal')).toHaveAttribute('aria-checked', 'true')
  await userEvent.click(screen.getByTestId('rebuild-mode-worker'))
  await userEvent.click(screen.getByTestId('terminated-rebuild-worker'))
  expect(rebuildAsWorker).toHaveBeenCalledWith(expect.objectContaining({ hostId: H, sessionId: 'S', cwd: '/w' }))
})
it('shows no choice without a session id, a cwd, or nex', () => {
  useNexHostStore.setState({ byHost: { [H]: handoffReadyEntry } })
  renderTerminated({ rebuild: { sessionName: 'p1', tmuxInstance: 'i', capturedAt: 1 } })
  expect(screen.queryByTestId('rebuild-mode-worker')).toBeNull()
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/components/TerminatedPane.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement** as described.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/components && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/components/TerminatedPane.tsx spa/src/components/TerminatedPane.test.tsx -m "feat(spa): a closed terminal pane can be rebuilt as a worker

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

**PR P1c-3:** `feat(spa): conversation entity P1c-3 — terminal pane rebuild as worker`. Then codex R1 + R2. P1 is then complete: report it to the coordinator with the deployable daemon set.

---

## Phase P2-1 — SPA: New Tab Sessions / Workers switch; Settings → Worker tabs

### Task 24: New Tab per-host Sessions / Workers switch (Q3)

**Files:**
- Create: `spa/src/components/HostWorkerRows.tsx` (shared by New Tab and Settings)
- Create: `spa/src/components/HostWorkerRows.test.tsx`
- Modify: `spa/src/components/SessionSection.tsx:74-170` (`HostSessionSection` header switch and body)
- Test: `spa/src/components/SessionSection.test.tsx` (append)
- Modify: locales

**Interfaces:**
- Consumes: `useHostExecutions`, `liveEntityRows`, `ExecutionRowCompact`, `selectReady`.
- Produces:

```tsx
export function HostWorkerRows(props: { hostId: string; onOpen: (executionId: string) => void; testIdPrefix: string }): JSX.Element
```

- `HostWorkerRows` renders live entity rows with `ExecutionRowCompact`, plus loading, error-with-retry and empty states (`${prefix}-empty`).
- In `HostSessionSection`, when `useNexHostStore(selectReady(hostId))`, the header shows a `role="tablist"` group of two buttons, `host-view-sessions-${hostId}` and `host-view-workers-${hostId}`, with `aria-selected`.
  - Local `useState<'sessions'|'workers'>('sessions')`; the cached per-host component keeps it.
  - Workers → `<HostWorkerRows hostId onOpen={(id) => onSelect({ kind: 'execution', executionId: id, host: hostId })} testIdPrefix={`newtab-workers-${hostId}`} />`.
  - When Nexen is not ready, the switch is hidden and the view is sessions.
- Copy (zh-TW): `newtab.view.sessions` Sessions, `newtab.view.workers` Workers, `newtab.workers.empty` 這台主機沒有進行中的 worker, `newtab.workers.error` 讀取失敗：{{error}}, `newtab.workers.retry` 重試.

- [ ] **Step 1: Write the failing tests**

```tsx
// SessionSection.test.tsx
it('switches a host block to its live workers and opens one', async () => {
  useNexHostStore.setState({ byHost: { [A]: readyEntry }, ensure: vi.fn() })
  vi.mocked(listExecutions).mockResolvedValue({ items: [rowOf({ id: 'E1', state: 'idle', session_id: 'S' }), rowOf({ id: 'E2', state: 'terminated', session_id: 'T' })], next_cursor: '' })
  const onSelect = vi.fn()
  render(<HostSessionSection hostId={A} onSelect={onSelect} />)
  await userEvent.click(screen.getByTestId(`host-view-workers-${A}`))
  const rows = await screen.findAllByTestId('executions-row')
  expect(rows).toHaveLength(1)
  await userEvent.click(rows[0])
  expect(onSelect).toHaveBeenCalledWith({ kind: 'execution', executionId: 'E1', host: A })
})
it('hides the switch when nex is not ready', () => {
  useNexHostStore.setState({ byHost: { [A]: disabledEntry }, ensure: vi.fn() })
  render(<HostSessionSection hostId={A} onSelect={vi.fn()} />)
  expect(screen.queryByTestId(`host-view-workers-${A}`)).toBeNull()
})
```

`HostWorkerRows.test.tsx`: the empty state, the error state with a retry calling `refetch`, and live-only rows.

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/components/SessionSection.test.tsx src/components/HostWorkerRows.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement** as described. `ExecutionsView` (activity bar) keeps its own markup. Do not refactor it onto `HostWorkerRows` in this task.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/components && pnpm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/components/HostWorkerRows.tsx spa/src/components/HostWorkerRows.test.tsx spa/src/components/SessionSection.tsx spa/src/components/SessionSection.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): New Tab host blocks switch between sessions and live workers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 25: Settings → Worker becomes a tab registry (Appearance, Workers)

**Files:**
- Create: `spa/src/lib/worker-settings-tabs.ts`
- Create: `spa/src/lib/worker-settings-tabs.test.ts`
- Create: `spa/src/components/settings/WorkerSettingsPage.tsx`
- Create: `spa/src/components/settings/WorkerSettingsPage.test.tsx`
- Create: `spa/src/components/settings/WorkerLiveTab.tsx`
- Modify: `spa/src/lib/register-modules/index.tsx:247-259` (setting component → `WorkerSettingsPage`; register the `appearance` and `workers` tabs)
- Modify: `spa/src/lib/__tests__/test-bootstrap-harness.ts` (clear the new registry)
- Modify: locales

**Interfaces:**
- Produces:

```ts
export interface WorkerSettingsTab {
  id: string; labelKey: string; order: number
  /** Host-scoped tabs get a host picker and receive hostId. Later tabs (Dormant, Aigora) plug in here (spec §9). */
  hostScoped: boolean
  component: ComponentType<{ hostId?: string }>
}
export function registerWorkerSettingsTab(def: WorkerSettingsTab): void // replace by id, keep sorted by order
export function getWorkerSettingsTabs(): WorkerSettingsTab[]
export function clearWorkerSettingsTabs(): void
```

- Registered tabs:
  - `appearance` (order 0, not host-scoped, the existing `WorkerSettingsSection` body);
  - `workers` (order 10, host-scoped, `WorkerLiveTab` = `HostWorkerRows` with `onOpen` → `openWorkerTab`).
- `WorkerSettingsPage`:
  - a `role="tablist"` strip (`border-b-2 border-accent` on the active tab, as `ThemeImportModal.tsx:137-153`), testids `worker-settings-tab-${id}`;
  - for host-scoped tabs, a `SegmentControl` over the shown hosts (`useShownHostsStore` + `useHostStore` names), defaulting to the first one;
  - the active tab is local state. A remembered tab is out of scope.
- Copy (zh-TW): `settings.worker.tabs.appearance` 外觀, `settings.worker.tabs.workers` Workers, `settings.worker.tabs.exited` 已退出, `settings.worker.no_hosts` 沒有顯示中的主機.

- [ ] **Step 1: Write the failing tests**

```ts
// worker-settings-tabs.test.ts
it('keeps tabs sorted and replaces by id', () => {
  clearWorkerSettingsTabs()
  registerWorkerSettingsTab({ id: 'b', labelKey: 'b', order: 20, hostScoped: false, component: () => null })
  registerWorkerSettingsTab({ id: 'a', labelKey: 'a', order: 10, hostScoped: false, component: () => null })
  registerWorkerSettingsTab({ id: 'b', labelKey: 'b2', order: 5, hostScoped: true, component: () => null })
  expect(getWorkerSettingsTabs().map((t) => `${t.id}:${t.labelKey}`)).toEqual(['b:b2', 'a:a'])
})
```

```tsx
// WorkerSettingsPage.test.tsx (after registerBuiltinModules via the harness)
it('shows Appearance first and switches to a host-scoped tab with a host picker', async () => {
  render(<WorkerSettingsPage ctx={ctx} />)
  expect(screen.getByTestId('worker-settings-tab-appearance')).toHaveAttribute('aria-selected', 'true')
  await userEvent.click(screen.getByTestId('worker-settings-tab-workers'))
  expect(screen.getByRole('radiogroup', { hidden: true }) ?? screen.getByText(hostAName)).toBeTruthy()
})
```

Write the second test against the real markup `SegmentControl` renders (check its role / testids, and assert on the host name text if it has no role).

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/lib/worker-settings-tabs.test.ts src/components/settings/WorkerSettingsPage.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement** as described. Keep `WorkerSettingsSection` intact as the appearance tab's component; the existing tests of it must stay green.

- [ ] **Step 4: Run the tests**

Run: `npx vitest run src/lib src/components/settings && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/worker-settings-tabs.ts spa/src/lib/worker-settings-tabs.test.ts spa/src/components/settings/WorkerSettingsPage.tsx spa/src/components/settings/WorkerSettingsPage.test.tsx spa/src/components/settings/WorkerLiveTab.tsx spa/src/lib/register-modules/index.tsx spa/src/lib/__tests__/test-bootstrap-harness.ts spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): Settings → Worker tabs (Appearance, Workers) on a tab registry

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

**PR P2-1:** `feat(spa): conversation entity P2-1 — New Tab workers switch, Settings → Worker tabs`. Then codex R1 + R2.

---

## Phase P2-2 — SPA: Settings → Worker → Exited

### Task 26: Exited tab — searchable, rebuild from here (§9, D10)

**Files:**
- Create: `spa/src/lib/nex/exited-entities.ts`
- Create: `spa/src/lib/nex/exited-entities.test.ts`
- Create: `spa/src/hooks/useExecutionHistory.ts`
- Create: `spa/src/hooks/useExecutionHistory.test.ts`
- Create: `spa/src/lib/nex/terminal-session-ids.ts`
- Create: `spa/src/components/settings/WorkerExitedTab.tsx`
- Create: `spa/src/components/settings/WorkerExitedTab.test.tsx`
- Modify: `spa/src/lib/register-modules/index.tsx` (register `exited`, order 20, host-scoped)
- Modify: locales

**Interfaces:**
- Consumes: `listAllExecutions` (Task 12), `isLiveRow` / `entityKeyOf` (Task 13), `openWorkerTab`, `WorkerEndedPane` (via the opened tab).
- Produces:

```ts
// exited-entities.ts
/** Entities in no live state (spec §4.2: any live stint means the entity IS a worker — it is listed live, never as exited), each represented by its latest stint, newest updated_at first. */
export function exitedEntities(items: readonly ExecutionSummary[]): ExecutionSummary[]
export function matchesExitedQuery(row: ExecutionSummary, q: string): boolean // case-insensitive over workerLabel(row), cwd and session id
// useExecutionHistory.ts
export function useExecutionHistory(hostId: string): { items: ExecutionSummary[]; phase: 'loading' | 'ready' | 'error'; error: string | null; truncated: boolean; refetch: () => void }
// refetches on mount, on hostId change, and whenever useExecutionListStore.byHost[hostId].refreshRevision changes
// terminal-session-ids.ts
/** Session ids the SPA knows are running in a terminal on hostId: tmux-session panes, not terminated, rebuild.agent.sessionId set and !agentExited. */
export function liveTerminalSessionIds(hostId: string): Set<string>
```

- `WorkerExitedTab({ hostId })`:
  - a search input (`worker-exited-search`), then rows (`worker-exited-row`);
  - each row shows the label (`workerLabel(row)` from Task 18), the cwd basename and the relative age;
  - a row whose session id is in `liveTerminalSessionIds` gets the badge `worker-exited-in-terminal` ("在終端機中") and **no** rebuild button;
  - otherwise the row has a `worker-exited-rebuild` button that opens `openWorkerTab({ kind: 'execution', executionId, host: hostId })`. The tab shows the exited screen, whose choices do the rest.
  - plus empty, loading and error states.
- Copy (zh-TW): `settings.worker.exited.search` 搜尋已退出的對話, `settings.worker.exited.empty` 沒有已退出的對話, `settings.worker.exited.in_terminal` 在終端機中, `settings.worker.exited.rebuild` 重建…, `settings.worker.exited.truncated` 清單太長，只顯示前 {{n}} 筆.

- [ ] **Step 1: Write the failing tests**

```ts
// exited-entities.test.ts
it('lists entities with no live stint, latest stint each, newest first', () => {
  const rows = [
    r({ id: 'a1', session_id: 'A', state: 'terminated', archived: true, created_at: 1, updated_at: 10 }),
    r({ id: 'a2', session_id: 'A', state: 'terminated', archived: true, created_at: 2, updated_at: 20 }),
    r({ id: 'b1', session_id: 'B', state: 'terminated', archived: true, created_at: 3, updated_at: 30 }),
    r({ id: 'b2', session_id: 'B', state: 'idle', created_at: 4, updated_at: 40 }), // B is live → not exited
    r({ id: 'c1', session_id: 'C', state: 'terminated', created_at: 5, updated_at: 50 }), // terminated, unarchived = exited (D16)
  ]
  expect(exitedEntities(rows).map((x) => x.id)).toEqual(['c1', 'a2'])
})
it('an entity with an older live stint and a newer exited one is live, not exited (§4.2)', () => {
  const rows = [
    r({ id: 'o', session_id: 'S', state: 'idle', created_at: 1, updated_at: 1 }),
    r({ id: 'n', session_id: 'S', state: 'terminated', archived: true, created_at: 2, updated_at: 2 }),
  ]
  expect(exitedEntities(rows)).toEqual([])
})
it('search matches title, brief, cwd and session id', () => {
  const row = r({ id: 'x', session_id: 'abc-123', cwd: '/Users/w/proj', brief: '修 bug\n細節' })
  expect(matchesExitedQuery(row, 'PROJ')).toBe(true)
  expect(matchesExitedQuery(row, 'abc')).toBe(true)
  expect(matchesExitedQuery(row, '修')).toBe(true)
  expect(matchesExitedQuery(row, '細節')).toBe(false) // first line only
})
```

`useExecutionHistory.test.ts`: calls `listAllExecutions(host, { includeArchived: true }, …)`; refetches when `refreshRevision` bumps; a stale response (host changed) is dropped.

`WorkerExitedTab.test.tsx`:
- an exited row whose session id is in a live terminal pane (seed `useTabStore` with a tmux-session pane carrying `rebuild.agent.sessionId`) shows the badge and no rebuild button;
- another row's rebuild opens a worker tab with the right content (mock `openWorkerTab`);
- typing in search filters the rows.

- [ ] **Step 2: Run them to verify they fail**

Run: `npx vitest run src/lib/nex/exited-entities.test.ts src/hooks/useExecutionHistory.test.ts src/components/settings/WorkerExitedTab.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement** as described. `useExecutionHistory` keeps its own state: it does **not** write into the shared list store, which stays live-only (`include_archived=false`).

- [ ] **Step 4: Run the tests**

Run: `npx vitest run && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint`
Expected: full suite PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/nex/exited-entities.ts spa/src/lib/nex/exited-entities.test.ts spa/src/hooks/useExecutionHistory.ts spa/src/hooks/useExecutionHistory.test.ts spa/src/lib/nex/terminal-session-ids.ts spa/src/components/settings/WorkerExitedTab.tsx spa/src/components/settings/WorkerExitedTab.test.tsx spa/src/lib/register-modules/index.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): Settings → Worker → Exited — searchable, rebuild from here

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

**PR P2-2:** `feat(spa): conversation entity P2-2 — exited conversations`. Then codex R1 + R2.

---

## P3 / P4 — outline only (plan v2 follows once the coordinator completes spec §8 / §10, D15)

- **P3a — Nexen v0.17** (nexen repo, its own spec / plan / codex flow, merge + tag):
  - `delegate` with `resume_session_id`, no brief and no attachments creates an `idle` row with **no turn**. It needs `store.CreateIdle`, sets `session_id` from the resume id at creation, and records the prelude boundary at creation. Capability: `delegate.idle_resume: true`.
  - A transcript page endpoint bounded by "now" (`prelude.LastLineEnd`) instead of `prelude_end`, with the same item kinds and caps.
  - A `session_id` filter on `GET /v1/executions` and `store.ListOptions`.
- **P3b — Purdex:**
  - pin v0.17;
  - handoff and `worker-rebuild` delegate without a brief when the capability is present (placeholder otherwise);
  - `liveWorkersFor` switches to the session filter;
  - transcript-first rendering per §10, with Nexen supplements by `tool_use_id` and the user-prompt line, across all stints.
- **P4 — Purdex:** the daemon scans transcript metadata (`ai-title`, cwd, last activity) per host; a Dormant tab (registered on the Task 25 registry) and search.

## Self-review record

**Spec coverage.**

| Spec item | Task(s) |
|---|---|
| E1 / E2 (one entity, one state) | 4, 7, 11, 13 |
| E3 (terminal not in worker lists) | 13: only executions are listed |
| E4 (one exit) | 5, 6, 15–17 |
| E5 (closing a tab is not exiting) | unchanged; no task closes or exits on tab close |
| E6 (rendering) | P3b |
| Q1 | 3, 11, 18 |
| Q2 | 10, 21, 22 |
| Q3 | 24–26 |
| Q4 | 20, 21, 23 |
| Q5 | 13 |
| §4.3 owner lock | 7–10 |
| §5 exit table | 5 |
| §6 | 21, 22 |
| §7 | 19–23 |
| §8 | placeholder brief in 19; P3a / P3b |
| §9 | 12–14, 24–26 |
| D1 | 4 |
| D2 | 7–11 |
| D3 | 11 |
| D4 | 5–6 |
| D5 | 8–9 |
| D6 | 8 |
| D7 | 10, 22 |
| D8 | 14 |
| D9 | 12, 13 |
| D10 | 26 |
| D11 | 19 |
| D12 | 21, 23 |
| D13 | 1 |
| D14 | PR map |
| D15 | this section |
| D16 | 5 |

**Type consistency, checked across tasks:** `isLiveExecution` / `isLiveRow`, `entityKeyOf`, `sidLockKey`, `takeToTerminalLockKey`, `control` / `ctlPtr`, `exitOutcome.Exited()`, `TerminalSessions.LiveBySessionID` / `SubscribeSessionStart`, `SessionStartEvent`, `nexExitWorker`, `exitWorker`, `nexWorkerRebuild`, `rebuildAsWorker`, `workerEndedKind`, `RebuildMode`, `registerWorkerSettingsTab`, `renewControl`, `workerLabel`.

## Plan review record (codex `task-muvrd2bb-vampfx`, gpt-5.6-sol, with the spec)

| # | Finding (severity, confidence) | Disposition |
|---|---|---|
| 1 | `exitWorker` skipped the archive when a running row's terminate failed, against D4 (critical, 0.99) | **Fixed** (Task 5): the archive is always attempted. A refusal while running returns the terminate error; a turn that ended meanwhile archives. New tests. |
| 2 | The send fence could lapse: a borrowed or own lease was never renewed across create + resume (critical, 0.97) | **Fixed**: `RenewLease` is added to the seam (Task 4) and `renewControl` to Task 5. Tasks 8 and 9 renew to a full TTL right before the resume (the resume waits ≤ 15 s against a 120 s TTL). An expired or changed lease is re-taken once. New tests assert renew-before-keys. |
| 3 | The SessionStart publish did not check the lifecycle (important, 0.98) | **Fixed** (Task 3): an explicit `LifecycleSessionStart` guard, plus a test with a non-SessionStart payload that carries `source:"resume"`. Today the provenance grant already implies SessionStart (`frame_ops.go:1116`); the guard pins it. |
| 4 | Q1 acts on a truncated worker scan (important, 0.96) | **Kept, documented, tested** (Task 11). The owner check fails closed because it gates a transfer that has not happened. Q1 runs after the terminal already took S, so exiting the workers found is strictly better than exiting none. The cap is 10 000 rows. |
| 5 | Take-to-terminal and take-back answered 200 even when the worker failed to exit, untested (important, 0.94) | **Fixed**: 200 stays, because the terminal does run S and the SPA must swap the pane. The response carries `exited:false` and `exit_error`, and the SPA shows a persistent notice (Task 16). New tests in Tasks 8 and 9. |
| 6 | Task 10's cleanup used `r.Context()` (important, 0.92) | **Rejected with evidence**: every engine call wraps the context in `detachedContext`, which is `context.WithTimeout(context.WithoutCancel(parent), d)` (`internal/module/nex/handoff.go:42-44`), so a disconnect cannot cancel it. A comment was added in Task 10. |
| 7 | Exited was defined as "no live stint" rather than "latest stint exited" (important, 0.95) | **Rejected with evidence**: spec §4.2 makes an entity with any live execution a worker, and the live list shows it. Listing it as exited too would show one entity in two states (E2). The D10 wording in the spec was clarified, and a Task 26 test pins the "older live + newer exited" case. |
| 8 | `session_title` is `{text, source}`, not a string (important, 0.99) | **Fixed**: `workerLabel(row)` in Task 18, reused by Task 26. Tests use the object shape. |
| 9 | Task 19 lacked a replace-lock refusal test (important, 0.91) | **Fixed**: the replaced row's `exec:` lock held → 409, with no exit and no delegate. Every creator of a worker for S that goes through Purdex holds `sid:<S>`; that race is covered by the sid-lock test. |
| 10 | The Task 8 / Task 9 boundary left take-back's interim behaviour unspecified (minor, 0.98) | **Fixed**: Task 8 specifies the interim take-back call site, which keeps today's behaviour. The existing take-back tests must pass unchanged except two named lease-path assertions. |

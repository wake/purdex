# Interface language U1-6 — conversation API — plan

Spec: `docs/specs/2026-10-08-interface-u1-spec.md` **§8.2 (U1-6 detailed contract)**, §8.1 (the model and normalizer it serves), §8 (later phases it must not take over: U1-5 mod source, U1-7 usage, U1-8 pane → key, U1-9 writes). Built on `internal/convmodel` / `internal/convmodel/ccnorm` (U1-4, merged).

Format as the U1-2 / U1-3 plans: contracts, rules, named tests and mutation gates; implementers write the code (TDD: each named test is written red first). Line numbers are as of `c8fd8f63`; re-check before editing.

Three PRs, each ≤ 800 lines diff / ≤ 20 files:

| PR | Content | Depends on | Est. lines |
|---|---|---|---|
| **U1-6a** | `internal/convfeed`: the normalizer cache and file follower — resolve, feed to EOF, reset on a replaced file, window / changes-since / size cap, LRU eviction (pure Go, injected FS and resolver) | — | ~650 |
| **U1-6b** | module `internal/module/conversation`: HTTP snapshot / increments / subagents, the path resolver over the agent module + conversation index + bounded lookup, `conversations.v1` | a | ~600 |
| **U1-6c** | `/ws/conversations/claude/{session_id}`: snapshot or catch-up, live changes, state, approvals (snapshot + ops), per-connection `seq`, strict | b | ~650 |

a → b → c. Each PR is safe to deploy on its own: a has no route; b adds read-only routes; c adds one WS route.

Common rules:
- Go: `make lint`; `go test` for touched packages; `go test -race` only for the touched package, one at a time (`internal/convfeed`, `internal/module/conversation`).
- **Measure before shipping** (`feedback_measure_work_inside_lock`): the cache holds a mutex per conversation while it reads and feeds; the PR states the measured time to feed a real 20 MB transcript from zero and an incremental feed of one row, and the snapshot's encode time for 200 turns — and nothing slow runs under a module-wide lock.
- No tmux in tests. Real-file tests use `t.TempDir()` transcripts built from the U1-4 golden fixtures (`testdata/conversation/v1/cc-transcript/*/input.jsonl`).
- Each task one commit.

---

## U1-6a — `internal/convfeed`

Imports: the standard library, `internal/convmodel`, `internal/convmodel/ccnorm`, `internal/transcripttail` (complete lines after an offset). No module, no HTTP (an import-boundary test like U1-4's).

```go
type Source struct { Path string; Live bool } // what the resolver says about a session now
type Resolver interface { Resolve(ctx context.Context, sessionID string) (Source, error) } // ErrNotFound
type FS interface { Open(path string) (File, error) } // File: io.ReaderAt + Stat (size, inode/dev identity)

type Cache struct { /* LRU of *Entry, max 16, idle 10 min */ }
func NewCache(r Resolver, fs FS, clock func() time.Time) *Cache
func (c *Cache) Acquire(ctx context.Context, sessionID string) (*Entry, release func(), error) // pins the entry; ErrBusy when all 16 are pinned
type Entry struct { /* mu; normalizer; transcriptID; offset; identity */ }
func (e *Entry) Refresh(ctx context.Context) (changes []ccnorm.Change, reset bool, err error) // stat → reset if shrank / replaced → read complete lines from Next() to EOF in ≤ 2 MiB chunks → Feed each → SetLive(src.Live)
func (e *Entry) Window(turns, before int, maxBytes int) (convmodel.Conversation, WindowInfo)   // last N turns with Index < before, trimmed to maxBytes (oldest dropped, never below one turn)
func (e *Entry) ChangesSince(offset int64) []TurnChange                                          // turns whose own or items' Updated > offset; items in turn order
func (e *Entry) Cursor() string; func ParseCursor(string) (transcriptID string, offset int64, error)
```

Rules (spec §8.2): `transcript_id` = the file's basename + identity generation (a replaced file → new id); stale cursor = other id or offset > size; a line the normalizer reports `ErrGap` for (offset jump) → reset the entry (new normalizer, re-read from 0) — never feed past a gap; oversize lines are the normalizer's (`Stats.Skipped`); every read is bounded (2 MiB chunks, a context check between chunks). `Window` deep-copies via `Conversation()` and slices turns; `WindowInfo{FirstIndex, LastIndex, TotalTurns, HasMoreBefore}`.

Tests: `feeds a fixture from zero and matches its expected.json` (every fixture), `incremental feed after an append yields only the new row's changes`, `ChangesSince returns turns with changed items, items in turn order`, `a late tool result updates an older step and ChangesSince carries that turn`, `truncated file → reset, new transcript id`, `replaced file (same size, new inode) → reset`, `ErrGap → reset and re-read`, `Window last N`, `Window before=I`, `Window trims to maxBytes but keeps one turn`, `stale cursor detected (id, offset > size)`, `LRU evicts the oldest idle, never a pinned one`, `ErrBusy when all pinned`, `idle eviction after 10 min (fake clock)`, `Refresh honours ctx between chunks`.
Mutation gates: feed past a gap → "ErrGap → reset" red; slice turns without the byte cap → "trims to maxBytes" red; evict pinned → "never a pinned one" red; compare by size only → "replaced file" red.

---

## U1-6b — HTTP

Files: new `internal/module/conversation/{module.go,http.go,resolve.go}` (+ tests), `internal/module/agent` (export `LiveSession(sessionID) (transcriptPath, status string, ok bool)` — the live root frame with that session id; backed by an indexed store query, measured), `internal/conversations` (a lookup by session id over the index, if not already exported), `internal/core/info_handler.go` (`conversations.v1`), the module wiring (`cmd/pdx` / where modules are assembled).

- Routes (Go 1.22 patterns): `GET /api/conversations/{provider}/{session_id}` (snapshot or `after=` increments) and `GET /api/conversations/{provider}/{session_id}/subagents/{agent_id}`; token auth as every `/api/` route.
- Resolver (spec §8.2 order): `agent.LiveSession` → index row → bounded `<sid>.jsonl` lookup under the projects root (reuse the containment checks of `transcript_path.go`, exported as a helper or duplicated with the same tests); containment failure → `not_found`.
- Snapshot: `Acquire` → `Refresh` → `Window(turns, before, 4 MiB)` → fill `key.host_id` (`core.Cfg.HostID`), `backend`, `status` (`LiveSession` status, else `ended`) → JSON `{conversation, window, cursor, live}`. Increments: `ParseCursor` → stale → `{reset: true, …snapshot}`; else `{changes, cursor, live}`.
- Subagents: path `<dir of the transcript>/<session_id>/subagents/agent-<agent_id>.jsonl`, `agent_id` validated against `^[A-Za-z0-9_-]{1,64}$` before joining; `NormalizeSubagent`; `partial` per spec.
- Errors per spec §8.2; every validation before any file access.

Tests (`httptest`, temp projects root, fake agent / index): `snapshot of a fixture conversation: window, cursor, live, host_id`, `turns default 20, 0 / 201 / junk → 400`, `before pages older turns`, `after=cursor returns only changes`, `stale cursor → reset with a snapshot`, `turns and after together → 400`, `unknown provider → 404 provider_unsupported`, `bad session id → 400 before touching the disk`, `resolver order: live pane wins over the index; index over lookup`, `symlink escaping the root → 404 not_found`, `status from the live pane, ended without one`, `subagent ok / partial / missing / bad agent_id`, `capability listed`, `4 MiB cap on a synthetic huge conversation`.
Mutation gates: resolve before validating → "bad session id … before touching the disk" red (fake FS counts opens); skip containment → symlink test red; index before live pane → resolver-order red.

---

## U1-6c — WebSocket

Files: `internal/module/conversation/ws.go` (+ tests), the approvals source (below), module wiring.

- `GET /ws/conversations/{provider}/{session_id}?after=&turns=` — upgrade like `/ws/host-events` (`core/events.go` `HandleHostEvents`: origin check, ping / pong deadlines); ticket auth comes from the middleware.
- Per connection: one goroutine owns the follower loop: `Acquire` (pinned for the connection's life) → first frame (snapshot, or catch-up changes from `after`) → every 500 ms `Refresh` → `conversation.changes` from that refresh's change list (grouped by turn, items in turn order, turn headers included) / `conversation.reset` + snapshot / `conversation.state` when `live`, `status` or `backend` changed → write with a bounded send buffer (64 frames); a full buffer closes the connection (strict).
- Frames `{type, seq, value}`, `seq` from 1, contiguous per connection, assigned by the single writer.
- **Approvals source** — a narrow interface the team module implements (agreed with purdex-1f before this PR; the team module is its line's): `type ApprovalFeed interface { OpenFor(sessionID string) []team.Approval; Subscribe(fn func(op string, a team.Approval)) (cancel func()) }`. On connect: `approvals.snapshot` with `OpenFor(session_id)`; then `approval` `{op, approval}` for events whose `origin.session_id` matches. The callback only enqueues (never blocks the team module's broadcast).
- Ordering: the approvals snapshot is taken after `Subscribe` is armed, and an `opened` op for an approval already in the snapshot is dropped (dedupe by id + state), so nothing is lost or doubled across the connect race.

Tests: `no after → snapshot first, seq 1`, `after → changes first`, `append to the file → one conversation.changes with that turn`, `replaced file → reset + snapshot`, `live flips when the pane goes → conversation.state`, `approvals snapshot lists only this session's open approvals`, `opened / closed ops for this session arrive, others do not`, `connect race: an approval opened during connect appears exactly once`, `slow reader → connection closed (strict), no goroutine leak` (`goleak` or a counter), `seq contiguous across frame types`, `close releases the pin`.
Mutation gates: subscribe after the snapshot → "connect race" red; per-type seq → "seq contiguous" red; unbounded buffer → "slow reader" red; forget release → "close releases the pin" red.

---

## Acceptance (after U1-6c merges; the coordinator deploys)

Script `accept-u1-6` (member scratchpad), tmux rules as U1-3 (`acc-u16-<n>` on the real server, `kill-session -t` by name only), a Sonnet `claude` in default permission mode with a cwd inside a throw-away git repo:
1. `GET /api/info` lists `conversations.v1`.
2. Run two turns (one with a Bash permission ask, one that starts a subagent); snapshot shows both turns, the step statuses, `live: true`, `status` matching the light; `before` pages back; `after` returns only the second turn's rows when taken between turns.
3. The subagent endpoint returns its items (confirms the on-disk layout `<sid>/subagents/agent-<id>.jsonl` with this CC version).
4. WS: connect, run a turn → `conversation.changes` within ~1 s of each row; the permission ask → `approval` opened, approve → closed; disconnect mid-turn, reconnect with the last cursor → catch-up has exactly the missed changes.
5. `/clear` → the old conversation's WS goes `live: false` / `ended`; the new session id has its own conversation.
6. Exit → `ended`. Results into the PR and the kickoff memory; then the lead sends iOS its **second start notice** (U1-6 deployed on mlab).

## Coordination

- The approvals interface (U1-6c) is implemented in the team module (purdex-1f's line): signature to 1f before the PR.
- `agent.LiveSession` touches the agent module (this line's own lights code lives there); no lock is taken that the emit slot holds.
- iOS: the WS and HTTP shapes above are what U2 consumes; send iOS the §8.2 link when this plan merges so it can review early.

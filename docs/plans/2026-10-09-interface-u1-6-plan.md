# Interface language U1-6 — conversation API — plan

Spec: `docs/specs/2026-10-08-interface-u1-spec.md` **§8.2 (U1-6 detailed contract)**, §8.1 (the model and normalizer it serves), §8 (later phases it must not take over: U1-5 mod source, U1-7 usage, U1-8 pane → key, U1-9 writes). Built on `internal/convmodel` / `internal/convmodel/ccnorm` (U1-4, merged).

Format as the U1-2 / U1-3 plans: contracts, rules, named tests and mutation gates; implementers write the code (TDD: each named test is written red first). Line numbers are as of `c8fd8f63`; re-check before editing.

Four PRs, each ≤ 800 lines diff / ≤ 20 files:

| PR | Content | Depends on | Est. lines |
|---|---|---|---|
| **U1-6a** | `ccnorm.Skip`; `internal/convfeed` entry: open-file follower (bounded line reader that skips oversize lines, growth / shrink / replace / rewrite detection, epochs), revisions per turn / item, `Window` with the 4 MiB body cap and `omitted_items`, `ChangesSince(rev)` | — | ~700 |
| **U1-6b** | `internal/convfeed` cache (one entry per conversation, pins, LRU 16, idle 10 min, `ErrBusy`) and the resolver (live pane → index → bounded lookup, descriptor-relative opens) | a | ~650 |
| **U1-6c** | module `internal/module/conversation`: HTTP snapshot / increments / subagents, header, errors, `conversations.v1`, the agent module's `LiveSession` | b | ~600 |
| **U1-6d** | `/ws/conversations/claude/{session_id}` + the team module's approvals feed (atomic snapshot + subscribe, responder registration); `scripts/acceptance/u1-6.sh` | c (+ 1f agreement) | ~700 |

a → b → c → d. Each PR is safe to deploy on its own: a and b have no route; c adds read-only routes; d adds one WS route and the team-module interface.

Common rules:
- Go: `make lint`; `go test` for touched packages; `go test -race` only for the touched package, one at a time.
- **Measure before shipping** (`feedback_measure_work_inside_lock`): an entry's mutex is held while it reads and feeds; each PR from a on states the measured time to feed a real 20 MB transcript from zero, one appended row, and to encode a 200-turn snapshot — and nothing slow runs under the cache-wide lock (the cache lock only maps keys to entries).
- No tmux in unit tests. Real-file tests use `t.TempDir()` transcripts built from the U1-4 golden fixtures (`testdata/conversation/v1/cc-transcript/*/input.jsonl`).
- Each task one commit.

---

## U1-6a — the entry (follower, revisions, window)

Files: `internal/convmodel/ccnorm/normalizer.go` (+ test: `Skip`), new `internal/convfeed/{entry.go,reader.go,window.go}` (+ tests), an import-boundary test (stdlib + `convmodel` + `ccnorm` only).

- `ccnorm.(*Normalizer).Skip(offset, length int64) error` — advances `Next()` past a line the caller did not read (contiguity checked like `Feed`), counts `Stats.Skipped["line:oversize"]`.
- `reader.go`: from an open `File` (`io.ReaderAt` + `Stat() (size, identity)`), read complete lines from `Next()` to the size seen at the start of the refresh, in ≤ 2 MiB chunks, a context check between chunks; a line longer than 8 MiB is never buffered whole — its bytes are counted to the next `\n` and passed to `Skip`. An unterminated tail (writer mid-line) waits for the next refresh.
- `Entry` (one normalizer instance; `epoch` random per instance; `mu` held for a refresh and for reads of the model):
  - `Refresh(ctx, src Source) (RefreshResult{Changed, Reset bool}, error)` — `src` is the resolver's current answer (an open file + identity + live). **New epoch** when: identity differs from the entry's (replaced), size < the last fed offset (shrank), the 64 bytes before the last fed offset differ from the remembered fingerprint (same-inode rewrite), or `Feed` returned `ErrGap`; a new epoch re-reads from 0. Then read / feed, then `SetLive(src.Live)`. Every non-empty change list bumps `rev`; each changed turn / item gets `changedRev[id] = rev` (from the change list, so `SetLive`'s `Offset = -1` changes count). The header fields (title, usage) are compared before / after and bump `rev` too.
  - `Window(turns, before int, budget func([]byte) bool) (WindowResult)` — the last `turns` turns with `Index < before`; the caller's `budget` reports whether the encoded body still fits: drop older turns down to one, then that turn's oldest items (`omitted_items`). (Encoding happens in the module, so the cap is on the **final** body — the window works on per-turn encodings and a measured envelope.)
  - `ChangesSince(rev uint64) []TurnChange` — turns whose header or any item has `changedRev > rev`, items in turn order with `changedRev > rev`.
  - `Cursor()` = `"<epoch>:<rev>"`; `ParseCursor` → `(epoch, rev, error)`; a foreign epoch is stale.

Tests: `fixtures from zero match expected.json`; `append one row → only its changes, rev+1`; `late tool result → that older turn in ChangesSince`; `SetLive(false) on an unfinished step → turn and step in ChangesSince with no file growth`; `title row → header change bumps rev`; `oversize 9 MiB line skipped without buffering it (alloc check), later rows fed`; `unterminated tail waits`; `shrink → new epoch`; `replaced (identity) → new epoch`; `same-inode truncate-and-rewrite to a larger size → new epoch (fingerprint)`; `ErrGap → new epoch, re-read`; `Window last N / before=I`; `budget drops older turns, then oldest items with omitted_items`; `stale cursor (foreign epoch)`; `ctx cancelled between chunks`.
Mutation gates: changes by `Position.Updated` instead of revisions → the `SetLive` test red; read the whole oversize line → alloc check red; detect only shrink / identity → the rewrite test red; budget on the window only (not items) → `omitted_items` test red.

---

## U1-6b — cache and resolver

Files: new `internal/convfeed/{cache.go,resolve.go}` (+ tests); the descriptor-relative open helper from `internal/module/agent/transcript_path.go` moved to a small shared package (`internal/transcriptpath`, a pure move proved by the byte-for-byte rule) so both modules use one implementation.

- `Cache.Acquire(ctx, sessionID) (*Entry, release func(), error)`: concurrent callers for one session get the **same** entry (singleflight on creation); pins are reference counts; `release` is idempotent; eviction only of unpinned entries idle ≥ 10 min (fake clock in tests) or, when a 17th is needed, the oldest unpinned; all pinned → `ErrBusy`. The cache-wide lock covers only the map and pins — never a refresh.
- `Resolver.Resolve(ctx, sessionID) (Source, error)`, `Source{File, Identity, Live, Status, Backend}` (spec §8.2 order): (1) `LiveSessions(sessionID)` from an injected interface (the agent module, U1-6c) → confirmed owners → the one seen last → open its transcript path with the descriptor-relative walk; (2) the conversation index row → open, skip on missing / containment failure; (3) bounded lookup (depth-1 slug dirs, ≤ 2000, ≤ 1 s, ctx) → open. Nothing → `ErrNotFound`. An owner-lookup error → continue with (2) / (3) and `Status: "unknown"`.
- Re-resolve: every HTTP request and every 10 s on a WebSocket; the entry's `Refresh` compares identity.

Tests: `same session concurrently → one entry, one normalizer` (`-race`, a barrier); `pins counted, release idempotent`; `pinned never evicted, idle evicted after 10 min`; `17th with 16 pinned → ErrBusy`; `refresh racing an eviction attempt keeps the entry`; `resolver: live pane wins over index, index over lookup`; `index candidate missing or outside the root → lookup continues`; `symlinked intermediate directory swapped between resolve and read → no escape` (the open is descriptor-relative; the test swaps a directory after resolve); `lookup bound: 2001 slug dirs → not_found within the bound`; `owner lookup error → status unknown, content served`.
Mutation gates: create entries without singleflight → "one entry" red; evict pinned → "pinned never evicted" red; open by path string after resolve → swap test red; lookup without a bound → bound test red.

---

## U1-6c — HTTP module

Files: new `internal/module/conversation/{module.go,http.go}` (+ tests), `internal/module/agent` (`LiveSessions(sessionID) ([]Owner, error)` — frames with that session id, each confirmed by the existing `resolveSessionOwnerErr` path; measured), `internal/conversations` (lookup by session id, if not exported), `internal/core/info_handler.go` (`conversations.v1`), the module wiring.

- Routes: `GET /api/conversations/{provider}/{session_id}` (snapshot, or increments with `after=`) and `GET /api/conversations/{provider}/{session_id}/subagents/{agent_id}`; token auth; every validation (provider, session id, turns, before, cursor, agent id with `ccnorm`'s own rule — exported) **before** `Acquire`.
- Snapshot: `Acquire` → `Refresh(resolve())` → `Window(turns, before | around, budget(4 MiB))` → `{conversation, header, window, cursor}` with `key.host_id`. `around=<item_id>`: the entry keeps `itemTurn map[itemID]turnIndex` (filled from the change lists) and centres the window on that turn; unknown → 404 `item_not_found`; `before` + `around` → 400. Increments: stale → `{reset: true, …snapshot}`; else `{changes, header, cursor}`, and a catch-up over 4 MiB → `reset` with a snapshot.
- Subagents: the descriptor-relative open of `<session_id>/subagents/agent-<agent_id>.jsonl` beside the transcript; `NormalizeSubagent` → `{items, partial}`; errors per spec.

Tests (`httptest`, temp projects root, fake agent / index): `snapshot: window, header, cursor, host_id`; `turns default / 0 / 201 / junk`; `before pages`; `around centres the window on the item's turn, clamped at the ends`; `around unknown → 404`; `before+around → 400`; `after → only changes; header always present`; `stale cursor → reset`; `turns+after → 400`; `bad provider / session id / agent id → 4xx with zero file opens` (counting FS); `status: live pane / ended / unknown`; `4 MiB cap measured on the final body` (a synthetic 10 MB turn → `omitted_items`, body ≤ 4 MiB); `subagent ok / partial / missing / 128-char id accepted, 129 rejected`; `busy → 503`; `capability listed`.
Mutation gates: validate after `Acquire` → zero-opens test red; cap on the window before the envelope → final-body test red.

---

## U1-6d — WebSocket and approvals

Files: `internal/module/conversation/ws.go` (+ tests); `internal/module/team` — a narrow `ApprovalFeed` (**agreed with purdex-1f first**: signature below goes to 1f before the PR); module wiring; `scripts/acceptance/u1-6.sh` (manual acceptance script, documented at its top).

- Handler order: validate → `Acquire` (503 `busy` as plain HTTP) → upgrade (a failed upgrade releases) → one writer goroutine per connection (frames `{type, seq, value}`, `seq` from 1 per connection, a 64-frame send queue) → one follower loop (`Refresh` every 500 ms, re-resolve every 10 s) → frames per spec §8.2 → close releases the pin and cancels the feed subscription. Auth is the shared middleware (Bearer header or ticket).
- **Back-pressure** (spec §8.2): the follower keeps `sentRev`; when the queue is under half full and `rev > sentRev` it sends one `conversation.changes` from `ChangesSince(sentRev)` and sets `sentRev = rev`; otherwise it waits for the next tick (changes coalesce); a frame over 4 MiB → `conversation.reset` + snapshot (with the connection's `turns`); the connection closes only on a real overflow.
- `ApprovalFeed` (team module; uses its existing `eventMu` ordering, the same as the host-events `approval.request` snapshot):

```go
type ApprovalFeed interface {
    // SubscribeSession returns the open approvals of sessionID and arms fn for every later opened / closed
    // op of that session, atomically with respect to the module's broadcasts. fn must not block (enqueue only).
    SubscribeSession(sessionID string, fn func(op string, a team.Approval)) (open []team.Approval, cancel func(), err error)
    // HoldResponder marks one more remote responder (terminal-only rows are created while > 0), like a host-events subscriber.
    HoldResponder() (release func())
}
```

- A `SubscribeSession` error closes the connection (no empty snapshot).
- **purdex-1f's conditions (agreed 2026-10-09):** (1) `fn` runs under the team module's `eventMu`: it only enqueues and never calls back into the team module (stated in the interface comment); (2) `cancel` and the `HoldResponder` release are idempotent (repeated calls never drive the count negative) and every disconnect path calls them; (3) filtering is by `Origin.SessionID` only — after a relay the new session has a new id and its own WebSocket; the old id's subscription ends with the old WebSocket, never forwarded across the lineage; (4) `HoldResponder` changes nothing about how terminal approvals behave: it has exactly the effect an App with `/ws/host-events` open has today (terminal-only rows are created and can be answered remotely). The lead tells 1f before the U1-6d PR merges.

Tests: `no after → snapshot first, seq 1`; `after → catch-up first`; `append → one conversation.changes`; `replaced file → reset + snapshot`; `pane goes away → conversation.header live false / ended`; `approvals.snapshot lists only this session`; `ops for this session only`; `approval closed during connect is never shown open` (team module test with the real `eventMu`, a hook between list and arm); `subscribe error → connection closed`; `conversation WS alone makes a terminal-only ask create a row` (responder held), `released on close`; `slow reader gets coalesced changes frames, no disconnect` (a reader paused for 5 s while 200 rows land → ≤ a few frames, every change present); `real overflow → closed, no goroutine leak`; `turns applies to the snapshot after a reset`; `responder count returns to zero after all WebSockets close (incl. upgrade failure, read error, write error, server shutdown)`; `cancel / release idempotent`; `ops of another session id (e.g. the relayed successor) not delivered`; `seq contiguous across frame types`; `all pinned → 503 before upgrade`; `upgrade failure releases`.
Mutation gates: list then subscribe without the module's lock → "closed during connect" red; no `HoldResponder` → "WS alone creates a row" red; acquire after upgrade → "503 before upgrade" red; per-type seq → "seq contiguous" red; enqueue per refresh regardless of the queue → "slow reader gets coalesced frames" red; release not idempotent → "count returns to zero" red.

---

## Acceptance (after U1-6d merges; the coordinator deploys)

`scripts/acceptance/u1-6.sh` (in the repo, run by hand; tmux rules as U1-3: `acc-u16-<n>` sessions on the real server, `kill-session -t` by name only, `unset TMUX` around any test server; tokens read into variables, never printed), a Sonnet `claude` in default permission mode with a cwd inside a throw-away git repo:
1. `GET /api/info` lists `conversations.v1`.
2. Two turns (one Bash permission ask, one that starts a subagent): the snapshot shows both turns, step statuses, `live: true`, `status` matching the light; `before` pages back; `after` between turns returns only the second turn's rows.
3. The subagent endpoint returns its items (confirms `<sid>/subagents/agent-<id>.jsonl` on this CC version).
4. WS: a turn → `conversation.changes` within ~1 s of each row; the permission ask → `approval` opened, approve → closed; with **only** the conversation WS connected (no App), a no-mod session's permission ask still creates the terminal-only row; disconnect mid-turn, reconnect with the last cursor → catch-up has exactly the missed changes.
5. `/clear` → the old conversation's WS goes `live: false` / `ended`; the new session id is its own conversation.
6. Exit → `ended`. Results into the PR and the kickoff memory; then the lead sends iOS its **second start notice** (U1-6 deployed on mlab).

## Coordination

- `ApprovalFeed` lives in the team module (purdex-1f's line): signature to 1f before U1-6d.
- `LiveSessions` is in the agent module (this line's lights code lives there); it takes no lock the emit slot holds.
- The `internal/transcriptpath` move (U1-6b) is a pure move proved byte-for-byte.
- iOS: when this plan merges, send iOS the §8.2 link so it can review the shapes early.

## Plan review fold-in (codex `task-mv032cxq-x50o1w`, 2026-10-09)

| # | Sev / conf | Finding | Disposition |
|---|---|---|---|
| 1 | critical 0.98 | subscribe-then-list lets a closed approval reappear | Accepted: `SubscribeSession` lists and arms under the team module's `eventMu` (spec §8.2, U1-6d) + test |
| 2 | important 0.99 | `Position.Updated` misses `SetLive` changes (`Offset = -1`) | Accepted: per-entry revisions from the change lists; cursor = epoch:rev |
| 3 | important 0.99 | `transcripttail.After` errors on > 8 MiB lines | Accepted: own bounded reader + `ccnorm.Skip` (U1-6a) |
| 4 | important 0.98 | 4 MiB vs an oversized turn; cap not on the final body | Accepted: cap on the final body; drop turns, then oldest items with `omitted_items` |
| 5 | important 0.97 | title / model / effort lost by cursors | Accepted: `header` on every snapshot / increment / frame; header changes bump rev |
| 6 | important 0.99 | 64 vs 128 agent id length | Accepted: `ccnorm`'s own rule, exported |
| 7 | important 0.97 | path-based open after resolve = TOCTOU | Accepted: resolver returns an open file; descriptor-relative walk; swap test |
| 8 | important 0.94 | index candidate missing / outside root undefined | Accepted: skipped, lookup continues |
| 9 | important 0.93 | `LiveSession` correctness | Accepted: frames confirmed by the existing owner resolution; last seen wins; error → status unknown |
| 10 | important 0.91 | bounded lookup unbounded | Accepted: depth 1, ≤ 2000 dirs, ≤ 1 s, ctx |
| 11 | important 0.96 | conversation WS not a responder | Accepted: `HoldResponder` |
| 12 | important 0.95 | `OpenFor` had no error path | Accepted: error closes the connection |
| 13 | important 0.90 | busy after upgrade | Accepted: acquire before upgrade, release on failure |
| 14 | important 0.90 | same-inode truncate-and-rewrite | Accepted: 64-byte fingerprint before the fed offset |
| 15 | important 0.90 | no concurrency tests for the cache | Accepted: singleflight, pins, evict races (U1-6b) |
| 16 | minor 0.94 | acceptance not reproducible | Accepted: `scripts/acceptance/u1-6.sh` in U1-6d |

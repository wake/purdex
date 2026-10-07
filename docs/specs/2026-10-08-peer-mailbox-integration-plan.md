# Peer mailbox integration — Implementation Plan (P1–P5; P6 outlined)

> **Source:** spec `docs/specs/2026-10-08-peer-mailbox-integration-spec.md` (U1–U9, §3–§11). The spec is binding; where this plan and the spec disagree, the spec wins and the plan is fixed.
> **Base:** origin/main `33b4ff91` (alpha.588, Nexen pinned at v0.20.0 by #1874). `file:line` references were read on that commit; re-verify them before starting a PR, other lines move fast.
> **Format:** compact — contracts, rules, tests and mutation gates, no full code blocks. The implementer writes the code test-first from these contracts.
> **Review:** codex plan+spec round 1 (task-muyn9igd-so6zc4, 10 findings: 4 critical, 5 important, 1 minor, all confidence ≥ 0.95) — all applied: C1 truncate-before-validate; C3/C5 late-lineage upgrade; P4 folding over all rows; P4 full pagination + `executions_unavailable`; P3b SPA/CLI callers named; spec §4.2 full error table + typed error; never-interrupt proven on the nex side; P1 per-template mutations; P2 site-wide events stay out of the pane reducer; P3a→P3b bump gate.

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Each PR is TDD, one task per commit.

**Goal:** `pdx msg send` reaches any conversation by one stable pdx-assigned address (`<base>-<ref[1:3]>`), including a sleeping execution, which Nexen's peer mailbox wakes. The deck shows a peer turn as a peer message, not as the user's input.

---

## Global constraints

- **Size.** Each PR ≤ 800 lines of diff **and** ≤ 20 files. P3 is pre-split into P3a/P3b.
- **Tests.**
  - Go: `go test -race ./<pkg>/...`, `gofmt -l cmd internal` (empty), `go vet ./...`.
  - SPA: `cd spa && npx vitest run <path>`, `pnpm run lint`, `npx tsc -p tsconfig.app.json --noEmit`, `pnpm run build`.
  - `go build ./cmd/pdx/` dirties the tracked repo-root `pdx` binary (#1699): `git checkout -- pdx` before committing.
- **Mutation gates are deliverables.** Each PR names its mutations; the implementer applies each one, shows the named test going red, and reverts. Report the result in the PR body.
- **Commits.** English messages ending with the attribution line from the session's system reminder. Parallel subagents in one worktree commit with `git commit --only <files>`.
- **Copy.** SPA strings go in `spa/src/locales/{en,zh-TW}.json` (pinned by `locale-completeness.test.ts`). User-facing words follow the interface vocabulary: 執行體 (execution), 指揮台 (deck); never "worker" in new copy. Program keys keep their current names.
- **Real samples.** Wire shapes come from the recorded Nexen fixtures (nexen PR #161, `docs/contract/fixtures/peer-mailbox/`), copied into `spa/src/lib/nex/__fixtures__/peer-mailbox/` with a README naming the source commit. Never hand-write a `peer_message` payload when a recorded one exists.
- **Deploy tags:** daemon (swap `bin/pdx`, restart), CLI (swap `bin/pdx`), SPA (HMR on the main checkout).

## Review focus (for codex, plan round)

1. §3.2 assignment: is "assign once at first sighting" race-free across concurrent inventory passes (two passes, same new sid)? Is the lineage-inheritance lookup order deterministic?
2. §3.3: does making registry names non-routing break any existing caller (team/relay code, lead notices, `pdx msg whoami`, SPA peers view, `origin_resolver.go`) that composes an address from `Agent.PeerName`?
3. §4.1 folding: a live entry whose sid matches an execution must never also be listed or resolved as a plain cc row.
4. §4.2: the never-interrupt rule must be provable by a test; the error mapping must not leak Nexen internals beyond the `detail`.
5. §6: a bad template must be rejected at `PUT /api/config`, not at the next restart.
6. Plan vs spec drift anywhere.

## PR table

| PR | Content | Depends | Deploy |
|---|---|---|---|
| P1 | `[nex.peer]` config + settings page | — | daemon, SPA |
| P2 | SPA: `peer_message` opens a turn; peer message block; site-wide allowlist; types; fixtures | — | SPA |
| P3a | Virtual names: `peer_names` store, `VirtualName`, assignment, `PeerRecord.Name`, addresses, whoami, frame from-name | — | daemon, CLI |
| P3b | Resolution by virtual name; remote `name`; not-found hint; `CLAUDE.md` | P3a | daemon, CLI |
| P4 | Execution rows + local last hop | P1, P3b | daemon |
| P5 | Cross-host delivery to executions | P4 | daemon (both hosts) |
| P6 | "由 peer 訊息喚醒" collapsed row | P2, spec §8 outcome | SPA (+ Nexen) |

P1, P2, P3a may run in parallel (different files). First user-visible deploy after P4.

---

## Shared contracts

- **C1 `ipeers.VirtualName(base, ref string) (string, bool)`** (new `internal/peers/vname.go`). `ref` must satisfy `IsRef`; suffix = `ref[1:3]`. Order matters (`RoutableName` itself caps the length, so it must not run on the raw base): (1) shape check on the base only — `^[a-z0-9][a-z0-9-]*$`; (2) truncate from the end so `len(base)+3 ≤ 64`, then trim trailing `-`; (3) `RoutableName(base + "-" + suffix)`. Return `false` when step 1 or 3 fails or the truncated base is empty. Table test includes a 100-char valid base (truncated, accepted).
- **C2 `ipeers.NormalizeBase(s string) string`**: lowercase; every rune outside `[a-z0-9-]` → `-`; collapse runs of `-`; trim leading/trailing `-`. Used only for the execution cwd-basename fallback.
- **C3 `store.PeerNameStore`** (new `internal/store/peer_name.go`, DDL in `internal/store/meta.go` next to `peer_messages`):
  - `Assign(ctx, sessionID, ref, name, source string, nowMs int64) (stored Entry, err error)` — `INSERT … ON CONFLICT(session_id) DO NOTHING` then `SELECT`, in one transaction; returns the row that won (first writer wins, concurrent callers converge on the same name).
  - `AdoptLineage(ctx, sessionID, name string, nowMs int64) (stored Entry, err error)` — `UPDATE … SET name=?, source='lineage' WHERE session_id=? AND source <> 'lineage'` then `SELECT`, one transaction. The only path that changes a name (spec §3.2 "lineage 晚到時升級一次").
  - `Lookup(ctx, sessionIDs []string) (map[string]Entry, error)`; `ByRefs(ctx, refs []string) (map[string]string, error)`. `Entry = {Name, Source}`.
  - session ids are stored lowercase; `source ∈ {lineage, registry, conversation_name, dir}`.
- **C4 `PeerRecord.Name string \`json:"name,omitempty"\``** — the virtual name, empty when none. `Address` = `alias + "/" + Name` when `Name != ""`, else `alias + "/" + Ref`. The registry name stays in `Agent.PeerName` for display only.
- **C5 Namer** (new `internal/module/peers/vnames.go`): `resolveNames(ctx, cands []nameCandidate) map[sid]string`, where a candidate is `{sid, ref, registryName, previousRefs, dirBase}`. Per candidate:
  1. Lineage name: walk `previousRefs` newest-first (the order `internal/module/team/relay_store_lineage.go:17-63` already returns), first hit in `ByRefs` = `ln`.
  2. Existing row (`Lookup`): if present and `source == lineage` → use it. If present, `source != lineage` and `ln != ""` → `AdoptLineage(ln)`. Otherwise use it as is.
  3. No row: `ln` if any (`source=lineage`); else `VirtualName(registryName)` (`registry`); else `conversation_names` (`ConversationNameReader`, `internal/module/nex/conversations_http.go:116`) → `VirtualName` (`conversation_name`); else executions only `VirtualName(NormalizeBase(dirBase))` (`dir`) → `Assign`.
  - A store error for a candidate → no name for that row this pass (address falls back to the ref form), logged once per pass; never an unpersisted name.
  - Race argument (pin in a comment and a test): two concurrent passes converge through `DO NOTHING`; a pass that saw lineage either inserts `lineage` first or upgrades the loser's fallback row via `AdoptLineage`, so lineage always wins eventually and a `lineage` row is never overwritten.
- **C6 Execution rows** (P4): `RowKind = "execution"`; new fields `ExecutionID string \`json:"execution_id,omitempty"\``, `ExecState string \`json:"exec_state,omitempty"\`` (idle|running|queued); `Agent = &AgentInfo{Type:"cc", SessionID: sid, PID: <live pid or 0>}`; `Deliverable`/`Reason` per spec §4.1 (`mailbox_disabled`).
- **C7 `nex.peers` service** (P4), registered by the nex module in `Init` under registry key `"nex.peers"`:
  ```
  type ExecPeers interface {
    Rows(ctx) ([]ExecPeerRow, error)      // non-terminal, non-archived, with a session id
    MailboxEnabled() bool                 // cfg.Peer.Enabled as assembled
    Send(ctx, executionID string, m PeerSend) (PeerSendResult, error)
  }
  ```
  The interface lives in a small shared package (`internal/peers/execpeers`) so neither module imports the other, and it has **no** interrupt-capable method: the peers module cannot interrupt by construction. The nex-side implementation takes the module's full `nexService` (`internal/module/nex/engine_iface.go:17`, which gains `PeerMessage`, keeping the compile-time assertion), so the never-interrupt rule is tested where an `Interrupt` call is actually possible: a recording fake `nexService` asserts the call log of every `Send` path is exactly `[PeerMessage]`.
  `Send` returns a typed error carrying the spec §4.2 classification (`Kind`: disabled | queue_full | invalid | gone | credential | turn_failed | internal; `Detail`; `TurnID` when Nexen created a turn) — the mapping from Nexen sentinels (`errors.Is` against `execution.ErrPeerDisabled`, `ErrPeerInvalid`, `ErrPeerQueueFull`, `store.ErrInvalidMessageText`, the terminal/archived/not-found errors, credential errors, `ErrTurnFailedToLaunch`, `ErrTurnStalled`; read `execution/peer.go:56-63,102-133,167-226` and `execution/send.go:264-329` for the exact values and how the turn id is carried) lives in the nex module next to the call.
  Rows are listed by walking every page of `nexStore.List` with the repeated-cursor guard of `internal/module/nex/conversations_http.go:395-419`; any page error → `Rows` returns an error (no partial list).
- **C8 Wire additions** (`internal/peers/wire.go`): `ResultQueued = "queued"`; error codes `queue_full` (429) and `mailbox_error` (502); reuse `not_deliverable` (409), `bad_request` (400), `target_gone` (409), `self_target` (400), `not_ready` (503). Envelope flag `executions_unavailable` (P4). `WireTo.ExecutionID` and `WireFrom.Name` (P5), both `omitempty`. The Nexen turn id goes into the audit row's existing `native_msg_id`.

---

## PR P1 — `[nex.peer]` config + settings page

**Files:** `internal/config/nex.go` (+`nex_test.go`), `internal/module/nex/build_config.go` (+test), `internal/module/nex/status.go` (+test), `internal/core/config_handler_test.go`, `internal/core/info_handler_test.go`, `spa/src/lib/host-api.ts`, `spa/src/components/hosts/nex/NexConfigForm.tsx` (+test), `spa/src/components/hosts/nex/nex-config-diff.ts`, locales.

1. **Config shape.** `NexPeerConfig{Enabled bool; MaxPending int; WakeTemplate, ReplyLine string}` with toml/json keys `enabled`, `max_pending`, `wake_template`, `reply_line`; `NexConfig.Peer` under key `peer`. `DefaultNexConfig().Peer.Enabled = true`. Tests: section absent → default enabled; explicit `enabled = false` honoured; TOML round trip (`nex_test.go:263-293` pattern).
2. **Validate** (`nex.go:74`): runs whether or not `[nex].enabled`: `max_pending < 0` → `nex.peer.max_pending: must not be negative`; templates via Nexen's own parsers (`nexconfig.ParseWakeTemplate` / `ParseReplyLine`, or `PeerConfig.Build` if those are unexported — verify) → `nex.peer.wake_template: <nexen message>` / `nex.peer.reply_line: …`. Empty template = Nexen default, valid. Check the import boundary for `internal/config` (it already imports `nexen/sandbox`).
3. **Equal** (`nex.go:199`) compares `Peer` (scalar struct, `==`). Test in `info_handler_test.go:290-320` style: changing only `peer.enabled` sets `restart_required`.
4. **buildOptions** (`build_config.go:80`) maps `Peer` → `cfg.Peer`; extend `TestBuildOptionsFullMapping` (`build_config_test.go:23`): `max_pending = 0` comes back as Nexen's default after `cfg.Validate()`.
5. **Status** (`status.go:21-44`): effective map gains `peer_enabled`, `peer_max_pending`.
6. **PUT** (`config_handler.go:57-66`): a bad template → 400 naming `nex.peer.wake_template`; table test in `config_handler_test.go:425-450`.
7. **SPA.** `NexConfig.peer` type; `emptyNexConfig` gets the default (enabled true, 0, "", ""); `trimForSubmit` (`NexConfigForm.tsx:49-69`) sends all four peer keys, templates passed through untouched; the form gets a toggle「peer 信箱」and a number「排隊上限」(0 = 預設); the error regex (`:37`) already maps `nex.peer.x`. Update the key-set test (`NexConfigForm.test.tsx:71-80`) to 8 keys; add a test that two non-empty templates loaded by GET are PUT back byte-identical.

**Mutations:** drop `Peer` from `Equal` → restart test red; omit `peer` in `trimForSubmit` → round-trip test red; omit only `wake_template` → round-trip test red; omit only `reply_line` → round-trip test red; default `Enabled=false` → default test red; skip template validation in `Validate` → PUT test red.

## PR P2 — SPA: the peer turn

**Files:** `spa/src/lib/nex/event-reducer.ts` (+test), `spa/src/lib/nex/message-types.ts`, new `spa/src/components/peer/PeerMessageBlock.tsx` (+test), `spa/src/components/room/MessageRow.tsx`, `spa/src/components/chat/ChatTurnBody.tsx`, `spa/src/lib/nex/execution-list-effects.ts` (+`useExecutionListStore.test.ts:177`), `spa/src/lib/nex/types.ts`, `spa/src/lib/nex/transcript-search.ts`, fixtures dir, locales.

1. **Fixtures.** Copy the three recorded files (+ README with source commit) into `__fixtures__/peer-mailbox/`.
2. **Reducer** (switch near `event-reducer.ts:595`; the reducer only ever sees the per-execution stream and history pages, both full payloads): `case 'peer_message'`: `markTurnStart({...next, summaryStale: true}, created_at, turn_id || null)`; **does not** touch `pendingLocal` / `sendLocked`; push one synthetic `{type: 'purdex_peer', from_name, text, msg_id, at: created_at}` (defensive: a payload without `text` opens the turn and pushes nothing). `applyTurnRules` (`:514`) adds `peer_message` to the `turnLive` condition. The raw payload is never appended (the unknown-kind fallthrough at `:576-580` must not see it). Site-wide frames never reach this reducer (step 6).
3. **Types.** `PurdexPeerMessage` in `message-types.ts`; `isOpeningLine` (`turns.ts:36`) stays false for it, so sent-history (ArrowUp) never offers a peer's text as the user's own.
4. **Block.** `PeerMessageBlock`: left border in the info colour, header「來自 {from_name} · {time}」, body through `RoomProse`. Room view renders it from `MessageRow`; chat view renders the same block left-aligned (never a user bubble). The component takes plain props so the terminal underlying can reuse it later.
5. **Search.** `transcript-search.ts` indexes the peer text (the existing prelude peer note path at `:252-255` is the pattern).
6. **Site-wide.** `SITE_STREAM_KINDS` (`execution-list-effects.ts:29-56`) gains `'peer_message'`; update the exact-list test.
7. **Capabilities type.** `NexCapabilities.peer_message?: {enabled, route, max_pending, wake_template_version}`.

**Tests (real fixture driven):** the scoped fixture opens a turn with its `turn_id` and yields one `purdex_peer` line; a peer turn arriving while the pane's own send is pending leaves `pendingLocal`/`sendLocked` intact; the site-wide fixture schedules a list refetch (execution-list-effects test) and is not applied to any pane; Room and Chat transcripts render the block with sender and text and no user bubble; sent-history excludes it; search finds it.
**Mutations:** remove `peer_message` from the `turnLive` condition → test red; clear `pendingLocal` in the peer case → test red; render through `ChatUserBubble` → chat test red.

## PR P3a — virtual names: store, assignment, addresses

**Files:** `internal/store/meta.go`, new `internal/store/peer_name.go` (+test), new `internal/peers/vname.go` (+test), `internal/peers/record.go`, new `internal/module/peers/vnames.go` (+test), `internal/module/peers/module.go` (wiring, `localEnvelope`), `internal/module/peers/titles.go` (whoami), `internal/module/peers/origin_resolver.go`, daemon wiring where `WithNameSink` is attached (`cmd/pdx`), `internal/module/peers/send_local_test.go`.

1. C1/C2 with table tests (suffix, truncation to 64, trailing `-`, unroutable base, 6-char base36 impossibility).
2. C3 store, including a concurrency test: two goroutines `Assign` different names for one sid → both return the same stored name.
3. `PeerRecord.Name` (C4); `applyIdentity` (`record.go:353-366`) takes the virtual name instead of the registry name for `Address`.
4. C5 namer, called from `localEnvelope` before `Build` with candidates for every live entry; `Build` input gets `VirtualNames map[string]string`.
5. whoami (`titles.go:116-134`) and `OriginResolver.originOf` (`origin_resolver.go:71-89`) produce the same address as the envelope (same namer, same store).
6. Frames: `send_local.go:106-112` already uses the origin's address as `FromName`; add a test pinning that it is the virtual address.

**Tests:** assignment once (a later pass with a different registry name keeps the first name); lineage inherits the predecessor's name; **late lineage**: a fallback name assigned first is upgraded once when lineage appears, and a `lineage` row is never overwritten by a later pass; concurrent passes (one with lineage, one without) end with the lineage name; manual `/clear` (no lineage) gets a new name; store error → ref-form address, no name invented; whoami == envelope address; a team notice built through `OriginResolver.Address` carries the virtual address (regression for `internal/module/team/spawn_handler.go:43-60,199-220`).
**Mutations:** `Assign` without `DO NOTHING` (overwrite) → rename test red; lineage walk skipped → inherit test red; `AdoptLineage` without the `source <> 'lineage'` guard → never-overwritten test red.
**Merge gate:** after P3a and before P3b, addresses *display* the virtual name while routing still matches the registry name. So **P3a merges with no bump PR after it**; the bump PR comes only after P3b merges, and the deploy is cut from that bump. The P3a PR body states this.

## PR P3b — resolution by virtual name

**Files:** `internal/peers/address.go` (+test), `internal/module/peers/module.go` (`remoteAddress` `:992-1010`), `internal/module/peers/send.go` (hint `:31-57`, not-found path `:432`), `cmd/pdx/msg.go` (usage text `:45-59`, whoami rendering `:685-702`), `spa/src/stores/usePeerStore.ts` (`:95-110`), `spa/src/components/StatusBar.tsx` (`:263,294-297`, displayed and copied address), `spa/src/components/RenamePopover.tsx` (`:119-137`), their tests, repo `CLAUDE.md` § Peer addresses.

1. Tier 1 (`address.go:305-322`) and the combined `"<name> [<ref>]"` check (`:211-291`) compare `rec.Name`; a row is name-addressable when it is a live entry row (today's `hasLiveEntry`) — P4 extends the predicate to execution rows.
2. Remote rows: `remoteAddress` prefers `rec.Name` when `RoutableName`, else the current rule (old remote daemons keep working).
3. Not-found hint: when the head equals some live row's `Agent.PeerName`, the 404 detail appends `did you mean <alias>/<Name>?` (only when that row has a `Name`). Rewrite `peerNotFoundHint` for v5.
4. Docs: repo `CLAUDE.md` Peer-address section describes the virtual name (`<base>-<ref[1:3]>`, fixed for the conversation's life, registry name no longer routes).
5. SPA: the status bar shows and copies the record's `address` (not `agent.peer_name`); the rename popover shows the address as the conversation's address and the registry name only as "CLI 名稱" if at all. `cmd/pdx` whoami prints the virtual address.
6. Audit every other caller that composes an address from `Agent.PeerName` / `agent.peer_name` (grep under `internal/`, `cmd/`, `spa/src`), switch each to `Address`/`Name`, and list the audit in the PR body with file:line.

**Tests:** registry name no longer resolves (404 + hint); virtual name resolves; `"<registry name> [ref]"` → `name_mismatch`; two rows with one virtual name → `ambiguous`; remote row without `name` resolves by the old rule; StatusBar copy test yields the virtual address; whoami output test.
**Mutations:** tier 1 back to `Agent.PeerName` → test red; hint dropped → test red; StatusBar back to `peer_name` → copy test red.

## PR P4 — execution rows + local last hop

**Files:** new `internal/peers/execpeers/execpeers.go`, `internal/module/nex/module.go` (register), new `internal/module/nex/peers_service.go` (+test), `internal/module/nex/engine_iface.go`, `internal/peers/record.go`, `internal/peers/address.go`, `internal/peers/wire.go`, `internal/module/peers/module.go`, new `internal/module/peers/send_exec.go` (+test), `internal/module/peers/send.go`, `cmd/pdx` peers rendering (kind column), SPA peers view (render `row_kind: execution` without crashing).

1. C7 service in the nex module: `Rows` walks every page (C7) of non-terminal, non-archived executions with `SessionID` else `ResumeSessionID`, carrying cwd, state, title, live pid; `MailboxEnabled` from the assembled `cfg.Peer.Enabled`; `Send` → `PeerMessage` with `PrincipalID` = the host principal plus `/peers`, error classified per C7.
2. peers module: lazy `c.Registry.Get("nex.peers")` per request (absent → no execution rows, nothing else changes; `Rows` error → no execution rows and envelope `executions_unavailable = true`). `localEnvelope` appends C6 rows; namer candidates include execution sids with `dirBase = filepath.Base(cwd)`.
3. **One row per sid** (spec §4.1), applied after `Build` over **all** rows by `Agent.SessionID`, not only `row_kind:"entry"` (`internal/peers/record.go:147-173,209-223,287-319` — a live entry may already be consumed by a tmux session row): tmux session row > execution row > entry row. An entry row with an execution's sid is dropped and its pid moves onto the execution row; a tmux session row with an execution's sid suppresses the execution row.
4. Resolve: execution rows are name/ref-addressable (extend the P3b predicate); a miss while `executions_unavailable` → `ErrResolveNotReady` (same shape as `LineageUnavailable`, `address.go:397`); the deliverable guard (`send.go:436`) admits execution rows.
5. `sendExecution` (spec §4.2): field mapping table, result mapping (`delivered` / `queued`), the full error table of spec §4.2 from the typed error's `Kind`, audit `out` row + `SetResult` with the turn id in `native_msg_id`, `self_target` when the origin sid equals the execution sid.

**Tests:** idle and running executions each yield exactly one row; folding when the live entry is a standalone entry row **and** when it is attached to a tmux session row; second `List` page included; repeated cursor and mid-walk error → `executions_unavailable` and a miss answers 503 `not_ready`; mailbox off → `mailbox_disabled`; nex absent → peers unchanged; each mapped field; each row of the error table (nex side: sentinel → Kind; peers side: Kind → HTTP/code/detail); turn id in the audit row; self-target; never-interrupt (nex side, recording `nexService` fake: call log is exactly `[PeerMessage]` for success and every error path).
**Real-engine test:** one test assembling the real Nexen (pattern `exit_engine_test.go`) with `[peer] enabled=true` and a fake claude binary, proving the 200/403 paths through `Send`.
**Mutations:** emit the folded entry too → one-row test red; fold only `row_kind:"entry"` → tmux-attached test red; stop after the first page → second-page test red; map `queued` to `delivered` → result test red; add an `Interrupt` call before `PeerMessage` in the nex wrapper → call-log test red; map `ErrPeerInvalid` to `mailbox_error` → error-table test red.
**Acceptance (mlab, after deploy):** send to a sleeping execution → `delivered`, it wakes, its reply arrives through `pdx msg send <reply_to>`; a second message while it runs → `queued`, processed after; mailbox toggled off → `not_deliverable`.

## PR P5 — cross-host

**Files:** `internal/peers/wire.go` (+validate tests), `internal/module/peers/send.go` (remote branch), `internal/module/peers/deliver.go` (+test), `internal/module/peers/e2e_test.go`.

1. `WireTo.ExecutionID` (validate: exactly one of the session tuple or the execution id); `WireFrom.Name` filled from the origin's `Name`.
2. Sender: a resolved remote execution row → `postDeliver` with `ExecutionID`.
3. Receiver (`deliver.go`): an `ExecutionID` target is looked up among local execution rows (`target_gone` when absent) and sent through `sendExecution`, with `from_name` = `reply_to` = `<receiver's alias for the sender host>/<Name, else Ref>`, `from_mode` clamped as today (prompting); audit `in` row.
4. Old receivers have no execution rows, so nothing resolves to them — no compatibility path.

**Tests:** two-daemon e2e (`e2eDaemon`) with a fake `nex.peers` on the receiver: delivered, queued, target gone, mailbox off. **Mutation:** use the sender's own alias in `from_name` → e2e test red.

## PR P6 — "由 peer 訊息喚醒" (outline; gated)

Starts after nexen-74's two-step verification (spec §8). If Nexen adopts the two-step behind the same endpoint, the wake turn and the native peer line both arrive as events/transcript; P6 renders the wake exchange as one collapsed tool-like row and the peer line through `PeerMessageBlock`. If not, P6 collapses Nexen's wrapper (template frame) into that row. Either way P6 is written as its own plan section once the data shape is known.

---

## Coordinator decisions (derived from the user's rulings; the user may overturn)

1. Registry names stop routing (consequence of U6 "one rule for all conversations"); a not-found hint points to the virtual address.
2. A store error never invents an unpersisted name: the row falls back to the ref form for that pass.
3. Executions with no observed registry name use the cwd basename as the base (spec §3.2 (3)).
4. Wording「由 peer 訊息喚醒」, not「喚醒」alone (taken by relay U21).
5. P3a and P3b deploy together (no bump between them).
6. A fallback-sourced virtual name is upgraded once to the relay predecessor's name when lineage shows up late; this is the only time a name changes (spec §3.2).
7. One row per session id: tmux session row > execution row > entry row (spec §4.1).

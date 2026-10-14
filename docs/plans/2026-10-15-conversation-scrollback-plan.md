# Scroll back across relays and /clear — plan (S-1 … S-3)

Spec: `docs/specs/2026-10-15-conversation-scrollback-spec.md` (facts §3, origin/main `dbb46b57d`, purdex-ios `23032c6b8`).

Rev 2 (2026-10-15) after codex plan review `task-mv8ecfog-qg8w52` (16 findings, all taken): the previous id is captured inside the frame update, the agent module offers its own reader, the clients map global indices back to file indices, a replacing snapshot drops earlier segments, read-only turn views use their segment's session, search stays in the live segment.

## 1. Design choices (for the reviewer)

- **One record point for every `/clear`**, in the agent module where `SessionStart{source:clear}` is applied — the only place that holds both ids durably. The previous id is captured **inside `applyFrameEvent`, before `recordSessionIdentity` overwrites the frame's `SessionID`** (`frame_ops.go:~1147`), and carried out on the apply result and on `SessionStartEvent.PrevSessionID`; `last_agent_run` of the same tmux code is the fallback for the order where `SessionEnd` already ended the frame. The mod's `session.switch` is in-memory and also fires on resume; the relay's `cleared` report arrives after the SessionStart. A relay's `/clear` is recorded here too, as a plain clear; the reader upgrades it to `relay` from `session_lineage` at read time.
- **`session_lineage` is not touched.** Its primary key and NOT NULL columns (`op_id`, `predecessor_ref`) would refuse a manual clear, a shared row would collide with the relay's own insert (`checkLineage`, `relay_store_report.go:44-52`), and quota chain roots and the workbook root read it.
- **Two readers, one answer.** The agent module registers `agent.PredecessorReaderKey` (over the new table); the team module registers `team.PredecessorReaderKey` (over `session_lineage`). The conversation module (its `Dependencies()` already name `agent` and `team`) asks team first (`relay`), then agent (`clear`); a missing reader is skipped.
- **The predecessor rides on `window`** in every snapshot body — HTTP and WS share `snapshotBody` — never in increments.
- **Clients keep one index space** with per-segment negative bases. Every request into a segment maps back to that file's own index: `before = segmentFirstGlobal − segment.base`. A replacing snapshot of the live session (reset, reconnect, return-to-live, `around=`) drops the earlier segments, so bases are only ever computed against a fully loaded (exhausted) newer segment.
- **Earlier segments are fetched by plain HTTP**, never acquired: no WS, no live state, no `retireStaleSessions` interaction.

## 2. Phases

| PR | content | reviewer extra | est. lines |
|---|---|---|---|
| S-1 | daemon: table in the frames migration, capture + recording, agent and team readers, conversation `window.predecessor` | 1f (team reader, agent module) | ~550 |
| S-2 | SPA: segments, cross-segment `loadBefore`, replacing-snapshot rule, divider / end states, read-only and search scope (deck + chat), i18n | — | ~750 (split into two stacked PRs if over 800) |
| S-3 | iOS (purdex-ios): the same | — | ~600 |

S-2 and S-3 start after S-1's response shape is merged (fixtures may come first). Every PR: TDD, one commit per task, mutation table; daemon `go test -race` on the touched packages + `gofmt -l` / `go vet`, never the full `go test ./...`; SPA full vitest `--maxWorkers=3` + `tsc -b --force` + lint before merge on a fresh origin/main; iOS `swift test` / build on air26 only. codex R1 → attacker → critic.

## 3. S-1 — daemon

- **T-1.1 Table** (`internal/store`): `session_predecessors(session_id TEXT PRIMARY KEY, predecessor_session_id TEXT NOT NULL, at INTEGER NOT NULL, tmux_code TEXT NOT NULL)` created in **`migrateFramesDB`** (the lifecycle `last_agent_run` and frame identity already use; additive, `IF NOT EXISTS`); methods on the frames store: `RecordPredecessor(sid, prev, at, code)` = `INSERT OR IGNORE`; `PredecessorOf(sid) (prev string, at int64, ok bool, err error)`.
- **T-1.2 Capture and record** (`internal/module/agent`):
  - In `applyFrameEvent`, for `SessionStart` with `source == "clear"`, read the frame's `SessionID` **before** `recordSessionIdentity`; return it as `PrevSessionID` on the apply result, and add `PrevSessionID` to `SessionStartEvent`.
  - At the publish point (`handler.go:~749-760`): `prev` = the captured id; if empty, resolve the tmux code with `resolveSessionCodeFromHook(req)` (failure → no fallback, logged at debug) and use `LastRun(code)` when its `session_id` is set and `ended_at` is within 120 s. Skip when `prev` is empty or equals the new id. Record with that code (empty code allowed for the frame path).
  - Any other `source` records nothing. A store error is logged, never blocks the hook.
- **T-1.3 Readers**: `agent.PredecessorReaderKey` registered in the agent module's `Init` (over T-1.1); `team.PredecessorReaderKey` in `internal/team` + impl in `internal/module/team` (`SELECT predecessor_session_id, at FROM session_lineage WHERE session_id = ?`), registered in team `Init`.
- **T-1.4 Conversation** (`internal/module/conversation`): `predecessorOf(sid)` = team reader (kind `relay`) else agent reader (kind `clear`), each optional; `windowJSON.Predecessor *predecessorJSON \`json:"predecessor,omitempty"\`` filled in `snapshotBody` (HTTP first page, `before=` / `around=` pages and the WS snapshot); included in the envelope sizing before the 4 MiB trim and kept in the final marshal (incl. the late echo-ID re-encode); increments untouched.
- Tests:
  - Migration: an existing DB without the table gains it; idempotent re-run; the table exists when the store is used only through `.Frames()`.
  - Record, each arrival order: **Start first** (frame still holds the old id → captured), **End first** (frame ended, `last_agent_run` written → fallback), **late old SessionEnd after the Start overwrote the id** (rejected by the exact-id gate, record already made by the Start), **sweep removed the frame before the End** (no `last_agent_run` → nothing recorded, logged); stale `ended_at` (> 120 s) skipped; code unresolvable → no fallback; same id skipped; `source=resume` / `startup` / `compact` skipped; a second record for the same sid ignored. (M: record on resume; M: read the id after the overwrite; M: drop the 120 s check.)
  - Readers: hit / miss for each.
  - Conversation: relay wins over clear for the same sid; the relay-gap case (clear first, relay after the lineage row lands) answers `clear` then `relay`; none → field absent; a missing reader tolerated; `before=`, `around=` and the WS snapshot carry it; increments do not; a body at the 4 MiB edge keeps the field and still trims the oldest turns (M: kind always `clear`; M: field only on the first HTTP snapshot).

## 4. S-2 — Mac (SPA)

- **T-2.1 Types / parse**: `Window.predecessor?: {sessionId, kind: 'relay' | 'clear', at}` (strict parse; unknown kind → absent).
- **T-2.2 Model** (`lib/conversations/model.ts`): the document gains `segments: Segment[]` (`{sessionId, kind, at, base, totalTurns, hasMoreBefore, predecessor, ended?: 'start' | 'missing'}`), newest last; the live segment has base 0.
  - `prependSegment(doc, page)`: allowed only when the oldest segment is exhausted; `base = oldestSegment.base − page.window.total_turns` (an exhausted segment's lowest global index is its base); the page's turns are shifted by it.
  - `applyOlderPage` within a segment shifts the page by that segment's base.
  - `doc.totalTurns` stays the **live** segment's count (the scroll-memory anchor check in `useTranscriptScroll` reads it for the live session only); each segment keeps its own.
  - A replacing snapshot (`applySnapshot` on reset / reconnect / return-to-live, `applyAround`) rebuilds the document with the live segment only.
  - Live increments touch only the live segment.
  - Document `hasMoreBefore` = the oldest segment has more, or it has a predecessor not yet tried.
- **T-2.3 Store** (`stores/useConversationStore.ts`) `loadBefore`:
  - The oldest segment has more → `before = segmentFirstGlobal − segment.base` in its session (HTTP via `fetchConversationSnapshot`).
  - Else predecessor present → its newest page, `prependSegment`.
  - `not_found` → oldest segment `ended: 'missing'`; a session id already in `segments` → `ended: 'start'`.
  - Fence: the result applies only when the document under that key still exists, its generation is unchanged, and its oldest segment is still the one the load started from (a replacing snapshot or a pane switching to another key makes it a no-op); `release` of the key aborts in-flight segment loads.
- **T-2.4 Views** (`ChatView`, `DeckView`, `TurnView`):
  - A divider item at each segment boundary (「接力 · MM/DD HH:mm」 / 「/clear · MM/DD HH:mm」, the newer segment's `kind` / `at`, refreshed by each live snapshot); 「history start」 only when the oldest segment ended `start`; 「更早的內容已不存在」 when `missing`.
  - Turns carry their segment's `sessionId`; read-only views (show all output, open a subagent, the subagent loader) use it; live-only actions (send, rewind, fork, approvals, queued input) are absent on earlier segments.
  - Search and jump index the live segment's turns only.
  - i18n zh-TW / en.
- Tests:
  - Model: bases keep indices unique and increasing across three segments; `before` maps back to the file index (M: send the global index); `applyOlderPage` inside an earlier segment; live increment touches only the live segment; replacing snapshot / `applyAround` drop earlier segments; `totalTurns` stays the live count (M: base 0 for every segment).
  - Store: exhausted → predecessor fetched without `acquire` (spy) (M: acquire it); `not_found` → missing; cycle → start; a replacing snapshot during an in-flight segment load → no-op; pane switching key → no-op; release aborts.
  - Views: divider per kind and its refresh from `clear` to `relay`; end texts; earlier-segment turns have no live actions and open subagents with their own session id (M: live session id); search ignores earlier segments; scroll anchoring across a segment prepend (the existing `useOlderTurns` test pattern).
  - Tab switch (Tab-hosted checklist): the document memory keeps or drops segments on remount — assert what it does and that the live view is intact either way.

## 5. S-3 — iOS (purdex-ios)

- `ConvWindow.predecessor` optional (`ConvAPI.swift`); `ConversationState` segments with bases (`prepend` within a segment maps back to the file index for `before=`; `prependSegment`; replacing snapshot / around keeps the live segment only; `totalTurns`-like values stay live); `ConversationFeed.loadOlder()` crosses to the predecessor as in T-2.3 (HTTP only, no WS) with the same fence (session reset via `recheckSession` and replacing snapshots make in-flight loads no-ops); deck and dialogue views — divider, end texts, earlier segments read-only with their own session for read-only views, search / jump on the live segment.
- Tests: decoding with and without `predecessor`; base arithmetic and the `before` mapping; live increments; replacing snapshot and around; `recheckSession` during an in-flight segment load; missing transcript end state; read-only actions on earlier segments; `loadOlder` across three segments with a stub client.
- Built and tested on air26 only (`swift test` / xcodebuild), OTA swap after release (the usual iOS release steps).

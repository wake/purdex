# Scroll back across relays and /clear — plan (S-1 … S-3)

Spec: `docs/specs/2026-10-15-conversation-scrollback-spec.md` (facts §3, origin/main `dbb46b57d`, purdex-ios `23032c6b8`).

## 1. Design choices (for the reviewer)

- **One record point for every `/clear`**, in the agent module where `SessionStart{source:clear}` is accepted — the only place that holds both ids durably (frame before overwrite, or `last_agent_run`). The mod's `session.switch` is in-memory and also fires on resume; the relay's `cleared` report arrives after the SessionStart. A relay's `/clear` is therefore recorded here too, as a plain clear; the reader upgrades it to `relay` from `session_lineage`.
- **`session_lineage` is not touched.** Its primary key and NOT NULL columns (`op_id`, `predecessor_ref`) would refuse a manual clear, a shared row would collide with the relay's own insert (`checkLineage`, `relay_store_report.go:44-52`), and quota chain roots and the workbook root read it.
- **The predecessor rides on `window`**, beside `has_more_before`, in every snapshot response (first and `before=` pages), never in increments: the client needs it exactly when a segment runs out, and header / increments are the live session's.
- **Clients keep one index space** with per-segment negative bases instead of composite keys, so `applyOlderPage`, `data-turn-index`, `useOlderTurns` and iOS `prepend` keep their contracts; only the code that knows about segments changes.
- **Earlier segments are fetched by plain HTTP**, never acquired: no WS, no live state, no `retireStaleSessions` interaction.

## 2. Phases

| PR | content | reviewer extra | est. lines |
|---|---|---|---|
| S-1 | daemon: table + migration, recording, team reader, conversation `window.predecessor` | 1f (team reader, agent module) | ~450 |
| S-2 | SPA: segments, cross-segment `loadBefore`, divider / end states (deck + chat), i18n | — | ~650 |
| S-3 | iOS (purdex-ios): the same | — | ~500 |

S-2 and S-3 start after S-1's response shape is merged (fixtures may come first). Every PR: TDD, one commit per task, mutation table; daemon `go test -race` on the touched packages + `gofmt -l` / `go vet`, never the full `go test ./...`; SPA full vitest `--maxWorkers=3` + `tsc -b --force` + lint before merge on a fresh origin/main; iOS `swift test` / build on air26 only. codex R1 → attacker → critic.

## 3. S-1 — daemon

- **T-1.1 Table** (`internal/store`, the agent events store): `session_predecessors(session_id TEXT PRIMARY KEY, predecessor_session_id TEXT NOT NULL, at INTEGER NOT NULL, tmux_code TEXT NOT NULL)`, created in that store's migration (additive; an existing deployed DB gains the table on start); `RecordPredecessor(sid, prev, at, code)` = `INSERT OR IGNORE`; `PredecessorOf(sid) (prev string, at int64, ok bool, err error)`.
- **T-1.2 Recording** (`internal/module/agent`): when a `SessionStart` with `source == "clear"` is accepted (the path that publishes `SessionStartEvent`, `handler.go:~759`), take `prev` = the frame's `SessionID` read **before** `recordSessionIdentity` overwrites it; if empty, `LastRun(code)` when its `session_id` is set and `ended_at` is within 120 s; skip when `prev` is empty or equals the new id; record. Any other `source` records nothing. A store error is logged, never blocks the hook.
- **T-1.3 Team reader** (`internal/team` key + `internal/module/team` impl): `PredecessorReaderKey` → `PredecessorOf(sid) (prev string, at int64, ok bool, err error)` over `session_lineage` (`SELECT predecessor_session_id, at … WHERE session_id = ?`), registered in team `Init`.
- **T-1.4 Conversation** (`internal/module/conversation`): `predecessorOf(sid)` = team reader first (kind `relay`), else the store (kind `clear`), each optional (missing reader → skipped); `windowJSON.Predecessor *predecessorJSON \`json:"predecessor,omitempty"\`` filled on every snapshot body (`snapshotBody`) — the first response and `before=` / `around=` pages; increments untouched. `convmodel` gains nothing (the window is the HTTP shape).
- Tests: migration on an existing DB without the table (and idempotent re-run); record — frame-before-overwrite case, `last_agent_run` case, stale `ended_at` (> 120 s) skipped, same id skipped, `source=resume` / `startup` / `compact` skipped, second record for the same sid ignored (M: record on resume; M: drop the 120 s check); team reader hit / miss; conversation — relay wins over clear for the same sid, clear only, none (field absent), a missing reader tolerated, `before=` page carries it, increments do not (M: kind always `clear`; M: field only on the first snapshot).

## 4. S-2 — Mac (SPA)

- **T-2.1 Types / parse**: `Window.predecessor?: {sessionId, kind: 'relay' | 'clear', at}` (strict parse; unknown kind → absent).
- **T-2.2 Model** (`lib/conversations/model.ts`): `segments: Segment[]` on the document (`{sessionId, kind, at, base, hasMoreBefore, predecessor, ended?: 'start' | 'missing'}`); `prependSegment(doc, page, predecessorMeta)` shifts the page's indices by `base = (lowest current index) − (page.window.total_turns)` (so the segment's highest index sits just below the next segment's lowest) and prepends; `applyOlderPage` is applied per segment (the page's indices shifted by that segment's base). The live segment keeps base 0; live increments touch only it. `hasMoreBefore` (document) = the oldest segment has more, or it has a predecessor not yet tried.
- **T-2.3 Store** (`stores/useConversationStore.ts`): `loadBefore` — oldest segment has more → `before=` in its session (HTTP via `fetchConversationSnapshot`, base shift); else predecessor present → its newest page, `prependSegment`; `not_found` → oldest segment `ended: 'missing'`; a session id already in `segments` → `ended: 'start'`. Generation-fenced like today (a pane re-resolving to a new session drops in-flight older loads).
- **T-2.4 Views**: `ChatView` / `DeckView` render a divider item at each segment boundary (「接力 · MM/DD HH:mm」 / 「/clear · MM/DD HH:mm」), 「history start」 only when the oldest segment ended `start`, 「更早的內容已不存在」 when `missing`; items of earlier segments render without live-only actions (send / rewind / fork / approval affordances — list confirmed against the current item actions). i18n zh-TW / en.
- Tests: model — base arithmetic keeps indices unique and increasing across three segments; `applyOlderPage` inside an earlier segment; live increment touches only the live segment (M: base 0 for every segment → de-dup drops turns); store — exhausted → predecessor fetched without `acquire` (spy), `not_found` → missing, cycle → start, generation change drops the result (M: acquire the predecessor); views — divider text per kind, end texts, earlier-segment items have no live actions; scroll anchoring across a segment prepend (the existing `useOlderTurns` test pattern); a tab switch away and back keeps the loaded segments if the existing doc memory keeps them, else documents why not (Tab-hosted checklist).

## 5. S-3 — iOS (purdex-ios)

- `ConvWindow.predecessor` optional (`ConvAPI.swift`); `ConversationState` segments with bases (`prepend` within a segment, `prependSegment`); `ConversationFeed.loadOlder()` crosses to the predecessor as in T-2.3 (HTTP only, no WS); `recheckSession` keeps today's reset; deck and dialogue views — divider, end texts, read-only earlier segments.
- Tests: the same model cases; decoding with and without `predecessor`; `loadOlder` across segments with a stub client.
- Built and tested on air26 only (`swift test` / xcodebuild), OTA swap after release (the usual iOS release steps).

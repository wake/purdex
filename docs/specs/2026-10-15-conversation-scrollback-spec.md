# Scroll back across relays and /clear in deck and chat — spec

> 2026-10-15, 88 (interface line). The user's decisions are §2; the rest is 88's design on them.
> Facts (paths from the repo root) read from origin/main `dbb46b57d` and purdex-ios origin/main `23032c6b8` on 2026-10-15.

## 1. Problem

A relay — and a plain `/clear` — is `/clear` in the same Claude Code process: a new session id and a new transcript file (`~/.claude/projects/<proj>/<newSid>.jsonl`; the old file stays, measured: 12 MB before, a new file after). Deck and chat read one session id: when the pane's session id moves, the view switches to the new file and scrolling up stops at its first turn (「history start」). Everything before the relay is out of reach, although the file is still on disk.

## 2. User decisions (2026-10-15, do not reopen)

1. Deck and chat scroll up past a relay into the previous session, and on through every earlier one.
2. **No depth limit**: as long as the previous transcript file exists, keep going; when it does not, stop there.
3. **A manual `/clear` is joined too**, not only relays.
4. **Mac and iOS** both.

## 3. Facts this rests on

- **Pane → session**: `spa/src/hooks/useConversationOfPane.ts:36-61` resolves the pane's CC session id from `tmux.rebuild.agent.sessionId` (`fetchSessionProvenance`) and holds it with `useHeldConversation`; a new id is a new store key `hostId\0sessionId` (`spa/src/stores/useConversationStore.ts:53`) and a new document. `retireStaleSessions` (`spa/src/lib/conversations/pane-release.ts:64-79`) drops the old session's per-pane memories.
- **Reading**: `GET /api/conversations/claude/{sid}?turns=&before=&around=&after=` (`internal/module/conversation/http.go:118`), default 20 turns, max 200, a 4 MiB body cap dropping the oldest turns; `convfeed` opens only `<sid>.jsonl` (`internal/convfeed/resolve.go:84,152`) — a previous session's file is readable by its id with no new path rule. A missing file is `not_found`.
- **Paging**: `window {first_index, last_index, total_turns, has_more_before}` (`http.go:60-64`, all required); SPA `loadBefore` (`useConversationStore.ts:250-272`) → `applyOlderPage` (`spa/src/lib/conversations/model.ts:73-79`, de-duplicates and sorts by `turn.index`); scroll anchoring `useOlderTurns` (`spa/src/hooks/useOlderTurns.ts:63-74`) + `shiftBy` (`useTranscriptScroll.ts:432-446`) — prepending is already anchored. 「history start」: `ChatView.tsx:155`, `DeckView.tsx:170`. `fetchConversationSnapshot` (`spa/src/lib/conversations/api.ts:33-43`) is plain HTTP, no WS.
- **Turn index is per file** (starts at 0 in every transcript): a previous session's indices collide with the current one's (SPA de-dup by index, `data-turn-index` anchors at `ChatView.tsx:152`, iOS `ConversationState.prepend` sorts by index, `firstIndex = min`).
- **Relay lineage**: `session_lineage(session_id PK, predecessor_session_id, predecessor_ref, op_id, at)` (`internal/module/team/relay_store.go:56`), written at the relay's `cleared` report (`relay_store_report.go:353`), never deleted; it also feeds quota chain roots and the workbook's `RootSessionOf`. No reader returns "the predecessor sid of X" today (`PreviousRefs` returns refs; `RootSessionOf` walks to the root). The `cleared` report lands **after** the daemon has handled the relay's `SessionStart` (`cmd/pdx/plugin/purdex/hooks/register.js:1936-1946`).
- **Manual `/clear` leaves no link**: hook `SessionEnd{reason:clear}` + `SessionStart{source:clear}`; `SessionStartEvent` (`internal/module/agent/terminal_sessions.go:34-47`) carries the new id, pane and tmux code but not the old id. The old id is available while handling it: the frame's `SessionID` before `recordSessionIdentity` overwrites it (`frame_ops.go:1147`, out-of-order case) or `last_agent_run[code].session_id` (`internal/store/last_run.go:122-165`, written when the SessionEnd ended the frame; not written when `tmux_instance` is empty, `last_run.go:144`). The mod's `session.switch{prev_sid}` is in-memory only and also fires on `resume`.
- **Same host**: the lineage row and the transcript live on the host of the pane; relays do not cross hosts (`relay_store_report.go:101-104`, `relay_moved.go:41`).
- **iOS**: `App/Sources/Conversation/ConversationFeed.swift` `loadOlder()` (`:250-257`) pages with `before=` and `state.prepend`; `ConversationState.swift:36-41` de-duplicates by turn id but sorts by index; `ConvAPI.swift:62-79` `ConvWindow` fields required; a session change (`recheckSession`, `:230-243`) clears the state; deck and dialogue show 「載入較早的內容…」 / 「已到對話開頭」 (`Deck/DeckView.swift:66-85`, `Deck/DialogueView.swift:127`).
- Claude Code deletes a transcript 30 days after its last write (`cleanupPeriodDays` unset).

## 4. Design

### 4.1 Daemon: who came before

- **Record every `/clear`** (manual and relay alike) in the agent module, when a `SessionStart` with `source = clear` is accepted: the previous id is the frame's session id before it is overwritten, else `last_agent_run` of the same tmux code when it ended within the last 120 s; it must differ from the new id. Stored in a new table of the agent store, `session_predecessors(session_id TEXT PRIMARY KEY, predecessor_session_id TEXT NOT NULL, at INTEGER NOT NULL, tmux_code TEXT NOT NULL)` — created by a migration of that store (additive), never deleted (a row is ~150 bytes). `INSERT OR IGNORE`: the first record wins. `source = resume`, `startup` and `compact` are never predecessors.
- **Read**: the conversation module answers `PredecessorOf(sid) → {session_id, kind, at} | none`. A relay's predecessor comes from `session_lineage` (kind `relay`) — read through a new team registry reader `PredecessorOf` (the existing `SELECT predecessor_session_id … WHERE session_id = ?`); otherwise from `session_predecessors` (kind `clear`). Both are local lookups by primary key. `session_lineage` is not changed (quota and workbook keep their meaning).
- **API**: every snapshot response of `GET /api/conversations/claude/{sid}` — the first one and every `before=` page — carries `window.predecessor: {session_id, kind: "relay" | "clear", at}` when one is known, and omits it otherwise. Increments do not carry it. A previous session is read with the same endpoint and its own id; its file gone → the existing `not_found`.

### 4.2 Mac (SPA)

- **Segments**: the document keeps an ordered list of segments, newest last: `{sessionId, kind, at, base, hasMoreBefore, predecessor}`. The live session is the last segment with `base 0`; an earlier segment's turns are shifted by a negative `base` (its own `-(total_turns)` below the next one's lowest index), so one index space stays strictly increasing and every existing consumer of `turn.index` (de-dup, sort, anchors, `useOlderTurns`) keeps working.
- **Loading older**: `loadBefore` first pages the oldest loaded segment (`before=` within its own session); when that segment is exhausted and has a `predecessor`, it fetches the predecessor's newest page with `fetchConversationSnapshot` (HTTP only — no `acquire`, no WS, no live state, not seen by `retireStaleSessions`) and prepends it as a new segment. Lazy: one page per scroll-to-top, as today.
- **Divider** between segments: 「── 接力 · 10/14 22:37 ──」 for `relay`, 「── /clear · 10/14 22:37 ──」 for `clear` (the newer segment's `at`, local time).
- **Ends**: the oldest segment exhausted with no predecessor → 「history start」 as today; its predecessor answers `not_found` → 「更早的內容已不存在」 and stop. A session id seen twice in the chain → stop (defensive).
- **Earlier segments are read-only**: no actions that belong to the live session (send, rewind, fork, approvals, queued input); search / jump stay within the live segment.
- Both deck and chat; the pane-resolution, live WS and send paths are unchanged.

### 4.3 iOS

- Same rule in `ConversationFeed.loadOlder()`: when the oldest segment is exhausted and `window.predecessor` is set, fetch the predecessor's newest page and prepend it as a segment; `ConversationState` keeps segment bases so indices stay unique and increasing; `ConvWindow.predecessor` decodes as optional (an older daemon → today's behaviour).
- The same divider and end texts in deck and dialogue views; earlier segments read-only.

## 5. Out of scope

Search or jump across segments; showing a previous session's live state; providers other than Claude Code (codex has no clear; opencode switches sessions in-process without `SessionStart`); keeping transcripts past Claude Code's 30-day cleanup (the user can raise `cleanupPeriodDays`).

## 6. Phases (each ≤ 800 lines / 20 files)

| phase | side | content |
|---|---|---|
| S-1 | daemon | `session_predecessors` table + migration; recording on `SessionStart{source:clear}`; team `PredecessorOf` reader; conversation `PredecessorOf` + `window.predecessor` |
| S-2 | SPA | segments in the conversation model; `loadBefore` across segments; divider and end states in deck and chat; i18n |
| S-3 | iOS (purdex-ios) | the same in `ConversationFeed` / `ConversationState` / deck and dialogue views |

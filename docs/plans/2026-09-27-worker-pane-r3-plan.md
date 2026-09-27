# Plan — worker pane R3: quick replies and transcript search

Spec: `docs/specs/2026-09-20-worker-pane-views-spec.md` v1.0 — §4.8 (input:
the quick-reply dock and output search), §3 #6 ("No search"), §4.1 (a turn
stays in the DOM so it can be "scrolled to, searched"), §10 Q5 (nothing in
R1–R3 may depend on new **wire** data — per-host config is not wire data from
Nexen; it is Purdex's own daemon). R1 shipped as alpha.448–454, R2 as
455–456.

Anchors measured on `0e62e52d`+ (alpha.456) in worktree `pane-r3`. Paths are
relative to the worktree root; SPA paths are under `spa/`.

## User decisions for R3 (2026-09-27, do not reopen)

| # | decision |
|---|---|
| Q1 | Tapping a quick reply **sends it at once**, like typing it and pressing Enter. Whatever is half-typed in the input stays untouched. |
| Q2 | The list is **per host**, stored in the daemon's host config next to resume templates, edited on the Hosts page. mlab and air26 each keep their own. Needs a daemon change and a daemon deploy on both machines. |
| Q3 | With nothing configured, the dock shows the defaults `continue` / `run the tests` / `explain that`. They can be edited or deleted; an emptied list shows no dock. |
| Q4 | Search covers **everything in the transcript, folded content included**; moving to a match inside a folded block expands that block and marks the text. Opened with Cmd/Ctrl+F; works in room and chat. |

## Facts this plan relies on (measured)

- Host config lives in the daemon module `internal/module/hostconfig/`:
  SQLite `host_config.db` (`module.go:30`), key constants `store.go:16-20`,
  routes `module.go:35-41`, `emptyFor` + `handleGet`'s fixed field map
  `handler.go:48-68`, CAS `putHandler` (`{items, baseRevision}`, 409 on a
  stale revision) `handler.go:71-124`, validation `validate.go`
  (`normalizeCommands` is the closest list-shaped example).
- SPA: `lib/host-config-api.ts` (`HostConfigPayload`,
  `HostConfigCollectionItems`, `fetchHostConfig`, `putHostConfig`),
  `stores/useHostConfigStore.ts` (`byHost[hostId]`, `ensureLoaded`, CAS
  `save`, 404 → `unsupported`), `lib/resume-templates.ts` (override →
  default lookup + `useResumeTemplateLookup` hook — the pattern Q3 copies),
  settings UI `components/hosts/CommandsSection.tsx` (tabs `normal` /
  `resume`) + `components/hosts/useHostConfigCollection.ts`
  (`CollectionKind = 'projects' | 'commands'`).
- Worker input: `components/room/WorkerInput.tsx` keeps its value in local
  state; `ExecutionView.tsx:228-229` renders it with
  `disabled={st.pendingSend || ended || !st.historyLoaded || streamDead || takeBackBusy}`
  and `onSend={(text) => void handleSend(text)}`; `handleSend`
  (`hooks/useExecutionActions.ts:55-90`) guards re-entry on `pendingSend`.
- Folding: `components/room/fold-context.tsx` `FoldStore` has `isExpanded`,
  `toggle`, `register`, `unregister`, `setTurn` — **no "expand this key"**.
  `FoldedOutput` does **not** render folded text (only the preview lines), and
  `SubagentBlock` / `ChatToolsLine` / `ChatEditedLine` / `ChatFailedLine` /
  `OperationBlock`'s raw input render nothing while collapsed. So search must
  run over the **data** (`messages`, tool activity), not the DOM, and reveal a
  match by expanding keys.
- Fold keys: output `foldKey` (= `blockKey(i,j)`), `${k}:input`,
  `${k}:thinking`, `${k}:diff`, `${k}:subagent`,
  `${keyPrefix}-turn-${ti}:chat-tools`, `${k}:chat-edited`, `${k}:chat-failed`.
- No find UI, highlight utility or Cmd+F handling exists; Electron registers
  no `CommandOrControl+F` accelerator (`electron/keybindings.ts`), so a
  renderer `keydown` handler receives it.
- Transcript scroll containers: `RoomTranscript.tsx:105`,
  `ChatTranscript.tsx:184`; refs are private.

## Working rules

As R1/R2: TDD, one commit per task with `git commit --only`, subagents prefix
every Bash with `cd <worktree>/spa && ` (or `cd <worktree> && ` for Go), type
check `npx tsc --noEmit -p tsconfig.app.json`, Go `go test ./internal/module/hostconfig/...`
and `go vet ./...`, both locale files together, Phosphor icons, **no
kill-type commands**.

## PR split

| PR | content | est. (non-test) | review |
|---|---|---|---|
| **R3-A** | daemon `quick_replies` collection + SPA api/store/lookup + Hosts-page tab | ~350 (Go ~120) | R1 + attack + critic (daemon contract) |
| **R3-B** | the quick-reply dock above the input | ~150 | R1 |
| **R3-C1** | search index, fold `expand`, `data-search-unit` anchors, highlight module (T3.1–T3.2) | ~350 | R1 + attack + critic |
| **R3-C2** | the search bar and Mod+F (T3.3) | ~250 | R1 |

Plan review (Claude reviewer, standing in for codex until 2026-09-30): 6
findings, all applied — #1 (critical) a failed quick-reply send must not
remount the input over half-typed text (T2.1), #2 the collection hook's
kind→field mismatch (T1.3), #3 the old-daemon note needs its own check (T1.3),
#4 no focusable pane root — Mod+F is a document listener on the active pane
(T3.3), #5 the quick-reply dock is input, so chat keeps it (T2.1), #6 R3-C
split in two.

Deploy: R3-A changes the daemon — after merge, rebuild and restart the mlab
daemon (`reference_pdx_daemon_runtime` in memory: new inode when replacing
the binary; pdx currently has no LaunchAgent, so confirm it is back up with a
health check) and update air26's daemon. The SPA must work against a daemon
that does not have the collection yet (see T1.2).

---

## R3-A

### T1.1 daemon: the `quick_replies` collection (TDD, Go)

- `store.go`: `KeyQuickReplies = "quick_replies"`.
- `validate.go`: `type QuickReply struct { ID string \`json:"id"\`; Text string \`json:"text"\` }`;
  `normalizeQuickReplies(raw)`: a JSON array, at most `maxQuickReplies = 20`,
  ids unique and matching `idPattern` (reuse `checkIDs`), text trimmed
  non-empty, ≤ `quickReplyMaxBytes = 1000`, no NUL, valid UTF-8.
- `handler.go`: `handleGet`'s field map gains `"quickReplies": KeyQuickReplies`
  (empty `[]`, revision 0 when never written).
- `module.go`: `PUT /api/hostconfig/quick-replies`.
- Tests (`validate_test.go`, `handler_test.go`, following the existing
  commands tests): accepts a valid list; rejects non-array, >20, empty text,
  oversize text, NUL, duplicate id, bad id; GET includes `quickReplies` with
  revision 0 before any write; PUT round trip bumps the revision; stale
  `baseRevision` → 409.

### T1.2 SPA: api, store, lookup (TDD)

- `lib/host-config-api.ts`: `QuickReply { id: string; text: string }`;
  `HostConfigPayload.quickReplies?: Versioned<QuickReply[]>` — **optional**,
  because an older daemon's GET has no such field;
  `HostConfigCollectionItems['quick-replies']`.
- `stores/useHostConfigStore.ts`: `HostConfigEntry.quickReplies` +
  `revisions.quickReplies`; the load patch (`:151-158`) keeps
  `quickRepliesSupported = payload.quickReplies !== undefined`;
  `saveQuickReplies`.
- `lib/quick-replies.ts` (new, mirrors `resume-templates.ts`):
  `DEFAULT_QUICK_REPLIES` (ids `continue`, `run-tests`, `explain` with the
  three texts from Q3); `effectiveQuickReplies(entry)` shows the defaults only
  when the host is *known* to hold none of its own, because a tap sends at
  once (Q1) and an emptied list means "no dock" (Q3) — a stray default
  `continue` after the user deleted it is one tap from being sent:
  - collection loaded once (`quickRepliesSupported`) → regardless of the
    current `status` (a failed or running reload keeps the last known copy):
    **revision 0 (never written)** → defaults, otherwise the stored items,
    **even when empty**;
  - old daemon (`status === 'unsupported'`, or `ready` without the
    collection) → defaults;
  - anything else (no entry, `idle`, `loading`, an `error` before any
    success) → `[]` (no dock). The settings tab lists nothing then either;
    the section's load notice explains.
  `useQuickReplies(hostId)` hook (calls `ensureLoaded`).
- Tests: `quick-replies.test.ts` — `defaults when never written`; `stored
  items win`; `an emptied list stays empty`; `error after ready keeps the
  stored list`; `error after ready keeps an emptied list empty`; `loading
  shows nothing`; `never-loaded error shows nothing`; `an old daemon still
  gets the defaults`. Store test: `loads quickReplies and its revision`;
  `marks the collection unsupported on an old daemon payload`.

### T1.3 SPA: the Hosts-page editor (TDD)

- `components/hosts/CommandsSection.tsx` gains a third tab `quick`
  ("Quick replies" / "快速回覆") beside `normal` / `resume`.
- `useHostConfigCollection.ts` today works only because each kind string
  equals its entry field (`entry[kind]`, `useHostConfigCollection.ts:36,45-54`)
  and save is a two-way ternary. Replace both with an explicit table
  `{ projects: { field: 'projects', save: saveProjects }, commands: …,
  'quick-replies': { field: 'quickReplies', save: saveQuickReplies } }` —
  `entry['quick-replies']` would silently read `undefined` (plan review #2).
  Existing projects/commands tests must stay green unchanged.
- `components/hosts/QuickReplySettings.tsx` (new), on
  `useHostConfigCollection` with kind `'quick-replies'`:
  list with add / edit (one text field) / delete / up-down reorder, saved
  through the existing host-config save queue. When the list was never
  written it shows the three defaults with a "defaults" note; the first save
  writes them for real. When the daemon lacks the collection: a muted
  "this host's daemon is too old for quick replies" line and no editor. The
  shared `useHostConfigGate` (`HostConfigNotice.tsx:16-31`) only knows the
  whole host config is `ready`, so this check reads the store's
  `quickRepliesSupported` in `QuickReplySettings` itself (plan review #3).
- Locale: `hosts.quick_replies.*` (tab label, add, empty, defaults note,
  unsupported note, text label).
- Tests: `lists the defaults when never written`; `adds, edits, reorders and
  deletes`; `saves through the queue with the base revision`; `shows the
  unsupported note on an old daemon`.

### T1.4 R3-A

Full gate (vitest / lint / tsc / build + `go test ./...` + `go vet ./...`).

---

## R3-B

### T2.1 the dock (TDD)

- `components/room/QuickReplyDock.tsx` (new): a single row of pill buttons
  (`data-testid="quick-reply"`), horizontally scrollable, above
  `WorkerInput` in **both** views (it is input, not the room dock of §4.6).
  Props `{ replies: QuickReply[]; onSend(text): void; disabled: boolean }`.
  Renders nothing for an empty list.
- `ExecutionView`: `useQuickReplies(hostId)`; `onSend` = `handleSend` with
  a new option `{ restoreDraft: false }`; `disabled` = the input's disabled
  expression (extract it to one const so both read it). The dock sits above
  the input in **both** views: it is input (§4.8), not the §4.6 persistent
  dock that spec §5 removes from chat, and chat is the phone view Q1 is for
  (plan review #5).
- **Q1 on the failure path** (plan review #1, critical): today a failed send
  calls `setDraft(text)` (`useExecutionActions.ts:87`), and `WorkerInput` is
  keyed on `draft` (`ExecutionView.tsx:228`), so it **remounts** with the
  failed text — a failed quick reply would wipe what was half-typed.
  `handleSend(text, opts?: { restoreDraft?: boolean })`, default `true`
  (typed sends keep today's recovery); the dock passes `false`, so a failed
  quick reply only shows the send error and the input is not touched.
- Tests: `renders a button per reply`; `renders nothing for an empty list`;
  `a tap sends the reply's text`; `is disabled while a send is pending / the
  worker ended`; `does not clear what is typed in the input`
  (ExecutionView); **`a failed quick reply leaves the half-typed input
  alone and shows the error`** (ExecutionView, with the send rejected);
  `a failed typed send still restores it as the draft` (unchanged
  behaviour, `useExecutionActions.test.ts`); `shows in chat as well as room`.

### T2.2 R3-B — full gate.

---

## R3-C

### T3.1 the search index (TDD, pure)

- `lib/nex/transcript-search.ts` (new):
  `buildSearchUnits(messages, index: OperationIndex, tools, view: 'room' | 'chat')`
  → `SearchUnit[]` in transcript order, each
  `{ id: string; text: string; reveal: string[] }` — `id` is the DOM anchor
  (`data-search-unit`), `reveal` the fold keys that must be expanded for the
  text to be rendered. Units: user lines; assistant prose; thinking text
  (room only — chat never shows thinking); a tool call's summary/argument and
  raw input (`reveal: [${k}:input]`); its result text (`reveal: [k]`); diff
  lines (`reveal: [${k}:diff]`); a subagent's own messages recursively
  (`reveal` gains the Task's `${k}:subagent`); in chat, every plain
  operation's units additionally need the turn's `chat-tools` key, edited ones
  `${k}:chat-edited`, failed ones `${k}:chat-failed` (use
  `classifyTurnOperations` so chat's grouping is the one it draws).
- `findMatches(units, query)` → `{ unitId, start, end, reveal }[]`,
  case-insensitive, literal (no regex), ignoring queries shorter than 2
  characters.
- Tests: `finds a match in folded tool output and lists the key that reveals
  it`; `a subagent match needs the Task's subagent key`; `chat reveals through
  the tools line`; `chat does not search thinking`; `room does`; `is case
  insensitive and literal` (`a.b` does not match `axb`); `ignores a one-letter
  query`; `keeps transcript order`.

### T3.2 revealing and marking (TDD)

- `fold-context.tsx`: `FoldStore.expand(keys: string[])` — sets each key
  expanded (never collapses), one state update.
- Every rendered text container that corresponds to a unit carries
  `data-search-unit={id}` (the same id scheme as T3.1 — define it in
  `transcript-search.ts` and import it where the components render).
- `lib/nex/search-highlight.ts` (new): given the scroll container, a unit id
  and a `[start, end)` range in that unit's **text**, locate the text nodes
  under `[data-search-unit=id]`, build DOM `Range`s and register them in
  `CSS.highlights` (`search-match`, `search-current`); feature-detected — where
  the API is missing (jsdom, old Safari) it only scrolls. Styles for
  `::highlight(search-match)` / `::highlight(search-current)` in the theme
  CSS with existing tokens. Rendered text can differ from the unit text
  (markdown); the locator searches the unit element's `textContent` for the
  query at the match's ordinal instead of trusting offsets — document this.
- Tests: `expand opens every key and collapses none`; highlight module with a
  stubbed `CSS.highlights`: `marks the current match and the rest`;
  `scrolls the current match into view`; `does nothing to highlights without
  the API`.

### T3.3 the search bar (TDD)

- `components/room/TranscriptSearch.tsx` (new): a slim bar at the top of the
  transcript area, inside the pane: input, `n / m` count, previous / next
  buttons, close. Enter = next, Shift+Enter = previous, Escape = close and
  clear highlights (focus returns to where it was).
- `ExecutionView` owns `searchOpen`. **No focusable pane root** (plan review
  #4: a click-to-focus root would fight the input's auto-focus, PR-6's header
  refocus and every fold button). Instead, while `isActive`, a `document`
  `keydown` listener opens the bar on Mod+F (`metaKey` on mac, `ctrlKey`
  elsewhere) with `preventDefault` (the web build would otherwise open the
  browser's find) — only when the event's target is `document.body` or inside
  this pane's root element, so another pane, a dialog, or Monaco keeps its own
  Mod+F. Assert: inactive pane ignores it; a target inside another pane
  ignores it; body and inside-pane targets open it.
- Moving to a match: `foldStore.expand(match.reveal)`, then after the render
  commits, highlight + scroll. Matches are recomputed when `messages`, `tools`,
  `view` or the query change; the current index is kept when possible.
- Both transcripts accept a `scrollRef` (forwarded) so the bar can reach the
  container.
- Locale: `room.search.placeholder`, `room.search.count`
  (`"{{current}} / {{total}}"`), `room.search.none`, `room.search.next`,
  `room.search.prev`, `room.search.close`.
- Tests (`TranscriptSearch.test.tsx`, `ExecutionView.test.tsx`): `Mod+F
  opens the bar and focuses the input`; `shows the match count`; `next
  expands a folded output that holds the match`; `wraps from the last match
  to the first`; `escape closes and clears`; `works in chat and reveals
  through the tools line`; `a new message keeps the current match`.

**Carried from the R3-C1 review (PR #1492, critic verdict: belongs to C2).**
R3-C1 changed the APIs this task uses: `findMatches(units, query, limit?)`
returns `{ matches, truncated }` (limit `SEARCH_MATCH_LIMIT` = 10,000);
`highlightSearch(owner, container, query, matches, current)` and
`clearSearchHighlights(owner)` take an owner — pass the pane id.

- **A4 auto-scroll.** The transcript's stick-to-bottom must not pull the
  reader away from a match. While the bar is open, a new commit follows the
  bottom only if the reader was already at the bottom before it; jumping to a
  match (which leaves the bottom) therefore stops following until the reader
  scrolls back down or closes the bar. Test: `a streaming message does not
  scroll away from the current match`.
- **A5 streaming partials.** Decision: partials are **not** indexed. They are
  not in `messages`, carry no block key (so no stable anchor — OperationBlock
  and RoomProse draw them without `searchUnit`), and their text changes every
  chunk. A message becomes searchable when it lands in `messages`. The
  current match is kept across that and every other recompute by identity —
  `(unitId, ordinal within the unit)` — not by list index; when that match no
  longer exists, the nearest following match (else the last) becomes current.
  Test: `the current match survives the stream ending` (partial ends → same
  match stays current and marked).
- **A8 re-applying marks.** Marks are Ranges over live text nodes and
  collapse when React replaces them. Re-run `highlightSearch` in a layout
  effect after every commit while the bar is open with a non-empty query
  (depends on `messages`, `tools`, `view`, fold state, query, current) — not
  only when the query or current changes. Test: `a mark survives a new
  message` (a new message re-renders; the current range is non-collapsed).
- **A10 query rules.** Normalise the query to NFC and trim it; an empty
  result searches nothing. Minimum length: **1 character if it contains a CJK
  character, else 2** (code points). Reason: user decision Q4 is that search
  finds everything, and a single Han character (`錯`, `檔`) already narrows a
  transcript as well as two Latin letters do, while a single Latin letter
  matches nearly every line. Unit text is normalised to NFC at index time so
  the offsets stay aligned with the query. `SEARCH_MIN_CHARS` and
  `searchPattern` move to these rules; tests: `a single Han character
  searches`, `a single Latin letter does not`, `whitespace-only searches
  nothing`, `NFD input matches NFC text`.
- **A11 cost.** `buildSearchUnits` is memoised on `messages`, `tools` and
  `view` only (never the query), so typing re-runs only `findMatches`; the
  prose units are already memoised per content (`proseText`). The count shows
  `10000+` when `truncated` (`room.search.count_more`, `"{{current}} /
  {{total}}+"`). Test: `shows 10000+ past the limit`.
- **Where a search starts (user decision, 2026-09-27; do not reopen).** Like
  a browser's find: when a query starts searching, the current match is the
  first one at or below the top of the visible area — the first unit whose
  bottom edge is below the scroll box's top is the anchor, and its own
  matches count — else it wraps to the very first match. Next goes down and
  wraps from the last to the first; a query refining one that had a match
  stays on that match (or the next). **Past `SEARCH_MATCH_LIMIT` the matches
  kept are the ones around the screen, not the oldest 10,000**, so the newest
  content stays reachable: `findMatches(units, query, limit, anchor)` keeps up
  to half the limit before the anchor unit and the rest from it on (a side
  with fewer gives its share to the other) and reports `truncatedBefore` /
  `truncatedAfter`; every match carries its `ordinal` within its unit, so a
  list that starts mid-unit still locates its matches. Stepping past an end
  of the kept list re-centres it (onwards when that side was cut, else round
  to the very first / last). Tests: `typing starts at the first match at or
  below the viewport`, `wraps to the first match when none is below`, `past
  the limit, keeps the matches around the viewport and can reach the newest`.

### T3.4 gates

R3-C1 = T3.1 + T3.2 (full gate), R3-C2 = T3.3 (full gate).

---

## After R3

- R4 after the §9 wire batch is sent to Nexen and shipped.
- P4 (permission prompts as tappable buttons) lands in the space the dock
  reserves (§4.8).

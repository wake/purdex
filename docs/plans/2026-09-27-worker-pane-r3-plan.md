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
| **R3-C** | transcript search | ~500 | R1 + attack + critic |

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
  three texts from Q3); `effectiveQuickReplies(entry)`: not loaded /
  unsupported / **revision 0 (never written)** → defaults; otherwise the stored
  items, **even when empty** (an emptied list means "no dock", Q3);
  `useQuickReplies(hostId)` hook (calls `ensureLoaded`).
- Tests: `quick-replies.test.ts` — `defaults when never written`; `defaults
  when the daemon has no collection`; `stored items win`; `an emptied list
  stays empty`. Store test: `loads quickReplies and its revision`;
  `marks the collection unsupported on an old daemon payload`.

### T1.3 SPA: the Hosts-page editor (TDD)

- `components/hosts/CommandsSection.tsx` gains a third tab `quick`
  ("Quick replies" / "快速回覆") beside `normal` / `resume`.
- `components/hosts/QuickReplySettings.tsx` (new), on
  `useHostConfigCollection` (extend `CollectionKind` with `'quick-replies'`):
  list with add / edit (one text field) / delete / up-down reorder, saved
  through the existing host-config save queue. When the list was never
  written it shows the three defaults with a "defaults" note; the first save
  writes them for real. When the daemon lacks the collection: a muted
  "this host's daemon is too old for quick replies" line and no editor.
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
- `ExecutionView`: `useQuickReplies(hostId)`; `onSend` = the same
  `handleSend` as the input (Q1: sends at once; the input's own value is
  untouched because it lives in `WorkerInput`); `disabled` = the input's
  disabled expression (extract it to one const so both read it).
- Tests: `renders a button per reply`; `renders nothing for an empty list`;
  `a tap sends the reply's text`; `is disabled while a send is pending / the
  worker ended`; `does not clear what is typed in the input`
  (ExecutionView); `shows in chat as well as room`.

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
- `ExecutionView` owns `searchOpen`; the pane root gets `tabIndex={-1}` so a
  click inside focuses it, and a `keydown` handler on the root opens the bar on
  Mod+F (`metaKey` on mac, `ctrlKey` elsewhere) with `preventDefault` (the web
  build would otherwise open the browser's find). It must not fire when the
  event comes from inside another editor-like element that handles Mod+F
  itself (none exists in the pane today — assert the handler only runs for
  events whose target is inside this pane).
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

### T3.4 R3-C — full gate.

---

## After R3

- R4 after the §9 wire batch is sent to Nexen and shipped.
- P4 (permission prompts as tappable buttons) lands in the space the dock
  reserves (§4.8).

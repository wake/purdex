# Workbook journal v4 — plan, Mac App (J-2a … J-2d)

Spec: `docs/specs/2026-10-14-workbook-journal-spec.md` (v4: §2 items 3, 4, 8–23, §5, §6, §7, §11, §12). Prototype the user approved: `docs/prototypes/workbook-journal-v4/` (layout, pinned band, 待確認 detail to the left, task hover link, names and the highlighted person). Daemon API: `docs/plans/2026-10-14-workbook-journal-plan.md` §8 (J-1e).
Facts read from origin/main on 2026-10-14 (paths from `spa/src/`); lead notes `journal-j2-spa-facts-2026-10-14.md`.

## 1. Facts

- **Title bar** `components/TitleBar.tsx:194-198`: the right group is `{team ? <TeamNotebookButton/> : own && <OwnNotebookButton/>}`, `<UnattendedButton/>`, the layout buttons. Both notebook buttons wrap the private `NotebookButton` (`team/TeamTitleBar.tsx:167-186`; Phosphor `Notebook`, `BUTTON` / `PRESSED` / `IDLE` from `components/title-bar-styles.ts`, `no-drag`, `keepFocus`). No 「團隊」 / 「日誌」 button exists; the team strip shows in `titlebar` mode (`TeamTitleBar.tsx:27`).
- **One panel area today**: `App.tsx:251` `<TeamPanelArea/>` — one absolute rectangle anchored top-right (`team/TeamPanelArea.tsx`), left-edge resize via `RegionResize` (draft → commit), modes `titlebar | line | full | max` per team (`stores/useTeamUiStore.ts`, persisted `purdex-team-ui`; width bounds `currentPanelMin()` 356 / 412 … `PANEL_MAX_WIDTH` 720); `team/panel-view.ts` `panelView()` picks team view / drilled seat workbook / own workbook. No multi-card concept.
- **Workbook UI** mounted in: `TeamPanelArea` (`OwnWorkbookPanel`), `TeamPanel.tsx:56` (`TeamSeatWorkbookView`), `TitleBar` (`OwnTitleStrip`, `OwnNotebookButton`), `TeamPanelRow.tsx:33` (`useSeatWorkbook` status line), `TeamEndedGroup.tsx:14` (support). Stores / libs: `useWorkbookStore`, `lib/workbook/*` (loader started in `main.tsx`), `useWorkbookViewState`, `lib/workbook/view-memory.ts`.
- **Capabilities**: `lib/team/unattended-support.ts` `probe(hostId)` (`:31`) reads `/api/info` capabilities and sets flags (`:43-52`); disconnect invalidates (`:78-90`); re-point / removal forgets (`:67-77`).
- **Host events**: `lib/host-events.ts` union (`'workbook.entry'` `:23`); `hooks/useMultiHostEventWs.ts:309-316` routes with the `connectionKey` guard; template `lib/workbook/workbook-ws.ts` (events write the store, never fetch).
- **REST**: `lib/team/approval-api.ts` `send<T>(hostId, path, init)` (`:72`, pinned host, `ApprovalApiError` codes); template `lib/workbook/api.ts` + `parse.ts`.
- **Tab-hosted rule** (`CLAUDE.md` 「Tab-hosted 元件檢查清單」): state outside the component; template `lib/workbook/view-memory.ts`; regression pattern `components/team/TeamSeatWorkbookView.tab-switch.test.tsx` with the real `TabContent`.
- **Active tab → session / team**: `useTabStore` layout → `team/own-workbook.ts:19` `ownSessionOf` (first `cc` tmux leaf → `{hostId, sessionId}`); `useTeamDisplay().panelTeam(activeTabId)`.
- **Colours**: `--color-status-warning` / `-text` (`styles/themes.css:26-27` + per theme) for the 待確認 yellow; no shared chip component (inline spans: `MoreChip`, `ApprovalPill`).
- **i18n**: `locales/en.json`, `zh-TW.json`, `locale-completeness.test.ts`.

## 2. Design choices (for the reviewer)

- **A card stack replaces the single panel area.** A new `components/journal/CardStack.tsx` mounted where `TeamPanelArea` is: columns right-aligned under the title bar — `[team card + roadmap under it] [journal card] [tasks tab]` (spec §11; roadmap alone on the left when there is no team). The team card keeps today's `TeamPanel` content and modes; `TeamPanelArea` stays for hosts without `journal.v1` (no change there).
- **On a `journal.v1` host the workbook view is gone** (spec §12): the notebook buttons give way to 「團隊」 / 「日誌」, `panelView()`'s workbook branches and the team row's drill button are skipped; on older hosts everything stays as it is. The team row's status line stays (it is the team panel's, not the workbook view; spec v4 does not remove it).
- **Data**: a `useJournalStore` per host + unit (entries by month pages, pinned, open confirm count, tasks, roadmap, the person's name), loaded through `lib/journal/api.ts` and refreshed on `journal.changed` (re-read what is shown; the event carries no entries). Entries sorted by (`at`, `id`) client-side too (J-1e sorts; the client never trusts file order).
- **View state outside components** (`lib/journal/view-memory.ts`, per unit): journal open, roadmap open, tasks open, widths, filter, scroll position / pinned-to-bottom, opened 待確認 detail.
- **The person's name** comes from the API (`person_name`, J-1e) and is highlighted with the warning-text colour on a soft warning fill wherever it appears in an actor or a text (spec item 23).

## 3. Phases

| PR | content | depends on | est. lines |
|---|---|---|---|
| J-2a | data layer: `journal.v1` probe, `lib/journal/{types,parse,api,journal-ws}.ts`, `useJournalStore`, `journal.changed` branch, view memory | J-1e API contract (fixtures until it lands) | ~650 |
| J-2b | title-bar 「團隊」 / 「日誌」 buttons, `CardStack` layout (columns, widths, narrow-window drop order), `useJournalUiStore`, workbook view hidden on `journal.v1` hosts | J-2a | ~700 |
| J-2c | journal card: header, filters (active one clicked again clears), stream with date dividers and month paging, rows (kind chips, actor, refs, person highlight), pinned band, 待確認 checklist + detail to the left, pin / unpin, open-at-bottom + 「↓ N 則新事件」, the 日誌 button's number | J-2b | ~750 |
| J-2d | tasks tab (hover link both ways), roadmap tab (per-feature tables, status colours), tab-switch regression test, i18n completeness | J-2c | ~550 |

Every PR: TDD, one commit per task; affected vitest files only while developing; **before merge** full vitest (`--maxWorkers=3`) on a freshly fetched origin/main + `tsc -b --force` + `pnpm run lint`; each test group names a mutation. codex R1 → R2 attacker → critic.

## 4. J-2a — data layer

- **T-2a.1** `JOURNAL_CAPABILITY = 'journal.v1'`; `useJournalStore.setSupport` called beside the workbook probe (`unattended-support.ts:52`), invalidated on disconnect (`:81`), forgotten on re-point / removal (`:67-77`). Tests: probe yes / no / failed → unknown; disconnect → unknown (M: no invalidate).
- **T-2a.2** `lib/journal/types.ts` + `parse.ts`: the J-1e shapes (`unit{kind,id,label}`, `project`, `person_name`, `entries[]` with `id, kind, by, at, actor, text, refs, items[{text,done}], detail, pinned`, `pinned[]`, `more`, `open_confirms`; tasks; roadmap features); strict parse, unknown kinds dropped with one warning. Tests: every kind, malformed rows (M: lenient parse).
- **T-2a.3** `lib/journal/api.ts` on `send`: `getJournal(hostId, sessionId, before?)`, `getTasks`, `getRoadmap`, `setPinned`; 404 → `{kind: 'none'}`, 409 `lead_host` → `{kind: 'remote', leadHost}` (the store then reads the lead's host). Tests per outcome.
- **T-2a.4** `useJournalStore` (per `hostId\0unitKey`: month pages merged and sorted by (`at`, `id`), pinned, open count, tasks, roadmap, person name, load generation) + `lib/journal/journal-ws.ts` (`journal.changed` → mark the unit stale; the shown unit re-reads, debounced 300 ms) + the `useMultiHostEventWs` branch. Tests: out-of-order pages sort (M: keep file order), a stale event re-reads once for bursts (M: no debounce), a forgotten host drops its units.
- **T-2a.5** `lib/journal/view-memory.ts` (per unit, capped like `view-memory.ts`).

## 5. J-2b — buttons and the card stack

- **T-2b.1** Title bar on a `journal.v1` host: 「團隊」 (`UsersThree`, team tabs only) and 「日誌」 (`Notebook`, any tab with a `cc` session) replace the notebook buttons; the 日誌 button carries the open 待確認 item count (Q-B), nothing at 0. Older hosts unchanged. Tests: both host kinds, the count (M: show on 0).
- **T-2b.2** `CardStack` (replaces `TeamPanelArea` on `journal.v1` hosts): columns right-aligned under the title bar in the order team-column (team card, roadmap under it) · journal · tasks; each column its own width (journal and tasks resizable with `RegionResize`, bounds as the panel's); opening one pushes the others left (CSS transition). A window narrower than the open columns drops, in order: tasks, roadmap, team card (the journal stays). Tests: order, push, drop order at 3 widths (M: drop the journal first).
- **T-2b.3** `useJournalUiStore` (persisted UI prefs: widths) + the view memory (per unit open states). Tests: tab switch keeps the open states (real `TabContent`).
- **T-2b.4** Workbook view off on `journal.v1` hosts: `panelView()` skips the workbook branches, the team row drill button hidden, `OwnTitleStrip` not shown; `/workbook refresh` unaffected. Tests: both host kinds (M: always hide).

## 6. J-2c — the journal card

- Header (unit label, role line, live dot, roadmap / tasks toggles, close); filters 全部 / 執行 / 任務 / 請求與溝通 / 待確認 (count on 待確認; the active one clicked again → 全部); pinned band (newest first; a 待確認 shows 「已答 n／m 項」); the stream oldest → newest with date dividers, opened scrolled to the bottom, pinned there while at the bottom, else 「↓ N 則新事件」; scrolling to the top loads the previous month (`before`).
- Row: time, kind chip (the prototype's colours; 待確認 the warning colour; no coloured left edge), actor (`A → B`), text, refs as links (`#N` → the GitHub issue / PR URL of the project's remote when known, else plain), **the person's name highlighted** in actor and text; a hover action pins / unpins (PUT, optimistic, reverted on error with a toast).
- 待確認 row: its checklist (read-only), clicking opens the detail panel to the left of the journal card over the roadmap side (original text in a scrollable block); one open at a time; Esc closes.
- Tests: filter toggle-off (M: stays), bottom pinning vs the pill, month paging keeps the reading position, highlight wraps every occurrence, pin revert on error, detail open / close, the button count follows ticks.

## 7. J-2d — tasks and roadmap tabs

- Tasks tab: open tasks first, then done, dropped struck through (solo); id / number, title, owner (display name), refs; hovering a task_new / task_done row highlights its task and vice versa (shared hover state in the card stack).
- Roadmap tab: per feature a table 階段 / 說明 / 狀態, status colours (未開始 grey, 進行中 blue, 審查中 purple, 完成 green, 暫停 orange); live via `journal.changed`.
- The tab-switch regression test for the whole stack (open states, filter, scroll, opened detail) with the real `TabContent`; i18n keys under `journal.*` in both locales.

## 8. Not in J-2

iOS (J-3); the employee workbook; editing entries (pin only).

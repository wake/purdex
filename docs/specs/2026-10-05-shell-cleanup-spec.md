# Shell cleanup — spec v1.1 (2026-10-05)

v1.1 applies the codex plan review (job `task-muv5ptkz-8typid`): it fixes §4.2 (DOM structure), §4.6 (measured right placement), §5 (popup removal end to end), §8.1/§8.2 (subscription cleanup, activation-only focus), §9.4 (mode on the recovery path). It also adds the user's rule D.1a for which panes survive a layout change.

Worktree `shell-cleanup`, branch `worktree-shell-cleanup`, base origin/main @ alpha.475.

This spec covers six user requests (A–F) for the app shell: the left activity bar, the title bar, the status bar, and how panes take focus. All user-visible rules in §1 were decided by the user on 2026-10-05. Do not reopen them in review; flag a rule only when it cannot be implemented as written.

## 1. Decisions (user-confirmed, do not reopen)

| # | Rule |
|---|---|
| A | Remove the 4-region sidebar system entirely: the four `SidebarRegion`s, the 4 region toggles in the title bar, the region manage / context menus, the module `views` registry, the `regions` field of `useLayoutStore`, and the Settings → Interface "Pane" / "Sidebar" coming-soon stubs. It is unused and will not come back. |
| B | The left activity bar gets a **Workers** button in its bottom group. When on, the wide bar shows the worker list **below** the workspace list. Both stay visible; a draggable horizontal divider sets the list's height, and the height is remembered. Click again to hide the list. |
| B.1 | The list shows **every host shown in this workbench**, one section per host. The section header is the host name plus its Nexen status dot. Hosts whose Nexen phase is `disabled` get no section at all. |
| B.2 | Clicking a worker row: if a tab already shows that worker, switch to that tab (and its workspace). Otherwise open it as a new tab in the **current** workspace. Today it lands in "Unsorted"; that is a bug this spec fixes. |
| B.3 | In the narrow bar, the Workers button opens the same list in a floating panel beside the bar. It does not change the narrow/wide setting. |
| C | The bottom button group of the wide bar can switch between **rows** (today's look: one button per row with a label) and **compact** (one horizontal row of icon-only buttons). The switch is a small toggle button on the group itself. The choice is remembered. Narrow bar: unchanged (already icon-only). |
| D.1 | The three layout buttons (single / split left-right / split top-bottom) stay in the title bar only. Their meaning is **"change this tab to this layout"**. The button matching the current layout is highlighted. If applying it would close a pane that has content, ask first. |
| D.1a | Which panes the new layout keeps (user, 2026-10-05: "如果只有一個 agent，留他；否則彈出視窗問使用者"). The layout has k slots (1 for single, 2 for a split). If the non-blank panes fit in k, keep them all and ask nothing. Else if the tab has **exactly k agent panes**, keep those; the confirm from D.1 still lists what closes. Else open a dialog that lets the user **pick which k panes to keep**; that dialog is also the confirmation. |
| D.2 | The title bar exists only in Electron. In a plain browser the layout buttons are gone and splitting is right-click only. Accepted. |
| D.3 | The status bar loses its two split buttons. Instead it shows three buttons, **terminal / worker / chat**, that show and switch the mode of the pane the status bar is showing (see D.4). |
| D.4 | The **whole** status bar shows one pane of the active tab, chosen like this: (1) the most recently focused **agent pane**; (2) else, if the tab has any agent pane, the first one in layout order; (3) else the most recently focused pane; (4) else the primary pane. An agent pane is a non-terminated `tmux-session` pane whose session has a detected agent type (Claude Code, Codex, …), or any `execution` (worker) pane. Clicking a plain terminal or an editor therefore does not move the status bar away from an agent pane. |
| E | Delete the workspace file tree (both views) and the workspace "project path" setting (`moduleConfig.files.projectPath`). Host → Projects replaces it; the two share no data. |
| F | When a tab is shown, focus goes to that tab's most recently focused pane; with no record (new tab, or the pane is gone), to its primary pane. The record lives in memory only; after a reload every tab starts from its primary pane. |

## 2. Non-goals

- No change to Profile Sync projections. Every new layout field is device-local.
- No change to the daemon or Electron main/preload.
- No new pane kinds, and no change to the `execution` content's `mode` model: terminal stays outside the `room | chat` enum (decision recorded in the 2026-09-20 worker pane views spec). The status bar shows the three as one row, but terminal↔worker is still a rebind through Hand to Nex / Take to terminal.
- No keyboard shortcuts for the new buttons.

## 3. Phases

One phase = one PR. Size limit per PR: ≤ 800 changed lines **or** ≤ 20 files. Phases ship in this order: P1 → P2 → P3 → P4 → P5 → P6 → P7. P1 comes first so the worker list has its new home before the region system goes away. P5 comes before P6 and P7 because both read its focus record.

| Phase | Content | Estimated size |
|---|---|---|
| P1 | B + C: worker list in the activity bar, compact bottom group | ~600 lines / ~14 files |
| P2 | E: file trees, the `files` module, the project-path setting | ~780 lines / 13 files |
| P3 | A-1: unmount the regions, delete the region UI, drop the title-bar region toggles | ~900 lines / 9 files (deletion-heavy) |
| P4 | A-2: region state in the layout store, the `views` registry, the Interface stubs, locale keys | ~620 lines / 15 files |
| P5 | Pane focus record + F | ~450 lines / ~12 files |
| P6 | D.3 + D.4: status bar target pane and mode buttons | ~650 lines / ~12 files |
| P7 | D.1: title bar layout buttons (highlight + confirm) | ~300 lines / ~6 files |

---

## 4. P1 — Worker list and compact bottom group (B, C)

### 4.1 Layout store (device-local)

Add these fields to `useLayoutStore` and include them in `partialize`. They sync across windows on the same device (like `activityBarWideSize`), never across devices (the layout store is projected to Profile Sync only for `tabPosition`, `lib/profile/projections.ts:72-73`).

| Field | Type | Default | Notes |
|---|---|---|---|
| `workerListOpen` | `boolean` | `false` | Shared by the wide-bar section. The narrow-bar panel is transient component state, not this field. |
| `workerListHeight` | `number` | `240` | Clamped to `[WORKER_LIST_MIN, WORKER_LIST_MAX]` = `[96, 800]` on write. |
| `bottomNavCompact` | `boolean` | `false` | |

Setters: `setWorkerListOpen`, `toggleWorkerListOpen`, `setWorkerListHeight` (clamps), `setBottomNavCompact`, `toggleBottomNavCompact`. No `version` bump: the store has no `migrate`, and missing fields take their defaults.

### 4.2 Wide bar layout

Today's skeleton (`ActivityBarWide.tsx:271-344`): Home row, separator, the workspace scroll zone (`flex-1`), then the bottom group. New skeleton when `workerListOpen`:

```
Home row
separator
workspace scroll zone      (flex-1, min height WORKSPACE_ZONE_MIN = 96)
row divider                (drag to resize; dragging up grows the list)
worker list section        (height = rendered list height, own scroll)
bottom group
```

- The rendered list height is `min(workerListHeight, available − WORKSPACE_ZONE_MIN − dividerHeight)`. Here `available` is the height of the split box (the workspace zone, the divider and the list). A short window only caps the rendered height and **never writes** the stored value; when the window grows again, the list returns to the stored height.
- Dragging keeps a draft height in local state and commits to the store on mouseup, the same pattern as the bar's width resize (`ActivityBarWide.tsx:100-104, 346-369`), so a drag does not write the store on every mousemove.
- A drag starts from the **rendered** height, and its commit stores what the user sees at mouseup. So a drag inside a capped (short) window does replace the stored value: the user explicitly set that height. This is a coordinator decision from the P1 review (critic drift finding, 2026-10-05). The alternative, a drag that starts from the uncapped stored value, would leave the divider unresponsive under the cursor. Window blur ends a drag like a mouseup; an unmount mid-drag cleans up without committing. When the cap is below `WORKER_LIST_MIN` (a very short window), the divider does nothing: no draft is created and nothing is stored, so the stored value can never disagree with what was on screen.
- Divider: reuse `PaneSplitter` with `direction='v'` (`components/PaneSplitter.tsx`), adding an optional `onResizeEnd` and a `testId` prop. Do not write a new splitter.
- DOM structure. `DndContext` renders no DOM of its own, but Home, the separator and the workspace zone are its JSX children (`ActivityBarWide.tsx:276-317`). Measuring `available` needs one element that holds exactly the workspace zone and the list. So a new `flex-1 min-h-0 flex flex-col` wrapper (the "split box") goes after the separator. It holds the workspace scroll zone, the divider and the list section, and a ResizeObserver on it gives `available`. The split box may stay inside `DndContext`: the list registers no draggables or droppables, and `restrictWorkspaceDrag` still clamps to `wsScrollRef` (the workspace zone only). Workspace and tab drag-and-drop must behave exactly as before; the P1 tests include a regression test for that.
- When `workerListOpen` is false, nothing of the list is mounted. That matters because each host section subscribes to that host's SSE stream while mounted (`hooks/useHostExecutions.ts`).

### 4.3 Worker list content (B.1)

New component `WorkerList` (`components/executions/WorkerList.tsx`):

- It iterates `useHostStore.hostOrder`, keeps the hosts that are shown in this workbench (the `lib/shown-hosts` filter; add `WorkerList.tsx` to the allowlist in `lib/shown-hosts.import-guard.test.ts`), and renders one `ExecutionsView` per host.
- A host whose Nexen phase is `disabled` renders nothing. Every other phase (`loading`, `unknown`, `ready`, error/unavailable) renders its section, so the user can see a host that is still connecting or has failed.
- `ExecutionsView` keeps its header (status dot + host name) and its states. Its `ViewProps` typing becomes a local props type in P4, not here.
- With no visible section at all, the list shows one empty line: `workers.list.none` ("No hosts with Nexen" / 「沒有開啟 Nexen 的主機」).

### 4.4 Opening a worker (B.2)

Replace the row click in `ExecutionsView.tsx:55-58` with a shared helper, `openWorkerTab(content)`:

1. If an existing tab already holds this execution pane (the same lookup `openSingletonTab` does), select that tab. If the tab belongs to a workspace, make that workspace active (`handleSelectTab` semantics, `features/workspace/hooks.ts:106-115`). Do **not** move it.
2. Otherwise `openSingletonTab(content)`, then `insertTab(tabId)` with no workspace id. That targets the active workspace, or, when Home is active, the same fallback a new tab uses (`features/workspace/store.ts:187-195`). Then select it.

`insertTab` moves a tab that is already in another workspace (singleton dedup, `store.ts:222-228`). Step 1 must run before it, so an open worker is never moved. Hidden-host rows keep today's behavior: no click action.

### 4.5 Bottom group (B, C)

The bottom group gains a fourth entry, **Workers** (icon `Lightning`, the icon the Executions view already uses; label `nav.workers`). Order: New workspace, Workers, Hosts, Settings. Workers is a toggle: `aria-pressed`, highlighted with the same active style as the title bar's region toggles today (`text-accent-base bg-accent-base/10`).

Wide bar, rows mode (`bottomNavCompact=false`): today's rows plus Workers. A small icon button (`CaretDown`, `title=nav.bottom_compact`) sits at the right end of the first row. It switches to compact.

Wide bar, compact mode: one horizontal row of 30×30 icon buttons, left to right: New workspace, Workers, Hosts, Settings, then the toggle back (`CaretUp`, `title=nav.bottom_rows`). Every button keeps its `title` so it has a tooltip and an accessible name.

### 4.6 Narrow bar (B.3)

The narrow bottom group gains the Workers icon button. It opens `WorkerList` in a `FloatingPanel`:

- Add an optional `placement` prop to `FloatingPanel`: `'below'` (default, today's behavior) or `'right'`. `'right'` places the panel at `anchor.right + 4`, with the panel's **bottom edge aligned to the anchor's bottom edge**, clamped to the viewport.
- The panel's height comes from its content (it only has a `maxHeight`), so `'right'` cannot compute `top` from a constant. It positions in a layout effect **after** render, from the panel's measured height: `top = anchor.bottom − panelHeight`, clamped to `[topInset, innerHeight − PADDING − panelHeight]`. While the panel has not been dragged, a ResizeObserver on the panel re-runs the placement when its content height changes (rows loading in).
- The panel body is bounded by `maxHeight = innerHeight − topInset − PADDING` and scrolls inside.
- Clicking the button again, Escape, or a click outside closes it. These are FloatingPanel's existing close paths.

### 4.7 Locale keys (en / zh-TW)

`nav.workers` (Workers / Workers), `nav.bottom_compact` (Show as one row / 收合成一排), `nav.bottom_rows` (Show as list / 展開成清單), `workers.list.none`. Both files, per `locale-completeness.test.ts`.

### 4.8 Tests (P1)

- Store: defaults, clamp, setters, `partialize` includes the three fields, and Profile Sync projection unchanged (the projection test still sees only `tabPosition`).
- Wide bar: the list is absent when closed; open → list mounted below the workspace zone and above the bottom group. Divider drag commits once on mouseup with the clamped value, and dragging up grows the list. Render-time cap when the bar is short.
- `WorkerList`: shown hosts only, in `hostOrder`; a `disabled` host has no section; a `loading` host has one; the none line.
- `openWorkerTab`: existing tab in another workspace → selected, workspace switched, **not moved**; new → inserted into the active workspace; Home active → the new-tab fallback. Regression test: the tab does not end up in Unsorted when a workspace is active.
- Bottom group: four entries; the toggle switches modes and persists; compact renders icon buttons with titles; Workers `aria-pressed` follows the store.
- Wide bar regression: workspace reorder by drag and tab drag into another workspace still work with the list open.
- Narrow: the Workers button opens a right-placed FloatingPanel; a second click closes it. FloatingPanel `placement='right'` geometry: `panel.bottom === anchor.bottom` for a short panel; clamped when the anchor is near the top; re-placed when the content height changes.

---

## 5. P2 — File trees and the `files` module (E)

Delete:

| File | |
|---|---|
| `components/FileTreeView.tsx` + test | workspace tree |
| `components/FileTreeSessionView.tsx` + test | empty stub |
| `components/settings/FilesWorkspaceSettingsSection.tsx` + test | project path setting |

Edit:

- `lib/register-modules/index.tsx`: remove the whole `files` `registerModule` (`:286-328`). After its views and settings go, nothing is left in it (no panes, openers, or new-tab providers), and nothing calls `isEnabled('files')`. A stale persisted `enabled.files` is harmless (`useModuleEnabledStore.ts:17-24`). Remove the now-unused imports (`FolderOpen`, the two file-tree views, `FilesWorkspaceSettingsSection`). Keep `PlaceholderSettingsSection` (the browser module uses it).
- `lib/settings-order.ts`: remove `MODULE_FILES` and `WORKSPACE_FILES` and their doc rows.
- The file-not-found popup's "search the workspace" action reads `moduleConfig.files.projectPath`. After E nothing can set it, its tooltip points at a deleted setting, and the daemon answers that search with 501 (`internal/module/fs/search_handler.go:173-175`). Remove the whole feature end to end:
  - `lib/register-modules/file-open-bootstrap.ts`: `resolveProjectPath` (`:183-188`), and the file-tree-only service (`tryOpenFileForFileTree`, `_fileTreeService`, `getFileTreeService`).
  - `lib/file-open/file-not-found-popup-service.tsx`: the `projectPath` / `onSearchWorkspace` members of `ShowCallbacks` (`:20`) and their pass-through in **both** render paths (`:51` and the other one).
  - `components/editor/popups/FileNotFoundPopup.tsx`: the `projectPath` / `onSearchWorkspace` props, the action button (`:129-139`), and the expanded mode's workspace-results section (`:100`).
  - Their tests: `file-open-bootstrap.test.ts` (retarget to `tryOpenFileForTerminalLink`), the popup-service test, and the popup test.

  After P2, `grep -rn "projectPath\|onSearchWorkspace\|searchWorkspace" spa/src` returns nothing. If this pushes P2 over the size limit, move it to its own PR right after P2; do not leave it as an issue.
- Locale keys removed from both files: `file_tree.*` (3), `settings.files.project_path.label`, `settings.section.files_workspace`, `settings.section.files`, `modules.files.description`, plus any key the popup action used only for itself. **Keep** `sidebar.view.files_workspace` until P4 (a SidebarRegion test fixture still reads it).
- Tests: `register-modules.test.ts` (Files describe blocks, T5, the `'settings.section.files'` entry), `settings-order-pr2.test.ts` (`'files'`), `WorkspaceSettingsPage.registry.test.tsx` (Files describe). Retarget the "disabled module hides its workspace-scope setting" coverage (SR-2) to the editor module's `workspace-home-path` setting (`editor-module.tsx:44-45`) instead of losing it.
- `moduleConfig` itself stays (general purpose, synced opaquely).

---

## 6. P3 — Unmount the regions (A-1)

- `App.tsx`: remove the four `<SidebarRegion>` elements (`:217, 233, 242, 250`) and the import. The main area's flex structure must stay the same without them: the activity bar, then the column of tab bar, content, and status bar.
- `components/TitleBar.tsx`: remove `regionToggles`, the `regions` / `toggleVisibility` selectors, the toggle buttons and the divider after them. The `layout-buttons` cluster keeps only the three pattern buttons (P7 changes them).
- Delete `components/SidebarRegion.tsx`, `RegionManager.tsx`, `RegionContextMenu.tsx` and their tests. **Keep** `RegionResize.tsx` (the wide bar's width handle).
- `TitleBar.test.tsx`: the button count goes from 7 to 3; indices shift.

P3 is almost all deletion. Its review checks that nothing still imports the deleted files and that the shell looks the same with all regions collapsed (they were 24-px bars or hidden).

---

## 7. P4 — Region state and the views registry (A-2)

- `useLayoutStore`: remove `RegionState`, `regions`, the region actions (`setRegionMode` … `reconcileViews`), `createDefaultRegions`, `updateRegion`, the `getAllViews` import, and `regions` in `partialize`. Keep `MIN_WIDTH` / `MAX_WIDTH` / `clampWidth`. An old persisted `regions` key merges in as an unused key and is dropped on the next write; no `version` bump.
- `main.tsx`: remove the `reconcileViews()` call and its import.
- Delete `types/layout.ts` (it holds only `SidebarRegion`) and the type's re-export from `lib/module-registry.ts`.
- `lib/module-registry.ts`: remove `ViewProps`, `ViewDefinition`, `ModuleDefinition.views`, `getViewDefinition`, `viewLabel`, `getAllViews`.
- `lib/register-modules/index.tsx`: remove the execution module's `views:` entry and the Interface "Pane" / "Sidebar" stub sub-sections (`:357-372`). "New Tab" becomes the only Interface sub-tab; `InterfaceSubNav` must still render correctly with one entry.
- `ExecutionsView`: replace `ViewProps` with a local `{ hostId?: string; isActive?: boolean }`.
- Locale keys removed: `sidebar.manage_views`, `sidebar.add_views`, `sidebar.no_views_available`, `sidebar.section_enabled`, `sidebar.section_available`, `sidebar.view.executions`, `sidebar.view.files_workspace`, `sidebar.view.files_session`, `settings.interface.pane`, `settings.interface.sidebar`. Keep `settings.coming_soon` (still used).
- Tests: `useLayoutStore.test.ts` region blocks, `types/tab.test.ts` `SidebarRegion` block, `orchestrator.test.tsx` view registration block, `collector.test.ts:229-230` (no longer compiles), `module-registry.test.ts` views fixtures and describes, `register-modules.test.ts:186-194` (Interface subs become `['new-tab']`).
- Stale comments that name the deleted components (`StoragePane.tsx:575`, `CostPanel.tsx:89`, `file-open-bootstrap.ts`, `open-file.ts:109`, `file-not-found-popup-service.tsx:9`) are reworded.

After P4, `grep -rn "SidebarRegion\|getAllViews\|ViewDefinition\|reconcileViews\|primary-panel" spa/src` returns nothing.

---

## 8. P5 — Pane focus record and focus on tab switch (F)

### 8.1 The record

New non-persisted store `usePaneFocusStore` (`stores/usePaneFocusStore.ts`, not registered with `syncManager`):

```ts
recent: Record<tabId, paneId[]>   // most recent first, no duplicates, capped at 16
touch(tabId, paneId): void        // move to front
forgetTab(tabId): void
```

Plus pure helpers in `lib/pane-focus.ts` that read a tab's layout:

- `focusTargetOf(tab, recent)`: the first id in `recent[tab.id]` that is still a live leaf of `tab.layout`, else `getPrimaryPane(tab.layout).id`. This is rule F.
- `statusTargetOf(tab, recent, isAgentPane)` implements rule D.4 in P6. It lives here so P6 and P7 share one source of truth.

Writers:

- `PaneLayoutRenderer` leaf wrappers (both the header and no-header variants, `:203` and `:238`) get `onPointerDownCapture` and `onFocusCapture` handlers that call `touch(tabId, paneId)`.
- Programmatic focus fired by the F logic below lands on the same pane `focusTargetOf` picked, so `onFocusCapture` writing it is idempotent. Auto-focus elsewhere must be gated (§8.2) so it never writes a pane the user did not choose.
- Cleanup is a **subscription**, not a hook in `closeTab`. The tab world can be replaced wholesale (cross-window rehydrate, Profile Sync apply, standalone adoption) without going through `closeTab`. `usePaneFocusStore` therefore subscribes to `useTabStore` and drops `recent[id]` for every tab id that is no longer in `tabs`. That is the only cleanup point. Dead pane ids inside a live tab are tolerated: every reader filters against live leaves.

### 8.2 Who may grab focus

Add `isFocusTarget: boolean` to `PaneRendererProps` (`lib/module-registry.ts:20-23`) **next to** `isActive`, which keeps its current meaning (the tab is visible). `isFocusTarget` = `pane.id === focusTargetOf(tab, recent)`, independent of `isActive`.

A single boolean cannot tell "the tab just became visible" from "the user clicked another pane in a visible tab", so each site keeps both props and applies one rule:

> Programmatic focus happens only at **activation**: on the `isActive` false→true transition, or on first mount with `isActive` true. At that moment the pane focuses iff `isFocusTarget` is true. When the focus is deferred to the next frame, the frame re-checks that the tab is still active and the pane still the target (P5 review A1). A change of `isFocusTarget` while `isActive` stays true never calls `focus()`; the user's click already put focus there.

| Site | Today | After |
|---|---|---|
| `TerminalView.tsx:128-141` | focus when `visible` turns true | same trigger, plus `&& isFocusTarget` read at that moment |
| `useTerminalWs.ts:81-86` `reveal()` | always `term.focus()` | reveal is a first-mount activation path: focus only if, at reveal time, the tab is active **and** the pane is its focus target (both read through refs, the way `onReadyRef` is) |
| `WorkerInput.tsx:70-76` | `focused` = `isActive`; also refocuses when `disabled` flips back to false | activation rule above. The post-send refocus stays, but it is driven by an explicit `pendingSend` signal from `ExecutionView` (true→false = this pane's own send completed), gated by `isActive && isFocusTarget`. The aggregated `disabled` also covers stream loss, history load, encoding and take-back, so its transitions never focus (P5 review A2). One exception keeps rule F whole: an activation that lands while the input is disabled (for example, history still loading) leaves a pending focus. It is fulfilled the first time the input becomes enabled, provided the tab is still active, the pane is still the target, and the user is not typing elsewhere. Deactivation or losing the target cancels it. |
| `MonacoWrapper.tsx:65-67, 86-89` | `isActive` | activation rule; `isActive` alone for everything that is not focus |
| `TiptapEditor.tsx:102, 120-123` | `isActive` | same as Monaco |

Panes with no activation focus today (new-tab, settings, hosts, browser, …) are unchanged.

So when a tab becomes visible, or freshly mounts as the active tab, exactly one pane focuses: the recorded one, else the primary. A click inside a pane still focuses that pane natively and records it.

### 8.3 Tests (P5)

- Store: touch order, dedup, cap; the subscription drops entries when tabs disappear, including a wholesale `useTabStore.setState` replacement.
- `focusTargetOf`: recorded live pane; recorded pane gone → primary; no record → primary.
- Integration (jsdom; `inert` is not implemented, so assert on calls, not on `document.activeElement` across tabs). Three timing paths, each a named test:
  1. **keep-alive reactivation**: a split tab with terminals left and right. Click left, switch to another tab, switch back → only the left pane's focus is called. No record → the primary pane's.
  2. **first mount as the active tab** (the tab was not alive): only the target focuses, including through `reveal()`.
  3. **click inside a visible tab**: no programmatic `focus()` call from any site.
- A worker + editor split: only the target's focus fires. A worker that finishes sending while it is not the target does not take focus.

---

## 9. P6 — Status bar target and mode buttons (D.3, D.4)

### 9.1 Target pane

`StatusBar` reads `statusTargetOf(activeTab, recent, isAgentPane)` instead of `getPrimaryPane`. `isAgentPane(content)`:

- `execution` → true.
- `tmux-session` → true when not `terminated` and `useAgentStore.agentTypes[compositeKey(hostId, sessionCode)]` is set.
- anything else → false.

Rule D.4, in order:

1. The first id in `recent[tab.id]` that is a live leaf **and** an agent pane.
2. Else the first agent pane in layout order (`collectLeaves`).
3. Else the first id in `recent[tab.id]` that is a live leaf.
4. Else the primary pane.

What the bar shows for the target is unchanged per kind: the tmux full bar, an editor → empty, other kinds → the kind text. One exception: an `execution` target gets a worker bar (§9.2).

### 9.2 Worker bar

For an `execution` target: a host segment (the same host badge / name the tmux bar uses), the worker name, and its cwd (from the execution summary), then the mode buttons. No refresh / peer-id / upload segments; those are tmux-specific.

### 9.3 Mode buttons

Rendered for `tmux-session` and `execution` targets in the controls block (where the split buttons were; keep `max-[500px]:hidden`). Three icon buttons with `aria-pressed` and titles: terminal (`TerminalWindow`), worker (`Robot`; label `room.view.room` 指揮室), chat (`ChatCircle`; label `room.view.chat` 聊天). The one matching the target's current mode is pressed.

| Target now | terminal | worker | chat |
|---|---|---|---|
| tmux-session | pressed | enabled only if `isHandoffCandidate`: opens the Hand to Nex dialog; success → room | same gate: opens the same dialog; success → content saved with `mode: 'chat'` |
| execution, room | enabled only when Take to terminal is offered (`from` or `canTakeToTerminal`, `ExecutionView.tsx:310-311`): runs that flow, including its running-turn confirm | pressed | `withViewMode(content, 'chat')` |
| execution, chat | same as room | `withViewMode(content, 'room')` | pressed |

Disabled buttons carry a title that says why (for example "This terminal is not running Claude Code" or "Nexen is not ready on this host"; new keys `status.mode.*`).

### 9.4 Opening Hand to Nex from the status bar

Lift the handoff dialog state out of the leaf (`PaneLayoutRenderer.tsx:71, 155-160, 179-189`) into a small non-persisted store, `useHandoffDialogStore` (`open({ tabId, paneId, content, mode? })`, `close()`), like `useConflictPanelStore`. One `HandoffConfirmDialog` host renders from that store; place it next to the other app-level overlays. Both the pane context menu and the status bar call `open`. The existing "host became hidden → close the dialog" effect (`:77-79`) moves with it. The gate (`isHandoffCandidate` plus its `agentType` / `handoffReady` subscriptions and the `ensure(hostId)` effect, `:58-80`) becomes a shared hook, `useHandoffCandidate(content)`, used by both callers. `handToNex` gains an optional initial `mode`: on success the new execution content is written with `withViewMode(…, mode)` when given. The mode must survive **every** path that produces execution content for this handoff. That includes the recovery path: when the compare-and-swap fails because the pane was closed or changed mid-request, `HandoffConfirmDialog` builds an "Open execution" content via `executionContentFor(...)` (`HandoffConfirmDialog.tsx:72-79`, `lib/nex/handoff.ts:81, 114`), and that content carries the mode too.

### 9.5 Take to terminal from the status bar

Do not reimplement the flow. `ExecutionView` registers itself in a module-level registry keyed by pane id: `{ canTake: boolean, busy: boolean, takeToTerminal(): void }`. The registry is updated on change and removed on unmount, the same pattern as `findListeners` (`ExecutionView.tsx:91-92`). The status bar subscribes through a tiny store or `useSyncExternalStore` and calls `takeToTerminal()`. That runs the view's own `onTakeBack`, with its running-turn confirm, lease handling (`forgetLease`) and busy guards. A worker pane that is not mounted cannot be the status target, because the active tab's leaves are always mounted.

### 9.6 Removed

The two split buttons (`StatusBar.tsx:524-539`, `status-split-buttons`) and their tests. Splitting remains in the title bar (Electron) and the pane context menu.

### 9.7 Tests (P6)

- `statusTargetOf`: each of the four rules, plus examples D.4-1/2/3 from §1 as named tests (worker + plain terminal → worker after clicking the terminal; two agent panes → follows the click; editor + CC terminal, never clicked → the terminal).
- `isAgentPane`: terminated tmux → false; tmux without agent → false; execution → true.
- Mode buttons: the pressed state per row of §9.3; the handoff gate disables worker/chat with a title; clicking worker on a candidate opens the store-driven dialog with the right target; chat opens it with `mode: 'chat'`; room↔chat writes `withViewMode` on the target pane, not on the primary. Take to terminal calls the registered handler; the button is disabled while `busy` or when `canTake` is false.
- Context-menu Hand to Nex still works through the lifted store (existing tests retargeted).
- Chat handoff where the pane is closed mid-request: the recovery "Open execution" tab opens in chat.
- Removed split-button tests are deleted; the "exactly one `.ml-auto`" invariant still holds.

---

## 10. P7 — Title bar layout buttons (D.1)

- Labels go through i18n: `pane.layout_single`, plus the existing `pane.split_horizontal` / `pane.split_vertical`.
- `currentLayoutPattern(layout)`: a leaf → `single`; a split with exactly two **leaf** children → `split-h` / `split-v` by direction; anything else → none. The matching button gets the active style and `aria-pressed`.
- Clicking the pressed button does nothing.
- **Which panes survive (rule D.1a).** The pattern has k slots (1 for `single`, 2 for a split). Let *content panes* be the live leaves whose kind is not `new-tab`, and *agent panes* those for which `isAgentPane` (§9.1) is true.
  1. If there are at most k content panes: keep them all and fill the remaining slots with blank `new-tab` panes. Nothing with content closes, so there is no dialog.
  2. Else if there are **exactly k agent panes**: keep them. Content panes would close, so the confirm below is shown.
  3. Else: open the **keep picker**, a dialog that lists every content pane (display label + kind icon) and lets the user tick exactly k of them. Its confirm button stays disabled until k are ticked, and it says which panes will close. Before the user changes anything, the k most recently focused panes are ticked (P5 record, topped up in layout order). The picker is the confirmation, so no second dialog follows.

  Survivors keep their original layout order. Missing slots are filled with blank `new-tab` panes, as today.
- **Confirm (case 2).** `ConfirmDialog` (`components/ConfirmDialog.tsx`, `testIdPrefix="layout-apply"`) lists the panes that will close by their display labels. It also says that sessions and workers keep running and can be reopened, and that an editor with unsaved changes will lose them.
- `applyLayoutPattern` gains an optional `keepIds` argument, the survivor set. Its current tests remain valid when it is omitted.
- Tests:
  - pattern detection per shape; the pressed state; no-op on the pressed button;
  - case 1, no dialog: one terminal plus a blank pane → `single` applies at once;
  - case 2: one CC terminal + an editor + a plain terminal → `single` keeps the CC terminal after a confirm;
  - case 3: two CC terminals → `single` opens the picker with the most recently focused one pre-ticked; confirm stays disabled at 0 ticks; the result keeps the ticked one;
  - case 3 with no agent pane: an editor + a plain terminal → picker;
  - cancel in either dialog leaves the layout untouched;
  - the 3-button count from P3 still holds.

---

## 11. Review notes

- P3 and P4 are deletion PRs. Prove completeness with the grep in §7, not by line counts.
- Profile Sync guard tests (`sections.test.ts:566`, `collector.test.ts:229`) treat unlisted layout fields as ignored. The new P1 fields must not appear in any projection.
- Any new import of `lib/shown-hosts` outside the allowlist fails `shown-hosts.import-guard.test.ts`; P1 adds `WorkerList.tsx` to it deliberately.
- Follow-ups not in scope: none known. Anything found during review that is out of scope becomes a `gh issue`.

# Plan — shell cleanup (P1–P7)

Spec: `docs/specs/2026-10-05-shell-cleanup-spec.md` v1.0. The decisions in its §1 are the user's and are not reopened here.

Anchors were measured on `498b8d59` (origin/main alpha.475 + spec) in worktree `shell-cleanup`. SPA paths are relative to `spa/src/`.

## Working rules

- TDD: write the failing test first, then the code. One commit per task, with `git commit --only <files>` when subagents run in parallel.
- Every subagent Bash command is prefixed with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/shell-cleanup/spa && `.
- Gates before each PR:
  - `npx vitest run`
  - `pnpm run lint`
  - `npx tsc --noEmit -p tsconfig.app.json`
  - `pnpm run build`
- Both locale files change together, and `locale-completeness.test.ts` stays green.
- Icons come from Phosphor only.
- No kill-type commands.
- Each PR branches from the previous PR's merge on origin/main. Re-measure anchors at the start of each phase, because earlier phases move lines.

## Review per PR

| PR | Review |
|---|---|
| P1, P5, P6, P7 | codex R1 + attack → critic (serial) |
| P2, P3, P4 (deletion) | codex R1 only. R2 runs only if R1 raises a P1 or critical. Completeness is proven by the grep checks listed for each, not by line counts. |

---

## P1 — Worker list and compact bottom group (spec §4)

### T1.1 Layout store fields

**Files:** `stores/useLayoutStore.ts`, `stores/useLayoutStore.test.ts`

**Add**
- Exports: `WORKER_LIST_MIN = 96`, `WORKER_LIST_MAX = 800`, `WORKER_LIST_DEFAULT = 240`.
- Fields: `workerListOpen` (false), `workerListHeight` (240), `bottomNavCompact` (false), all added to `partialize`.
- Setters: `setWorkerListOpen`, `toggleWorkerListOpen`, `setWorkerListHeight` (clamps to [96, 800]), `setBottomNavCompact`, `toggleBottomNavCompact`.

**Tests**
- Defaults.
- Clamp at both ends.
- Each toggle.
- The `partialize` output contains the three fields.
- `lib/profile/projections` still projects only `purdex-layout.tabPosition`. Assert the projection list, not a snapshot.

### T1.2 `PaneSplitter` gains `onResizeEnd` and `testId`

**Files:** `components/PaneSplitter.tsx`, `PaneSplitter.test.tsx`

- `onResizeEnd?: () => void` fires in `handleMouseUp`.
- `testId?: string` goes on the root element.
- The existing h/v behaviour does not change.

**Tests**
- `onResizeEnd` fires once on mouseup.
- It does not fire on mousemove.
- `testId` is rendered.

### T1.3 `openWorkerTab`

**Files:** new `features/workspace/lib/open-worker-tab.ts` + test

```ts
export function openWorkerTab(content: Extract<PaneContent, { kind: 'execution' }>): string
```

1. `const tabId = useTabStore.getState().openSingletonTab(content)`. This finds an existing leaf, or creates a standalone tab, and sets `activeTabId`.
2. `const ws = useWorkspaceStore.getState()`, then `let owner = ws.findWorkspaceByTab(tabId)`.
3. If there is no `owner`: `ws.insertTab(tabId)` (no wsId → the active workspace, else `workspaces[0]`, else Unsorted; `features/workspace/store.ts:187-195`), then `owner = ws.findWorkspaceByTab(tabId)`.
4. If there is an `owner`: `setActiveWorkspace(owner.id)` and `setWorkspaceActiveTab(owner.id, tabId)`. These are the same calls `handleSelectTab` makes (`features/workspace/hooks.ts:106-115`).

`insertTab` is never called for a tab that already has a workspace. That rule is what keeps an open worker from being moved (`store.ts:222-228`).

**Tests**
- An existing tab in workspace W2, with W1 active: the tab is selected, W2 becomes active, and W2's tab list is unchanged.
- A new worker with W1 active: the tab is inserted into W1 and W1 stays active.
- A new worker with Home active: it gets the `insertTab` fallback, and that workspace becomes active.
- Regression: after `vi.advanceTimersByTime(1000)`, the tab is not in Unsorted while W1 is active.

### T1.4 `ExecutionsView` uses `openWorkerTab`

**Files:** `components/executions/ExecutionsView.tsx:55-58`, `ExecutionsView.test.tsx:195-201`

- `open` keeps its `isRefShownNow` guard, then calls `openWorkerTab({ kind: 'execution', executionId, host: id })`.
- The test now spies on `openWorkerTab` (module mock) instead of `openSingletonTab`.

### T1.5 `WorkerList`

**Files:** new `components/executions/WorkerList.tsx` + test, `lib/shown-hosts.import-guard.test.ts` (add it to the allowlist)

- It reads `useHostStore(s => s.hostOrder)` and the shown filter `useShownRefFilter()` from `lib/shown-hosts`.
- For each shown host it renders a `HostSection`, which reads `useNexHostStore(s => s.byHost[id]?.phase)`. When the phase is `'disabled'` the section returns null; otherwise it renders `<ExecutionsView hostId={id} isActive />`.
- **Phase subscription.** Something must call `useNexHostStore.getState().ensure(id)` for every shown host, so that a never-ensured host gets a phase. `useHostExecutions` already calls `ensure`, so in practice `ExecutionsView` mounting is enough. But a `disabled` host renders no `ExecutionsView`, so `HostSection` calls `ensure(id)` itself in an effect. Its phase then still updates, and a host whose Nexen is later enabled gets its section back.
- The none line: when every shown host is `disabled`, or there are none, render `<p data-testid="worker-list-none">{t('workers.list.none')}</p>`. Derive this from the phases, not from DOM emptiness.
- Root: `data-testid="worker-list"`, `flex flex-col`. The caller provides scrolling.

**Tests**
- Order follows `hostOrder`.
- A hidden host is skipped.
- A `disabled` host has no section, while a `loading` host has one.
- The all-disabled case shows the none line.
- `ensure` is called for each shown host.

### T1.6 Wide bar: Workers toggle, divider, list section, compact mode

**Files:** `features/workspace/components/ActivityBarWide.tsx`, `ActivityBarWide.test.tsx`, plus a new `features/workspace/components/BottomNav.tsx` (shared by wide and narrow; holds the entry list and the two render modes)

**`BottomNav`**
- Props: `{ variant: 'wide' | 'narrow', compact: boolean, workersOpen: boolean, onAddWorkspace, onToggleWorkers, onOpenHosts, onOpenSettings, onToggleCompact?, workersRef? }`.
- Entries, in order: New workspace (`Plus`), Workers (`Lightning`, `aria-pressed={workersOpen}`, active style `text-accent-base bg-accent-base/10`), Hosts (`HardDrives`), Settings (`Sliders`).
- `wide` + not compact: today's row classes. The first row also carries a trailing `CaretDown` toggle button, `data-testid="bottom-nav-compact-toggle"`, `title={t('nav.bottom_compact')}`, and its own click handler.
- `wide` + compact: `flex flex-row items-center justify-between px-2`, with 30×30 icon buttons, each with a `title`. The last one is `CaretUp`, `data-testid="bottom-nav-compact-toggle"`, `title={t('nav.bottom_rows')}`.
- `narrow`: today's 30×30 column (`ActivityBarNarrow.tsx:182-204`) plus Workers. There is no compact toggle.
- `data-testid="bottom-nav"`, plus `data-compact="true|false"` for tests.

**`ActivityBarWide`**
- The bottom group (`:319-344`) is replaced by `<BottomNav variant="wide" …/>`, wired to the store.
- When `workerListOpen`, insert after the `DndContext` (`:317`), in this order:
  - `<PaneSplitter direction="v" testId="worker-list-divider" onResize={…} onResizeEnd={…}/>`
  - `<div data-testid="worker-list-section" className="min-h-0 overflow-y-auto overscroll-contain" style={{ height: renderedHeight }}><WorkerList/></div>`
- **Height**
  - Keep a draft in local state and a ref, mirroring the width draft at `:100-104`. `onResize(dy)` sets the draft to `base − dy`, so dragging up grows the list. `onResizeEnd` commits through `setWorkerListHeight`.
  - The rendered height is `min(draft ?? stored, available − WORKSPACE_ZONE_MIN)`, where `WORKSPACE_ZONE_MIN = 96`.
  - `available` is measured with a ResizeObserver on a wrapper around the workspace zone plus the list. Without an RO (jsdom), skip the cap.
- `restrictWorkspaceDrag` keeps clamping to `wsScrollRef` only.

**Tests**
- The section is absent when closed. Open → the order is DndContext zone → divider → section → bottom-nav.
- A drag of −50px followed by mouseup calls `setWorkerListHeight(290)` exactly once.
- The cap applies when an RO stub reports a small `available`.
- The toggle flips `bottomNavCompact`, and `data-compact` follows it.
- Workers `aria-pressed` follows the store.
- The existing tests keep passing, including "root has overflow-hidden" and "only the workspace zone scrolls". That last assertion must now allow the worker section as a second scroller: update the test to name the two allowed scrollers explicitly.

### T1.7 `FloatingPanel` `placement='right'` and the narrow Workers panel

**Files:** `components/FloatingPanel.tsx` + test, `features/workspace/components/ActivityBarNarrow.tsx` + test

**`FloatingPanel`**
- Add `placement?: 'below' | 'right'`, default `'below'`.
- In `place()`, `'right'` sets `left = a.right + PADDING` and `top = a.bottom − RIGHT_PANEL_HEIGHT`, with `RIGHT_PANEL_HEIGHT = min(480, window.innerHeight − topInset − PADDING)`. It then clamps exactly like `'below'`, and `applyMaxHeight(top)` still bounds the body.

**`ActivityBarNarrow`**
- The bottom group becomes `<BottomNav variant="narrow" …/>`.
- Workers toggles local `workersPanelOpen` state, anchored on `workersRef`.
- It renders `{workersPanelOpen && <FloatingPanel title={t('nav.workers')} anchorRef={workersRef} placement="right" width={320} testId="workers-panel" onClose={…}><WorkerList/></FloatingPanel>}`.
- The narrow variant passes `workersOpen={workersPanelOpen}`, not the store field.

**Tests**
- `placement='right'` geometry: left = anchor.right + 4, and top is clamped.
- `'below'` is unchanged.
- In the narrow bar, a click opens `workers-panel` and a second click closes it. That works because the outside-mousedown check excludes the anchor.

### T1.8 Locale

`nav.workers`, `nav.bottom_compact`, `nav.bottom_rows`, `workers.list.none` in `locales/en.json` and `locales/zh-TW.json`. (These could be folded into T1.5/T1.6 commits, but they ship together.)

**P1 acceptance:** gates are green. Manual check on :5174 after merge: open the list, drag the divider, reload (height and open state are kept), switch to compact, open a worker from W1 (it stays in W1), and use the narrow bar's panel.

---

## P2 — File trees and the `files` module (spec §5)

### T2.1 Delete the file trees and the files module

**Delete**
- `components/FileTreeView.tsx` + test
- `components/FileTreeSessionView.tsx` + test
- `components/settings/FilesWorkspaceSettingsSection.tsx` + test

**Edit**
- `lib/register-modules/index.tsx`: remove the `files` registerModule (`:286-328`) and the imports `FolderOpen`, `FileTreeWorkspaceView`, `FileTreeSessionView`, `FilesWorkspaceSettingsSection`.
- `lib/settings-order.ts`: remove `MODULE_FILES`, `WORKSPACE_FILES` and their doc rows.

**Tests to update**
- `lib/register-modules.test.ts`: remove `:528-532`, `:620-665` and `:821-828`, and drop `'settings.section.files'` at `:846`.
- `lib/__tests__/settings-order-pr2.test.ts`: remove the `'files'` entries.
- `features/workspace/components/WorkspaceSettingsPage.registry.test.tsx:258-306`.
- Retarget the SR-2 coverage ("a disabled module hides its workspace-scope setting") to the editor module's `workspace-home-path` (`editor-module.tsx:44-45`), in both places it existed.

### T2.2 Remove the popup's workspace-search action

**Files:**
- `lib/register-modules/file-open-bootstrap.ts`: `resolveProjectPath` (`:183-188`), `tryOpenFileForFileTree`, `_fileTreeService`, `getFileTreeService` (`:206-217, 232-239, 274`)
- `components/editor/popups/FileNotFoundPopup.tsx:129-139`, plus its i18n keys if they become unused
- `file-open-bootstrap.test.ts:5,107`: retarget to `tryOpenFileForTerminalLink`
- Comments at `file-open-bootstrap.ts:29,33,193,232`, `file-not-found-popup-service.tsx:9`, `open-file.ts:109`

**Precondition:** before deleting, read `FileNotFoundPopup.tsx` in full and confirm that the workspace search is its own action and not the only way to dismiss the popup or to retry. If removing it changes anything other than that one action, stop and report back. Do not widen the change.

**Tests:** the popup renders without the action, and its other actions still work.

### T2.3 Locale

Remove from both files: `file_tree.select_workspace_first`, `file_tree.set_project_path`, `file_tree.session_not_implemented`, `settings.files.project_path.label`, `settings.section.files_workspace`, `settings.section.files`, `modules.files.description`, and any popup key that T2.2 orphaned.

**Keep** `sidebar.view.files_workspace`. `SidebarRegion.test.tsx:247-259` reads it until P3.

**Completeness check (put it in the PR body):**

```
grep -rn "FileTreeView\|FileTreeSessionView\|FilesWorkspaceSettingsSection\|projectPath\|file_tree\.\|MODULE_FILES\|WORKSPACE_FILES" spa/src
```

This should return nothing, apart from `moduleConfig` itself, which is general-purpose.

---

## P3 — Unmount the regions (spec §6)

### T3.1 App and TitleBar

- `App.tsx`: remove the `SidebarRegion` import (`:10`) and the four elements (`:217, 233, 242, 250`).
- `components/TitleBar.tsx`: remove the `SidebarSimple` / `SquareHalfBottom` imports, the `useLayoutStore` and `SidebarRegion` imports, `regionToggles`, the `regions` / `toggleVisibility` selectors, the toggle map, and the divider (`:16-21, 25-26, 58-75`).
- `TitleBar.test.tsx`: the count goes 7 → 3 and the indices shift (`:22-28, :51-55, :63-66`).

### T3.2 Delete the region UI

Delete `components/SidebarRegion.tsx`, `RegionManager.tsx`, `RegionContextMenu.tsx` and their three tests. **Keep** `RegionResize.tsx` and its test.

Now that `SidebarRegion.test.tsx` is gone, remove `sidebar.view.files_workspace` from both locale files here.

**Completeness check:**

```
grep -rn "SidebarRegion\b\|RegionManager\|RegionContextMenu\|toggleVisibility" spa/src
```

This should return only the store and type definitions that P4 removes.

---

## P4 — Region state and the views registry (spec §7)

### T4.1 Store

- `stores/useLayoutStore.ts`: remove `RegionState`, `regions`, the region action types and bodies (`:98-185`), `createDefaultRegions`, `updateRegion`, `regions` in `partialize`, and the imports of `SidebarRegion` and `getAllViews`.
- `main.tsx`: remove `reconcileViews()` (`:67`) and the import (`:22`).

**Tests**
- `useLayoutStore.test.ts`: delete `:3-4`, `:8`, `:13-35`, `:46-51`, `:231-312` and `:315-536`.
- `lib/profile/collector.test.ts:229-230`: delete.
- Add one test: hydrating from a stored payload that still has `regions` does not throw, and the next `partialize` output has no `regions`.

### T4.2 Views API and types

- Delete `types/layout.ts`.
- `lib/module-registry.ts`: remove the `SidebarRegion` import/re-export, `ViewProps`, `ViewDefinition`, `views?`, `getViewDefinition`, `viewLabel` and `getAllViews`.
- `lib/register-modules/index.tsx`: remove the execution module's `views:` (`:259-268`), the `Lightning` import if it is unused there, and the Interface Pane/Sidebar stubs (`:357-372`).
- `components/executions/ExecutionsView.tsx`: replace `ViewProps` with a local `{ hostId?: string; isActive?: boolean }`.

**Tests**
- `types/tab.test.ts`: remove `:3` and `:59-69`.
- `lib/module-registry.test.ts`: remove `:8-9`, `:27-46` (views fixtures) and `:113-147`.
- `lib/register-modules/__tests__/orchestrator.test.tsx:63-85` and its imports.
- `lib/register-modules.test.ts:186-194`: the Interface subs become `['new-tab']`.
- Add a test that `InterfaceSubNav` renders correctly with a single sub-tab.

### T4.3 Locale and comments

**Locale:** remove `sidebar.manage_views`, `sidebar.add_views`, `sidebar.no_views_available`, `sidebar.section_enabled`, `sidebar.section_available`, `sidebar.view.executions`, `sidebar.view.files_session`, `settings.interface.pane` and `settings.interface.sidebar`.

**Comments:** reword `StoragePane.tsx:575` and `CostPanel.tsx:89`.

**Completeness check:**

```
grep -rn "SidebarRegion\|getAllViews\|getViewDefinition\|ViewDefinition\|ViewProps\|reconcileViews\|primary-panel\|secondary-sidebar\|sidebar\.view\." spa/src
```

This should return nothing.

---

## P5 — Pane focus record and focus on tab switch (spec §8)

### T5.1 Store and helpers

**Files:** new `stores/usePaneFocusStore.ts` + test, new `lib/pane-focus.ts` + test

**Store:** `recent: Record<string, string[]>`, `touch(tabId, paneId)` (move to the front, dedupe, cap at 16), `forgetTab(tabId)`. Plain `create`, with no persist and no syncManager.

**Helpers:**
- `liveLeafIds(layout)`.
- `focusTargetOf(tab, recentIds)`, implementing spec rule F.
- `statusTargetOf(tab, recentIds, isAgent)`, implementing spec D.4 rules 1–4. It is pure; `isAgent` is passed in.

**Tests:** every rule, plus dead ids being skipped.

**Wiring `forgetTab`:** `useTabStore.closeTab` (or a subscription where tabs are removed) calls `forgetTab`. Test: closing a tab clears its entry.

### T5.2 Record user focus

**Files:** `components/PaneLayoutRenderer.tsx` (leaf wrappers at `:203` and `:238`) + test

- Add `onPointerDownCapture={() => touch(tabId, layout.pane.id)}` and `onFocusCapture={…same}` to both wrappers.
- `tabId` is already in scope.

**Tests:** a pointerdown in the right leaf of a split records it first; a focus event inside a leaf records it.

### T5.3 `isFocusTarget` prop

**Files:** `lib/module-registry.ts` (`PaneRendererProps`), `components/PaneLayoutRenderer.tsx:100-113`

- Add `isFocusTarget: boolean` to `PaneRendererProps`.
- Compute it in the leaf as `isActive && layout.pane.id === focusTargetOf(tab, recent)`.
- Use a selector that returns only the target id for this tab (`usePaneFocusStore(s => …)`), so that a `touch` on another tab does not re-render this one.
- The disabled-module path ignores it.
- `Component` typing is `ComponentType<PaneRendererProps>`.

### T5.4 Gate every activation-time focus

The rule for every site: focus only when the pane **becomes** the target of a visible tab, which means one of:
- (a) the tab turned visible while this pane is the target, or
- (b) first mount as the active tab's target.

A target change inside an already-visible tab must **not** call `focus()`.

Implementation pattern: track `prevIsActive` in a ref and focus when `isActive` flips false→true with `isFocusTarget` true. On mount, focus if `isActive && isFocusTarget`.

**Sites**
- `components/TerminalView.tsx:128-141`: thread `isFocusTarget` from `SessionPaneContent.tsx:91`.
- `hooks/useTerminalWs.ts:81-86`: `reveal()` reads `isFocusTargetRef.current`, a new option passed by TerminalView. It focuses only when that is true.
- `components/room/WorkerInput.tsx:70-76`: the `focused` prop becomes `isFocusTarget`. `ExecutionView.tsx:568` passes it, `ExecutionPaneWrapper` (`register-modules/index.tsx:104-138`) threads it, and `ExecutionView` gets an `isFocusTarget` prop. The effect's dependencies stay `[focused, disabled]`. That way a send finishing in the target pane still refocuses it, while a send finishing in a non-target pane does nothing.
- `components/editor/MonacoWrapper.tsx:65-67, 86-89` and `components/editor/TiptapEditor.tsx:102, 120-123`: they take an `isFocusTarget` prop from `EditorPane.tsx:342, 362`.

**Tests (jsdom; spy on `focus`, do not rely on `inert`)**
- Terminal: a split tab A with two terminals; record the right one; deactivate, then reactivate A. Only the right terminal's `focus` is called. With no record, only the primary's is called.
- `reveal()` does not focus a non-target pane.
- Worker + editor split: on activation only the target focuses.
- Clicking between panes of a visible tab causes no `focus()` calls from these effects.
- Monaco and Tiptap: `isActive` true with `isFocusTarget` false → no `focus`.

### T5.5 Stale comment

`TerminalView.tsx:124` still describes offscreen `-9999em` positioning. Reword it to match `TabContent.tsx:37-41` (`visibility: hidden` + `inert`).

---

## P6 — Status bar target and mode buttons (spec §9)

### T6.1 `isAgentPane` hook and the status target

**Files:** new `hooks/useStatusTargetPane.ts` + test

`useStatusTargetPane(tab)` returns `Pane | null` using `statusTargetOf`, with `isAgent(content)`:
- `execution` → true.
- `tmux-session` → true when not `terminated` and `useAgentStore.agentTypes[compositeKey(hostId, code)]` is set.

Subscribe narrowly: compute the agent-ness of each leaf in one selector that returns a stable string key.

**Tests:** the three named examples from spec §9.7, plus rules 3 and 4.

### T6.2 StatusBar reads the target

**Files:** `components/StatusBar.tsx:270-274, 348` + test

- Replace `getPrimaryPane(activeTab.layout)` with the hook result.
- Every downstream derivation (`content`, the host, the session, `agentCk`) reads the target pane.
- Existing tests that build single-pane tabs are unaffected.
- New tests: a split with an editor on the left and a CC terminal on the right shows the terminal's session name; a worker + plain-terminal split, after a pointerdown on the plain terminal, still shows the worker bar.

### T6.3 Worker bar

**Files:** `components/StatusBar.tsx` (the `execution` branch at `:355-361`), maybe a new `components/status/WorkerStatusSegments.tsx` to keep StatusBar from growing

For an `execution` target, render:
- the host segment (reuse the tmux bar's host badge/name rendering; extract a component if it is inline),
- the worker name and cwd from `useExecutionStore.executions[executionKey(resolveExecutionHostId(content.host), executionId)].summary`,
- the mode buttons (T6.5) in an `ml-auto` controls block.

**Tests:** host, name and cwd are rendered; exactly one `.ml-auto` element.

### T6.4 Lift the handoff dialog and share the gate

**Files**
- new `stores/useHandoffDialogStore.ts` + test
- new `hooks/useHandoffCandidate.ts` + test
- `components/PaneLayoutRenderer.tsx:58-80, 155-160, 179-189`
- the app-level overlay host (where `TabContextMenu` and the other overlays render in `App.tsx`)
- `lib/nex/handoff.ts` (`handToNex` gains `mode?`)
- `HandoffConfirmDialog.tsx` (passes `mode` through)

**Steps**
- `useHandoffCandidate(content)` moves the `agentType` / `handoffReady` subscriptions and the `ensure(hostId)` effect out of PaneLayoutRenderer, and returns `isHandoffCandidate(...)`.
- `useHandoffDialogStore`: `{ target: { tabId, paneId, content, mode? } | null, open(t), close() }`.
- A `HandoffDialogHost` renders `HandoffConfirmDialog` from the store. It closes when the target's host becomes hidden (moved from `PaneLayoutRenderer.tsx:77-79`) or when the pane no longer holds that tmux content.
- The context menu's `hand-to-nex` calls `open(…)`.
- `handToNex`: on success with `mode`, the written content is `withViewMode(newContent, mode)`.

**Tests**
- The existing hand-to-nex context-menu tests are retargeted and still pass.
- The store opens and closes.
- The host-hidden auto-close works.
- `handToNex` with `mode: 'chat'` writes chat.

### T6.5 Take-to-terminal registry and the mode buttons

**Files**
- new `lib/nex/take-to-terminal-registry.ts` + test
- `components/execution/ExecutionView.tsx` (register / unregister)
- new `components/status/PaneModeButtons.tsx` + test
- `StatusBar.tsx` (remove the split buttons at `:524-539` and their tests at `StatusBar.test.tsx:150-195, 314-334, 705`)

**Registry:** `Map<paneId, { canTake, busy, takeToTerminal }>` plus a subscribe/notify pair. Read it with `useSyncExternalStore`.

**`ExecutionView`** registers in an effect that depends on `[paneId, canTake, busy]`, where:
- `canTake = !!from || canTakeToTerminal`
- `busy = takeBackBusy || writeInFlight`
- `takeToTerminal = onTakeBack`, through a ref so the latest closure runs

It unregisters on unmount.

**`PaneModeButtons({ tabId, pane })`:** the three buttons follow the spec §9.3 table. The handoff path uses `useHandoffCandidate` and `useHandoffDialogStore.open`. room↔chat goes through the same guarded `setPaneContent(withViewMode(...))` that `ExecutionPaneWrapper.onModeChange` uses; extract it to `lib/nex/view-mode.ts` as `setExecutionPaneMode(tabId, paneId, executionId, mode)` and use it in both places.

**Disabled titles:** new keys `status.mode.not_agent`, `status.mode.nex_not_ready`, `status.mode.cannot_take`, `status.mode.busy`, plus labels `status.mode.terminal`, `status.mode.worker`, `status.mode.chat` (zh: 終端機／指揮室／聊天).

The buttons render in the tmux controls block (`max-[500px]:hidden`) and in the worker bar.

**Tests:** each cell of the §9.3 table; actions target the status pane, not the primary; disabled titles.

---

## P7 — Title bar layout buttons (spec §10)

### T7.1 Pattern detection and survivors

**Files:** `lib/pane-tree.ts` + test

- `currentLayoutPattern(layout): LayoutPattern | null`
- `applyLayoutPattern(layout, pattern, keepIds?)`:
  - survivors = the first k live ids of `keepIds`, topped up from `collectLeaves` order (k = 1 or 2);
  - they are placed in `collectLeaves` order;
  - the rest is dropped;
  - missing slots are filled with `new-tab` panes.
  - Without `keepIds` the behaviour is unchanged, so the tests at `pane-tree.test.ts:367-395` stay green.
- `droppedLeaves(layout, pattern, keepIds?)`: returns the leaves that would be closed.

**Tests:** each shape; survivors follow `keepIds`; the dropped list.

### T7.2 TitleBar

**Files:** `components/TitleBar.tsx` + test, `stores/useTabStore.ts` (`applyLayout` gains `keepIds?`)

- Labels go through i18n: new `pane.layout_single`, existing `pane.split_horizontal` / `pane.split_vertical`.
- The current pattern is pressed (`aria-pressed`, active style); clicking it does nothing.
- Click handling:
  - compute `dropped` with `keepIds = recent[activeTabId]`;
  - if any dropped leaf is not `new-tab`, open `ConfirmDialog` (`testIdPrefix="layout-apply"`). The body lists the dropped panes' labels via `lib/pane-labels.ts` and carries the keep-running note plus the unsaved-editor note (new keys `pane.layout_confirm_title`, `pane.layout_confirm_body`, `pane.layout_confirm_editor`);
  - otherwise apply immediately.
- Keep the 3-button count and the existing invariants (`translate-y-[2.5px]`, `cursor-pointer`).

**Tests**
- Pressed state for each pattern.
- A no-op on the pressed button.
- A split of `new-tab` panes applies with no dialog (the existing test at `:45-58` stays valid).
- Dropping a terminal pane opens the dialog; cancel leaves the layout unchanged; confirm applies it.
- `single` keeps the most recently focused pane.

---

## Out of scope

Found while measuring, not handled here:

- Any change to `RegionResize` beyond keeping it. It stays the wide bar's width handle.

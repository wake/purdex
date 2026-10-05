# Plan — shell cleanup (P1–P7)

Spec: `docs/specs/2026-10-05-shell-cleanup-spec.md` v1.1. The decisions in its §1 are the user's and are not reopened here.

The codex plan review (job `task-muv5ptkz-8typid`, gpt-5.6-sol) returned 6 findings, all with confidence ≥ 0.91, and all are applied:

| # | Severity | Finding | Fixed in |
|---|---|---|---|
| 1 | critical | P5 activation-only focus | T5.3, T5.4 |
| 2 | critical | P2 popup removal file list | T2.2 |
| 3 | important | P1 DOM split box | T1.6 |
| 4 | important | measured right placement | T1.7 |
| 5 | important | chat mode on the handoff recovery path | T6.4 |
| 6 | important | focus-record cleanup by subscription | T5.1 |

The user's rule D.1a (2026-10-05) reshaped T7.1 and T7.2.

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
- **The split box** (spec §4.2). Inside the `DndContext`, right after the separator (`:286-288`), a new `<div data-testid="worker-split-box" ref={splitBoxRef} className="flex min-h-0 flex-1 flex-col">` wraps, in this order:
  - the existing workspace scroll zone (`:290-316`). Its `flex-1` now flexes inside the box, and `wsScrollRef` stays on it;
  - when `workerListOpen`, `<PaneSplitter direction="v" testId="worker-list-divider" onResize={…} onResizeEnd={…}/>`;
  - when `workerListOpen`, `<div data-testid="worker-list-section" className="min-h-0 shrink-0 overflow-y-auto overscroll-contain" style={{ height: renderedHeight }}><WorkerList/></div>`.
  - Home and the separator stay where they are, outside the box.
- **Height**
  - Keep a draft in local state and a ref, mirroring the width draft at `:100-104`. `onResize(dy)` sets the draft to `base − dy`, so dragging up grows the list. `onResizeEnd` commits through `setWorkerListHeight`.
  - The rendered height is `min(draft ?? stored, available − WORKSPACE_ZONE_MIN − dividerHeight)`, where `WORKSPACE_ZONE_MIN = 96` and `available` is the split box's height, measured by a ResizeObserver on `splitBoxRef`. Without an RO (jsdom), skip the cap; tests stub RO.
- `restrictWorkspaceDrag` keeps clamping to `wsScrollRef` only. The list registers no draggables or droppables.

**Tests**
- The section is absent when closed. Open → the order is DndContext zone → divider → section → bottom-nav.
- A drag of −50px followed by mouseup calls `setWorkerListHeight(290)` exactly once.
- The cap applies when an RO stub reports a small `available`.
- The toggle flips `bottomNavCompact`, and `data-compact` follows it.
- Workers `aria-pressed` follows the store.
- The existing tests keep passing, including "root has overflow-hidden" and "only the workspace zone scrolls". That last assertion must now allow the worker section as a second scroller: update the test to name the two allowed scrollers explicitly.
- DnD regression, with the list open: workspace reorder still calls `onReorderWorkspaces`, and a tab drag into another workspace still calls `onMoveTabToWorkspace`. Copy the existing DnD test setup in `ActivityBarWide.test.tsx`; if that file has none, drive the handlers through the same `DndContext` events the inline-tab tests use.

### T1.7 `FloatingPanel` `placement='right'` and the narrow Workers panel

**Files:** `components/FloatingPanel.tsx` + test, `features/workspace/components/ActivityBarNarrow.tsx` + test

**`FloatingPanel`**
- Add `placement?: 'below' | 'right'`, default `'below'` (unchanged code path).
- `'right'`, per spec §4.6:
  - `maxHeight = innerHeight − topInset − PADDING`, set before measuring.
  - In the layout effect, after render, read the panel's `getBoundingClientRect().height` (`h`). Then `left = clamp(a.right + PADDING)` and `top = clamp(a.bottom − h, topInset, innerHeight − PADDING − h)`.
  - A ResizeObserver on the panel re-runs this while `draggedRef` is false. The resize/scroll re-anchoring (`:123-140`) also calls the `'right'` path.

**`ActivityBarNarrow`**
- The bottom group becomes `<BottomNav variant="narrow" …/>`.
- Workers toggles local `workersPanelOpen` state, anchored on `workersRef`.
- It renders `{workersPanelOpen && <FloatingPanel title={t('nav.workers')} anchorRef={workersRef} placement="right" width={320} testId="workers-panel" onClose={…}><WorkerList/></FloatingPanel>}`.
- The narrow variant passes `workersOpen={workersPanelOpen}`, not the store field.

**Tests**
- `placement='right'` geometry, with `getBoundingClientRect` stubbed: left = anchor.right + 4; `panel.bottom === anchor.bottom` for a short panel; clamped to `topInset` when the anchor is near the top; re-placed after an RO callback reports a taller panel.
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

**Files** (spec §5; the whole feature goes, end to end):
- `lib/register-modules/file-open-bootstrap.ts`: `resolveProjectPath` (`:183-188`), `tryOpenFileForFileTree`, `_fileTreeService`, `getFileTreeService` (`:206-217, 232-239, 274`), and wherever it builds `projectPath` / `onSearchWorkspace` for the popup.
- `lib/file-open/file-not-found-popup-service.tsx`: the `projectPath` / `onSearchWorkspace` members of `ShowCallbacks` (`:20`) and their pass-through in **both** render paths (`:51` and the second one). Also the stale comment at `:9`.
- `components/editor/popups/FileNotFoundPopup.tsx`: those props, the action button (`:129-139`), the expanded mode's workspace-results section (`:100`) and any state that only it uses. Drop i18n keys that become unused.
- Tests: `file-open-bootstrap.test.ts:5,107` (retarget to `tryOpenFileForTerminalLink`), the popup-service test, and the `FileNotFoundPopup` test.
- Comments at `file-open-bootstrap.ts:29,33,193,232` and `open-file.ts:109`.

**Precondition:** before deleting, read the popup and the service in full and confirm that the workspace search is self-contained: neither the popup's dismissal nor its other actions depend on it. If removing it would change anything else, stop and report back. Do not widen the change.

**Tests:** the popup renders without the search action or the results section, in both the compact and expanded modes. Its other actions still work.

**Check:** `grep -rn "projectPath\|onSearchWorkspace\|searchWorkspace" spa/src` returns nothing.

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

**Cleanup** (spec §8.1): the store module subscribes to `useTabStore` and, whenever the set of tab ids changes, deletes `recent[id]` for every id no longer in `tabs`. This is the only cleanup point, and `closeTab` is not touched.
- Make it cheap: compare `Object.keys(tabs)` identity or length before diffing.
- Export the subscription's install function and call it once from the module, so tests can reset state.

**Tests:** `closeTab` clears the entry. A wholesale `useTabStore.setState({ tabs: {...} })` that drops two tabs clears both. A tab that stays keeps its record.

### T5.2 Record user focus

**Files:** `components/PaneLayoutRenderer.tsx` (leaf wrappers at `:203` and `:238`) + test

- Add `onPointerDownCapture={() => touch(tabId, layout.pane.id)}` and `onFocusCapture={…same}` to both wrappers.
- `tabId` is already in scope.

**Tests:** a pointerdown in the right leaf of a split records it first; a focus event inside a leaf records it.

### T5.3 `isFocusTarget` prop

**Files:** `lib/module-registry.ts` (`PaneRendererProps`), `components/PaneLayoutRenderer.tsx:100-113`

- Add `isFocusTarget: boolean` to `PaneRendererProps`, **next to** `isActive`.
- Compute it in the leaf as `layout.pane.id === focusTargetOf(tab, recent)`. It does **not** include `isActive`: the sites need both to tell an activation from a click (spec §8.2).
- Use a selector that returns only the target id for this tab (`usePaneFocusStore(s => …)`), so that a `touch` on another tab does not re-render this one.
- The disabled-module path ignores it.
- `Component` typing is `ComponentType<PaneRendererProps>`.

### T5.4 Gate every activation-time focus

The rule for every site (spec §8.2): programmatic focus happens only at **activation**, meaning `isActive` flips false→true, or the pane first mounts with `isActive` true. At that moment the pane focuses iff `isFocusTarget` is true. A change of `isFocusTarget` while `isActive` stays true must **never** call `focus()`.

Implementation pattern, shared as a tiny hook `useActivationFocus(isActive, isFocusTarget, focusFn)` in `hooks/useActivationFocus.ts`:
- a `prevActive` ref, initialised to `false` so that mount counts as an activation;
- an effect with dependencies `[isActive]` **only**, which reads `isFocusTarget` and `focusFn` through refs;
- when `isActive && !prevActive && isFocusTargetRef.current`, it calls `focusFn` (inside a rAF where the site used one);
- it then sets `prevActive = isActive`.

Because `isFocusTarget` is not a dependency, a click inside a visible tab cannot trigger it. Test the hook directly for all three timing paths.

**Sites**
- `components/TerminalView.tsx:128-141`: the visible effect keeps its fit/resize work on `visible` false→true, and its `focus()` moves into `useActivationFocus(visible, isFocusTarget, …)`. Thread `isFocusTarget` from `SessionPaneContent.tsx:91`. Keep in mind that `prevVisible` starts at the initial value today, so a mount does not fire; the new hook handles mount on its own.
- `hooks/useTerminalWs.ts:81-86`: `reveal()` is the first-mount path. It focuses only if `activeRef.current && isFocusTargetRef.current`, two new refs that TerminalView passes in options, the way `onReadyRef` works.
  - When the activation hook also fires on mount, the terminal may not be connected yet. Whichever of the two runs first focuses, and the second call is harmless because it targets the same pane.
- `components/room/WorkerInput.tsx:70-76`:
  - replace `focused` with two props, `isActive` and `isFocusTarget`;
  - activation focus goes through the hook;
  - the post-send refocus is a separate effect on `disabled` true→false, with a `prevDisabled` ref, gated by `isFocusTargetRef.current && isActive`;
  - thread the props through `ExecutionView.tsx:568` and `ExecutionPaneWrapper` (`register-modules/index.tsx:104-138`).
- `components/editor/MonacoWrapper.tsx:65-67, 86-89` and `components/editor/TiptapEditor.tsx:102, 120-123`: they take `isFocusTarget` from `EditorPane.tsx:342, 362`.
  - The on-ready/on-mount focus becomes "if `isActive && isFocusTarget`".
  - The `isActive` effect becomes `useActivationFocus`.

**Tests (jsdom; spy on `focus`, do not rely on `inert`).** These are named after the three spec §8.3 timing paths.
- `useActivationFocus`:
  - mount active and target → called once;
  - mount active and not target → not called;
  - inactive→active with target → called;
  - target flips while active → **not** called.
- **Keep-alive reactivation**: a split tab A with two terminals; record the right one; deactivate, then reactivate A. Only the right terminal's `focus` is called. With no record, only the primary's is called.
- **First mount as active**: only the target focuses, through the hook and through `reveal()`; `reveal()` does not focus a non-target pane.
- **Click inside a visible tab**: a pointerdown on the other pane causes no programmatic `focus()` from any site.
- Worker + editor split: on activation only the target focuses. A worker that finishes sending while it is not the target does not take focus.
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
- Recovery path (spec §9.4): `executionContentFor(...)` (`lib/nex/handoff.ts:81`) takes the optional `mode`. `HandoffConfirmDialog`'s CAS-failure branch (`:72-79`) passes it, so the "Open execution" content it offers carries the mode. Thread the mode through `handoff.ts:114` too.

**Tests**
- The existing hand-to-nex context-menu tests are retargeted and still pass.
- The store opens and closes.
- The host-hidden auto-close works.
- `handToNex` with `mode: 'chat'` writes chat.
- With `mode: 'chat'`, a CAS failure (the pane closed mid-request) leads to a recovery "Open execution" that opens a chat tab.

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
  - with `keepIds`, it is the exact survivor set (size ≤ k);
  - survivors are placed in `collectLeaves` order;
  - the rest is dropped;
  - missing slots are filled with `new-tab` panes.
  - Without `keepIds` the behaviour is unchanged, so the tests at `pane-tree.test.ts:367-395` stay green.
- `planLayoutChange(layout, pattern, { isAgent, recent })` (new, in `lib/layout-change.ts`) implements spec D.1a. It returns one of:
  - `{ kind: 'apply', keepIds }`: case 1, nothing with content closes;
  - `{ kind: 'confirm', keepIds, closing }`: case 2, exactly k agent panes;
  - `{ kind: 'pick', k, candidates, preselected }`: case 3, where `candidates` are the content panes and `preselected` are the k most recently focused of them, topped up in layout order.

  `isAgent` is P6's `isAgentPane`, so P7 depends on P6.

**Tests:** each shape; survivors equal `keepIds`; `planLayoutChange`'s three cases with the spec §10 examples (one terminal + blank; one CC + editor + plain; two CC; editor + plain), and that `preselected` follows the focus record.

### T7.2 TitleBar

**Files:** `components/TitleBar.tsx` + test, `stores/useTabStore.ts` (`applyLayout` gains `keepIds?`)

- Labels go through i18n: new `pane.layout_single`, existing `pane.split_horizontal` / `pane.split_vertical`.
- The current pattern is pressed (`aria-pressed`, active style); clicking it does nothing.
- Click handling: `planLayoutChange(...)`.
  - `apply` → apply immediately.
  - `confirm` → `ConfirmDialog` (`testIdPrefix="layout-apply"`). The body lists the closing panes' labels (via `lib/pane-labels.ts`) and carries the keep-running note plus the unsaved-editor note. New keys: `pane.layout_confirm_title`, `pane.layout_confirm_body`, `pane.layout_confirm_editor`.
  - `pick` → a new `components/LayoutKeepPicker.tsx`:
    - it is built on `ConfirmDialog`'s `children` slot, with `testIdPrefix="layout-keep"`;
    - a checkbox per candidate (label + kind icon), preselected;
    - at most k can be ticked: ticking one more unticks the oldest tick when k = 1, and is refused at the cap when k = 2;
    - confirm is disabled until exactly k are ticked;
    - the closing list updates live.
    - New keys: `pane.layout_keep_title`, `pane.layout_keep_body`.
  - Both dialogs apply with `applyLayout(tabId, pattern, keepIds)`.
- Keep the 3-button count and the existing invariants (`translate-y-[2.5px]`, `cursor-pointer`).

**Tests**
- Pressed state for each pattern.
- A no-op on the pressed button.
- A split of `new-tab` panes applies with no dialog (the existing test at `:45-58` stays valid).
- Case 2 → confirm; cancel leaves the layout unchanged; confirm keeps the agent pane.
- Case 3 → picker:
  - the preselection follows the focus record;
  - confirm is disabled at the wrong count;
  - the result keeps the ticked panes;
  - cancel leaves the layout unchanged.

---

## Out of scope

Found while measuring, not handled here:

- Any change to `RegionResize` beyond keeping it. It stays the wide bar's width handle.

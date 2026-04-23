# Editor Restructure (PR 2) — Plan

- Spec: `docs/specs/2026-04-24-editor-restructure-pr2-spec.md` v1.1
- Plan revision: **v1.1** (2026-04-24) — aligned with spec v1.1 after
  Round-1 codex review
- Base commit: `bb5ce0c1` (main @ alpha.216)
- Target version: 1.0.0-alpha.217

## 0. Orientation

### Spec reference
Spec v1.1. Three-commit PR targeting alpha.217.

### Out of scope (mirroring spec §2)
- No changes to IndexedDB / InAppBackend storage layer internals.
- No new buffer features (versioning, import/export, search-in-buffers).
- No changes to EditorPane rendering logic (only toolbar chip and the
  store behind Monaco options).
- No cleanup of `globalConfig` / `workspaceConfig` / `ModuleConfigSection`
  (tracked as #618).
- **No Tiptap fontSize integration** (deferred to follow-up — see
  spec §2).
- **No FS backend API changes** — `FileEntry` stays at `{name, isDir,
  size}`; BuffersPane sorts by name ascending.
- **No subfolder rename** — rename validator rejects `/`; follow-up PR
  can patch `InAppBackend.rename` to create parent dirs.
- No HSR schema changes.

### Verification toolchain
Run from `spa/` (repo root has no `build` script):
`cd spa && pnpm run lint && npx vitest run && pnpm run build`

---

## 1. Current-state verification

Anchors confirmed accurate as of base commit `bb5ce0c1`:

- `spa/src/lib/register-modules.tsx:176-202` — Editor module
  `registerModule({id:'editor',...})` currently has
  `panes: [EditorPane, ImagePreviewPane, PdfPreviewPane]` and
  `settings: [{localId:'workspace-home-path',...},{localId:'host-home-path',...}]`.
  No `localId:'editor'` entry yet.
- `spa/src/lib/register-modules.tsx:323-328` — Legacy
  `registerSettingsSection({id:'editor-buffers', label:'settings.section.editor_buffers',
  order:9, component:BufferListSection})`. Remove in Commit 1.
- `spa/src/lib/register-modules.tsx:354-370` — Existing NewTab
  providers: `sessions` (order:0), `editor` (order:5), `browser`
  (order:-10). New `editor-buffers` at `order:6`.
- `spa/src/lib/register-modules.tsx:66-76` — NewTab `onSelect`
  handler: `setPaneContent(tabId, pane.id, content)`. Confirms §4.7
  correction: card does NOT call `openSingletonTab`.
- `spa/src/lib/settings-contribution-types.ts:46-55` — HSR shape;
  line 82-84 `isModuleOwnedContribution`.
- `spa/src/components/editor/MonacoWrapper.tsx:67-75` — Hardcoded
  options: `minimap:{enabled:true}`, `fontSize:13`, `lineNumbers:'on'`,
  `wordWrap:'on'`, `scrollBeyondLastLine:false`, `automaticLayout:true`.
  No `tabSize` / `insertSpaces` currently (Monaco defaults).
- `spa/src/components/editor/EditorToolbar.tsx:19,27-39` — inapp
  prefix chip block; Commit 3 wraps in `<button>`.
- `spa/src/components/editor/EditorPane.tsx:116-143` — EditorPane
  reads the buffer from `pane.content.filePath`. Confirms §4.6
  correction: cross-pane buffer swap requires `setPaneContent`, not
  `attachPane`.
- `spa/src/components/editor/BufferListSection.tsx` — Standalone
  legacy component. Delete in Commit 2.
- `spa/src/stores/useEditorStore.ts:107-135` — `attachPane(paneId,
  bufferKey)`; same-pane swap API (used by breadcrumb popover after
  `setPaneContent`).
- `spa/src/stores/useTabStore.ts:115,173-189` — `addTab`,
  `openSingletonTab`, `setPaneContent(tabId, paneId, content)`;
  `setActiveTab`. `renameEditorPanes` at 78-107 handles renames only
  (NOT deletions — see §4.9.5 of spec).
- `spa/src/lib/pane-tree.ts` — `scanPaneTree` / `collectLeaves` for
  layout traversal.
- `spa/src/lib/fs-backend-inapp.ts:105-109` — `rename(from,to)` does
  NOT create intermediate dirs → flat-only rename in UI.
- `spa/src/lib/fs-backend-inapp.ts:62-83` — `list(path)` direct
  children only → sort-by-name adequate.
- `spa/src/lib/new-tab-registry.ts` — `NewTabProvider` shape; add
  optional `moduleId?: string` for §4.9.3 filter.
- `spa/src/components/NewTabPage.tsx:27-60` — consumer site of
  `getNewTabProviders()`; add module-filter here.
- `spa/src/components/SettingsPage.tsx:92-99` — unknown-section
  fallback; add alias redirect `editor-buffers → editor`.
- `spa/src/lib/route-utils.ts:98-121` — `tabToUrl()` switch.
- `spa/src/lib/pane-labels.ts:19-82` — `getPaneLabel` and
  `getPaneIcon` switches.
- `spa/src/lib/storage/keys.ts` — add `EDITOR_SETTINGS`.
- `spa/src/locales/en.json:139`, `zh-TW.json:139` — target keys.
- `spa/src/lib/register-modules.test.ts:329,331,393` — `'editor-buffers'`
  references (update in Commit 1).
- `spa/src/components/RenamePopover.tsx` — positioning + z-50 pattern.
- `spa/src/stores/useModuleEnabledStore.ts` — zustand+persist template.
- `spa/src/types/tab.ts:36-47` — `PaneContent` union; extend in
  Commit 2.

---

## 2. TDD test matrix

Total: **36 tests** across 3 commits (v1.1 delta: +5 ancillary
integration + delete flow tests, -0 drops).

| ID | File | Test name | Asserts (one-line) | Commit |
|----|------|-----------|-------------------|--------|
| S1-1 | `stores/useEditorSettingsStore.test.ts` | defaults match spec | 6 fields equal spec defaults | 1 |
| S1-2 | `stores/useEditorSettingsStore.test.ts` | setFontSize clamps below 10 | `setFontSize(5)` → 10 | 1 |
| S1-3 | `stores/useEditorSettingsStore.test.ts` | setFontSize clamps above 24 | `setFontSize(30)` → 24 | 1 |
| S1-4 | `stores/useEditorSettingsStore.test.ts` | setFontSize NaN falls back | `setFontSize(NaN)` → 13 | 1 |
| S1-5 | `stores/useEditorSettingsStore.test.ts` | reset restores defaults | mutate all → reset → defaults | 1 |
| S1-6 | `stores/useEditorSettingsStore.test.ts` | persists under EDITOR_SETTINGS | localStorage has `purdex-editor-settings` after mutation | 1 |
| S1-7 | `stores/useEditorSettingsStore.test.ts` | merge sanitizes unknowns | bad rehydrate → defaults per field | 1 |
| S1-8 | `stores/useEditorSettingsStore.test.ts` | merge tolerates null | null rehydrate → no throw, all defaults | 1 |
| R1-1 | `lib/register-modules.test.ts` | editor settings has 3 entries | `editor.settings.length === 3` | 1 |
| R1-2 | `lib/register-modules.test.ts` | editor.editor scope is purdex | `getContribution('editor.editor').scope === 'purdex'` | 1 |
| R1-3 | `lib/register-modules.test.ts` | legacy editor-buffers gone | `getSettingsSections().find(s=>s.id==='editor-buffers')` undefined | 1 |
| R1-4 | `lib/register-modules.test.ts` | i18n keys present | both locales have `settings.section.editor` | 1 |
| R1-5 | `lib/register-modules.test.ts` | module-owned | `isModuleOwnedContribution(editor.editor) === true` | 1 |
| L1-1 | `components/SettingsPage.test.tsx` | legacy route alias redirects | navigate to `editor-buffers` → `editor` section renders | 1 |
| P2-1 | `types/tab.test.ts` | editor-buffers is valid kind | `createTab({kind:'editor-buffers'})` round-trips | 2 |
| B2-1 | `components/editor/EditorBuffersPane.test.tsx` | empty state | backend.list=[] → empty-state text | 2 |
| B2-2 | `components/editor/EditorBuffersPane.test.tsx` | lists entries by name asc | backend.list returns 3 → 3 rows in alphabetical order | 2 |
| B2-3 | `components/editor/EditorBuffersPane.test.tsx` | delete calls backend | click delete → `backend.delete` called with path | 2 |
| B2-4 | `components/editor/EditorBuffersPane.test.tsx` | new creates + refreshes | click New → `backend.write('/buffer/Untitled*.md', '')` + list reloads | 2 |
| B2-5 | `components/editor/EditorBuffersPane.test.tsx` | rename rejects slash | input `drafts/foo.md` → validator error visible; `rename` NOT called | 2 |
| B2-6 | `components/editor/EditorBuffersPane.test.tsx` | flat rename calls backend | input `bar.md` → `backend.rename('/buffer/foo.md', '/buffer/bar.md')` | 2 |
| B2-7 | `components/editor/EditorBuffersPane.test.tsx` | smart-open: active tab editor pane | active tab has editor pane → `setPaneContent(activeTabId, paneId, ...)` called; `setActiveTab` called with activeTabId; no addTab | 2 |
| B2-8 | `components/editor/EditorBuffersPane.test.tsx` | smart-open: other tab editor pane | active tab has none, other does → `setPaneContent(otherTabId, ...)` called + `setActiveTab(otherTabId)` | 2 |
| B2-9 | `components/editor/EditorBuffersPane.test.tsx` | smart-open: fallback new tab | no editor panes anywhere → `addTab` called with editor content | 2 |
| B2-10 | `components/editor/EditorBuffersPane.test.tsx` | delete resets open panes | deleting `/buffer/x.md` with 2 open editor panes on it → `setPaneContent` called twice with `filePath: null` BEFORE `backend.delete` | 2 |
| N2-1 | `lib/register-modules.test.ts` | buffers NewTab provider registered | `getNewTabProviders().find(p=>p.id==='editor-buffers')` defined | 2 |
| N2-2 | `lib/register-modules.test.ts` | provider order after editor | `editor-buffers.order > editor.order` | 2 |
| N2-3 | `components/editor/ManageBuffersNewTabCard.test.tsx` | card onSelect payload | click → `onSelect({kind:'editor-buffers'})` (NOT openSingletonTab) | 2 |
| A2-1 | `lib/route-utils.test.ts` | tabToUrl handles editor-buffers | `tabToUrl({layout: paneWith({kind:'editor-buffers'})})` returns stable path (plan picks exact) | 2 |
| A2-2 | `lib/pane-labels.test.ts` | getPaneLabel handles editor-buffers | `getPaneLabel({kind:'editor-buffers'})` returns i18n key resolved string | 2 |
| A2-3 | `lib/pane-labels.test.ts` | getPaneIcon handles editor-buffers | `getPaneIcon({kind:'editor-buffers'})` returns `'Stack'` | 2 |
| A2-4 | `components/NewTabPage.test.tsx` | module-disabled filter hides provider | with `useModuleEnabledStore.setState({enabled: {editor: false}})` → NewTab grid omits `editor-buffers` and `editor` cards | 2 |
| C3-1 | `components/editor/BreadcrumbPopover.test.tsx` | renders list | buffers=[a,b,c] → 3 items | 3 |
| C3-2 | `components/editor/BreadcrumbPopover.test.tsx` | current marked | item matching currentKey has aria-current | 3 |
| C3-3 | `components/editor/BreadcrumbPopover.test.tsx` | click switch | non-current click → `onSwitch` called with that key | 3 |
| C3-4 | `components/editor/BreadcrumbPopover.test.tsx` | Escape dismisses | keydown Escape → `onDismiss` | 3 |
| C3-5 | `components/editor/BreadcrumbPopover.test.tsx` | manage link | click Manage → `onManage` | 3 |
| T3-1 | `components/editor/EditorToolbar.test.tsx` | inapp chip is button | `source.type='inapp'` → Purdex `<button>` rendered | 3 |
| T3-2 | `components/editor/EditorToolbar.test.tsx` | non-inapp no chip | `source.type='daemon'` → no Purdex chip | 3 |

Note: A2-1's exact URL path will be picked in Commit 2 by greping the
existing `tabToUrl` convention (e.g. `/editor/buffers`).

---

## 3. Commit 1 — EditorSettingsStore + HSR migration + legacy URL redirect

### 3.1 Files touched

Create:
- `spa/src/stores/useEditorSettingsStore.ts`
- `spa/src/stores/useEditorSettingsStore.test.ts`
- `spa/src/components/settings/EditorPurdexSettingsSection.tsx`
- `spa/src/components/SettingsPage.test.tsx` (if not existing — check;
  otherwise extend existing)

Modify:
- `spa/src/lib/storage/keys.ts` — add `EDITOR_SETTINGS` key
- `spa/src/lib/register-modules.tsx` — add `localId:'editor'`; remove
  legacy `registerSettingsSection`; add `EditorPurdexSettingsSection`
  import
- `spa/src/components/editor/MonacoWrapper.tsx` — read 6 options from
  store
- `spa/src/components/SettingsPage.tsx` — add alias redirect
  `editor-buffers → editor` in the section resolution path
- `spa/src/locales/en.json` — add `settings.section.editor`; remove
  `settings.section.editor_buffers`
- `spa/src/locales/zh-TW.json` — same
- `spa/src/lib/register-modules.test.ts` — update lines 331/393; add
  R1-1..R1-5

### 3.2 TDD steps

**Step 1: Add EDITOR_SETTINGS key**

Action: Add `EDITOR_SETTINGS: 'purdex-editor-settings'` to
`STORAGE_KEYS`.

---

**Step 2: Write failing S1-1..S1-8**

Create `spa/src/stores/useEditorSettingsStore.test.ts`:
```
// beforeEach: localStorage.clear() + setState(DEFAULTS, false) merge-mode
// Mock syncManager with vi.hoisted (same as useModuleEnabledStore.test)

// S1-1: fresh state defaults match spec
// S1-2: setFontSize(5) -> 10
// S1-3: setFontSize(30) -> 24
// S1-4: setFontSize(NaN) -> 13
// S1-5: mutate all; reset(); assert defaults
// S1-6: setWordWrap('off'); localStorage has the key with wordWrap:'off'
// S1-7: rehydrate {tabSize:99, wordWrap:'maybe', fontSize:'big'}; invalids -> defaults
// S1-8: rehydrate {state:null, version:1}; no throw, all defaults
```

Expected: 8 fail on missing module.

---

**Step 3: Implement useEditorSettingsStore**

Action: `spa/src/stores/useEditorSettingsStore.ts`:
```
// interface EditorSettingsState: 6 fields + 6 setters + reset
// DEFAULT_EDITOR_SETTINGS constant
// setFontSize: Number.isFinite(v) ? clamp(round(v), 10, 24) : DEFAULT.fontSize
// merge: per-field validator, fallback to DEFAULT
// persist config: name, purdexStorage, version:1, partialize identity
```

Postcondition: S1-1..S1-8 pass.

---

**Step 4: MonacoWrapper store subscription**

Action: Edit `MonacoWrapper.tsx:67-75`:
- Import + destructure all 6 fields from store at component top.
- Replace options object: all 6 values from store; keep
  `scrollBeyondLastLine:false`, `automaticLayout:true`.

No unit test (Monaco needs real DOM); covered by build.

---

**Step 5: EditorPurdexSettingsSection (transitional)**

Action: `spa/src/components/settings/EditorPurdexSettingsSection.tsx`:
```
// Props: { ctx: SettingsContextFor<'purdex'> }  // ctx unused
// Read useEditorSettingsStore
// Sections:
//   Indentation: tabSize <select 2|4|8>, insertSpaces <toggle>
//   Display: wordWrap / lineNumbers / minimap toggles + fontSize <input number 10..24>
// Below: transitional <BufferListSection />   (Commit 1 only)
// Note: "These settings apply to the Monaco code editor."
```

---

**Step 6: register-modules.tsx HSR migration**

Actions in one file (atomic):
- Prepend to editor.settings: `{localId:'editor', scope:'purdex', order:9, labelKey:'settings.section.editor', component:EditorPurdexSettingsSection}`.
- Delete `registerSettingsSection({id:'editor-buffers',...})` at 323-328.
- Add `EditorPurdexSettingsSection` import.
- Remove `BufferListSection` import at 38 ONLY IF no other reference
  remains in this file (it's still rendered inside
  EditorPurdexSettingsSection imported from the section file itself —
  so the settings section file imports BufferListSection directly,
  and register-modules.tsx no longer needs the import).

---

**Step 7: i18n rename**

Both locale files:
- Remove `settings.section.editor_buffers`.
- Add `settings.section.editor` = `"Editor"` / `"編輯器"`.

---

**Step 8: Legacy URL redirect + L1-1**

Write L1-1 first (test-first):
```
// L1-1: render <SettingsPage initialSection='editor-buffers' />
//       assert active section resolved to 'editor' (use a testid or
//       check labelKey lookup in the rendered header)
```

Action: `spa/src/components/SettingsPage.tsx:92-99`:
Before the fallback resolution, add:
```
// if (rawId === 'editor-buffers') rawId = 'editor'
// then continue existing resolve logic
```

---

**Step 9: register-modules.test.ts updates**

Actions:
- Line 331 array: remove `'editor-buffers'` → `['appearance','terminal','interface','sync','module-config']`.
- Line 393 array: same removal.
- Append `describe('Commit 1: Editor HSR migration')` with R1-1..R1-5.

### 3.3 Acceptance (mirrors spec §7 Commit 1)
All 14 acceptance bullets from spec §7 Commit 1 verified via:
- Tests S1-*, R1-*, L1-1 passing.
- Build output contains no reference to `settings.section.editor_buffers`.
- Manual sidebar check confirms puzzle-piece marker on "Editor".
- `grep -r editor_buffers spa/src` returns empty.

### 3.4 Verification commands
```
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run lint
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && npx vitest run
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run build
```

---

## 4. Commit 2 — EditorBuffersPane + NewTab entry + ancillary integration

### 4.1 Files touched

Create:
- `spa/src/types/tab.test.ts` (or extend existing)
- `spa/src/components/editor/EditorBuffersPane.tsx`
- `spa/src/components/editor/EditorBuffersPane.test.tsx`
- `spa/src/components/editor/ManageBuffersNewTabCard.tsx`
- `spa/src/components/editor/ManageBuffersNewTabCard.test.tsx`
- `spa/src/lib/route-utils.test.ts` (or extend existing)
- `spa/src/lib/pane-labels.test.ts` (or extend existing)
- `spa/src/components/NewTabPage.test.tsx` (or extend existing)

Modify:
- `spa/src/types/tab.ts` — extend `PaneContent` union with
  `{kind:'editor-buffers'}`
- `spa/src/lib/route-utils.ts` — add `editor-buffers` branch in
  `tabToUrl`
- `spa/src/lib/pane-labels.ts` — add `editor-buffers` branches in
  `getPaneLabel` + `getPaneIcon`
- `spa/src/lib/new-tab-registry.ts` — add optional `moduleId?: string`
  to `NewTabProvider`
- `spa/src/components/NewTabPage.tsx` — module-aware filter on
  provider list
- `spa/src/lib/register-modules.tsx` — add pane kind + NewTab
  provider with `moduleId: 'editor'`; update existing `editor` NewTab
  provider to also set `moduleId: 'editor'`
- `spa/src/components/settings/EditorPurdexSettingsSection.tsx` —
  remove transitional `<BufferListSection />` + its import
- `spa/src/lib/register-modules.test.ts` — add N2-1, N2-2

Delete:
- `spa/src/components/editor/BufferListSection.tsx`

Add i18n keys:
- `newTab.editor.buffers.label`, `editor.buffers.tab_title`,
  `editor.buffers.empty`, `editor.buffers.new`, `editor.buffers.rename`,
  `editor.buffers.delete`, `editor.buffers.open`,
  `editor.buffers.confirm_delete`, `editor.buffers.rename_slash_error`
  (both locales).

### 4.2 TDD steps

**Step 1: PaneContent union + P2-1**

Action: add `| {kind:'editor-buffers'}` to union.

Test P2-1 passes immediately (`createTab` is generic).

---

**Step 2: Ancillary integration tests A2-1..A2-4 (all fail first)**

Create / extend test files for:
- A2-1: `route-utils.test.ts` — `tabToUrl({layout: pane({kind:'editor-buffers'})})` returns deterministic URL.
- A2-2 + A2-3: `pane-labels.test.ts` — `getPaneLabel`, `getPaneIcon` handle new kind.
- A2-4: `NewTabPage.test.tsx` — with `useModuleEnabledStore.setState({enabled:{editor:false}}, false)`, grid excludes `editor-buffers` and `editor` provider cards.

Expected: all 4 fail (switches don't know the new kind; filter does
not exist).

---

**Step 3: Implement ancillary integration**

- `route-utils.ts`: add `case 'editor-buffers': return '/editor/buffers'` (or matching existing convention).
- `pane-labels.ts` getPaneLabel: add `case 'editor-buffers': return t('editor.buffers.tab_title')`.
- `pane-labels.ts` getPaneIcon: add `case 'editor-buffers': return 'Stack'`.
- `new-tab-registry.ts`: add optional `moduleId?: string` to
  `NewTabProvider`. Keep registration API backward-compatible.
- `NewTabPage.tsx:27-60`: wrap `getNewTabProviders()` consumer with
  a filter that excludes providers whose `moduleId` is in a disabled
  module. Pseudocode:
  ```
  // const enabledStore = useModuleEnabledStore()
  // const providers = getNewTabProviders().filter(p =>
  //   !p.moduleId || enabledStore.isEnabled(p.moduleId))
  ```

Postcondition: A2-1..A2-4 pass.

---

**Step 4: Write failing B2-1..B2-10**

Create `EditorBuffersPane.test.tsx`:
```
// Mocks: fs-backend (list/write/delete/rename), useEditorStore,
//        useTabStore (controllable tabs + spies on setPaneContent,
//        setActiveTab, addTab)

// B2-1 empty: list=[] -> empty-state text
// B2-2 list: backend.list returns 3 -> 3 rows, alphabetical order
// B2-3 delete (single): select 1 + click Delete -> backend.delete called
// B2-4 new: click New -> backend.write('/buffer/Untitled*.md','') + refresh
// B2-5 rename slash rejected: RenamePopover input='drafts/foo.md' -> validator error; backend.rename NOT called
// B2-6 rename flat: input='bar.md' -> backend.rename called with old + '/buffer/bar.md'
// B2-7 smart-open active tab editor pane:
//   seed tabStore: activeTabId='TA', tabs.TA.layout has pane {id:'P1', content:{kind:'editor',...}}
//   select row + Open -> setPaneContent('TA','P1', expected editor content) + setActiveTab('TA'); addTab not called
// B2-8 smart-open other tab editor pane:
//   seed: activeTabId='TA' with no editor pane; tabOrder=['TA','TB'] where TB has editor pane P2
//   select + Open -> setPaneContent('TB','P2', ...) + setActiveTab('TB')
// B2-9 smart-open fallback new tab:
//   seed: no editor panes anywhere
//   select + Open -> addTab called with {kind:'editor', source:{type:'inapp'}, filePath:'/buffer/...'}
// B2-10 delete resets open panes BEFORE backend.delete:
//   seed: 2 tabs each with editor pane pointing to '/buffer/x.md'
//   select '/buffer/x.md' + Delete -> assertions:
//     setPaneContent called twice with filePath:null BEFORE mockBackend.delete is called
//   (verify ordering via mock.instances / vi.spyOn.getMockCall sequence)
```

Expected: 10 fail (pane doesn't exist).

---

**Step 5: Implement EditorBuffersPane**

Action: `spa/src/components/editor/EditorBuffersPane.tsx`:
```
// PaneRendererProps { pane }
// State: files, selected (Set), renameTarget, loading, error, refreshKey

// useEffect (mount + refreshKey): load
//   backend = getFsBackend({type:'inapp'})
//   if !backend: set error
//   raw = await backend.list('/buffer')
//   files = raw.filter(e=>!e.isDir).sort((a,b) => a.name.localeCompare(b.name))

// Toolbar: New | Rename (enabled iff selected.size===1) | Delete (enabled iff >=1) | Open (iff ===1)

// New: backend.write('/buffer/'+uniqueUntitled, new Uint8Array(0)); refresh
// Rename: open RenamePopover
//   validateName: if name.includes('/') return t('editor.buffers.rename_slash_error')
//   onConfirm: backend.rename(oldPath, '/buffer/'+name); refresh; clear selection
// Delete (helper fn deleteWithPaneReset):
//   const targets = Array.from(selected).map(n => '/buffer/'+n)
//   if selected.size > 1 and not window.confirm(...): return
//   // Step 1: reset every open editor pane pointing at any deleted path
//   const tabs = useTabStore.getState().tabs
//   for (const [tabId, tab] of Object.entries(tabs)):
//     for each leaf in scanPaneTree(tab.layout):
//       if leaf.content.kind==='editor' && leaf.content.source?.type==='inapp'
//          && targets.includes(leaf.content.filePath):
//         useTabStore.getState().setPaneContent(tabId, leaf.id,
//           {kind:'editor', source:{type:'inapp'}, filePath: null})
//   // Step 2: actual deletion
//   for (const path of targets): await backend.delete(path)
//   refresh; clear selection
// Open (smartOpen helper):
//   const path = '/buffer/'+selectedName
//   const newContent = {kind:'editor', source:{type:'inapp'}, filePath: path}
//   const {tabs, tabOrder, activeTabId} = useTabStore.getState()
//   const findFirstEditorPane = (tab) => {
//     for (const leaf of scanPaneTree(tab.layout)):
//       if leaf.content.kind === 'editor': return leaf.id
//     return null
//   }
//   // Rule 1: active tab
//   if (activeTabId && tabs[activeTabId]):
//     const pid = findFirstEditorPane(tabs[activeTabId])
//     if pid: setPaneContent(activeTabId, pid, newContent); setActiveTab(activeTabId); return
//   // Rule 2: tabOrder scan
//   for (const tid of tabOrder):
//     if tid === activeTabId: continue
//     const pid = findFirstEditorPane(tabs[tid])
//     if pid: setPaneContent(tid, pid, newContent); setActiveTab(tid); return
//   // Rule 3: new tab
//   const newTab = createTab(newContent)
//   addTab(newTab); setActiveTab(newTab.id)

// Empty / loading / error states
```

Postcondition: B2-1..B2-10 pass.

---

**Step 6: ManageBuffersNewTabCard + N2-3**

Action: `ManageBuffersNewTabCard.tsx`:
```
// Props: { onSelect: (content: PaneContent) => void }
// onClick: onSelect({kind:'editor-buffers'})
// Renders: card button with Stack icon + label t('newTab.editor.buffers.label')
```

Test N2-3: render with onSelect spy; click; assert called with
`{kind:'editor-buffers'}` — confirms it does NOT touch
`useTabStore.openSingletonTab` directly.

---

**Step 7: Register pane kind + NewTab provider + moduleId on existing**

Action in `register-modules.tsx`:
- Add `{kind:'editor-buffers', component:EditorBuffersPane}` to editor
  panes array.
- Existing `editor` NewTab provider: set `moduleId: 'editor'`.
- New provider: `{id:'editor-buffers', label:'newTab.editor.buffers.label', icon:'Stack', order:6, component:ManageBuffersNewTabCard, moduleId:'editor'}`.

Add tests N2-1, N2-2 to register-modules.test.ts.

---

**Step 8: Remove transitional BufferListSection + delete file**

- Edit `EditorPurdexSettingsSection.tsx`: remove import +
  `<BufferListSection />` usage.
- Delete `spa/src/components/editor/BufferListSection.tsx`.
- Grep `spa/src` for any remaining import; fix or flag.

---

**Step 9: i18n**

Add listed keys to both locale files.

### 4.3 Acceptance (mirrors spec §7 Commit 2)

Verified by tests P2-1, B2-1..B2-10, N2-1..N2-3, A2-1..A2-4
passing, plus:
- `grep -r BufferListSection spa/src` returns empty.
- `cd spa && pnpm run build` produces no type errors on the union
  extension or the switch additions.

### 4.4 Verification commands
```
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run lint
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && npx vitest run
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run build
```

---

## 5. Commit 3 — Breadcrumb popover

### 5.1 Files touched

Create:
- `spa/src/components/editor/BreadcrumbPopover.tsx`
- `spa/src/components/editor/BreadcrumbPopover.test.tsx`
- `spa/src/components/editor/EditorToolbar.test.tsx`

Modify:
- `spa/src/components/editor/EditorToolbar.tsx`
- `spa/src/components/editor/EditorPane.tsx` — wire callbacks down

### 5.2 TDD steps

**Step 1: Failing tests C3-1..C3-5, T3-1, T3-2**

Create `BreadcrumbPopover.test.tsx`:
```
// Props: { buffers: string[], currentBufferKey: string,
//          onSwitch(key), onManage(), onDismiss(),
//          anchorRect: DOMRect, onNewBuffer? }
// Mock createPortal: vi.mock('react-dom', async o => ({...await o(),
//                                                      createPortal: n => n}))

// C3-1: buffers=['a','b','c'] -> 3 items
// C3-2: item matching currentBufferKey has aria-current='true'
// C3-3: click non-current -> onSwitch called with full path
// C3-4: keydown Escape -> onDismiss
// C3-5: click 'Manage buffers' -> onManage
```

Create `EditorToolbar.test.tsx`:
```
// T3-1: source.type='inapp' -> Purdex rendered as <button>
// T3-2: source.type='daemon' -> no Purdex text or chip present
```

Expected: 7 fail.

---

**Step 2: Implement BreadcrumbPopover**

Action: `spa/src/components/editor/BreadcrumbPopover.tsx`:
```
// containerRef + useClickOutside(containerRef, onDismiss)
// useEffect: document keydown Escape -> onDismiss
// ReactDOM.createPortal(content, document.body)
// Position: fixed, below anchorRect, clamped to viewport
// z-index: 100
// Content:
//   <ul>
//     for each buffer name:
//       <li><button onClick={() => { onSwitch('/buffer/'+name); onDismiss() }}>
//         name + (Check icon if fullPath===currentBufferKey)
//       </button></li>
//   </ul>
//   <divider/>
//   <button onClick={() => { onManage(); onDismiss() }}>Manage buffers...</button>
//   if buffers.length===0: 'No buffers yet.' + optional [New buffer]
//   if onNewBuffer provided
```

Postcondition: C3-1..C3-5 pass.

---

**Step 3: Update EditorToolbar**

Action: Edit `EditorToolbar.tsx`:
- Add optional props: `onBufferSwitch?(key)`, `onManage?()`,
  `onNewBuffer?()`.
- State: `showPopover`, `popoverAnchorRect`, `bufferList`.
- Replace the inapp chip's `<span>` with `<button>`:
  ```
  // onClick: capture rect from currentTarget.getBoundingClientRect();
  //          load bufferList via InAppBackend.list('/buffer');
  //          setShowPopover(true)
  ```
- When `showPopover && popoverAnchorRect`, render `<BreadcrumbPopover>`
  with wired callbacks that dismiss after each action.
- Preserve existing `showInAppPrefix` gate.

Postcondition: T3-1, T3-2 pass.

---

**Step 4: Wire EditorPane callbacks**

Action: Edit `EditorPane.tsx` where `<EditorToolbar />` is rendered.
Pass callbacks:
- `onBufferSwitch={(newKey) => {`
    `  const tabId = findTabIdForPane(pane.id)`
    `  const newContent = {kind:'editor', source:{type:'inapp'}, filePath: newKey}`
    `  useTabStore.getState().setPaneContent(tabId, pane.id, newContent)`
    `  useEditorStore.getState().attachPane(pane.id, newKey)  // same-pane binding`
  `}`
- `onManage={() => useTabStore.getState().openSingletonTab({kind:'editor-buffers'})}`
- `onNewBuffer={async () => {`
    `  const path = '/buffer/Untitled-'+Date.now()+'.md'`
    `  await getFsBackend({type:'inapp'})?.write(path, new Uint8Array(0))`
    `  // then same setPaneContent + attachPane sequence as onBufferSwitch`
  `}`

Note the setPaneContent + attachPane sequence — spec §4.8 requires
both to avoid flash-remount.

Test coverage: build + lint. No direct unit test for EditorPane
wiring in this plan (pane rendering has extensive Monaco dependencies
that are heavy to mock).

### 5.3 Acceptance (mirrors spec §7 Commit 3)

Verified by tests C3-*, T3-* passing, plus:
- Manual visual check: inapp buffer editor shows clickable Purdex
  chip; popover opens below; Escape + outside-click dismiss; Manage
  link opens singleton buffers tab.
- `grep openSingletonTab spa/src/components/editor/ManageBuffersNewTabCard.tsx`
  returns empty (the card does NOT call it — only the popover
  manage link does, via `useTabStore`).

### 5.4 Verification commands
```
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run lint
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && npx vitest run
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run build
```

---

## 6. Cross-commit dependencies

**Commit 2 → Commit 1:**
- `EditorPurdexSettingsSection` exists (C1), so C2 can remove its
  transitional `<BufferListSection />`.
- `registerSettingsSection({id:'editor-buffers',...})` deleted (C1),
  preventing dual registration when the new pane kind lands.
- `SettingsPage` alias (C1) lets bookmarked `/settings/editor-buffers`
  URLs continue to resolve once the legacy section is gone.

**Commit 3 → Commit 2:**
- `{kind:'editor-buffers'}` in union (C2) — required for
  `openSingletonTab({kind:'editor-buffers'})` in the popover's
  Manage handler.
- `EditorBuffersPane` registered as pane renderer (C2) — required
  so the Manage singleton tab has a renderer.
- `tabToUrl` + `getPaneLabel` + `getPaneIcon` (C2) — tab bar shows
  correct icon/label for the buffers management tab opened from
  the popover.

**Independent of commit order:**
- `useEditorStore.attachPane` (unchanged from base) used by C2 and C3.
- `useTabStore.setPaneContent` (unchanged from base) used by C2 and C3.

---

## 7. Implementation risks (plan-level)

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| Zustand persist test isolation polluting between cases | Medium | `beforeEach`: `localStorage.clear()` + `setState(DEFAULTS, false)` merge-mode. Mock `syncManager` via `vi.hoisted` (same pattern as `useModuleEnabledStore.test.ts`). Do NOT `vi.mock('zustand/middleware')` — breaks the store. |
| React portal in BreadcrumbPopover breaks jsdom | Medium | `vi.mock('react-dom', async o => ({...await o(), createPortal: n => n}))` at top of BreadcrumbPopover test. |
| `window.confirm` in multi-delete untestable by default | Medium | `window.confirm = vi.fn(() => true)` in `beforeEach`; B2-10's multi-delete path covered under this mock. Single-delete path needs no confirm. |
| `useTabStore.setPaneContent` vs `attachPane` call ordering in popover causes flash-remount | Medium | Spec §4.8 requires setPaneContent first, then attachPane within the same microtask (same synchronous call frame). Plan step 5 of C3 wires them in that order. |
| Smart-open tests need layout-tree seeding that matches `scanPaneTree` | Medium | Use `createTab({kind:'editor', ...})` to build test tabs (gives valid layout), then inject into `useTabStore.setState({tabs, tabOrder, activeTabId}, false)`. Validated by reading `useTabStore.test.ts` patterns. |
| `tabToUrl` path convention for `editor-buffers` unclear | Low | Step 3 of C2 greps existing cases (`editor`, `sessions`, `browser`) to derive the pattern. Deterministic URL is required for the L1-1-style bookmark test (not included in C2 — URL is internal). |
| `NewTabProvider.moduleId` field causes existing consumers to misbehave | Low | Field is optional. Plan step 3 of C2 keeps `getNewTabProviders()` return shape identical; the filter is at consumer site (`NewTabPage`). Legacy providers with no `moduleId` pass the filter unconditionally. |
| `EditorToolbar` new optional callback props break existing callers | Low | All three `?`-optional. Existing `EditorToolbar` consumers (non-EditorPane) work unchanged. |
| Delete-open-buffer ordering: `setPaneContent` must happen BEFORE `backend.delete` so EditorPane doesn't momentarily read a deleted file | High (correctness) | B2-10 asserts call ordering. Use `vi.spyOn` sequence OR a per-mock `order` counter. If order violates, EditorPane briefly hits "file not found" in `getFsBackend(...).read(path)` → React error. |
| `register-modules.test.ts` line 331/393 edits collide with test fixture ordering | Low | Read full test before editing; two assertions are in separate `it()` blocks. |
| Monaco re-reading options prop causes full editor remount | Low | `@monaco-editor/react@4.7.0` uses `editor.updateOptions` via prop diff; confirmed in Round-1 codex review. |

---

## 8. Spec questions (remaining — plan cannot resolve)

All major architectural / correctness questions from plan v1.0 have
been resolved in spec v1.1. Remaining items:

1. **Exact URL produced by `tabToUrl({kind:'editor-buffers'})`** —
   plan step 3 of C2 picks it after greping existing convention.
   Likely `/editor/buffers` or `/buffers`. Not user-visible in this
   PR, so low stakes.
2. **Multi-delete modal vs `window.confirm`** — plan defaults to
   `confirm()` for simplicity; spec open question §8.3 flags a
   potential follow-up.
3. **`moduleId` rollout to sessions/browser NewTab providers** —
   spec §8.5 defers; plan default is editor-only for this PR.

---

## 9. Done definition

PR ready to merge when:
- [ ] All 3 commits pass `cd spa && pnpm run lint && npx vitest run &&
      pnpm run build` independently (green on each HEAD, not just the
      final merge).
- [ ] All 36 Vitest cases green (no skipped / pending / .todo).
- [ ] Spec v1.1 §7 acceptance lists for C1/C2/C3 fully ticked.
- [ ] `grep -r editor_buffers spa/src` empty (except migration guards
      if any).
- [ ] `grep -r BufferListSection spa/src` empty (post Commit 2).
- [ ] Round-2 codex plan review complete with no critical findings.
- [ ] Round-3 codex review rounds (standard + adversarial 3-way) on
      the PR diff complete; all HIGH-confidence findings addressed
      or tracked as follow-up issues.
- [ ] PR description lists any follow-up issues opened during review
      (at minimum: `InAppBackend.rename` subfolder patch, Tiptap
      fontSize integration).

*End of plan v1.1.*

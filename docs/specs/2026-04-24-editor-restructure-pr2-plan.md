# Editor Restructure (PR 2) — Plan

- Spec: `docs/specs/2026-04-24-editor-restructure-pr2-spec.md` v1.0
- Base commit: `bb5ce0c1` (main @ alpha.216)
- Plan date: 2026-04-24
- Target version: 1.0.0-alpha.217

## 0. Orientation

### Spec reference
Spec version 1.0, dated 2026-04-24. Base commit `bb5ce0c1`. Three-commit PR targeting alpha.217.

### Out of scope (mirroring spec §2)
- No changes to IndexedDB / InAppBackend storage layer internals.
- No new buffer features (versioning, import/export, search-in-buffers).
- No changes to EditorPane rendering logic (only toolbar chip and the store behind Monaco options).
- No cleanup of `globalConfig` / `workspaceConfig` / `ModuleConfigSection` (tracked as #618).
- No Tiptap settings beyond `fontSize` via CSS variable.
- No HSR schema changes.

### Verification toolchain summary
All commands run from `spa/` directory inside the worktree:
`cd spa && pnpm run lint && npx vitest run && pnpm run build`

---

## 1. Current-state verification

Anchors confirmed accurate as of base commit `bb5ce0c1`:

- `spa/src/lib/register-modules.tsx:176-202` — Editor module `registerModule({id:'editor',...})` with `panes: [EditorPane, ImagePreviewPane, PdfPreviewPane]` and `settings: [{localId:'workspace-home-path',...},{localId:'host-home-path',...}]`. No `localId:'editor'` entry exists yet.
- `spa/src/lib/register-modules.tsx:323-328` — Legacy `registerSettingsSection({id:'editor-buffers', label:'settings.section.editor_buffers', order:9, component:BufferListSection})`. This is the call to delete.
- `spa/src/lib/register-modules.tsx:354-370` — Three `registerNewTabProvider` calls: `sessions` (order:0), `editor` (order:5), `browser` (order:-10). The new `editor-buffers` provider will use `order:6` to sit immediately after `editor`.
- `spa/src/lib/settings-contribution-types.ts:46-55` — `SettingsContributionDeclaration<S>` has `localId`, `scope`, `order`, `labelKey`, `component`, optional `disabled`, `disabledReasonKey`.
- `spa/src/lib/settings-contribution-types.ts:82-84` — `isModuleOwnedContribution(c)` returns `!c.moduleId.startsWith('_builtin.')`.
- `spa/src/components/editor/MonacoWrapper.tsx:67-75` — Hardcoded options: `minimap:{enabled:true}`, `fontSize:13`, `lineNumbers:'on'`, `wordWrap:'on'`, `scrollBeyondLastLine:false`, `automaticLayout:true`. No `tabSize` or `insertSpaces` present (Monaco defaults apply). All six will move to store.
- `spa/src/components/editor/EditorToolbar.tsx:19,27-39` — `showInAppPrefix = source.type === 'inapp'`; the Purdex chip is a `<span>` inside a `<>` fragment. Lines 27-39 are the chip block. Commit 3 wraps this in `<button>`.
- `spa/src/components/editor/BufferListSection.tsx` — Standalone component, no props. Uses `getFsBackend({type:'inapp'})` and `backend.list('/buffer')`. Delete in Commit 2.
- `spa/src/stores/useEditorStore.ts:107-135` — `attachPane(paneId, bufferKey)` creates new pane state or swaps buffer key, cleans up orphaned buffers. Correct API for smart-open swap.
- `spa/src/stores/useTabStore.ts:115,173-189` — `addTab(tab, afterTabId?)` and `openSingletonTab(content)` confirmed. `openSingletonTab` scans `tabOrder` via `getPrimaryPane`.
- `spa/src/lib/pane-tree.ts:41-47` — `scanPaneTree(layout, fn)` walks all leaves. Also available: `collectLeaves(layout)` at line 107-110.
- `spa/src/lib/fs-backend-inapp.ts:105-110` — `rename(from,to)` does NOT auto-create intermediate dirs (it only `store.set(to,...)` then `store.delete(from)`). The `write()` method does auto-create. RISK: subfolder rename target will silently succeed in the store map but the parent dir entry will be missing. Plan flags this as a prerequisite gap — see §8 Spec questions.
- `spa/src/lib/fs-backend-inapp.ts:62-84` — `list(path)` returns only DIRECT children (skips paths with `/` in the rest). So `/buffer/drafts/foo.md` does NOT appear in `list('/buffer')`. `EditorBuffersPane` must either recursively list or document this limitation.
- `spa/src/lib/storage/keys.ts` — `STORAGE_KEYS` object (const). Add `EDITOR_SETTINGS: 'purdex-editor-settings'`.
- `spa/src/locales/en.json:139` — `"settings.section.editor_buffers": "Editor Buffers"`. Delete this line; add `"settings.section.editor": "Editor"`.
- `spa/src/locales/zh-TW.json:139` — `"settings.section.editor_buffers": "編輯器暫存"`. Delete; add `"settings.section.editor": "編輯器"`.
- `spa/src/lib/register-modules.test.ts:329,331,393` — `'editor-buffers'` appears in the always-on list assertion and the legacy-view assertion. Both must be updated in Commit 1.
- `spa/src/types/tab.ts:36-47` — `PaneContent` union: 11 members. Add `{kind:'editor-buffers'}` as 12th member in Commit 2.
- `spa/src/components/RenamePopover.tsx` — `anchorRect:DOMRect`, `currentName`, `onConfirm:(name)=>Promise<void>`, `onCancel:()=>void`, `validateName?`. Uses `useClickOutside` + `useLayoutEffect` for positioning. `position:fixed`, z-index `z-50`. Breadcrumb popover must use z-index > 50.
- `spa/src/hooks/useClickOutside.ts` — `useClickOutside(ref, handler)` listens on `mousedown`.
- `spa/src/stores/useModuleEnabledStore.ts` — zustand+persist pattern template: `create<State>()(persist(...))` with `purdexStorage`, `version:1`, `merge` sanitizer, `partialize`. Its test file shows merge-mode `setState({...})` pattern for test resets.

---

## 2. TDD test matrix

Total: 31 tests across 3 commits.

| ID | File | Test name | Asserts (one-line) | Commit |
|----|------|-----------|-------------------|--------|
| S1-1 | `stores/useEditorSettingsStore.test.ts` | defaults match spec | all 6 fields equal spec defaults | 1 |
| S1-2 | `stores/useEditorSettingsStore.test.ts` | setFontSize clamps below 10 | `setFontSize(5)` -> `fontSize===10` | 1 |
| S1-3 | `stores/useEditorSettingsStore.test.ts` | setFontSize clamps above 24 | `setFontSize(30)` -> `fontSize===24` | 1 |
| S1-4 | `stores/useEditorSettingsStore.test.ts` | setFontSize NaN falls back to 13 | `setFontSize(NaN)` -> `fontSize===13` | 1 |
| S1-5 | `stores/useEditorSettingsStore.test.ts` | reset restores all defaults | after mutation, `reset()` -> all fields back to defaults | 1 |
| S1-6 | `stores/useEditorSettingsStore.test.ts` | persists under EDITOR_SETTINGS key | after `setWordWrap('off')`, localStorage has key `purdex-editor-settings` | 1 |
| S1-7 | `stores/useEditorSettingsStore.test.ts` | merge sanitizes unknown fields | rehydrate with `{tabSize:99, wordWrap:'maybe'}` -> defaults for bad values | 1 |
| S1-8 | `stores/useEditorSettingsStore.test.ts` | merge tolerates null/missing persisted state | rehydrate with `null` -> no throw, all defaults | 1 |
| R1-1 | `lib/register-modules.test.ts` | editor module settings has 3 entries | `editor` module's `settings.length === 3` after `registerBuiltinModules()` | 1 |
| R1-2 | `lib/register-modules.test.ts` | editor.editor contribution dispatched as purdex scope | `getContribution('editor.editor')` defined with `scope:'purdex'` | 1 |
| R1-3 | `lib/register-modules.test.ts` | editor-buffers no longer in legacy sections | `getSettingsSections().find(s=>s.id==='editor-buffers')` is undefined | 1 |
| R1-4 | `lib/register-modules.test.ts` | settings.section.editor i18n key present | both locales have the key | 1 |
| R1-5 | `lib/register-modules.test.ts` | isModuleOwnedContribution true for editor.editor | `isModuleOwnedContribution(...)` is true | 1 |
| P2-1 | `types/tab.test.ts` | editor-buffers is a valid PaneContent kind | `createTab({kind:'editor-buffers'})` does not throw; kind survives round-trip | 2 |
| B2-1 | `components/editor/EditorBuffersPane.test.tsx` | renders empty state when no buffers | given empty InAppBackend, renders empty-state text | 2 |
| B2-2 | `components/editor/EditorBuffersPane.test.tsx` | lists files returned by backend | given backend with 2 files, renders 2 rows | 2 |
| B2-3 | `components/editor/EditorBuffersPane.test.tsx` | delete button calls backend.delete | click delete -> `mockBackend.delete` called with correct path | 2 |
| B2-4 | `components/editor/EditorBuffersPane.test.tsx` | new button creates file and refreshes | click New -> `mockBackend.write` called with `/buffer/Untitled.md` and list refreshes | 2 |
| B2-5 | `components/editor/EditorBuffersPane.test.tsx` | rename triggers RenamePopover | select one row, click Rename -> popover renders with current name | 2 |
| B2-6 | `components/editor/EditorBuffersPane.test.tsx` | open with existing editor pane calls attachPane | smart-open: tabStore has editor pane -> `attachPane` called, no new tab added | 2 |
| B2-7 | `components/editor/EditorBuffersPane.test.tsx` | open with no editor pane adds new tab | smart-open: tabStore has no editor pane -> `addTab` called | 2 |
| N2-1 | `lib/register-modules.test.ts` | editor-buffers NewTab provider registered | `getNewTabProviders().find(p=>p.id==='editor-buffers')` defined | 2 |
| N2-2 | `lib/register-modules.test.ts` | editor-buffers provider order is after editor | `editor-buffers` order > `editor` order | 2 |
| N2-3 | `components/editor/ManageBuffersNewTabCard.test.tsx` | card click calls onSelect with kind:editor-buffers | click card -> `onSelect({kind:'editor-buffers'})` called | 2 |
| C3-1 | `components/editor/BreadcrumbPopover.test.tsx` | renders buffer list | given 3 buffers, renders 3 items | 3 |
| C3-2 | `components/editor/BreadcrumbPopover.test.tsx` | current buffer marked | item matching currentBufferKey has check-icon / aria-current | 3 |
| C3-3 | `components/editor/BreadcrumbPopover.test.tsx` | click buffer calls onSwitch | click non-current item -> `onSwitch` called with that key | 3 |
| C3-4 | `components/editor/BreadcrumbPopover.test.tsx` | Escape key dismisses | keydown Escape -> `onDismiss` called | 3 |
| C3-5 | `components/editor/BreadcrumbPopover.test.tsx` | manage link calls onManage | click "Manage buffers" -> `onManage` called | 3 |
| T3-1 | `components/editor/EditorToolbar.test.tsx` | inapp source renders button not span | given `source.type='inapp'`, Purdex chip is a `<button>` element | 3 |
| T3-2 | `components/editor/EditorToolbar.test.tsx` | non-inapp source keeps span | given `source.type='daemon'`, Purdex chip absent or rendered as `<span>` | 3 |

---

## 3. Commit 1 — EditorSettingsStore + HSR migration

### 3.1 Files touched

Create:
- `spa/src/stores/useEditorSettingsStore.ts`
- `spa/src/stores/useEditorSettingsStore.test.ts`
- `spa/src/components/settings/EditorPurdexSettingsSection.tsx`

Modify:
- `spa/src/lib/storage/keys.ts` — add `EDITOR_SETTINGS` key
- `spa/src/lib/register-modules.tsx` — add `localId:'editor'` settings entry; remove legacy `registerSettingsSection({id:'editor-buffers',...})`
- `spa/src/components/editor/MonacoWrapper.tsx` — replace hardcoded options object with store subscription
- `spa/src/locales/en.json` — add `settings.section.editor`; remove `settings.section.editor_buffers`
- `spa/src/locales/zh-TW.json` — same
- `spa/src/lib/register-modules.test.ts` — update assertions at lines 331/393; add R1-1..R1-5

Delete:
- nothing yet (BufferListSection.tsx deleted in Commit 2)

### 3.2 TDD steps

**Step 1: Add EDITOR_SETTINGS key**

Precondition: `STORAGE_KEYS` has no `EDITOR_SETTINGS`.

Action: Add `EDITOR_SETTINGS: 'purdex-editor-settings'` to the `STORAGE_KEYS` object (follows existing `purdex-` prefix convention).

Postcondition: `STORAGE_KEYS.EDITOR_SETTINGS` imports successfully.

---

**Step 2: Write failing tests S1-1 through S1-8**

Precondition: Store file does not exist yet.

Action: Create `spa/src/stores/useEditorSettingsStore.test.ts`:
```
// Import useEditorSettingsStore (fails until Step 3)
// beforeEach: reset store via merge-mode setState + localStorage.clear()
// Mock syncManager with vi.hoisted (same pattern as useModuleEnabledStore.test.ts)

// S1-1: fresh state, all defaults match spec
//   tabSize===2, insertSpaces===true, wordWrap==='on',
//   lineNumbers==='on', minimap===true, fontSize===13

// S1-2: setFontSize(5) -> 10
// S1-3: setFontSize(30) -> 24
// S1-4: setFontSize(NaN) -> 13
// S1-5: mutate all -> reset() -> defaults
// S1-6: setWordWrap('off') -> localStorage.getItem('purdex-editor-settings') contains wordWrap:'off'
// S1-7: rehydrate with {tabSize:99, wordWrap:'maybe', fontSize:'big'} -> invalids overridden to defaults
// S1-8: rehydrate with {state:null, version:1} -> no throw, all defaults
```

Expected: 8 tests fail ("Cannot find module").

---

**Step 3: Implement useEditorSettingsStore**

Action: Create `spa/src/stores/useEditorSettingsStore.ts`:
```
// interface EditorSettingsState:
//   tabSize: 2|4|8 = 2
//   insertSpaces: boolean = true
//   wordWrap: 'on'|'off' = 'on'
//   lineNumbers: 'on'|'off' = 'on'
//   minimap: boolean = true
//   fontSize: number = 13
//   + setters + reset

// DEFAULT_EDITOR_SETTINGS constant used by reset() and merge fallback

// setFontSize clamp:
//   if !Number.isFinite(v) -> DEFAULT.fontSize
//   else Math.min(24, Math.max(10, Math.round(v)))

// merge: per-field type/range validation; fall back to DEFAULT on invalid

// persist config:
//   name: STORAGE_KEYS.EDITOR_SETTINGS
//   storage: purdexStorage
//   version: 1
//   partialize: identity
//   merge: sanitize function
```

Postcondition: S1-1..S1-8 pass.

---

**Step 4: Update MonacoWrapper to read store**

Action: Modify `spa/src/components/editor/MonacoWrapper.tsx`:
- Add `useEditorSettingsStore` import
- Destructure all 6 fields at top of component
- Replace `options={{...}}` with store-driven values:
  - `minimap: {enabled: minimap}`
  - `fontSize` (from store)
  - `lineNumbers` (from store)
  - `wordWrap` (from store)
  - `tabSize` (new, from store)
  - `insertSpaces` (new, from store)
  - Keep `scrollBeyondLastLine:false`, `automaticLayout:true`

No direct unit test (Monaco requires ResizeObserver / real DOM); covered by build + lint.

---

**Step 5: Create EditorPurdexSettingsSection (transitional)**

Action: Create `spa/src/components/settings/EditorPurdexSettingsSection.tsx`:
```
// Props: { ctx: SettingsContextFor<'purdex'> }  // ctx unused, reserved
// Reads from useEditorSettingsStore
// Sections:
//   Indentation: Tab size <select> (2|4|8), Insert spaces <toggle>
//   Display: Word wrap, Line numbers, Minimap toggles + Font size <input number>
// Below: transitional <BufferListSection /> (Commit 1 only)
// Note: Monaco vs Tiptap support
```

---

**Step 6: HSR migration in register-modules.tsx**

Action (3 changes in one file):

A. Prepend new entry to editor module settings array:
```
// settings: [
//   { localId: 'editor', scope: 'purdex', order: 9,
//     labelKey: 'settings.section.editor',
//     component: EditorPurdexSettingsSection },
//   { localId: 'workspace-home-path', ... },
//   { localId: 'host-home-path', ... },
// ]
```

B. Delete the legacy `registerSettingsSection({id:'editor-buffers',...})` block (lines 323-328). Remove `BufferListSection` import (line 38) only if no longer referenced in this file.

C. Add import for `EditorPurdexSettingsSection`.

---

**Step 7: i18n rename**

Action:
- `en.json`: delete `"settings.section.editor_buffers": "Editor Buffers"`; add `"settings.section.editor": "Editor"` near other section keys.
- `zh-TW.json`: delete `"settings.section.editor_buffers": "編輯器暫存"`; add `"settings.section.editor": "編輯器"`.

---

**Step 8: Update register-modules.test.ts + add R1-1..R1-5**

Action:
- Line 331 array: remove `'editor-buffers'` (now `['appearance','terminal','interface','sync','module-config']`).
- Line 393 array: same removal from `legacyView` assertion.
- Append new `describe('Commit 1: Editor module HSR migration')` with R1-1..R1-5:
```
// R1-1: after registerBuiltinModules(), editor module settings.length === 3
// R1-2: getContribution('editor.editor').scope === 'purdex'
// R1-3: getSettingsSections().find(s=>s.id==='editor-buffers') === undefined
// R1-4: both en and zh-TW JSON have 'settings.section.editor' key
// R1-5: isModuleOwnedContribution(getContribution('editor.editor')) === true
```

Postcondition: All 13 Commit-1 tests pass; no existing tests regress.

### 3.3 Acceptance (mirrors spec §7 Commit 1)

- [ ] `useEditorSettingsStore` with 6 persisted fields + sane defaults + clamp behaviors.
- [ ] `EditorPurdexSettingsSection` renders 6 controls bound to the store.
- [ ] `MonacoWrapper` reads all 6 options from store (no hardcoded values).
- [ ] Editor module `settings` has 3 entries; `localId:'editor'` first (`order:9`).
- [ ] Legacy `registerSettingsSection({id:'editor-buffers',...})` removed.
- [ ] i18n: `settings.section.editor` added (both locales); `settings.section.editor_buffers` removed (both).
- [ ] `register-modules.test.ts` line 331/393 updated; R1-1..R1-5 pass.
- [ ] Settings sidebar "Editor" row shows puzzle-piece marker (verified manually or via isModuleOwnedContribution).
- [ ] `<BufferListSection />` rendered inside `EditorPurdexSettingsSection` as transitional.
- [ ] lint + vitest + build all green.

### 3.4 Verification commands

```
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run lint
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && npx vitest run
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run build
```

---

## 4. Commit 2 — EditorBuffersPane + NewTab entry

### 4.1 Files touched

Create:
- `spa/src/types/tab.test.ts` (if not existing)
- `spa/src/components/editor/EditorBuffersPane.tsx`
- `spa/src/components/editor/EditorBuffersPane.test.tsx`
- `spa/src/components/editor/ManageBuffersNewTabCard.tsx`
- `spa/src/components/editor/ManageBuffersNewTabCard.test.tsx`

Modify:
- `spa/src/types/tab.ts` — extend `PaneContent` union with `{kind:'editor-buffers'}`
- `spa/src/lib/register-modules.tsx` — add pane kind + NewTab provider
- `spa/src/components/settings/EditorPurdexSettingsSection.tsx` — remove transitional `<BufferListSection />` + its import
- `spa/src/lib/register-modules.test.ts` — add N2-1, N2-2
- `spa/src/locales/{en,zh-TW}.json` — add buffers-pane i18n keys

Delete:
- `spa/src/components/editor/BufferListSection.tsx` — legacy component retired

### 4.2 TDD steps

**Step 1: Extend PaneContent union + write P2-1**

Action: Add `| {kind: 'editor-buffers'}` to the `PaneContent` union (after `pdf-preview`).

Write P2-1:
```
// createTab({kind:'editor-buffers'}) returns tab where
//   layout.pane.content.kind === 'editor-buffers'
// JSON round-trip preserves kind
```

---

**Step 2: Write failing component tests B2-1..B2-7**

Action: Create `spa/src/components/editor/EditorBuffersPane.test.tsx`:
```
// Mocks:
//   vi.mock('../../lib/fs-backend') — mockBackend controls list/write/delete/rename
//   vi.mock('../../stores/useEditorStore') — spy attachPane
//   vi.mock('../../stores/useTabStore') — spy addTab, setActiveTab; controllable tabOrder/tabs

// B2-1 empty: backend.list returns [] -> empty-state text visible
// B2-2 list: backend.list returns 2 entries -> 2 rows
// B2-3 delete: click delete -> backend.delete called with '/buffer/'+name
// B2-4 new: click New -> backend.write called with '/buffer/Untitled*.md'; list refreshes
// B2-5 rename: select row + click Rename -> RenamePopover visible, currentName===entry.name
// B2-6 smart-open existing editor pane:
//   seed tabStore with tab containing pane { content:{kind:'editor'}, id:'P1' }
//   select row + click Open -> useEditorStore.attachPane('P1', '/buffer/foo.md');
//                               useTabStore.setActiveTab called; addTab NOT called
// B2-7 smart-open no editor pane:
//   seed tabStore with tab containing only non-editor panes
//   select row + click Open -> useTabStore.addTab called
```

Expected: all 7 fail.

---

**Step 3: Implement EditorBuffersPane**

Action: Create `spa/src/components/editor/EditorBuffersPane.tsx`:
```
// Pane renderer: receives PaneRendererProps { pane }
// Local state: files, selected (Set<string>), renameTarget, loading, error, refreshKey

// useEffect (mount + refreshKey): load files
//   backend = getFsBackend({type:'inapp'})
//   if (!backend) set error
//   raw = await backend.list('/buffer')
//   files = raw.filter(e=>!e.isDir).sort(mtimeDesc)

// Toolbar actions:
//   New        (always enabled)
//   Rename     (enabled iff selected.size === 1)
//   Delete     (enabled iff selected.size >= 1)
//   Open       (enabled iff selected.size === 1)

// New: backend.write('/buffer/'+uniqueUntitled, ''); refresh
// Rename: show RenamePopover anchored to row
//   validateName: reject if contains '/'  (flat-only; subfolder rename deferred until
//     InAppBackend.rename auto-creates intermediate dirs — see §8)
//   onConfirm: backend.rename(oldPath, '/buffer/'+newName); refresh; clear selection
// Delete:
//   if selected.size > 1: window.confirm(t('editor.buffers.confirm_delete', {count}))
//   for each sel: backend.delete('/buffer/'+name); refresh; clear selection
// Open (smartOpen):
//   targetPath = '/buffer/'+selectedName
//   iterate useTabStore.getState().tabOrder
//     for tabId, walk tabs[tabId].layout via scanPaneTree/collectLeaves
//       find first leaf where content.kind==='editor'
//         and content.source?.type==='inapp' (match our buffer source)
//   if found: useEditorStore.getState().attachPane(paneId, targetPath);
//             useTabStore.getState().setActiveTab(tabId)
//   else: useTabStore.getState().addTab(createTab({kind:'editor', source:{type:'inapp'}, filePath:targetPath}))

// Empty state, error state, loading state
```

Postcondition: B2-1..B2-7 pass.

---

**Step 4: Create ManageBuffersNewTabCard + test N2-3**

Action: Create `ManageBuffersNewTabCard.tsx`:
```
// Props: { onSelect: (content: PaneContent) => void }
// onClick: onSelect({kind:'editor-buffers'})
// Renders card button with Stack icon + label t('newTab.editor.buffers.label')
```

Create test N2-3: render with `onSelect` spy; click; assert called with `{kind:'editor-buffers'}`.

---

**Step 5: Register pane kind + NewTab provider**

Action: Modify `register-modules.tsx`:

A. Add `{kind:'editor-buffers', component:EditorBuffersPane}` to editor module `panes`.

B. Add after existing `editor` NewTab provider:
```
// registerNewTabProvider({
//   id: 'editor-buffers',
//   label: 'newTab.editor.buffers.label',
//   icon: 'Stack',
//   order: 6,
//   component: ManageBuffersNewTabCard,
// })
```

C. Add imports for `EditorBuffersPane` and `ManageBuffersNewTabCard`.

Add tests N2-1, N2-2 to `register-modules.test.ts`.

---

**Step 6: Remove transitional BufferListSection render + delete file**

Action:
- Modify `EditorPurdexSettingsSection.tsx`: remove `import { BufferListSection }` + remove `<BufferListSection />` from JSX. Section now renders only Monaco preferences.
- Delete `spa/src/components/editor/BufferListSection.tsx`.
- If any other file imports `BufferListSection`, fix or flag.

---

**Step 7: Add i18n keys**

Both locales:
- `newTab.editor.buffers.label` — `"Manage Buffers"` / `"管理暫存檔"`
- `editor.buffers.empty` — `"No buffers yet"` / `"尚無暫存檔"`
- `editor.buffers.new` — `"New Buffer"` / `"新增暫存檔"`
- `editor.buffers.rename` — `"Rename"` / `"重新命名"`
- `editor.buffers.delete` — `"Delete"` / `"刪除"`
- `editor.buffers.open` — `"Open"` / `"開啟"`
- `editor.buffers.confirm_delete` — `"Delete {count} buffer(s)?"` / `"刪除 {count} 個暫存檔？"`

### 4.3 Acceptance (mirrors spec §7 Commit 2)

- [ ] `PaneContent` union includes `{kind:'editor-buffers'}`.
- [ ] `EditorBuffersPane` implements list / create / rename (flat-only) / delete (single + multi) / open.
- [ ] Smart-open swaps existing editor pane's buffer; otherwise opens new tab.
- [ ] `ManageBuffersNewTabCard` registered at `order:6`; click dispatches `{kind:'editor-buffers'}`.
- [ ] `BufferListSection.tsx` deleted; no imports remain anywhere.
- [ ] `EditorPurdexSettingsSection` no longer renders `<BufferListSection />`.
- [ ] Commit message notes transitional removal.
- [ ] lint + vitest + build all green.

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
- `spa/src/components/editor/EditorToolbar.tsx` — wrap inapp chip in button trigger + popover render
- `spa/src/components/editor/EditorPane.tsx` — pass `onBufferSwitch`, `onManage`, (`paneId`) props to `EditorToolbar`

### 5.2 TDD steps

**Step 1: Write failing tests C3-1..C3-5 and T3-1, T3-2**

Action: Create `spa/src/components/editor/BreadcrumbPopover.test.tsx`:
```
// Props shape:
//   { buffers: string[], currentBufferKey: string,
//     onSwitch: (key)=>void, onManage: ()=>void, onDismiss: ()=>void,
//     anchorRect: DOMRect, onNewBuffer?: ()=>void }
// Mock createPortal: vi.mock('react-dom', (orig) => ({...orig, createPortal: (n)=>n}))

// C3-1: buffers=['a','b','c'] -> 3 items
// C3-2: item with key matching currentBufferKey has aria-current or data-current
// C3-3: click non-current item -> onSwitch called with that full path ('/buffer/a' etc.)
// C3-4: keydown Escape -> onDismiss called
// C3-5: click 'Manage buffers' button -> onManage called
```

Create `EditorToolbar.test.tsx`:
```
// T3-1: source.type='inapp' -> Purdex chip is a <button> element (byRole 'button')
// T3-2: source.type='daemon' -> no Purdex chip (showInAppPrefix===false)
```

---

**Step 2: Implement BreadcrumbPopover**

Action: Create `spa/src/components/editor/BreadcrumbPopover.tsx`:
```
// Props as above
// containerRef for useClickOutside(containerRef, onDismiss)
// useEffect: document keydown Escape -> onDismiss
// Render via ReactDOM.createPortal(content, document.body)
// Position: fixed; compute top from anchorRect.bottom + 4; left from anchorRect.left
//           clamp within viewport
// z-index: 100 (above RenamePopover z-50)
// Content:
//   Scrollable <ul> of buffers; each <li> is <button> with name
//     If full-path===currentBufferKey: render Check icon, aria-current='true'
//     onClick: onSwitch(fullPath); onDismiss()
//   Divider
//   <button> 'Manage buffers...' -> onManage(); onDismiss()
//   If buffers.length===0: empty-state 'No buffers yet.' + 'New buffer' button calling onNewBuffer
```

Postcondition: C3-1..C3-5 pass.

---

**Step 3: Update EditorToolbar**

Action: Modify `spa/src/components/editor/EditorToolbar.tsx`:
```
// Add optional props:
//   onBufferSwitch?: (key: string) => void
//   onManage?: () => void
//   onNewBuffer?: () => void

// Local state: showPopover, popoverAnchorRect, bufferList

// Replace inapp chip <span> block with:
//   <button
//     type="button"
//     className="..." // preserve existing styles
//     onClick={async (e) => {
//       const rect = e.currentTarget.getBoundingClientRect()
//       const backend = getFsBackend({type:'inapp'})
//       const raw = await backend?.list('/buffer') ?? []
//       setBufferList(raw.filter(e=>!e.isDir).map(e=>e.name))
//       setPopoverAnchorRect(rect)
//       setShowPopover(true)
//     }}
//   > logo + 'Purdex' </button>

// Conditionally render <BreadcrumbPopover> when showPopover && popoverAnchorRect:
//   onSwitch: (key) => { onBufferSwitch?.(key); setShowPopover(false) }
//   onManage: () => { onManage?.(); setShowPopover(false) }
//   onDismiss: () => setShowPopover(false)
//   onNewBuffer: onNewBuffer ? () => { onNewBuffer(); setShowPopover(false) } : undefined

// Non-inapp paths: chip absent (existing showInAppPrefix gate preserved)
```

---

**Step 4: Wire EditorPane to provide callbacks**

Action: Modify `EditorPane.tsx` where `<EditorToolbar ... />` is rendered:
```
// Pass:
//   onBufferSwitch={(key) => useEditorStore.getState().attachPane(pane.id, key)}
//   onManage={() => useTabStore.getState().openSingletonTab({kind:'editor-buffers'})}
//   onNewBuffer={async () => {
//     const path = '/buffer/Untitled-'+Date.now()+'.md'
//     await getFsBackend({type:'inapp'})?.write(path, '')
//     useEditorStore.getState().attachPane(pane.id, path)
//   }}
```

No direct unit test; covered by build + lint.

### 5.3 Acceptance (mirrors spec §7 Commit 3)

- [ ] EditorToolbar Purdex chip is `<button>` on `source.type==='inapp'`; visually identical when popover closed.
- [ ] BreadcrumbPopover renders all buffers; current marked; Manage link; Escape + outside-click + switch/manage dismiss.
- [ ] Non-inapp paths: no chip, no popover.
- [ ] Portal renders at `document.body` with z-index 100.
- [ ] Empty-buffer-list state renders New Buffer CTA (via `onNewBuffer`).
- [ ] No TS errors in modified files.
- [ ] lint + vitest + build all green.

### 5.4 Verification commands

```
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run lint
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && npx vitest run
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run build
```

---

## 6. Cross-commit dependencies

**Commit 2 depends on Commit 1:**
- `EditorPurdexSettingsSection` must exist (Commit 1) so Commit 2 can remove the `<BufferListSection />` transitional render from it.
- The legacy `registerSettingsSection({id:'editor-buffers',...})` must already be deleted (Commit 1) before the new pane kind is introduced, so users never see both the legacy settings entry and the new pane simultaneously.
- Commit 2 does NOT directly use `useEditorSettingsStore`, but relies on the fact that `register-modules.tsx` is in its Commit-1-migrated state.

**Commit 3 depends on Commit 2:**
- `{kind:'editor-buffers'}` in `PaneContent` union — required so `openSingletonTab({kind:'editor-buffers'})` in popover's `onManage` handler type-checks.
- `EditorBuffersPane` registered as a pane renderer — required so the `editor-buffers` singleton tab can actually render content when the Manage link is clicked.
- `useEditorStore.attachPane` (unchanged since base) — used by both Commit 2 (smart-open) and Commit 3 (popover buffer switch). No new dependency.

---

## 7. Implementation risks (plan-level)

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| `InAppBackend.rename()` does not create intermediate dirs (confirmed gap) — breaks "move across subfolders" design in spec §4.5 | High | **Commit 2 scope-limits rename to flat names** (validator rejects `/` in input). Subfolder rename becomes a tracked follow-up issue (patch rename to reuse write() parent-dir logic). Spec §4.5 needs amendment — see §8 Q1. |
| `InAppBackend.list('/buffer')` only returns direct children — subfolder buffers invisible | High (confirmed) | Document as known limitation in `EditorBuffersPane`. The buffer system was designed flat; subdir discovery is a separate feature. |
| Zustand persist tests polluting between tests | Medium | `beforeEach`: `localStorage.clear()` + `useEditorSettingsStore.setState(DEFAULTS, false)` (merge-mode). Mock `syncManager` via `vi.hoisted` (pattern from `useModuleEnabledStore.test.ts`). Do NOT `vi.mock('zustand/middleware')` — breaks the store. |
| React portal in BreadcrumbPopover breaks jsdom tests | Medium | `vi.mock('react-dom', async (orig)=>({...await orig(), createPortal: (n)=>n}))` at top of test file. |
| Monaco options re-apply on store change does not re-format existing text | Low | Standard Monaco behavior; matches VS Code UX. Plan adds a code comment citing spec §5.2. |
| EditorToolbar callback-prop plumbing must compile without breaking existing EditorPane consumers | Medium | All new props `?`-optional. Popover simply no-ops if `onBufferSwitch` is undefined. `EditorPane` provides concrete implementations in Commit 3. |
| Updating `register-modules.test.ts` lines 331/393 may conflict with test fixture ordering | Low | Read the full test file before editing; the two assertions are in separate `it()` blocks. Run `npx vitest run --reporter=verbose` to confirm no ripple. |
| `EditorBuffersPane` multi-select delete uses `window.confirm()` (not testable in jsdom by default) | Medium | `window.confirm = vi.fn(() => true)` in `beforeEach`. Or skip multi-delete unit test and only cover single-delete path. (Spec §8 Q5 defers modal-vs-confirm; plan picks `window.confirm` for Commit 2 simplicity.) |
| BreadcrumbPopover z-index 100 collision with future modals | Low | No current modals use >=100. If needed, raise to `z-[200]`. |
| `InAppBackend.write` side effect (auto-creates parents) — used by `onNewBuffer` in Commit 3 — unrelated to `rename` gap | Low | write() is the canonical creation path; no risk. |

---

## 8. Spec questions (need decision before implementation)

1. **`InAppBackend.rename()` does not create intermediate directories.** Confirmed at line 105-110 — it only does `store.set(to,...)` then `store.delete(from)`, with no parent-dir entry creation. Spec §4.5 says rename CAN create subfolders (`/buffer/foo.md` → `/buffer/drafts/foo.md`). **Resolution options:**
   - (A) Scope-limit rename to flat names in Commit 2 (plan's default). File a follow-up issue to patch `rename()`. Amend spec §4.5 to remove the subfolder-rename claim.
   - (B) Patch `rename()` in Commit 2 to call the write() parent-dir creation logic. Adds ~15 LOC + 1 test to Commit 2.
   - Plan default: **(A)** — keeps Commit 2 focused, the capability can land in a dedicated PR.

2. **`InAppBackend.list('/buffer')` only returns direct children.** Subfolder buffers (if created by future tools) would not appear in the management pane. Related to Q1 — if rename stays flat-only, this is moot. If Q1 chooses (B), `EditorBuffersPane` needs recursive listing, which requires a new backend method.

3. **`EditorToolbar` plumbing for popover actions.** Spec §4.8 pseudocode uses `useEditorStore.attachPane(paneId, newKey)` as if `paneId` is in scope inside `EditorToolbar`. The plan proposes adding **optional callback props** (`onBufferSwitch`, `onManage`, `onNewBuffer`) that `EditorPane` supplies — this keeps `EditorToolbar` agnostic to the editor store. Confirm this approach matches spec intent.

4. **Spec open question §8.1 (icon):** Plan picks `Stack` (Phosphor). Accept?

5. **Spec open question §8.2 (zh-TW label):** Plan picks `"編輯器"` (short). Accept?

6. **Spec open question §8.5 (multi-delete confirmation):** Plan picks `window.confirm()` for Commit 2 simplicity. A proper modal is a scope increase — file follow-up if needed?

7. **Smart-open self-overwrite guard:** The plan argues that filtering on `content.kind === 'editor'` naturally excludes the `editor-buffers` pane (different kind), so no extra guard is needed. Spec §4.6 step 2 says "The tab is not the current buffers-management tab (don't self-overwrite)" which implied an explicit guard. Confirm the kind-filter-alone approach is acceptable.

8. **Breadcrumb popover empty-state CTA (`New Buffer`):** Plan proposes `onNewBuffer` callback on `BreadcrumbPopover`, with `EditorToolbar` and `EditorPane` wiring the actual filesystem call + attachPane. Confirm this is desired rather than the popover owning the write.

---

## 9. Done definition

PR ready to merge when:
- [ ] All 3 commits pass `pnpm run lint && npx vitest run && pnpm run build` independently (green on each commit, not only on HEAD).
- [ ] All 31 vitest cases green (no skipped / pending).
- [ ] Spec §7 acceptance lists for C1/C2/C3 fully ticked.
- [ ] `register-modules.test.ts` contains no remaining `'editor-buffers'` references in the legacy-section always-on assertions.
- [ ] Codex standard review (Round 1) complete with no critical/P1 findings.
- [ ] Codex parallel adversarial + defensive + file-health review (Round 2) complete; all HIGH-confidence findings addressed or tracked as GitHub issues.
- [ ] PR description lists any follow-up issues opened during review.

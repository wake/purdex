# Editor Restructure (PR 2) — Plan

- Spec: `docs/specs/2026-04-24-editor-restructure-pr2-spec.md` v1.5
- Plan revision: **v1.5** (2026-04-24) — aligned with spec v1.5 after
  Round-5 PR verification review (4-parallel, 0 CRIT + 2 HIGH + 4 MED
  + 1 LOW). Adds Commit 5 as a single fix-up commit on top of
  Commits 1-4. Commit 4 (R4 absorption, `17c1a405`) already landed.
- Base commit: `bb5ce0c1` (main @ alpha.216)
- Target version: 1.0.0-alpha.219 (alpha.218 consumed by Lights Phase 2 PR-2a)

## 0. Orientation

### Spec reference
Spec v1.5. Five-commit PR targeting alpha.219. Commits 1-4 already
landed (`b149d93c`, `7546a4ed`, `e58cc004`, `17c1a405`). Commit 5 is
the fix-up for R5 must-fix findings G1/G2/G3/G7; G4/G5/G6 become
follow-up issues.

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

Total: **51 tests** across 5 commits (v1.5 delta vs v1.4: +3 in
Commit 5 — B2-16 rename syncs tab + editor stores, B2-17 delete
cleans editor store in background tabs, B2-18 double-click opens
correct buffer without stale closure).

v1.4 delta (already landed in Commit 4, `17c1a405`): +7 — S1-9
rehydrate happy-path, B2-12 rename-exists, B2-13 delete-locked-
refused, B2-14 delete-dirty-confirm + single-confirm, B2-15 smart-
open skips dirty/non-inapp, C3-7 onNewBuffer dirty guard, A2-7
NewTabPage all-null columns empty state.

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
| L1-1 | `components/SettingsPage.test.tsx` | legacy route alias redirects | render `<SettingsPage />` after setting `location.hash` or whatever URL mechanism SettingsPage uses (check 71-99 in implementation; plan step 8 picks the right API) to `editor-buffers` → rendered heading matches the `editor` section | 1 |
| M1-1 | `components/editor/MonacoWrapper.test.tsx` (extend existing) | options read from store | existing file already mocks `@monaco-editor/react` default `Editor` with `editorPropsSpy` (MonacoWrapper.test.tsx:15). Add cases: mutate `useEditorSettingsStore.setState({fontSize:20, tabSize:4, ...}, false)`; re-render `<MonacoWrapper />`; assert `editorPropsSpy.options` reflects all 6 fields (fontSize/tabSize/insertSpaces/wordWrap/lineNumbers/minimap). | 1 |
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
| B2-10 | `components/editor/EditorBuffersPane.test.tsx` | delete closes open panes | deleting `/buffer/x.md` with 2 open editor panes on it → `useTabStore.closePane` called for each affected tab/pane pair BEFORE `backend.delete`; ordering asserted via event log (`close:<paneId>` / `delete:<path>`) | 2 |
| B2-11 | `components/editor/EditorBuffersPane.test.tsx` | delete with locked tab | buffer open in a **locked** tab with single editor pane → `closePane` called (but useTabStore mock confirms no-op for locked); pane & tab remain in mocked state; `backend.delete` still runs. Asserts EditorBuffersPane does not assume successful closure | 2 |
| N2-1 | `lib/register-modules.test.ts` | buffers NewTab provider registered | `getNewTabProviders().find(p=>p.id==='editor-buffers')` defined | 2 |
| N2-2 | `lib/register-modules.test.ts` | provider order after editor | `editor-buffers.order > editor.order` | 2 |
| N2-3 | `components/editor/ManageBuffersNewTabCard.test.tsx` | card onSelect payload | click → `onSelect({kind:'editor-buffers'})` (NOT openSingletonTab) | 2 |
| A2-1 | `lib/route-utils.test.ts` | tabToUrl handles editor-buffers | `tabToUrl('tab1', {kind:'editor-buffers'})` returns `'/'` (same as other ephemeral kinds — confirmed route-utils.ts:100) | 2 |
| A2-2 | `lib/pane-labels.test.ts` | getPaneLabel handles editor-buffers | `getPaneLabel({kind:'editor-buffers'})` returns resolved i18n string | 2 |
| A2-3 | `lib/pane-labels.test.ts` | getPaneIcon handles editor-buffers | `getPaneIcon({kind:'editor-buffers'})` returns `'Stack'` | 2 |
| A2-4 | `components/NewTabPage.test.tsx` | module-disabled filter hides provider | with `useModuleEnabledStore.setState({enabled: {editor: false}}, false)` → NewTab grid omits `editor-buffers` and `editor` cards | 2 |
| A2-5 | `components/NewTabPage.test.tsx` | provider without moduleId always visible | register a fake provider with no `moduleId`; with editor module disabled, fake provider still visible (back-compat) | 2 |
| A2-6 | `components/editor/EditorBuffersPane.integration.test.tsx` (or extend EditorBuffersPane.test.tsx) | pane renders when module disabled | open a tab with `{kind:'editor-buffers'}`, then set editor module disabled; assert `EditorBuffersPane` still renders (pane lifetime bound to tab, not registration — documented in spec edge cases) | 2 |
| C3-1 | `components/editor/BreadcrumbPopover.test.tsx` | renders list | buffers=[a,b,c] → 3 items | 3 |
| C3-2 | `components/editor/BreadcrumbPopover.test.tsx` | current marked | item matching currentKey has aria-current | 3 |
| C3-3 | `components/editor/BreadcrumbPopover.test.tsx` | click switch | non-current click → `onSwitch` called with that key | 3 |
| C3-4 | `components/editor/BreadcrumbPopover.test.tsx` | Escape dismisses | keydown Escape → `onDismiss` | 3 |
| C3-5 | `components/editor/BreadcrumbPopover.test.tsx` | manage link | click Manage → `onManage` | 3 |
| C3-6 | `components/editor/EditorToolbar.test.tsx` | popover switch with dirty buffer prompts confirm | with `useEditorStore.setState({buffers:{[currentKey]:{...,isDirty:true}}})` → click non-current buffer item; `window.confirm` called; on cancel, `setPaneContent` NOT called; on OK, `setPaneContent` called. (Tests both branches via two `window.confirm = vi.fn(() => true\|false)` permutations.) | 3 |
| T3-1 | `components/editor/EditorToolbar.test.tsx` | inapp chip is button | `source.type='inapp'` → Purdex `<button>` rendered | 3 |
| T3-2 | `components/editor/EditorToolbar.test.tsx` | non-inapp no chip | `source.type='daemon'` → no Purdex chip | 3 |
| S1-9 | `stores/useEditorSettingsStore.test.ts` | happy-path rehydrate restores persisted values | write non-defaults (e.g. wordWrap='off', tabSize=4, fontSize=18); simulate reload by re-reading `localStorage` payload and reinitializing the store; assert non-defaults survive. Fails against v1.3 code because `merge` reads envelope instead of `envelope.state` | 4 |
| B2-12 | `components/editor/EditorBuffersPane.test.tsx` | rename rejects when destination exists | seed backend with `/buffer/foo.md` + `/buffer/bar.md`; select foo, rename to `bar.md`; assert inline `editor.buffers.rename_exists_error`; `backend.rename` NOT called | 4 |
| B2-13 | `components/editor/EditorBuffersPane.test.tsx` | delete refused when any affected pane is in locked tab | seed tabStore with locked tab containing editor pane for `/buffer/x.md`; click Delete; assert error `editor.buffers.delete_locked_refused`; `backend.delete` NOT called; `closePane` NOT called | 4 |
| B2-14 | `components/editor/EditorBuffersPane.test.tsx` | delete confirms for single / dirty | (a) dirty case: pane has isDirty=true → `window.confirm` with dirty-specific message; cancel aborts, OK proceeds. (b) single-clean case: confirm with single-specific message. Use `window.confirm = vi.fn()` spy | 4 |
| B2-15 | `components/editor/EditorBuffersPane.test.tsx` | smart-open skips dirty / non-inapp panes | seed activeTab with editor pane where `content.source.type='inapp'` but buffer isDirty=true; other tab with `content.source.type='daemon'`; open a buffer → `setPaneContent` NOT called on either pane; `addTab` called (new-tab fallback) | 4 |
| C3-7 | `components/editor/EditorToolbar.test.tsx` (extend) | onNewBuffer dirty confirm | current pane buffer isDirty=true, trigger `onNewBuffer`; `window.confirm` called; cancel → `setPaneContent` NOT called; OK → proceeds normally | 4 |
| A2-7 | `components/NewTabPage.test.tsx` | all columns filter to null → empty state | seed profile with columns that all pin editor-module providers; disable editor module; assert empty-state element renders (not a blank grid); assert no `null` column render (queries by testid or role) | 4 |
| B2-16 | `components/editor/EditorBuffersPane.test.tsx` | rename syncs tab layout + editor store | Seed `useTabStore` with a non-locked tab whose layout contains a single editor pane `content={kind:'editor', source:{type:'inapp'}, filePath:'/buffer/foo.md'}`. Seed `useEditorStore.buffers['inapp:/buffer/foo.md']` + matching `paneStates[paneId]`. Mock `backend.rename` / `backend.stat` / `backend.list`. Select foo, rename to `bar.md`. After the rename promise resolves: assert the pane's `content.filePath` === `/buffer/bar.md`; assert `useEditorStore.buffers['inapp:/buffer/bar.md']` exists AND `'inapp:/buffer/foo.md'` is absent; assert `paneStates[paneId].bufferKey === 'inapp:/buffer/bar.md'`. | 5 |
| B2-17 | `components/editor/EditorBuffersPane.test.tsx` | delete cleans editor store (background keepAlive=0) | Seed `useTabStore` with a non-locked background tab (`keepAliveCount=0`, `isActive=false`) whose layout contains an editor pane pointing at `/buffer/x.md`. Seed `useEditorStore.buffers['inapp:/buffer/x.md']` with `isDirty:true` + `paneStates[paneId]` bound to it (simulating a prior mount that left stale state). Mock `backend.delete`. Confirm the dirty-delete prompt. After resolution: assert `useEditorStore.buffers` does NOT contain `'inapp:/buffer/x.md'`; assert `useEditorStore.paneStates[paneId]` is absent; assert `useTabStore.closePane` was called. | 5 |
| B2-18 | `components/editor/EditorBuffersPane.test.tsx` | double-click opens buffer without stale closure | Render with 3 files, nothing pre-selected. Double-click the SECOND row (not the first). Seed active tab with an eligible editor pane. Assert `setPaneContent` called with `content.filePath === '/buffer/<second-row-name>'` (not `undefined`, not the first row). Implicitly verifies `openBufferByName` reads from the explicit arg, not `singleSelected`. | 5 |

Note: A2-1 asserts `'/'` exactly (confirmed from existing ephemeral
kind cases at `route-utils.ts:100`). No dedicated route for
`editor-buffers` (spec v1.3 §4.9.1). `parseRoute('/')` is a no-op,
so the tab is intentionally non-addressable — no round-trip test
needed or possible.

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

### 3.3 Acceptance (mirrors spec v1.2 §7 Commit 1)
Verified via:
- Tests S1-1..S1-8, R1-1..R1-5, L1-1, M1-1 passing.
- `grep -r editor_buffers spa/src` returns empty (aside from legacy
  route alias mention).
- `grep -rn "minimap:\s*{ enabled: true }" spa/src/components/editor/
  MonacoWrapper.tsx` returns empty (no hardcoded values remain).
- Manual sidebar check confirms puzzle-piece marker on "Editor".

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

- `route-utils.ts`: add `case 'editor-buffers': return '/'` — matches existing ephemeral kinds at route-utils.ts:100. Deliberately non-addressable (spec v1.3 §4.9.1).
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
// B2-10 delete closes open panes BEFORE backend.delete:
//   seed: 2 unlocked tabs each with editor pane pointing to '/buffer/x.md'
//   select '/buffer/x.md' + Delete -> assertions:
//     useTabStore.closePane called twice (once per tab/pane pair) BEFORE mockBackend.delete
//     (event log pushes 'close:<paneId>' and 'delete:<path>' — assert all 'close:' indices precede 'delete:')
// B2-11 delete with locked tab:
//   seed: 1 locked tab with single editor pane pointing to '/buffer/x.md'
//        (mock closePane to no-op when tab.locked, matching real useTabStore behavior)
//   select + Delete -> assertions:
//     useTabStore.closePane called once (still invoked — helper doesn't check lock)
//     mocked tab state unchanged (pane remains)
//     backend.delete called
//     (confirms EditorBuffersPane tolerates closePane no-op gracefully)
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
// Delete (helper fn deleteWithPaneCleanup):
//   const targets = Array.from(selected).map(n => '/buffer/'+n)
//   if selected.size > 1 and not window.confirm(...): return
//   setLoading(true)   // disable UI during async loop (R3 Part 2 F concurrency guard)
//   try:
//     // Step 1: close every open editor pane pointing at any deleted path
//     //         (snapshot tabs BEFORE closing; closing invalidates references)
//     const tabs = useTabStore.getState().tabs
//     const panesToClose: Array<[tabId, paneId]> = []
//     for (const [tabId, tab] of Object.entries(tabs)):
//       for each leaf in scanPaneTree(tab.layout):
//         if leaf.content.kind==='editor'
//            && leaf.content.source?.type==='inapp'
//            && targets.includes(leaf.content.filePath):
//           panesToClose.push([tabId, leaf.id])
//     for (const [tabId, paneId] of panesToClose):
//       useTabStore.getState().closePane(tabId, paneId)
//       // closePane is no-op when tab is locked (useTabStore.ts:195 via closeTab guard)
//       // EditorBuffersPane does not need to check this — behavior is intentional
//     // Step 2: actual deletion (after panes are closed where possible)
//     for (const path of targets): await backend.delete(path)
//     clear selection; setRefreshKey(r=>r+1)
//   finally:
//     setLoading(false)
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

### 4.3 Acceptance (mirrors spec v1.2 §7 Commit 2)

Verified by tests P2-1, B2-1..B2-10, N2-1..N2-3, A2-1..A2-6
passing, plus:
- `grep -r BufferListSection spa/src` returns empty.
- `cd spa && pnpm run build` produces no type errors on the union
  extension or the switch additions.
- `grep -rn "filePath: null" spa/src/components/editor/EditorBuffersPane.tsx`
  returns empty (confirms §4.9.5 uses closePane, not null).

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
```
// Local helper (EditorPane.tsx) — no shared util exists:
// function findTabIdForPane(paneId: string): string | undefined {
//   const tabs = useTabStore.getState().tabs
//   for (const [tabId, tab] of Object.entries(tabs)) {
//     if (findPane(tab.layout, paneId)) return tabId   // findPane is pane-tree.ts:13
//   }
//   return undefined
// }

// onBufferSwitch (with dirty-guard per spec v1.3 §4.8):
//   const currentKey = bufferKey({type:'inapp'}, content.filePath)
//   const buf = useEditorStore.getState().buffers[currentKey]
//   if (buf?.isDirty && !window.confirm(t('editor.buffers.confirm_switch_dirty'))) return
//   const tabId = findTabIdForPane(pane.id)
//   if (!tabId) return
//   useTabStore.getState().setPaneContent(tabId, pane.id,
//     { kind:'editor', source:{type:'inapp'}, filePath: newKey })
//   // Do NOT call attachPane — EditorPane's own useEffect at 141-143 fires it on key change.

// onManage:
//   useTabStore.getState().openSingletonTab({kind:'editor-buffers'})

// onNewBuffer (no dirty-guard needed — new blank buffer doesn't displace):
//   const path = '/buffer/Untitled-'+Date.now()+'.md'
//   await getFsBackend({type:'inapp'})?.write(path, new Uint8Array(0))
//   const tabId = findTabIdForPane(pane.id)
//   if (!tabId) return
//   useTabStore.getState().setPaneContent(tabId, pane.id,
//     { kind:'editor', source:{type:'inapp'}, filePath: path })
```

Spec v1.2 §4.8 confirms: caller only calls setPaneContent; the
editor-store rebind happens automatically via EditorPane's existing
useEffect(attachPane, [key]). No flash-remount concern either — React
reuses the EditorPane component instance because only `key` (derived
from `filePath`) changes.

Test coverage: build + lint. No direct unit test for EditorPane
wiring in this plan (pane rendering has extensive Monaco dependencies
that are heavy to mock).

### 5.3 Acceptance (mirrors spec v1.2 §7 Commit 3)

Verified by tests C3-*, T3-* passing, plus:
- Manual visual check: inapp buffer editor shows clickable Purdex
  chip; popover opens below; Escape + outside-click dismiss; Manage
  link opens singleton buffers tab.
- `grep openSingletonTab spa/src/components/editor/ManageBuffersNewTabCard.tsx`
  returns empty (the card does NOT call it — only the popover
  manage link does, via `useTabStore`).
- `grep -n "attachPane" spa/src/components/editor/EditorToolbar.tsx
  spa/src/components/editor/BreadcrumbPopover.tsx` returns empty
  (spec §4.8 invariant — the popover / toolbar never invoke
  attachPane directly; EditorPane's useEffect handles it).

### 5.4 Verification commands
```
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run lint
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && npx vitest run
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run build
```

---

## 5b. Commit 4 — Fix-up (v1.4, post-R4 PR review)

Single commit addressing 8 R4 findings. Landing AFTER Commit 3 lands
(`e58cc004`).

### 5b.1 Files touched

Modify:
- `spa/src/stores/useEditorSettingsStore.ts` — fix `merge` to unwrap persisted envelope (§4.9.6)
- `spa/src/stores/useEditorSettingsStore.test.ts` — add S1-9 happy-path rehydrate
- `spa/src/components/editor/EditorBuffersPane.tsx` — rewrite rename/delete/smart-open per spec v1.4 §4.5/§4.6/§4.9.5
- `spa/src/components/editor/EditorBuffersPane.test.tsx` — add B2-12/B2-13/B2-14/B2-15
- `spa/src/components/editor/EditorPane.tsx` — add dirty guard to `onNewBuffer`
- `spa/src/components/editor/EditorToolbar.test.tsx` — add C3-7
- `spa/src/components/NewTabPage.tsx` — empty-state fallback when every column resolves to null after filter
- `spa/src/components/NewTabPage.test.tsx` — add A2-7
- `spa/src/locales/{en,zh-TW}.json` — add 4 new i18n keys

Create: nothing — all work lives in existing files.

Delete: nothing.

### 5b.2 TDD steps

**Step 1 — Persist regression guard (F1 withdrawn; S1-9 retained)**

Spec v1.4 §4.9.6 withdraws F1 after implementation verified that
Zustand's `merge` receives unwrapped state — the originally-
prescribed "fix" (unwrapping `persisted.state`) would break persist
entirely. Current v1.3 code is correct.

**S1-9 is still written**, but as a regression guard — it asserts
happy-path rehydrate works (non-defaults survive reload), so any
future refactor that introduces the misdiagnosed bug fails this
test. **Do not modify** `useEditorSettingsStore.ts`.

Test shape:
```
// Write non-defaults via the public API
useEditorSettingsStore.getState().setWordWrap('off')
useEditorSettingsStore.getState().setTabSize(4)
useEditorSettingsStore.getState().setFontSize(18)

// Simulate reload: read the localStorage envelope, reset the
// in-memory store (setState to initial defaults via merge-mode),
// then trigger rehydrate.
const raw = localStorage.getItem(STORAGE_KEYS.EDITOR_SETTINGS)
expect(raw).toBeTruthy()
expect(JSON.parse(raw!)).toMatchObject({ state: { wordWrap: 'off', tabSize: 4, fontSize: 18 } })

// Reset in-memory state (mirror beforeEach behavior)
useEditorSettingsStore.setState(DEFAULT_EDITOR_SETTINGS, false)
expect(useEditorSettingsStore.getState().wordWrap).toBe('on')  // confirm reset

// Rehydrate from the persisted payload
await useEditorSettingsStore.persist.rehydrate()
const s = useEditorSettingsStore.getState()
expect(s.wordWrap).toBe('off')
expect(s.tabSize).toBe(4)
expect(s.fontSize).toBe(18)
```

S1-9 passes immediately against current v1.3 code. S1-7 + S1-8
remain unchanged.

**Step 2 — Rename existence check (F4)**

Write B2-12 first. Seed mocked backend with two files. Wire mocked `backend.stat` to resolve for known paths and reject otherwise.

Fix: in `EditorBuffersPane.tsx`, add to the rename submit handler:
```
if (targetPath !== renameTarget) {
  const exists = await backend.stat(targetPath).then(() => true).catch(() => false)
  if (exists) {
    setRenameError(t('editor.buffers.rename_exists_error'))
    return
  }
}
await backend.rename(renameTarget, targetPath)
```
Run B2-12 + existing B2-5/B2-6: all pass.

**Step 3 — Delete pre-check gate (F2 + F5 + F6)**

Write B2-13 + B2-14. B2-13 sets `tab.locked = true` in the mocked tabStore state. B2-14 sets buffer `isDirty = true` for the affected filePath. Both should fail against v1.3's `deleteWithPaneCleanup`.

Fix: rewrite the handler. Helper structure:
```
async function handleDelete() {
  const targets = selected.map(n => '/buffer/' + n)
  const openPanes = collectOpenEditorPanesFor(targets)  // [[tabId, pane], ...]

  // Refusal: locked tabs
  const tabs = useTabStore.getState().tabs
  if (openPanes.some(([tid]) => tabs[tid]?.locked)) {
    setErrorToast(t('editor.buffers.delete_locked_refused'))
    return
  }

  // Confirm: dirty
  const dirtyCount = openPanes.filter(([_, pane]) => {
    const key = bufferKey(pane.content.source, pane.content.filePath)
    return useEditorStore.getState().buffers[key]?.isDirty === true
  }).length
  if (dirtyCount > 0) {
    if (!window.confirm(t('editor.buffers.delete_dirty_confirm', { count: dirtyCount }))) return
  } else if (selected.size === 1) {
    if (!window.confirm(t('editor.buffers.delete_one_confirm'))) return
  } else {
    if (!window.confirm(t('editor.buffers.confirm_delete', { count: selected.size }))) return
  }

  setLoading(true)
  try {
    for (const [tabId, pane] of openPanes) {
      useTabStore.getState().closePane(tabId, pane.id)
    }
    for (const path of targets) await backend.delete(path)
    clear selection; setRefreshKey(r => r + 1)
  } finally { setLoading(false) }
}
```
Run B2-13 + B2-14 + existing B2-3 + B2-10 + B2-11: all pass. (B2-10/B2-11 should pass because behavior is unchanged for unlocked/clean paths except a single new window.confirm for multi-delete, which is already expected.)

**Step 4 — Smart-open tightening (F3)**

Write B2-15. Seed tabs carefully: activeTab has a dirty inapp editor pane; other tab has a clean daemon editor pane. Expect `addTab` fallback, not `setPaneContent` on either.

Fix: in `smartOpen`, change the `findFirstEditorPane` predicate from
```
leaf.content.kind === 'editor'
```
to
```
leaf.content.kind === 'editor'
  && leaf.content.source?.type === 'inapp'
  && !useEditorStore.getState().buffers[bufferKey(leaf.content.source, leaf.content.filePath)]?.isDirty
```
Run B2-15 + existing B2-7/B2-8/B2-9: all pass.

**Step 5 — onNewBuffer dirty guard (F7)**

Write C3-7. Mock `useEditorStore` buffers so current `filePath` is dirty. Trigger `onNewBuffer`. Spy `window.confirm`. Two branches: cancel aborts setPaneContent; OK proceeds.

Fix: in `EditorPane.tsx`'s `onNewBuffer` callback:
```
const currentKey = bufferKey({type:'inapp'}, filePath)
const buf = useEditorStore.getState().buffers[currentKey]
if (buf?.isDirty && !window.confirm(t('editor.buffers.confirm_switch_dirty'))) return
// proceed with write + setPaneContent
```
(This is identical to the existing `onBufferSwitch` dirty guard from Commit 3; extract a tiny helper if both become repetitive.)

Run C3-7: passes.

**Step 6 — NewTabPage empty state (F8)**

Write A2-7. Seed a profile with columns all pinning editor-module providers; disable editor module; assert empty-state element renders.

Fix: in `NewTabPage.tsx`:
```
const filteredProviders = providers.filter(...)
const byId = new Map(filteredProviders.map(p => [p.id, p]))
const visibleColumns = profile.columns
  .map(col => col.entries.map(e => byId.get(e.id)).filter(Boolean))
  .filter(col => col.length > 0)

if (visibleColumns.length === 0) {
  return <NewTabEmptyState />  // reuse existing empty-state component
}
// else render grid using filteredProviders / visibleColumns
```
Run A2-7 + existing A2-4 + A2-5: all pass.

**Step 7 — i18n keys (both locales)**

Add:
- `editor.buffers.rename_exists_error` — "A buffer with that name already exists." / "已存在同名暫存檔。"
- `editor.buffers.delete_locked_refused` — "This buffer is open in a locked tab. Unlock or close the tab first." / "此暫存檔在鎖定的分頁中開啟。請先解鎖或關閉該分頁。"
- `editor.buffers.delete_dirty_confirm` — "Delete {count} buffer(s) with unsaved changes?" / "刪除 {count} 個有未儲存變更的暫存檔？"
- `editor.buffers.delete_one_confirm` — "Delete this buffer?" / "確定刪除此暫存檔？"

**Step 8 — Final verification**

```
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run lint
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && npx vitest run
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run build
```

Expected: all 48 new tests green; only 3 pre-existing `hosts.test.ts` failures remain.

### 5b.3 Acceptance (mirrors spec v1.4 §7 Commit 4)

Verified via tests S1-9, B2-12, B2-13, B2-14, B2-15, C3-7, A2-7; plus:
- `grep -n 'editor.buffers.rename_exists_error\|delete_locked_refused' spa/src/components/editor/EditorBuffersPane.tsx` returns matches.

Note (v1.5 G7 correction): the v1.4 draft of this acceptance
included `grep -n 'persisted.state' spa/src/stores/useEditorSettingsStore.ts`
— that check was based on the soon-withdrawn F1 fix. Zustand passes
the unwrapped state to `merge`, so `persisted.state` should NOT
appear in the store file. The accurate invariant is "S1-9 passes
against current v1.3 code" (see spec v1.5 §4.9.6 F1-withdrawn note).

### 5b.4 Verification commands
(same as other commits — see above).

---

## 5c. Commit 5 — R5 fix-up (v1.5, post-R5 PR verification review)

Single commit addressing the four R5 must-fix findings (G1/G2/G3/G7)
on top of Commit 4 (`17c1a405`). 3 new tests (48 → 51).

### 5c.1 Files touched

Modify:
- `spa/src/components/editor/EditorBuffersPane.tsx` — add
  `performBufferRename` helper + wire into `handleRenameConfirm`;
  add `useEditorStore.closePane` to delete close loop; extract
  `openBufferByName(name)` helper, rewire `handleOpen` +
  `onDoubleClick`.
- `spa/src/components/editor/EditorBuffersPane.test.tsx` — add
  B2-16, B2-17, B2-18.
- `docs/specs/2026-04-24-editor-restructure-pr2-spec.md` — v1.5 as
  committed above.
- `docs/specs/2026-04-24-editor-restructure-pr2-plan.md` — v1.5 as
  committed above.

Create: nothing.

Delete: nothing.

### 5c.2 TDD steps

**Step 1 — Rename store-sync (G1)**

Write B2-16 first.

- `beforeEach`: reset `useTabStore` + `useEditorStore` via
  merge-mode `setState` (pattern from existing test). Mock `backend`
  via `vi.mock('../../lib/fs-backend', ...)`.
- In the test body: call helpers to seed `useTabStore.setState({
    tabs: {T1: {id:'T1', layout: {type:'leaf', id:'P1', content:
    {kind:'editor', source:{type:'inapp'}, filePath:'/buffer/foo.md'}},
    isActive:true, locked:false, keepAliveCount:1, ...}},
    tabOrder:['T1'], activeTabId:'T1'
  }, false)`.
- `useEditorStore.setState({ buffers: {'inapp:/buffer/foo.md':
    {content:..., savedContent:..., isDirty:true, ...}},
    paneStates: {P1: {bufferKey:'inapp:/buffer/foo.md', ...}}
  }, false)`.
- Render `<EditorBuffersPane />`; select `foo.md`; click Rename;
  type `bar.md`; submit.
- After `await` the promise: assert
  `useTabStore.getState().tabs.T1.layout.content.filePath === '/buffer/bar.md'`;
  assert `useEditorStore.getState().buffers['inapp:/buffer/bar.md']` exists
  with `isDirty:true`; assert `'inapp:/buffer/foo.md'` is absent;
  assert `paneStates.P1.bufferKey === 'inapp:/buffer/bar.md'`.

Expected: test fails (rename only calls `backend.rename`).

Fix — in `EditorBuffersPane.tsx`, add at the top (next to
`bufferKeyFor`):
```ts
async function performBufferRename(fromPath: string, targetPath: string) {
  const backend = getFsBackend({ type: 'inapp' })
  if (!backend) throw new Error('InApp backend unavailable')
  await backend.rename(fromPath, targetPath)
  const source: FileSource = { type: 'inapp' }
  useTabStore.getState().renameEditorPanes(source, fromPath, targetPath)
  const oldKey = bufferKeyFor(source, fromPath)
  const newKey = bufferKeyFor(source, targetPath)
  useEditorStore.getState().renameBuffer(oldKey, newKey)
}
```

Inside `handleRenameConfirm`, replace the `await backend.rename(...)`
line with `await performBufferRename(fromPath, targetPath)`.

Keep the pre-rename `stat`-based collision check exactly as today —
the new helper only covers the post-check path.

Run B2-16 + existing B2-5/B2-6/B2-12: all pass.

**Step 2 — Delete editor-store cleanup (G2)**

Write B2-17 first.

- `beforeEach`: as above.
- Seed `useTabStore` with a non-locked **background** tab:
  `{tabs: {T1: {…, isActive:false, keepAliveCount:0,
    layout: {type:'leaf', id:'P1', content:{kind:'editor',
    source:{type:'inapp'}, filePath:'/buffer/x.md'}}}},
    tabOrder:['T1'], activeTabId:null}`.
- Seed `useEditorStore` with
  `{buffers: {'inapp:/buffer/x.md': {…, isDirty:true}},
    paneStates: {P1: {bufferKey:'inapp:/buffer/x.md', …}}}`
  (simulating the paneState that *should* have been cleaned up by
  EditorPane unmount but wasn't — the exact failure mode G2 guards
  against).
- Stub `window.confirm = vi.fn(() => true)`.
- Render `<EditorBuffersPane />`; select `x.md`; click Delete.
- After promise settles, assert:
  `useEditorStore.getState().buffers['inapp:/buffer/x.md']` is
  `undefined`; `useEditorStore.getState().paneStates.P1` is
  `undefined`; `useTabStore.getState().tabs.T1.layout` no longer
  contains pane P1 (or `T1` itself is gone if last pane);
  `backend.delete` called with `/buffer/x.md`.

Expected: fails (existing delete loop only calls `useTabStore.closePane`).

Fix — in the delete close loop inside `handleDelete`:
```ts
for (const [tabId, pane] of openPanes) {
  useTabStore.getState().closePane(tabId, pane.id)
  if (pane.content.kind === 'editor') {
    const key = bufferKeyFor(pane.content.source, pane.content.filePath)
    useEditorStore.getState().closePane(pane.id, key)
  }
}
```

Run B2-17 + existing B2-3/B2-10/B2-11/B2-13/B2-14: all pass.

**Step 3 — `openBufferByName` helper (G3)**

Write B2-18 first.

- Seed 3 files via `backend.list` mock. Seed `useTabStore` with a
  single eligible editor pane (clean, inapp).
- Render; `await` for rows; double-click the SECOND row (index 1).
- Assert `useTabStore.getState()` spy on `setPaneContent` received
  the filePath matching the second row's name, not the first.

Expected: fails — current code reads stale `singleSelected` and
targets row 1 (or no-ops when nothing pre-selected).

Fix — in `EditorBuffersPane.tsx`, extract:
```ts
const openBufferByName = useCallback((name: string) => {
  const path = `/buffer/${name}`
  const newContent: PaneContent = {
    kind: 'editor', source: { type: 'inapp' }, filePath: path,
  }
  // paste the existing smart-open body from handleOpen, replacing
  // `singleSelected` / `path` lookup with the `name` parameter.
  // The eligibility predicate (firstEligibleEditorPaneId) is
  // unchanged.
}, [])

const handleOpen = useCallback(() => {
  if (!singleSelected) return
  openBufferByName(singleSelected)
}, [singleSelected, openBufferByName])
```

Rewire the row `onDoubleClick`:
```tsx
onDoubleClick={() => openBufferByName(f.name)}
```

(Remove the `setSelected(new Set([f.name]))` + `queueMicrotask(() =>
handleOpen())` dance. Single-click selection is preserved via the
existing `onClick={() => toggleSelect(f.name)}`.)

Run B2-18 + existing B2-7/B2-8/B2-9/B2-15: all pass.

**Step 4 — Docs hygiene (G7)**

No code changes in this step — verify:
- `grep -n 'persisted?.state\|persisted\.state' docs/specs/2026-04-24-editor-restructure-pr2-*.md`
  returns ONLY the two lines inside spec §4.9.6 that explain *why*
  the F1 unwrap fix was withdrawn (lines around the `persisted.state
  unwrap actually breaks persist` paragraph). No acceptance checks
  or plan grep directives that expect `persisted.state` to appear
  in the store source file.
- `grep -rn 'merge now unwraps' docs/specs/` returns empty.
- Spec §8 v1.4 decisions list correctly marks F1 as WITHDRAWN.

All three have already been addressed when the v1.5 spec landed
(steps in task 2 above). This step is a `grep` re-verification
before committing.

**Step 5 — Final verification**

```
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run lint
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && npx vitest run
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 && cd spa && pnpm run build
```

Expected: all 51 tests green (48 existing + 3 new B2-16/17/18); only
3 pre-existing `hosts.test.ts` failures remain (unrelated, present
on base).

### 5c.3 Acceptance (mirrors spec v1.5 §7 Commit 5)

Verified via tests B2-16, B2-17, B2-18; plus:
- `grep -n 'performBufferRename' spa/src/components/editor/EditorBuffersPane.tsx`
  returns a helper definition AND exactly one call site inside
  `handleRenameConfirm`.
- `grep -n 'useEditorStore.getState().closePane' spa/src/components/editor/EditorBuffersPane.tsx`
  returns a call inside the delete close loop.
- `grep -n 'openBufferByName' spa/src/components/editor/EditorBuffersPane.tsx`
  returns a helper definition AND two call sites (`handleOpen` +
  row `onDoubleClick`).
- `grep -n 'queueMicrotask' spa/src/components/editor/EditorBuffersPane.tsx`
  returns empty.

### 5c.4 Verification commands
(same as other commits — see above).

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
| Popover `onBufferSwitch` accidentally calls `attachPane` directly (as in earlier spec drafts) and races with EditorPane's useEffect | Low (after spec v1.2 clarification) | Plan step 4 of C3 explicitly comments "Do NOT call attachPane" inline. `useEditorStore.attachPane` is not imported by EditorToolbar or BreadcrumbPopover. A lint / grep check in the done-definition ensures this. |
| Dirty buffer lost when user switches via popover | Reduced to Low | Spec v1.3 §4.8 adds `window.confirm` dirty-guard at the popover's onSwitch boundary. Smart-open from management pane intentionally does not prompt (different mental model). Tested by C3-6. |
| `closePane` on locked single-pane tab is a silent no-op | Medium | Documented in spec v1.3 §4.9.5 and §6 edge cases. EditorBuffersPane calls closePane unconditionally; useTabStore's closeTab:195 guards locked tabs. Affected pane shows EditorPane's existing "file not found" banner after backend.delete. Tested by B2-11. |
| Concurrent state drift during multi-file delete (user closes a tab between snapshot and close loop) | Low | Helper sets `loading: true` during the async delete loop to disable user actions in EditorBuffersPane. `closePane` on a non-existent tab/pane is a no-op in useTabStore (early return on `!state.tabs[id]`). Safe. |
| `filePath: string` type still accepts `''` / junk — smart-open could produce an invalid path | Low | All callers construct via `'/buffer/' + name` where `name` is a validated FileEntry. B2-5 covers slash rejection for user input. |
| `closePane` in delete flow may close the user's currently-active tab unexpectedly | Medium | Documented edge case in spec §4.9.5 — "closes the tab if its last pane closes" — matches useTabStore existing behavior. B2-10 integration test covers tab-close cascade via mocked tabStore actions. |
| `tabToUrl` fallback string not stable across other ephemeral kinds | Low | A2-1 derives expected value from existing ephemeral cases; implementation copies the exact pattern. If cases disagree on what `/` vs `''` means, plan step in C2 picks the most common and documents. |
| Smart-open tests need layout-tree seeding that matches `scanPaneTree` | Medium | Use `createTab({kind:'editor', ...})` to build test tabs (gives valid layout), then inject into `useTabStore.setState({tabs, tabOrder, activeTabId}, false)`. Validated by reading `useTabStore.test.ts` patterns. |
| `tabToUrl` path convention for `editor-buffers` unclear | Low | Step 3 of C2 greps existing cases (`editor`, `sessions`, `browser`) to derive the pattern. Deterministic URL is required for the L1-1-style bookmark test (not included in C2 — URL is internal). |
| `NewTabProvider.moduleId` field causes existing consumers to misbehave | Low | Field is optional. Plan step 3 of C2 keeps `getNewTabProviders()` return shape identical; the filter is at consumer site (`NewTabPage`). Legacy providers with no `moduleId` pass the filter unconditionally. |
| `EditorToolbar` new optional callback props break existing callers | Low | All three `?`-optional. Existing `EditorToolbar` consumers (non-EditorPane) work unchanged. |
| Delete-open-buffer ordering: `setPaneContent` must happen BEFORE `backend.delete` so EditorPane doesn't momentarily read a deleted file | High (correctness) | B2-10 asserts call ordering. Use `vi.spyOn` sequence OR a per-mock `order` counter. If order violates, EditorPane briefly hits "file not found" in `getFsBackend(...).read(path)` → React error. |
| `register-modules.test.ts` line 331/393 edits collide with test fixture ordering | Low | Read full test before editing; two assertions are in separate `it()` blocks. |
| Monaco re-reading options prop causes full editor remount | Low | `@monaco-editor/react@4.7.0` uses `editor.updateOptions` via prop diff; confirmed in Round-1 codex review. |

---

## 8. Spec questions (remaining — plan cannot resolve)

All major architectural / correctness questions from plans v1.0 and
v1.1 have been resolved in spec v1.2. Remaining minor items:

1. **Exact `tabToUrl` fallback string** for `editor-buffers` — plan
   step 3 of C2 picks from the existing ephemeral-kind pattern
   (`/`, `''`, or workspace-root). A2-1 asserts the chosen value.
2. **Multi-delete modal vs `window.confirm`** — plan defaults to
   `confirm()` for simplicity. A proper modal is a potential
   follow-up.
3. **`moduleId` rollout to sessions/browser NewTab providers** —
   plan default is editor-only this PR; spec §8.5 defers.
4. **`findTabIdForPane` helper** in EditorPane wiring (C3 step 4) —
   may or may not exist as a shared util. Plan phase either reuses
   or inlines a small scan.

---

## 9. Done definition

PR ready to merge when:
- [ ] All 5 commits pass `cd spa && pnpm run lint && npx vitest run &&
      pnpm run build` independently (green on each HEAD, not just the
      final merge).
- [ ] All 51 Vitest cases green (no skipped / pending / .todo).
- [ ] Spec v1.5 §7 acceptance lists for C1/C2/C3/C4/C5 fully ticked.
- [ ] `grep -r editor_buffers spa/src` empty (except migration guards
      if any).
- [ ] `grep -r BufferListSection spa/src` empty (post Commit 2).
- [ ] `grep -rn "attachPane" spa/src/components/editor/EditorToolbar
      .tsx spa/src/components/editor/BreadcrumbPopover.tsx` empty
      (reconfirms §4.8 invariant that toolbar/popover never call
      `attachPane` directly).
- [ ] Round-3 codex spec+plan review complete with no HIGH/CRIT
      findings.
- [ ] Round-4 codex PR review (standard + adversarial 3-way) on the
      PR diff complete; HIGH-confidence findings addressed or
      tracked (landed as Commit 4 `17c1a405`).
- [ ] Round-5 codex PR verification review (4-parallel) complete;
      must-fix G1/G2/G3/G7 addressed as Commit 5; G4/G5/G6 opened as
      GitHub follow-up issues.
- [ ] Round-6 focused adversarial codex review (single agent,
      targeted at G1/G2 fix correctness + buffer lifecycle cross-
      store sync) complete with no HIGH findings.
- [ ] PR description lists follow-up issues opened during review
      (at minimum: `InAppBackend.rename` subfolder patch, Tiptap
      fontSize integration, `editor-buffers` deep-link URL, dirty-
      buffer-switch prompt UX, G4 openSingletonTab primary-pane-only
      scan, G5 S1-9 full round-trip, G6 lift locked-tab refusal
      into `useTabStore.closePane`).

*End of plan v1.5.*

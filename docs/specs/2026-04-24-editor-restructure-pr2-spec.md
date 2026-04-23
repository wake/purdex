# Editor Restructure (PR 2) — Spec

- **Version**: 1.0.0-alpha.217 (target bump)
- **Date**: 2026-04-24
- **Base**: `bb5ce0c1` (main @ alpha.216)
- **Author**: claude-code + wake
- **Status**: Draft (pending codex review)

## 1. Context

PR 1 (Modules Switchboard + `isModuleOwnedContribution` marker, `#617` at
alpha.216) introduced a framework where any module-owned settings
contribution visibly carries a rotated puzzle-piece icon in the Settings
sidebar. Concurrently, `useModuleEnabledStore` now gates every module's
contributions (panes + settings).

The Editor module is caught mid-migration:

- It **owns** two HSR settings sections already (`workspace-home-path`,
  `host-home-path`) — both module-owned and therefore eligible for the
  marker.
- But the "Editor Buffers" section is still registered via the legacy
  `registerSettingsSection({ id: 'editor-buffers', ... })` code path
  (`spa/src/lib/register-modules.tsx:324-328`), which means:
  1. It currently renders as `_builtin.legacy-section` — so no marker
     shows up next to it in the sidebar.
  2. It is not disabled when the Editor module is toggled off in the
     Switchboard (wrong gating).
  3. It is a "settings tab" (preferences UI) being used as a data
     management page (CRUD over `/buffer/*` files) — wrong abstraction.
- Additionally, Monaco editor options (`minimap`, `fontSize`,
  `lineNumbers`, `wordWrap`) are hardcoded at
  `spa/src/components/editor/MonacoWrapper.tsx:67-75` with no way for
  users to customise.
- And there is no fast way to switch between `/buffer/*` documents
  inside an editor pane — users must close / reopen from the buffer
  list.

This PR addresses all four issues in **one** three-commit PR. It is the
Editor module's structural alignment with the PR 1 framework, and
incidentally produces the first sidebar entry that visibly carries the
puzzle-piece marker.

## 2. Non-goals

- No changes to the `/buffer/*` storage layer (IndexedDB backing is
  unchanged).
- No new buffer features (versioning, import/export, search-in-buffers,
  etc.) — those are Backlog.
- No changes to `EditorPane` rendering itself, only to the toolbar chip
  and the store behind Monaco options.
- No cleanup of `globalConfig` / `workspaceConfig` / `ModuleConfigSection`
  (tracked as `#618`; post-PR-2).
- No Tiptap (WYSIWYG) settings beyond `fontSize` (propagated via CSS
  variable — see §5.3 risk). Tab / word-wrap / lineNumbers / minimap are
  Monaco-only.
- No HSR schema changes: we use the existing
  `SettingsContributionDeclaration<'purdex'>` shape.

## 3. Invariants

1. After this PR, every Editor-module contribution (pane kinds + settings
   sections) is module-owned. `isModuleOwnedContribution` returns true
   for all three settings entries.
2. Settings sidebar's "Editor" row (label key
   `settings.section.editor`) shows the rotated puzzle-piece marker
   automatically — **no change to `SettingsSidebar.tsx`**.
3. When the Editor module is disabled in the Switchboard, all three
   settings sections disappear from the sidebar (existing PR 1 filter
   `dispatchSettingsContributions` handles this — verified by existing
   test).
4. Monaco's runtime options always reflect the current
   `useEditorSettingsStore` state; no hardcoded Monaco options remain
   in `MonacoWrapper.tsx`.
5. The `/buffer/*` CRUD operations exposed by `EditorBuffersPane` are
   thin wrappers over the existing `InAppBackend` API; no duplicated
   filesystem logic.
6. The breadcrumb popover is purely additive — disabling / never
   opening it leaves the current EditorToolbar rendering identical.
7. All three commits keep the test suite green (`npx vitest run &&
   pnpm run lint && pnpm run build`).

## 4. Design

### 4.1 Data model — `useEditorSettingsStore`

New Zustand store (`spa/src/stores/useEditorSettingsStore.ts`), pattern
mirrored from `useModuleEnabledStore`:

```ts
interface EditorSettingsState {
  tabSize: 2 | 4 | 8        // default 2
  insertSpaces: boolean     // default true
  wordWrap: 'on' | 'off'    // default 'on'
  lineNumbers: 'on' | 'off' // default 'on'
  minimap: boolean          // default true
  fontSize: number          // default 13, clamp [10, 24]

  setTabSize: (v) => void
  setInsertSpaces: (v) => void
  setWordWrap: (v) => void
  setLineNumbers: (v) => void
  setMinimap: (v) => void
  setFontSize: (v) => void   // clamps input
  reset: () => void
}
```

- Persisted under `STORAGE_KEYS.EDITOR_SETTINGS = 'purdex-editor-settings'`
  (added to `spa/src/lib/storage/keys.ts`).
- `version: 1`, `merge` sanitizes unknown values back to defaults.

### 4.2 HSR migration — Editor module settings

In `register-modules.tsx`, the `editor` module's `settings: [...]` grows
from 2 to 3 entries. The new entry sits first (lowest `order`) so it
appears at the top of the Editor's collapsed sidebar group:

```ts
settings: [
  {
    localId: 'editor',
    scope: 'purdex',
    order: 9,
    labelKey: 'settings.section.editor',
    component: EditorPurdexSettingsSection,
  },
  { /* workspace-home-path — unchanged */ },
  { /* host-home-path — unchanged */ },
]
```

And the legacy call at lines 324-328 is **deleted**:

```ts
registerSettingsSection({
  id: 'editor-buffers',
  label: 'settings.section.editor_buffers',
  order: 9,
  component: BufferListSection,
})
```

i18n keys:
- Add `settings.section.editor` = `"Editor"` (EN) / `"編輯器"` (zh-TW).
- Delete `settings.section.editor_buffers` (both locales).

Existing tests at `spa/src/lib/register-modules.test.ts:329,331,393`
reference the `'editor-buffers'` id — they are updated to reference
`'editor'` instead (or removed if redundant with the new HSR-aware
assertions). No test coverage is lost.

### 4.3 `EditorPurdexSettingsSection`

New component at
`spa/src/components/settings/EditorPurdexSettingsSection.tsx`, rendered
as the first Editor settings section. Props: `{ ctx }` (unused; reserved
for future purdex-scope context).

Sections (in order):

1. **Indentation**
   - `Tab size` — `<select>` with options `2`/`4`/`8`
   - `Insert spaces` — toggle
2. **Display**
   - `Word wrap` — toggle (`'on'` ↔ `'off'`)
   - `Line numbers` — toggle (`'on'` ↔ `'off'`)
   - `Minimap` — toggle
   - `Font size` — number input, clamped 10-24

Each control is wired to the corresponding `set*` action in
`useEditorSettingsStore`. A small note below the sections explains that
only the Monaco code editor respects all settings; Tiptap WYSIWYG only
respects `fontSize`.

**Commit 1 transitional state only:** below these preferences, the
existing `<BufferListSection />` is rendered as-is (it takes no props
and has its own internal UI). This is the bridge state between the
legacy section and the future pane; it disappears in Commit 2.

### 4.4 `MonacoWrapper` — read from store

`MonacoWrapper.tsx:67-75` changes from static options to reactive:

```tsx
const { tabSize, insertSpaces, wordWrap, lineNumbers, minimap, fontSize }
  = useEditorSettingsStore()

<Editor
  options={{
    minimap: { enabled: minimap },
    fontSize,
    lineNumbers,
    wordWrap,
    tabSize,
    insertSpaces,
    scrollBeyondLastLine: false,
    automaticLayout: true,
  }}
  ...
/>
```

Monaco re-reads options prop on each render. Zustand subscription in
the component body re-renders on store change — Monaco applies the new
option next draw. No explicit `editor.updateOptions(...)` call needed
(Monaco's React wrapper handles it via prop diff).

### 4.5 `EditorBuffersPane` (Commit 2)

New pane kind (`spa/src/types/tab.ts` adds `'editor-buffers'` to the
`PaneContent.kind` union). Registration in Editor module:

```ts
panes: [
  { kind: 'editor', component: EditorPane },
  { kind: 'editor-buffers', component: EditorBuffersPane },  // new
  { kind: 'image-preview', component: ImagePreviewPane },
  { kind: 'pdf-preview', component: PdfPreviewPane },
]
```

**Component**: `spa/src/components/editor/EditorBuffersPane.tsx`

- Lists all `/buffer/*` entries (from `InAppBackend.list('/buffer/')`),
  sorted by `mtime desc`.
- Columns: Name (path relative to `/buffer/`), Modified, size.
- Toolbar actions: `New`, `Rename` (only when exactly one selected),
  `Delete` (one or more), `Open` (one selected).
- Multi-select via checkbox + shift-click range.
- Double-click row = Open (smart behavior — see §4.6).
- Empty state: illustration + `New Buffer` CTA.
- Error / loading states: basic toast or inline message.

**Rename dialog**: reuses `RenamePopover` pattern. Accepts arbitrary
target paths — a user can rename `/buffer/foo.md` to
`/buffer/drafts/foo.md`, creating the subfolder. Uses
`InAppBackend.rename(from, to)` which already supports this.

### 4.6 Smart-open flow

When the user clicks `Open` on a buffer entry:

1. Iterate `useTabStore.getState().tabs` preserving `tabOrder`; for
   each tab traverse its `layout` tree.
2. Find the first pane satisfying:
   - `pane.content.kind === 'editor'` (the code/editor pane, not
     `editor-buffers` or previews).
   - The tab is not the current buffers-management tab (don't
     self-overwrite).
3. If found: call `useEditorStore.attachPane(paneId, newBufferKey)` —
   swapping buffer. Set that tab active.
4. If not found: `useTabStore.addTab(createTab({ kind: 'editor',
   source: { type: 'inapp' }, path: bufferPath, ... }))`. Set it
   active.

This keeps buffer-editing in one pane slot instead of spawning a new
tab per open — matches the user's "hybrid" mental model.

### 4.7 NewTab entry — "Manage Buffers"

In `register-modules.tsx`, register a NewTab provider alongside the
existing Editor one:

```ts
registerNewTabProvider({
  id: 'editor-buffers',
  label: 'newTab.editor.buffers.label',
  icon: 'Stack',  // Phosphor icon
  order: ??,  // placed next to the existing Editor card
  component: ManageBuffersNewTabCard,
})
```

`ManageBuffersNewTabCard` is a thin wrapper that calls `props.onSelect({
kind: 'editor-buffers' })` when clicked. The NewTab grid will display it
as a card, and the card's click opens the buffers management pane in a
new tab (via `openSingletonTab`).

Alternative we decided against: replacing the Editor card's default
action. Rejected because (a) users expect "Editor" to open a blank
editor, and (b) two separate cards make discovery obvious.

### 4.8 Breadcrumb popover (Commit 3)

In `EditorToolbar.tsx`, wrap the existing "Purdex" chip (lines 27-39,
only rendered when `source.type === 'inapp'`) in a `<button>` that
toggles a popover:

```tsx
{showInAppPrefix && (
  <BreadcrumbPopoverTrigger
    buffers={bufferList}
    currentBufferKey={path}
    onSwitch={(newKey) => useEditorStore.attachPane(paneId, newKey)}
    onManage={() => useTabStore.openSingletonTab({ kind: 'editor-buffers' })}
  />
)}
```

`BreadcrumbPopover` (new component):
- Positioned below chip using anchored rect (reuse `RenamePopover`
  positioning helper if viable).
- Dismissed on: outside click (`useClickOutside`), Escape, or after
  `onSwitch` / `onManage`.
- Top section: scrollable list of all `/buffer/*`. Current buffer is
  marked with a check icon; click = switch.
- Bottom section: `Manage buffers...` link, opens or focuses
  `editor-buffers` singleton tab.
- Empty list state: "No buffers yet. [New buffer]" — creates, opens,
  and dismisses.

Only rendered when `source.type === 'inapp'` (same gate as the
original chip). Non-inapp paths (remote hosts, workspace files, etc.)
preserve the pre-PR toolbar.

## 5. Risks & mitigations

### 5.1 `BufferListSection` double-render during Commit 1
Commit 1 renders `<BufferListSection />` inside
`EditorPurdexSettingsSection`, and simultaneously removes the legacy
`registerSettingsSection` call. Risk: if the section is somehow
registered twice (one as HSR, one as legacy) during a partial deploy,
buffers would appear in two places. Mitigation: both changes happen in
the same commit; the legacy call is deleted atomically with the HSR
`settings: []` addition.

### 5.2 Monaco option re-apply glitches
Changing `tabSize` or `insertSpaces` on Monaco mid-edit may not
retroactively reformat existing text — it only affects new input. This
is standard Monaco behavior and acceptable (VS Code behaves the same
way). No mitigation beyond matching the VS Code UX; spec note only.

### 5.3 Tiptap `fontSize` propagation
Tiptap doesn't expose a `fontSize` prop. Options considered:
1. **CSS variable** (`--editor-font-size: 13px`) applied to the Tiptap
   container via inline style and consumed by the Tiptap-rendered HTML.
2. **Tailwind arbitrary value** (`style={{ fontSize: Xpx }}`) on the
   wrapper.
3. Ship PR 2 without Tiptap `fontSize` support; ship as a follow-up.

Decision: option 1 — inline `style={{ fontSize: fontSize + 'px' }}` on
Tiptap's parent div. Matches how most rich-text editors do it. Simple,
no extension needed.

### 5.4 Subfolder creation on rename
`InAppBackend.rename('/buffer/foo.md', '/buffer/drafts/foo.md')` must
create the `/buffer/drafts/` directory. We rely on the existing rename
implementation; if it does not auto-create intermediate dirs, the spec
adds that capability as a prerequisite. Plan phase will verify via
existing tests or a probe.

### 5.5 Outside-click dismissal on breadcrumb popover
Popovers layered inside pane contents are susceptible to z-index
conflicts with Monaco's own popups (IntelliSense, hover). Mitigation:
place popover in a React portal mounted at `document.body`, use a
z-index higher than Monaco's (default is ~20-50 in Monaco; we pick
100).

### 5.6 Empty editor when the smart-open target is in a closed tab
Spec: iterating `tabOrder` only covers open tabs; closed tabs with
editor panes are not revived. This is intentional — closed = user's
explicit intent to dismiss. Plan step has a test asserting a new tab is
created when all editor panes were closed.

### 5.7 Subagent worktree enforcement
Standing feedback (`feedback_subagent_cwd_enforcement.md`): every Bash
invocation by an implementation subagent must prefix with
`cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/editor-restructure-pr2 &&`.
Plan phase encodes this in every step.

## 6. Edge cases

| Case | Behavior |
|---|---|
| User has `editor-buffers` singleton tab open, clicks "Manage buffers" in popover | `openSingletonTab` focuses existing tab |
| User switches buffer via popover while current pane has unsaved diff | Reuses `attachPane` — existing semantics: if the current buffer has unsaved work, Monaco persists it under its local key (no data loss) |
| Rename buffer to a path outside `/buffer/*` | Validation in rename dialog rejects; inline error shown |
| Delete the currently open buffer | Other panes showing it close via existing `renameEditorPanes` untitled fallback |
| All `/buffer/*` entries deleted while buffers pane is open | Pane shows empty state with "New buffer" CTA |
| `useEditorSettingsStore` persist state corrupted | `merge` fn returns defaults; tests assert this |
| Monaco mounted with `fontSize: NaN` (e.g. from corrupted storage) | Store setter clamps → fallback to 13; test covers |
| Two tabs both have editor panes open; smart-open picks which? | First in `tabOrder`. Test asserts ordering |
| User disables Editor module in Switchboard while `editor-buffers` pane is open | Pane keeps rendering (lifetime is bound to the tab, not the module registration). Module-off only hides future settings and NewTab card. Acceptable; existing behavior for panes. |
| i18n locale missing `settings.section.editor` key | `t()` falls back to key literal; not ideal but not crashing. Test ensures both locales have the key. |

## 7. Acceptance criteria

### Commit 1
- [ ] `useEditorSettingsStore` exists with 6 persisted fields and sane
      defaults.
- [ ] `EditorPurdexSettingsSection` renders 6 controls bound to the
      store.
- [ ] `MonacoWrapper` reads all 4 relevant options from the store
      (tabSize/insertSpaces/wordWrap/lineNumbers/minimap/fontSize).
- [ ] Editor module in `register-modules.tsx` has `settings: [...]`
      with 3 entries, `localId: 'editor'` first (`order: 9`).
- [ ] Legacy `registerSettingsSection({ id: 'editor-buffers', ... })`
      removed.
- [ ] i18n keys: `settings.section.editor` added (EN + zh-TW);
      `settings.section.editor_buffers` removed (EN + zh-TW).
- [ ] `register-modules.test.ts` references `'editor'` not
      `'editor-buffers'`.
- [ ] Settings sidebar "Editor" row visibly carries the puzzle-piece
      marker (by virtue of `isModuleOwnedContribution` returning true).
- [ ] `BufferListSection` rendered as transitional block inside
      `EditorPurdexSettingsSection` (commit-1 only; removed in Commit 2).
- [ ] `pnpm run lint && npx vitest run && pnpm run build` all green.

### Commit 2
- [ ] New pane kind `editor-buffers` exists in `PaneContent` union.
- [ ] `EditorBuffersPane` renders list, create, rename (incl. move),
      delete, open.
- [ ] Smart-open swaps the current editor pane's buffer when an
      editor pane exists; opens new tab otherwise.
- [ ] "Manage Buffers" NewTab card registered; clicking opens
      `editor-buffers` singleton tab.
- [ ] `BufferListSection` removed from `EditorPurdexSettingsSection`
      and its source file deleted.
- [ ] Commit message explicitly notes transitional removal.
- [ ] All verification commands green.

### Commit 3
- [ ] EditorToolbar "Purdex" chip is a `<button>` on
      `source.type === 'inapp'` paths, renders unchanged visually when
      popover is closed.
- [ ] Breadcrumb popover lists all `/buffer/*` with current marked,
      and a Manage link.
- [ ] Popover dismisses on outside click, Escape, and after switch /
      manage.
- [ ] Non-inapp paths: chip unchanged (still `<span>`, no popover).
- [ ] All verification commands green.

## 8. Open questions

1. **Manage Buffers card icon**: Phosphor `Stack`? Or `Files`? I'll
   pick during plan phase unless codex suggests a better semantic
   match.
2. **Editor purdex section label in zh-TW**: `"編輯器"` or `"編輯
   器偏好"`? Short is better for sidebar density; I'll default to
   `"編輯器"` unless pushback.
3. **Clamp bounds for `fontSize`**: spec says `[10, 24]`. Some users
   may want `>24` for presentations. Easy to relax later; keeping
   conservative for now.
4. **Breadcrumb popover position**: below chip (flowing under "/") or
   to the right of the filename? Below feels natural for breadcrumb
   semantics; plan phase will prototype.
5. **Multi-select delete confirmation**: plain JS `confirm()` or a
   proper modal? For consistency with other delete flows in Purdex, a
   modal would be nicer — plan phase will check what pattern exists.

---

*End of spec.*

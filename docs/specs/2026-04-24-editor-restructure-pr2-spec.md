# Editor Restructure (PR 2) — Spec

- **Version**: 1.0.0-alpha.217 (target bump)
- **Date**: 2026-04-24
- **Spec revision**: v1.4 (2026-04-24) — incorporates Round-4 PR review (8 findings: 1 CRIT persist bug + 7 HIGH data-loss / policy gaps from 4 parallel reviewers)
- **Base**: `bb5ce0c1` (main @ alpha.216)
- **Author**: claude-code + wake
- **Status**: Draft (pending Round-2 codex plan review)

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
- **No Tiptap integration in this PR.** `useEditorSettingsStore` wires
  only to Monaco. Tiptap `fontSize` propagation is deferred to a
  follow-up (needs a stable strategy — either CSS custom property
  applied to `.tiptap-editor` or a prose-style override). Revision
  note: spec v1.0 proposed an inline-style approach; v1.1 removes it
  from scope after Round-1 review flagged that Tiptap's `prose
  prose-sm` classes on the editable node make a naive style override
  unreliable.
- **Monaco option changes do not retroactively reformat existing
  text.** Changing `tabSize` / `insertSpaces` applies to new input
  only; existing whitespace stays as-is. This matches VS Code's
  default UX and is an explicit non-goal, not a risk.
- **No FS backend API changes.** `FileEntry` keeps `{name, isDir,
  size}` only. Per-entry `mtime` would require extending the daemon's
  Go list handler, the daemon HTTP API contract, and the Electron
  preload IPC — all out of scope. Consequence: `EditorBuffersPane`
  sorts by name (ascending) instead of mtime-descending.
- No HSR schema changes: we use the existing
  `SettingsContributionDeclaration<'purdex'>` shape.
- **No `/buffer/*` subfolder operations exposed in the UI.**
  `InAppBackend.rename()` does not create intermediate directories
  (line 105-109), and `list()` only returns direct children (line
  62-83). Rename is flat-only; subfolder support is a follow-up.
- **No deep-link URL for the buffers management tab.** Spec v1.1
  speculated `/editor/buffers`; v1.2 drops that. `tabToUrl` for
  `editor-buffers` returns the same fallback as other ephemeral,
  non-addressable tabs (current workspace root). `parseRoute` is
  not extended. A future PR can add a dedicated route with a
  matching `parseRoute` branch and a round-trip test.
- **No preservation of dirty-buffer state across pane-content
  swaps.** Switching `pane.content.filePath` (via smart-open,
  popover switch, or delete-then-reopen) causes
  `EditorPane`'s `useEffect(attachPane, [key])` to rebind the pane
  to the new buffer key. `attachPane` resets pane state
  (editorMode, showDiff, cursor, Monaco view state) and, if the
  previous buffer has no other pane referencing it, removes the
  buffer from `useEditorStore.buffers`. This is existing
  `EditorPane` behavior — VS Code-style "save before switching"
  semantics. Users must save manually; this PR does NOT add an
  unsaved-changes prompt.

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
7. All three commits keep the test suite green. Verification is run
   from the `spa/` subdirectory (the repo root has no `build` script):
   `cd spa && pnpm run lint && npx vitest run && pnpm run build`.
8. The new `editor-buffers` pane kind is integrated across the
   pane-aware switch sites: `tabToUrl()`, `getPaneLabel()`,
   `getPaneIcon()`, and the NewTab provider module-filter. Any pane
   kind missing from one of these is a bug.

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
from 2 to 3 entries. The new entry sits first among purdex-scope
entries (the Settings sidebar is flat, not grouped; "order" only
sorts within a scope):

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

**Legacy route compatibility**: `SettingsPage` (`spa/src/components/
SettingsPage.tsx:92-99`) resolves an unknown section id by falling
back to the first available section. To avoid a bad experience for
users whose URL bar or history contains `/settings/editor-buffers`,
add a single-line alias map before the fallback: when the requested
section id is `editor-buffers`, redirect to the new `editor`
purdex-scope contribution id. This is a one-liner; the full HSR
routing is unchanged.

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
`useEditorSettingsStore`. A small note below the sections states:
"These settings apply to the Monaco code editor. Rich-text (Tiptap)
integration arrives in a follow-up." (See non-goals.)

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

- Lists `/buffer/*` entries returned by `InAppBackend.list('/buffer')`
  (direct children only — subfolders are out of scope, see non-goals).
- Columns: **Name** (relative to `/buffer/`), **Size** (bytes,
  human-formatted). Sorted alphabetically ascending (by `name`).
  `Modified` column is explicitly omitted because `FileEntry` does
  not carry `mtime` and extending the FS backend contract is out of
  scope for this PR.
- Toolbar actions: `New`, `Rename` (enabled iff exactly one
  selected), `Delete` (enabled iff ≥1 selected), `Open` (enabled iff
  exactly one selected).
- Multi-select via checkbox + shift-click range.
- Double-click row = Open (smart behavior — see §4.6).
- Empty state: illustration + `New Buffer` CTA.
- Error / loading states: basic toast or inline message.

**Rename dialog**: reuses `RenamePopover` pattern.

- **Flat rename only** — the popover's `validateName` rejects any
  input containing `/`. Subfolder moves are out of scope because
  `InAppBackend.rename()` (line 105-109) does not create intermediate
  directory entries.
- **Existence check (v1.4 fix for R4-F4)** — `InAppBackend.rename(from, to)`
  is a blind overwrite: `store.set(to, ...)` followed by
  `store.delete(from)`. If the destination already exists, its
  content is silently destroyed. `handleRenameConfirm` MUST check
  before calling rename:
  ```
  const targetPath = '/buffer/' + newName
  if (targetPath !== renameTarget) {
    const exists = await backend.stat(targetPath)
      .then(() => true).catch(() => false)
    if (exists) {
      setRenameError(t('editor.buffers.rename_exists_error'))
      return  // abort rename; popover stays open for the user to fix
    }
  }
  await backend.rename(renameTarget, targetPath)
  ```
  A future PR may harden `InAppBackend.rename` itself to throw on
  existing destinations — but this PR guards at the UI layer to
  unblock the release.

### 4.6 Smart-open flow

**Correction over spec v1.0**: `EditorPane` reads its buffer from
`pane.content.filePath` (EditorPane.tsx:116-143), not from
`useEditorStore.paneStates`. Therefore swapping the displayed buffer
requires `useTabStore.setPaneContent(tabId, paneId, newContent)` —
*not* `useEditorStore.attachPane()`, which only updates the editor
store's internal view state.

**Caller does not call `attachPane` directly**: `EditorPane` has a
`useEffect(() => attachPane(paneId, key), [paneId, key])` at
line 141-143. When `setPaneContent` mutates the pane's `content.filePath`,
React re-renders `EditorPane`, `key` changes, and the useEffect fires
`attachPane` automatically. Smart-open, popover switch, and
`onNewBuffer` all just call `setPaneContent` — the editor-store
rebind happens as a downstream effect without explicit orchestration.
(See non-goals: `attachPane` resets pane state and may discard the
previous buffer. This is pre-existing VS Code-style semantics.)

**Targeting order** (stop at first match; **v1.4 tightened** —
see below):

1. **Active tab's first eligible editor pane.** Eligible means:
   `content.kind === 'editor'` **AND** `content.source.type === 'inapp'`
   **AND** the pane is NOT currently hosting a dirty buffer.
2. **Any other tab's first eligible editor pane** (scanned in
   `tabOrder`). Same eligibility rule.
3. **Fallback**: open a new tab with `{ kind: 'editor', source:
   { type: 'inapp' }, filePath: '/buffer/' + name }`.

**Dirty guard (v1.4 fix for R4-F3)**: The previous v1.3 rule
("find first editor pane regardless of source") allowed smart-open
to silently replace a local / daemon editor pane or a pane with
unsaved in-app edits, discarding work with one click. v1.4 tightens
the filter: reuse requires both (a) matching source (inapp ↔ inapp)
and (b) clean buffer state. This turns the failure mode from silent
data loss into "a new tab is opened" — a UX downgrade, not a data
downgrade, which is acceptable for a file-manager-style flow.

To read dirty state, smart-open looks up the pane's buffer via
`bufferKey(content.source, content.filePath)` and checks
`useEditorStore.getState().buffers[key]?.isDirty`. An absent buffer
entry (never mounted) counts as clean.

**Action on match**: build `newContent = { kind: 'editor', source:
{ type: 'inapp' }, filePath: '/buffer/' + name }` and call
`useTabStore.getState().setPaneContent(targetTabId, targetPaneId,
newContent)`. Then `setActiveTab(targetTabId)`. `setPaneContent` is
the existing pathway (used by NewTab flows) and is the only call the
caller makes — `EditorPane`'s `useEffect` performs the editor-store
rebind as described above.

This policy keeps buffer-editing in one pane slot where possible —
matches VS Code's "reveal in editor" behavior.

### 4.7 NewTab entry — "Manage Buffers"

In `register-modules.tsx`, register a NewTab provider alongside the
existing Editor one:

```ts
registerNewTabProvider({
  id: 'editor-buffers',
  label: 'newTab.editor.buffers.label',
  icon: 'Stack',  // Phosphor icon
  order: 6,       // immediately after the existing Editor card (order: 5)
  component: ManageBuffersNewTabCard,
})
```

`ManageBuffersNewTabCard` is a thin wrapper that calls
`props.onSelect({ kind: 'editor-buffers' })` when clicked.

**Correction over spec v1.0**: This does *not* open a singleton tab.
The existing NewTab flow (`register-modules.tsx:66-76`) handles
`onSelect` by calling `setPaneContent(tabId, pane.id, content)` —
it replaces the content of the current NewTab pane. That is the
uniform pattern shared with the `sessions`, `editor`, and `browser`
cards, and the "Manage Buffers" card follows it.

**Re-opening a separate buffers management tab**: Spec v1.0
incorrectly conflated the card behavior with `openSingletonTab`.
Cleanly: if the user already has a NewTab open, clicking "Manage
Buffers" transforms that NewTab into a buffers pane in place. If
they want to open a *new* buffers tab while editing, they use the
breadcrumb popover's `Manage buffers...` link (§4.8), which calls
`useTabStore.openSingletonTab({ kind: 'editor-buffers' })` directly
— that is the correct place for singleton semantics.

Alternative we decided against: replacing the Editor card's default
action. Rejected because (a) users expect "Editor" to open a blank
editor, and (b) two separate cards make discovery obvious.

**Visibility gating**: NewTab providers today have no module-aware
filter (`new-tab-registry.ts:19-25`). This PR adds a thin filter at
the NewTab consumer site (`NewTabPage.tsx:27-60`) so cards owned by
a disabled module are hidden. The filter reuses the same
`isModuleOwnedContribution` / `useModuleEnabledStore` pattern from
PR #617's settings filter. See §4.9.3.

### 4.8 Breadcrumb popover (Commit 3)

In `EditorToolbar.tsx`, wrap the existing "Purdex" chip (lines 27-39,
only rendered when `source.type === 'inapp'`) in a `<button>` that
toggles a popover:

```tsx
{showInAppPrefix && (
  <BreadcrumbPopoverTrigger
    buffers={bufferList}
    currentBufferKey={path}
    onSwitch={(newKey) => {
      // tabId resolved by the caller (EditorPane passes paneId + finds tabId)
      useTabStore.getState().setPaneContent(tabId, paneId,
        { kind: 'editor', source: { type: 'inapp' }, filePath: newKey })
    }}
    onManage={() =>
      useTabStore.getState().openSingletonTab({ kind: 'editor-buffers' })
    }
  />
)}
```

**`setPaneContent` is the only state transition the popover triggers.**
As described in §4.6, `EditorPane`'s `useEffect(attachPane, [key])`
auto-rebinds the editor store once React re-renders the pane with the
new `content.filePath`. The caller does not invoke `attachPane`.

**Dirty-buffer guard (added v1.3, extended v1.4)**: Before calling
`setPaneContent`, the popover's `onSwitch` handler — and similarly
`onNewBuffer` — checks
`useEditorStore.getState().buffers[currentBufferKey]?.isDirty`. If
the current buffer is dirty, prompt with `window.confirm(t('editor.
buffers.confirm_switch_dirty'))`; abort on cancel.

**v1.4 scope widening**: v1.3 limited this guard to the popover
switch handler on the premise that smart-open ("open this file" from
a management view) had a different mental model and should not
prompt. R4 review disagreed: silent data-loss is silent data-loss
regardless of UI entry point, and the practical cost of one extra
confirm click is minimal. v1.4 therefore applies the dirty-protection
pattern to every place that reuses an existing editor pane:

- **Popover `onSwitch`** — unchanged from v1.3 (`window.confirm`).
- **Popover `onNewBuffer`** — NEW in v1.4: same `window.confirm`
  before replacing the current pane with the freshly-written file.
- **Smart-open from management pane** — handled differently (§4.6):
  dirty panes are **skipped during targeting**, so smart-open never
  silently overwrites them. No prompt needed — the pane simply isn't
  eligible. If all editor panes are dirty, smart-open falls through
  to "open new tab".
- **Delete (§4.9.5)** — handled via its own refusal-or-confirm
  pre-check gate.

Consequence for §2 non-goals: "no preservation of dirty-buffer state
across pane-content swaps" remains true, BUT v1.4 adds
UI-layer guards (confirm or skip-and-fallback) so that no single
click of a normal UI affordance silently destroys unsaved work.

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

### 4.9 Ancillary integration (Commit 2)

Adding a new `PaneContent` kind requires updates at four other
pane-aware switch sites. Spec v1.0 missed these; v1.1 makes them
explicit.

#### 4.9.1 `tabToUrl()` — route serialization
`spa/src/lib/route-utils.ts:98-121` has a switch on `content.kind`.
Add an `editor-buffers` branch that returns `'/'` — the same value
existing non-addressable ephemeral kinds (`new-tab`, `dashboard`,
`history`, `hosts`, etc.) return (confirmed by inspection of
`route-utils.ts:100`).

**Deliberately non-addressable** (v1.3 clarification): `parseRoute('/')`
is a no-op (route-utils.ts:27). An `editor-buffers` tab serialized to
`'/'` will NOT round-trip — reloading the app or navigating via URL
bar to `/` will not restore a buffers tab. This is intentional: the
buffers management tab is a transient UI entry invoked from the
breadcrumb popover's Manage link or the NewTab card; it should not
persist across reload. A future PR that wants bookmark support must
add both a `tabToUrl` branch (non-`/` path) and a matching
`parseRoute` branch, plus a round-trip test. Spec v1.1 speculated
`/editor/buffers` with round-trip; v1.2 removed it because a
serializer without a parser is strictly worse.

#### 4.9.2 `getPaneLabel()` + `getPaneIcon()` — tab bar rendering
`spa/src/lib/pane-labels.ts:19-82` returns the tab title and icon
based on pane kind. Add:
- `getPaneLabel`: `'editor-buffers' → t('newTab.editor.buffers.label')`
  or a specific `'editor.buffers.tab_title'` key.
- `getPaneIcon`: `'editor-buffers' → 'Stack'` (Phosphor name,
  matching the NewTab card).
Without these, the tab bar shows "Untitled" and a generic icon.

#### 4.9.3 Module-aware NewTab provider filter
`new-tab-registry.ts` currently stores providers as a flat list with
no module linkage. Spec v1.1 adds:
- A field `moduleId?: string` to `NewTabProvider` (optional; legacy
  providers without it are always visible).
- A consumer-site filter in `NewTabPage.tsx:27-60` that hides
  providers whose `moduleId` is owned by a disabled module (via
  `useModuleEnabledStore.isEnabled(moduleId)`).
- The `editor-buffers` provider (this PR) and the existing `editor`
  provider (moved as part of this PR) set `moduleId: 'editor'`.

This keeps the NewTab registry contract backwards-compatible and
aligned with PR #617's module-gating philosophy.

#### 4.9.4 `PaneContent` union + `createTab()` compatibility
`spa/src/types/tab.ts:36-47` adds `| { kind: 'editor-buffers' }`.
`createTab()` (wherever it normalizes content for new tabs) must
accept this without throwing. `useTabStore.setPaneContent` already
accepts any `PaneContent` — no type narrowing needed there.

#### 4.9.6 `useEditorSettingsStore.merge` payload shape (v1.4 — R4-F1 WITHDRAWN as false positive)

**Resolution**: after implementation started, the assigned engineer
verified that Zustand's `persist` middleware at
`node_modules/zustand/esm/middleware.mjs:405` returns
`[false, deserializedStorageValue.state]` — i.e. the `merge` function
receives the **unwrapped** state, not the envelope. Empirically
reproduced: v1.3's `sanitize(persisted)` correctly restores
non-default values on reload; applying the originally-prescribed
`persisted.state` unwrap actually **breaks** persist (the new
`stored` is `undefined`, sanitize returns `{}`, all defaults).

R4 reviewers independently diagnosed this as a CRIT bug; it is not.
The false-positive consensus is a useful lesson: four independent
reviewers pattern-matched against a different persist library's
behavior and none verified against the actual Zustand source.

Existing `useModuleEnabledStore.ts:111-113` confirms the repo
convention (`const p = persisted as {enabled?: unknown}` directly —
no envelope unwrap).

**Residual value**: S1-9 is retained as a **regression guard** —
a happy-path rehydrate test that locks in the currently-working
behavior. It asserts that non-default values written via the store's
setters survive a simulated reload. Future refactors of `merge` that
would reintroduce the misdiagnosed bug will fail this test.

#### 4.9.7 NewTabPage empty state when all columns are filtered out (v1.4 fix for R4-F8)

`NewTabPage.tsx:31-39` currently filters the flat provider list by
module-enabled state but the render still iterates `profile.columns`
using the pre-filter `providerId → provider` map. When a user's
profile pins providers that have since been hidden (e.g. pinned
`editor` and `editor-buffers`, then disabled the Editor module), every
column resolves to `null` and the page renders as an entirely blank
tab — not a usable empty state.

Fix: after computing the filtered provider map, check whether at
least one column would resolve to a visible provider. If none do,
render the existing NewTabPage empty state (the one shown when the
user has an empty `profile.columns`). Keep the column grouping when
at least one column survives; only fall back to the empty state when
**all** columns are null after filtering.

#### 4.9.5 Handling deletion of the currently-open buffer (v1.4 rewrite)

Previous revisions (v1.2/v1.3) relied on `closePane` doing the right
thing. R4 review revealed two problems:

- `closePane` only respects `locked` when **the tab's last pane is
  being closed** (delegates to `closeTab:195`). A locked tab with
  multiple panes happily removes a single pane → layout altered
  despite the lock.
- Even in the locked-single-pane case where `closePane` is a no-op,
  the pane stays mounted on the deleted path. The editor buffer
  remains in `useEditorStore.buffers` and a subsequent save will
  re-write the file, undoing the deletion.

**v1.4 policy — pre-check gate with two refusal conditions** (fix
for R4 F2 + F5 + F6):

`handleDelete` runs this pre-check BEFORE touching the backend or
the pane tree:

```
const targets: string[] = selected.map(n => '/buffer/' + n)
const openPanes = scanAllTabs(tabs => {
  yield panes where content.kind==='editor'
    && content.source.type==='inapp'
    && targets.includes(content.filePath)
})

// Refusal 1: any affected pane lives in a locked tab
const lockedHits = openPanes.filter(([tabId, _]) => tabs[tabId].locked)
if (lockedHits.length > 0) {
  showError(t('editor.buffers.delete_locked_refused'))  // "Unlock / close the tab first"
  return  // do NOT touch backend
}

// Refusal 2: any affected pane hosts a dirty buffer
const dirtyHits = openPanes.filter(([_, pane]) => {
  const key = bufferKey(pane.content.source, pane.content.filePath)
  return useEditorStore.getState().buffers[key]?.isDirty === true
})
if (dirtyHits.length > 0) {
  if (!window.confirm(t('editor.buffers.delete_dirty_confirm', {count: dirtyHits.length})))
    return
}

// Single-delete also needs a confirm (previously only multi did)
if (selected.size === 1 && dirtyHits.length === 0) {
  if (!window.confirm(t('editor.buffers.delete_one_confirm')))
    return
}

// Proceed: close every (now-unlocked, possibly-confirmed-dirty) pane
for (const [tabId, pane] of openPanes) {
  useTabStore.getState().closePane(tabId, pane.id)
}

// Backend delete
for (const path of targets) await backend.delete(path)

// Refresh + clear selection
```

Key properties:

- **Locked tabs block deletion unconditionally.** A user must
  explicitly unlock or close the hosting tab before the backend is
  touched. Delete is therefore authoritative — no way to resurrect
  a deleted buffer via a zombie locked pane.
- **Dirty panes require explicit destructive confirmation.** This
  matches the popover's dirty guard but uses a content-loss-specific
  message ("Delete N buffer(s) with unsaved changes?"), not a generic
  switch prompt.
- **Single-delete now also confirms.** Previously only multi-delete
  confirmed — a slip for a destructive action.
- The buffers management pane's own layout (`content.kind === 'editor-buffers'`)
  cannot appear in `openPanes` — the `kind === 'editor'` filter
  excludes it.

i18n keys (both locales):
- `editor.buffers.delete_locked_refused` → `"This buffer is open in a locked tab. Unlock or close the tab first."` / `"此暫存檔在鎖定的分頁中開啟。請先解鎖或關閉該分頁。"`
- `editor.buffers.delete_dirty_confirm` → `"Delete {count} buffer(s) with unsaved changes?"` / `"刪除 {count} 個有未儲存變更的暫存檔？"`
- `editor.buffers.delete_one_confirm` → `"Delete this buffer?"` / `"確定刪除此暫存檔？"`
- `editor.buffers.rename_exists_error` → `"A buffer with that name already exists."` / `"已存在同名暫存檔。"`

**Why `closePane` instead of changing `filePath`**: `PaneContent`
editor variant requires `filePath: string` (tab.ts:45) — setting it
to `null` is a type error. Changing it to an untitled path
(`'untitled:Untitled'` + `untitled` metadata) is technically valid
but creates a confusing UX (a new Untitled document appears out of
nowhere). Closing the pane is the simplest, least surprising
behavior that matches what VS Code does when a file is deleted
externally while open.

`useTabStore.renameEditorPanes` handles renames (line 78-107) but
not deletions; `useTabStore.closePane` is the right primitive here.
This flow is a small helper (~20 LOC) inside `EditorBuffersPane` —
no new tabStore method needed.

## 5. Risks & mitigations

### 5.1 `BufferListSection` double-render during Commit 1
Commit 1 renders `<BufferListSection />` inside
`EditorPurdexSettingsSection`, and simultaneously removes the legacy
`registerSettingsSection` call. Risk: if the section is somehow
registered twice (one as HSR, one as legacy) during a partial deploy,
buffers would appear in two places. Mitigation: both changes happen in
the same commit; the legacy call is deleted atomically with the HSR
`settings: []` addition.

### 5.2 Monaco option re-apply (moved to §2 non-goals)
Spec v1.0 listed this as a risk; it is actually a non-goal — Monaco
behaves like VS Code and applies new options only to subsequent
input. Documented above in §2.

### 5.3 Tiptap `fontSize` propagation (moved to §2 non-goals)
Spec v1.0 picked an inline-style approach for Tiptap; v1.1 postpones
the integration to a follow-up because Tiptap's `prose` classes on
the editable node make a naive override unreliable. See §2.

### 5.4 Ancillary switch-site drift
Adding a new `PaneContent` kind without updating every downstream
switch (`tabToUrl`, `getPaneLabel`, `getPaneIcon`) silently degrades
the UX (wrong URL, generic icon). Mitigation: §4.9 lists every
required site; the plan's test matrix asserts each one; code review
checklist explicitly checks for coverage.

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
| User has `editor-buffers` singleton tab open, clicks "Manage buffers" in popover | `openSingletonTab({kind:'editor-buffers'})` focuses existing tab |
| User switches buffer via popover while current pane has unsaved diff | Popover's onSwitch `window.confirm('editor.buffers.confirm_switch_dirty')`; abort on cancel. On confirm, `setPaneContent` fires; `EditorPane`'s `attachPane` useEffect rebinds → previous buffer discarded (§4.8) |
| User clicks `New Buffer` from popover empty-state while current buffer is dirty | Popover's `onNewBuffer` also shows the confirm dialog (v1.4 scope widening §4.8); abort on cancel, proceed to write + attach on OK |
| User opens a buffer from the management pane while an editor pane has unsaved work | Smart-open's targeting filter (§4.6 v1.4) skips dirty panes AND non-`inapp` panes → silent overwrite impossible. Falls through to "open new tab" if every editor pane is dirty or non-inapp |
| User renames a buffer to a name that already exists | Pre-rename `backend.stat(target)` check; if exists, RenamePopover shows `editor.buffers.rename_exists_error` inline and stays open for user to correct (§4.5) |
| User deletes buffer(s) open in a **locked** tab (any pane count) | Pre-check refuses outright: `editor.buffers.delete_locked_refused` error shown; backend untouched. User must unlock or close the tab first (§4.9.5) |
| User deletes buffer(s) with **dirty** unsaved pane | Pre-check shows `editor.buffers.delete_dirty_confirm` with count; abort on cancel, proceed on OK (§4.9.5) |
| User deletes a single buffer that is NOT dirty | Pre-check shows `editor.buffers.delete_one_confirm`; OK proceeds (§4.9.5) |
| User opens a blank NewTab after disabling the Editor module, but had pinned `editor` + `editor-buffers` cards | After filter, every column is null → NewTabPage renders its existing empty state (§4.9.7 v1.4 fix); previously rendered as entirely blank |
| App reloads after user changed `tabSize` to 4 | `useEditorSettingsStore` rehydrates from `localStorage` — values survive because `merge` now unwraps the `{state,version}` envelope (§4.9.6 v1.4 fix) |
| Rename buffer to name containing `/` | Validation rejects; inline error "Subfolders not supported" (see non-goals) |
| Delete currently open buffer (any pane) | Per §4.9.5: every affected pane is closed via `useTabStore.closePane` *before* `backend.delete`; tabs whose last pane closes are closed too |
| Delete currently open buffer (breadcrumb popover is showing it) | Hosting pane is closed by §4.9.5 flow; popover unmounts with its host `EditorToolbar` |
| All `/buffer/*` entries deleted while buffers pane is open | Pane shows empty state with "New buffer" CTA |
| `useEditorSettingsStore` persist state corrupted | `merge` fn returns defaults; tests assert this |
| Monaco mounted with `fontSize: NaN` (corrupted storage) | Store setter clamps → fallback to 13; test covers |
| Smart-open: active tab has editor pane | Target is active tab's first editor pane (§4.6 rule 1) |
| Smart-open: active tab has no editor pane, another tab does | Target is first editor pane in `tabOrder` (§4.6 rule 2) |
| Smart-open: no editor panes open anywhere | New tab opened with `setPaneContent` initial state (§4.6 rule 3) |
| User disables Editor module in Switchboard while `editor-buffers` pane is open | Pane keeps rendering (pane lifetime bound to tab, not module). Module-off hides *future* settings + NewTab card (via §4.9.3 filter); existing panes persist — documented behavior |
| User loads `/settings/editor-buffers` URL (bookmarked before upgrade) | `SettingsPage` alias map (§4.2) redirects to the new `editor` section |
| i18n locale missing `settings.section.editor` key | `t()` falls back to key literal; test ensures both locales have the key |

## 7. Acceptance criteria

All verification commands run from the `spa/` subdirectory:
`cd spa && pnpm run lint && npx vitest run && pnpm run build`.

### Commit 1
- [ ] `useEditorSettingsStore` exists with 6 persisted fields and sane
      defaults.
- [ ] `EditorPurdexSettingsSection` renders 6 controls bound to the
      store.
- [ ] `MonacoWrapper` reads all 6 options from the store
      (tabSize / insertSpaces / wordWrap / lineNumbers / minimap /
      fontSize); no hardcoded values remain.
- [ ] Editor module in `register-modules.tsx` has `settings: [...]`
      with 3 entries; `localId: 'editor'` present with `scope: 'purdex'`
      and `order: 9`.
- [ ] Legacy `registerSettingsSection({ id: 'editor-buffers', ... })`
      removed.
- [ ] i18n keys: `settings.section.editor` added (EN + zh-TW);
      `settings.section.editor_buffers` removed (EN + zh-TW).
- [ ] `register-modules.test.ts` references `'editor'` not
      `'editor-buffers'`.
- [ ] `SettingsPage` redirects `editor-buffers` → `editor` (legacy
      URL compatibility, §4.2).
- [ ] Settings sidebar "Editor" row visibly carries the puzzle-piece
      marker (via `isModuleOwnedContribution`).
- [ ] `BufferListSection` rendered as transitional block inside
      `EditorPurdexSettingsSection` (commit-1 only; removed in
      Commit 2).
- [ ] All verification commands green.

### Commit 2
- [ ] `{kind:'editor-buffers'}` added to `PaneContent` union in
      `types/tab.ts`.
- [ ] `EditorBuffersPane` renders list, create, rename (flat-only),
      delete (single + multi), open.
- [ ] Smart-open uses `setPaneContent` (not `attachPane`) and prefers
      the active tab's first editor pane; falls back to `tabOrder`
      scan; finally opens a new tab (§4.6).
- [ ] Deletion of a buffer calls `useTabStore.closePane` on every
      open editor pane pointing at it *before* calling
      `backend.delete` (§4.9.5).
- [ ] Deletion when the pane sits in a locked tab: `closePane` is a
      no-op (by `closeTab` locking guard); the pane stays and shows
      EditorPane's read-error banner. Verified by test B2-11.
- [ ] "Manage Buffers" NewTab card registered with
      `moduleId: 'editor'`, `order: 6`, icon `Stack`; click replaces
      current NewTab pane via `onSelect` (not `openSingletonTab`).
- [ ] Ancillary integration complete (§4.9): `tabToUrl`
      (workspace-root fallback — no dedicated route), `getPaneLabel`,
      `getPaneIcon`, and NewTab provider module-filter all
      recognise `editor-buffers`.
- [ ] `BufferListSection` removed from `EditorPurdexSettingsSection`
      and its source file deleted; no imports remain anywhere in the
      codebase.
- [ ] All verification commands green.

### Commit 3
- [ ] EditorToolbar "Purdex" chip is a `<button>` on
      `source.type === 'inapp'` paths, renders visually unchanged when
      popover is closed.
- [ ] Breadcrumb popover lists all `/buffer/*` (direct children),
      marks current, and shows a "Manage buffers..." link that calls
      `openSingletonTab({kind:'editor-buffers'})` — the *only* place
      singleton semantics apply.
- [ ] Popover dismisses on outside click, Escape, and after switch /
      manage actions.
- [ ] Popover renders in a React portal at `document.body` with
      z-index 100 (above RenamePopover's z-50 and Monaco popups).
- [ ] Non-inapp paths (`source.type !== 'inapp'`): no Purdex chip
      rendered at all (the `showInAppPrefix` gate is unchanged); no
      popover mounted.
- [ ] Popover buffer-switch calls `setPaneContent` only
      (§4.8); `attachPane` rebind happens automatically via
      `EditorPane`'s existing `useEffect`.
- [ ] Popover buffer-switch with dirty current buffer shows
      `window.confirm` dialog; cancel aborts switch, OK proceeds
      (§4.8 dirty-guard).
- [ ] i18n: `editor.buffers.confirm_switch_dirty` added (both
      locales).

### Commit 4 — Fix-up (v1.4 new)

All fix-up work lands in a single commit after Commits 1-3. 7 new tests.

- [ ] **F1 (WITHDRAWN)** persist is not broken — R4 diagnosis
      was a false positive (Zustand passes unwrapped state to
      `merge`; see §4.9.6). S1-9 retained as a regression guard
      asserting happy-path rehydrate of non-default values.
- [ ] **F4** `handleRenameConfirm` pre-checks destination with
      `backend.stat`; if exists, inline error via
      `editor.buffers.rename_exists_error`; rename aborted (§4.5).
      B2-12 asserts: given `/buffer/bar.md` exists, renaming
      `/buffer/foo.md` → `bar.md` calls NO `backend.rename` and
      shows the inline error.
- [ ] **F2 + F5 + F6** `handleDelete` pre-check gate (§4.9.5):
      - Refuses delete when any affected pane is in a locked tab
        (B2-13).
      - Confirms delete with dirty-specific message when affected
        pane is dirty (B2-14).
      - Single-delete also confirms (B2-14 part 2).
- [ ] **F3** `smartOpen` filters candidates by source `== 'inapp'`
      AND buffer `!isDirty`. Dirty or non-inapp panes are skipped
      → falls through to new tab (B2-15).
- [ ] **F7** `EditorPane.onNewBuffer` checks current buffer dirty
      state; if dirty, `window.confirm(t('editor.buffers.confirm_switch_dirty'))`
      before `setPaneContent` (§4.8 v1.4 widening). C3-7 asserts:
      two branches (cancel aborts, OK proceeds).
- [ ] **F8** `NewTabPage` renders empty state when every
      `profile.columns` entry resolves to `null` after
      module-filter. A2-7 asserts: with editor module disabled and
      a profile pinning only editor providers, empty-state element
      is present (not a blank render).
- [ ] New i18n keys (both locales):
      `editor.buffers.rename_exists_error`,
      `editor.buffers.delete_locked_refused`,
      `editor.buffers.delete_dirty_confirm`,
      `editor.buffers.delete_one_confirm`.
- [ ] `grep -n "backend.rename(" spa/src/components/editor/EditorBuffersPane.tsx`
      must show the call guarded by the stat check (or moved
      into a helper named e.g. `performRename` that does both).
- [ ] All verification commands green.
- [ ] All verification commands green.

## 8. Open questions

Decisions taken in v1.1 (carried forward):
- NewTab card icon → `Stack` (Phosphor)
- zh-TW label → `"編輯器"` (short, matches sidebar density)
- Legacy `/settings/editor-buffers` URL → alias redirect in
  `SettingsPage` (§4.2)
- NewTab card singleton behavior → not singleton; standard `onSelect`
  replaces NewTab pane in place (§4.7)
- Smart-open targeting → active tab first, then `tabOrder` scan, then
  new tab (§4.6)
- `FileEntry.mtime` extension → out of scope; buffers sort by name
  (§2 non-goals)
- Tiptap `fontSize` integration → deferred to follow-up (§2 non-goals)

Decisions taken in v1.2:
- Delete-open-buffer flow → `closePane` (not `setPaneContent` with
  a null/untitled fallback). §4.9.5.
- Buffer-switch semantics → caller invokes `setPaneContent` only;
  `attachPane` auto-fires via `EditorPane`'s existing useEffect.
  Dirty-state preservation is NOT promised — standard VS Code
  "save before switching" behavior. §4.6 + §4.8 + §2 non-goals.
- `editor-buffers` deep-link URL → omitted this PR; `tabToUrl`
  returns workspace-root fallback, no `parseRoute` branch. §4.9.1.

Decisions taken in v1.3:
- Popover switch while buffer is dirty → `window.confirm` guard
  (§4.8 dirty-guard).
- `tabToUrl` returns `'/'` (confirmed from existing ephemeral
  kinds); `editor-buffers` is deliberately non-addressable.

Decisions taken in v1.4 (new — supersede relevant v1.3 items):
- Smart-open dirty/source policy — **changed from v1.3**: was
  "don't prompt, just switch"; now "skip ineligible panes during
  targeting" (source must be `'inapp'` and buffer must be clean).
  No prompt; fall through to new tab if every eligible pane is
  ineligible. §4.6.
- Locked-tab delete — **changed from v1.3**: was "closePane no-op
  accepted; pane shows read-error banner"; now "refuse delete
  outright with UI error; user must unlock or close first". §4.9.5.
- Delete dirty confirm + single-delete confirm added. §4.9.5.
- `onNewBuffer` dirty guard added (matches popover switch). §4.8.
- Rename existence check before `backend.rename`. §4.5.
- `useEditorSettingsStore.merge` unwraps persisted envelope
  (`persisted.state`) — fix persist-ineffective CRITICAL bug. §4.9.6.
- NewTabPage empty-state fallback when all columns filter to null.
  §4.9.7.

Still open:
1. **`fontSize` clamp bounds `[10, 24]`** — conservative default;
   easy to relax later. Keep as-is unless a reviewer objects.
2. **Breadcrumb popover position** — below chip (flowing under the
   `/`) is the plan default; plan phase will prototype and adjust if
   awkward.
3. **Multi-select delete confirmation** — plan uses `window.confirm()`
   for Commit 2 simplicity. A proper modal is a scope increase; if a
   reviewer prefers, file a follow-up.
4. **Sidebar naming collision** — the `editor` contribution is
   labeled "Editor" and sits alongside two other Editor-module
   entries ("Home Path — Workspace", "Home Path — Host"). Visually
   verified there is no duplicate "Editor" heading because the
   sidebar is flat (§4.2). Plan acceptance includes a manual check
   step.
5. **NewTab `moduleId` field rollout** — spec v1.1 adds an optional
   `moduleId` to `NewTabProvider` (§4.9.3). Should the existing
   `editor` and `sessions` providers also set `moduleId` in this PR
   (to exercise the filter), or wait for a dedicated alignment PR?
   Plan default: set `moduleId: 'editor'` for the two Editor-module
   providers only (`editor`, `editor-buffers`); leave `sessions` /
   `browser` untouched this PR.

---

*End of spec v1.4.*

# Device State Backup — P3 Plan (merge)

Spec: `docs/specs/2026-09-16-device-state-backup-spec.md` §5, §6, §7 (P3 bullets).
Prerequisite: P2 merged (`lib/device-state/{reattach,prev,restore}.ts`, list + Replace UI). New worktree from
`origin/main` after P2's bump.

Rules: identical to the P1 plan header.

Waves: **A** = T1 → **B** = T2 → **C** = T3 → **D** = T4 → **E** = T5.

---

## T1 — Identity keys (`spa/src/lib/device-state/identity.ts` + test)

```ts
export function sourceKey(source: FileSource): string                       // daemon:<hostId> | local | inapp
export function paneKey(content: PaneContent, wsNameById: ReadonlyMap<string, string>): string | null
export function tabKey(tab: Tab, wsNameById: ReadonlyMap<string, string>): string | null
```
Table exactly spec §5.2. `settings` with `{ workspaceId }` → `settings:ws:<name>` via `wsNameById`; an id not in
`wsNameById` (dangling scope) → `settings:global`, matching the clone rewrite in spec §5.3 step 5 which turns an
unmapped scope into `global`. Test covers the dangling case. Untitled editor (`content.untitled` present) → `null`. `tabKey` joins leaf keys in
pre-order (`scanPaneTree`) with `|`; any `null` leaf → `null`.
Tests: one row per `PaneContent` kind (compile-time exhaustiveness: a `switch` with `never` default), untitled null,
split tab join order, null propagation, settings scope resolution.

Commit: `feat(spa): device state tab identity keys`

## T2 — `mergeDeviceState` (`spa/src/lib/device-state/merge.ts` + test)

```ts
export interface TabWorld { tabs: Record<string, Tab>; tabOrder: string[]; activeTabId: string | null; workspaces: Workspace[]; activeWorkspaceId: string | null }
export interface MergeReport { addedWorkspaces: number; addedTabs: number; skippedTabs: number }
export function mergeDeviceState(current: TabWorld, incoming: TabWorld, idGen: () => string): { next: TabWorld; report: MergeReport }
export function cloneTabWithFreshIds(tab: Tab, freshId: () => string): Tab   // new tab id, every pane id and split id
```
- **Id uniqueness**: `mergeDeviceState` builds `used = Set(current tab ids ∪ current pane ids ∪ current split ids)` and a
  `freshId()` that calls `idGen()` until the value is not in `used`, then adds it. Every generated tab/pane/split id
  goes through `freshId`. Test with a counter `idGen` whose first values deliberately equal existing current ids, and
  assert over `next`: tab keys, all pane ids, all split ids are globally unique.
- **Rebuild record**: cloned tmux panes **keep** `content.rebuild` unchanged (it carries no tab/pane id; reattach is
  name-only so `record.sessionName` still matches `cachedName`). Test: record deep-equal after clone; a reattached
  pane's `cachedName === rebuild.sessionName` when the source had them equal.
Spec §5.3 steps 1–5 exactly, plus:
- `new-tab`-only tabs (every leaf `new-tab`) are skipped and **not** counted in `skippedTabs`.
- A `null` tab key is always added (never matched), and its key is not added to `existing`.
- `wsIdMap` covers every incoming workspace; settings panes in cloned tabs are rewritten after all workspaces are
  mapped (two-pass), so a settings tab pointing to a later workspace in the list still maps.
- Pure: inputs deep-equal before/after (test with `structuredClone` comparison).
Tests: spec §7 P3 `mergeDeviceState` bullets; `idGen` is a deterministic counter; assert no id in `next` collides
with a pre-existing current id; `validateSnapshotConsistency({ ...next, version:1, capturedAt:0, sessionMeta:{} })` ok.

Commit: `feat(spa): device state merge`

## T3 — `restoreDeviceStateMerge` (`spa/src/lib/device-state/restore.ts` + test)

```ts
export interface DeviceStateMergeReport extends DeviceStateRestoreReport, MergeReport {}
export async function restoreDeviceStateMerge(snap: unknown, deps?: { now?: number; buildSnapshotFn?: typeof buildSnapshot; idGen?: () => string }): Promise<DeviceStateMergeReport>
```
Spec §5.4 under `withOperationLock(DEVICE_STATE_LOCK_OWNER.merge, …)`: `isWellFormedSnapshotV1` → `markMissingHosts`
→ `reattachByName` → rewrite incoming layouts with `remapLayoutSessions` → `mergeDeviceState(currentWorld, incoming,
idGen ?? generateId)` → try { `writeDeviceStatePrev` + `replaceTabSnapshot({ version:1, capturedAt: now, sessionMeta: {}, ...next })` }
catch → `RestoreError` → `syncSessionStore(remap)`.
Tests: spec §7 P3 `restoreDeviceStateMerge` bullets (lock refusal, rollback, structure-only `-prev` before mutation,
Undo after merge never calls `createSession`, current active ids unchanged, report counts).

Commit: `feat(spa): device state merge restore`

## T4 — Merge button in `DeviceStateRow` + i18n

Files: `spa/src/components/settings/device-state/DeviceStateRow.tsx` (+test), `DeviceStateSection.tsx` (+test if the
status wiring changes), `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`.

- Merge button next to Replace, inline confirm, same single-flight / lock / status / `onRestored` path as Replace
  (owner `DEVICE_STATE_LOCK_OWNER.merge`).
- Locale keys (i18next `{{var}}`):

| key | en | zh-TW |
|---|---|---|
| `settings.device_state.action.merge` | Merge | 合併 |
| `settings.device_state.action.merge_confirm` | Add this state's missing workspaces and tabs? Existing ones are kept. | 把此狀態中本機沒有的 workspace 與分頁加進來？既有的不會變動。 |
| `settings.device_state.toast.merged` | Merged: {{addedWorkspaces}} workspaces and {{addedTabs}} tabs added, {{skippedTabs}} already present | 已合併：新增 {{addedWorkspaces}} 個 workspace、{{addedTabs}} 個分頁，{{skippedTabs}} 個已存在 |

Tests: confirm calls `restoreDeviceStateMerge` with the fetched payload; cancel does not; counts shown via `data-*`
attrs; error tone on RestoreError; disabled under a foreign lock.

Commit: `feat(spa): device state merge UI`

## T5 — Gates

`pnpm exec vitest run` · `pnpm run lint` · `pnpm run build` — green; fixes in their own commits.

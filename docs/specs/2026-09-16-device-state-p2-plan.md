# Device State Backup — P2 Plan (list + full replace)

Spec: `docs/specs/2026-09-16-device-state-backup-spec.md` §4, §5.1, §6, §7 (P2 bullets).
Prerequisite: P1 merged (daemon module, `lib/device-state/{payload,api}.ts`, `useDeviceStateStore`,
`DeviceStateSection` P1 block). New worktree from `origin/main` after P1's bump.

Rules: identical to the P1 plan header (TDD, `cd` prefix, `pnpm exec vitest`, `git commit --only`, trailer,
query by role/aria/testid).

Waves: **A** = T1, T2 (parallel) → **B** = T3 → **C** = T4 → **D** = T5.

---

## T1 — `markMissingHosts` + `reattachByName` (`spa/src/lib/device-state/reattach.ts` + test)

```ts
export function markMissingHosts(snap: WorkspaceSnapshot, hostIds: ReadonlySet<string>): { snap: WorkspaceSnapshot; hostRemoved: number }
export async function reattachByName(sessionMeta: WorkspaceSnapshot['sessionMeta']): Promise<{ remap: Remap; report: EnsureReport }>
```
- `markMissingHosts`: pure; returns new tabs with every tmux-session pane on an unknown host set
  `terminated: 'host-removed'` (use `updatePaneInLayout` / `scanPaneTree` from `lib/pane-tree`), removes those hosts'
  `sessionMeta` keys, `hostRemoved` = number of such panes. Input untouched.
- `reattachByName`: spec §4.2 exactly. Uses `listSessions` from `lib/host-api`; never imports `createSession`.
- Tests: spec §7 P2 `markMissingHosts` / `reattachByName` bullets; assert `createSession` mock never called.

Commit: `feat(spa): device state reattach by name`

## T2 — `writeDeviceStatePrev` (`spa/src/lib/device-state/prev.ts` + test)

```ts
export async function writeDeviceStatePrev(now: number, build?: typeof buildSnapshot): Promise<void>
```
`build(now)` → map every `sessionMeta[h][c]` to `{ ...meta, restorable: false }` → `writePrevSnapshot`.
Tests: stored `-prev` has all entries `restorable:false`; input snapshot object not mutated; integration — seed
`-prev` via `writeDeviceStatePrev` with a dead restorable session, run `undoLastRestore()` with `host-api` mocked
(`listSessions` → no live sessions), assert `createSession` never called and the pane ends `terminated`.

Commit: `feat(spa): structure-only prev backup for device state`

## T3 — `restoreDeviceStateReplace` (`spa/src/lib/device-state/restore.ts` + test)

```ts
export const DEVICE_STATE_LOCK_OWNER = { replace: 'snapshot:deviceStateReplace', merge: 'snapshot:deviceStateMerge' } as const
export interface DeviceStateRestoreReport extends RestoreReport { hostRemoved: number }
export async function restoreDeviceStateReplace(snap: unknown, deps?: { now?: number; buildSnapshotFn?: typeof buildSnapshot }): Promise<DeviceStateRestoreReport>
```
Spec §4.3 sequence under `withOperationLock(DEVICE_STATE_LOCK_OWNER.replace, …)`; refusal throws
`Error('snapshot:deviceStateReplace refused: another operation is already running (<holder>)')`.
Shape guard = `isWellFormedSnapshotV1`. Layout rewrite = `remapLayoutSessions(layout, remap, {})` per tab.
`writeDeviceStatePrev` + `replaceTabSnapshot` inside try → `RestoreError({ ...report, rebuiltButUnattached: [] }, cause)`.
Then `syncSessionStore(remap)`.
Tests: spec §7 P2 `restoreDeviceStateReplace` bullets (lock held by another owner → throws and no store change;
malformed → throws, stores untouched, `-prev` untouched; host-removed count; same-name different-code pane
re-pointed; `replaceTabSnapshot` throw → RestoreError + stores rolled back; `-prev` written before stores change).

Commit: `feat(spa): device state full replace`

## T4 — List + Replace UI in `DeviceStateSection`

Files: `spa/src/components/settings/device-state/DeviceStateSection.tsx` (+test),
`spa/src/components/settings/device-state/DeviceStateRow.tsx` (+test),
`spa/src/components/settings/SnapshotSettingsSection.tsx` (pass `onRestored={refresh}` so the parent re-renders and
its Undo button picks up the new `-prev`), `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`.

- Section fetches `listDeviceStates(devHostId)` on mount, on devHostId change, on Refresh click, and when
  `useDeviceStateStore.status` transitions to `ok`. States: loading / error (inline, retry) / empty / rows.
- `DeviceStateRow` per spec §4.1: name, `this-computer` badge (`clientId === useSyncStore.getState().getClientId()`),
  relative updatedAt (reuse the section's relative-time helper — move `formatRelativeTime` out of
  `SnapshotSettingsSection.tsx` into `spa/src/lib/relative-time.ts` only if needed; otherwise implement locally),
  appVersion, counts; expand toggles lazy `getDeviceState` → workspace groups → tab labels via `getPaneLabel`
  (first leaf pane) plus "no workspace" group; Replace button → inline confirm (Confirm / Cancel) → calls
  `restoreDeviceStateReplace(record.payload)`; Delete button → inline confirm → `deleteDeviceState` then refetch;
  Delete disabled on own row.
- Single-flight + global lock exactly like `SnapshotSettingsSection.runRestore`: disabled while
  `useRebuildStore.lockedBy` is non-null and not ours; status line success/error with counts; `RestoreError` → error
  tone; after finish call `onRestored?.()`.
- Locale keys (i18next `{{var}}`):

| key | en | zh-TW |
|---|---|---|
| `settings.device_state.list.title` | Computers | 各電腦最後狀態 |
| `settings.device_state.list.refresh` | Refresh | 重新整理 |
| `settings.device_state.list.empty` | No saved states yet | 尚無已儲存的狀態 |
| `settings.device_state.list.load_failed` | Could not load: {{message}} | 無法載入：{{message}} |
| `settings.device_state.list.this_computer` | This computer | 本機 |
| `settings.device_state.list.counts` | {{workspaces}} workspaces · {{tabs}} tabs | {{workspaces}} 個 workspace · {{tabs}} 個分頁 |
| `settings.device_state.list.no_workspace` | No workspace | 未歸屬 workspace |
| `settings.device_state.action.expand` | Show tabs | 顯示分頁 |
| `settings.device_state.action.replace` | Replace | 完整取代 |
| `settings.device_state.action.replace_confirm` | Replace all current workspaces and tabs with this state? Undo is available above. | 以此狀態完整取代目前所有 workspace 與分頁？可用上方「復原」還原。 |
| `settings.device_state.action.delete` | Delete | 刪除 |
| `settings.device_state.action.delete_confirm` | Delete this computer's saved state? | 刪除這台電腦的已儲存狀態？ |
| `settings.device_state.action.confirm` | Confirm | 確認 |
| `settings.device_state.action.cancel` | Cancel | 取消 |
| `settings.device_state.toast.replaced` | Replaced: {{reattached}} reattached, {{failed}} disconnected, {{hostRemoved}} on missing hosts | 已取代：接回 {{reattached}}、未連線 {{failed}}、host 不存在 {{hostRemoved}} |
| `settings.device_state.toast.failed` | Restore failed: {{message}} | 還原失敗：{{message}} |
| `settings.device_state.toast.locked` | Another operation is running ({{owner}}) | 另一個操作進行中（{{owner}}） |

Tests: spec §7 P2 list bullets + replace confirm flow (confirm calls restore with the fetched payload; cancel does
not; success status shows counts via `data-*` attrs; error tone on RestoreError; `onRestored` called; buttons disabled
under a foreign lock).

Commit: `feat(spa): device state list and replace UI`

## T5 — Gates

`pnpm exec vitest run` · `pnpm run lint` · `pnpm run build` — green; fixes in their own commits.

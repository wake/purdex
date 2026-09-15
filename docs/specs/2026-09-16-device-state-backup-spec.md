# Device State Backup — Spec

Date: 2026-09-16 · Scope: Go daemon + SPA · 3 phases, one PR each

## 1. Goal

Every Purdex client (Electron window group or browser) continuously saves its latest
workspace + tab structure to one daemon, labelled with the source computer. Settings shows the
latest state of every computer and restores any of them onto the current client, either by
**full replace** or by **merge**.

Existing local-only Workspace Snapshot (Settings > Snapshot) stays as is; this feature is added
to the same page.

## 2. Decisions (user-confirmed 2026-09-15, do not reopen)

| # | Decision |
|---|----------|
| D1 | Storage target = the client's **`devHostId`** (`selectDevHostId`). Unset or not connected → skip upload, show status. |
| D2 | Upload automatically, **debounced ~5s** after a structural change; daemon keeps **only the latest** record per client (overwrite). Identical content (hash) is not re-sent. |
| D3 | Source identity = sync **`clientId`** (`useSyncStore.getClientId()`, `c_` + 12 hex). Display name is **editable**; default Electron `os.hostname()`, web "Browser · OS". |
| D4 | Restore is **structure only**: panes re-attach to a live session with the same host + name; otherwise `terminated`. No tmux session is created. |
| D5 | Merge identity: tmux tab = **hostId + session name**; other tabs = path / URL; else kind (+ title-free discriminator). |
| D6 | Merge placement: same-name workspace keeps its settings and gets the missing tabs appended; a workspace missing locally is added whole (new ids). Existing tabs and active selections are untouched. |
| D7 | UI lives in the existing **Settings > Snapshot** page, below the local snapshot. |
| D8 | Payload containing cwd / tab titles stored on the user's own daemon is acceptable. |
| D9 | A pane whose `hostId` does not exist on the restoring client → `terminated: 'host-removed'`; never guess another host. |
| D10 | Phases: **P1** daemon module + uploader · **P2** list + full replace · **P3** merge. |

## 3. P1 — Daemon module `devicestate`

### 3.1 Storage

New package `internal/module/devicestate`, registered in `cmd/pdx/main.go` next to `syncmod`.
`Init` opens `filepath.Join(c.Cfg.DataDir, "device_state.db")` with the same SQLite driver and
DSN pragmas as `OpenSyncStore`.

```sql
CREATE TABLE IF NOT EXISTS device_state (
  client_id       TEXT PRIMARY KEY,
  device_name     TEXT    NOT NULL,
  app_version     TEXT    NOT NULL DEFAULT '',
  captured_at     INTEGER NOT NULL,   -- client clock, ms
  updated_at      INTEGER NOT NULL,   -- daemon clock, ms
  workspace_count INTEGER NOT NULL,
  tab_count       INTEGER NOT NULL,
  payload         TEXT    NOT NULL    -- JSON WorkspaceSnapshot
);
```

### 3.2 HTTP API (default mux → same token-auth chain as `/api/sync/*`)

| Method & path | Body / response |
|---|---|
| `PUT /api/device-state/{clientId}` | Body `{ deviceName, appVersion, capturedAt, payload }`. `200 {"stored": true}` or `200 {"stored": false}` when `capturedAt` < stored `captured_at` (stale write from a slower window; not an error). |
| `GET /api/device-state` | `200 [ { clientId, deviceName, appVersion, capturedAt, updatedAt, workspaceCount, tabCount } ]`, ordered `updatedAt` desc; `[]` when empty. No payload. |
| `GET /api/device-state/{clientId}` | `200 { ...summary, payload }` or `404`. |
| `DELETE /api/device-state/{clientId}` | `204` (idempotent; missing row also `204`). |

Validation (all → `400` with a short message, never partial write):
- `clientId` matches `^c_[0-9a-f]{12}$`.
- `deviceName` trimmed, 1–64 runes. `appVersion` ≤ 64 bytes.
- `capturedAt` > 0.
- Body read through `io.LimitReader(cap+1)`; > **5 MB** → `413`.
- `payload` is a JSON object with `version == 1`, `workspaces` array, `tabs` object, `tabOrder`
  array; counts are computed server-side (`len(workspaces)`, `len(tabs)`). Payload stored as the
  raw JSON bytes received (re-marshalled only for validation, never rewritten).

### 3.3 SPA — payload builder (`spa/src/lib/device-state/payload.ts`)

`buildDeviceStatePayload(now: number): WorkspaceSnapshot` — **synchronous, no network**
(runs every debounce tick, must not call `listSessions` like `buildSnapshot` does):
- `tabs`, `tabOrder`, `activeTabId` from `useTabStore`; `workspaces`, `activeWorkspaceId` from
  `useWorkspaceStore`.
- `sessionMeta[hostId][sessionCode]` for every tmux-session pane: `name = cachedName`, `mode`,
  `cwd = pane.content.rebuild?.cwd` when present, `restorable: false` (structure-only by D4),
  no `captureError`.

`structuralKey(payload): string` — stable JSON (sorted keys) of the payload **without
`capturedAt`**; `hashPayload` = SHA-256 hex via existing `lib/crypto-hash.ts`.

### 3.4 SPA — API client (`spa/src/lib/device-state/api.ts`)

`putDeviceState`, `listDeviceStates`, `getDeviceState`, `deleteDeviceState` over `hostFetch`.
Non-2xx → throw `DeviceStateApiError { status }`. `getDeviceState` runs the fetched payload
through the snapshot shape guard (§5.1) and throws on malformed data.

### 3.5 SPA — device identity + status store (`spa/src/stores/useDeviceStateStore.ts`)

```ts
deviceName: string | null            // persisted; null = use default
defaultDeviceName: string            // not persisted; resolved once at startup
status: { kind: 'idle' | 'no-target' | 'offline' | 'uploading' | 'ok' | 'error'; at?: number; hostId?: string; message?: string }
setDeviceName(name: string | null)   // trims; '' → null; > 64 runes truncated
```

Default name: `window.electronAPI?.localDaemonStatus?.()` → `.hostname` when it resolves to a
non-empty string; otherwise `"<Browser> · <OS>"` parsed from `navigator.userAgent`
(Chrome/Safari/Firefox/Edge × macOS/Windows/Linux/iOS/Android, fallback `"Browser"`).
`effectiveDeviceName = deviceName ?? defaultDeviceName`.

### 3.6 SPA — uploader (`spa/src/lib/device-state/uploader.ts`)

`startDeviceStateUploader(deps?)` returns `stop()`; called once from `main.tsx` after stores
hydrate (same place as the backup auto-trigger).

- Subscribes to `useTabStore` (`tabs`, `tabOrder`, `activeTabId`), `useWorkspaceStore`
  (`workspaces`, `activeWorkspaceId`), `useHostStore` (`devHostId`, target host
  `runtime.status`), and `useDeviceStateStore.deviceName`. `visitHistory` changes are ignored.
- Any relevant change schedules a single **5000 ms** trailing debounce.
- On fire: target = `selectDevHostId`. None → status `no-target`. Target not `connected` →
  status `offline`; the target turning `connected` later schedules a tick.
- Build payload; if `hash === lastUploadedHash[target]` and the device name has not changed since
  that upload → no request. Else `PUT`; success → record hash, status `ok`; failure → status
  `error` with message, hash not recorded (next change retries). At most one request in flight;
  a tick during a flight re-runs once after it settles.
- `devHostId` change clears nothing but naturally misses the hash map → uploads to the new target.
- Multiple Electron windows share `clientId` and synced stores; duplicate PUTs are harmless
  (identical content, `capturedAt` ordering on the daemon).

### 3.7 SPA — P1 settings UI

New file `spa/src/components/settings/device-state/DeviceStateSection.tsx`, rendered inside
`SnapshotSettingsSection` below the existing blocks (the 871-line section only gains one
`<DeviceStateSection />` line). P1 shows: this computer's name (inline editable, reset to
default), target host name (or "Not set — choose a host in Settings > Development"), and the
status line (`ok` with relative time / `offline` / `error` message).

## 4. P2 — List + full replace

### 4.1 List

`DeviceStateSection` adds a list from `listDeviceStates(devHostId)` (refresh button + refetch
after own successful upload):
- Row: device name, "This computer" badge when `clientId === getClientId()`, relative
  `updatedAt`, `appVersion`, `workspaceCount` / `tabCount`.
- Expand → lazy `getDeviceState`, tree of workspaces → tab labels (`pane-labels.ts`), plus a
  "no workspace" group for `tabOrder` ids not in any workspace.
- Actions per row: **Replace**, **Merge** (P3; not rendered in P2), **Delete** (confirm;
  disabled for this computer's own row).
- Errors (list/get/delete) → inline error text; never throw to the page.

### 4.2 Reattach by name — `reattachByName(sessionMeta)` (`lib/device-state/reattach.ts`)

The existing `ensureSessions` reattaches only on **code + name** (restore.ts:62), which misses a
same-name session whose code changed. D4 requires **host + name**, so device-state restores use
their own reconciler (never `ensureSessions`, never `createSession`):

- Exactly one `listSessions(hostId)` per host in `sessionMeta`; a throw → every entry of that host
  `failed`.
- For each `[oldCode, meta]`: live session with `s.name === meta.name` and non-empty string
  `s.code` → `{ status: 'reattached', newCode: s.code, session: s }` (code may differ); otherwise
  `failed`.
- Returns `{ remap: Remap; report: EnsureReport }` (`rebuilt` always 0), so the existing
  `remapLayoutSessions` / `syncSessionStore` consume it unchanged. `failed` panes become
  `terminated: 'tmux-restarted'`; revive-by-name may re-point them later when a same-name session
  appears.

### 4.3 Full replace — `restoreDeviceStateReplace(snap, deps?)` (`lib/device-state/restore.ts`)

Under `withOperationLock('snapshot:deviceStateReplace', …)` (refusal → throw like
`lockRefused`):

1. Shape guard (§5.1); invalid → throw before any mutation.
2. `markMissingHosts(snap, hostIds)`: every tmux-session pane whose `hostId` is not in
   `useHostStore.hosts` → `terminated: 'host-removed'`; its `sessionMeta` entry is dropped so
   `reattachByName` never contacts it.
3. `reattachByName(snap.sessionMeta)` → rewrite every tab layout with `remapLayoutSessions`.
4. `writeDeviceStatePrev(now)` (§4.4), then `replaceTabSnapshot(rewritten)` (validates + rollback);
   any throw here → `RestoreError` with the report.
5. `syncSessionStore(remap)`; return report plus `hostRemoved: number`.

UI: confirm dialog stating the current workspaces/tabs will be replaced (undo available via the
existing "Undo last restore" of the same page), then toast with reattached / terminated /
host-removed counts. `RestoreError` handled like the existing snapshot actions.

### 4.4 Undo stays structure-only

The page's existing "Undo last restore" replays `-prev` through `restoreAll`, which rebuilds any
`restorable` dead session. To keep D4 for device-state restores, `writeDeviceStatePrev(now)` =
`buildSnapshot(now)` with **every `sessionMeta` entry forced `restorable: false`** before
`writePrevSnapshot`. `ensureSessions` then never reaches `createSession` for that backup
(restore.ts:74), so Undo after a device-state replace or merge only reattaches or terminates.
The local snapshot actions keep writing `-prev` as today.

## 5. P3 — Merge

### 5.1 Shared shape guard

`lib/snapshot/storage.ts` exports `isWellFormedSnapshotV1` (plain object + `version === 1` + the
existing private `isWellFormedSnapshot`), used by `getDeviceState` and both restore paths. The
per-host upload bookkeeping (last hash, last device name) lives inside the uploader, not the store
(§3.6).

### 5.2 Identity keys (`lib/device-state/identity.ts`, pure)

`paneKey(content)`:

| kind | key |
|---|---|
| `tmux-session` | `tmux:<hostId>:<cachedName>` |
| `editor` / `image-preview` / `pdf-preview` | `<kind>:<sourceKey>:<filePath>`; untitled editor → `null` (never matches) |
| `browser` | `browser:<url>` |
| `execution` | `execution:<host ?? ''>:<executionId>` |
| `settings` | `settings:global` or `settings:ws:<workspace name>` (resolved through the snapshot's own workspaces) |
| `new-tab`, `dashboard`, `hosts`, `history`, `memory-monitor`, `editor-buffers` | `<kind>` |

`sourceKey`: `daemon:<hostId>` / `local` / `inapp`.
`tabKey(tab)` = leaf `paneKey`s in pre-order joined with `|`; any `null` leaf → tab key `null`
(always treated as new). `new-tab`-only tabs are skipped by merge entirely.

### 5.3 `mergeDeviceState(current, incoming, idGen)` (pure)

Inputs: current `{ tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId }`, incoming
snapshot **after** `markMissingHosts` and the reattach remap (§5.4). Output: next state + report
`{ addedWorkspaces, addedTabs, skippedTabs }`.

1. `existing` = set of `tabKey` over **all** current tabs (any workspace or none).
2. For each incoming workspace in order: find a current workspace with the same trimmed `name`
   (first match; also matches workspaces created earlier in this merge).
   - Found → for each incoming tab id in that workspace's order: skip if key in `existing`,
     else clone with fresh tab/pane/split ids, append to the workspace's `tabs` and to
     `tabOrder`, add key to `existing`. Workspace `name/icon/moduleConfig/activeTabId` untouched.
   - Not found → append a new workspace (fresh id, same name/icon/iconWeight/moduleConfig) with
     the non-skipped cloned tabs; its `activeTabId` = the clone of the incoming active tab if it
     was added, else first added tab, else `null`.
3. Incoming tabs in `tabOrder` but in no incoming workspace → same skip/clone rule, appended to
   `tabOrder` only.
4. `activeTabId`, `activeWorkspaceId` unchanged.
5. `wsIdMap: incoming workspace id → local workspace id` is recorded for **every** incoming
   workspace, whether matched by name (local id) or newly added (fresh id). Every cloned
   `settings` pane with `{ workspaceId }` scope is rewritten to `wsIdMap[workspaceId]`; only when
   the referenced id has no mapping (it is not among the snapshot's workspaces) does it become
   `'global'`.

### 5.4 `restoreDeviceStateMerge(snap, deps?)`

Under the snapshot operation lock (new owner `snapshot:deviceStateMerge`): shape guard →
`markMissingHosts` → `reattachByName(meta)` + `remapLayoutSessions` (same as replace) →
`mergeDeviceState` → `writeDeviceStatePrev(now)` (§4.4) → `replaceTabSnapshot(next)` (validates +
rollback) → `syncSessionStore(remap)`. Report counts in a toast. Undo = existing "Undo last
restore" (structure-only per §4.4).

## 6. Error handling summary

- Daemon: validation `400`, oversize `413`, missing `404`, DB errors `500` (logged). Upload
  errors never block the UI and never retry in a loop (next change retries).
- SPA restore: every failure before store mutation leaves state untouched; mutation failures
  roll back via `replaceTabSnapshot`.

## 7. Testing

**Go** (`internal/module/devicestate`, testify, `:memory:` store, `httptest` for handlers):
upsert insert/overwrite; stale `capturedAt` → `stored:false` and row unchanged; list order and no
payload; get 404; delete idempotent; validation matrix (bad clientId, empty/65-rune name,
capturedAt 0, non-object payload, version ≠ 1, missing arrays); 5 MB + 1 → 413; counts computed
from payload.

**SPA** (vitest):
- P1: `buildDeviceStatePayload` (sessionMeta from panes, no network — `listSessions` mock never
  called); `structuralKey` ignores `capturedAt` and key order; API client status → error; store
  `setDeviceName` trim/null/truncate; default-name resolution (Electron mock, UA matrix);
  uploader with fake timers: debounce collapses bursts, `visitHistory`-only change ignored,
  no-target, offline then connect, identical hash skipped, rename forces upload, error keeps hash
  unrecorded, single in-flight + one rerun, `stop()` unsubscribes; `DeviceStateSection` P1 render
  and rename.
- P2: `markMissingHosts`; `reattachByName` (same name + **different code** reattaches with new
  code, no same-name → failed, host `listSessions` throw → all failed, `createSession` never
  called); `restoreDeviceStateReplace` (lock refusal, malformed payload rejected without
  mutation, host-removed count, rollback on `replaceTabSnapshot` throw); `writeDeviceStatePrev`
  forces `restorable:false`, and **Undo after a device-state replace never calls
  `createSession`** (integration through `undoLastRestore` with mocked host-api); list render
  (own badge, delete disabled on own row, expand lazy-loads, error states); replace confirm flow.
- P3: `paneKey` / `tabKey` table incl. untitled null and split tabs; `mergeDeviceState`: same-name
  workspace append, missing workspace add with fresh ids (no id collisions with current),
  duplicate tab anywhere skipped, standalone tabs, active ids unchanged, settings scope re-point
  to a matched workspace, to a **newly added** workspace, and to `global` only for an unmapped id,
  two incoming workspaces with same name merge into one; `restoreDeviceStateMerge` lock refusal,
  rollback on `replaceTabSnapshot` throw, structure-only `-prev` written before mutation, Undo
  after merge never calls `createSession`.
- Gates per PR: `go test ./...` (P1), `pnpm exec vitest run`, `pnpm run lint`, `pnpm run build`.

## 8. Deploy

P1 changes the daemon → after merge+bump: rebuild `bin/pdx` on mlab and restart the daemon
(per `pdx start` runtime notes), confirm `GET /api/device-state` returns `[]`. SPA changes go
live via the dev server. Electron main/preload unchanged in all phases.

## 9. Out of scope

- Restoring app-frame layout, window positions, or tmux session creation.
- Uploading to more than one daemon; history of older states.
- Pruning records of computers that stopped uploading (manual Delete only).

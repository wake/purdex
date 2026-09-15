# Device State Backup — P1 Plan (daemon module + uploader)

Spec: `docs/specs/2026-09-16-device-state-backup-spec.md` §3, §6, §7 (P1 bullets), §8.
Branch `worktree-device-state`. P2/P3 get their own plans after P1 ships.

Rules for every task:
- TDD: failing test first, run red, implement, run green.
- Every Bash command prefixed with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/device-state && ` (Go) or `.../device-state/spa && ` (SPA).
- Go tests: `go test ./internal/module/devicestate/...`. SPA tests: `pnpm exec vitest run <files>` (never `npx`).
- Parallel subagents share the worktree: only touch the task's files; new files `git add <paths>` then
  `git commit --only <paths>` as separate commands; never `git add -A`, stash, reset. git writes need sandbox disabled.
- Commit trailer `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- SPA tests query by role / aria-label / data-testid, never translated text; stores via merge-mode `setState`.

Waves: **A** = T1, T3, T4, T5 (parallel) → **B** = T2, T6 (parallel) → **C** = T7 → **D** = T8.

---

## T1 — Go store (`internal/module/devicestate/store.go`, `store_test.go`)

```go
type Record struct {
    ClientID       string `json:"clientId"`
    DeviceName     string `json:"deviceName"`
    AppVersion     string `json:"appVersion"`
    CapturedAt     int64  `json:"capturedAt"`
    UpdatedAt      int64  `json:"updatedAt"`
    WorkspaceCount int    `json:"workspaceCount"`
    TabCount       int    `json:"tabCount"`
    Payload        json.RawMessage `json:"payload,omitempty"`
}
func OpenStore(path string) (*Store, error)          // same driver + DSN pragmas as sync.OpenSyncStore; creates table (spec §3.1)
func (s *Store) Close() error
func (s *Store) Upsert(r Record) (stored bool, err error)   // stored=false when existing captured_at > r.CapturedAt; UpdatedAt set by store clock (s.now func() int64, injectable)
func (s *Store) List() ([]Record, error)             // no Payload; updated_at DESC; empty → non-nil empty slice
func (s *Store) Get(clientID string) (Record, bool, error)
func (s *Store) Delete(clientID string) error         // idempotent
```
Equal `capturedAt` overwrites (same-window retry). Tests: spec §7 Go store bullets, `:memory:`.

Commit: `feat(daemon): device state store`

## T2 — Go validation, handlers, module, registration

Files: `validate.go` (+test), `handler.go` (+test), `module.go` (+ `module_test.go`), `cmd/pdx/main.go` (one `AddModule` line after `syncmod`).

- `validateClientID`, `validateDeviceName` (trim, 1–64 runes), `validateAppVersion` (≤ 64 bytes),
  `parsePayload(raw json.RawMessage) (workspaceCount, tabCount int, err error)` per spec §3.2.
- Handlers exactly as spec §3.2 table; body cap `5 << 20` via `io.LimitReader(cap+1)` → 413; JSON errors → 400;
  `Content-Type: application/json` on JSON responses.
- Module: `Name() "devicestate"`, `Dependencies() nil`, `Init` opens `device_state.db` in `c.Cfg.DataDir`,
  `RegisterRoutes` 4 routes, `Start` logs `[devicestate] endpoints enabled`, `Stop` closes store.
- Handler tests use `httptest` + a module built with an in-memory store (add unexported `newTestModule(t)`); cover
  the full spec §7 Go validation matrix, 413, stale write `stored:false`, list/get/delete, 404.

Commit: `feat(daemon): device state HTTP module`

## T3 — SPA payload (`spa/src/lib/device-state/payload.ts` + test)

```ts
export function buildDeviceStatePayload(now: number): WorkspaceSnapshot
export function structuralKey(snap: WorkspaceSnapshot): string   // stable sorted-key JSON without capturedAt
export async function hashPayload(snap: WorkspaceSnapshot): Promise<string> // sha256Hex(TextEncoder(structuralKey))
```
sessionMeta per spec §3.3 (dedupe per `(hostId, sessionCode)`; first pane wins; `cwd` from `content.rebuild?.cwd` only
when a non-empty string). Tests: spec §7 P1 payload bullets; mock `../host-api` and assert `listSessions` never called.

Commit: `feat(spa): device state payload builder`

## T4 — SPA API client (`spa/src/lib/device-state/api.ts` + test)

```ts
export interface DeviceStateSummary { clientId: string; deviceName: string; appVersion: string; capturedAt: number; updatedAt: number; workspaceCount: number; tabCount: number }
export interface DeviceStateRecord extends DeviceStateSummary { payload: WorkspaceSnapshot }
export class DeviceStateApiError extends Error { constructor(readonly status: number, message?: string) }
export function putDeviceState(hostId: string, clientId: string, body: { deviceName: string; appVersion: string; capturedAt: number; payload: WorkspaceSnapshot }): Promise<{ stored: boolean }>
export function listDeviceStates(hostId: string): Promise<DeviceStateSummary[]>
export function getDeviceState(hostId: string, clientId: string): Promise<DeviceStateRecord>   // shape-guards payload
export function deleteDeviceState(hostId: string, clientId: string): Promise<void>
```
`clientId` URL-encoded. Shape guard: export `isWellFormedSnapshot` from `lib/snapshot/storage.ts` (add `version === 1`
check inside the exported wrapper `isWellFormedSnapshotV1`, keep existing callers unchanged) — this task owns that
one-line export change. Tests: mock `hostFetch`; success paths, non-2xx → `DeviceStateApiError.status`, malformed payload → throws.

Commit: `feat(spa): device state API client`

## T5 — SPA identity + status store

Files: `spa/src/stores/useDeviceStateStore.ts` (+test), `spa/src/lib/device-state/device-name.ts` (+test),
`spa/src/lib/storage` STORAGE_KEYS (add `DEVICE_STATE: 'purdex-device-state'` following existing naming).

- Store per spec §3.5; persist only `deviceName` (partialize); `syncManager.register` like other stores.
- `device-name.ts`: `parseUserAgentName(ua: string): string`, `resolveDefaultDeviceName(): Promise<string>`
  (Electron `localDaemonStatus` with a 1500 ms timeout → hostname; any error/empty → UA name).
- `effectiveDeviceName(state)` selector exported.
- Tests: spec §7 P1 store + default-name bullets (UA matrix: Chrome/macOS, Safari/macOS, Firefox/Windows,
  Edge/Windows, Chrome/Linux, Safari/iOS, Chrome/Android, unknown → `Browser`).

Commit: `feat(spa): device identity store`

## T6 — Uploader (`spa/src/lib/device-state/uploader.ts` + test, `spa/src/main.tsx`)

```ts
export interface UploaderDeps { debounceMs?: number; now?: () => number; appVersion?: string }
export function startDeviceStateUploader(deps?: UploaderDeps): () => void
```
Behaviour exactly spec §3.6. `appVersion` default from the SPA's existing version constant (find the one
Settings/About uses; fall back to `''`). Also calls `resolveDefaultDeviceName()` once and stores it via
`useDeviceStateStore.setState({ defaultDeviceName })`. `main.tsx`: call right after `startBackupAutoTrigger()`.
Tests (fake timers): the full spec §7 uploader list.

Commit: `feat(spa): device state uploader`

## T7 — `DeviceStateSection` (P1 UI) + i18n

Files: `spa/src/components/settings/device-state/DeviceStateSection.tsx` (+test),
`spa/src/components/settings/SnapshotSettingsSection.tsx` (one render line at the end of its content),
`spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`.

Per spec §3.7. Keys (follow existing interpolation syntax in the locale files):

| key | en | zh-TW |
|---|---|---|
| `settings.device_state.title` | Device state backup | 各電腦狀態備份 |
| `settings.device_state.desc` | This computer's workspaces and tabs are saved to the Development host automatically. | 本機的 workspace 與分頁會自動存到 Development 選定的 host。 |
| `settings.device_state.device_name` | This computer | 本機名稱 |
| `settings.device_state.device_name_aria` | Computer name | 電腦名稱 |
| `settings.device_state.device_name_reset` | Use default | 使用預設 |
| `settings.device_state.target` | Saved to | 存放位置 |
| `settings.device_state.target_none` | Not set — choose a host in Settings > Development | 未設定——請到 設定 > Development 選擇 host |
| `settings.device_state.status.idle` | Waiting for changes | 等待變更 |
| `settings.device_state.status.uploading` | Saving… | 儲存中… |
| `settings.device_state.status.ok` | Saved {time} | 已儲存（{time}） |
| `settings.device_state.status.offline` | Host offline — will save when it reconnects | Host 離線，連線後自動儲存 |
| `settings.device_state.status.no_target` | No storage host | 未設定存放 host |
| `settings.device_state.status.error` | Save failed: {message} | 儲存失敗：{message} |

Tests: renders name (default vs custom), rename commit via Enter/blur (IME-safe), reset, target none vs host name,
each status kind.

Commit: `feat(spa): device state settings block`

## T8 — Gates

`go test ./...` · `go vet ./internal/module/devicestate/...` · `pnpm exec vitest run` · `pnpm run lint` · `pnpm run build` —
all green; any fix in its own commit.

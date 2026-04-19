# Sync P1 — History + Restore Design

> 使用者可以瀏覽過往的 sync snapshot 並還原，匯錯一包救得回來。

## 背景

P0（PR #432）完成體質清理後，Sync 系統已具備 Foundation（#384）、Manual/Daemon Provider（#393）、ConflictBanner（#432）。但目前沒有「時間回溯」能力：

- Auto-sync 後如果發現拉進來一份壞資料，無法回到上一份狀態
- 使用者匯入一個錯的 `.purdex-sync` 檔後，原本的環境就沒了
- daemon 雖然每次 push 都存了 `sync_bundles`，但 client 端沒有 UI 能看

P1 的目標：補齊本地 snapshot 儲存、跨裝置歷史瀏覽、一鍵還原。屬於 Sync 路線圖第二階段（P0 ✅ → **P1** → P3 → P5，本階段排除 P2/P4/P6）。

## 決策摘要

| 面向 | 決策 |
|------|------|
| Scope | 本地 snapshot + daemon remote 分頁顯示 + 還原 |
| 本地儲存 | 純 IndexedDB（SPA / Electron 共用一套） |
| 建立時機 | 有實質變動才建（去重）+ 破壞性操作前建（pre-op） |
| Retention | Tiered（hourly/daily/weekly/monthly）+ pre-op 獨立 pool，SPA 與 daemon 共用 policy |
| UI 容器 | Settings subpage `/settings/sync/history`，沿用 P0 deep-link infra |
| 清單顯示 | Local / Remote 兩個 tab（語義不同，不合併） |
| 詳情內容 | metadata + 與當前狀態的 diff summary（不做 per-field diff） |
| Restore 流程 | 單一 confirm modal + 自動建 pre-restore snapshot + 清 pendingConflicts + 若為 remote 來源則 update lastSyncedBundle |
| Remote pull 策略 | `listHistory` 只回 metadata，點詳情時 lazy fetch bundle |

## §1 架構

```
┌─────────────────────────────────────────────────────────────────┐
│ SPA                                                              │
│ ┌─────────────────────┐  ┌────────────────────────────────────┐ │
│ │ SyncHistoryPage     │  │ SyncEngine                         │ │
│ │ /settings/sync/     │→ │ ┌──────────────────────────────┐   │ │
│ │   history           │  │ │ SnapshotStore (new, IDB)     │   │ │
│ │  - Local tab        │  │ │  - listLocal / getLocal      │   │ │
│ │  - Remote tab       │  │ │  - createSnapshot            │   │ │
│ │  - Detail pane      │  │ │  - compact (tiered)          │   │ │
│ │  - Restore modal    │  │ └──────────────────────────────┘   │ │
│ └─────────────────────┘  │ ┌──────────────────────────────┐   │ │
│                          │ │ Provider.listHistory         │   │ │
│                          │ │ Provider.getSnapshotBundle   │   │ │
│                          │ │   (NEW — lazy bundle fetch)  │   │ │
│                          │ └──────────────────────────────┘   │ │
│                          └────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
                                        ↕
┌─────────────────────────────────────────────────────────────────┐
│ Go daemon (internal/module/sync)                                 │
│  - sync_bundles table（既有）                                      │
│  - compactor.go（新）tiered policy                                │
│  - GET /api/sync/history                 — list metadata          │
│  - GET /api/sync/history/:id/bundle      — NEW 回傳單筆 bundle     │
└─────────────────────────────────────────────────────────────────┘
```

### 三個新模組

1. **`SnapshotStore`**（SPA，IndexedDB 封裝，tiered compaction logic）
2. **`SyncHistoryPage`** + `HistoryList` + `SnapshotDetail`（SPA UI）
3. **Daemon `compactor.go`**（Go，tiered policy porting）

### SyncEngine / useSyncStore 改動

- `export()` / `importBundle()` / auto-pull / manual import 成功收尾時呼叫 `SnapshotStore.createSnapshot(bundle, trigger)`
- restore 流程前先呼叫 `createSnapshot(currentBundle, 'pre-restore')` 再做 deserialize

### Provider 介面擴充

```typescript
interface SyncProvider {
  // existing
  push(bundle: SyncBundle): Promise<void>
  pull(): Promise<SyncBundle | null>
  listHistory(limit: number): Promise<SyncSnapshot[]>
  // NEW
  getSnapshotBundle(id: string): Promise<SyncBundle>
}
```

- `Manual`：拋 `not-supported`（Manual 沒有 remote 歷史）
- `Daemon`：打 `GET /api/sync/history/:id/bundle`
- `File`（P3）：之後自己實作
- `listHistory()` 語義不變（只回 metadata，不夾 bundle）

## §2 Components

### SPA · `spa/src/lib/sync/snapshot-store.ts`

IndexedDB 封裝，database `purdex-sync`, object store `snapshots`。

```typescript
type SnapshotTrigger = 'auto' | 'manual' | 'pre-import' | 'pre-restore'

interface StoredSnapshot {
  id: string                             // nanoid
  timestamp: number                      // ms epoch
  device: string                         // from SyncBundle.meta.device
  trigger: SnapshotTrigger
  bundle: SyncBundle                     // full payload
  bundleSize: number                     // bytes (cached for UI)
  moduleCounts: Record<string, number>   // { workspaces: 3, hosts: 2, ... }
}

interface SnapshotStore {
  init(): Promise<void>
  listLocal(): Promise<StoredSnapshot[]>           // newest first
  getLocal(id: string): Promise<StoredSnapshot | null>
  createSnapshot(bundle: SyncBundle, trigger: SnapshotTrigger): Promise<StoredSnapshot>
  deleteLocal(id: string): Promise<void>
  compact(): Promise<{ kept: string[]; evicted: string[] }>
  clear(): Promise<void>
}
```

- `moduleCounts` 在 `createSnapshot` 時從 bundle 計算一次
- `compact()` 每次 `createSnapshot` 後自動呼叫（以 promise queue 序列化，避免並發）
- 實作使用 [`idb`](https://github.com/jakearchibald/idb) 套件（`IDBDatabase` 的 Promise wrapper，專案目前無此依賴，P1 引入）

### SPA · `spa/src/lib/sync/snapshot-compaction.ts`

```typescript
interface Bucket {
  kind: 'hourly' | 'daily' | 'weekly' | 'monthly' | 'pre-op'
  bucketKey: string   // 一律以 UTC 計算，避開 DST / 時區切換造成同一筆 snapshot 跨 bucket
                      // hourly: '2026-04-19T13Z' | daily: '2026-04-19Z'
                      // weekly: ISO week '2026-W16' | monthly: '2026-04'
  snapshots: StoredSnapshot[]
}

// Tiered policy（P1 採用）
// Hourly: 最近 24 小時內，每小時 bucket 保留最新一筆（max 24）
// Daily: 1–30 天前，每天 bucket 保留最新一筆（max 30）
// Weekly: 30–90 天前，每週 bucket 保留最新一筆（max ~13）
// Monthly: >90 天，每月 bucket 保留最新一筆（max 12）
// Pre-op pool: 獨立 ring，永不因時間老化，只看數量（max 5）
// 合計最大約 79 + 5 = 84 筆，估 0.8-8 MB（bundle 10-100KB）

function computeCompaction(all: StoredSnapshot[], now: number): {
  kept: StoredSnapshot[]
  evicted: StoredSnapshot[]
}
```

純函式：輸入全量 snapshot + 當前時間，輸出保留清單和淘汰清單。`SnapshotStore.compact()` 消費這個函式並對 IDB 發 DELETE。

### SPA · `spa/src/features/settings/sections/sync-history/`

```
SyncHistoryPage.tsx      — 主頁，訂閱 route param + provider
├─ HistoryTabs.tsx       — Local / Remote 分頁（Remote 在非 daemon provider 時顯示 "Not supported"）
├─ HistoryList.tsx       — 左側 snapshot list
│  └─ HistoryRow.tsx     — 單筆列（trigger icon + relative time + bucket tag）
├─ SnapshotDetail.tsx    — 右側詳情（metadata + diff summary + Restore button）
├─ RestoreConfirmDialog.tsx — 沿用專案既有 ConfirmDialog，加自訂內容
└─ hooks/
   ├─ useLocalHistory.ts — 訂閱 SnapshotStore（透過 Zustand store 介面）
   ├─ useRemoteHistory.ts — 從 activeProvider.listHistory() 拉 + cache
   └─ useSnapshotDiff.ts — 算 moduleCounts diff（snapshot vs current state）
```

- 走 P0 的 `/settings/<section>` 路由，延伸為 `/settings/sync/history`
- SyncSection 原有的 "View Sync History" 按鈕走 `useLocation(() => '/settings/sync/history')`
- SettingsPage 解析 `/settings/sync/history` 時切到新的 `SyncHistoryPage`
- Breadcrumb / 返回鍵回 `/settings/sync`
- **Settings nav 不加 History 子項**，只能透過 Sync section 按鈕進入

### SPA · `useSyncStore` 擴充

```typescript
interface SyncStoreActions {
  // NEW
  createPreOperationSnapshot(trigger: 'pre-import' | 'pre-restore'): Promise<string>
  restoreFromSnapshot(snapshot: StoredSnapshot, source: 'local' | 'remote'): Promise<void>
}
```

`restoreFromSnapshot` 是單一入口，綁定四件事：
1. `createPreOperationSnapshot('pre-restore')` — 安全網
2. `clearPendingConflicts()` — 清 P0 的 pending state
3. 對每個 contributor 派發 `deserialize(bundle.data[contributor.id])`
4. 若 `source === 'remote'` 且 active provider 存在 → `setLastSyncedBundle(bundle)`

### Daemon · `internal/module/sync/compactor.go`

```go
type Bucket struct {
    Kind    string    // "hourly" | "daily" | "weekly" | "monthly" | "pre-op"
    Key     string    // bucket identifier
    KeepID  string    // snapshot id to keep in this bucket
}

// Compact runs after each successful POST /api/sync/push
// per clientId
func (m *Module) compact(ctx context.Context, clientID string) error {
    // tx BEGIN
    // SELECT id, timestamp, trigger FROM sync_bundles WHERE client_id = ?
    // bucketize by tiered policy
    // for each bucket: keep newest, DELETE others
    // tx COMMIT
}
```

與 SPA `computeCompaction` policy 完全相同（同 24/30/13/12/5 數字），但以 Go 獨立實作。共用 table-driven test fixture 保證 parity。

### Daemon · `GET /api/sync/history/:id/bundle`

```go
func (m *Module) handleGetSnapshotBundle(w http.ResponseWriter, r *http.Request) {
    // 1. auth: ticket or clientId match（沿用既有 middleware）
    // 2. Parse :id from URL
    // 3. SELECT bundle FROM sync_bundles WHERE id = ? AND client_id = ?
    //    404 若不存在 or clientId mismatch
    // 4. 回傳 JSON (SyncBundle)
}
```

## §3 Data flow

### 建立 snapshot（四種觸發）

```
auto-sync 完成
  → compare new bundle vs lastSyncedBundle (ignoring .meta.device + .meta.timestamp)
  → if equal: skip (dedup)
  → if different: SnapshotStore.createSnapshot(bundle, 'auto') → compact()

manual sync
  → 同上，trigger='manual'，不 dedup（使用者明確意圖）

匯入前
  → SyncSection.handleImport: createPreOperationSnapshot('pre-import') → importBundle(bundle)

還原前
  → useSyncStore.restoreFromSnapshot:
    createPreOperationSnapshot('pre-restore')
      → clearPendingConflicts()
      → deserialize 各 contributor
      → if remote: setLastSyncedBundle(bundle)
```

### Dedup 判斷

```typescript
function equalExceptMeta(a: SyncBundle, b: SyncBundle): boolean {
  // compare a.data vs b.data (deep equal)
  // ignore a.meta.device, a.meta.timestamp, a.meta.version
  // other meta fields（e.g. future schemaVersion）正常比對
}
```

### 載入 Local history

```
SyncHistoryPage mount
  → useLocalHistory() → SnapshotStore.listLocal()
  → 回傳 StoredSnapshot[] 含完整 bundle（但 list 只用 metadata）
  → Zustand cache，onChange 時 invalidate
使用者點一筆
  → SnapshotDetail 從 cache 讀完整資料 + 算 diff
```

### 載入 Remote history

```
SyncHistoryPage mount + activeProvider 是 daemon
  → useRemoteHistory() → provider.listHistory(200)   ← P1 上限 200，足以覆蓋 tiered policy 滿載
  → GET /api/sync/history?clientId=xxx
  → 回傳 SyncSnapshot[]（只有 metadata）
  → 渲染 HistoryList（bundle 欄位 null）
使用者點一筆
  → SnapshotDetail 發現 bundle === null
  → provider.getSnapshotBundle(snapshot.id)
  → GET /api/sync/history/:id/bundle
  → cache 到 component-level Map<snapshotId, SyncBundle>（不 persist）
    - 切換 snapshot 時沿用 cache，避免來回切點重複打 API
    - 離開 SyncHistoryPage unmount 時整個 Map 丟棄
  → 算 diff + 顯示
```

### `getSnapshotBundle` 非 daemon provider 行為

- Manual Provider：拋 `SnapshotNotSupportedError`
- UI 層：Remote tab 根據 provider 類型決定是否顯示；Manual 下 Remote tab 顯示 `settings.sync.history.tabs.remoteUnsupported` 空狀態，`getSnapshotBundle` 不會被呼叫

### Restore 流程

```
使用者在詳情頁按 Restore
  → 開 ConfirmDialog：
    "Restore this snapshot from <timestamp>?
     Your current state will be overwritten.
     A pre-restore backup will be created automatically."
    [若有 pending conflicts 追加：
     "This will discard <N> pending conflicts."]
  → [Cancel] [Restore]
按 Restore:
  setRestoring(true)（防雙點）
  restoreFromSnapshot(snapshot, source)
  toast: "Restored from <relative time>"
  navigate back to /settings/sync
```

### Compaction 時機

- **SPA**：`createSnapshot()` 後同步呼叫 `compact()`；以 promise queue 序列化，避免並發
- **Daemon**：`POST /api/sync/push` handler 尾端呼叫 `m.compact(clientID)`，失敗只 log 不阻斷 push
- **不做背景定時任務**：避免 Electron 睡眠 / daemon reload 造成時序錯亂

## §4 Error handling

### IndexedDB 層

| 情境 | 行為 |
|------|------|
| IDB 不可用（privacy mode / edge browser） | `init()` fail 時設 error state；UI 顯示「This browser does not support snapshot history」；auto-sync 不建 snapshot，但 sync 本身照常 |
| Quota exceeded | `createSnapshot` catch → 強制 `compact()` → 重試 1 次 → 仍失敗則 toast 警告，不 block sync；pre-op 若失敗則 restore 中止 |
| Bundle JSON 毀損 | 該 row 隔離，list 跳過，Detail 顯示 "Corrupted" + delete 按鈕 |

### Remote / Daemon 層

| 情境 | 行為 |
|------|------|
| `listHistory` 失敗 | Remote tab inline error + Retry；不影響 Local tab |
| `getSnapshotBundle` 失敗 | Detail 頁 error banner + Retry；Restore 按鈕 disabled |
| TOCTOU（選取時 daemon 已刪） | 404 → 顯示「This snapshot no longer exists」 + 自動 refresh remote list |
| Daemon compaction 失敗 | push 流程不阻斷；下次 push 再試；錯誤只 log |

### Restore 流程

| 情境 | 行為 |
|------|------|
| pre-restore snapshot 建立失敗 | **中止 restore**，顯示 "Failed to create backup. Restore cancelled." |
| Deserialize 某 contributor 拋 exception | try/catch 包每個；失敗的 log + 計入 warnings；最後 toast "Restored with warnings: failed to restore <X>" |
| 頁面 reload 中斷 | restore 是純 local，不依賴 network；最新 persist state 反映已完成部分 |
| 使用者連按兩次 Restore | UI 層 setRestoring(true) + button disable；store action idempotent key 防護 |

### Compaction

| 情境 | 行為 |
|------|------|
| 並發 createSnapshot | Promise queue 序列化 |
| SPA compact 刪錯 | 靠單元測試保證；runtime 不加 rollback |
| Daemon tx 失敗 | tx rollback；下次 push 再試 |

### pendingConflicts 邊界

| 情境 | 行為 |
|------|------|
| 有 conflicts 時 restore | Dialog 警告含 discard count；確認後清 |
| 有 conflicts 時看 history | 不 block，允許瀏覽 |

### UI 錯誤分級

- **Transient**（network / retry-able）→ inline banner + Retry
- **Terminal**（corrupted / not supported）→ empty-state 位含說明
- **Operation failure** → toast + 點開詳情

## §5 Testing

### SPA · Vitest

| 測試對象 | 覆蓋重點 |
|---------|---------|
| `SnapshotStore` | create / list / get / delete / clear；`fake-indexeddb`；moduleCounts、bundleSize 計算 |
| `computeCompaction` tiered policy | `vi.useFakeTimers` + 時間分佈固定輸入 → 驗證每 bucket 留最新；pre-op pool 與 time-tier 不互擾；邊界（空、單筆、只有 pre-op） |
| `equalExceptMeta` | 同 data 不同 meta → true；改一欄 → false；nested 差異 |
| `useSyncStore.restoreFromSnapshot` | 整合：mock SnapshotStore + contributors；驗證執行順序、source=local 不更新 lastSyncedBundle、pre-restore 失敗時中止 |
| `useSyncStore.createPreOperationSnapshot` | 兩種 trigger；失敗向上拋 |

### SPA · UI (Vitest + RTL)

| 測試對象 | 覆蓋重點 |
|---------|---------|
| `SyncHistoryPage` | 空狀態；有 local / 無 remote；provider 切換 toggle；deep link 預設 tab |
| `HistoryList` | 排序；trigger tag；bucket tag；選中高亮 |
| `SnapshotDetail` | metadata 顯示；diff summary 計算；Restore disabled 條件；error banner |
| `RestoreConfirmDialog` | pending conflicts 警告有/無；spinner；double-click 防護 |
| `HistoryTabs` | Local/Remote 切換；non-daemon provider 的 Remote "Not supported" |

### Daemon · Go

| 測試對象 | 覆蓋重點 |
|---------|---------|
| `compactor.go` | Table-driven policy tests（parity with SPA）；tx；空 DB；單筆；1000 筆性能 |
| `handleGetSnapshotBundle` | 正常；404；auth fail；clientId mismatch |
| push + compact 整合 | 成功 push 後 sync_bundles 符合 policy |

### 手動 integration（PR test plan）

1. 連續 sync 數十次 → 觀察 hourly/daily bucket 形成
2. 塞時間戳假資料到 daemon DB → 觀察 daily/weekly/monthly 壓縮
3. snapshot → import 壞 bundle → 驗證 pre-import snapshot → restore 回來 → 驗證 state
4. 拔網路 → Remote tab error + Retry
5. 雙 window 並發 auto-sync → compaction mutex 無重刪
6. i18n 切 zh-TW/en 檢查新增 key
7. 有 pending conflicts 時 restore → warning 顯示 → conflicts 被清
8. 塞滿 IDB → createSnapshot 失敗 gracefully
9. Provider 切換（daemon → manual → daemon）→ Remote tab 行為正確
10. 清空 history → 重新同步 → 清單長回

### 覆蓋率目標

- SPA 新 code：行覆蓋 > 90%（SnapshotStore / compaction 核心必須 100%）
- Go 新 code：函式覆蓋 > 85%

## §6 新增依賴

- `idb`（Jake Archibald，~4KB gzipped，MIT）— IndexedDB 的 Promise wrapper，SPA 直接 import
- `fake-indexeddb`（dev-only）— Vitest 環境的 IDB mock

## §7 i18n

估 ~30 新 key，全部放 `settings.sync.history.*`：

```
settings.sync.history.title
settings.sync.history.tabs.local
settings.sync.history.tabs.remote
settings.sync.history.tabs.remoteUnsupported
settings.sync.history.empty.local
settings.sync.history.empty.remote
settings.sync.history.trigger.auto
settings.sync.history.trigger.manual
settings.sync.history.trigger.preImport
settings.sync.history.trigger.preRestore
settings.sync.history.bucket.hourly
settings.sync.history.bucket.daily
settings.sync.history.bucket.weekly
settings.sync.history.bucket.monthly
settings.sync.history.bucket.preOp
settings.sync.history.detail.metadata
settings.sync.history.detail.diff.added_one
settings.sync.history.detail.diff.added_other
settings.sync.history.detail.diff.removed_one
settings.sync.history.detail.diff.removed_other
settings.sync.history.detail.diff.changed
settings.sync.history.detail.diff.noChange
settings.sync.history.detail.restore
settings.sync.history.restore.confirmTitle
settings.sync.history.restore.confirmBody
settings.sync.history.restore.confirmPendingConflicts_one
settings.sync.history.restore.confirmPendingConflicts_other
settings.sync.history.restore.cancel
settings.sync.history.restore.proceed
settings.sync.history.restore.success
settings.sync.history.restore.failedBackup
settings.sync.history.restore.warnings
settings.sync.history.error.loadList
settings.sync.history.error.loadBundle
settings.sync.history.error.notFound
settings.sync.history.error.corrupted
settings.sync.history.notSupported
```

## §8 不做（YAGNI）

- Per-field diff viewer（spec §3 原則）
- 背景定時 compaction
- 手動 snapshot 建立（「幫我現在存一份」按鈕）— trigger 已涵蓋
- Snapshot export as file（未來 P3/P4 再談）
- Snapshot 分類 / 標籤
- Cross-device restore 通知（另一台 restore 了，本機會收到推播）
- History retention 的 UI 設定頁（policy 寫死在 code 裡，不給調）

## §9 相依

- **前置**：P0 體質清理（PR #432）已 merge 至 alpha.164 — 已滿足
- **後續**：P3 File Provider 可重用 `Provider.getSnapshotBundle` 介面；P5 content-addressed 會改 bundle 儲存形式、屆時需調整 snapshot 儲存策略

## §10 文件位置

- 本 Spec：`docs/superpowers/specs/2026-04-19-sync-p1-history-design.md`
- 原架構 Spec：`docs/superpowers/specs/2026-04-16-sync-architecture-design.md`
- 原 Plan：`docs/superpowers/plans/2026-04-16-sync-architecture.md`
- 實作 Plan（下一步 writing-plans 產出）：`docs/superpowers/plans/2026-04-19-sync-p1-history.md`

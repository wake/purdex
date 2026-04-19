# Sync P1 — History + Restore Design

> 使用者可以瀏覽過往的 sync snapshot 並還原，匯錯一包救得回來。

## Revision Log

- **2026-04-19 r1**：初稿（Brainstorm 定調）
- **2026-04-19 r2**：三位 subagent review（攻擊 / 防守 / code-alignment）後全面修正
  - 修 A 類 11 項：SyncBundle 結構、daemon 表名與欄位、FIFO prune 衝突、雙層路由、restore 語義、函式命名、timestamp 單位、id 型別、limit clamp、pre-import 時序、host token 保留
  - B 決策：restore 走 append-only（B1）/ daemon-specific getSnapshotBundle（B2）/ 拆 PR A+B（B3）/ contributor-level deepEqual diff（B4）/ metadata-only listLocal（B5）/ session-start pristine 釘住（B6）/ 改名 `SnapshotHistoryPage`（B7）

## 背景

P0（PR #432，alpha.164）完成體質清理後，Sync 系統已具備 Foundation（#384）、Manual/Daemon Provider（#393）、ConflictBanner（#432）。但目前沒有「時間回溯」能力：

- Auto-sync（未來引入）拉壞資料時無法回上一份
- 匯入壞 `.purdex-sync` 檔後，原本的環境就沒了
- daemon 每次 push 都存 `sync_history`，但 client 端沒 UI 看

P1 的目標：補齊本地 snapshot 儲存、跨裝置歷史瀏覽、一鍵還原。Sync 路線圖第二階段（P0 ✅ → **P1** → P3 → P5，本階段排除 P2/P4/P6）。

**當前 VERSION**：main 已至 alpha.187；本 spec 提交的 worktree 從最新 main 分岔。

## 決策摘要

| 面向 | 決策 |
|------|------|
| Scope | 本地 snapshot + daemon remote 分頁顯示 + 還原 |
| **交付策略** | **拆 PR A（SPA-only）+ PR B（Daemon endpoint + Remote tab）** |
| 本地儲存 | 純 IndexedDB（SPA + Electron 共用一套） |
| 建立時機 | 有實質變動才建（去重）+ 破壞性操作前建（pre-op） |
| Retention | Tiered（hourly/daily/weekly/monthly）+ pre-op 獨立 pool；SPA 與 daemon 同 policy |
| UI 容器 | Settings subpage `/settings/sync/history`（P0 deep-link infra 需擴充雙層支援） |
| 清單顯示 | Local / Remote 兩個 tab（分頁不合併） |
| 詳情差異 | **Contributor 粒度 deepEqual**（identical / changed），不做 per-field diff |
| Restore 語義 | **Append-only**：僅更新 local state + 建 pre-restore 備份 + 清 pendingConflicts；**不動 lastSyncedBundle**，下次 auto-sync 自然走三方比對並可能 push 一筆新 history |
| Remote pull 策略 | `listHistory` 回 metadata，點詳情時 lazy fetch bundle |
| Provider 介面 | `getSnapshotBundle` **為 Daemon 專屬方法**，不上 `SyncProvider` 介面 |

## §1 架構

```
┌─────────────────────────────────────────────────────────────────┐
│ SPA                                                              │
│ ┌─────────────────────┐  ┌────────────────────────────────────┐ │
│ │ SnapshotHistoryPage │  │ SyncEngine + SnapshotStore         │ │
│ │ /settings/sync/     │→ │ ┌──────────────────────────────┐   │ │
│ │   history           │  │ │ SnapshotStore (new, IDB)     │   │ │
│ │  - Local tab        │  │ │  - listLocal (metadata-only) │   │ │
│ │  - Remote tab       │  │ │  - getLocal(id)  (+bundle)   │   │ │
│ │  - Detail pane      │  │ │  - createSnapshot            │   │ │
│ │  - Restore dialog   │  │ │  - compact (tiered)          │   │ │
│ └─────────────────────┘  │ └──────────────────────────────┘   │ │
│                          │ ┌──────────────────────────────┐   │ │
│                          │ │ DaemonProvider               │   │ │
│                          │ │  + getSnapshotBundle (NEW,   │   │ │
│                          │ │    daemon-specific)          │   │ │
│                          │ └──────────────────────────────┘   │ │
│                          └────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
                                        ↕
┌─────────────────────────────────────────────────────────────────┐
│ Go daemon (internal/module/sync)                                 │
│  - sync_history table（既有，P1 ALTER 加 trigger 欄位）              │
│  - compactor.go（新）tiered policy；per group_id 為 scope           │
│  - 移除 store.go:197-203 既有 LIMIT 100 FIFO prune                 │
│  - GET /api/sync/history                   — list metadata        │
│  - GET /api/sync/history/:id/bundle        — NEW 回傳單筆 bundle    │
└─────────────────────────────────────────────────────────────────┘
```

### 新增 / 改動模組

**SPA（PR A 範疇）**
1. `SnapshotStore`（IDB wrapper，tiered compaction logic）
2. `SnapshotHistoryPage` + `HistoryList` + `SnapshotDetail` + `SnapshotRestoreDialog`（新）
3. `useSyncStore` 新 actions：`createPreOperationSnapshot`、`restoreFromSnapshot`
4. 路由基礎設施擴充：`route-utils.ts` 的 `SETTINGS_SECTION_PATTERN` 放寬至雙層；`SettingsPage` 支援 subsection 路由
5. 現有 contributors 的 `deserialize` 須正確處理「secret preservation」—— P1 具體是 `hosts` 的 `token` 欄位（見 §4）

**SPA（PR B 範疇）**
6. `DaemonProvider.getSnapshotBundle(id)`（新方法，非介面擴充）
7. `useRemoteHistory` hook（lazy fetch + unmount 丟快取）
8. `HistoryTabs` 支援 Remote tab 切換

**Daemon（PR B 範疇）**
9. `internal/module/sync/compactor.go`（新，porting SPA tiered policy）
10. `GET /api/sync/history/:id/bundle` handler（新）
11. 移除 `store.go:197-203` 的 `LIMIT 100` FIFO prune
12. Migration：`ALTER TABLE sync_history ADD COLUMN trigger TEXT DEFAULT 'auto'`；push handler 接受 `trigger` query param 並寫入

### Provider 介面策略（B2）

`SyncProvider` 介面不動。`DaemonProvider` 另加 `getSnapshotBundle(id): Promise<SyncBundle>` 為類型層級的額外方法。UI 層用 type narrow：

```typescript
// 使用處
if (provider instanceof DaemonProviderClass) {
  const bundle = await provider.getSnapshotBundle(id)
}
```

或 `provider.id === 'daemon' ? (provider as DaemonProvider) : ...`。Manual / File Provider 未來若要支援，各自加自己的 method，UI 層以策略分派。

### 路由擴充（解 A4）

- **`route-utils.ts`**：`SETTINGS_SECTION_PATTERN` 擴為 `^[a-z0-9-]+(\/[a-z0-9-]+)?$`；`parseRoute` 拆 `section` + `subsection` 兩段
- **`SettingsPage.tsx`**：self-heal effect 判斷若 `section` 有效但 `subsection` 無效才 replace；Subsection 由 section-level component 接手 dispatch
- **SyncSection**：新增 "View Sync History" 按鈕；內部路由到 `/settings/sync/history` 時換上 `SnapshotHistoryPage`

## §2 Components

### SPA · `spa/src/lib/sync/snapshot-store.ts`（PR A）

IndexedDB 封裝，DB `purdex-sync`, objectStore `snapshots`。

```typescript
type SnapshotTrigger = 'auto' | 'manual' | 'pre-import' | 'pre-restore'
// P1 實際會出現的 trigger 只有 manual / pre-import / pre-restore
// 'auto' 為未來 auto-sync feature 保留，目前無 callsite

interface SnapshotMetadata {
  id: string                             // nanoid（SPA 本地生成）
  timestamp: number                      // ms epoch
  device: string                         // from SyncBundle.device（top-level，非 meta）
  trigger: SnapshotTrigger
  bundleSize: number                     // bytes, new TextEncoder().encode(json).byteLength
  contributorIds: string[]               // bundle.collections 的 keys
  isSessionPristine: boolean             // session-start pre-op pinning（B6）
}

interface StoredSnapshot extends SnapshotMetadata {
  bundle: SyncBundle                     // full payload
}

interface SnapshotStore {
  init(): Promise<void>
  listLocal(): Promise<SnapshotMetadata[]>          // B5: metadata only
  getLocal(id: string): Promise<StoredSnapshot | null>
  createSnapshot(bundle: SyncBundle, trigger: SnapshotTrigger, opts?: { isSessionPristine?: boolean }): Promise<SnapshotMetadata>
  deleteLocal(id: string): Promise<void>
  compact(): Promise<{ kept: string[]; evicted: string[] }>
  clear(): Promise<void>
}
```

- 使用 `idb`（Jake Archibald）封在 `lib/storage/idb.ts` 模組，sync 層不直接 import `idb`
- `contributorIds` 用於 list 顯示「4 modules」；`bundleSize` 用於顯示大小
- Detail pane 要完整 diff 再呼叫 `getLocal(id)` 取 bundle
- `createSnapshot` 於結尾呼叫 `compact()`；以 Promise queue 序列化避免並發
- `bundleSize`：`new TextEncoder().encode(JSON.stringify(bundle)).byteLength`（解 M2）

### SPA · `spa/src/lib/sync/snapshot-compaction.ts`（PR A）

```typescript
interface Bucket {
  kind: 'hourly' | 'daily' | 'weekly' | 'monthly' | 'pre-op'
  bucketKey: string
  // 一律 UTC 計算，避開 DST / 時區造成同一筆 snapshot 跨 bucket
  // hourly: '2026-04-19T13Z' (YYYY-MM-DDTHHZ)
  // daily:  '2026-04-19Z'    (YYYY-MM-DDZ)
  // weekly: 'YYYY-Www'       (ISO week)
  // monthly:'YYYY-MM'
  // pre-op: trigger 為 'pre-import' | 'pre-restore' 者進這池
  snapshots: SnapshotMetadata[]
}

// Tiered policy
// Hourly: 最近 24 小時，每小時 bucket 保留最新一筆 (max 24)
// Daily:  1–30 天前，每天 bucket 保留最新一筆 (max 30)
// Weekly: 30–90 天前，每週 bucket 保留最新一筆 (max ~13)
// Monthly:>90 天，每月 bucket 保留最新一筆 (max 12)
// Pre-op: 獨立 ring (max 5)，但 isSessionPristine=true 的不 evict（B6）
// 合計最大約 79 + 5 + 1(pristine) = 85 筆

function computeCompaction(all: SnapshotMetadata[], now: number): {
  kept: SnapshotMetadata[]
  evicted: SnapshotMetadata[]
}
```

分流規則（解 M12）：
- `trigger ∈ {'pre-import', 'pre-restore'}` → pre-op pool
- 其他 → time-tier（pre-op 也不擠 time-tier 的額度）
- `isSessionPristine === true` 的 pre-op 不計入 max 5，永不 evict

`computeCompaction` 為純函式，`SnapshotStore.compact()` 消費它並對 IDB 發 DELETE。

### SPA · `spa/src/features/settings/sections/sync-history/`（PR A）

```
SnapshotHistoryPage.tsx       — 主頁，訂閱 route param + provider
├─ HistoryTabs.tsx            — Local / Remote 分頁
│                               Remote tab 在 provider !== daemon 時 disabled + "Not supported"
├─ HistoryList.tsx            — 左側 snapshot list
│  └─ HistoryRow.tsx          — 單筆列（trigger icon + relative time + bucket tag）
├─ SnapshotDetail.tsx         — 右側詳情（metadata + per-contributor diff + Restore button）
├─ SnapshotRestoreDialog.tsx  — 新寫，非通用 ConfirmDialog
└─ hooks/
   ├─ useLocalHistory.ts      — Zustand slice，subscribe SnapshotStore metadata
   ├─ useRemoteHistory.ts     — lazy fetch + component-scoped Map cache
   └─ useSnapshotDiff.ts      — 以 contributor-level deepEqual 比較 snapshot vs current
```

- 重命名避開既有 `HistoryPage`（瀏覽紀錄）（解 L7）
- `SnapshotHistoryPage` 接收 URL subsection 路由參數 `/settings/sync/history`
- SyncSection 加 "View Sync History" 按鈕：`onClick = () => setLocation('/settings/sync/history')`
- 返回鍵 / breadcrumb 回 `/settings/sync`
- **Settings nav 不加 History 子項**，只從 Sync section 按鈕進入

### Diff summary（B4 升級）

```typescript
interface ContributorDiff {
  id: string
  status: 'identical' | 'changed' | 'missing-in-snapshot' | 'missing-in-current'
}

function computeSnapshotDiff(
  snapshotBundle: SyncBundle,
  currentBundle: SyncBundle,
): ContributorDiff[] {
  const ids = union(
    Object.keys(snapshotBundle.collections),
    Object.keys(currentBundle.collections),
  )
  return ids.map(id => {
    const s = snapshotBundle.collections[id]
    const c = currentBundle.collections[id]
    if (!s) return { id, status: 'missing-in-snapshot' }
    if (!c) return { id, status: 'missing-in-current' }
    return { id, status: deepEqualCollection(s, c) ? 'identical' : 'changed' }
  })
}
```

Detail pane 顯示每個 contributor 的狀態（identical / changed / missing）。`deepEqualCollection` 必須正確處理 `ChunkedPayload`（比 manifest hash，不走 JSON.stringify，解 M3）；`deepEqual` 從 `three-way-merge.ts` export（解 code-alignment D8）。

### SPA · `useSyncStore` 擴充（PR A）

```typescript
interface SyncStoreActions {
  // NEW（PR A）
  createPreOperationSnapshot(trigger: 'pre-import' | 'pre-restore'): Promise<string>
  restoreFromSnapshot(snapshot: StoredSnapshot, source: 'local' | 'remote'): Promise<void>
}
```

`restoreFromSnapshot` 做三件事（B1 精簡版）：
1. `createPreOperationSnapshot('pre-restore')` — 安全網；失敗則中止
2. `clearPendingConflicts()` — 清 P0 的 pending state
3. 對 snapshot.bundle.collections 的每個 contributor 派發 `deserialize(collections[id])`
   - **不動 `lastSyncedBundle` / `lastSyncedAt`**（B1 append-only）
   - 下次 sync 由 three-way merge 自然處理：local 已變 → 比對 remote canonical → 如果 diverge 產生衝突或產生新 push，daemon 自然 append 一筆新 history

### Daemon · `internal/module/sync/compactor.go`（PR B）

```go
type Bucket struct {
    Kind   string    // "hourly" | "daily" | "weekly" | "monthly" | "pre-op"
    Key    string    // bucket identifier (UTC)
    KeepID int64     // snapshot id 保留
}

// Compact runs after successful POST /api/sync/push
// 以 group_id 為 scope（非 client_id，解 C4 / D1）
func (m *Module) compact(ctx context.Context, groupID string) error {
    // BEGIN IMMEDIATE TRANSACTION（避免 SQLite WAL 並發 race，解 C9）
    // SELECT id, timestamp, trigger FROM sync_history WHERE group_id = ?
    // bucketize by tiered policy (per-device optional: per-client sub-bucket)
    // for each bucket: keep newest, DELETE others
    // COMMIT
}
```

- Policy 與 SPA `computeCompaction` 同（24/30/13/12/5）
- 共用 test fixture（同 JSON，SPA / Go 各自 load 比對）；parity CI 測試
- **移除既有 `store.go:197-203` 的 `LIMIT 100` prune**（解 C5 / T4）

### Daemon · Schema migration（PR B）

```sql
ALTER TABLE sync_history ADD COLUMN trigger TEXT DEFAULT 'auto';
```

- Alpha 階段不需 migration 機制（feedback_no_alpha_migration.md），直接改 schema
- Push handler 接收 `trigger` query param（`?trigger=manual`）並寫入；未帶時預設 `'auto'`

### Daemon · `GET /api/sync/history/:id/bundle`（PR B）

```go
func (m *Module) handleGetSnapshotBundle(w http.ResponseWriter, r *http.Request) {
    // 1. auth：沿用現有 clientId query param scope（daemon 目前無 ticket auth，解 T6）
    // 2. Parse :id (numeric)，JSON 回應 id 時 serialize 成 string（解 C3）
    // 3. SELECT bundle FROM sync_history WHERE id = ? AND group_id IN (SELECT group_id FROM sync_groups WHERE client_id = ?)
    // 4. 404 若 snapshot 不存在
    //    403 若 client 不在對應 group（可讓 UI 分辨，解 M11）
    // 5. 回傳 SyncBundle JSON；timestamp 欄位以毫秒表示（SPA 端解析不需 ×1000，解 M8）
}
```

**Daemon 端時間單位統一為 SPA 端期望的毫秒**：

- daemon 現存 `sync_history.timestamp` 為秒。P1 引入時：
  - Option (a)：daemon 欄位改 `timestamp_ms`，migrate 既有資料 `×1000`
  - Option (b)：保留 `timestamp` 秒欄位，wire layer 在 JSON serialize 時 `×1000`
- 本 spec 選 **(b)**：wire 層轉換，不動 DB schema；介面 JSON 一律毫秒

### Daemon · `handleHistory` limit（PR B）

- 既有 cap 100 放寬為 200（解 C6）
- Tiered policy 滿載 84 筆，預留 buffer 100% 足夠

## §3 Data flow

### Dedup 判斷（flat bundle shape，解 C1 / T1）

```typescript
// equalExceptEnvelope 忽略 top-level 的 envelope 欄位，deep compare collections
function equalExceptEnvelope(a: SyncBundle, b: SyncBundle): boolean {
  return deepEqualCollection(a.collections, b.collections)
}
```

- 忽略 `version`、`timestamp`、`device`（三者 envelope 欄位）
- 比對 `collections`：`ChunkedPayload` 走 manifest hash 比對，不走 JSON.stringify（解 M3）

### 建立 snapshot（四種觸發）

```
manual sync（使用者點 "Sync Now"）
  → SyncEngine.syncNow() 成功
  → if equalExceptEnvelope(new, lastSyncedBundle): skip (dedup，解 M4)
  → SnapshotStore.createSnapshot(bundle, 'manual')
  → compact()

auto-sync（未來 feature，P1 保留欄位不實作）

匯入前（解 M5 時序）
  → SyncSection.handleFileChange 讀檔
  → applyImport validate 通過（ImportError 會拋）
  → createPreOperationSnapshot(currentBundle, 'pre-import')   ← 順序修正：驗證後才建
  → applyImport 繼續 deserialize
  注意：spec 原本寫 'importBundle'，實際函式名是 'applyImport'（解 T5）

還原前
  → useSyncStore.restoreFromSnapshot(snapshot, source)
  → createPreOperationSnapshot(currentBundle, 'pre-restore')  ← B6: 若為 session 第一筆 pre-restore，mark isSessionPristine=true
  → clearPendingConflicts()
  → for each contributor in snapshot.bundle.collections:
       dispatch contributor.deserialize(collections[contributorId])
  → DONE（不動 lastSyncedBundle，B1）
```

### Session-start pristine pinning（B6）

- SPA 在 module init / useSyncStore 初始化時，檢查 SnapshotStore 是否已有 `isSessionPristine=true` 的 pre-op snapshot
- 若沒有：listLocal 拉最近一筆 snapshot，mark 為 session pristine（or 建一筆當前狀態 tag 為 pristine）
- 使用者不管重複 restore 幾次，永遠可以回到「啟動這個 session 時的狀態」

### 載入 Local history（B5 調整）

```
SnapshotHistoryPage mount
  → useLocalHistory() → SnapshotStore.listLocal()
  → 回傳 SnapshotMetadata[]（不含 bundle）
  → 渲染 HistoryList
使用者點一筆
  → SnapshotDetail useSnapshotDiff(snapshot)
  → 懶取完整 bundle: SnapshotStore.getLocal(snapshot.id)
  → 算 contributor-level diff（B4）
```

### 載入 Remote history（PR B）

```
SnapshotHistoryPage mount + activeProvider === daemon
  → useRemoteHistory() → provider.listHistory(200)
  → GET /api/sync/history?clientId=xxx&limit=200
  → 回傳 SyncSnapshot[]（metadata only，timestamp 為毫秒）
  → SPA 直接使用，不需單位換算
  → 渲染 HistoryList
使用者點一筆
  → SnapshotDetail 發現 bundle === null
  → daemonProvider.getSnapshotBundle(snapshot.id)  ← type-narrow 後呼叫
  → GET /api/sync/history/:id/bundle
  → cache 到 component-level Map<snapshotId, SyncBundle>
    - 切換 snapshot 沿用 cache
    - Page unmount 整個 Map 丟（自動 GC）
  → 算 diff + 顯示
```

### Restore 流程（append-only）

```
使用者在詳情頁按 Restore
  → 開 SnapshotRestoreDialog：
    "Restore this snapshot from <timestamp>?
     Your current state will be overwritten on this device.
     A pre-restore backup will be created automatically.
     [若 pendingConflicts > 0 追加：
      This will discard <N> pending conflicts.]"
  → [Cancel] [Restore]
按 Restore（setRestoring=true 防雙點，解 M4 idempotency）
  → restoreFromSnapshot(snapshot, source)
  → SyncSection status banner 顯示 "Restored from <device> · <relative time>"（解 D7：用 banner 取代 toast）
  → navigate back to /settings/sync
  → 下次 auto-sync / 手動 Sync Now 時，diff 會自然被 push，daemon 以 append-only 新增一筆 history
```

### Host token 保留（解 A6 / D3）

`hosts` contributor 的 `deserialize` 實作規則：

```typescript
// spa/src/lib/sync/contributors/hosts.ts（修改）
export const hostsContributor: SyncContributor = {
  id: 'hosts',
  serialize: () => {
    // 原本就排除 token
    const hosts = useHostStore.getState().hosts
    return Object.fromEntries(
      Object.entries(hosts).map(([id, h]) => [id, omit(h, ['token'])])
    )
  },
  deserialize: (incoming) => {
    // NEW: 保留當前 state 裡的 token
    const current = useHostStore.getState().hosts
    const merged = Object.fromEntries(
      Object.entries(incoming).map(([id, h]) => [
        id,
        { ...h, token: current[id]?.token ?? null }
      ])
    )
    useHostStore.setState({ hosts: merged }, true)
  },
}
```

- Token 是 secret，不進 sync bundle
- restore 時若 host id 在當前 state 有對應 token → 保留；若是新 id（snapshot 有但當前沒有）→ token=null，使用者需重新登入
- 其他 contributor 日後若有 secret 欄位，依此模式處理

## §4 Error handling

### IndexedDB 層

| 情境 | 行為 |
|------|------|
| IDB 不可用（privacy mode / 罕見邊界） | `init()` fail → error state；UI 顯示「Snapshot history unavailable」；manual/pre-op 不建，但 sync 正常 |
| Quota exceeded | createSnapshot catch → compact() → 若 `evicted.length === 0` 直接 fail fast（避免無限重試，解 C10）→ toast 警告；pre-op 失敗則**提供 bypass 選項**：SnapshotRestoreDialog 追加「Backup failed. Continue anyway?」，使用者明知風險可繼續（解 C10） |
| Bundle JSON 毀損 | 該 row 隔離；list 跳過；Detail 顯示 "Corrupted" + delete |

### Remote / Daemon 層

| 情境 | 行為 |
|------|------|
| `listHistory` 失敗 | Remote tab inline error + Retry；不影響 Local |
| `getSnapshotBundle` 404 | "This snapshot no longer exists" + auto refresh remote list |
| `getSnapshotBundle` 403（踢出 group，解 M11） | "You no longer have access to this group's history." + 隱藏 Remote tab |
| 其他 5xx | Error banner + Retry |
| Daemon compaction 失敗 | push 不阻斷；錯誤 log；下次 push 再試 |
| Lazy fetch race（A→B 快切，解 L9 / M11 變體） | useEffect cleanup 取消前一個 fetch；或以 AbortController 中止 |

### Restore 流程

| 情境 | 行為 |
|------|------|
| pre-restore snapshot 建立失敗 | 預設中止；Dialog 提供 "Continue anyway" override |
| Deserialize 某 contributor 拋 exception | try/catch 每個；失敗 log + warnings list；成功的 deserialize 保留；最後 status banner「Restored with warnings: <X>」 |
| 半 restore 之後 state corruption | 新一次 sync 時三方比對看到 local ≠ lastSyncedBundle，自然重試合併；不需 rollback（append-only 特性） |
| 頁面 reload 中斷 | restore 純 local，不依賴 network；最新 persist state 反映已完成部分 |
| 連按兩次 Restore | setRestoring=true + button disabled；store action 以 refIdempotencyKey 保護 |

### Compaction

| 情境 | 行為 |
|------|------|
| 並發 createSnapshot（SPA） | Promise queue 序列化；**單 process 假設**。Electron 多 WebContentsView 的 IDB 是否共用 origin 待測（原 Electron single-instance lock 下不構成問題）；若 P3 時發現跨 view race 再加 `navigator.locks` |
| 並發 push（daemon） | `BEGIN IMMEDIATE TRANSACTION` 包「SELECT→ compute → DELETE」（解 C9） |
| SPA compact 刪錯 | 靠單元測試保證；runtime 不加 rollback |
| Daemon tx 失敗 | rollback；下次 push 再試 |

### pendingConflicts 邊界（解 P0-1）

| 情境 | 行為 |
|------|------|
| 有 conflicts 時 restore | Dialog 警告「This will discard N pending conflicts」；確認後清 |
| Banner 半選後 restore | 同上；任何未 apply 的選擇一併清除；不做部分保留 |
| pendingRemoteBundle 已 trim（P0）vs restore 前的 currentBundle | `createPreOperationSnapshot('pre-restore')` 的參數必須是 **engine.serialize()** 得到的完整 currentBundle，不能用 trimmed pendingRemoteBundle（解 P0-4） |

### UI 錯誤分級

- **Transient**（network / retry-able）→ inline banner + Retry
- **Terminal**（corrupted / not supported）→ empty-state + 說明
- **Operation failure** → status banner（取代 toast，沿用 SyncSection 的 `StatusLine` pattern，解 D7）

## §5 Testing

### SPA · Vitest（PR A）

| 測試對象 | 覆蓋重點 |
|---------|---------|
| `SnapshotStore` | create / list / get / delete / clear；`fake-indexeddb`；contributorIds / bundleSize 計算；session-pristine pinning |
| `computeCompaction` tiered policy | `vi.useFakeTimers` + 時間分佈固定輸入 → 每 bucket 留最新；pre-op pool 與 time-tier 不互擾；邊界（空、單筆、只 pre-op、pristine 固定）；UTC bucket key（跨 DST + 跨年測試） |
| `equalExceptEnvelope` | 同 collections 不同 envelope → true；改一欄 → false；nested；ChunkedPayload manifest hash 比對 |
| `useSyncStore.restoreFromSnapshot` | mock SnapshotStore + contributors；驗證執行順序、**不動 lastSyncedBundle**、pre-restore 失敗 → 中止；partial deserialize failure → warnings；append-only: restore 後立刻 sync → 新 snapshot 出現 |
| `useSyncStore.createPreOperationSnapshot` | 兩種 trigger；validate 後才建（pre-import）；失敗向上拋 |
| `hosts.deserialize` token 保留 | restore 時原 host token 仍在；新 host id token=null |

### SPA · UI (Vitest + RTL, PR A)

| 測試對象 | 覆蓋重點 |
|---------|---------|
| `SnapshotHistoryPage` | 空狀態；有 local；deep link 進來預設 tab；subpage route |
| `HistoryList` | 排序；trigger tag；bucket tag；pristine 固定標記；選中高亮 |
| `SnapshotDetail` | metadata；contributor-level diff（identical / changed / missing）；Restore disabled 條件；error banner |
| `SnapshotRestoreDialog` | pending conflicts 警告；pre-restore fail override；spinner；double-click 防護 |
| `HistoryTabs` | Local 切換；Remote tab 在 PR A 階段 disabled 顯示「Available in PR B / Daemon」 |
| 路由 `/settings/sync/history` | deep link；back to `/settings/sync`；bad sub-path self-heal |

### SPA · Vitest（PR B 新增）

| 測試對象 | 覆蓋重點 |
|---------|---------|
| `DaemonProvider.getSnapshotBundle` | 成功 200；404 → 特定 error；403 → 特定 error；timestamp ms 直接使用 |
| `useRemoteHistory` | lazy fetch；Map cache；unmount 清空；race cancel（快速切 snapshot） |
| `HistoryTabs` Remote 行為 | daemon provider 下可切換；Manual 下 disabled「Not supported」 |

### Daemon · Go（PR B）

| 測試對象 | 覆蓋重點 |
|---------|---------|
| `compactor.go` | Table-driven policy tests（與 SPA parity JSON fixture）；BEGIN IMMEDIATE tx；空 DB；per group_id 分組；1000 筆性能 |
| `handleGetSnapshotBundle` | 正常；404；403（client 不在 group）；timestamp ms wire；id string serialize |
| push + compact 整合 | 成功 push 後 sync_history 符合 policy；多 client 同 group 不互砍對方 |
| migration `ALTER TABLE` | idempotent（重跑不失敗）；既有資料 trigger=default |

### 手動 integration

**PR A test plan**：
1. manual sync 連續數十次 → observe hourly/daily bucket 形成
2. snapshot → import 壞 bundle → 驗 pre-import snapshot → restore 回來 → state 正確
3. 有 pending conflicts 時 restore → warning 顯示 → conflicts 被清
4. 塞滿 IDB → createSnapshot fail gracefully + pre-op bypass 選項
5. Restore 不同機器 host 的 snapshot → token 保留（現有登入不消失）
6. Session 開 → 多次 restore → 回得去 session-start pristine
7. i18n 切 zh-TW / en 檢查新增 key
8. 清空 history（debug action）→ 重新同步 → 長回

**PR B test plan**：
9. Remote tab 在 daemon provider 下可顯示、lazy fetch 運作
10. 多 client 同 group 各自 push → daemon compactor 依 group_id scope 不互砍
11. 塞時間戳假資料到 daemon → 觀察 daily/weekly/monthly 壓縮
12. 拔網路 → Remote tab error + Retry
13. Provider 切換（daemon → manual → daemon）→ Remote tab 行為正確
14. Client 被踢出 group → Remote tab 顯示 403 「No longer have access」
15. Restore remote snapshot → 下一次 manual sync → 新 history 產生（append-only 驗證）

### 覆蓋率目標

- SPA 新 code：行覆蓋 > 90%（SnapshotStore / compaction 核心 100%）
- Go 新 code：函式覆蓋 > 85%

## §6 新增依賴

- **`idb`**（Jake Archibald，~4KB gzipped，MIT）— IndexedDB Promise wrapper，封在 `lib/storage/idb.ts`
- **`fake-indexeddb`**（dev-only）— Vitest IDB mock；在 `spa/src/test-setup.ts` top 加 `import 'fake-indexeddb/auto'`（解 D6）

## §7 i18n

估 ~35 新 key，全部放 `settings.sync.history.*`：

```
# 主頁
title
tabs.local
tabs.remote
tabs.remoteUnsupported
tabs.remoteDaemonOnly

# 空 / 錯誤
empty.local
empty.remote
error.loadList
error.loadBundle
error.notFound
error.noAccess          # 403
error.corrupted
notSupported

# Trigger tags
trigger.auto
trigger.manual
trigger.preImport
trigger.preRestore
trigger.sessionPristine

# Bucket tags
bucket.hourly
bucket.daily
bucket.weekly
bucket.monthly
bucket.preOp

# Detail
detail.metadata
detail.diff.identical
detail.diff.changed
detail.diff.missingInSnapshot
detail.diff.missingInCurrent
detail.restore

# Restore dialog
restore.confirmTitle
restore.confirmBody
restore.confirmPendingConflicts_one
restore.confirmPendingConflicts_other
restore.preOpFailed
restore.continueAnyway
restore.cancel
restore.proceed
restore.success
restore.warnings
```

Plural keys 採 `_one/_other` split（和 P0 pluralKey 機制一致）。

## §8 PR 拆分策略（B3）

### PR A — SPA-only 本地 snapshot + restore

**範圍**：
- `lib/storage/idb.ts`（新）+ `idb` dep
- `lib/sync/snapshot-store.ts`（新）
- `lib/sync/snapshot-compaction.ts`（新）
- `lib/sync/contributors/hosts.ts` 修改（token 保留）
- `three-way-merge.ts` export `deepEqual`
- `features/settings/sections/sync-history/` 新資料夾
- `useSyncStore` 新增 2 actions
- `route-utils.ts` + `SettingsPage.tsx` 雙層 route 擴充
- `SyncSection` 加 "View Sync History" 按鈕
- i18n 新 keys
- 對應測試
- 預估 ~20 files / +2000 行

**交付價值**：使用者可「匯錯救得回」 — P1 核心使用情境獨立可用。Remote tab 顯示「Available with daemon provider (coming soon)」。

**test plan**：上方列表 1-8

### PR B — Daemon endpoint + Remote tab + compactor

**範圍**：
- `internal/module/sync/compactor.go`（新）
- `internal/module/sync/handler.go` + `store.go` 改：trigger 欄位、LIMIT 100 移除、compact 呼叫、GET /:id/bundle、limit cap=200、timestamp ms wire
- `lib/sync/providers/daemon-provider.ts` 加 `getSnapshotBundle`
- SPA `useRemoteHistory` + `HistoryTabs` 啟用 Remote tab
- Schema migration（`ALTER TABLE`）
- 對應測試
- 預估 ~15 files / +1500 行

**test plan**：上方列表 9-15

### 相依

PR A 先 merge，PR B 依 PR A。PR B 可獨立 review，不阻塞使用者取得 PR A 的 core feature。

## §9 不做（YAGNI）

- Per-field diff viewer
- 背景定時 compaction
- 手動「Snapshot now」按鈕（manual sync 已蘊含）
- Snapshot 匯出成獨立檔（未來 P3/P4 再談）
- Snapshot 分類 / 標籤
- Cross-device restore 通知推播
- History retention UI 設定頁（policy 寫死）
- 通用 ConfirmDialog 元件（SnapshotRestoreDialog 為新、單用）

## §10 相依

- **前置**：P0 體質清理（PR #432）已 merge 至 alpha.164；截至本 spec 提交時 main 已至 alpha.187 — 滿足
- **後續**：
  - P3 File Provider 可沿用 `DaemonProvider.getSnapshotBundle` 的策略（加 File 專屬方法）
  - P5 content-addressed 會改 bundle 儲存形式；屆時需調整 snapshot 儲存策略（大 editor bundle 切 chunk，Map hash reference）

## §11 未決 / 待 follow-up（開 gh issue，不擋 P1）

- [FU-1] `idb` vs 手寫 Promise wrapper 的權衡（L5）
- [FU-2] `navigator.locks` 跨 tab 協調（若未來 Electron 多 view）
- [FU-3] Bundle size 估算在 editor payload 的實際表現（M15，P5 時重估）
- [FU-4] Daemon schema `timestamp` 欄位是否 migrate 成毫秒（現 wire 層轉換，未來整併）
- [FU-5] 通用 toast service 抽取（D7；目前用 status banner 繞過）
- [FU-6] i18n plural `changed_one/other`、`removed_one/other` 是否也需 zero 變體

## §12 文件位置

- 本 Spec：`docs/superpowers/specs/2026-04-19-sync-p1-history-design.md`
- 原架構 Spec：`docs/superpowers/specs/2026-04-16-sync-architecture-design.md`
- 原 Plan：`docs/superpowers/plans/2026-04-16-sync-architecture.md`
- 實作 Plan（下一步 writing-plans 產出）：
  - PR A：`docs/superpowers/plans/2026-04-19-sync-p1-history-pr-a.md`
  - PR B：`docs/superpowers/plans/2026-04-19-sync-p1-history-pr-b.md`

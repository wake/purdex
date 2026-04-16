# Purdex Sync Architecture Design

> 跨裝置匯出 / 匯入 / 同步 Purdex SPA / Electron App 資料

## 概述

Purdex 的使用者資料目前全部存在 SPA 層的 localStorage（Zustand persist），無法跨裝置共享。本設計提供三種同步通道（File / Daemon / Manual），讓使用者在不同裝置間同步工作環境。

### 設計原則

- **Export/Import 為核心**：所有同步都是「自動化的匯出匯入」，三種通道搬運同一份 SyncBundle
- **Module 自主**：SyncEngine 不 hardcode 任何資料來源，module 自行註冊 contributor
- **全 roaming**：不做 scope 分類，所有註冊的資料預設同步，敏感欄位由 module 自行排除
- **Eventual consistency**：不需要即時同步，snapshot-based 足夠
- **三方比對**：衝突偵測基於 local current vs last-synced vs remote 三方比對，不侵入現有 store 結構
- **未來可擴充**：Cloud Provider 介面已定義，加回來只需實作 adapter

## §1 核心架構

### SyncEngine

SPA 層的中央協調器，不擁有資料，只負責搬運與合併。

```
┌─────────────────────────────────────────────┐
│  SyncEngine                                 │
│  ┌──────────┐  ┌──────────────────────────┐ │
│  │ Registry │  │ SyncBundle (JSON)        │ │
│  │ ─────────│  │  ├─ meta (version, ts,   │ │
│  │ module A │  │  │        device)         │ │
│  │ module B │  │  ├─ full collections     │ │
│  │ module C │  │  └─ content-addressed    │ │
│  └──────────┘  │     chunks               │ │
│                └──────────────────────────┘ │
│  ┌──────────┐                               │
│  │SyncState │ ← lastSyncedBundle + meta     │
│  └──────────┘                               │
│  ┌──────────────────────────────────────┐   │
│  │ SyncProviders                       │   │
│  │  File  │  Daemon  │  Manual         │   │
│  └──────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
```

### SyncState

SyncEngine 自行維護的同步狀態，persist 在 localStorage key `purdex-sync-state`：

```typescript
interface SyncState {
  lastSyncedBundle: SyncBundle | null  // 上次同步成功的完整 snapshot
  lastSyncedAt: number | null          // Unix ms
  activeProviderId: string | null      // 'file' | 'daemon' | null
  enabledModules: string[]             // 使用者啟用的 contributor IDs
}
```

- **Per-provider**：切換 provider 時 `lastSyncedBundle` 重置為 null，觸發首次同步流程
- **首次同步**：`lastSyncedBundle === null` 時，整包視為 `full-replace`，不做衝突偵測

### 資料流

1. **Export**：SyncEngine 遍歷 registry（只含 `enabledModules`）→ 每個 contributor 回傳 payload → 組成 SyncBundle → 交給 active provider 送出
2. **Import**：Provider 收到 remote SyncBundle → SyncEngine 拆開 → 三方比對產生衝突清單 → 使用者解決衝突 → 分發給各 contributor 的 `deserialize()` 做合併
3. **Sync History**：每次成功同步後，SyncBundle snapshot 存入歷史列表
4. **更新 SyncState**：合併完成後，將合併結果存為新的 `lastSyncedBundle`

### 觸發時機

- Store 變更時 debounce auto-sync（如果有 active provider）
- 使用者手動觸發
- App 啟動 / 恢復前景時 pull

## §2 Module 註冊機制

### SyncContributor 介面

```typescript
interface SyncContributor {
  id: string
  strategy: 'full' | 'content-addressed'
  serialize(): FullPayload | ChunkedPayload
  deserialize(payload: unknown, merge: MergeStrategy): void
  getVersion(): number
  migrate?(payload: unknown, fromVersion: number): unknown
}
```

- 各 module 在初始化階段呼叫 `syncEngine.register(contributor)`
- SyncEngine 完全被動收集，不主動 import 任何 module
- 資料所有權在 module，過濾邏輯也在 module（例如 hosts 排除 auth token）
- Module 開關由 `SyncState.enabledModules` 控制，SyncEngine 在 serialize/deserialize 時跳過未啟用的 contributor

### MergeStrategy

`deserialize()` 的第二個參數，由 SyncEngine 根據衝突解決結果提供：

```typescript
type MergeStrategy =
  | { type: 'full-replace' }                          // 首次同步 / 還原 snapshot
  | { type: 'field-merge'; resolved: ResolvedFields }  // 正常同步，含已解決的衝突

interface ResolvedFields {
  [field: string]: 'local' | 'remote'   // 使用者對每個衝突欄位的選擇
}
```

### 版本處理

每個 contributor 透過 `getVersion()` 宣告當前 schema 版本。SyncBundle 的每個 collection 帶 `version` 欄位。

- **Remote version < local version**（舊 → 新）：SyncEngine 呼叫 contributor 的 `migrate(payload, fromVersion)` 將 remote payload 升級後再做比對/合併
- **Remote version > local version**（新 → 舊）：SyncEngine 跳過該 contributor 並警告使用者「此裝置的 App 版本較舊，部分資料無法同步」
- **版本相同**：正常合併
- `migrate()` 為 optional — 沒有實作時，版本不匹配一律跳過

### 同步策略

| 策略 | 適用場景 | 行為 |
|------|---------|------|
| `full` | 小資料（preferences、workspaces） | 每次搬整包 snapshot |
| `content-addressed` | 大資料（editor docs） | manifest（清單 + hash）+ chunks（只傳 remote 沒有的） |

### SyncBundle 結構

```json
{
  "version": 1,
  "timestamp": 1713264000000,
  "device": "MacBook-c_a1b2c3",
  "collections": {
    "workspaces": { "version": 1, "data": {} },
    "preferences": { "version": 1, "data": {} },
    "editor": {
      "version": 1,
      "manifest": [{ "id": "doc1", "hash": "a1b2c3" }],
      "chunks": { "a1b2c3": "..." }
    }
  }
}
```

### 預期的 Contributors

| Module | 策略 | 備註 |
|--------|------|------|
| workspaces | full | 結構 + module configs |
| hosts | full | serialize 時排除 auth token |
| preferences | full | theme、locale、UI settings 等 |
| quick-commands | full | 使用者自訂指令 |
| layout | full | sidebar regions、view visibility |
| i18n | full | 自訂翻譯 |
| notification-settings | full | per-agent 通知設定 |
| editor | content-addressed | 大量文字內容，用 manifest + chunks |

### 不參與同步

tabs、browse history、session cache、agent events

## §3 衝突解決

### 三方比對（Three-Way Merge）

不侵入現有 Zustand store 結構。衝突偵測基於三份資料的比對：

```
                    lastSyncedBundle
                   （上次同步的快照）
                    /              \
                   /                \
     local current                remote bundle
    （目前本地狀態）              （遠端傳來的）
```

**偵測邏輯**（per contributor, per field）：

```typescript
function detectConflict(field: string, last: unknown, local: unknown, remote: unknown): ConflictResult {
  const localChanged = !deepEqual(last, local)
  const remoteChanged = !deepEqual(last, remote)

  if (!localChanged && !remoteChanged) return 'no-change'
  if (localChanged && !remoteChanged)  return 'use-local'
  if (!localChanged && remoteChanged)  return 'use-remote'
  if (deepEqual(local, remote))        return 'both-same'   // 雙方改成一樣
  return 'conflict'                                          // 真正的衝突
}
```

- **不需要 `SyncField<T>` 包裝**，不改動任何現有 store 結構
- **不需要 per-field 時間戳**，只需要 `lastSyncedBundle` 作為比對基準
- 首次同步（`lastSyncedBundle === null`）：全部 `full-replace`，不做衝突偵測

### 合併流程

```
Remote bundle 進來
  → 逐 contributor 比對 local vs lastSynced vs remote
    → no-change / use-local / use-remote / both-same → 自動合併
    → conflict → 收集到衝突清單
  → 衝突清單為空 → 靜默完成
  → 衝突清單不為空 → 暫停，攔截詢問使用者
```

### ConflictItem

```typescript
interface ConflictItem {
  contributor: string        // 'preferences', 'workspaces', ...
  field: string              // 'theme', 'workspace.main.layout', ...
  lastSynced: unknown        // 上次同步時的值
  local: unknown             // 目前本地值
  remote: { value: unknown; device: string }  // 遠端值 + 來源裝置
}
```

### 使用者操作選項

| 選項 | 行為 |
|------|------|
| 保留本地 | 該欄位用 local value，推送回 remote |
| 採用遠端 | 該欄位用 remote value |
| 全部保留本地 | 所有衝突欄位一律 local |
| 全部採用遠端 | 所有衝突欄位一律 remote |

無衝突的欄位靜默合併，只有真正衝突的才提示。衝突數量為零時使用者完全無感。

### Sync History

每次成功同步後，完整 SyncBundle 作為 snapshot 存入歷史。SyncEngine 負責管理本地歷史，Provider 負責管理 remote 歷史。

```typescript
interface SyncSnapshot {
  id: string              // nanoid
  timestamp: number
  device: string          // 來源裝置（從 bundle.device 取得）
  source: 'local' | 'remote'
  trigger: 'auto' | 'manual'
  bundleRef: string       // snapshot 檔案 / DB row 的 reference
}
```

- `bundleRef` 指向實際 bundle 資料，避免 snapshot 清單本身過大
- 本地歷史：SyncEngine 存在 localStorage（只存 metadata）+ IndexedDB（存 bundle 資料）
- Remote 歷史：Provider 負責儲存
- 本地保留最近 30 份，remote 保留最近 100 份
- 使用者可在 Settings 瀏覽歷史（合併顯示本地 + remote，依時間排序）
- 還原 = 整包 `full-replace` deserialize，等同匯入操作

### 不做的事

- 不做 CRDT / operational transform — 資料量小、不即時，三方比對夠用
- 不做 diff viewer — 歷史還原只有「摘要 + 還原」

## §4 SyncProviders

三個 provider 實作同一介面：

```typescript
interface SyncProvider {
  id: string
  push(bundle: SyncBundle): Promise<void>
  pull(): Promise<SyncBundle | null>
  pushChunks(chunks: Record<string, Uint8Array>): Promise<void>
  pullChunks(hashes: string[]): Promise<Record<string, Uint8Array>>
  listHistory(limit: number): Promise<SyncSnapshot[]>
}
```

> Chunks 使用 `Uint8Array` 而非 `Blob`，確保 Browser / Electron (Node.js) 環境一致性。

### File Provider（iCloud Drive / Syncthing / 任何同步資料夾）

- 使用者在 Settings 指定資料夾路徑
- 結構：
  ```
  purdex-sync/
  ├── manifest.json        # 最新 bundle metadata
  ├── history/             # snapshot 檔案
  │   ├── 2026-04-16T...json
  │   └── ...
  └── chunks/              # content-addressed blobs
      ├── a1b2c3.bin
      └── ...
  ```
- Electron 用 `fs.watch` 監聯變化觸發 import
- 僅 Electron 可用，純 SPA 不支援此 provider
- `listHistory()` 回傳 `history/` 資料夾內的 snapshot 清單

### Daemon Provider

- API endpoints：
  - `POST /api/sync/push` — 上傳 bundle（帶 `clientId`）
  - `GET /api/sync/pull?clientId=xxx` — 下載 group canonical bundle（見下方說明）
  - `POST /api/sync/chunks` + `GET /api/sync/chunks?hashes=...`
  - `GET /api/sync/history?clientId=xxx`
- Daemon 側用現有 SQLite，新增 `sync_bundles` + `sync_chunks` table
- 多 client 隔離與 sync group 機制見 §5
- `listHistory()` 回傳 daemon 儲存的 remote 歷史

**Pull 語義 — Group Canonical Bundle**：

當 sync group 有 3+ 台裝置（A/B/C）時，C 執行 pull 不會收到 A 或 B 其中一台的 bundle，而是由 daemon 合併產生的 **group canonical bundle**：

1. Daemon 收到任何 client 的 push → 與目前的 canonical bundle 做 field-level merge（LWW by timestamp）
2. Client 執行 pull → daemon 回傳 canonical bundle
3. 衝突解決在 client 端進行（canonical bundle vs local）

這樣 pull 永遠只回傳一份 bundle，不會有「要合併多份 remote bundle」的問題。Daemon 端的合併是無衝突的（LWW），真正的衝突解決（需要使用者介入的）只發生在 client 端。

### Manual Provider

- Export：SyncEngine serialize → 下載 `.purdex-sync` 檔案（JSON，大資料時 zip）
- Import：使用者選擇檔案 → SyncEngine deserialize → 衝突解決流程
- 隨時可用，不受 active provider 限制
- `listHistory()` 回傳空陣列（Manual 無 remote 歷史概念）

### Provider 規則

- 同一時間只有一個 active provider（File 或 Daemon）
- Manual 隨時可用，獨立於 active provider
- 切換 provider 時 `SyncState.lastSyncedBundle` 重置，觸發首次同步（full-replace）
- 未來擴充 Cloud Provider 只需實作 adapter

## §5 Daemon Sync 拓撲

### 5.1 Multiple Daemons — Sync Host 指定

使用者在 Settings → Sync 明確指定一台 daemon 作為 sync host：

- 只有一台 daemon 存 sync 資料，不做 daemon 間同步
- 切換 sync host 時：先從舊 host pull 最新 → push 到新 host
- Sync host 離線時：本地正常運作，連回時自動 pull + 衝突解決

### 5.2 Multiple Clients — Sync Group 配對

#### Client ID

每個 client 有穩定的 `clientId`（首次啟動時產生）：

- **Electron**：persist 在 `app.getPath('userData')/purdex-client-id`（檔案，不受瀏覽器資料清除影響）
- **Web SPA**：persist 在 localStorage key `purdex-client-id`（清除瀏覽器資料會遺失，需重新配對）

#### 配對流程

類似 Signal device linking 的簡化版：

1. 已有裝置點 `+ Add Device` → daemon 產生配對碼 + 5 分鐘有效期
2. 新裝置輸入配對碼（或掃描 QR code）→ daemon 驗證後加入同 group
3. 配對碼格式：8 位英數字元（`[A-Z0-9]`，共 `36^8 ≈ 2.8 兆` 組合）
4. **Rate limiting**：同一配對碼最多嘗試 5 次，超過即失效
5. **IP 遮蔽**：QR code 中的 daemon host 經 base64 編碼，非明文暴露（降低肩窺風險，非加密）

#### Daemon 儲存結構

```
sync_groups table:
┌──────────┬───────────┬─────────┬────────────┐
│ group_id │ client_id │ device  │ last_seen  │
├──────────┼───────────┼─────────┼────────────┤
│ g_x1y2z3 │ c_a1b2c3  │ MacBook │ 1713264000 │
│ g_x1y2z3 │ c_d4e5f6  │ iPad    │ 1713263900 │
└──────────┴───────────┴─────────┴────────────┘

sync_canonical table:
┌──────────┬────────────┬───────────┐
│ group_id │ updated_at │ bundle    │
├──────────┼────────────┼───────────┤
│ g_x1y2z3 │ 1713264000 │ {...}     │
└──────────┴────────────┴───────────┘

sync_history table:
┌──────────┬───────────┬────────────┬───────────┬─────────┐
│ group_id │ client_id │ timestamp  │ bundle    │ device  │
└──────────┴───────────┴────────────┴───────────┴─────────┘
```

- Push 時帶 `clientId` → daemon 查出所屬 group → merge into canonical bundle → 存入 history
- Pull 時帶 `clientId` → daemon 回傳 canonical bundle
- 不同 group 的資料完全隔離

#### 孤立 Client 清理

- `sync_groups` 帶 `last_seen` 欄位，每次 push/pull 更新
- Settings UI 的 Sync Group 列表顯示各裝置最後活動時間
- 使用者可手動 Remove Device 移除不再使用的 client
- Daemon 不主動清理（避免誤刪離線裝置）

### 5.3 多人共用 Daemon

暫不處理。目前 `clientId` + sync group 已提供天然隔離，不同人的裝置在不同 group，資料不會互相覆蓋。未來加帳號系統後，clientId 可綁定 userId。

## §6 Settings UI

### 前提條件

- **Daemon Provider**：新裝置須能連到 sync host daemon（同一 Tailnet / 區域網路）
- **File Provider**：僅 Electron 環境支援
- **QR Code 配對**：掃碼裝置須能連到 daemon（同一網路環境）

### Sync 主頁面

```
┌─ Settings > Sync ────────────────────────────┐
│                                               │
│  Sync Provider                                │
│  ○ Off   ● Daemon   ○ File                   │
│                                               │
│  ── Daemon Sync ──────────────────────────── │
│  Sync Host:  [▼ mini-lab (connected) ]       │
│                                               │
│  Sync Group                                   │
│  ┌────────────────────────────────────┐      │
│  │  MacBook Pro    c_a1b2  ● online  │      │
│  │  iPad Air       c_d4e5  ○ 3h ago  │      │
│  └────────────────────────────────────┘      │
│  [ + Add Device ]   [ Remove Device ]        │
│                                               │
│  ── Sync Status ─────────────────────────── │
│  Last sync: 2 minutes ago (auto)             │
│  [ Sync Now ]                                 │
│                                               │
│  ── Modules ─────────────────────────────── │
│  ☑ Workspaces          ☑ Quick Commands      │
│  ☑ Hosts (excl. token) ☑ Layout              │
│  ☑ Preferences         ☑ i18n                │
│  ☑ Notification Settings                      │
│  ☑ Editor              (content-addressed)    │
│                                               │
│  ── History ─────────────────────────────── │
│  [ View Sync History ]                        │
│                                               │
│  ── Export / Import ─────────────────────── │
│  [ Export All ]   [ Import ]                  │
└───────────────────────────────────────────────┘
```

### Module 開關語義

- **關閉某 module**：該 contributor 不再參與 serialize/deserialize，本地資料保留不動，remote 已有的資料也保留
- **重新開啟**：下次同步時，因為 `lastSyncedBundle` 中該 contributor 的資料與本地不同（期間可能各自變化），會觸發正常的三方比對流程
- **Module 開關設定本身不同步** — `enabledModules` 是 `SyncState` 的一部分，存在本地，每台裝置獨立設定

### Add Device — QR Code + 配對碼

```
┌─ Add Device ───────────────────────────┐
│                                         │
│  ┌─────────────┐    或者手動輸入        │
│  │             │                        │
│  │   QR Code   │    配對碼              │
│  │             │    A3F8-2K9P           │
│  │             │                        │
│  └─────────────┘    4:32 後過期         │
│                     5 次錯誤後失效       │
│                                         │
└─────────────────────────────────────────┘
```

**QR Code 內容**：

```
https://desk.purdex.app/pair#<base64(host:port)>.<token>
```

> 使用 URL fragment（`#`）而非 query string（`?`），fragment 不會被送到 web server、不會出現在 referer header 或 server log。

**掃碼流程**：
- 手機 / 平板：系統相機掃碼 → 開啟 `desk.purdex.app` SPA → 自動解析 pair 參數 → 進入配對流程
- 電腦：手動輸入 8 位配對碼

**前提**：掃碼裝置須能連到 daemon（同一 Tailnet / 區域網路）。若無法連到，SPA 顯示提示訊息引導使用者確認網路環境。

**Fallback**：沒有相機或不方便掃碼時，手動輸入配對碼，效果相同。兩種方式背後走同一個 daemon pairing API。

### Provider 切換

- 切到 File：出現資料夾路徑選擇器
- 切到 Daemon：出現 sync host 選單 + group 管理
- 切到 Off：提示「本地資料保留，停止同步」

### 衝突提示

非 modal，在 Sync section 頂部顯示 banner：

```
⚠ 3 個欄位有衝突  [ 查看詳情 ]
```

點進去逐項列出 local vs remote（含 lastSynced 值作為參考），逐項選擇或一鍵全選。

### View Sync History

1. 開啟 snapshot 列表（合併本地 + remote 歷史，依時間排序，標示來源）
2. 點選任一筆 → 預覽差異摘要
3. 確認後 → 整包 full-replace 還原

### 不做的 UI

- 不做即時同步狀態指示器
- 不做 per-field 的同步開關（粒度是 module 級）
- 不做 diff viewer（歷史還原只有「摘要 + 還原」）

## §7 Content-Addressed Chunks 生命週期

### GC 策略

Content-addressed chunks（用於 editor 等大資料 module）只寫入不自動刪除，需要定期清理：

1. 每次成功同步後，SyncEngine 掃描所有存活 snapshot（本地 30 + remote 100）的 manifest
2. 收集所有被引用的 chunk hashes → 形成 live set
3. 刪除不在 live set 中的 orphan chunks

**執行位置**：
- File Provider：SyncEngine 在 Electron 端直接清理 `chunks/` 資料夾
- Daemon Provider：daemon 提供 `POST /api/sync/gc` endpoint，由 client 觸發或 daemon 定期自行執行

## 網域規劃

| 用途 | 網域 |
|------|------|
| 首頁 / Landing page | `purdex.app` |
| SPA（Web 版） | `desk.purdex.app` |

## 主流參考架構

本設計參考以下產品的同步機制：

| 產品 | 借鑒的部分 |
|------|-----------|
| **Syncthing** | 無帳號、Device ID 交換、P2P 同步 |
| **Signal** | 一次性配對碼 + 主裝置 relay 的 device linking UX |
| **VS Code Settings Sync** | 分類同步（machine-scoped vs user-scoped）的概念 → 簡化為全 roaming + module 自行排除 |
| **Chrome Sync** | Sync types 獨立開關 → module-level checkbox |
| **Git** | content-addressed storage（manifest + chunks）用於大資料同步；三方比對（three-way merge）用於衝突偵測 |

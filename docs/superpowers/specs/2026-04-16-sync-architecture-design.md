# Purdex Sync Architecture Design

> 跨裝置匯出 / 匯入 / 同步 Purdex SPA / Electron App 資料

## 概述

Purdex 的使用者資料目前全部存在 SPA 層的 localStorage（Zustand persist），無法跨裝置共享。本設計提供三種同步通道（File / Daemon / Manual），讓使用者在不同裝置間同步工作環境。

### 設計原則

- **Export/Import 為核心**：所有同步都是「自動化的匯出匯入」，三種通道搬運同一份 SyncBundle
- **Module 自主**：SyncEngine 不 hardcode 任何資料來源，module 自行註冊 contributor
- **全 roaming**：不做 scope 分類，所有註冊的資料預設同步，敏感欄位由 module 自行排除
- **Eventual consistency**：不需要即時同步，snapshot-based 足夠
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
│  │ module A │  │  │        device, scope)  │ │
│  │ module B │  │  ├─ full collections     │ │
│  │ module C │  │  └─ content-addressed    │ │
│  └──────────┘  │     chunks               │ │
│                └──────────────────────────┘ │
│  ┌──────────────────────────────────────┐   │
│  │ SyncProviders                       │   │
│  │  File  │  Daemon  │  Manual         │   │
│  └──────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
```

### 資料流

1. **Export**：SyncEngine 遍歷 registry → 每個 contributor 回傳 payload → 組成 SyncBundle → 交給 active provider 送出
2. **Import**：Provider 收到 remote SyncBundle → SyncEngine 拆開 → 分發給各 contributor 的 `deserialize()` 做合併
3. **Sync History**：每次成功同步後，SyncBundle snapshot 存入歷史列表

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
  getVersion(): number       // schema 升級用
}
```

- 各 module 在初始化階段呼叫 `syncEngine.register(contributor)`
- SyncEngine 完全被動收集，不主動 import 任何 module
- 資料所有權在 module，過濾邏輯也在 module（例如 hosts 排除 auth token）

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

### 偵測 → 攔截 → 使用者決定

合併流程：

```
Remote bundle 進來
  → 逐 contributor 比對 local vs remote
    → 無衝突（只有一邊改過）→ 自動合併
    → 有衝突（雙方都改過同一欄位）→ 暫停，攔截詢問
```

### 衝突判斷

每個欄位帶 `updatedAt` 時間戳。雙方都比上次同步時間新 = 衝突。

```typescript
interface SyncField<T> {
  value: T
  updatedAt: number   // Unix ms
}

interface ConflictItem {
  contributor: string        // 'preferences', 'workspaces', ...
  field: string              // 'theme', 'workspace.main.layout', ...
  local: { value: unknown; updatedAt: number }
  remote: { value: unknown; updatedAt: number; device: string }
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

```typescript
interface SyncSnapshot {
  id: string              // nanoid
  timestamp: number
  device: string
  bundle: SyncBundle
  trigger: 'auto' | 'manual'
}
```

- 本地保留最近 30 份
- Remote（daemon / file）保留最近 100 份
- 使用者可在 Settings 瀏覽歷史，選擇任一 snapshot 還原
- 還原 = 整包 deserialize，等同匯入操作

### 不做的事

- 不做 CRDT / operational transform — 資料量小、不即時，field-level 比對夠用
- 不做 diff viewer — 歷史還原只有「摘要 + 還原」

## §4 SyncProviders

三個 provider 實作同一介面：

```typescript
interface SyncProvider {
  id: string
  push(bundle: SyncBundle): Promise<void>
  pull(): Promise<SyncBundle | null>
  pushChunks(chunks: Record<string, Blob>): Promise<void>
  pullChunks(hashes: string[]): Promise<Record<string, Blob>>
  listHistory(limit: number): Promise<SyncSnapshot[]>
}
```

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
- Electron 用 `fs.watch` 監聽變化觸發 import
- 僅 Electron 可用，純 SPA 不支援此 provider

### Daemon Provider

- API endpoints：
  - `POST /api/sync/push` — 上傳 bundle
  - `GET /api/sync/pull` — 下載最新 bundle
  - `POST /api/sync/chunks` + `GET /api/sync/chunks?hashes=...`
  - `GET /api/sync/history`
- Daemon 側用現有 SQLite，新增 `sync_bundles` + `sync_chunks` table
- 多 client 隔離與 sync group 機制見 §5

### Manual Provider

- Export：SyncEngine serialize → 下載 `.purdex-sync` 檔案（JSON，大資料時 zip）
- Import：使用者選擇檔案 → SyncEngine deserialize → 衝突解決流程
- 隨時可用，不受 active provider 限制

### Provider 規則

- 同一時間只有一個 active provider（File 或 Daemon）
- Manual 隨時可用，獨立於 active provider
- 切換 provider 時不遷移資料，只改變同步目標
- 未來擴充 Cloud Provider 只需實作 adapter

## §5 Daemon Sync 拓撲

### 5.1 Multiple Daemons — Sync Host 指定

使用者在 Settings → Sync 明確指定一台 daemon 作為 sync host：

- 只有一台 daemon 存 sync 資料，不做 daemon 間同步
- 切換 sync host 時：先從舊 host pull 最新 → push 到新 host
- Sync host 離線時：本地正常運作，連回時自動 pull + 衝突解決

### 5.2 Multiple Clients — Sync Group 配對

每個 client 有穩定的 `clientId`（首次啟動時產生，persist 在 localStorage）。要互相同步的 clients 透過配對碼加入同一個 sync group。

**配對流程**（類似 Signal device linking 的簡化版）：

1. 已有裝置點 `+ Add Device` → daemon 產生 6 位配對碼 + 5 分鐘有效期
2. 新裝置輸入配對碼（或掃描 QR code）→ daemon 驗證後加入同 group

**Daemon 儲存結構**：

```
sync_groups table:
┌──────────┬───────────┬─────────┐
│ group_id │ client_id │ device  │
├──────────┼───────────┼─────────┤
│ g_x1y2z3 │ c_a1b2c3  │ MacBook │
│ g_x1y2z3 │ c_d4e5f6  │ iPad    │
└──────────┴───────────┴─────────┘

sync_bundles table:
┌───────────┬────────────┬───────────┬─────────┐
│ client_id │ timestamp  │ bundle    │ device  │
├───────────┼────────────┼───────────┼─────────┤
│ c_a1b2c3  │ 1713264000 │ {...}     │ MacBook │
│ c_d4e5f6  │ 1713263900 │ {...}     │ iPad    │
└───────────┴────────────┴───────────┴─────────┘
```

- Push 時帶 `clientId`，daemon 按 client 分開存
- Pull 時帶 `clientId`，daemon 回傳同 group 內其他 client 的最新 bundle
- 不同 group 的資料完全隔離

### 5.3 多人共用 Daemon

暫不處理。目前 `clientId` + sync group 已提供天然隔離，不同人的裝置在不同 group，資料不會互相覆蓋。未來加帳號系統後，clientId 可綁定 userId。

## §6 Settings UI

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
│  │  iPad Air       c_d4e5  ○ offline │      │
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

### Add Device — QR Code + 配對碼

```
┌─ Add Device ───────────────────────────┐
│                                         │
│  ┌─────────────┐    或者手動輸入        │
│  │             │                        │
│  │   QR Code   │    配對碼              │
│  │             │    A3F-82K             │
│  │             │                        │
│  └─────────────┘    4:32 後過期         │
│                                         │
└─────────────────────────────────────────┘
```

**QR Code 內容**：

```
https://desk.purdex.app/pair?host=100.64.0.2:7860&token=a3f82k
```

**掃碼流程**：
- 手機 / 平板：系統相機掃碼 → 開啟 `desk.purdex.app` SPA → 自動解析 pair 參數 → 進入配對流程
- 電腦：手動輸入 6 位配對碼

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

點進去逐項列出 local vs remote，逐項選擇或一鍵全選。

### View Sync History

1. 開啟 snapshot 列表（時間、裝置來源、觸發方式）
2. 點選任一筆 → 預覽差異摘要
3. 確認後 → 整包還原

### 不做的 UI

- 不做即時同步狀態指示器
- 不做 per-field 的同步開關（粒度是 module 級）
- 不做 diff viewer（歷史還原只有「摘要 + 還原」）

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
| **Git** | content-addressed storage（manifest + chunks）用於大資料同步 |

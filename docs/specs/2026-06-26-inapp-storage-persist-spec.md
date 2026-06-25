# Spec — InAppBackend 持久化（修 #856 In-App Storage data-loss）

> Date: 2026-06-26
> Status: Draft（待 codex round-1 審閱）
> Repo: purdex / branch: `worktree-inapp-persist`
> Issue: #856

## 1. Context

新建文件（New File）預設 `source = { type: 'inapp' }`（`EditorNewTabSection.tsx:18,55`），對應 `InAppBackend`（`fs-backend-inapp.ts`）。其儲存是 `private store = new Map()` —— **純記憶體、零持久化**。使用者新建文件、按儲存、命名（如 `twkc.txt` → 路徑 `/buffer/twkc.txt`），`write` 對記憶體成功、UI 顯示已存，但 **app upgrade / 重啟 → 記憶體隨進程消滅 → 內容永久遺失**。`useTabStore` 有 persist（記住 filePath），重開時 `read` 在空 Map 找不到 → 開空 buffer → 顯示空白 + dirty dot。

這是 data-loss trap：`InAppBackend` 掛 "In-App Storage" 名稱、提供完整 fs UX，底層卻 ephemeral。

**使用者已確認 scope = 止血（本地持久）**：把 In-App Storage 持久化即可，**不**做跨機同步、不做衝突 diff。

## 2. Goals / Non-Goals

### Goals
- **G1**：`InAppBackend` 的儲存從記憶體 `Map` 改為 **IndexedDB**（復用既有 `spa/src/lib/storage/idb.ts` 的 `openIDB`）。新建文件 save 後，**app 重啟仍能讀回**（`new InAppBackend()` 對同一 IDB 讀得到）。
- **G2**：7 個 `FsBackend` method（`read` / `write` / `stat` / `list` / `mkdir` / `delete` / `rename`）行為契約**完全不變**，只換底層儲存。呼叫端（`EditorPane` / `EditorBuffersPane` / file tree）**零改動**。
- **G3**：測試以既有 `fake-indexeddb`（已在 `test-setup.ts`）覆蓋 persist 行為——尤其「write → 重建 backend → read 仍在」。

### Non-Goals
- **N1**：不做跨機同步、不雙寫 daemon、不做衝突偵測 / diff（使用者明確 scope 外）。
- **N2**：不碰 `DaemonBackend` / `LocalBackend`。
- **N3**：不遷移既有記憶體 inapp 資料 —— 它本來就隨重啟消失、無可遷移（alpha 階段慣例 [[feedback_no_alpha_migration]]）。改版後新檔即 persist；舊 tab 指向的已遺失檔重開仍空白（無法救）。
- **N4**：不改 `untitledStoragePath`（`/buffer/<name>`）、不改新建文件預設 source。

## 3. Invariants

- **I1**：`InAppBackend` 仍 `implements FsBackend`，method 簽章與回傳型別不變（皆 async）。
- **I2**：`read` 找不到路徑仍 `throw`（`InAppBackend: file not found: <path>`）；`stat` / `rename` 找不到仍 throw（保留既有契約，呼叫端如 `EditorPane.tsx:150` 依賴 catch → 開空 buffer）。
- **I3**：`write` 仍 auto-create parent directories；`delete` 仍遞迴刪 prefix 子項；`list` 仍只回 direct children（無額外 slash）、dir-first + name 排序。
- **I4**：IDB 為 **per-origin** —— **任何 origin 變動（protocol / host / port 任一不同）都會切到另一份獨立的 In-App Storage**。包含 Electron bundled（`app://`）vs dev server，以及 dev 時 `localhost` / LAN IP / port 不同的情況。此為「本地持久」語義的正常結果（known limitation，記入 §7）。
- **I5**：`write`（含 auto-create dirs + 寫檔）在**單一 readwrite transaction** 內完成，確保原子性，避免半寫狀態。
- **I6（保留既有寬鬆語意，不向真 FS 靠攏）**：本案只換儲存層，既有的寬鬆行為**一律保留、不得改成像真實 FS 的 throw**：`rename(from, to)` 對已存在的 `to` 為 **blind overwrite**（不 throw）；`mkdir(path)` 對已存在 path 直接覆寫為 dir entry（不 throw）；`write` 只確保每層 parent path 有 entry，**不驗證 parent 是否真為 directory**。（呼叫端如 `EditorBuffersPane.tsx:140` 已對 blind overwrite 自行補償。）
- **I7（rename non-recursive）**：`rename(from, to)` 只搬 `from` 單一 entry，**不遞迴搬子項**（與既有 Map 版一致）。inapp UI 為 flat `/buffer/*`、無資料夾 rename 入口；directory rename 的子項語意維持 non-recursive，本案不擴張。

## 4. 實作要點

### 4.1 IDB schema
- DB name：`pdx-inapp-fs`，version `1`。
- objectStore：`files`，`keyPath: 'path'`。value = `StoredFile { path: string; content: Uint8Array; isDirectory: boolean; mtime: number }`（Uint8Array 由 structured clone 原生支援）。
- 連線：lazy `private dbPromise()` → `openIDB('pdx-inapp-fs', 1, upgrade)`；upgrade 內 `createObjectStore('files', { keyPath: 'path' })`。openIDB cache 確保同 origin 單連線、blocking/terminated 自癒。

### 4.2 method → IDB 對應（行為等價於現有 Map 版）
- `read(path)`：`db.get('files', path)` → 無 throw / dir throw / 回 `entry.content`。
- `write(path, content)`：一個 `readwrite` transaction：對每層 parent dir `if (!exists) put(dir entry)`，再 `put(file entry)`（`mtime = Date.now()`）。
- `stat(path)`：`db.get` → 無 throw / 回 `{ size: content.byteLength, mtime, isDirectory, isFile }`。
- `list(path)`：`db.getAll('files')` → 沿用既有 prefix + direct-children filter + dir-first/name 排序。（inapp 檔量小，getAll 可接受；不做 key-range 優化。）
- `mkdir(path)`：`put(dir entry)`。
- `delete(path)`：`readwrite` transaction：`delete(path)` + 以 cursor / getAllKeys 找 `prefix` 子項逐一 `delete`。
- `rename(from, to)`：`readwrite` transaction：`get(from)` → 無 throw；`put({ ...entry, path: to })` + `delete(from)`。（沿用既有：僅搬單一 entry，不遞迴搬子項——與現行行為一致，不擴張。）

> `Date.now()` 在 method 內呼叫（非 render path），無 react-hooks/purity 顧慮。

### 4.3 移除
- 刪 `private store = new Map()`，所有 method 改走 dbPromise。

## 5. Acceptance Criteria（= 測試契約，`fs-backend-inapp.test.ts`）

> **測試隔離（codex review #1）**：每個 case 開始前 `await closeAllIDB()` + `await indexedDB.deleteDatabase('pdx-inapp-fs')`（await 刪除完成）確保乾淨——fake-indexeddb 為記憶體實作、跨 case 持久。
>
> **persist 驗證的關鍵**：`openIDB` 按 `(name, version)` 共用 cache 連線，所以**單純 `new InAppBackend()` 只會拿到同一個 cached connection，驗到的是 singleton reuse 而非真 persist**。persist 類測試（AC2 / AC11）必須在建立第二個 backend 前 **先 `await closeAllIDB()`** 關閉 cache 連線（模擬 app 進程結束），讓新實例重新 `openIDB` 開啟落地的 DB，才真正驗證資料持久化。

- **AC1**：`write('/buffer/a.txt', X)` → `read` 回 X。
- **AC2（persist 核心）**：`write('/buffer/a.txt', X)` → **`await closeAllIDB()`**（關閉 cache 連線，模擬重啟）→ **建立全新 `InAppBackend` 實例**（觸發重新 `openIDB` 開啟同一落地 DB）→ `read('/buffer/a.txt')` 仍回 X。
- **AC3**：`write('/buffer/sub/a.txt', X)` → `stat('/buffer/sub')` isDirectory=true（auto-create parent）。
- **AC4**：`stat` 回正確 `size` / `mtime` / `isDirectory` / `isFile`。
- **AC5**：`list` 回 direct children，dir-first + name 排序；不含孫層。
- **AC6**：`mkdir('/buffer/d')` → `stat` isDirectory=true。
- **AC7**：`delete(file)` → `read` throw。
- **AC8**：`delete(dir)` → 遞迴刪：子檔 `read` throw。
- **AC9**：`rename(from, to)` → `read(to)` 回內容、`read(from)` throw。
- **AC10**：`read` / `stat` / `rename` 對不存在路徑 throw（契約保留）。
- **AC11（persist 全面）**：write 多檔 + mkdir → **`await closeAllIDB()`** → 重建 backend → `list` / `stat` / `rename` / `delete` 對 persisted 資料皆正確生效。
- **AC12（保留 overwrite 語意，對應 I6）**：`rename` 到已存在 target → 覆寫且**不 throw**；`mkdir` 已存在 path → **不 throw**；`write` 到 parent 為既有檔（非 dir）的路徑時不額外 throw（沿用既有寬鬆行為，不向真 FS 靠攏）。
- **AC13（rename non-recursive，對應 I7）**：`write('/buffer/d/a.txt')` → `rename('/buffer/d', '/buffer/e')` → 只搬 `/buffer/d` entry 本身；子項 `/buffer/d/a.txt` 仍在原 path（驗證 non-recursive，與既有一致）。

## 6. Commit 切分（單一 PR）

1. `feat(editor): persist In-App Storage to IndexedDB (fixes #856)` — `InAppBackend` 改 IDB-backed（G1/G2/I*）+ `fs-backend-inapp.test.ts` 全 AC（G3）。TDD：先寫 AC2 persist 紅 → 實作 → 綠。

> 單一邏輯改動（換儲存後端），不拆多 commit；測試與實作同 commit。

## 7. Out of Scope / Known Limitations

- 跨機同步 / daemon 雙寫 / 衝突 diff（§N1）—— 若日後要，屬 sync roadmap P5 content-addressed 範疇。
- **IDB per-origin（I4）**：**任何 origin 變動（protocol / host / port）都產生獨立的 In-App Storage** —— dev server vs bundled `.app`、以及 dev 時 `localhost` / LAN IP / port 不同皆然；切換時看不到對方的 inapp 檔。屬本地持久正常語義，不在本次處理。
- 既有記憶體 inapp 檔已遺失，不遷移（§N3）。

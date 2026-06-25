# Plan — InAppBackend 持久化（#856）

> Date: 2026-06-26
> Spec: `docs/specs/2026-06-26-inapp-storage-persist-spec.md`（過 codex round-1，4 finding 全修）
> Branch: `worktree-inapp-persist`
> 前提：單一邏輯改動（`InAppBackend` 的儲存層 Map → IndexedDB），呼叫端零改動。

## 0. 現況
- `InAppBackend`（`spa/src/lib/fs-backend-inapp.ts`）7 method 全對 `private store = new Map()` 操作，純記憶體、重啟即失（#856）。
- 既有基礎設施：`openIDB`（`spa/src/lib/storage/idb.ts`，`idb` 套件 `openDB` + connection cache + `closeAllIDB`）、`snapshot-store.ts` 為使用範例、`fake-indexeddb` 已在 `test-setup.ts`。
- 既有 `fs-backend-inapp.test.ts`？— 若無則新建；本 plan 以新建/補齊計。

## 1. Task（單一，TDD）

### T1 — InAppBackend 改 IDB-backed
對應 **G1 / G2 / I1-I7 / AC1-13**。

**IDB schema**
- DB `pdx-inapp-fs` v1；objectStore `files`，`keyPath: 'path'`；value `StoredFile { path, content: Uint8Array, isDirectory, mtime }`。
- lazy `private db(): Promise<IDBPDatabase>` → `openIDB('pdx-inapp-fs', 1, (db) => db.createObjectStore('files', { keyPath: 'path' }))`。

**method 改寫（行為等價於現有 Map 版，對照 spec §4.2，參考 `snapshot-store.ts` 的 `idb` 用法）**
- `read`：`(await db).get('files', path)` → 無 throw / dir throw / 回 content。
- `write`：`db.transaction('files', 'readwrite')` 單一 tx：逐層 parent `if (!await tx.store.get(dir)) tx.store.put(dirEntry)`，再 `tx.store.put(fileEntry)`，`await tx.done`（I5 原子性）。**不驗 parent 是否 dir（I6）。**
- `stat`：`get` → 無 throw / 回 FileStat。
- `list`：`getAll('files')` → 既有 prefix + direct-children filter + dir-first/name 排序。
- `mkdir`：`put(dirEntry)`（已存在 blind overwrite，I6）。
- `delete`：`readwrite` tx：`delete(path)` + 以 cursor（`tx.store.openCursor()`）或 `getAllKeys` 找 `prefix='path/'` 子項逐一 delete。
- `rename`：`readwrite` tx：`get(from)` → 無 throw；`put({ ...entry, path: to })`（target blind overwrite，I6）+ `delete(from)`。**只搬單一 entry，不遞迴（I7）。**

**移除** `private store = new Map()`。

**TDD 順序**
1. 新建/補 `fs-backend-inapp.test.ts`，先寫 **AC2 persist 紅**（write → `closeAllIDB()` → new instance → read）——確認現行 Map 版無法通過（或新 IDB 未實作前紅）。
2. 實作 IDB-backed → AC1-13 全綠。
3. 測試隔離：`beforeEach`/`afterEach` `await closeAllIDB()` + `await indexedDB.deleteDatabase('pdx-inapp-fs')`。

## 2. 整合驗證
```
cd spa
npx vitest run src/lib/fs-backend-inapp.test.ts \
  src/components/editor/__tests__/EditorPane.test.tsx \
  src/components/editor/EditorBuffersPane.test.tsx
pnpm run lint
pnpm run build
```
- 通過標準：`fs-backend-inapp.test.ts` 13 AC 全綠 + **caller regression**（EditorPane / EditorBuffersPane 用 inapp backend，尤其 EditorBuffersPane blind-overwrite 補償 `:140`）不回歸 / lint clean / build 通過。
- 完整套件既有 pre-existing failures（origin/main alpha.295 同樣 fail）不在範圍。

## 3. Commit
1. `feat(editor): persist In-App Storage to IndexedDB (fixes #856)` — 實作 + 測試 + spec/plan docs。單一邏輯改動，不拆。

## 4. 風險
| 風險 | 等級 | 緩解 |
|------|------|------|
| AC2 假驗 persist（cache 連線） | 高→已解 | spec/測試強制 `closeAllIDB()` 後重開（codex review #1） |
| 實作者把 blind overwrite / 不驗 parent「修正」成 throw | 中 | I6 + AC12 釘死契約 |
| `idb` 套件 transaction API 用錯（多 put 單 tx / cursor 刪 prefix） | 中 | 對照 `snapshot-store.ts` 既有用法；AC1/3/8 覆蓋 |
| fake-indexeddb 與真 IDB 行為差異 | 低 | 既有 sync 測試已大量用 fake-indexeddb，先例充分 |
| caller 用 inapp 的 regression | 中 | §2 納入 EditorPane/EditorBuffersPane targeted 測試 |

## 5. 開發方式
- 依 `feedback_subagent_tdd_priority` 派 subagent 跑 TDD（寫 13 AC + 實作 + 驗證）；主 session 整合驗證 + 切 commit + PR。

## 6. PR 後流程
- PR → codex 兩輪 review（R1 標準 + R2 三平行）→ 彙整表 → 修 → merge → bump → 清理對齊。

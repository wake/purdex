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

**TDD 順序（plan-review #1 / #4）**
1. 改造既有 `fs-backend-inapp.test.ts`（已有 8 基本測試，**無 persist、無 IDB 隔離**）。測試隔離 helper：`beforeEach` `await closeAllIDB()` + `await deleteInappDB()`，其中 **`deleteInappDB` 是 spec §5 的 Promise-wrapped `deleteDatabase`（等 `onsuccess`、`onblocked` 當失敗）—— 絕不可直接 `await indexedDB.deleteDatabase(...)`**（它回 `IDBOpenDBRequest` 非 Promise，await 不會等刪除完成）。
2. **先寫紅燈（把本輪 review 釘死的風險一起打紅）**：AC2（persist）+ AC12（overwrite 語意）+ AC13（rename non-recursive）+ 至少一個 **persisted delete & rename** case（write → `closeAllIDB()` → 重建 → delete/rename 仍正確），加 AC14（binary/empty persist）。確認現行 Map 版/未實作前皆紅。
3. 實作 IDB-backed → **AC1-14 全綠**。

## 2. 整合驗證
```
cd spa
npx vitest run src/lib/fs-backend-inapp.test.ts \
  src/components/editor/__tests__/EditorPane.test.tsx \
  src/components/editor/EditorBuffersPane.test.tsx
pnpm run lint
pnpm run build
```
- 通過標準：`fs-backend-inapp.test.ts` **14 AC 全綠** / lint clean / build 通過。
- **caller regression（plan-review #2）**：跑 `EditorPane.test.tsx` + `EditorBuffersPane.test.tsx` 確認不回歸——但**這兩份測試把 backend mock 掉了**（`EditorPane.test.tsx:13` / `EditorBuffersPane.test.tsx:28`），只驗 caller 對 `FsBackend` 契約的**用法**，**驗不到真 IDB persist / blind-overwrite 整合**。為補此缺，在 `fs-backend-inapp.test.ts` 內加一個**薄 integration case**：用真 `InAppBackend` 經 `getFsBackend({ type: 'inapp' })`（registry 取用）走 `write → closeAllIDB() → 重建 read` 端到端，驗證 registry 路徑下 persist 正常（不另開重元件測試）。
- 完整套件既有 pre-existing failures（origin/main alpha.295 同樣 fail）不在範圍。

## 3. Commit
1. `feat(editor): persist In-App Storage to IndexedDB (fixes #856)` — 實作 + 測試 + spec/plan docs。單一邏輯改動，不拆。

## 4. 風險
| 風險 | 等級 | 緩解 |
|------|------|------|
| AC2 假驗 persist（cache 連線） | 高→已解 | spec/測試強制 `closeAllIDB()` 後重開（spec-review #1） |
| `deleteDatabase` 誤當 Promise → 隔離失真、case 競態 | 高→已解 | spec §5 Promise-wrapped `deleteInappDB` + `onblocked` 當失敗（plan-review #1） |
| 實作者把 blind overwrite / 不驗 parent「修正」成 throw | 中 | I6 + AC12 釘死契約，TDD 先紅（plan-review #4） |
| `idb` 套件 transaction API 用錯（多 put 單 tx / cursor 刪 prefix） | 中 | 對照 `snapshot-store.ts` 既有用法；AC1/3/8 覆蓋 |
| binary / 空 `Uint8Array` 經 IDB structured-clone 失真 | 低 | AC14 覆蓋空 + 非文字 bytes 的 persist round-trip（plan-review #3） |
| fake-indexeddb 與真 IDB 行為差異 | 低 | 既有 sync 測試已大量用 fake-indexeddb，先例充分 |
| caller mock 掉 backend → 驗不到真整合 | 中 | §2 補薄 integration case（真 InAppBackend 經 registry round-trip，plan-review #2） |

## 5. 開發方式
- 依 `feedback_subagent_tdd_priority` 派 subagent 跑 TDD（寫 13 AC + 實作 + 驗證）；主 session 整合驗證 + 切 commit + PR。

## 6. PR 後流程
- PR → codex 兩輪 review（R1 標準 + R2 三平行）→ 彙整表 → 修 → merge → bump → 清理對齊。

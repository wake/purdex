# Editor IndexedDB File Tree Design

**Date**: 2026-04-20
**Status**: Design approved, ready for plan
**Scope**: Editor module in-app file tree, IndexedDB persistence, breadcrumb rename UX, deleted/orphaned document flow

## 1. 目標

讓 `Editor module` 擁有可持久化的內建檔案系統，作為沒有外部檔案來源時的基礎功能，並補齊以下能力：

- IndexedDB 持久化，重啟 / 更新後文件不消失
- 全域 in-app file tree，可在 sidebar / panel view 中管理
- editor toolbar 從單一檔名改為 breadcrumb
- 檔案 / 資料夾 rename、delete、create 行為完整
- New Tab 的 Editor 區塊提供最近開啟清單
- 為未來 sync contributor 保留穩定資料模型，但本次不實作 sync

## 2. Non-Goals

- **File module 重構**：`File module` 仍只橋接 daemon fs，不和 `Editor module` 共用資料層
- **統一 VFS**：不把 daemon/local/in-app 三種來源合成同一套虛擬檔案系統
- **Editor sync contributor 實作**：本次只預留 schema 與介面，不接入 `syncEngine`
- **搜尋 / filter in tree**
- **真正可用的 editor 偏好設定**
- **breadcrumb 中間 segment 的完整導覽互動**

## 3. 模組邊界

### 3.1 File module

- 只負責 daemon fs 橋接
- 現有 `FileTreeWorkspaceView` / `FileTreeSessionView` 維持 daemon 路徑語意
- 不讀寫 `Editor module` 的 IndexedDB

### 3.2 Editor module

- 自己擁有一份全域 in-app file tree
- 這份 file tree 存在 IndexedDB，路徑格式維持使用者可見的絕對路徑，例如 `/notes/a.md`
- `Editor module` 提供自己的 sidebar / panel view，作為內建檔案管理入口
- New Tab 與 Editor pane 都消費這份 file tree

## 4. 核心資料模型

### 4.1 三層拆分

`Editor module` 內部拆成三層，避免把 tab、buffer、path 綁死在一起：

1. `tab / pane reference`
   - pane 只引用 `docId`
   - tab 不以 `path` 作為文件唯一識別
2. `document / buffer state`
   - runtime editor 狀態
   - 內容、`savedContent`、dirty、cursor、language、open state
3. `file tree binding`
   - `docId <-> path`
   - file / folder 節點
   - `bindingStatus`、timestamps、recent-open metadata

### 4.2 穩定識別

- `docId` 是文件穩定主鍵
- `path` 是可變屬性，用於 UI、breadcrumb、recent open、未來 sync manifest
- folder 本身也有自己的 node id，但檔案編輯狀態只綁文件 `docId`

### 4.3 Path 規則

- 使用者可見 path 一律長這樣：`/notes/a.md`
- root 就是 `/`
- 不引入 `In-App/` 這類 display root alias

## 5. IndexedDB 設計

### 5.1 Stores

至少拆兩個 store：

1. `editor_nodes`
   - 檔案樹 metadata
   - 欄位：
     - `id`
     - `docId | null`
     - `path`
     - `name`
     - `parentPath`
     - `kind: 'file' | 'folder'`
     - `bindingStatus: 'active' | 'deleted' | 'orphaned'`
     - `createdAt`
     - `updatedAt`
     - `lastOpenedAt | null`
2. `editor_contents`
   - 實際內容
   - 欄位：
     - `docId`
     - `text`
     - `encoding: 'utf-8'`
     - `savedAt`
     - `version`

### 5.2 資料分離理由

- rename / move 只改 metadata，不重寫大段內容
- future sync 可分別 serialize tree metadata 與 content payload
- recent open 只需要 metadata，不必掃描內容
- file tree UI 只渲染 `bindingStatus === 'active'` 的節點；`deleted` / `orphaned` 只保留給已開文件狀態與恢復流程使用

### 5.3 Repository / Service 邊界

IndexedDB 的直接讀寫應集中在 `Editor module service`：

- `createFile(path, initialContent)`
- `createFolder(path)`
- `renameNode(fromPath, toPath)`
- `deleteNode(path)`
- `readDocument(docId)`
- `writeDocument(docId, text)`
- `listChildren(path)`
- `listRecentOpened(limit)`

UI 元件不直接操作 IndexedDB API。

## 6. Pane 與 Buffer 模型調整

### 6.1 PaneContent

現有 `{ kind: 'editor', source, filePath }` 對 in-app 文件不再足夠。新模型至少要支援：

```ts
type PaneContent =
  | { kind: 'editor'; source: { type: 'inapp' }; docId: string; filePath?: string; diff?: { against: 'saved' | string } }
  | { kind: 'editor'; source: { type: 'daemon'; hostId: string }; filePath: string; diff?: { against: 'saved' | string } }
  | { kind: 'editor'; source: { type: 'local' }; filePath: string; diff?: { against: 'saved' | string } }
```

- in-app 路徑下，`docId` 才是真正識別
- `filePath` 可保留做顯示快取與遷移，但不是唯一來源

### 6.2 Buffer Key

in-app 文件的 buffer key 改成以 `docId` 為主，而不是 `path`。

原因：

- rename / folder move 後 buffer 不應重建
- delete 後文件內容仍要保留在已開 tab
- 多個 tab 開同一文件時應共享同一份 runtime state

## 7. 檔案狀態模型

### 7.1 Binding Status

`deleted/orphaned` 應屬於文件和檔案樹的綁定狀態，不是 tab state。

本次定義：

- `active`
  - 文件在 file tree 中有有效 path
- `deleted`
  - path 已從 file tree 移除，但已開啟 buffer 仍存在
- `orphaned`
  - 文件內容仍存在，但原 path 無法直接寫回，例如父資料夾缺失，需要 `Save As`

### 7.2 Delete 後行為

- 刪除檔案 / 資料夾前先要求確認
- 不直接關閉已開 tab
- 受影響文件轉成 `deleted` 或 `orphaned`
- 內容保留在已開 buffer

這個行為要貼近 VS Code：

- tab 不會因外部刪除立即消失
- 使用者回到該 tab 仍可看到內容
- 後續可透過 `Save` 或 `Save As` 恢復

### 7.3 Save 等價流程

依使用者確認，`Save` 採 VS Code 等價流程：

- 原 path 仍可用：直接存回原路徑
- 父資料夾缺失：不自動補整段目錄，改要求 `Save As`
- `Save As` 允許把 orphaned 文件重新綁到新 path

## 8. Rename / Move 行為

### 8.1 單檔 rename

- 由 breadcrumb 最後一段或 file tree 觸發
- 同層名稱衝突時阻止提交並顯示警告
- rename 成功後：
  - 更新 file tree path
  - 更新 breadcrumb 顯示
  - 已開 tab 保留
  - dirty buffer 保留

### 8.2 Folder rename

- folder rename 是 prefix rewrite
- `/notes` -> `/journal` 時，所有子節點 path 同步更新
- 所有受影響的已開 editor pane 只刷新顯示路徑，不重建 tab 或 buffer

### 8.3 衝突規則

- 同一 parent 下不得重名
- file 和 folder 視為同名衝突
- 衝突時不覆寫、不 merge

## 9. UI 設計

### 9.1 Editor File Tree View

新增 `Editor file tree view`：

- scope: `system`
- 顯示全域 in-app file tree
- 可被放入 sidebar / panel region

v1 提供：

- expand / collapse folder
- open file
- create file
- create folder
- rename
- delete

這個 view 是 `Editor module` 的管理入口，不是 `File module`。

### 9.2 Editor Toolbar -> Breadcrumb

`EditorToolbar` 從單一檔名改成 breadcrumb：

- 反映完整 path，例如 `/notes/daily/2026-04-20.md`
- 最後一段是檔名
- dirty 狀態仍顯示
- rename 從 breadcrumb 進入

中間資料夾 segment 本次只做顯示，不強制實作導覽。

### 9.3 New Tab -> Recent Open

`Editor` new-tab provider 擴充為：

- 新建文字檔
- 新建 Markdown
- 最近開啟清單

最近開啟的排序定義：

- `open` 即更新 `lastOpenedAt`
- 不要求編輯或存檔才列入

### 9.4 Settings -> Editor

- 取代現在的 `editor-buffers` section 命名
- 這次不實作真正的偏好設定
- 顯示空狀態文案，表明 editor 相關偏好將放在這裡

## 10. Migration

### 10.1 舊有 in-app 文件

目前 `InAppBackend` 是記憶體 `Map`，沒有持久化，因此沒有需要從舊版 IndexedDB 遷移的真實資料。

### 10.2 舊有 Tabs

需要處理 persisted tabs 裡舊的 in-app pane：

- 舊資料形狀：`{ kind: 'editor', source: { type: 'inapp' }, filePath }`
- 新資料形狀：`{ kind: 'editor', source: { type: 'inapp' }, docId, filePath? }`

遷移策略：

- 若新啟動時找不到對應文件，該 pane 不直接開空白新文件
- 應顯示明確的「文件不存在 / 已失效」狀態，避免把資料遺失偽裝成新檔
- 只有經過使用者明確操作，才建立新文件

## 11. Sync 預留

本次不接 sync，但 schema 要符合未來 `editor` contributor 需求：

- 穩定 `docId`
- tree metadata 和 content 分離
- recent-open metadata 可獨立 serialize
- content version 欄位預留 migration / chunking 空間

## 12. 測試策略

### 12.1 Repository / Service

- create file / folder
- rename file
- rename folder with descendants
- delete file
- delete folder with descendants
- name conflict
- recent-open ordering

### 12.2 Store / Migration

- 舊 tab persist 形狀遷移
- in-app pane 以 `docId` 驅動 open buffer
- rename 後 open tab 維持 dirty state

### 12.3 UI

- breadcrumb 顯示完整 path
- breadcrumb rename 驗證衝突
- new tab recent open list
- editor file tree CRUD 互動
- delete 後文件進入 deleted/orphaned 狀態

### 12.4 Integration

- 重啟後文件仍存在
- 更新後 reopen editor 能讀回內容
- folder rename 後所有已開 tab 路徑同步刷新
- delete 後 `Save` / `Save As` 行為符合定義

## 13. 檔案改動概觀

**預期修改**

- `spa/src/lib/fs-backend-inapp.ts`
- `spa/src/components/editor/EditorPane.tsx`
- `spa/src/components/editor/EditorToolbar.tsx`
- `spa/src/components/editor/EditorNewTabSection.tsx`
- `spa/src/lib/register-modules.tsx`
- `spa/src/stores/useEditorStore.ts`
- `spa/src/stores/useTabStore.ts`
- `spa/src/types/tab.ts`
- `spa/src/locales/en.json`
- `spa/src/locales/zh-TW.json`

**預期新增**

- `spa/src/lib/editor-db/*`
- `spa/src/lib/editor-service/*`
- `spa/src/components/editor/EditorFileTreeView.tsx`
- `spa/src/components/editor/*` 對應測試

## 14. 風險與取捨

- 最大風險不是 IndexedDB 本身，而是既有 in-app editor 還綁 `filePath`
- 因此本次不採最小修補，而是直接把 in-app path 識別切到 `docId`
- 這會讓改動面稍大，但能一次解掉 rename / delete / recent / future sync 的結構問題

## 15. 結論

這次實作採用「`Editor module` 自有 IndexedDB file tree」而不是「擴充既有記憶體 `Map`」。

核心原則是：

- `File module` 與 `Editor module` 職責分離
- `docId` 穩定、`path` 可變
- tab、buffer、file binding 解耦
- delete 不直接摧毀使用者正在編輯的內容
- UI 先補齊 file tree、breadcrumb、recent open，sync 留明確接點但不提前實作

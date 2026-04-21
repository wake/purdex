# Automated Test Suite Stabilization Design

## 目標

讓最新 `origin/main` 基線上的主要自動化測試入口重新可跑完，且修正方式對齊目前已存在的架構，而不是把已淘汰的舊介面補回來。

本次目標限定為：

1. 修復 `make test` 目前被過期 Go 測試卡住的問題。
2. 修復 `pnpm --prefix spa exec vitest run` 目前被過期 `EditorPane` 測試卡住的問題。
3. 維持現行 `fs` module 與 `FsBackend` editor 架構，不為了讓舊測試通過而回滾設計。

## 問題摘要

### 1. Go 測試卡在已不存在的 `internal/module/files`

- `internal/module/files/` 目前只剩 `handler_test.go`，沒有任何實作檔。
- 該測試仍引用舊的 `New()`、`FileEntry`、`GET /api/files?path=` 契約。
- 實際上線的 module 已經是 `internal/module/fs/`，路由也已改成 `POST /api/fs/*`。
- 因此 `make test` 失敗的原因是 repo 內殘留了重構前的孤兒測試，而不是 `fs` module 本身壞掉。

### 2. SPA 測試卡在已移除的 editor / in-app API 假設

- `spa/src/components/editor/__tests__/EditorPane.test.tsx` 仍 mock `getInAppBackend().openDocument()` / `saveDocument()`。
- 現行 `EditorPane` 已經改為只走 `getFsBackend(source)` 的 `read/write/stat` 契約。
- 測試同時假設 buffer key 使用 `docId`，並驗證 `bindingStatus: "orphaned"`；這兩個語意目前在 store 與 component 都不存在。
- 因此失敗原因是測試案例仍然綁在已刪除的產品模型，不是 `EditorPane` 執行時偶發 loading 問題。

## 設計原則

1. 以現行產品行為為準修正測試，不回補 repo 其他地方已不存在的 API。
2. 只做能恢復測試可信度的最小變更，不順手擴大重構。
3. 先讓測試描述目前真實行為，再討論是否要新增新的產品能力。

## 非目標

- 不重建 `internal/module/files` package。
- 不恢復 `GET /api/files` 舊 endpoint。
- 不把 editor buffer 身分重新改回 `docId` 模型。
- 不新增 `bindingStatus` / `orphaned` save lifecycle，除非後續另外立 spec。

## 方案摘要

### 1. 移除或遷移孤兒 `files` 測試，讓 Go coverage 回到 `fs` module

處理方式：

- 以 `internal/module/fs/` 為唯一有效 module。
- 把 `internal/module/files/handler_test.go` 的有效覆蓋需求遷移成 `internal/module/fs/handler_test.go`。
- 新測試要對齊現行契約：
  - `Name()` 應為 `"fs"`
  - list API 走 `POST /api/fs/list`
  - request body 用 JSON `{"path":"/abs/path"}`
  - hidden file filtering、dir-first sorting、relative path reject、not found handling 等既有行為保留驗證
  - empty dir 時 `entries` 應回傳空陣列而不是 `null`
  - path 指向一般檔案時應維持既有錯誤處理
  - broken symlink 不應讓 list handler 崩潰
- 舊的 `internal/module/files/handler_test.go` 應移除，避免 `go test ./...` 再次編到不存在的 package 介面。

這樣做的理由：

- `files -> fs` 是既有架構遷移，不是暫時雙軌。
- 若保留舊測試，只會持續對 repo 宣告一套實際不存在的 API。

### 2. 以 `FsBackend` 為基準重寫 `EditorPane` 測試

測試應改為驗證目前真實存在的行為，而不是舊 in-app document model：

- mock `getFsBackend(source)`，回傳具備 `read/write/stat` 的測試 backend
- 讓 `EditorPane` 正常完成初始 load，避免測試自己把 backend mock 成 `undefined`
- buffer key 驗證改用現行規則：
  - `inapp:/notes/original.md`
  - `local:/path/to/file`
  - `daemon:<hostId>:/path/to/file`
- assertions 改為覆蓋目前 component 實作中的真實責任，例如：
  - 初次 render 後會讀檔、開 buffer、顯示 `pane.content.filePath` 的檔名
  - 修改內容後點 save，會呼叫 backend `write()` 與 `stat()`，並清掉 dirty 狀態
  - save 失敗時不會誤標記為已儲存，dirty state 仍保留
  - component unmount 時會 `closeBuffer(key)`，避免 store 殘留 buffer
  - tab 重新成為 active 且檔案外部變更時，只有在 buffer 非 dirty 狀態下才會 reload 內容

這樣做的理由：

- 目前 `EditorPane` 沒有 `docId path rebind` 與 `Save As -> orphaned` 邏輯。
- 若硬把這些語意補回來，只是為了讓過時測試通過，會把 editor 架構重新拉回已經移除的模型。

### 3. 驗證策略

修正後至少驗證：

1. `go test ./internal/module/fs -count=1`
2. `make test`
3. `pnpm --prefix spa exec vitest run "src/components/editor/__tests__/EditorPane.test.tsx"`
4. `pnpm --prefix spa exec vitest run`

所有驗證都必須在本次修改的目標 worktree 內完成，不能以其他 workspace 的通過結果替代。

若 fresh worktree 缺少前端依賴，應先在該 worktree 內補齊可執行的依賴環境，再執行上述 SPA 驗證。

## 風險與取捨

### 1. 風險：把「應該存在但被誤刪的功能」當成過期測試刪掉

目前已知依據：

- repo 內除了失敗測試本身，找不到 `docId` / `bindingStatus` 的現行使用點
- `internal/module/files` 實作已整體被 `internal/module/fs` 取代

因此目前判斷這兩塊都是測試漂移，不是產品功能被意外破壞。

### 2. 取捨：優先恢復測試可信度，而不是補歷史兼容層

這次修正會讓測試集反映「現在的產品」，代價是放棄保留已不存在的歷史契約。這個取捨符合目前 repo 已完成的架構遷移方向。

## 完成條件

以下條件全部成立才算完成：

1. `internal/module/files/` 不再因孤兒測試導致 Go suite build fail。
2. `EditorPane` 測試不再依賴 `getInAppBackend().openDocument/saveDocument`、`docId`、`bindingStatus`。
3. `internal/module/fs` 測試覆蓋至少保留舊 `files` suite 中仍對現行 handler 有意義的 edge cases。
4. `EditorPane` 測試覆蓋初始 load、save 成功、save 失敗、unmount cleanup、active reload 等目前真實責任。
5. `make test` 可在目標 worktree 跑完。
6. SPA Vitest 全量可在目標 worktree 跑完。

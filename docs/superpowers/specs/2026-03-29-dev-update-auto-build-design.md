# Dev Update Auto-Build 設計

## 問題

Dev update pipeline 有三個狀態點：Source（git）→ Build（`out/`）→ Client（`.app`）。

現行 `check` 端點用 `git log` 取 source hash，`download` 端點打包 `out/` 目錄。兩者來源不同步：source 有新 commit 但 `out/` 未重建時，Client 會無限顯示 "Update available"，點 Update App 後 hash 依然不變。

## 設計

### 1. Build Metadata — `out/.build-info.json`

`electron.vite.config.ts` 的 `closeBundle` hook 在 build 完成後寫入：

```json
{
  "version": "1.0.0-alpha.24",
  "spaHash": "088a9173",
  "electronHash": "731ef05d",
  "builtAt": "2026-03-29T04:30:00Z"
}
```

- Hash 來源：與現有 `buildDefines` 的 `gitHash()` 一致
- `out/` 已在 `.gitignore`

### 2. API — check 改版

`GET /api/dev/update/check` 新回應格式：

```json
{
  "version": "1.0.0-alpha.24",
  "spaHash": "088a9173",
  "electronHash": "731ef05d",
  "source": {
    "spaHash": "abc12345",
    "electronHash": "def67890"
  },
  "building": false,
  "buildError": ""
}
```

| 欄位 | 來源 | 用途 |
|------|------|------|
| 頂層 `spaHash`/`electronHash` | `out/.build-info.json` | Client 比對用（反映 download 實際內容） |
| `source.*Hash` | `git log` | 偵測是否需要 build |
| `building` | daemon 內部狀態 | Client 顯示 "Building…" |
| `buildError` | daemon 內部狀態 | 上次 build 失敗的錯誤訊息（空字串 = 無錯誤） |

### 3. Auto-Build 邏輯（Daemon）

`handleCheck` 流程：

1. 讀 `out/.build-info.json` → build hash（檔案不存在則為 `"unknown"`）
2. 讀 `git log` → source hash
3. 比對 source vs build：
   - 相同 → `building: false`，回傳 build hash
   - 不同且未在 building → 啟動背景 goroutine 執行 `npx electron-vite build`，回傳 `building: true`
   - 不同且已在 building → 回傳 `building: true`
4. Build 完成後 `.build-info.json` 由 vite hook 自動更新，daemon 的 `building` 歸 false

防護機制：
- `sync.Mutex` 防止並發 build
- Build 失敗時 `building` 歸 false，錯誤存入 `buildError`，下次 check 重新觸發
- `buildError` 在下次 build 啟動時清空
- `gitHash` 方法保留，用於 `source` 欄位

### 4. Client — Electron + SPA

#### 型別變更

```ts
interface RemoteVersionInfo {
  version: string
  spaHash: string
  electronHash: string
  source: { spaHash: string; electronHash: string }
  building: boolean
  buildError: string
}
```

#### UpdateStatus 擴充

`UpdateStatus` 新增 `'building'` 狀態。

#### DevEnvironmentSection UI 流程

```
Check → building: true?
  ├─ YES → 顯示 "Building…" (text-accent) + 每 3 秒 poll check
  └─ NO → 比對 build hash vs client hash
           ├─ 不同 → "Update available" + [Update App]
           └─ 相同 → "Up to date" ✅
```

- Hash 顯示：client hash → build hash（source hash 不顯示，是實作細節）
- Building 完成後自動觸發比對，不需使用者再按
- Building 狀態文字用 `text-accent`，與現有 update step 風格一致
- `buildError` 非空時顯示錯誤訊息（`text-status-error`）

#### main.ts startup check

結構不變。若回傳 `building: true`，不發 `dev:update-available` IPC 事件（SPA 自己 poll 到結果後處理）。

### 5. download 端點

不變。繼續打包 `out/main/` + `out/preload/`。Auto-build 由 check 觸發，download 時 `out/` 已是最新。

### 6. 完整流程

```
使用者開 app / 按 Check
        │
        ▼
  GET /check
        │
        ├─ 讀 .build-info.json (build hash)
        ├─ 讀 git log (source hash)
        │
        ▼
  source ≠ build?
    ├─ NO → 回傳 building:false, build hash
    │        Client 比對 build vs client
    │        ├─ 相同 → "Up to date"
    │        └─ 不同 → "Update available" + [Update App]
    │
    └─ YES → 已在 building?
              ├─ YES → 回傳 building:true
              └─ NO  → 啟動 goroutine: electron-vite build
                        回傳 building:true
                        │
                        ▼
              Client 顯示 "Building…"
              每 3 秒 poll GET /check
                        │
                        ▼
              Build 完成 → .build-info.json 已更新
              下次 poll → building:false
                        │
                        ▼
              比對 build vs client → 正常 update 流程
```

## 影響的檔案

| 檔案 | 變更 |
|------|------|
| `electron.vite.config.ts` | 加 `closeBundle` hook 寫 `.build-info.json` |
| `internal/module/dev/handler.go` | `handleCheck` 改讀 `.build-info.json` + auto-build |
| `internal/module/dev/module.go` | 加 `building` 狀態 + `sync.Mutex` + `triggerBuild()` |
| `electron/updater.ts` | `RemoteVersionInfo` 加 `source` + `building` 欄位 |
| `electron/main.ts` | startup check 處理 `building` 狀態 |
| `spa/src/components/settings/DevEnvironmentSection.tsx` | UI 流程 + polling + building 狀態 |
| `spa/src/locales/en.json` + `zh-TW.json` | building 狀態文字 |

## 關聯 Issue

- #78（feat: dev update — auto-build before download）：此設計涵蓋，PR merge 後關閉

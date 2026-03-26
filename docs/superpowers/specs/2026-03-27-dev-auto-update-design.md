# Dev Auto-Update System Design

開發環境專用的自動更新機制，讓 Air 上的 Electron `.app` 能從 Mini 的 daemon 檢查並下載更新。

## 背景

- Mini（100.64.0.2）：開發主機，程式碼編譯在此進行
- Air：作業端，執行 Electron `.app` 日常使用（dog-fooding）
- SPA 更新透過 dev server HMR 即時生效，不需額外機制
- Electron（main/preload）更新需要下載新的 build artifacts 並重啟

## 版本機制

- `VERSION` 檔案為 SOT（例如 `1.0.0-alpha.21`）
- 每個元件額外帶 build hash（git commit short hash）區分實際變更
- SPA hash：`spa/` 目錄下最新 commit 的 short hash
- Electron hash：`electron/` + `electron.vite.config.ts` 相關檔案最新 commit 的 short hash
- Hash 不同 = 該元件有變更需要更新

## Daemon 端

### `GET /api/dev/update/check`

回傳目前 Mini 上的版本資訊：

```json
{
  "version": "1.0.0-alpha.21",
  "spaHash": "abc1234",
  "electronHash": "def5678"
}
```

實作方式：
- 讀取 `VERSION` 檔案取得版本號
- 執行 `git log -1 --format=%h -- spa/` 取得 SPA hash
- 執行 `git log -1 --format=%h -- electron/ electron.vite.config.ts` 取得 Electron hash
- 僅在開發模式啟用（config flag 或 build tag 控制）

### `GET /api/dev/update/download`

回傳 `out/` 目錄的 tar.gz：

- 包含 `out/main/index.mjs` + `out/preload/index.js`（~12KB）
- Content-Type: `application/gzip`
- 下載前先執行 `electron-vite build` 確保 artifacts 為最新

## Electron 端

### 啟動時靜默檢查

App 啟動後，背景查詢 daemon 的 `/api/dev/update/check`：
- 比對本地 `electronHash`（build 時寫入的常數）
- 如果不同，在 title bar 或 status bar 顯示「有新版本可用」提示
- 不自動下載，等使用者手動觸發

### 更新流程

```
使用者點擊「更新 App」
→ GET /api/dev/update/download
→ 解壓 tar.gz 到暫存目錄
→ 覆蓋 .app bundle 內的 out/ 目錄
→ app.relaunch() + app.exit(0)
→ 新 main/preload 生效
```

### Build hash 注入

`electron-vite build` 時透過 Vite `define` 將 hash 注入為編譯時常數：

```typescript
// electron.vite.config.ts
define: {
  __ELECTRON_HASH__: JSON.stringify(execSync('git log -1 --format=%h -- electron/').toString().trim()),
  __SPA_HASH__: JSON.stringify(execSync('git log -1 --format=%h -- spa/').toString().trim()),
  __APP_VERSION__: JSON.stringify(readFileSync('VERSION', 'utf-8').trim()),
}
```

Main process 在啟動時將這些常數透過 IPC 提供給 renderer。

### Production 排除

- 使用環境變數 `TBOX_DEV_UPDATE=1` 控制（打包的 .app 中 `app.isPackaged` 為 `true`，不能用它區分 dev/prod）
- 不帶此環境變數的 build 不包含更新檢查邏輯
- Daemon 的 `/api/dev/update/*` 端點在 config 中透過 `[dev] update = true` 控制是否註冊

## SPA 端

### Settings「開發環境」section

新增 `DevEnvironmentSection`，註冊條件：環境變數 `TBOX_DEV_UPDATE` 存在（透過 preload IPC 傳給 renderer）。瀏覽器模式下不顯示。

#### 顯示內容

| 項目 | 來源 |
|------|------|
| App 版本 | `__APP_VERSION__`（build 時注入） |
| SPA 版本 | `__SPA_HASH__`（build 時注入）+ 遠端最新 hash |
| Electron 版本 | `__ELECTRON_HASH__`（build 時注入）+ 遠端最新 hash |
| 更新狀態 | 「已是最新」/「有新版本」/「檢查中...」 |

#### 操作

- **檢查更新按鈕**：呼叫 daemon `/api/dev/update/check`，比對 hash
- **更新 App 按鈕**：下載 + 覆蓋 + 重啟（僅在有新 Electron 版本時啟用）
- **SPA 重新載入按鈕**：`window.location.reload()`（保底，通常 HMR 已處理）

### i18n

新增 `settings.dev.*` 翻譯 key（en + zh-TW）。

### 註冊

在 `register-panes.tsx` 中：

```typescript
if (caps.isElectron && caps.devUpdateEnabled) {
  registerSettingsSection({
    id: 'dev-environment',
    label: 'settings.section.dev_environment',
    order: 20,
    component: DevEnvironmentSection,
  })
}
```

## 檔案變更清單

### Daemon（Go）
- `internal/dev/update_handler.go` — `/api/dev/update/check` + `/api/dev/update/download`
- `internal/dev/module.go` — Dev module（條件性註冊）
- `cmd/tbox/main.go` — 註冊 dev module

### Electron
- `electron.vite.config.ts` — `define` 注入 build hash + version
- `electron/main.ts` — 啟動時背景檢查更新
- `electron/preload.ts` — 新增 `getAppInfo()` + `checkUpdate()` + `applyUpdate()` IPC
- `electron/updater.ts` — 下載、解壓、覆蓋、重啟邏輯

### SPA
- `spa/src/components/settings/DevEnvironmentSection.tsx` — 開發環境設定頁面
- `spa/src/lib/register-panes.tsx` — 註冊 dev-environment section
- `spa/src/locales/en.json` — 新增 `settings.dev.*` keys
- `spa/src/locales/zh-TW.json` — 新增 `settings.dev.*` keys
- `spa/src/types/electron.d.ts` — 擴充 electronAPI 型別

### 測試
- `spa/src/components/settings/DevEnvironmentSection.test.tsx`
- `internal/dev/update_handler_test.go`

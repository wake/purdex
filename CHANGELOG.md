# Changelog

## [1.0.0-alpha.113] - 2026-04-14

### 修復

- **spa**：Workspace name input 加入 `maxLength=64` 防止超長名稱（#337）

## [1.0.0-alpha.112] - 2026-04-14

### 修復

- **spa**：ActivityBar badge 數字超過 99 時截斷為 `99+`（#336）

## [1.0.0-alpha.111] - 2026-04-14

### 功能

- **spa**：RenamePopover 新增 client-side session name 驗證（#335）
  - 即時格式檢查，鏡像 daemon 的 `^[a-zA-Z0-9_-]+$` 正則
  - 抽取 `isValidSessionName()` utility 至 `lib/session-name.ts`
  - Legacy session name 不觸發 popover 打開即顯示錯誤
  - 新增 i18n key（en + zh-TW）

## [1.0.0-alpha.110] - 2026-04-13

### 重構

- **daemon**：Probe Chain 三層探測架構取代舊 CC Detector（#334）
  - Liveness 層：process name + child process + content fallback，統一 CC/Codex 偵測
  - Readiness 層：ReadinessChecker interface，CC/Codex 各自實作狀態辨識
  - Activity 層：CapturePaneContent hash diff 偵測畫面變化，解決黃燈卡住問題
  - 刪除 `cc.Detector`、`cc.CCDetector` interface、`codex.detector`
  - Stream orchestrator 改用 `IsAliveFor` + `CheckReadiness` 組合

## [1.0.0-alpha.109] - 2026-04-13

### 功能

- **spa**：Icon 系統重構 — React.lazy + 1,445 chunks 改為 SVG path data 架構（#333）
  - Build-time 腳本從 `@phosphor-icons/core` 提取 SVG path，產生 6 個 per-weight 靜態 JSON
  - `icon-path-cache` 按需 fetch + 記憶體快取 + concurrent dedup + 失敗重試
  - `WorkspaceIcon` 同步 SVG 渲染，消除 Suspense 閃爍
  - `WorkspaceIconPicker` 改用 Fuse.js 模糊搜尋（支援 tags/categories）+ TanStack Virtual 虛擬捲動
  - Picker 新增 weight toggle UI，支援 6 種 Phosphor weight 即時預覽
  - `IconWeight` 從 3 種擴充為全部 6 種（bold/regular/thin/light/fill/duotone）
  - Build 產出：JS 檔案 1,446 → 1，dist 9.0MB → 6.1MB，main bundle 453KB → 448KB gz

## [1.0.0-alpha.108] - 2026-04-13

### 測試

- **spa**：新增 SortableTab 測試 — onPointerDown focus prevention、data-tab-id attribute、onSelect/onClose 互動（#211）

## [1.0.0-alpha.107] - 2026-04-13

### 功能

- **spa**：Workspace tooltip 顯示 unread 數量與 agent 狀態，與 aria-label 一致（#228）

### 修正

- **spa**：修正 active workspace 的 aria-label 錯誤包含 agent status（#228 review 發現）

## [1.0.0-alpha.106] - 2026-04-13

### 功能

- **spa**：RenamePopover 新增垂直 viewport clamping — 下方空間不足時翻轉到 anchor 上方，上下皆不足時 clamp 到頂部（#212）

## [1.0.0-alpha.105] - 2026-04-13

### 功能

- **daemon + spa**：Session list 即時同步 — SPA 新增 `useSessionWatch()` ref-counted polling hook，daemon `handleList` 新增 1s TTL debounce cache（#128）

## [1.0.0-alpha.104] - 2026-04-13

### 測試

- **spa**：補充 HandoffButton `agentStatus='error'` 和 `'waiting'` 測試案例（#125）

## [1.0.0-alpha.103] - 2026-04-13

### 修正

- **daemon**：`Stop()` 取消進行中的 build goroutine，防止 daemon restart 時產生並行 build（#99）

## [1.0.0-alpha.102] - 2026-04-13

### 功能

- **daemon**：dev auto-build 新增 5 分鐘逾時，防止 electron-vite 卡住導致 building 狀態永久鎖定（#97）

## [1.0.0-alpha.101] - 2026-04-13

### 重構

- **spa**：拆分 `useTabStore.test.ts` (517→343 行)，獨立 `terminated` 和 `migration` 測試檔案（#213）

## [1.0.0-alpha.100] - 2026-04-13

### 修正

- **electron**：merge window list 在新視窗 SPA 未載入時顯示 'Purdex' fallback 而非空白按鈕（#220）

## [1.0.0-alpha.99] - 2026-04-13

### 重構

- **spa**：建立 `closeTab()` helper 統一 locked guard 和 `destroyBrowserViewIfNeeded` 呼叫，修��� WorkspaceSettingsPage BrowserView 洩漏（#217）

## [1.0.0-alpha.98] - 2026-04-13

### 測試

- **electron**：新增 `keybindings.ts` 19 個單元測試 + vitest 測試基礎設施（#83）

## [1.0.0-alpha.97] - 2026-04-13

### 修正

- **daemon**：`handleDownload` 改為 buffer tar.gz 後才送出，Walk 錯誤時回傳 HTTP 500 而非產出損壞的 tar（#82）

## [1.0.0-alpha.96] - 2026-04-13

### 修正

- **daemon**：dev update 改用 config `repo_root` 欄位取代 `os.Getwd()`，daemon 從非 repo 目錄啟動時不再失效（#79）

## [1.0.0-alpha.95] - 2026-04-13

### 修正

- **LocaleEditor / ThemeEditor**：編輯 custom entry 時就地更新，不再每次 Save 建立重複項目（#74）

## [1.0.0-alpha.94] - 2026-04-13

### 變更

- **App icon**：符合 Apple HIG 標準（留白 100px、圓角 185.4px、陰影 Y12 blur28 @1024）
- **Per-arch build**：`scripts/build-electron.mjs` 按 arch 切換 icon 打包
- **i18n**：補齊 `settings.section.modules` 翻譯
- **ActivityBar**：Home 按鈕使用 Purdex 透明 logo

## [1.0.0-alpha.93] - 2026-04-12

### 修正

- **App icon**：改用白色圓角背景版，ActivityBar 保留透明版（`logo-transparent.png`）
- **i18n**：補齊 `settings.section.modules` 翻譯（Modules / 模組）
- **Legacy hooks**：清除舊 tbox hook 殘留，不做向下相容

## [1.0.0-alpha.92] - 2026-04-12

- chore: rename cleanup + Purdex logo (#315)

### 變更

- **Go 內部符號**：`tboxPath`/`makeTboxEntry`/`isTboxCommand` 等全部更名為 `pdxPath`/`makePdxEntry`/`isPdxCommand`
- **App 圖示**：PWA icons、macOS .icns、favicon 全部替換為 Purdex logo
- **ActivityBar**：Home 按鈕使用 Purdex logo 取代 SquaresFour icon
- **Maskable icon**：加入紫色背景 + 70% safe zone padding
- **型別安全**：新增 `SplitLayout` 型別 + `isSplit` type guard，移除 `PaneLayoutRenderer` 的 unsafe `as` cast
- **測試**：新增 `isGrid4` 6 個單元測試

## [1.0.0-alpha.91] - 2026-04-12

- refactor: brand rename from tmux-box to Purdex (#308, #309, #310, #311, #312)

### 變更

- **專案更名**：tmux-box → Purdex，CLI binary tbox → pdx
- **Go module**：`github.com/wake/tmux-box` → `github.com/wake/purdex`
- **Config 路徑**：`~/.config/tbox/` → `~/.config/pdx/`
- **環境變數**：`TBOX_TOKEN` → `PDX_TOKEN`、`TBOX_DEV_UPDATE` → `PDX_DEV_UPDATE`
- **tmux session channel**：`tbox_sess_evt` → `purdex_sess_evt`
- **Electron**：appId `dev.wake.purdex`、productName `Purdex`

## [1.0.0-alpha.90] - 2026-04-12

- feat: daemon background mode + crash log + reconnect error clear (#307)

### 新增

- **`tbox start/stop/status` 子命令**：daemon 可背景啟動，使用 `flock` PID file 管理生命週期，`stop` 以 SIGTERM 優雅關閉（30 秒 timeout + SIGKILL fallback）
- **Crash log**：`runServe` 加 panic recover defer，寫入 `~/.config/tbox/logs/crash-YYYYMMDD-HHMMSS.log`，含 secret redaction（Authorization header、purdex\_/tbox\_ token、cfg.Token）
- **Logs 子頁**：per-host Logs sub-page 顯示 Daemon Log（`/api/logs/daemon`）+ Crash Logs（`/api/logs/crash`），含手動 refresh + offline 狀態

### 修正

- **Reconnect 後 testResult 殘留**：`OverviewSection` 加 transition-aware `useEffect`，只在 status 從非 connected 轉為 connected 時清除 stale 錯誤訊息，避免 `manualRetry()` 循環清掉成功 pill

### 追蹤

- #305 — safeGo helper for cross-goroutine panic recovery
- #306 — HTTP recover middleware

## [1.0.0-alpha.89] - 2026-04-12

- fix(electron): restore renderer focus when backgrounding BrowserView (#301)

### 修正

- **切回 Terminal tab 後 terminal 無法 auto-focus**：使用者點過 Browser tab 的 `WebContentsView` 內容後，OS 鍵盤 focus 會留在那個 webContents；切回 Terminal tab 時 `BrowserPane` unmount → `closeBrowserView` → `BrowserViewManager.deactivate()` 只把 view 移到 off-screen，沒把 focus 交回主視窗 renderer，導致 `TerminalView` 在 visible-effect 中呼叫的 `termRef.current.focus()` 形同無效（DOM element focus 撈不到鍵盤輸入），使用者必須手動點一下 terminal 才能恢復輸入。修法在 `deactivate()` 移 off-screen 後呼叫 `entry.window.webContents.focus()` 把鍵盤 focus 交回 host renderer；以 `entry.window.isFocused()` 守衛避免 multi-window 場景搶奪其他 window 的 focus。後續在 #302/#303/#304 追蹤 destroy/discard 路徑、pop-out 流程、反向 activate focus 等延伸問題

## [1.0.0-alpha.88] - 2026-04-12

- revert: undo ineffective TitleBar cursor-pointer attempts (#296, #297) (#299)

### 回退

- **TitleBar 右側按鈕 pointer cursor 問題無法用 CSS 解決**：`#296`（把 `-webkit-app-region: drag` 移到 flex spacer）與 `#297`（加 `relative z-10` + `cursor-pointer!` important）都已用 Electron DevTools 驗證對 macOS OS 游標毫無影響 — computed cursor 已經是 `pointer !important`，問題出在 Electron 上游（[electron/electron#5723](https://github.com/electron/electron/issues/5723)、[#21632](https://github.com/electron/electron/issues/21632)）：`titleBarStyle: 'hiddenInset'` 下 NSWindow 仍保留頂部 ~38px 的 title bar 區域並攔截 Chromium 的 cursor update，但 click event 正常。將 `spa/src/components/TitleBar.tsx` 回退到 `#296` 之前的狀態，避免保留無效的 dead code；真正的修復（結構性繞開，仿 VSCode 把按鈕放到 tracking zone 以外）改由 #300 追蹤，標 `pending` 暫擱

## [1.0.0-alpha.87] - 2026-04-12

- fix(electron): restore Toggle Developer Tools in View menu (#298)

### 修正

- **DevTools 快捷鍵失效**：`electron/keybindings.ts:buildMenuTemplate` 的 View menu 只收錄 `byCategory.get('View')` 的自訂項目，完全沒有 `role: 'toggleDevTools'`，自訂 menu 一旦 `Menu.setApplicationMenu` 取代預設 menu，Electron 內建的 `Cmd+Option+I` 就一併遺失。在 View submenu 尾端補上 separator + `{ role: 'toggleDevTools' }`，Electron 自動綁回預設 accelerator，`Cmd+Option+I` 與 View → Toggle Developer Tools 都可打開 DevTools

## [1.0.0-alpha.86] - 2026-04-12

- fix(spa): force cursor pointer on TitleBar buttons (#297)

### 修正

- **TitleBar 按鈕 cursor pointer 強制生效**：#296 只把 `-webkit-app-region: drag` 從容器移到 spacer，但 absolute `inset-0 pointer-events-none` 的 title 層在 paint order 上仍高於未定位的 buttons（CSS positioned siblings 不論 DOM 順序皆覆於 non-positioned siblings 上），Electron/Chromium 的 cursor fall-through 在此情境下不穩。將 buttons 容器升入 positioned stacking layer（`relative z-10`）讓它確實畫在 title 之上、重新加回 `WebkitAppRegion: 'no-drag'` 雙保險、每顆 button 改用 Tailwind v4 的 `cursor-pointer!`（產生 `!important`）硬覆寫任何競爭的 cursor 規則，disabled 態對應 `disabled:cursor-default!`

## [1.0.0-alpha.85] - 2026-04-12

- fix(spa): restore cursor pointer on TitleBar buttons (#296)

### 修正

- **TitleBar 右側按鈕無 pointer cursor**：原本 `spa/src/components/TitleBar.tsx` 最外層容器帶 `-webkit-app-region: drag`，Chromium 對 drag region 的所有子孫強制使用預設游標，即便按鈕 container 已設 `no-drag` 也無法覆寫 `cursor-pointer`。改為最外層不帶 drag、另外插入一個 `flex-1 self-stretch` spacer 承擔 `WebkitAppRegion: 'drag'`，讓右側按鈕脫離 drag region 祖先鏈；視窗拖曳行為由 spacer 提供，置中標題仍以 `absolute inset-0 pointer-events-none` 跨滿整條 bar

## [1.0.0-alpha.84] - 2026-04-12

- fix(session): guard handleCreate with mutex against same-name race (#61) (#294)

### 修正

- **#61 handleCreate TOCTOU race**：`internal/module/session/handler.go:handleCreate` 原本從 `HasSession` 到 `SetMeta` 完全無鎖，兩個同名 POST 可以同時通過 duplicate check 然後各自呼叫 `NewSession`，在 FakeExecutor 上重現為 `sessionOrder` 重複 entry（確定性），在真實 tmux 上會讓第二次呼叫失敗並回傳 500 而非 409。新增 `SessionModule.createMu sync.Mutex`，`handleCreate` 於輸入驗證後 `Lock + defer Unlock`，涵蓋整段 `HasSession → NewSession → ListSessions → SetMeta` critical section。Watcher goroutines 不取此鎖，可能看到中間狀態但下一輪會自動修正（已追蹤為 #295）
- **test infra — `:memory:` connection pool pinning**：`internal/store/meta.go:OpenMeta(":memory:")` 新增 `db.SetMaxOpenConns(1)`。Go `database/sql` 的 pool 可能對同一 DSN 開多條連線，而每條 `:memory:` 連線各自是一個獨立的空 DB，先前讓並發測試隨機打到 empty DB 出現 `no such table: session_meta`。只對 `:memory:` 路徑生效，production 使用 `cfg.DataDir/meta.db` 不受影響
- **新增並發回歸測試**：`TestHandlerCreateSessionConcurrentSameName` 以 close-on-start barrier 釋放 N=100 個 goroutine 同時 POST 相同 session name，斷言恰好 1 個 201、99 個 409、`ListSessions()` 長度 == 1；於 `-race -count=20` 下穩定通過

## [1.0.0-alpha.83] - 2026-04-12

- fix: rollback in-memory config on writeConfig failure (#28) (#293)

### 修正

- **#28 atomic config update**：`internal/core/config_handler.go` 的 `handlePutConfig` 原本先更新記憶體 `c.Cfg`、再寫檔；若 `config.WriteFile` 失敗則回 500 但記憶體已變，造成執行中的 daemon 使用新設定、重啟後讀回舊設定的不一致。改在 mutation 前 `snapshot := *c.Cfg`，寫檔失敗時 `*c.Cfg = snapshot`（透過指標寫回，保留其他 goroutine 持有的 `c.Cfg` 指標身分），整段都在 `CfgMu.Lock` 範圍內；`NotifyConfigChange` 仍只在成功分支觸發
- **新增 rollback 回歸測試**：`TestPutConfigRollsBackOnWriteFailure` 將 `CfgPath` 指向「父路徑是一般檔案」的位置，觸發 `MkdirAll` ENOTDIR（跨平台可靠、無需 chmod cleanup），驗證 500 + 錯誤訊息含 "not a directory"、Stream/Detect/Terminal 全數 rollback、callback 未觸發、`c.Cfg` 指標身分未變、rollback 後再發一次正常 PUT 仍可成功
- **新增 invariant 註解**：snapshot 與 mutation block 各有一段註解明確要求所有 mutation 必須整個欄位指派，禁止 `append` 或 map in-place 寫入（shallow snapshot rollback 正確性的前提）

## [1.0.0-alpha.82] - 2026-04-12

- fix: lock CfgMu when reading sizing mode in HandleTerminalWS (#26) (#292)

### 修正

- **#26 race fix**：`internal/module/session/service.go` 的 `HandleTerminalWS` 讀取 `m.core.Cfg.Terminal.GetSizingMode()` 時未持 `CfgMu.RLock`，與 `handlePutConfig` 在 `CfgMu.Lock` 下寫入相同欄位產生 data race。改採 snapshot pattern：read lock 內取值後立即解鎖，仿 `internal/module/stream/handler.go:177-182` 既有 pattern
- **新增 race 回歸測試**：`TestHandleTerminalWS_NoConfigRace`（50 reader × 20 iterations + 1 writer），於 `go test -race` 下驗證；修復前命中 DATA RACE，修復後通過。Test cleanup 用 `sync.Once + t.Cleanup` 確保 panic 時 writer goroutine 不 leak

## [1.0.0-alpha.81] - 2026-04-11

- refactor: App.tsx 拆分 — 提取 hooks + 具名 callback (#282)
- fix: SubagentDots 燈號殘留 (5 root cause + 3 輪 review follow-up) (#283)

### 重構

- **App.tsx 409 → 286 行**：提取 `GlobalUndoToast` 為獨立元件、`useElectronIpc` hook（收納 4 個 IPC effect）、`useWorkspaceWindowActions` hook（workspace tear-off/merge）
- **`openSingletonAndSelect`**：在 `useTabWorkspaceActions` 新增 helper 統一 4 處 singleton tab 開啟模式，支援可選 `wsId` 參數
- **Inline lambda 全面具名化**：JSX 不再包含業務邏輯，所有 handler 提為 `useCallback`
- Closes #202, #219, #225, #237, #243, #261, #281

### 修正

- **#231**：`onWorkspaceReceived` catch 範圍縮窄，僅捕 `JSON.parse` 錯誤避免靜默吞掉 store mutation 錯誤
- **`openWsSettings` cross-workspace**：修正右鍵非 active workspace 的 settings 時 tab 被插入錯誤 workspace
- **Bug 0 (主因)**：移除 `Subagents` 的 `omitempty` tag，最後一筆 `SubagentStop` 永遠送 `subagents:[]`，前端不再卡住
- **Bug 0b**：新增 `RenameSessionAtomic(old, new, doRename)` API，把 tmux + DB + in-memory rename 包進單一 lock，修復 rename 後 hook 用新名查空 map 的問題
- **Bug 1**：新增 `useAgentStore.clearSession(hostId, code)` action，session-closed 時集中清理
- **Bug 2**：`checkAliveAll` orphan 分支改用 `tmux.HasSession()` 二次確認，防止 `ListSessions` 暫時性失敗時誤刪
- **Bug 3**：`SubagentStart` guard 改用 `events.Get + DeriveStatus(StatusClear)` 持久化 DB state，修復 daemon restart + compact `SessionStart` 邊界
- **rename rollback**：`renameSessionAtomic` 改為 DB-first，tmux 失敗時 best-effort 回滾 DB，確保三方一致

## [1.0.0-alpha.80] - 2026-04-10

fix: SubagentDots 相位同步 + terminal reconnect 自動恢復 (#279)

### 修正

- **SubagentDots 動畫相位同步**：`useMemo` dependency 改為 `[clamped]` + key 強制 remount，修正新 subagent dot 與既有 dot 相位不同步的雜亂閃爍
- **Terminal reconnect gate 持續重試**：`canReconnect()` gate 回 false 時改為固定間隔輪詢（不累積 backoff），host 恢復後自動重連
- **connectWithTicket 殭屍 WS 防護**：await getTicket 後加 `closed` guard，防止 unmount 後建立多餘 WebSocket

## [1.0.0-alpha.79] - 2026-04-10

fix: topbar cursor pointer + host 子分頁切換保留 (#276)

### 修正

- **TitleBar 按鈕 cursor pointer**：明確加 `cursor-pointer` class 修正 Electron drag region 覆蓋 cursor 樣式的問題
- **Host 子分頁切換保留**：展開不同 host 時保留當前選中的 subPage，不再 hardcode reset 到 overview

## [1.0.0-alpha.78] - 2026-04-10

Quick Commands + Host Agents + tmux 精確匹配修正 (#269)

### 新增

- **Quick Commands Module**：可插拔指令快捷系統 — module registry `commands` extension point、QuickCommandStore（global + per-host persist）、`useCommands()` hook、QuickCommandMenu 下拉選單
- **POST /api/sessions/{code}/send-keys**：透過 `SendKeysRaw` 送指令到 tmux session
- **GET /api/agents/detect**：偵測 host 上 claude/codex CLI 安裝狀態（path + version）
- **Host > Agents 子頁面**：顯示各 agent CLI 的安裝狀態
- **Codex Hooks**：HOOK_MODULES 加入 CODEX_HOOKS，Hooks 頁面顯示 tmux/CC/Codex 三區

### 修正

- **tmux 精確匹配**：RenameSession / KillSession / send-keys target 加 `=` 前綴避免 prefix matching 誤判
- **handleRename 重名 409**：rename 前檢查目標名稱是否已存在
- **CC getLastTrigger**：加 `agent_type` 過濾，避免 codex event 被算進 CC

### 移除

- **Settings > Agent 區塊**：hooks 統一走 Host > Hooks 管理

## [1.0.0-alpha.77] - 2026-04-10

Sidebar / Panel View Management (#266)

### 新增

- **Region 管理 UI**：每個 sidebar/panel 可自行管理啟用哪些 view、拖曳排序
- **三個管理入口**：⚙ 按鈕（pinned header）、+ 按鈕（collapsed bar）、右鍵 context menu
- **RegionManager 元件**：替換 region 內容的管理畫面，@dnd-kit drag-to-reorder
- **RegionContextMenu 元件**：右鍵 checkbox 清單快速開關 view
- **View scope 三層模型**：ViewDefinition.scope 支援 `'system' | 'workspace' | 'tab'`
- **Layout Store 新 actions**：`addView`、`removeView`、`reorderViews`

### 改善

- **toggleVisibility 記憶狀態**：TitleBar toggle 隱藏後恢復時記住之前是 pinned 或 collapsed
- **Region 空狀態**：空 region 仍渲染管理入口（不再完全消失）
- **TitleBar**：始終顯示全部 4 個 region toggle 按鈕

### 移除

- `ViewDefinition.defaultRegion`（view 不再綁定特定 region）
- `getViewsByRegion()` 查詢函式（改為 `getAllViews()`）
- `WorkspaceSidebarState` 型別（未被使用）

## [1.0.0-alpha.76] - 2026-04-10

### 修正

- **Topbar 標題置中**：改用 absolute 定位，以完整視窗寬度置中，不受右側按鈕影響 (#262)
- **全域 cursor: pointer**：全域 CSS 規則讓所有 button、[role="tab"]、[role="button"] 預設顯示 pointer cursor (#263)
- **Workspace icon 拖曳範圍限制**：restrictToVertical modifier 增加 Y 軸邊界計算，限制拖曳在列表範圍內 (#264)

### 變更

- **Tab Indicator Style 設定隱藏**：從 Settings UI 移除選項，預設改為 replace，保留 store 架構

## [1.0.0-alpha.75] - 2026-04-10

### 修正

- **Session 建立重名 409**：`handleCreate` 建 session 前先 `HasSession` 檢查，回 409 Conflict + 明確訊息；`HasSession` 改用 `=` 前綴精確匹配避免 tmux prefix matching 誤判 (#260)
- **createRequest 加 Mode 欄位**：前端傳的 mode 不再被 JSON decoder 靜默忽略，新建 session 正確套用 terminal/stream (#260)
- **Workspace icon 拖曳排序恢復**：恢復在 status pill refactor 中遺失的 `@dnd-kit` DnD（`SortableWorkspaceButton`、`reorderWorkspaces` store action）；加防禦性 guard 防止 stale orderedIds 丟失 workspace (#260)
- **Tab bar + 按鈕位置**：移除 `normalTabsRef` 的 `flex-1`，+ 按鈕緊鄰最後一個 tab (#260)
- **前端錯誤訊息改善**：NewSessionDialog 顯示 response body 而非僅 HTTP status code (#260)

## [1.0.0-alpha.74] - 2026-04-10

### 修正

- **Topbar region toggle**：從展開/收合改為完全顯示/隱藏，新增 `toggleVisibility` action (#259)
- **Workspace module settings**：WorkspaceSettingsPage 加入 ModuleConfigSection，workspace 範圍模組設定現在可見 (#259)

## [1.0.0-alpha.73] - 2026-04-10

Agent Module Provider Pattern 重構 (#247)

### 新增

- **AgentProvider 介面**：capability-based composition（HookInstaller、HistoryProvider、StreamCapable），支援多 agent 擴充
- **Agent Registry**：thread-safe provider 註冊/查詢，hook event 路由依 agent_type 分派
- **CC Provider**：從散落的 cc/detect 模組整合為 `internal/agent/cc/`（detector、operator、history、hooks、status derivation）
- **Codex Provider**：新增 `internal/agent/codex/`（status derivation、process detection、hook installer）
- **NormalizedEvent**：後端推導 status/model/subagents，前端零 per-agent 邏輯
- **Per-agent hook 管理**：`GET/POST /api/hooks/{agent}/status|setup` 參數化 API
- **Agent icons**：`spa/src/lib/agent-icons.ts` icon + name mapping
- **AgentSection UI**：Settings 頁面顯示每個 agent 的 hook 安裝狀態與操作按鈕

### 改善

- **CLI `--agent` flag**：`tbox hook` 和 `tbox setup` 必須指定 agent type
- **useAgentStore 簡化**：移除前端 `deriveStatus`，store 只接收後端 pre-derived 狀態
- **Frontend agent-agnostic**：新增 agent 只需後端加 provider + 前端加一行 icon mapping

### 移除

- `internal/module/cc/`（整個目錄）— 合併至 `internal/agent/cc/`
- `internal/detect/`（整個目錄）— 合併至 `internal/agent/cc/`
- `internal/module/agent/cc_hooks.go` — 移至 `internal/agent/cc/hooks.go`
- 前端 `deriveStatus` 函式、`AgentHookEvent` 型別、`clearSubagentsForHost` action

## [1.0.0-alpha.72] - 2026-04-10

Sidebar/Panel/Pane 修正 + Module Config 系統 (#245)

### 新增

- **Module Config 系統**（#244）：Module 透過 registry 宣告 workspace/global 層級設定，Workspace 提供泛用 `moduleConfig` 儲存，Settings 頁面自動產生表單
- **Files Module 拆分**：workspace view（以 projectPath 為根）+ session view（placeholder，待 daemon cwd API）
- **Pane swap 功能**：PaneHeader 新增交換按鈕，可在同 tab 的不同 pane 間交換內容
- **Tab mergeToTab**：Tab 右鍵選單新增「加入 Tab 成為 pane」功能
- **TitleBar Region Toggle**：4 個 sidebar/panel 切換按鈕，僅在 region 有 view 時顯示
- **Global Module Config Store**：`useModuleConfigStore` 支援全域 module 設定持久化

### 改善

- **PaneSplitter 視覺**：hover 加寬、顏色加深、hit area 擴大
- **PaneHeader 視覺**：按鈕加大、邊框加強
- **Grid-4 水平聯動**：四宮格水平 splitter 同步 resize
- **Pane detach 位置**：彈出的 tab 插入到來源 tab 的下一位（而非尾端）
- **SidebarRegion props**：正確傳遞 region/workspaceId/hostId 給 view component
- **insertTab afterTabId**：workspace store 支援指定位置插入 tab

## [1.0.0-alpha.71] - 2026-04-10

Home 按鈕未讀指示修正 + standalone tab 關閉 scope 修正

### 修正

- **Home badge 未讀數**：Home 按鈕 badge 改用 `useWorkspaceIndicators` 計算未讀數，而非顯示 standalone tab 總數
- **Home status dot**：Home 按鈕新增 status dot indicator（running/waiting/error），與 workspace 按鈕一致
- **standalone tab 關閉 scope**：關閉 standalone tab 時，nextTab 候選範圍改為只包含其他 standalone tabs，避免跳到 workspace tab
- **standaloneTabIds memoize**：`useMemo` 穩定 `standaloneTabIds` 陣列引用，避免每次 render 重算 indicator

## [1.0.0-alpha.70] - 2026-04-09

Workspace 狀態指示器迭代

### 變更

- **Status dot**：workspace icon 狀態指示器從 3px 左側 pill 改為 5px 圓點，與 TabStatusDot 風格一致
- **Active 隱藏**：active workspace 不顯示狀態圓點（狀態已可在 tab bar 看到）
- **Aria-label 強化**：workspace 按鈕的 aria-label 現在包含 agent 狀態（running/waiting/error）
- **測試補強**：新增 4 個 status dot 測試（顯示/隱藏/waiting 靜態/aria-label）

## [1.0.0-alpha.69] - 2026-04-09

Module Layout Foundation (Plan 1+2) + Review 修正

### 新功能

- **Module Registry**：統一 pane + view 註冊系統（`module-registry.ts`），取代舊 `pane-registry.ts`
- **Layout Store**：4-region sidebar/panel 狀態管理（`useLayoutStore`），持久化到 `purdex-layout`
- **TitleBar**：Electron 標題列元件，含 traffic light safe zone + layout pattern 按鈕 placeholder
- **SidebarRegion**：可折疊/展開的 sidebar 容器，支援 view 切換 + 拖曳調整寬度
- **RegionResize**：拖曳調整 region 寬度的把手元件
- **App 佈局重構**：統一 TabBar 位置 + 4 SidebarRegion 整合

### 修正（Review）

- **RegionResize stale closure**：drag 時 `onResize` callback 使用 `useRef` 保持最新引用
- **syncManager 註冊遺漏**：`useLayoutStore` 現在正確註冊 syncManager，跨 tab/視窗同步
- **ViewDefinition icon 型別**：`icon` 從 `string` 改為 React component type，collapsed bar 渲染 Phosphor Icon
- **activeViewId fallback**：展開 region 時若 activeViewId 未設定，自動 fallback 到第一個 view
- **移除 `mode: 'default'`**：移除從未使用的 mode 值，RegionState 只保留 `'pinned' | 'collapsed'`
- **`side` → `resizeEdge`**：prop 更名為更清晰的語義
- **TitleBar layout 按鈕加 disabled**：Plan 3 前 placeholder 按鈕標記為 disabled
- **移除 `--app-region` 死碼**：TitleBar 移除無效的 CSS custom property
- **PaneLayoutRenderer 空 children 防護**：split layout children 為空時顯示 fallback
- **SidebarRegion 收合按鈕**：展開狀態新增 collapse button

## [1.0.0-alpha.68] - 2026-04-09

Workspace icon indicators — unread badge + status pill (PR #226)

## [1.0.0-alpha.67] - 2026-04-09

Tab 拖曳 / Rename CORS / 通知 GC / Tab 溢出 / 上傳階段修復 (PR #224)

### 修正

- **Active tab 無法拖曳**：dnd-kit 檢查 `nativeEvent.defaultPrevented` 會靜默中止拖曳；調整 `handlePointerDown` 順序，先呼叫 dnd-kit handler 再 `preventDefault()`
- **Rename session "Failed to fetch"**：CORS middleware 缺少 `PATCH` method，瀏覽器 preflight 被拒
- **通知點擊無反應**：Electron `Notification` JS wrapper 在 `show()` 後被 GC 回收（C++ 層不使用 `SelfKeepAlive`），用 `Set<Notification>` 保持強引用
- **Tab 溢出整個 app**：Electron title bar tab 容器缺少寬度約束；加入 `flex-1 min-w-0` 並讓 `normalTabsRef` 使用 `flex-1` + `min-content` 讓 tab 先縮減再捲動

### 新功能

- **上傳「輸入中…」階段**：圖片上傳流程新增 `typing` 狀態，完整流程為 `uploading → typing(1.5s) → done(3s) → dismiss`

## [1.0.0-alpha.66] - 2026-04-09

Browser UX 修復 — mini window theme + tab shortcuts (PR #223)

### 修正

- **Mini window toolbar 不可見**：獨立瀏覽器視窗現在正確初始化 theme（ThemeInjector + useThemeStore hook），toolbar 可見
- **Mini window Cmd+W**：獨立視窗現在可用 Cmd+W 關閉

### 新功能

- **Tab shortcut handler registry**：不同 tab type 可註冊各自的快捷鍵 handler，useShortcuts 作為 dispatcher
- **Browser 導航快捷鍵**：Cmd+[/] (back/forward)、Cmd+←/→ (macOS)、Cmd+R (reload)、Cmd+L (focus URL)、Cmd+P (print)
- **Print IPC**：新增 browser-view:print channel，支援 Cmd+P 列印

## [1.0.0-alpha.65] - 2026-04-09

Home tab 殘留修復 + workspace icon tooltip (PR #221)

### 修正

- **Home tab 殘留**：切回 Home 時若無 standalone tabs，`activeTabId` 現在正確清除為 null，不再殘留前一個 workspace 的 tab
- **Workspace icon tooltip**：ActivityBar workspace icon hover 即時顯示名稱（CSS tooltip），取代原生 title 延遲；改用 `aria-label` 避免雙重 tooltip

## [1.0.0-alpha.64] - 2026-04-09

新增 ⌘⇧H 快捷鍵開啟 Host 管理面板 (#178)

### 新功能

- **⌘⇧H 開啟 Hosts 面板** — 以 singleton tab 方式開啟 Host 管理頁面，與 ⌘, (Settings)、⌘Y (History) 操作模式一致

## [1.0.0-alpha.63] - 2026-04-09

Workspace 增強 — Ctrl+Tab、通知導航、workspace tear-off/merge (PR #218)

### 新功能

- **Ctrl+Tab / Ctrl+Shift+Tab** — 切換 tab 的替代快捷鍵（macOS 部分鍵盤設定可能衝突）
- **通知點擊切 workspace** — 點擊通知自動切換到含有該 tab 的 workspace（或回到 Home）
- **Workspace tear-off** — 右鍵 workspace context menu「獨立到新視窗」，整個 workspace 搬到新 Electron 視窗
- **Workspace merge** — 右鍵「合併到視窗」，將 workspace 合併到另一個已開啟的視窗
- **importWorkspace store action** — 跨視窗 workspace 傳輸，含 ID 去重

### 修正

- merge 視窗清單過濾當前視窗（避免 self-merge）
- IPC 呼叫成功後才清理 store（防止失敗時資料遺失）
- merge 目標消失時正確 throw（不再靜默失敗）
- workspace 接收端驗證 activeTabId 存在性
- tear-off/merge 後全域 activeTabId 與 workspace activeTabId 同步
- spa:ready listener 在視窗關閉時清除（防止洩漏）
- 禁止 tear-off 空 workspace（0 tabs guard）
- getWindows() IPC 加 .catch() 防止永久 Loading

## [1.0.0-alpha.62] - 2026-04-08

Workspace activeTabId 同步修正

### 修正

- **快捷鍵 tab 切換同步 workspace** — switch-tab-*、prev/next-tab、switch-workspace 快捷鍵現在正確同步 `ws.activeTabId`，切回 workspace 時恢復最後瀏覽的 tab
- **冗餘寫入優化** — `activateTab` helper 在值未改變時跳過 `setWorkspaceActiveTab`

## [1.0.0-alpha.61] - 2026-04-08

Close-tab workspace scoping 重構 + ActivityBar 間距 (PR #216)

### 重構

- **`closeTabInWorkspace` composite action** — 取代 hook 層 post-close 補丁，在 workspace store 一次完成 recordClose → removeFromWorkspace → closeTab → workspace-scoped active tab 選取（visitHistory 優先 → adjacent fallback）
- **`closeTab` 簡化** — 移除全域 tabOrder auto-select，只負責刪除 tab + 清理 visitHistory
- **5 個 caller 統一遷移** — useShortcuts、hooks.ts、TerminatedPane、WorkspaceSettingsPage、host-lifecycle
- **`destroyBrowserViewIfNeeded` 共用 helper** — 修正 useShortcuts close-tab 缺少 browser view cleanup

### 修正

- **`ws.activeTabId` 同步** — close-tab 後正確同步 workspace 的 activeTabId（修 PR #208 review issue）
- **`ws.activeTabId` 覆寫** — 關閉非 active tab 不再錯誤覆寫 workspace activeTabId
- **host-lifecycle undo 還原 workspace** — cascade delete undo 現在同時還原 tab 的 workspace 歸屬
- **host-lifecycle skipHistory** — cascade delete 不再污染「最近關閉」記錄

### 改善

- **ActivityBar 按鈕尺寸** — 32px → 30px + 容器 px-px，減少側邊欄擠壓

### 測試

- 1045 tests pass / lint clean / build OK

## [1.0.0-alpha.60] - 2026-04-08

URL history dropdown 對齊修正

### 修正

- **dropdown 位置** — URL 歷史下拉選單左邊界對齊 input 左側，不再包含 Globe icon 的空間

## [1.0.0-alpha.59] - 2026-04-08

Tab UX 改善 (PR #209)

### 新功能

- **Rename Session** — tab 右鍵選單新增 Rename Session，popover 出現在 tab 正下方 inline 編輯，API 失敗時顯示錯誤訊息
- **URL 歷史下拉** — new tab 頁面 URL 欄位帶出歷史紀錄，輸入時 auto-filter，鍵盤 ↑↓ 選擇，最多 100 筆持久化
- **Session 鍵盤導航** — Tab 鍵進入 session list，↑↓/jk 移動，Enter 選擇
- **瀏覽紀錄回退** — 關閉 tab 時回到上一個瀏覽的 tab（visitHistory stack），而非相鄰 tab

### 改善

- **New Tab 頁面** — browser URL 欄位移到最上方並自動 focus，移除 Memory Monitor 區段
- **Focus 保持** — 點擊已 active 的 tab 不搶走 content 區域的 focus

### 測試

- 1047 tests pass / lint clean / build OK

## [1.0.0-alpha.58] - 2026-04-08

Tab 操作 workspace 隔離 (PR #208)

### 修正

- **close-tab 跨 workspace 防護** — cmd+w 只能關閉當前 workspace 可見的 tab，空 workspace 時不會誤刪其他 workspace 的 tab
- **post-close scoping** — 關閉後若 closeTab 自動選了其他 workspace 的 tab，重設為當前 workspace 內的 tab 或 null

### 測試

- 1012 tests pass / lint clean
- 新增跨 workspace close-tab 和 reopen-closed-tab 測試

## [1.0.0-alpha.57] - 2026-04-08

Workspace UI 微調 (PR #207)

### 改善

- **Context menu 精簡** — 只保留 Settings，移除 rename/color/icon/delete（已在設定頁可用）
- **Settings sidebar 左邊距** — active 指示線不再緊貼 ActivityBar
- **Icon picker ring 修正** — 選中 icon 的紫色邊框不再被裁切

### 清理

- **移除 workspace 顏色系統** — 刪除 WorkspaceColorPicker、WorkspaceRenameDialog、WorkspaceChip、workspaceColorStyle、WORKSPACE_COLORS 等死碼（7 檔 302 行）
- Workspace interface 移除 `color` 欄位
- 清理 App.tsx ~80 行 dialog state

### 測試

- 1009 tests pass / lint clean

## [1.0.0-alpha.56] - 2026-04-08

Phase 11 — Workspace UI 改善 (PR #201)

### 新功能

- **Workspace 設定頁** — 前台式單頁設定（名稱編輯、Phosphor Icon picker、icon weight toggle、刪除）
- **Phosphor Icons Picker** — 8 分類精選 + 搜尋完整 1512 icon 庫，lazy loading per-icon chunk
- **Icon Weight** — bold / duotone / fill 三種風格切換
- **Empty workspace state** — 切換到無 tab workspace 時顯示空白引導頁
- **WorkspaceContextMenu** — 新增 Settings 項目
- **Vibrant color palette** — S=55-80% L=55-65% 取代舊 S=36% 色板（12 色）

### 改善

- **ActivityBar** — 系統配色（白前景/深背景）、active 紫色背景 + ring-purple-400
- **WorkspaceChip 移除** — tab bar 不再顯示 workspace 標題，上下文完全由 ActivityBar 提供
- **Tab recall fix** — 切換 workspace 時正確回到上次瀏覽的 tab（getState 取代 stale closure）
- **useRouteSync** — workspace-settings 路由補 setActiveWorkspace + insertTab

### Review 修正

- WorkspaceIcon rules-of-hooks — useMemo 移到 early return 前避免條件式 hook 呼叫
- WorkspaceIcon ErrorBoundary — icon import 失敗時 fallback 至文字而非 crash 整個 app
- WorkspaceSettingsPage delete — 濾除 settings tab、記錄 history、sync activeTab
- Context menu 點 Settings 後正確關閉
- 搜尋去重：curated icons 跨 category 重複時不再產生 React key 警告
- 修正無效 Phosphor icon 名稱（Tv → Television, Brackets → BracketsCurly）
- workspaceColorStyle JSDoc 修正

### 測試

- 997 tests pass / lint clean

## [1.0.0-alpha.55] - 2026-04-07

Browser Tab 強化 (PR #200)

### 新功能

- **Browser tab toolbar** — 導航按鈕（← → ↻/✕）、可編輯 URL 欄位、⋯ 更多選單
- **Mini browser 獨立視窗** — Shift+click 連結彈出獨立視窗，共用同一套 BrowserToolbar
- **Terminal 連結統一處理** — click 開新 browser tab、shift+click 開 mini browser（SPA fallback 為 window.open）
- **WebContentsView preload 注入** — 攔截頁面連結點擊，回報 shiftKey 給 main process
- **MiniWindowManager** — 管理 mini browser BrowserWindow 生命週期
- **browser-view-ipc.ts** — 集中管理所有 browser-view IPC handler
- **useBrowserViewState hook** — 訂閱 Electron state-update（URL、title、canGoBack、isLoading）
- **useBrowserViewResize hook** — 共用 ResizeObserver 邏輯
- **link-handler factory** — createLinkHandler 根據 platform + shiftKey 分派
- **URL 正規化** — normalizeUrl utility（自動補 https://、scheme 白名單）
- **Browser tab close → destroy** — 主動關閉走 destroy 路徑，tab 切換走 background

### 測試

- 1011 tests pass / lint clean

## [1.0.0-alpha.54] - 2026-04-07

Phase 10 — Workspace 強化 (PR #189, #190, #191)

### 新功能

- **Workspace 全自由制** — 移除預設 workspace，支援 0 workspace 模式，`activeWorkspaceId` 可為 null
- **Feature module 架構** — workspace 相關程式碼搬遷至 `features/workspace/`（store、hooks、components、lib）
- **insertTab store action** — 原子化操作 + singleton dedup（跨 workspace 移除重複 tab）
- **getVisibleTabIds 共用函式** — 純函式，含 Home mode 支援
- **insertTab 收斂** — 所有 `addTabToWorkspace + setWorkspaceActiveTab` 模式統一為 `insertTab`
- **WorkspaceDeleteDialog** — 刪除確認 UI + tab 勾選清單
- **右鍵選單 + Chip** — WorkspaceContextMenu + Titlebar WorkspaceChip
- **重新命名/顏色/圖示設定** — RenameDialog、ColorPicker、IconPicker
- **Electron 快捷鍵** — ⌘⌥1-9 位置切換 + ⌘⌥↑/↓ 循環切換
- **MigrateTabsDialog** — 首個 workspace 建立時詢問遷移既有 tab
- **Standalone Tabs Home 入口** — ActivityBar 頂部 Home 按鈕

### 修正

- deleteWs 後同步 activeTabId 到新 workspace
- Home 按鈕高亮條件修正
- MigrateTabsDialog Skip 後自動切回 Home
- prev-workspace 從 Home 出發跳到最後一個 workspace

### 測試

- 981 tests pass / lint clean

## [1.0.0-alpha.53] - 2026-04-07

Phase 7.4 — Daemon 品質改善 (PR #196, #133, #130, #131, #121, #134)

### 修正

- **upload delete TOCTOU (#133)** — 移除 `os.Stat` check-then-act，直接 `os.Remove` + `IsNotExist` 判斷
- **send-keys 失敗清理 (#130)** — `SendKeysRaw` 失敗時 `os.Remove(destPath)` 清理孤立檔案
- **dedup filename TOCTOU (#131)** — `deduplicateFilename` 改為 `createDedupFile` 使用 `O_CREATE|O_EXCL` 原子佔檔名
- **settings.json atomic write (#121)** — `mergeHooks` 改用 tmp + `os.Rename` 原子寫入

### 效能

- **session watcher debounce (#134)** — `broadcastSessions()` 加 500ms debounce，防止 wait-for + ticker 重複廣播

### 測試

- 新增 6 個 Go 測試（upload delete 404/success、send-keys fail cleanup、atomic write、debounce、debounce expiry）

## [1.0.0-alpha.52] - 2026-04-07

Phase 7.3 — Refactor 拆檔 (PR #192, #163, #138, #185, #182)

### 重構

- **deleteHostCascade 提取 (#163)** — 從 OverviewSection.tsx 提取 cascade delete + undo 邏輯至 `lib/host-lifecycle.ts`，同時修正原本遺漏的 `models` snapshot/restore
- **form-fields 提取 (#138, #185)** — Section / Field / EditableField / TokenField 提取至 `hosts/form-fields.tsx`，OverviewSection 655→280 行
- **cc_hooks 拆分 (#182)** — handler.go CC hook 邏輯移至 `cc_hooks.go`，與 session/hooks.go 結構對稱，handler.go 227→87 行

### 關閉（無需修復）

- **#184** — deriveStatus 已是獨立 exported pure function，提取無實質改善

### 測試

- Tests: 913 → **914**（新增 agentModels undo 測試）

## [1.0.0-alpha.51] - 2026-04-07

Phase 7.2 — UI 元件修正 (PR #187, #139, #179)

### 修正

- **HostSidebar auto-expand (#139)** — selectedHostId 變更時（如 host 刪除 fallback）自動展開新 host，用 derived state（`||`）取代 useEffect
- **WebGL context loss handler (#179 L1)** — `onContextLoss` → dispose addon → fallback DomRenderer → re-fit，防止 terminal 切離再切回時縮小
- **TabContent visibility:hidden (#179 L2)** — 取代 `left:-9999em` off-screen hack，語意更正確
- **keepAliveCount WebGL cap (#179 L3)** — WebGL 上限 6、DOM 上限 10，DOM→WebGL 切換時 auto-clamp，settings 顯示 hint

### 關閉（無需修復）

- **#140** — 已在 commit `e8cfe7ff` 修復（empty draft 跳過驗證）
- **#157** — storage key 切換早於欄位新增，舊資料已遺棄
- **#176** — 架構上場景不成立（inactive tab off-screen）

### 測試

- Tests: 906 → **912**（新增 HostSidebar auto-expand、TabContent visibility、TerminalSection WebGL cap、WebGL context loss 行為測試）

## [1.0.0-alpha.50] - 2026-04-06

Phase 7.1 — Lint + Agent Store 修正 (PR #183, #175, #105, #92, #93, #94, #126, #124, #110, #169)

### 修正

- **SubagentStop events 保護 (#126)** — SubagentStart/Stop 不再覆寫 events map，只更新 activeSubagents
- **Error 狀態白名單 (#124)** — 只有 UserPromptSubmit/SessionStart/Stop 可清除 error，非白名單事件完全跳過（events + status 都不更新）
- **PermissionRequest i18n (#105)** — 通知 body 改用 `{{tool}}` 參數
- **Notification body (#110)** — permission_prompt/elicitation_dialog 顯示特定 body
- **SubagentDots 同步 (#169)** — negative CSS animation-delay 同步呼吸動畫相位
- **Rename tbox_ → purdex_ (#175)** — user-facing 字串 + `tbox_version` → `purdex_version` API 欄位
- **setState-in-useEffect (#92)** — useReducer、render-time state、direct DOM、ref deps
- **useCallback deps (#93)** — 修復遺漏 deps + justified eslint-disable
- **Lint cleanup (#94)** — fast-refresh、explicit-any、globals-reassign
- **TerminalView retrying** — SM cycle 完成後 `Promise.resolve().finally()` 清除 spinner
- **manualRetry 型別** — `() => void` → `() => Promise<void> | void`

### 改善

- Lint: 31 errors/warnings → **0**
- Tests: 904 → **906**（新增 error guard + events 一致性測試）

## [1.0.0-alpha.49] - 2026-04-06

Phase 6 — Hooks 統一架構 (PR #181, #150, #109, #108, #103, #142, #127)

### 新功能

- **Daemon API 統一** — tmux 和 CC hooks 統一為 `/api/hooks/{module}/status` + `/setup` 路由模式
- **HookModule 介面** — 新增模組化架構，新 hook module 只需加一個 config 物件
- **HookModuleCard 元件** — 通用 card 元件，含安裝/移除按鈕、事件狀態、loading spinner
- **Agent Hooks UI 完成** — CC hooks 從 stub 升級為完整的安裝/移除/狀態/錯誤 UI
- **Model Name 持久化 (#127)** — `models` map 防止 model name 被後續事件覆寫
- **觸發時間顯示 (#142)** — `getLastTrigger` API + 相對時間顯示（剛剛 / Nm ago / Nh ago）

### 修正

- **StatusBar reactivity** — model badge 改用 reactive store selector，確保 hook event 到達後即時更新
- **getLastTrigger 解耦** — 從 lib 層移除 store 依賴，改為純函式 + `useMemo` 穩定引用
- **setup() unmount guard** — `mountedRef` 防止 unmount 後 setState
- **exec.Command timeout** — CC hook setup 加入 30s context timeout
- **Response agent_type 移除** — hook status/setup API 回應不再包含多餘的 `agent_type` 欄位
- **i18n placeholder** — 修正 `{n}` → `{{n}}` 格式

### 改善

- **useModuleHook** — 共用 data-fetching hook，管理 loading/error/status 生命週期
- **死路徑清除** — 移除 `useHookStatus`、`App.tsx` init fetch、`hooksInstalled` 欄位、舊 API 函式
- **測試覆蓋** — 新增 `HookModuleCard.test.tsx`（13 tests）、`useModuleHook.test.ts`（9 tests）、`hooks_test.go`（5 tests）、setup handler tests（4 tests）、reactive tests

### 關閉 Issues

- #150, #109, #108, #103, #142, #127, #114, #64

## [1.0.0-alpha.48] - 2026-04-06

api.ts 舊 API 層缺 auth — 遷移至 hostFetch 統一認證 (PR #180, #177)

### 修正

- **API 層統一認證** — 將 `api.ts` 全部 9 個函式遷移至 `host-api.ts`，改用 `hostFetch` 自動帶 Bearer token，解決 daemon 設 token 後所有 API 呼叫 401 的問題
- **Raw fetch 修正** — `App.tsx`、`useHookStatus.ts` 的 3 處 raw fetch 改用 `fetchAgentHookStatus` / `setupAgentHook`
- **Electron updater auth** — `checkUpdate` / `applyUpdate` 新增 `token?` 參數，透過 IPC 傳遞

### 改善

- **Store 簽名簡化** — `useSessionStore.fetchHost(hostId, base)` → `fetchHost(hostId)`、`useConfigStore.fetch/update(base)` → `fetch/update(hostId)`，消除 caller 傳錯 base URL 的可能
- **Dead code 清理** — 移除 `electron/main.ts` 啟動時的 background update check（`dev:update-available` 無 listener、main process 無法取得 auth token）
- **測試補充** — 新增 `host-api.test.ts`（21 個測試），覆蓋所有遷移函式含 auth header 驗證

### 刪除

- `spa/src/lib/api.ts` — 舊 API 層，已完全由 `host-api.ts` 取代

## [1.0.0-alpha.47] - 2026-04-06

Phase 5b — WS Ticket 統一 + Auth Error UI (PR #168)

### 新功能

- **Negotiation-First 狀態機** — `checkHealth` 升級為兩階段（GET /api/health + POST /api/ws-ticket），同時驗證 daemon reachability 與 token auth
- **WS Ticket 統一** — terminal、stream、host-events 三條 WS 全面使用 ticket auth
- **Auth Error 狀態** — `HostRuntime.status` 新增 `'auth-error'`，狀態機偵測 401/503 後不重試
- **Auth Error UI** — HostSidebar 鎖頭圖示（animate-pulse）、StatusBar 可點擊導航至設定頁、OverviewSection 紅色 banner + Token 自動重試
- **Health Mode 消費（#167）** — AddHostDialog 根據 daemon mode 自動導流（pairing → 配對碼、pending/normal → Token）
- **Per-host diff 更新** — 修改單一 host 的 IP/Port 只重建該 host 連線，不影響其他 host
- **connectHostEvents lazy mode** — WS 不立即連線，等待狀態機 negotiation 完成後由 `reconnectWithTicket` 啟動
- **connectTerminal 雙函式設計** — sync `connect()` + async `connectWithTicket()`，既有同步呼叫端不受影響

### 安全性

- **移除 `?token=` query param fallback** — 消除 token 出現在 URL/log/瀏覽器歷史的風險
- **Token-less host 偵測** — SPA 無 token 時回傳 `auth-error`（daemon pairing mode 除外），避免 WS 被 daemon 401 拒絕後的死循環
- **PairingGuard 503 偵測** — daemon 在 pairing mode 時 ws-ticket 回 503，狀態機正確判定為 auth-error

### 修正

- **狀態機 + Auth 死循環** — health endpoint 不驗 auth → 誤判 connected → WS 靜默失敗。兩階段 negotiation 解決
- **新 host token 錯時靜默失敗** — 狀態機不自動啟動 → 灰色圓圈無回饋。lazy mode + 立即 `sm.trigger()` 解決
- **`startBackground` auth-error guard** — L1 背景重試遇 auth-error 正確停止，不再無限循環
- **`ws.close()`/`send()`/`resize()` 安全性** — async getTicket 期間 ws 未初始化時加 optional chaining
- **Relay 雙重 stream WS** — `pendingFetches` Set 防止快速 reconnect 建立重複 WS
- **`reconnect()`/`reconnectWithTicket()` double-trigger** — `ws.onclose = null` 後再 close，防止 onclose handler 重複觸發

### 關聯 Issues

- #148 pt.2 — Terminal/Stream WS auth（統一用 ticket）
- #148 pt.3 — WS 401 auth error 提示
- #167 — health mode SPA 消費

## [1.0.0-alpha.46] - 2026-04-05

Phase 5a — 配對系統 + Token 認證 (PR #164)

### 新功能

- **Quick 模式** — `tbox serve --quick`：13 碼 Base58 配對碼（IP+Port+Secret 編碼），PairingGuard 攔截非配對 API，verify → setupSecret → setup 三步完成
- **一般模式** — `tbox serve`（無 token）：daemon 產生 `purdex_` runtime token 印到 terminal，client 用 `POST /api/token/auth` 確認後持久化
- **PairingState 狀態機** — pairing/pending/normal 三態，thread-safe（atomic.Int32）
- **SetupSecretStore** — 128-bit one-time secret，5 分鐘 TTL，constant-time 比較
- **TokenAuth getter** — 每請求動態讀取 token，支援 runtime token 變更
- **PairingGuard middleware** — Quick 模式下只放行 `/api/pair/*`，其餘回 503
- **Base58 codec** — `internal/pairing/` 獨立 package，encode/decode 配對碼（9 bytes → 13 chars, 4-4-5 格式）
- **Pairing handlers** — `/api/pair/verify` + `/api/pair/setup`，brute-force 保護（10 次失敗 regenerate）
- **Token auth handler** — `/api/token/auth`，confirm 後持久化到 config.toml
- **`/api/health` mode 欄位** — 回傳 `{"ok":true,"mode":"pairing"|"pending"|"normal"}`
- **SPA AddHostDialog 重寫** — 配對碼 + Token 雙路線，stage state machine，IP:port 去重
- **SPA pairing-codec** — TypeScript Base58 解碼 + `purdex_` token 產生
- **i18n** — 新增 12 個配對相關翻譯 key（en + zh-TW）

### 安全性

- **persist-first** — `handlePairSetup` 先 WriteFile 成功才設 runtime token，失敗不污染 state
- **config.toml 權限** — `WriteFile` 改用 `0600`（原為 `0666`），保護明文 token
- **concurrent verify 序列化** — CfgMu.Lock + TOCTOU guard 防止並發 verify 覆蓋 setupSecret
- **PairingSecret 鎖保護** — CfgMu RLock/Lock 保護讀寫，消除 data race

### 重構

- `internal/core/base58.go` → `internal/pairing/base58.go`（獨立 package）
- `cmd/tbox/main.go` 配對初始化抽取至 `cmd/tbox/quick.go:initPairing()`
- PairingGuard 移除 OPTIONS dead code（CORS 已處理）

## [1.0.0-alpha.45] - 2026-04-04

Phase 4 Hotfix — SM tmuxState 覆寫 + L2 背景重連 + Test Connection 重連

### 修復

- **SM onStateChange 不覆寫 tmuxState** — `checkHealth` 的 tmux 是硬編碼 `unavailable`，不應覆蓋 WS event 推送的正確狀態
- **Test Connection 觸發 SM reconnect** — 成功時呼叫 `manualRetry()` 讓 WS 恢復連線
- **i18n** — 「測試連線」→「嘗試連線」

### 新功能

- **L2 refused 背景重連** — FAST_RETRY 後每 3 秒嘗試連線，3 分鐘後停止。Daemon 重啟後不再需要手動操作

## [1.0.0-alpha.44] - 2026-04-04

Phase 4 錯誤 UI — Terminated Tab + Host Error Display + Cascade Delete (PR #162)

### 新功能

- **Tab 狀態模型** — PaneContent `kind: 'session'` 改名為 `kind: 'tmux-session'`，新增 `terminated?: TerminatedReason` 欄位（event-sourced）
- **TerminatedPane 錯誤頁** — session 關閉 / tmux 重啟 / host 刪除三種情境，顯示對應訊息 + 關閉按鈕 + 跨 host SessionPickerList
- **SessionPickerList** — 列出所有已連線 host 的 session，按 host 分組，可用於 tab 重新綁定
- **deriveTabState** — 從 PaneContent + HostRuntime 推導 tab 顯示狀態（active / reconnecting / terminated）
- **Host 層級 L1-L3 錯誤 UI** — StatusBar / HostSidebar / OverviewSection / SessionsSection 各自顯示連線錯誤狀態
- **useHostConnection hook** — 封裝 ConnectionStateMachine 存取，提供 `manualRetry()` 手動重連
- **Reconnecting overlay 手動重連按鈕** — TerminalView 斷線覆蓋層新增重連按鈕 + spinner
- **Host 刪除 cascade cleanup** — checkbox 選擇是否關閉分頁，多 store cascade（AgentStore.removeHost + StreamStore.clearHost）+ 全域 undo toast（5s snapshot 復原）
- **NotificationAction 模組化** — 通知 click handler 改為 action payload dispatch 模式，支援 `open-session` / `open-host`
- **L2/L3 系統通知** — daemon refused / tmux unavailable 狀態轉換時發送桌面通知

### 重構

- **PaneContent kind rename** — `'session'` → `'tmux-session'`，含 Zustand persist migration v1→v2
- **connectionErrorMessage 共用** — 抽取到 `lib/host-utils.ts`，OverviewSection 和 SessionsSection 共用
- **Undo toast 全域化** — `useUndoToast` store + `GlobalUndoToast` 元件，跨頁面導航持續顯示

### 修復

- **#156** — AddHostDialog 顯示具體 L1/L2/401 錯誤訊息
- **#140** — TokenField 清空時不觸發驗證
- **health timeout** — checkHealth timeout 從 3s 調整為 6s

## [1.0.0-alpha.43] - 2026-04-03

Phase 3 連線偵測 — Watcher 狀態機 + WS ping/pong + useHostConnection 閘控 (PR #158)

### 新功能

- **Watcher 狀態機** — NORMAL/TMUX_DOWN 雙模式，tmux 斷線自動偵測 + broadcast `tmux` event，wait-for goroutine 在 TMUX_DOWN 時暫停
- **WS ping/pong** — host-events WS 加入 30s ping / 10s pong timeout，整合 write pump（one-writer rule）
- **API 三層分離** — `/api/health`（無 auth, liveness）、`/api/ready`（有 auth, readiness + tmux 狀態）、`/api/info`（有 auth, identity）
- **ConnectionStateMachine** — 純 class 重連狀態機，L1 不間斷背景重試 + L2 停止 + epoch counter 防止 stale callback
- **checkHealth** — AbortController 3s timeout，L1（unreachable）/ L2（refused）分類
- **WS 閘控** — host-events WS 停止自身 reconnect 由 SM 管理，terminal WS 受 `canReconnect` gate 閘控
- **HostRuntime 擴充** — 新增 `daemonState`（connected/refused/unreachable）+ `tmuxState`（ok/unavailable）
- **TmuxAlive** — Executor interface 新增 `TmuxAlive() bool`，RealExecutor 用 `tmux info`（5s timeout）

### 重構

- **Rename** — `SessionEvent` → `HostEvent`、`/ws/session-events` → `/ws/host-events`（Go + SPA 全面更新）

### 修復

- **notifyWaitFor drain** — 先清空 channel 再寫入，防止 stale signal 阻塞 resume
- **wstate 封裝** — `updateHash` accessor 取代 tickNormal 直接操作 mutex
- **SM stopped guard** — await 後檢查 stopped + epoch，防止 unmount 後 state mutation
- **reconnect ws close** — 重連前關閉既有 WS，防止 duplicate connection

## [1.0.0-alpha.42] - 2026-04-03

SPA 識別系統整合 — Phase 2b (PR #155)

### 新功能

- **PaneContent 擴充** — session kind 新增 `cachedName`（斷線後仍顯示名稱）+ `tmuxInstance`（偵測 tmux 重啟）
- **Daemon Host ID 整合** — AddHostDialog 連線成功後 fetch `/api/info` 取得 daemon 的 `host_id` 作為 store key
- **cachedName 即時同步** — WS session 更新時自動同步 tab 的 cachedName（rename 即時反映）
- **Tab label fallback 鏈** — live name → cachedName → sessionCode

### 修復

- **addHost dedup** — 重複 daemon `host_id` 不再造成 `hostOrder` 重複
- **updateSessionCache layout 安全** — 使用 `updatePaneInLayout` 取代 hardcoded leaf，保護未來 split pane 結構
- **notification cachedName** — 從 notification 重開 tab 時從 sessionStore 查 name，避免空窗期顯示 sessionCode

### Breaking Changes

- PaneContent session 新增 required 欄位，既有 persisted tabs 資料重置

## [1.0.0-alpha.41] - 2026-04-03

Daemon Host ID 產生 + /api/info 擴充 — Phase 2a (PR #153)

### 新功能

- **Host ID** — Daemon 啟動時產生穩定的 `hostname:6-char-code` 識別碼，持久化到 `config.toml`
- **/api/info 擴充** — 回傳 `host_id`（daemon 自報 ID）+ `tmux_instance`（`pid:startTime`，偵測 tmux server 重啟）
- **config.WriteFile** — 統一的 config 原子寫入函式（取代重複的 `persistConfig` / `writeConfig`）

### 修復

- **HostID rollback** — `EnsureHostID` 持久化失敗時回滾 `cfg.HostID`，避免使用不穩定的 ID
- **PUT /api/config redact** — 回應與 GET 一致，隱藏 `host_id` + `token`

## [1.0.0-alpha.40] - 2026-04-03

Storage 抽象層 + Key 遷移 — Phase 1a (PR #152)

### 新功能

- **Storage 抽象層** — 新增 `spa/src/lib/storage/` 模組，以 Zustand `StateStorage` 介面為基礎建立可替換的 storage backend
- **BrowserBackend** — localStorage 包裝 + BroadcastChannel 跨 tab 狀態同步
- **STORAGE_KEYS 常數** — 11 個 localStorage key 的 single source of truth

### 重構

- **Key 遷移** — 所有 10 個 persist store 的 key 從 `tbox-*` → `purdex-*`，統一使用 `STORAGE_KEYS` 常數
- **移除死碼 migrate** — useHostStore（v0→v1）、useSessionStore（v1→v2）、useTabStore（v1→v2）的 migrate 函式及 `addHostIdToLayout` helper（-92 行）
- **Version 統一** — 所有 store 重設為 `version: 1`

### 修復

- **Rehydration 迴圈** — `browserStorage.setItem` 加入 equality check，防止 `onRehydrateStorage` callback 觸發無限 BroadcastChannel ping-pong
- **BC null guard** — `sync.ts` onmessage 加入型別檢查，防止非預期訊息格式導致 TypeError

### Breaking Changes

- 所有 localStorage key 更名（alpha 階段不向下相容），升級後本地 persist 資料重置

## [1.0.0-alpha.39] - 2026-04-02

Unify session mode naming + remove JSONL mode (PR #151)

### 重構

- **Mode 命名統一** — Daemon 和 SPA 統一使用 `"terminal"` / `"stream"`（原 daemon 回傳 `"term"`，導致 StatusBar 顯示不一致）
- **移除 JSONL session mode** — 移除未使用的 JSONL mode（`JSONLConfig`、jsonl preset、jsonl icon），保留 CC 歷史記錄 `.jsonl` 檔案格式解析
- **DDL schema 同步** — SQLite `DEFAULT 'term'` → `'terminal'`
- **升級遷移** — `ResetStaleModes()` 啟動時自動將舊的 `"term"` / `"jsonl"` 記錄遷移為 `"terminal"`

### 清理

- **刪除 TopBar** — Phase 1 遺留的 deprecated 元件（已被 TabBar + StatusBar 取代，無任何引用）

## [1.0.0-alpha.38] - 2026-03-31

Host Page UI + Multi-Host Integration — Phase 1.6c C+D (PR #136)

### 新功能

- **Host Page** — 完整主機管理頁面（ActivityBar HardDrives 按鈕），含 sidebar accordion 導覽 + 4 個子頁面
- **Overview** — 連線設定（editable name/ip/port/token with validation）、Daemon Config（sizing mode）、System Info
- **Sessions** — Session CRUD table（New/Open/Rename/Delete）+ agent status badge
- **Hooks** — tmux hooks + agent hooks 安裝狀態、install/remove 操作、per-event indicator
- **Uploads** — 暫存檔案按 session 分組、stats 顯示、個別/批次刪除
- **Add Host dialog** — health check → 401 偵測 → token 輸入 onboarding 流程
- **Token editing** — show/hide toggle、儲存前 /api/sessions 驗證、401 error handling
- **Multi-host grouping** — SessionPanel + SessionSection 按 host 分組、single host 時隱藏 header
- **Offline handling** — SortableTab WifiSlash icon、StatusBar 三色狀態、離線 disable
- **ErrorBoundary** — React error boundary 防止全畫面崩潰
- **Electron crash recovery** — render-process-gone 自動重載 SPA

### 修正

- sizing_mode 選項值對齊 daemon（auto/terminal-first/minimal-first）
- isOffline 邏輯統一（runtime undefined = not offline）
- isActiveSession 檢查 activeHostId（multi-host 防衝突）
- getAgentStatus 改用 reactive hook（非 getState）
- StatusIcon grey for undefined runtime
- AddHostDialog stage reset 涵蓋 needs-token/error
- HooksSection schema 對齊 daemon（tmux_hooks map）
- EditableField double-save 防護（savedRef）
- ws?.close() null safety + onerror handler (#137)
- fetchHost unhandled promise .catch() (#137)
- i18n 全面覆蓋（9 hardcoded strings 修正）
- a11y: AddHostDialog aria-modal + Escape、Section aria-expanded、TokenField aria-label
- InlineRename onBlur 防止編輯卡住

### 測試

- 63 個新測試（7 個 test files），734/735 pass

### 追蹤 Issues

- #138 OverviewSection 拆檔
- #139 HostSidebar expanded 同步
- #140 TokenField 清除 UX
- #141 Terminal palette 顯示
- #142 Hook 最後觸發時間
- #143 Upload 暫存目錄編輯
- #144 SessionPanel host header 可收合

## [1.0.0-alpha.37] - 2026-03-31

Agent File Upload — 拖曳檔案上傳到 CC agent（PR #129）

### 新功能

- **Daemon upload endpoint** — `POST /api/agent/upload`，multipart 上傳 → 存到 `~/tmp/tbox-upload/{session}/` → `tmux send-keys` 注入路徑
- **TerminalView drag-drop** — CC agent 活躍時攔截拖曳，逐一上傳逐一注入，drop overlay 提示
- **StatusBar 上傳進度** — uploading（黃色 spinner + 檔名）/ done（綠色勾）/ error（紅色叉，可點擊消除）
- **Agent label badge** — 有 model name 時橘棕色 badge，fallback 白色帶框
- **useUploadStore** — per-session 上傳狀態管理（Zustand，不 persist）
- **i18n** — 上傳相關文字支援 en/zh-TW，含單複數處理

### 安全修復

- **Path traversal 防護** — `filepath.Base()` sanitize 上傳檔名
- **路徑引號包裹** — send-keys 注入路徑以雙引號包裹，處理含空格檔名

### 修正

- **並發 Drop 保護** — `dismiss()` 不清除 uploading 狀態，防止重複拖曳污染 store

### 追蹤 Issues

- #127 StatusBar agent label modelName 被 latest event 覆蓋
- #130 upload send-keys 失敗時清理孤立暫存檔案
- #131 deduplicateFilename TOCTOU race condition

## [1.0.0-alpha.36] - 2026-03-31

Agent Hook 增強 — error 狀態、subagent 追蹤、unread 修正（PR #123）

### 新功能

- **error 狀態** — `StopFailure` 推導為新的 `error` 狀態，紅色燈號（`#ef4444`），觸發 unread 紅點和桌面通知
- **Subagent 追蹤** — 註冊 `SubagentStart`/`SubagentStop` hooks，以 `agent_id` 追蹤 active subagents（ephemeral，不存 DB）
- **SubagentDots 元件** — tab icon 左側顯示 1-3 顆藍色呼吸燈（`#60a5fa`），依 subagent 數量遞減尺寸
- **通知 newline 壓縮** — 彈窗 body 連續換行壓成單個

### 修正

- **Unread 紅點不可見** — 移除 tab `overflow-hidden`，重新定位到右上角框線上（`-top-[4px] -right-[4px]`、`z-20`），不再被 close 按鈕 gradient 遮蔽
- **SessionStart 推導修正** — `startup`/`resume`/`clear` → `idle`（等待輸入），非 `running`
- **shouldNotify 遺漏 error** — `StopFailure` 現在正確觸發桌面通知
- **error 不被 idle Notification 降級** — `idle_prompt`/`auth_success` 不會覆蓋 error 燈號
- **HandoffButton error 支援** — `isAgentActive` 加入 `error`，StopFailure 後 Handoff 按鈕不會 disabled
- **SessionStart(compact) 不清空 subagents** — compact 是工作中途壓縮，subagent 可能還在跑
- **WS 重連清空 subagents** — `onOpen` callback 清除 ephemeral 追蹤，避免斷線後殘留
- **SessionStatusBadge 加 error 顏色** — `bg-red-500`

### 追蹤 Issues

- #124 PermissionRequest 可覆寫 error 狀態
- #125 HandoffButton isAgentActive error 測試
- #126 SubagentStop orphan event 覆寫 events map

## [1.0.0-alpha.35] - 2026-03-30

衍生 focusedSession + tab 切換 auto-focus（PR #122）

### 修正

- **通知在 active tab 仍彈出** — `focusedSession` 從手動同步改為從 `activeTabId` 即時衍生（`getActiveSessionCode()`），鍵盤快捷鍵切 tab 時通知正確抑制
- **隱藏 tab 攔截鍵盤輸入** — 非 active 的 keep-alive tab 加上 `inert` attribute，阻止 offscreen terminal 捕獲 focus
- **Stream tab 切換後自動 focus** — `StreamInput` 加 `focused` prop，切到 stream tab 時 textarea 自動 focus
- **通知點擊 active tab 未清 unread** — `handleNotificationClick` 補 `markRead` 保底

### 重構

- 移除 `focusedSession` / `setFocusedSession` 狀態，改用 cross-store subscription 自動 markRead
- 移除 `useTabWorkspaceActions` 中的手動 focus 同步邏輯

## [1.0.0-alpha.34] - 2026-03-30

防止 tbox setup 重複 hook entries（PR #120）

### 修正

- **setup 防重複 hook** — 從不同路徑執行 `tbox setup`（如 `./tbox` vs `./bin/tbox`）不再累積重複 entries，每次 setup 先清除所有既有 tbox entries 再加入當前路徑
- **`entryIsTbox` 精確比對** — 用 binary basename 邊界檢查（`/tbox"` / `/tbox `）取代寬鬆的 `Contains`，避免誤刪 `tbox-extra` 等第三方工具的 hooks
- **移除死碼** — 清除不再使用的 `hasTboxEntry`、`filterOutTbox`、`entryMatchesTbox`
- **測試 `expectedEvents` 改用 `hookEvents`** — 補齊遺漏的 `StopFailure` 事件覆蓋

## [1.0.0-alpha.33] - 2026-03-30

移除 tab 燈號 fallback（PR #119）

### 修正

- **移除無 snapshot 時的 idle fallback** — 原本 hooksInstalled 為 true 時預設顯示灰色燈號，無法區分「agent 在跑但尚未送出 event」與「沒有 agent」，現在只在收到實際 hook event 後才顯示燈號

## [1.0.0-alpha.32] - 2026-03-30

通知去重 + 視窗焦點感知（PR #118）

### 修正

- **通知持久化去重** — 用 localStorage `lastSeenTs`（Infinity sentinel）取代 in-memory `notifiedRef`，跨重啟不重複通知
- **視窗焦點感知** — 只在 App 視窗有焦點且正看該 tab 時抑制通知，App 在背景時仍發通知
- **SessionEnd 清理** — 清除 `lastSeenTs` 防止 session code 重用時舊 ts 阻擋通知
- 三層 dedup 架構文件化（localStorage → shouldNotify focus → Electron main recentBroadcasts）

## [1.0.0-alpha.31] - 2026-03-30

Hook 引號路徑匹配修復（PR #117）

### 修正

- **`findTboxCommand` 支援引號路徑** — strip `"` 後匹配 `tbox hook`，修復 `hooksInstalled` 在引號路徑下回傳 false 的問題

## [1.0.0-alpha.30] - 2026-03-30

SPA 來源切換 Preflight（PR #116）

### 修正

- **forceLoadSPA preflight** — 切換至 Dev Server 前先驗證可達性（2s timeout + `response.ok`），避免用戶困在錯誤頁
- IPC error 序列化為 plain string（contextBridge 相容）
- 錯誤訊息顯示具體原因 + i18n 化

## [1.0.0-alpha.29] - 2026-03-30

Dev Update 包含 Renderer（PR #115）

### 修正

- **Dev update 包含 renderer** — download tar 現在打包 `out/renderer/`，updater 也替換它，SPA 改動不再需要重裝 `.app`
- Rollback 各目標獨立 try-catch，防止連鎖失敗
- 測試 tar reader 區分 `io.EOF` 和真實錯誤

## [1.0.0-alpha.28] - 2026-03-30

Electron CORS 修復 + SPA 來源切換（PR #113）

### 新增

- **`app://` custom protocol** — bundled SPA 改用 `app://` 取代 `file://`，啟用標準 CORS 行為
- **SPA Source 顯示** — Development Settings 顯示當前 SPA 來源（Dev Server / Bundled）
- **SPA 來源切換** — 一鍵切換 Dev Server ↔ Bundled，即時 reload
- **`forceLoadSPA` IPC** — Electron preload 暴露（`TBOX_DEV_UPDATE` gate 內）

### 修正

- Protocol handler 路徑遍歷防護（`startsWith` 驗證 + 403）
- `forceLoadSPA` 改 async、await `loadURL`、IPC handler return promise
- `spaSource` 偵測改正向匹配 `app:` protocol

## [1.0.0-alpha.27] - 2026-03-30

Agent Hook 子類別狀態判斷（PR #107）

### 新增

- **`deriveStatus` 子類別判斷** — Notification 依 `notification_type`、SessionStart 依 `source` 精確判斷狀態
- **`StopFailure` 事件處理** — daemon 註冊 hook + SPA 狀態映射（idle）+ 桌面通知（error_details）
- **`hooksInstalled` fallback** — hooks 已安裝但尚無事件的 session tab 預設顯示 idle 狀態點
- **CC Hook Event Reference 文件** — `docs/cc-hook-event-reference.md`，完整事件映射與設計決策

### 修正

- `Notification(idle_prompt)` 不再覆蓋 `Stop` 設定的 idle 狀態為 waiting
- `SessionStart(compact)` 背景壓縮不再錯誤觸發 running 狀態
- `shouldNotify` 排除 idle Notification（避免 idle_prompt 重複通知）
- `useNotificationDispatcher` 傳入 `rawEvent` 給 `deriveStatus`（修正通知靜默 regression）
- `useHookStatus` install/remove 後同步 `hooksInstalled` 到全域 store
- `App.tsx` hook-status fetch 加入 `res.ok` 檢查
- 未知 `notification_type` 記錄 console.warn
- Unread dot 排除資訊性 Notification（idle_prompt / auth_success）
- 新增 `notification.fallback.stopFailure` i18n key（en + zh-TW）

## [1.0.0-alpha.26] - 2026-03-29

1.6c-pre2: CC 通知系統（PR #102）

### 新增

- **Electron 系統通知** — Agent hook 事件（waiting + idle）觸發 macOS 系統通知，點擊跳轉對應 tab
- **`agent_type` 欄位** — `tbox hook --agent cc` 識別 agent 類型，daemon 儲存並廣播
- **`broadcast_ts` 持久化** — 多視窗去重 + 防止 WS 重連通知爆發
- **Agent Settings section** — Settings 內新增 Agent 區塊，per-agent 通知開關 + per-event toggle
- **Hook 狀態檢視** — Agent Settings 顯示 hook 安裝狀態 + 一鍵安裝/移除按鈕
- **`GET /api/agent/hook-status`** — 讀取 CC settings.json 回報 hook 安裝狀態
- **`POST /api/agent/hook-setup`** — 執行 `tbox setup` 安裝或移除 hook
- **`useNotificationDispatcher`** — SPA 通知判斷 + Electron/PWA 雙路徑分發
- **`useNotificationSettingsStore`** — Per-agent 通知設定（Zustand + persist）
- **`useHookStatus`** — Hook 狀態查詢 custom hook
- **i18n** — 新增 19 個 Agent 相關 locale key（en + zh-TW）
- **三層級設定預留** — system → host → workspace 覆寫架構（先做 system 層）

### 修正

- `focusedSession` 切到非 session tab 時正確清除
- 通知點擊重開 tab 時正確加入 workspace
- 多視窗通知點擊只聚焦有 tab 的視窗（不閃現所有視窗）

## [1.0.0-alpha.25] - 2026-03-29

Dev Update Auto-Build（PR #96）

### 新增

- **`out/.build-info.json`** — `electron-vite build` 完成後寫入 build metadata（version + hash + timestamp）
- **check/download 一致性** — `check` 端點改讀 `.build-info.json` 作為 build hash，頂層回傳 build hash + `source` 回傳 git hash
- **Auto-build** — source ≠ build 時 daemon 背景自動觸發 `electron-vite build`，回傳 `building: true`
- **Build 失敗退避** — 同一 source hash 失敗後不重複觸發，source 改變才重試
- **Download 建置鎖** — build 進行中 download 回傳 409 Conflict
- **SPA Building 狀態** — 顯示「建置中…」+ 每 3 秒 poll，完成後自動比對

### 修正

- Partial build 不再污染 `.build-info.json`（build 前先刪除）
- `pnpm exec` 取代 `npx`，符合 pnpm-only 規範
- `RemoteInfo` 型別從 `electron.d.ts` derive，消除三處重複定義

### 關閉 Issue

- #78（feat: dev update — auto-build before download）
- #98（refactor: unify RemoteVersionInfo type）

## [1.0.0-alpha.24] - 2026-03-29

Agent Hook 狀態偵測（PR #91）

### 新增

- **`tbox hook` 子命令** — CC hook 觸發時讀取 stdin + tmux session name，POST 到 daemon `/api/agent/event`
- **`tbox setup` 子命令** — 自動配置 `~/.claude/settings.json` hook entries（冪等、支援 `--remove`）
- **Agent module（daemon）** — 純 relay：存 raw event + WS broadcast，不解析 payload
- **AgentEventStore** — 獨立 SQLite 儲存每 session 最近一筆 hook event，新 WS subscriber 自動 snapshot
- **useAgentStore（SPA）** — hook event → running/waiting/idle 狀態機 + unread 管理
- **TabStatusDot** — 三種 tab 指示器樣式（overlay / replace / inline），Settings 可切換，預設 overlay
- **呼吸燈動畫** — running 狀態 `background-color` fade 到 tab 底色
- **未讀紅點** — 5px 暗紅圓點在 inactive tab 右上角
- **Session Panel 燈號** — 狀態 dot 在 code 前方（running/waiting/idle）
- **StatusBar agent 資訊** — 有 agent 時顯示名稱（`getAgentLabel` 集中化）

### 變更

- **移除 CC status poller** — 完全移除 `poller.go`，CC 狀態改為 hook 驅動
- **WS "hook" 事件取代 "status"** — `SessionEvent.type` 不再包含 `'status'`
- **SessionStatusBadge** — 改用 `AgentStatus`（running/waiting/idle），非 agent session 不顯示 badge
- **HandoffButton** — `sessionStatus` prop 改為 `agentStatus`，語意等價
- **useStreamStore** — 移除 `sessionStatus` 欄位，agent 狀態獨立管理

### 修正

- Hook POST 加入 `Authorization: Bearer` header（token 環境下不再 401）
- TabStatusDot running 加 fallback `backgroundColor`（CSS animation 不生效時仍可見）
- `tbox setup` 路徑含空格時加引號、`entryMatchesTbox` 改用 `HasPrefix` 避免誤刪
- 空 `tmux_session` 不存入 DB（避免 garbage row）
- SortableTab 抽出 `renderTabIcon` 消除 pinned/normal 重複邏輯

## [1.0.0-alpha.23] - 2026-03-28

Dev update 進度回饋（PR #85）

### 新增

- **Update 進度顯示** — 點 Update App 後即時顯示 Downloading → Extracting → Applying 各階段
- **錯誤訊息顯示** — 更新失敗時顯示具體錯誤（之前完全無回饋）
- **`dev:update-progress` IPC** — main process 透過 push 事件回報步驟

### 修正

- Error 跨 contextBridge 序列化失敗 — 改在 main process catch 後轉為 string re-throw
- 加入 `updateInProgress` lock 防止重複呼叫 `applyUpdate` 導致檔案競態
- 移除不可達的 `progress('restarting')` 呼叫（app.exit 前 IPC 來不及送達）

## [1.0.0-alpha.22] - 2026-03-28

Electron 快捷鍵系統 + tear-off 修正（PR #84）

### 新增

- **Keybinding registry** — `electron/keybindings.ts` 集中定義快捷鍵，`menuGroup` 分組，為未來自定義擴充預留
- **Electron Menu** — App / File / Edit / Tab / View 五層選單，含快捷鍵提示
- **快捷鍵** — `Cmd+T` 新增分頁、`Cmd+N` 新增視窗、`Cmd+1~9` 切換 tab、`Cmd+Option+←/→` 前後切換、`Cmd+,` Settings、`Cmd+Y` History、`Cmd+Shift+T` 重開 tab
- **useShortcuts hook** — 統一 `shortcut:execute` IPC listener，workspace-aware tab 切換
- **17 個單元測試** — 含 workspace 整合、邊界情況

### 修正

- **Tear-off 帶走所有 tab** — 新視窗從 localStorage persist 恢復出全部 tab。改用 `replace` 旗標，tear-off 時清空再加入
- **reopen-closed-tab 不加入 workspace** — 重開的 tab 現在加入 active workspace
- **prev-tab/next-tab 用全域 tabOrder** — 改用 workspace visible tabs，與 TabBar 顯示一致
- 移除 `App.tsx` 硬編碼 `Cmd+Shift+T`，統一由 Menu accelerator 驅動

## [1.0.0-alpha.21] - 2026-03-27

Dev auto-update system（PR #77）

### 新增

- **Daemon dev module** — `/api/dev/update/check` + `/api/dev/update/download` 端點，`[dev] update = true` config 控制
- **Build hash 注入** — `__APP_VERSION__`、`__ELECTRON_HASH__`、`__SPA_HASH__` 透過 Vite define 編譯時注入
- **Electron updater** — 下載 tar.gz、解壓、備份 + rollback、替換 out/、重啟
- **Settings「Development」section** — 版本資訊 + 檢查更新 / 更新 App / 重新載入 SPA
- **啟動時背景檢查** — main process 啟動後靜默查詢 daemon 有無新版

### 修正

- `devUpdateEnabled` 改由 `TBOX_DEV_UPDATE` 環境變數控制（preload 條件性暴露 IPC）
- 更新流程加入 backup + rollback 防止 partial update 損壞

## [1.0.0-alpha.20] - 2026-03-26

Electron shell — 桌面應用完整實作（PR #76）

### 新增

- **Electron desktop shell** — electron-vite + pnpm workspace monorepo 架構
- **多視窗管理** — tear-off / merge via context menu
- **WebContentsView browser pane** — 生命週期管理（ACTIVE → BACKGROUND → DISCARDED）
- **System tray** — 最小化到系統匣
- **Memory monitor** — process metrics 監控頁面

## [1.0.0-alpha.19] - 2026-03-26

PWA + Platform capabilities + Browser pane（PR #75）

### 新增

- **PWA installability** — manifest.json + icons（192/512/maskable）+ Apple meta tags
- **Platform capabilities** — `getPlatformCapabilities()` + ambient `electron.d.ts` 型別宣告
- **PaneContent `browser` kind** — labels + route mapping + i18n keys
- **NewTabProvider disabled 支援** — disabled provider 顯示說明文字

## [1.0.0-alpha.18] - 2026-03-25

i18n 系統 — 自建多語系 + 自訂語系 + 編輯器/匯入（PR #72）

### 新增

- **Locale Registry** — Map-based，與 Theme Registry 同架構
- **useI18nStore** — `t(key, params?)` 翻譯函式，persist `tbox-i18n`
- **Fallback chain** — active locale → en → key itself
- **LocaleEditor / LocaleImportModal** — fork builtin → 編輯 → 另存
- **locale-completeness.test.ts** — en/zh-TW key 完全對稱守門測試

## [1.0.0-alpha.17] - 2026-03-25

Theme 系統 — 多主題 + 自訂主題 + 匯入匯出（PR #71）

### 新增

- **23 語義 CSS token** — Tailwind v4 `@theme` + CSS Variables，分 6 組
- **4 預設主題** — Dark / Light / Nord / Dracula
- **Theme Registry** — Map-based + Zustand Theme Store（localStorage persist）
- **ThemeEditor** — 即時預覽 + fork / export / import
- **ThemeInjector** — runtime 注入自訂主題 `<style>`

## [1.0.0-alpha.16] - 2026-03-24

Settings UI — VSCode 風格 sidebar + content pane（PR #70）

### 新增

- **Settings pane** — 取代 overlay，以 singleton tab 呈現
- **Settings Section Registry** — 動態註冊，新增 section 只需 2 檔
- **通用元件** — SegmentControl / ToggleSwitch / SettingItem
- **Appearance section** — Theme / Language（disabled, 待 Phase 2/3）
- **Terminal section** — Renderer / Keep-alive / Reveal Delay

## [1.0.0-alpha.15] - 2026-03-24

Tab/Session 解耦 + Pane 模型 + wouter 路由（PR #69）

### 新增

- **Tab/Session 解耦** — Tab 從 Session 1:1 容器改為通用容器
- **Pane 模型** — PaneLayout tree + PaneContent discriminated union（new-tab / session / dashboard / history / settings）
- **Pane Registry** — `registerPaneRenderer(kind, { component })`
- **NewTab Provider Registry** — 可擴充的 content picker
- **wouter 路由** — hash → path-based（`/t/:tabId/:mode`、`/w/:workspaceId`）
- **useRouteSync** — 雙向路由同步（Tab ↔ URL）

## [1.0.0-alpha.14] - 2026-03-24

整合 CC + Stream modules，刪除 legacy server（PR #68）

### 變更

- **Module 整合** — `cc.New()` + `stream.New()` 接入 main.go
- **Legacy 清除** — 刪除 `internal/server/`（18 檔、~4600 LOC）+ legacy store + migration
- **Session code 統一** — SPA 全面從 session name 改用 session code

## [1.0.0-alpha.13] - 2026-03-23

Phase 1.6b Tasks 9-10 — Stream module（PR #66）

### 新增

- **Stream module** — relay WS 管理、SPA subscriber fan-out、handoff 編排
- **Bridge 改用 session code** 作為 key
- **Handoff 改用 CCOperator methods** — 取代重複的 raw tmux send-keys

## [1.0.0-alpha.12] - 2026-03-23

Phase 1.6b Tasks 1-8 — Core 擴充 + CC module（PR #65）

### 新增

- **Core 擴充** — Module `Dependencies()` + `Stop(ctx)`、Kahn's algorithm 拓撲排序
- **EventsBroadcaster** — fire-and-forget + OnSubscribe snapshot
- **Config handler** — OnConfigChange callback
- **CC module** — CCDetector + CCOperator + CCHistoryProvider + Status Poller
- **Middleware 搬遷** — `internal/middleware/` 獨立 package

## [1.0.0-alpha.11] - 2026-03-22

Phase 1.6b Part 1 — Core 擴充 + CC module 基礎（PR #63）

### 新增

- Core layer 擴充基礎建設
- CC module 初始結構（後續 PR #65 完成）

## [1.0.0-alpha.10] - 2026-03-22

修復 MetaStore 冗餘寫入（PR #60）

### 修正

- Handoff Step 8 移除 `SetMeta` 後重複的 `UpdateMeta`
- `MigrateFromLegacy` 錯誤改為 log 輸出

## [1.0.0-alpha.9] - 2026-03-22

Phase 1.6a — Daemon Module 架構 + Session 重設計（PR #59）

### 新增

- **Module 架構** — Core + ServiceRegistry + Module interface，支援可插拔模組
- **Session 重設計** — tmux 為 SOT，DB 降級為 meta cache
- **Session ID 編碼** — 6 碼 base36 code（multiplicative cipher）

## [1.0.0-alpha.8] - 2026-03-22

修復 Tab 拖曳右邊界（PR #58）

### 修正

- Tab 拖曳右邊界限制在最後一個 tab，不再進入 + 按鈕區域

## [1.0.0-alpha.7] - 2026-03-22

Pin/Lock 獨立化（PR #57）

### 變更

- Pin 和 Lock 解耦為獨立旗標：pin 只負責定位，lock 只負責擋關閉
- Pinned tab 可被關閉（除非同時 locked）
- Reopen 恢復 pinned 狀態

## [1.0.0-alpha.6] - 2026-03-21

Tab 互動強化 — 拖曳排序 + 溢出箭頭 + 右鍵選單（PR #56）

### 新增

- **拖曳排序** — @dnd-kit 雙區（pinned / normal）+ restrictToTabZone modifier
- **溢出箭頭** — tab 超出可視範圍時顯示左右捲動按鈕
- **右鍵選單** — Pin / Lock / Close / Close Others
- **中鍵關閉** — 中鍵點擊 tab 關閉

## [1.0.0-alpha.5] - 2026-03-20

Phase 1.5 Task 2 — TerminalView 拆分 + Keep-Alive（PR #55）

### 新增

- **TerminalView 拆分** — 222 → 79 行，抽出 `useTerminal` + `useTerminalWs` hooks
- **useTabAlivePool** — LRU keep-alive pool 管理
- **TabContent pool 渲染** — `display: none` 隱藏非活躍 tab

## [1.0.0-alpha.4] - 2026-03-20

Phase 1.5 Task 1 — Tab 模型擴充（PR #54）

### 新增

- Tab interface 加入 `pinned` / `locked` 欄位
- useTabStore 新增 pin / unpin / lock / unlock 方法
- `removeTab` / `dismissTab` 加入 locked guard

## [1.0.0-alpha.3] - 2026-03-20

Phase 1.1 — Tab 模型修正 + view toggle（PR #48）

### 變更

- Tab 模型從封閉 union 改為開放式 `type: string` + `viewMode` + `data` bag
- 新增 Tab Renderer Registry
- 還原 v0 的檢視/handoff 分離設計

## [1.0.0-alpha.2] - 2026-03-20

xterm addons + terminal renderer toggle（PR #47）

### 新增

- **@xterm/addon-unicode11** — CJK 字元寬度支援
- **@xterm/addon-web-links** — 可點擊 URL
- **Terminal 渲染器切換** — WebGL / DOM 下拉選單

## [1.0.0-alpha.1] - 2026-03-20

Phase 1: 分頁系統 + Activity Bar — SPA 架構從「單 session 檢視」升級為「多分頁 + 工作區」

### 新增

- **Tab 系統** — 每個 tmux session 自動對應一個 tab，支援 terminal / stream / editor 三種類型
- **ActivityBar** — 左側垂直工作區切換列（Workspace icons + standalone tabs + 設定入口）
- **TabBar** — 水平分頁列（切換 / 關閉 / 新增 / dirty indicator）
- **TabContent** — 只掛載 activeTab，切換即銷毀重建（keep-alive 因 tmux resize corruption + WebGL 耗盡移除）
- **StatusBar** — 底部狀態列（host / session / mode）
- **SessionPicker** — Session 選擇 popover（搜尋 + 已開啟標記）
- **useTabStore** — Tab CRUD + `dismissTab` 防止關閉的 tab 被 auto-sync 復活 + localStorage 持久化
- **useWorkspaceStore** — Workspace 管理 + tab 歸屬 + per-workspace activeTab
- **useHostStore** — 取代 hardcoded daemonBase（最小版，Phase 6 擴充為多主機）
- **useUISettingsStore** — 前端 UI 設定（terminalRevealDelay 300ms + terminalRenderer webgl/dom）
- **useIsMobile** — 響應式 breakpoint hook（768px）
- **Hash routing** — `#/tab/{tabId}` 格式，支援 back/forward + 重整後保留
- **App.tsx 重構** — 提取 `useSessionEventWs`、`useSessionTabSync`、`useHashRouting` 三個 custom hooks（345→247 行）
- **xterm.js addons** — `@xterm/addon-unicode11`（CJK 字元寬度）+ `@xterm/addon-web-links`（可點擊 URL）
- **Terminal 渲染器切換** — Settings 新增 WebGL / DOM 下拉選單，變更後自動重連

### 修正

- **crypto.randomUUID fallback** — 非 localhost HTTP context 無法使用，加了 Date.now + Math.random fallback
- **Terminal reveal delay 設定化** — 從 hardcoded 300ms 改為 `useUISettingsStore` 可調整，用 ref + subscribe 避免設定變更觸發 terminal 重建
- **Reconnect overlay 回歸修復** — 恢復 `if (revealed) setReady(true)` 讓 WS 重連後立即顯示 terminal
- **Stale tab 清理** — sessions 消失時自動移除對應 tab（guard `sessions.length > 0` 防止初始渲染清空）
- **Subscribe 洩漏修復** — TerminalView 的 Zustand subscribe 移入 useEffect + cleanup
- **Lint + type errors 全面修正** — 移除 `as any`、修正 SessionStatus type、補 missing fields

### 已知限制

- keep-alive 已移除，每次切 tab 都重新建立 terminal WS 連線（TerminalView 的 `visible` 路徑保留供未來 LRU 快取）
- StatusBar 狀態固定顯示 'connected'（未接 relayStatus/sessionStatus）
- TopBar 標記 @deprecated 但未刪除
- useIsMobile hook 已建立但未在任何元件中使用（Phase 7b）

## [0.5.4] - 2026-03-19

修復 handoff 相關的 terminal resize 與 copy-mode 問題

### 修復

- **Handoff 後 tmux 自動 resize 恢復** — `tmux resize-window -x 80 -y 24`（handoff step 3.5）會讓 tmux 進入手動尺寸模式，導致回到 term 後 window 卡在 80x24 不隨瀏覽器 viewport 調整。handoff 完成 `/status` 擷取後立即呼叫 `resize-window -A` 清除手動旗標
- **Handoff 前退出 tmux copy-mode** — terminal 處於 copy-mode（捲動瀏覽歷史）時 handoff 會失敗。改用 `tmux send-keys -X cancel` 取代依賴 `Escape`，不受 vi/emacs mode 影響且不送按鍵到底層應用

### 新增

- **`[terminal] auto_resize` 設定** — 預設啟用，每次 terminal WS 連線時自動清除手動尺寸旗標。使用者可設 `auto_resize = false` 停用
- **`Executor.ResizeWindowAuto`** — 封裝 `tmux resize-window -A`
- **`Relay.OnStart` callback** — PTY 啟動後的 hook，用於 terminal 連線時重設視窗尺寸

## [0.5.2] - 2026-03-19

架構重構：Stream UI 狀態改由 server-derived relayStatus 驅動

### 重構

- **ConversationView 改用 relayStatus** — 不再依賴 ephemeral `handoffState`，改為 `relayStatus[session]` 作為 single source of truth。Page refresh / WS 重連後自動恢復 stream UI 狀態
- **移除 handoffState** — store 中的 `HandoffState` type、`handoffState` map、`setHandoffState` action 全部移除
- **HandoffButton 簡化** — props 從 `state: HandoffState` 改為 `inProgress: boolean`

### 新增

- **TerminalView `visible` prop** — 切回 term tab 時自動 refit + resize，用遮罩擋住 500ms 等待 tmux 調整完畢再 fadeout

### 修復

- **Handoff 前退出 copy-mode** — 發送 Escape + C-u 退出 tmux 捲動模式並清空輸入，避免 send-keys 注入失敗
- **Handoff Escape error check** — SendKeysRaw(Escape) 失敗時提早返回

## [0.5.1] - 2026-03-18

Bugfix: Handoff tmux target、pane resize、xterm.js 選取

### 修復

- **Handoff tmux target 解析** — 所有 tmux 操作改用 `sess.TmuxTarget`（session:window 格式），避免 bare session name 被 tmux 模糊解析到錯誤的 pane
- **Handoff pane resize** — xterm.js 在 `display:none` 時 PTY 尺寸過小（10x5），tmux smallest-client 規則縮小 pane，`/status` TUI 渲染錯亂。relay PTY 預設 80x24，handoff 前檢查 pane 尺寸並 resize window
- **xterm.js 文字選取** — 啟用 `macOptionClickForcesSelection` 和 `rightClickSelectsWord`，抑制 terminal container 的右鍵選單

## [0.5.0] - 2026-03-18

Stream Message UI — 完整渲染所有 CC 訊息類型

### 新增

- **ThinkingBlock** — 可摺疊的 thinking 區塊（Brain icon，collapsed by default）
- **ToolResultBlock** — 可摺疊的 tool result 顯示（CheckCircle/XCircle 區分成功/錯誤）
- **Slash command 氣泡** — `/exit`、`/status` 等指令以黃棕色氣泡顯示（TerminalWindow bold icon）
- **Interrupted 提示** — 中斷訊息靠左紅棕色顯示（Prohibit icon）
- **@tailwindcss/typography** — 啟用 prose markdown 樣式

### 修改

- **MessageBubble** — User: 藍色氣泡靠右；Assistant: 移除氣泡，直接 prose markdown 輸出（Cowork 風格）
- **ToolCallBlock** — 統一 Wrench icon（移除 per-tool icons），新增 Agent/Grep/Glob summary
- **ConversationView** — 接線所有 content block 類型（thinking、tool_use、tool_result、text、command、interrupted）

### 修復

- **ParseJSONL 過濾 CC 內部標記** — 跳過 `isMeta`、`<local-command-caveat>`、`<local-command-stdout>`、`<synthetic>` assistant；解析 `<command-name>` 為乾淨文字
- **aria-expanded** — 所有可摺疊元件加入無障礙屬性

## [0.4.2] - 2026-03-18

Bugfix: 從 CC `/status` 取得 cwd，修復空 cwd session 的歷史載入

### 新增

- **`detect.ExtractStatusInfo`** — 從 CC `/status` 同時解析 Session ID 和 cwd
- **`store.SessionUpdate.Cwd`** — 支援更新 session 的 cwd 欄位

### 修復

- **空 cwd 導致歷史載入失敗** — auto-scan 使用 `#{session_path}` 取得 cwd，但部分 tmux session 該值為空，導致 history handler 無法定位 JSONL 檔案。改為在 handoff 時從 CC `/status` 輸出取得 cwd 並寫入 DB
- **cwdRegex 空白行誤匹配** — `cwd:` 行僅含空白時不再匹配為有效路徑

## [0.4.1] - 2026-03-18

Bugfix: Handoff 狀態管理修正

### 修復

- **Stream→Term handoff 後 stream 頁面狀態錯誤** — handoff 完成後 `handoffState` 錯留在 `'connected'`，切回 stream tab 時顯示無法互動的對話 UI 而非 HandoffButton。現在根據 session mode 判斷，term handoff 後正確重置為 `'idle'`
- **Term→Stream handoff 載入對話歷史** — `fetchSessions` 改為 await，確保用 fresh session data（含 `cc_session_id`）取得歷史。同時移除 `msgs.length > 0` 條件，空歷史也正確覆蓋避免舊 messages 殘留
- **Relay 關閉時的誤觸事件** — `runHandoffToTerm` 在關閉 relay 前先更新 DB mode 為 `"term"`，防止 `revertModeOnRelayDisconnect` 發送假的 `"failed:relay disconnected"` 事件
- **Handoff 失敗後的 mode rollback** — `runHandoffToTerm` 的 pre-update 在後續步驟失敗時會 rollback mode 到原始值，避免留下不一致的 DB 狀態
- **Term handoff 後清理 per-session state** — 切回 term 時呼叫 `clearSession` 清除上一輪 stream 的 messages、cost、sessionInfo
- **fetchSessions 失敗時的 fallback** — 從 `'connected'`（可能導致無法互動的 UI）改為 `'idle'`（安全預設，顯示 HandoffButton 讓使用者重試）

## [0.4.0] - 2026-03-18

Phase 2.5b: Stream WS Lifecycle Redesign — 修復 stream 訊息不通的根因

### 新增

- **Per-session store** — `useStreamStore` 從全域單例改為 `Record<string, PerSessionState>`，切換 session 不再丟失對話
- **useRelayWsManager hook** — relay 事件驅動 WS 生命週期（relay:connected → 建立 WS，relay:disconnected → 關閉 WS）
- **Relay 事件廣播** — session-events WS 新增 `relay` 事件類型 + snapshot，冷啟動單一資料源
- **Init metadata 攔截** — bridge handler 捕獲 CC init message 的 model 資訊存 DB
- **JSONL history API** — `GET /api/sessions/{id}/history` 讀取 CC 的 JSONL 檔案，resume 時顯示之前的對話
- **SessionResponse DTO** — session list API 回傳 `has_relay` + `cc_model`
- **`cc_model` DB 欄位** — sessions 表新增 cc_model 欄位 + migration
- **`GetSessionByName`** — store 新增 O(1) name 查詢方法
- **`RelaySessionNames`** — bridge 新增列舉所有有 relay 的 session 方法
- **`fetchHistory`** — SPA API client 新增歷史訊息查詢函式

### 修復

- **幽靈連線根因修復** — ConversationView 不再管理 WS 連線，改為純 UI 元件從 per-session store 讀取狀態
- **WS 生命週期脫鉤** — WS 建立/銷毀完全由 relay 事件驅動，不再依賴 component mount 時機
- **set() 內 side effect** — clearSession 的 conn.close() 移到 set() 外避免 re-entrant mutation
- **selector 穩定性** — 使用 stable 空陣列常數避免 Zustand `?? []` 造成的無限 render loop
- **subscribeWithSelector** — store 加入 Zustand middleware 支援 relay status 訂閱

### 改善

- ConversationView props 簡化為 `sessionName`（移除 `wsUrl`、`sessionStatus`）
- session-events type 擴充為 `'status' | 'handoff' | 'relay'`
- bridge 測試恢復 4 個被刪除的單元測試

## [0.3.0] - 2026-03-18

Phase 2.5a: Stream Handoff — 雙向切換

### 新增

- **Stream Handoff** — term（互動式 CC）與 stream（`-p` 串流模式）之間的雙向 handoff
- **SendKeysRaw** — tmux 控制鍵注入（C-u, C-c, Escape 不帶 Enter）
- **ExtractSessionID** — 解析 CC `/status` 輸出的 Session ID（UUID regex）
- **cc_session_id** — sessions 表新欄位 + migration + CRUD
- **Handoff 8 步流程** — CC 偵測 → 中斷 → `/status` 取 ID → `/exit` 退出 → relay `--resume`
- **Handoff to Term** — 6 步反向 handoff（shutdown relay → shell → `claude --resume`）
- **HandoffButton** — CC 狀態感知、進度標籤、disabled 狀態
- **StreamInput "Handoff to Term"** — 底部操作按鈕
- **E2E pipeline 測試** — SPA→bridge→relay→subprocess→bridge→SPA 完整往返驗證
- **Relay 斷線自動 revert** — session mode 自動回 term
- **session-events snapshot** — 新 subscriber 收到初始狀態快照

### 修復

- 混合式 CC 偵測（子程序樹 + pane content fallback）
- relay command 使用 config bind address
- `--verbose` 加入 stream-json preset（CC 2.1.77+ 要求）

## [0.2.0] - 2026-03-17

Phase 2: Stream 模式 — Claude Code 結構化互動

### 新增

- **StreamManager** — `claude -p` 子程序生命週期管理（spawn / stop / pub-sub stdout）
- **WebSocket `/ws/stream/{session}`** — 雙向 NDJSON 中繼（write mutex 保護）
- **Mode Switch API** — `POST /api/sessions/{id}/mode`（term ↔ stream 切換）
- **Store.GetSession** — 單一 session 查詢
- **ConversationView** — 結構化對話渲染（markdown / 程式碼高亮 / 自動捲動）
- **MessageBubble** — user / assistant 訊息氣泡（react-markdown + rehype-highlight）
- **ToolCallBlock** — 可摺疊工具呼叫區塊（工具圖示 + 摘要）
- **PermissionPrompt** — Allow / Deny 按鈕（`can_use_tool` control_request）
- **AskUserQuestion** — radio / checkbox 選項（支援完整 protocol 格式）
- **StreamInput** — 底部訊息輸入框
- **TopBar** — session 名稱 + 三模式按鈕（term / jsonl / stream）+ Stop 按鈕
- **SessionPanel 更新** — Phosphor Icons 狀態燈號 + 底部 Settings 入口
- **useStreamStore** — stream 模式狀態管理（messages / control requests / cost）
- **stream-ws** — 訊息型別定義 + WebSocket 連線管理（含 interrupt / sendControlResponse）

### 修復

- StreamSession：readLoop 中呼叫 cmd.Wait()（防止 zombie process）
- StreamSession：Unsubscribe 關閉 channel（防止 goroutine 洩漏）
- StreamSession：Send 使用 Lock（防止 stdin write race）
- Delete handler：同時停止 stream session（防止子程序洩漏）
- SwitchMode：UpdateSession 錯誤處理 + 回滾
- main.go：st.Close() 改用 defer 保護
- switchMode API：POST 方法（修正 PUT → POST）
- isStreaming 語意：僅在使用者送訊息時啟用（非 WebSocket open）
- AskUserQuestion：回應格式符合 STREAM_JSON_PROTOCOL（含 questions + answers）
- window.__streamConn hack 改為 Zustand store 管理
- clear() 同時重置 sessionId / model
- ConversationView handlers 用 useCallback memoize

### 改善

- TopBar 三模式按鈕（term / jsonl / stream）各自 active 樣式
- TopBar 底色提亮
- Settings 文字亮度對齊 SESSIONS 標題

## [0.1.1] - 2026-03-17

### 修復

- Terminal relay 生命週期：goroutine 互相取消，防止無限 block
- WebSocket write race condition：加入 mutex 保護
- Token 認證改用 constant-time 比較，防止 timing attack
- Token 認證支援 `?token=` query param（WebSocket 無法送 header）
- Session 建立失敗時 rollback tmux session（防止孤立 session）
- Delete handler 正確處理 ListSessions 錯誤
- Batcher 釋放 mutex 後再呼叫 onFlush（防止 deadlock）
- UpdateSession / UpdateGroup 正確回傳錯誤和 ErrNotFound
- Session name 驗證（`^[a-zA-Z0-9_-]+$`）

### 改善

- 自動掃描主機上既有的 tmux sessions（不需手動透過 API 建立）
- Terminal 全寬高顯示（修正 flex layout + 初始 resize 時序）
- WebSocket 連線後送出初始 resize（防止 tmux 按 80x24 渲染）
- URL encode session 名稱（支援空格、中文等特殊字元）
- Loading overlay 帶呼吸動畫，收到資料後 300ms fade out
- Session 按鈕加 cursor pointer + 切換後自動 focus terminal
- Sidebar 文字亮度提升
- Zustand persist 只存 activeId（避免快取過期 session 資料）
- WebSocket onmessage 加 ArrayBuffer type guard
- ResizeObserver 用 requestAnimationFrame debounce
- ws.ts 修正 onerror + onclose 重複觸發 onClose

## [0.1.0] - 2026-03-17

Phase 1: Daemon 基礎 + Terminal 模式

### 新增

- **tbox daemon** — Go HTTP + WebSocket API server
  - Config 載入（TOML，自動讀取 `~/.config/tbox/config.toml`）
  - SQLite 持久化（sessions / groups CRUD）
  - tmux session 管理（建立 / 刪除 / 列出）
  - Terminal relay（WebSocket ↔ PTY 雙向中繼，含 resize）
  - DataBatcher（16ms / 64KB 輸出批次化）
  - 安全：IP 白名單（CIDR）、token 認證（constant-time 比較）、CORS
  - Session name 驗證（`^[a-zA-Z0-9_-]+$`）
  - Graceful shutdown（SIGTERM / SIGINT）

- **tbox spa** — React SPA（獨立部署）
  - Session 面板（左側選單，模式圖示，active 高亮）
  - Terminal 畫面（xterm.js + WebGL + FitAddon + resize）
  - API client（可設定 daemon base URL）
  - Session store（Zustand + localStorage 持久化）

### 架構

- Daemon 和 SPA 完全分離部署
- Daemon 是純 API server，不含前端檔案
- SPA 可封裝為 Electron 或放在獨立主機上 serve

### 技術棧

- Daemon: Go / net/http / gorilla/websocket / creack/pty / modernc.org/sqlite / BurntSushi/toml
- SPA: React 19 / Vite / xterm.js / Zustand / Tailwind CSS / Vitest

# Changelog

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

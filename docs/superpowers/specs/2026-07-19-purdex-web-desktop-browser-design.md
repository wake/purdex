# Purdex Web（桌面瀏覽器版）— 設計

> 讓現有 SPA 能在**純瀏覽器**（無 Electron 外殼）中完整運作，並可透過 daemon 自託管、掛在 `purdex.mlab.host` 上使用。
> 北極星是產品化的 `desk.purdex.app` hosted client + daemon；本 spec 只做**桌面瀏覽器**的地基與功能對齊（子專案 1+2），刻意不含手機/responsive、不含 Web Push/離線。

---

## 1. 動機

目前主力使用路徑是 **Purdex Electron app → Tailscale → mlab daemon（`100.64.0.2:7860`）**。當使用者不在桌面（例如手機、或臨時用別台機器的瀏覽器）時完全無法使用。

需求是一個 `https://purdex.mlab.host/` 的 web 版本，讓純瀏覽器就能連上 daemon 操作 session。但這**不能是拋棄式的一次性開發**——北極星設在產品化（`desk.purdex.app` hosted web client + daemon）。因此本 spec 的每一步都必須是北極星的地基，而非繞路。

### 1.1 現況：架構本來就接近 web-ready

盤點現有程式碼，核心早已 origin 無關：

- **daemon middleware chain**（`cmd/pdx/main.go:193-205`）：`CORS(*)` → `IPWhitelist` → `PairingGuard` → `TokenAuth(+tickets)`。跨 origin 瀏覽器存取在 HTTP 層已通。
- **三個 WebSocket 端點皆 `CheckOrigin: true`**（`internal/core/events.go:177`、`internal/terminal/relay.go:19`、`internal/module/stream/handler.go:17`）。
- **SPA 本就是多 host client**：透過可設定的 host + WS + **ticket auth** 連 daemon（`useHostStore` / `internal/core/ticket.go`）。
- **FS 有三個 backend**（`spa/src/lib/register-modules/fs-backends.tsx`）：`inapp`（IndexedDB）、`daemon`（遠端 host 檔案走 daemon HTTP）、`local`（僅 Electron IPC）。前兩者瀏覽器可用。
- **平台能力已抽象**：`getPlatformCapabilities()`（`spa/src/lib/platform.ts`）以 `isElectron` 統一 gate 原生功能。
- **通知已內建 Web fallback**：`useNotificationDispatcher.ts:269` 在非 Electron 時走 `new Notification()`。

缺的兩塊：

1. daemon **不託管 SPA**（無 static file server / embed）。
2. 部分功能只綁 Electron，web 需 fallback 或優雅降級（見 §4 盤點）。

---

## 2. 目標與非目標

### 2.1 目標

- daemon 能自託管 build 好的 SPA，`https://purdex.mlab.host/` 純瀏覽器打開即可用。
- 核心流程（選 host → 看 session → terminal/stream 互動 → 開檔）在桌面瀏覽器完整可用。
- 所有 Electron-only 功能在 web 中**乾淨降級**（隱藏而非壞掉）。
- 提供**可安裝的 PWA 殼**（standalone 視窗），體感貼近 Electron。
- 全程守住 origin 無關、顯式 host+ticket 連線，使成果直接成為 `desk.purdex.app` hosted client 的地基。

### 2.2 非目標（明確不含）

- **手機 / responsive / 觸控**（Tier D）— 另立子專案。
- **Web Push（背景推播）** — 需 VAPID + 推播服務 + daemon push sender，另議。
- **離線資料** — app 本質是 daemon 的 live client，離線無意義；SW 僅做 network-first 殼載入。
- **`desk.purdex.app` hosted 部署 / BYO-daemon pairing UX**（子專案 3）— 本 spec 只確保「不繞路」，不實作。
- **token/pairing 認證流程** — 本輪 `purdex.mlab.host` 走 Tailscale/IP whitelist 網路層保護，token 維持預設關（但 §5.2 的 serving 設計不得阻擋未來開啟 token）。

---

## 3. 架構決策

### 3.1 拓撲：daemon 自託管 SPA（同源），origin 無關不走捷徑

`purdex.mlab.host` 由 Herd/valet **proxy** 反向代理到本分支的 daemon（`:7860`），TLS 走既有 `*.mlab.host` wildcard 憑證。daemon 在 `/` 提供 build 好的 SPA。

雖然是同源，但**連線模型維持顯式 host + ticket、origin 無關**——這是防繞路的核心約束（§6）。同源只當作「靜態檔從哪送出來」的部署選擇，不當作簡化 auth 的藉口。

### 3.2 為何不做 hosted BYO-daemon（子專案 3）先行

`desk.purdex.app` hosted client 的價值是「一個公開網址」，但它沒有省掉真正的門檻——瀏覽器連任何 daemon 都需要 daemon 有對外有效 TLS 憑證的 `wss://`。既然 TLS 門檻無論如何都在，先做同源自託管（delta 最小、消掉整類跨 origin/mixed-content/cookie 坑），而 client 端程式碼與 hosted 版**完全共用**，故子專案 3 之後疊加即可，非繞路。

---

## 4. 現有功能 → web 難度盤點

### 🟢 Tier A — 已能在瀏覽器直接跑（零/近零工）

Terminal / Stream / JSONL、Sessions 清單 / Dashboard / New Tab、Hosts 多 host 連線、History、Files/Editor（`daemon` + `inapp` backend）、Image/PDF preview、Settings 多數頁、Quick Commands、Workspace Snapshot、Sync。

> 唯一差別：Files/Editor 的 Electron 本機 `local` backend 在 web 消失；但主用法走 `daemon` backend（mlab host 上的檔案），不受影響。

### 🟡 Tier B — 有 Electron 依賴，fallback 已存在或很輕（P3 處理）

| 功能 | 處理 | 難度 |
|---|---|---|
| 通知 | Web Notification fallback 已在 code；補「請求權限」UX + click 導向 | LOW |
| 開外部連結（terminal link） | `electronAPI.openExternalUrl` → `window.open` | LOW |
| `focusMyWindow` | `window.focus()` / no-op | TRIVIAL |
| App 鍵盤快捷鍵 | 目前只靠 Electron `onShortcut` IPC（`useShortcuts.ts:13`）；web 需補一層 window keydown 分發同樣 actions | MEDIUM |

### 🔴 Tier C — Electron-only，web 優雅降級（P4 處理，均已被 `isElectron` gate）

| 功能 | 降級策略 |
|---|---|
| Browser pane（BrowserView） | web 隱藏 provider（已 `disabled when no electronAPI`）；terminal link 一律走「開新分頁」 |
| 本機 FS（`local` backend） | web 不註冊；`daemon` + `inapp` 覆蓋 |
| Tear-off / merge 多視窗 | web 隱藏對應 UI（`canTearOffTab`/`canMergeWindow` 已 = `isElectron`） |
| System tray / Memory Monitor / Dev Update | web 隱藏入口（`canSystemTray` / `getProcessMetrics` / `devUpdateEnabled` 已 gate） |

---

## 5. Phase 拆解

四個 phase，各為 PR-sized、可獨立委派 codex review。

### P1 — Serving 地基（walking skeleton）

**目標**：`https://purdex.mlab.host/` 在純瀏覽器打開 → 選 mlab host → 開既有 session 的 terminal/stream 並互動。

- daemon 新增靜態託管：以 Go `embed.FS`（或設定指向 `spa/dist` 目錄）在 `/` 提供 build 好的 SPA，非 `/api` 的 GET 路徑做 **SPA history fallback**（回 `index.html`），且不得遮蔽既有 API/WS 路由。
- **靜態殼須在 auth 之前**：SPA 靜態資產（HTML/JS/CSS）的服務不得被 `TokenAuth` 擋（比照 `/api/health` bypass），否則 token 開啟時會 chicken-and-egg 無法載入登入頁。**只有 `/api` 與 WS 需 auth**。
- 瀏覽器端連線走**既有 host + WS + ticket** 機制，不新增同源捷徑（不用相對 URL 硬連自身、不靠 same-origin cookie）。
- **驗收**：`purdex.mlab.host` proxy 掛通（Herd/valet proxy → `:7860`，TLS 用 wildcard 憑證）；桌面瀏覽器能連 mlab host、開 terminal 並雙向互動。

> **Dev vs Prod serving**：daemon 服務的是 build 好的 `spa/dist`。SPA 主動開發仍可直接用既有 Vite dev server（`:5174`）走 HMR；`purdex.mlab.host` 走 daemon-served bundle 代表「真 web 路徑」。實作階段（plan）再定 daemon 在 dev mode 是否需 watch/重載 dist。

### P2 — 連線/認證 UX + 可安裝 PWA 殼

**目標**：瀏覽器初次進入即能順利連上 host；桌面可「安裝成 app」。

- **首連 UX**：Electron 版靠自動加 local host；web 沒有。web 首次進入須有清楚的「新增/選擇 host」入口（可預填當前 origin 對應的 daemon 作為建議，但仍以顯式 host entity 存在，不硬編）。
- **token 維持關**：本輪不做 token 輸入流程；但 UI 與 serving 不得假設「永遠無 token」——保留未來 token/pairing 疊加空間。
- **可安裝 PWA 殼**：`manifest.json`（名稱 / 圖示 / `display: standalone`）+ 圖示資產 + **極簡 network-first service worker**（`vite-plugin-pwa`）。
- **SW 快取約束（重要）**：service worker **必須 network-first / 版本感知**，不得 cache-first，以免吐出與 daemon 版本不對齊的舊 SPA。SW 只為「可安裝 + 殼載入」，**不快取 API/WS 回應、不做離線資料**。
- **驗收**：桌面 Chrome 出現安裝提示、安裝後為無瀏覽器 chrome 的 standalone 視窗；重新部署 SPA 後不會被 SW 卡在舊版。

### P3 — Tier B fallback

**目標**：web 中通知與快捷鍵可用。

- **通知**：在 web（非 Electron）路徑補「請求 `Notification` 權限」UX（適當時機請求，不在載入即彈）；沿用既有 `new Notification()` fallback；接上 `Notification.onclick` → session 導向（已有雛形於 `useNotificationDispatcher.ts:270`）。
- **web 快捷鍵層**：新增 window `keydown` 分發，觸發與 Electron `onShortcut` 相同的 action 集合；與既有 Electron IPC 路徑並存（`isElectron` 時仍用 IPC，避免重複觸發）。
- `openExternalUrl` → web 用 `window.open(url, '_blank', 'noopener')`。
- `focusMyWindow` → web 用 `window.focus()` 或 no-op。
- **驗收**：web 中 idle/notification 事件能跳 OS 通知並點擊導向；至少一組核心快捷鍵在 web 生效。

### P4 — Tier C 優雅降級

**目標**：所有 Electron-only 功能在 web **不出現**（而非報錯/空白）。

- 逐一確認並補齊 `isElectron` / capability gate，使下列在 web 乾淨隱藏：Browser pane provider、`local` FS backend、Tear-off/merge 入口、System tray、Memory/Performance Monitor、Dev Update 頁。
- terminal link 在 web 一律走「開新分頁」而非 browser pane。
- **驗收**：web 中無任何 Electron-only 入口殘留、無因 `window.electronAPI` undefined 造成的 console error 或壞畫面。

---

## 6. 貫穿原則（防繞路，所有 phase 適用）

1. **連線一律顯式 host + ticket**，origin 無關。
2. **不用相對 URL 硬連自身 daemon**（即使同源）。
3. **不靠 same-origin cookie 免 auth**。
4. **靜態殼 pre-auth，API/WS post-auth**——serving 分層不得讓 token 開啟時鎖死殼載入。
5. **Electron 功能一律 capability gate**，web 缺失走隱藏/降級，不 throw。
6. 目標：P1–P4 每一行都能被未來的 `desk.purdex.app` hosted client 原封沿用。

---

## 7. 開發／維運模式（本次特例）

偏離 CLAUDE.md 平常的「每 task PR→main」流程，為本次刻意特例：

- **長命 feature 分支**：全程於 worktree `worktree-web-version` 開發，phase 間仍各自獨立 commit、仍可各自委派 codex review，但**暫不逐 phase merge 回 main**（因主線正推另一大開發，避免互踩）。
- **定期 `git merge origin/main` → 本分支**：週期性拉主線進來，把衝突分散消化、避免掉隊。
- **`purdex.mlab.host` 暫掛本分支 daemon**：Herd/valet proxy → `:7860`，TLS 用 `*.mlab.host` wildcard。
- **收尾**：1+2 全完成、主線大開發告一段落後，再一次性或分幾個 PR merge 回 main。

---

## 8. 測試策略

- **SPA**：`cd spa && npx vitest run`；新增的 web fallback（通知權限、web keydown、capability gate 於非 Electron 環境）以既有測試 harness（無 `window.electronAPI` 的預設環境即 web 情境）覆蓋。
- **daemon**：`go test ./...`；靜態託管新增 handler 的路由/SPA-fallback/auth-bypass 行為以 handler test 覆蓋。
- **Lint/Build**：`cd spa && pnpm run lint && pnpm run build`。
- **手動驗證**：P1 完成後於真桌面瀏覽器（經 `purdex.mlab.host`）走一次核心流程；P2 驗 PWA 安裝與 SW 版本刷新；P4 檢查 console 無 Electron undefined 錯誤。

---

## 9. 驗收標準（整體）

- 桌面瀏覽器經 `https://purdex.mlab.host/` 可完成：選 mlab host → 看 session → terminal/stream 雙向互動 → 開遠端檔案。
- 可安裝為 standalone PWA，重新部署後不被 SW 卡舊版。
- web 通知可跳出並點擊導向；至少核心快捷鍵於 web 生效。
- web 中無 Electron-only 入口殘留、無 `window.electronAPI` undefined 造成的錯誤。
- 全程無新增同源捷徑；連線維持顯式 host + ticket、origin 無關。
- `go test` / `vitest` / `lint` / `build` 全綠。

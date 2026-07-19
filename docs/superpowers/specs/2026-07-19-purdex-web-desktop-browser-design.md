# Purdex Web（桌面瀏覽器版）— 設計

> 讓現有 SPA 能在**純瀏覽器**（無 Electron 外殼）中完整運作，並可透過 daemon 自託管、掛在 `purdex.mlab.host` 上使用。
> 北極星是產品化的 `desk.purdex.app` hosted client + daemon；本 spec 只做**桌面瀏覽器**的地基與功能對齊（子專案 1+2），刻意不含手機/responsive、不含 Web Push/離線。
>
> **修訂 r2**（codex spec review 後）：新增 P0（endpoint model + token-off negotiation 兩個地基阻斷）；serving 改寫成明確路由/中介層矩陣；SW 補版本對齊策略；快捷鍵改為共享 action/binding manifest；補公開 proxy 安全硬前提。

---

## 1. 動機

目前主力使用路徑是 **Purdex Electron app → Tailscale → mlab daemon（`100.64.0.2:7860`）**。當使用者不在桌面（例如手機、或臨時用別台機器的瀏覽器）時完全無法使用。

需求是一個 `https://purdex.mlab.host/` 的 web 版本，讓純瀏覽器就能連上 daemon 操作 session。但這**不能是拋棄式的一次性開發**——北極星設在產品化（`desk.purdex.app` hosted web client + daemon）。因此本 spec 的每一步都必須是北極星的地基，而非繞路。

### 1.1 現況：架構接近 web-ready，但有兩個地基阻斷

盤點現有程式碼，核心多數 origin 無關：

- **daemon middleware chain**（`cmd/pdx/main.go:193-205`）：`/api/health` 只包 `CORS`（bypass 其餘 auth）；`/` 包 `CORS` → `IPWhitelist` → `PairingGuard` → `TokenAuth(+tickets)`。
- **三個 WebSocket 端點皆 `CheckOrigin: true`**（`internal/core/events.go:177`、`internal/terminal/relay.go:19`、`internal/module/stream/handler.go:17`），路徑皆為 `GET /ws/...`。
- **SPA 是多 host client**：透過可設定的 host + WS + **ticket auth** 連 daemon。
- **FS 三個 backend**（`spa/src/lib/register-modules/fs-backends.tsx`）：`inapp`（IndexedDB）、`daemon`（遠端 host 檔案走 daemon HTTP，且為 **active-host proxy**，非永久綁定某 host）、`local`（僅 Electron IPC）。前兩者瀏覽器可用。
- **平台能力已抽象**：`getPlatformCapabilities()`（`spa/src/lib/platform.ts`）以 `isElectron` gate 原生功能。
- **通知已內建 Web fallback**：`useNotificationDispatcher.ts:269` 在非 Electron 時走 `new Notification()`。

**但有兩個地基阻斷（P0 必修，見 §5.0）：**

1. **Host endpoint model 無法表達 `https://hostname`**：`HostConfig` 只有 `ip + port`；`getDaemonBase()` 寫死 `http://${ip}:${port}`、`getWsBase()` 寫死 `ws://`（`useHostStore.ts:143`）。`purdex.mlab.host`（https / hostname / 無顯式 port）無法表達，硬塞會變 `http://purdex.mlab.host:443`（scheme 錯 + https 頁面下 mixed-content 被瀏覽器擋）。這也直接卡住 hosted client 北極星。
2. **token 關時連線卡死**：daemon `TokenAuth` 在 `token==""` 時放行所有請求（含 `/api/ws-ticket`），但 client `checkHealth()`（`host-connection.ts:33`）在「無 client token 且 mode=normal」時直接回 `daemon: 'auth-error'`，不去取 WS ticket、不連 WS（`useMultiHostEventWs.ts`）。與本輪「token 維持關」的驗收前提直接衝突。

缺的另外兩塊：daemon **不託管 SPA**；部分功能只綁 Electron，web 需 fallback 或優雅降級（§4）。

---

## 2. 目標與非目標

### 2.1 目標

- daemon 能自託管 build 好的 SPA，`https://purdex.mlab.host/` 純瀏覽器打開即可用。
- 核心流程（選 host → 看 session → terminal/stream 互動 → 開檔）在桌面瀏覽器完整可用。
- 所有 Electron-only 與 dev-only 入口在 web 中**乾淨降級**（隱藏而非壞掉）。
- 提供**可安裝的 PWA 殼**（standalone 視窗），且不會吐出與 daemon 版本不對齊的舊 SPA。
- 全程守住 origin 無關、顯式 host+ticket 連線，使成果直接成為 `desk.purdex.app` hosted client 的地基。

### 2.2 非目標（明確不含）

- **手機 / responsive / 觸控**（Tier D）— 另立子專案。
- **Web Push（背景推播）** — 需 VAPID + 推播服務 + daemon push sender，另議。
- **離線資料** — app 本質是 daemon 的 live client；SW 僅做「可安裝 + 殼載入」，不快取 API/WS、不做離線資料。
- **`desk.purdex.app` hosted 部署 / BYO-daemon pairing UX**（子專案 3）— 本 spec 只確保「不繞路」，不實作。
- **token/pairing 認證 UI 流程** — 本輪 `purdex.mlab.host` 走網路層保護，token 維持預設關。但 P0 的 negotiation 與 P1 的 serving 分層**不得**讓「未來開啟 token」無法載入登入頁（見 §6）。

---

## 3. 架構決策

### 3.1 拓撲：daemon 自託管 SPA（同源），origin 無關不走捷徑

`purdex.mlab.host` 由 Herd/valet **proxy** 反向代理到本分支的 daemon（`:7860`），TLS 走既有 `*.mlab.host` wildcard 憑證。daemon 在 `/` 提供 build 好的 SPA。

雖然是同源，但**連線模型維持顯式 host（含 scheme）+ ticket、origin 無關**——這是防繞路的核心約束（§6）。同源只當作「靜態檔從哪送出來」的部署選擇，不當作簡化 auth 的藉口。

### 3.2 公開 proxy 的安全模型（硬前提，非背景敘述）

本輪 token 關，安全完全依賴 proxy 前置保護，故以下為**硬前提**：

- daemon 現況 `IPWhitelist` **只看 `RemoteAddr`**，不解析/不信任 proxy header（`internal/middleware/middleware.go`）。掛在反向代理後，daemon 很可能只看到 proxy 本機位址 → **IP 白名單在 proxy 後方形同失效**。
- 加上 `CORS(*)` 與所有 WS `CheckOrigin: true`：**token 關的服務，一旦被允許來源機器上的任意網站腳本觸及，即可跨站驅動 daemon**。
- **前提要求**：`purdex.mlab.host` 的 proxy 層（Herd/valet 或其前置）**必須**承擔存取控制——限制在 Tailnet 來源、或加前置驗證。若無法保證 proxy 前置保護，則**必須把 token/pairing 提前到 P0/P1**，而非留待未來。
- 本 spec 採「proxy 於 Tailnet 內、僅 tailnet 來源可達」為前提；若日後對公網開放，token/pairing 變為必需——列為子專案 3 的前置條件。

### 3.3 為何不做 hosted BYO-daemon（子專案 3）先行

`desk.purdex.app` hosted client 價值是「一個公開網址」，但沒省掉真正門檻——瀏覽器連任何 daemon 都需該 daemon 有對外有效 TLS 憑證的 `wss://`。既然 TLS 門檻無論如何都在，先做同源自託管（delta 最小、消掉整類跨 origin/mixed-content/cookie 坑），且 client 端程式碼與 hosted 版共用（endpoint model 一旦支援 scheme，兩者一致），故子專案 3 之後疊加即可。

---

## 4. 現有功能 → web 難度盤點

### 🟢 Tier A — 已能在瀏覽器直接跑（P0 修好 endpoint/negotiation 後）

Terminal / Stream / JSONL、Sessions 清單 / Dashboard / New Tab、Hosts 多 host 連線、History、Files/Editor（`daemon` + `inapp` backend）、Image/PDF preview、Settings 多數頁、Quick Commands、Workspace Snapshot、Sync。

> Files/Editor 的 Electron 本機 `local` backend 在 web 消失；主用法走 `daemon` backend（mlab host 上的檔案），不受影響。註：`daemon` backend 為 active-host proxy，跨 host 檔案流程另有 host-bound helper，hosted 延用時勿誤當 host-pinned。

### 🟡 Tier B — 有 Electron 依賴，fallback 已存在或很輕（P3）

| 功能 | 處理 | 難度 |
|---|---|---|
| 通知 | Web Notification fallback 已在 code；補「請求權限」UX + click 導向。**注意**：勿用 `canNotification`（實為「Electron 通知 IPC 可用」）判斷 web 通知能力，改用獨立的 web-notification 能力判斷或擴充 capability model | LOW |
| 開外部連結（terminal link） | `electronAPI.openExternalUrl` → `window.open(url, '_blank', 'noopener,noreferrer')` | LOW |
| `focusMyWindow` | `window.focus()` / no-op | TRIVIAL |
| App 鍵盤快捷鍵 | 見 §5.3：抽共享 action executor + binding manifest；web 補 keydown 分發 | MEDIUM |

### 🔴 Tier C — Electron-only / dev-only，web 優雅降級（P4）

| 功能 | 降級策略 |
|---|---|
| Browser pane（BrowserView） | web 隱藏 provider（已 `disabled when no electronAPI`）；terminal link 一律「開新分頁」 |
| 本機 FS（`local` backend） | web 不註冊；`daemon` + `inapp` 覆蓋 |
| Tear-off / merge 多視窗 | web 隱藏對應 UI（`canTearOffTab`/`canMergeWindow` 已 = `isElectron`） |
| System tray / Memory Monitor / Dev Update | web 隱藏入口（capability 已 gate） |
| **`tmux-agent-monitor`（dev-only）** | 目前在 `import.meta.env.DEV` 下即註冊（`register-modules/index.tsx:392`），**非 Electron 也會出現** → P4 須額外處理，不能只靠既有 Electron gate |

---

## 5. Phase 拆解

五個 phase（新增 P0），各為 PR-sized、可獨立委派 codex review。

### P0 — 地基阻斷修正（先於一切）

沒有這兩項，P1 的 `purdex.mlab.host` 核心流程不會穩定成立；故先做，且兩項都是既有機制的擴充，不引入同源捷徑。

- **P0-a Host endpoint model 支援 scheme**：`HostConfig` 由 `ip + port` 擴充為能表達 `scheme(http|https) + host + optional port`（缺 port 時依 scheme 取 80/443）。`getDaemonBase()`/`getWsBase()` 從同一 endpoint 正確導出（`http↔ws`、`https↔wss`）。既有 `ip+port` host 需 migration（alpha 階段依 [[feedback_no_alpha_migration]] 可用簡單就地轉換，不需完整 persist migration 框架——由 plan 定）。
- **P0-b token-off negotiation**：`checkHealth()` 在無 client token 時**仍嘗試 `POST /api/ws-ticket`**；`200`→token 關、取得 ticket；`401`→真 auth-error；`503`→依現況。使「token disabled」與「token required」可區分，token 關時 WS 能正常取得 ticket 並連線。
- **驗收**：可新增一個 `https://<hostname>` 形式的 host；在 token 關的 daemon 上，health negotiation 產出 ticket、WS 連上。（此階段仍可在既有 Electron/dev 環境驗證，不需 web serving。）

### P1 — Serving 地基（walking skeleton）

**目標**：`https://purdex.mlab.host/` 純瀏覽器打開 → 選 mlab host → 開既有 session 的 terminal/stream 並互動。

- daemon 新增靜態託管：以 **`embed.FS` + `fs.Sub` + `http.FileServerFS`**（或等價的受限檔案系統 / 路徑 containment）提供 build 好的 SPA，**避免手刻 `path.Join(distDir, req.URL.Path)` 造成 path traversal**。
- **明確路由 / 中介層矩陣**（不得只寫「不遮蔽 API/WS」）：

  | 路徑 | 中介層 | 說明 |
  |---|---|---|
  | `GET /api/health` | `CORS` only | 維持現況 bypass |
  | `/api/*`（其餘） | `CORS`→`IPWhitelist`→`PairingGuard`→`TokenAuth` | 現況 protected mux |
  | `/ws/*` | `CORS`→`IPWhitelist`→`PairingGuard`→`TokenAuth` | 三條 WS 皆 `GET /ws/...`，**必須走 protected**，不得被 static fallback 吃掉 |
  | 其餘（static / SPA fallback） | `CORS`→`IPWhitelist`（保留 IP 白名單）→ **bypass `TokenAuth`** | 僅 `GET`/`HEAD`；SPA history fallback 回 `index.html`。**`PairingGuard` 是否套用**：static 殼須在 pairing mode 也能載入（未來 pairing UI 才載得出），故 static **bypass `PairingGuard`**；由 plan 落實 |

  → 分流條件寫死：`/api/health` bypass、`/api/` 與 `/ws/` 走 protected、其餘僅 `GET|HEAD` 才走 static/fallback。static **保留 `IPWhitelist`、繞過 `TokenAuth` 與 `PairingGuard`**（不得整個放到 middleware 外連 IP 白名單一起繞掉）。
- 瀏覽器端連線走 **P0 修好的 host + WS + ticket** 機制，不新增同源捷徑（不用相對 URL 硬連自身、不靠 same-origin cookie）。
- **驗收**：`purdex.mlab.host` proxy 掛通（Herd/valet proxy → `:7860`，TLS 用 wildcard 憑證）；桌面瀏覽器能連 mlab host、開 terminal 雙向互動；`/ws/*` 未被 fallback 攔截。

> **Dev vs Prod serving**：daemon 服務 build 好的 `spa/dist`。SPA 主動開發仍可直接用 Vite dev server（`:5174`）走 HMR；`purdex.mlab.host` 走 daemon-served bundle 代表「真 web 路徑」。daemon dev mode 是否需 watch/重載 dist 由 plan 定。

### P2 — 連線/認證 UX + 可安裝 PWA 殼

**目標**：瀏覽器初次進入即能順利連上 host；桌面可「安裝成 app」且版本不卡舊。

- **首連 UX**：Electron 版靠自動加 local host；web 沒有。web 首次進入須有清楚的「新增/選擇 host」入口（可預填當前 origin 對應的 daemon 作為建議 endpoint，含正確 scheme，但仍以顯式 host entity 存在，不硬編、不隱式同源連線）。
- **token 維持關**：本輪不做 token 輸入流程；UI/serving 不得假設「永遠無 token」。
- **可安裝 PWA 殼**：`manifest.json`（名稱 / 圖示 / `display: standalone`）+ 圖示資產 + **service worker**（`vite-plugin-pwa`）。
- **SW 版本對齊策略（重要，避免吐舊 SPA）**，採**保守最小快取**：
  - SW **不快取 API/WS 回應、不做離線資料**；app shell 採 network-first（線上必取新），僅在完全離線時才回退（可接受此時無法運作）。
  - 明訂 `skipWaiting` + `clientsClaim`：新 SW 啟用即接管。
  - client 端偵測到有新版 SW 啟用時，**觸發一次 reload**，避免已載入的舊 bundle 與新 daemon 不對齊。
  - （可選）SPA↔daemon 版本握手：SPA 讀 `/api/info` 的 `purdex_version` 與自身 build 版本比對，不一致時提示/自動 reload——由 plan 評估是否納入本輪。
- **驗收**：桌面 Chrome 出現安裝提示、安裝後為 standalone 視窗；重新部署 SPA 後，reload 後即為新版，不被 SW 卡舊。

### P3 — Tier B fallback（通知 + 快捷鍵）

**目標**：web 中通知與快捷鍵可用，且與 Electron 路徑共享單一事實來源。

- **通知**：web（非 Electron）補「請求 `Notification` 權限」UX（適當時機請求，非載入即彈）；沿用既有 `new Notification()` fallback；接上 `Notification.onclick` → session 導向。**能力判斷勿用 `canNotification`**（該 flag 實為 Electron 通知 IPC 是否可用），改用獨立 web-notification 能力判斷。
- **快捷鍵——共享 action + binding manifest**（避免 Electron / web 兩份漂移）：
  - 抽出**共享 action executor**（收到 action id → 執行）與**共享 binding manifest**（accelerator ↔ action）。目前 accelerator 定義在 Electron main（`electron/keybindings.ts`），`useShortcuts()` 只負責執行端。
  - Electron IPC（`onShortcut`）與 web `keydown` **都只餵 action id** 給同一 executor；`isElectron` 時走 IPC、web 時走 keydown，二者互斥不重複觸發。
  - **明訂焦點規則**：在 `input` / `contentEditable` / xterm 終端焦點下，哪些快捷鍵該攔截、哪些放行（避免終端輸入與表單誤觸）——由 plan 列出規則表。
- `openExternalUrl` → `window.open(url, '_blank', 'noopener,noreferrer')`；`focusMyWindow` → `window.focus()` / no-op。
- **驗收**：web 中 idle/notification 事件跳 OS 通知並點擊導向；核心快捷鍵集在 web 生效且不誤觸終端/表單輸入。

### P4 — Tier C 優雅降級

**目標**：所有 Electron-only 與 dev-only 入口在 web **不出現**（而非報錯/空白）。

- 逐一確認並補齊 gate，使下列在 web 乾淨隱藏：Browser pane provider、`local` FS backend、Tear-off/merge 入口、System tray、Memory/Performance Monitor、Dev Update 頁。
- **已知 dev-only 例外**：`tmux-agent-monitor` 在 `import.meta.env.DEV` 下註冊（非 Electron 也出現），須明確處理（web 不出現，或改綁 Electron capability）。
- terminal link 在 web 一律走「開新分頁」而非 browser pane。
- **驗收**：web 中無任何 Electron-only / dev-only 入口殘留、無因 `window.electronAPI` undefined 造成的 console error 或壞畫面。

---

## 6. 貫穿原則（防繞路，所有 phase 適用）

1. **連線一律顯式 host endpoint（含 scheme）+ ticket**，origin 無關。
2. **不用相對 URL 硬連自身 daemon**（即使同源）；預填當前 origin 只是「建議值填入顯式 host entity」，非隱式連線。
3. **不靠 same-origin cookie 免 auth**。
4. **serving 分層矩陣固定**：static 保留 IPWhitelist、繞過 TokenAuth/PairingGuard；`/api` 與 `/ws` 走 protected；token 開啟時登入/pairing 頁仍載得出。
5. **Electron / dev-only 功能一律 capability gate**，web 缺失走隱藏/降級，不 throw。
6. 目標：P0–P4 每一行都能被未來的 `desk.purdex.app` hosted client 原封沿用。

---

## 7. 開發／維運模式（本次特例）

偏離 CLAUDE.md 平常的「每 task PR→main」流程，為本次刻意特例：

- **長命 feature 分支**：全程於 worktree `worktree-web-version` 開發，phase 間仍各自獨立 commit、各自可委派 codex review，但**暫不逐 phase merge 回 main**（因主線正推另一大開發，避免互踩）。
- **定期 `git merge origin/main` → 本分支**：週期性拉主線進來，把衝突分散消化、避免掉隊。
- **`purdex.mlab.host` 暫掛本分支 daemon**：Herd/valet proxy → `:7860`，TLS 用 `*.mlab.host` wildcard；proxy 前置保護見 §3.2 硬前提。
- **收尾**：1+2 全完成、主線大開發告一段落後，再一次性或分幾個 PR merge 回 main。

---

## 8. 測試策略

- **SPA**：`cd spa && npx vitest run`。P0 的 endpoint model / negotiation、P3 的通知權限與 web keydown（含焦點規則）、P4 的 capability gate，皆於既有測試 harness（無 `window.electronAPI` 即 web 情境）覆蓋。
- **daemon**：`go test ./...`。靜態託管新增 handler 的路由矩陣、SPA-fallback（`/ws/*` 不被吃）、auth-bypass（static 繞 TokenAuth 但保留 IPWhitelist）、path-traversal 防護，皆以 handler test 覆蓋。
- **Lint/Build**：`cd spa && pnpm run lint && pnpm run build`。
- **手動驗證**：P1 完成後於真桌面瀏覽器（經 `purdex.mlab.host`）走核心流程；P2 驗 PWA 安裝與 SW 版本刷新（部署新版→reload→新版）；P4 檢查 console 無 Electron undefined 錯誤。
- **Codex sandbox 無網路**（[[feedback_codex_sandbox_no_install]]）：SPA 任務由主 Claude 手動 `pnpm install` + vitest/lint/build 驗證。

---

## 9. 驗收標準（整體）

- 可新增 `https://<hostname>` 形式 host；token 關的 daemon 上 health negotiation 取得 ticket、WS 連上。
- 桌面瀏覽器經 `https://purdex.mlab.host/` 可完成：選 mlab host → 看 session → terminal/stream 雙向互動 → 開遠端檔案；`/ws/*` 未被 static fallback 攔截。
- 可安裝為 standalone PWA，重新部署後 reload 即新版，不被 SW 卡舊。
- web 通知可跳出並點擊導向；核心快捷鍵於 web 生效且不誤觸終端/表單輸入。
- web 中無 Electron-only / dev-only 入口殘留、無 `window.electronAPI` undefined 造成的錯誤。
- 全程無新增同源捷徑；連線維持顯式 host endpoint（含 scheme）+ ticket、origin 無關。
- static serving 具 path-traversal 防護；分層矩陣符合 §5.1（static 繞 TokenAuth 但保留 IPWhitelist）。
- `go test` / `vitest` / `lint` / `build` 全綠。

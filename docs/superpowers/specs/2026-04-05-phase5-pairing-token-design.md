# Phase 5: 配對系統 + Token 認證

> Phase 5a — 配對流程建立
> Phase 5b — WS ticket 統一 + Auth Error UI

## 背景

Phase 1-4 完成 storage 抽象、識別系統、連線偵測、錯誤 UI。Daemon 目前 `config.toml` 的 `token = ""` 預設未啟用認證，所有 endpoint 完全開放。

專案將提供 Cloudflare 靜態 SPA，使用者安裝 daemon 後直接透過公開 SPA 連線。需要一套安全且低摩擦的首次設定流程。

## 設計原則

- **安全性主導權在 daemon 側**（有 filesystem 存取權的一方）
- **配對與認證分離**：配對碼負責安全握手，token 負責長期認證
- **單一 token**：Phase 5 範圍內所有 client 共用同一組 token
- **兩種設定模式，兩個信任方向**：
  - Quick 模式：client 產生 token → 送給 daemon（便捷，有配對碼保護的風險窗口）
  - 一般模式：daemon 產生 token → 使用者抄到 client（無風險窗口）

## 專案命名

自 Phase 5 起，新開發的參數使用 **Purdex** 命名（token prefix `purdex_`）。
既有的 `tbox token generate` 命令標記為 deprecated，新程式碼統一用 `purdex_` 前綴。
設定完成後，舊 `tbox_` 前綴的 token 會被新 token 覆蓋而失效。

---

## Phase 5a：配對系統

### 兩種設定模式

| | Quick 模式 | 一般模式 |
|--|-----------|---------|
| 指令 | `tbox serve --quick` | `tbox serve` |
| Token 產生方 | **Client** | **Daemon** |
| Token 傳遞方向 | Client → Daemon（via pairing） | Daemon → terminal → 使用者 → Client |
| 配對碼 | **13 碼**（IP+Port+Secret） | **無**（daemon 直接印 token） |
| API 路線 | `/api/pair/verify` → `/api/pair/setup` | `/api/token/auth` |
| PairingGuard | 啟用（503 攔截非配對端點） | 不啟用（TokenAuth 已生效） |
| 風險窗口 | 配對碼暴露期間 | 無 |

兩種模式互斥：有 `--quick` 走 Quick，無 `--quick` 走一般。

### 配對碼（Quick 模式專屬）

**規格**：
- 編碼內容：IP(4 bytes) + Port(2 bytes, big-endian) + Secret(3 bytes) = 9 bytes = 72 bits
- 編碼方式：Base58（字元集 `123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz`）
- 固定長度：13 字元，左補 `1`（Base58 的零值）
- 顯示格式：`XXXX-XXXX-XXXXX`（4-4-5 分隔）
- Secret entropy：3 bytes = 16,777,216 組合
- 驗算：9 bytes 最大值 = 2⁷² - 1 ≈ 4.72 × 10²¹；58¹³ ≈ 8.41 × 10²² > 2⁷² ✓；58¹² ≈ 1.45 × 10²¹ < 2⁷² → 確實需要 13 碼
- 補碼規則：**固定**補到 13 碼，編解碼雙端必須嚴格��守

**暴力破解防護**：
- 10 次 verify 失敗 → secret 失效 → daemon 自動產生新配對碼
- Terminal 印出醒目警告：`⚠ 配對碼已失效（過多失敗嘗試），新配對碼：XXXX-XXXX-XXXXX`
- 失敗計數器隨新配對碼重置

**編碼**（Daemon 端，Go）：
1. 組成 9 bytes：`ip(4) || port(2, big-endian) || secret(3)`
2. Base58 encode（大整數除餘法）
3. 左補 `1` 到固定 13 碼
4. 插入分隔符 → `XXXX-XXXX-XXXXX`

**解碼**（SPA 端，TypeScript）：
1. 清除 `-`、`/`、空白
2. 驗證字元皆在 Base58 字元集內
3. Base58 decode → 9 bytes
4. 拆出 `ip(4) + port(2) + secret(3)`
5. 回傳 `{ ip: "x.x.x.x", port: number, secret: string(hex) }`
6. 解碼失敗或 byte 數不為 9 → 顯示「無效的配對碼」，不發起請求

**注意**：Base58 decode 時必須保留前導 `1` 作為零值 byte，不可修剪。

### Daemon 啟動模式

```
tbox serve --quick
  ├─ 有 token → 正常啟動（--quick 無額外效果）
  └─ 無 token
       ├─ config 無 bind → 互動選單（枚舉非 loopback IP，不含 0.0.0.0 / 127.0.0.1）
       │   只有 1 個 → 自動選取
       │   多個 → 使用者選擇（顯示 IP + 介面名稱）
       │   0 個 → 錯誤退出
       ├─ 選定 IP 存入 config.toml
       ├─ 產生 13 碼配對碼 → 印出 → PairingGuard 啟用
       └─ 啟動 HTTP server → 進入配對等待

tbox serve [--bind <ip>] [--port <port>]
  ├─ 有 token → 正常啟動
  └─ 無 token
       ├─ 產生 purdex_ token → runtime 暫存（不寫 config）
       ├─ runtime token 寫入 cfg.Token（TokenAuth getter 生效）
       ├─ 啟動 HTTP server（所有 API 已受 TokenAuth 保護）
       └─ terminal 印出 token → 等待 client 呼叫 /api/token/auth
```

**啟動順序要求**：runtime token 必須在 `srv.ListenAndServe()` 之前寫入 `cfg.Token`，避免 server 開始監聽後存在 token 為空的 pass-through 窗口。

**已知限制**：`--quick` 且 config 已有 token 時，`--quick` 無特殊行為（IP 已在首次配對時存入 config）。若需更換 bind IP，使用者須手動編輯 config.toml 或刪除後重新設定。

#### Quick 模式（`--quick`）

專為「安裝即用」設計的便捷入口：
1. 無 token → 互動選擇 bind IP → 產生 13 碼配對碼 → 進入配對等待
2. 有 token → 正常啟動

**PairingGuard 啟用**：只有 `/api/pair/*` 和 `/api/health` 開放，其他端點回 `503`（body: `{ "reason": "pairing_mode" }`）。

#### 一般模式（無 `--quick`，無 token）

適用所有部署情境（直連、nginx 轉發、tunnel）：
1. Daemon 產生 `purdex_` + 40 hex chars token（160-bit entropy）→ runtime 暫存
2. Token 寫入 `cfg.Token`，TokenAuth 立即生效
3. 啟動 HTTP server
4. Terminal 印出 token
5. 等待 client 呼叫 `/api/token/auth` 確認 → 寫入 config

**PairingGuard 不啟用**：TokenAuth 已在 runtime 生效，所有 API 都受保護。

**bind 限制**：一般模式不做互動式 IP 選擇。若 config bind 為 `127.0.0.1`（預設值），只有同機器的 client 能連線。Remote SPA 需使用者先用 `--bind` 指定可達 IP，或透過 nginx/tunnel 轉發到 localhost。

#### Daemon 重啟行為

- **Quick 配對中重啟**：secret 消失，產生新配對碼，舊配對碼失效。SPA 需重新輸入新碼。
- **一般模式 pending 中重啟**：runtime token 消失，config 無 token，再次進入一般模式，產生新 token。SPA 若已存了舊 token，連線會 401，需重新設定。
- **正常模式重啟**���token 在 config，無影響。

#### 正常模式（config 已有 token）

現有行為不變。`/api/pair/*` 端點仍掛載但回 `409`（body: `{ "reason": "already_paired" }`）。

### Middleware 架構

#### TokenAuth 動態化

現有 `TokenAuth(cfg.Token, tickets)` 是值傳遞，配對完成後更新 `cfg.Token` 不會生效。

**修正**：`TokenAuth` 改為接受 `func() string` getter：
```go
// Before
func TokenAuth(token string, tickets TicketValidator) func(http.Handler) http.Handler

// After
func TokenAuth(tokenFn func() string, tickets TicketValidator) func(http.Handler) http.Handler
```

Middleware 每次請求時呼叫 `tokenFn()` 取得當前 token 值。需同步更新所有呼叫點（`main.go` 及相關測試）。

#### Pairing Guard Middleware

新增 outer mux 層級的 middleware，讀取 runtime pairing state：
- Quick 配對模式下：非 `/api/pair/*` 和非 `/api/health` 的請求回 `503`（body: `{ "reason": "pairing_mode" }`）
- 其他模式：pass-through

```
outerMux
  ├─ /api/health → CORS only（不變）
  └─ / → CORS → IPWhitelist → PairingGuard → TokenAuth(getter) → innerMux
                                   ↑
                             Quick 配對模式: 只放行 /api/pair/*
                             其他: pass-through
```

**注意**：IPWhitelist 在 PairingGuard 之前，Quick 模式下 `/api/pair/*` 仍受 IP 白名單保護。使用者若有設定 `allow`，需確認 client IP 在白名單內。

#### Pairing State 存放

`Core` struct 新增 pairing state 欄位（`atomic.Value` 或 `sync.RWMutex` 保護的 enum），供 PairingGuard、`/api/health` handler 及配對 API 讀取。

狀態值：
- `pairing`：Quick 配對中，PairingGuard 啟用
- `pending`：一般模式，token 在 runtime 但未持久化
- `normal`：已就緒

### 配對 API

#### Quick 模式路線

**`POST /api/pair/verify`**

驗證 pairing secret，回傳一次性 setup secret。

```
Request:  { "secret": "a1b2c3" }          // hex(3 bytes)
Response: { "setupSecret": "..." }         // 32-char hex, 5 分鐘過期, 一次性
Error:    401 — secret 不符（計入失敗次數）
          409 — 已配對完成（body: { "reason": "already_paired" }）
```

**非配對模式下的行為**：`pending` 或 `normal` 模式下，請求先經過 TokenAuth（token 非空），無 token 的請求被攔截回 401。已認證的請求到達 handler 後回 409 `already_paired`。

setupSecret 使用獨立的 `SetupSecretStore`（與 WS TicketStore 分離，支援 5 分鐘 TTL + 一次性消費）。每次 verify 成功時，先清空 store 中的舊 setupSecret，再儲存新的，確保只有一個 active setupSecret。

Daemon 重啟 → setupSecret 消失 → client 從 verify 重新開始。

**`POST /api/pair/setup`**

設定 token，完成配對。

```
Request:  { "setupSecret": "...", "token": "purdex_..." }
Response: { "ok": true }
Error:    401 — setupSecret 無效或過期
          409 — 已配對完成（body: { "reason": "already_paired" }）
          422 — token 格式無效（空字串或長度 < 20 字元）
```

**Token 格式驗證**：daemon 端驗證 token 非空且長度 >= 20 字元（不限制前綴，接受任意字串），避免使用者設定空 token 導致 TokenAuth 退化為 pass-through。SPA 端 Token 欄位同步做最小長度驗證。

Setup 成功後的行為：
1. 驗證 setupSecret（一次性消費）
2. 驗證 token 格式
3. Token 寫入 `config.toml`（atomic write：寫 `.tmp` → rename）
4. Runtime `cfg.Token = token`（TokenAuth getter 立即生效）
5. Runtime pairing state 切換為 `normal`（PairingGuard 停止攔截）
6. 後續所有請求需 `Authorization: Bearer <token>`

#### 一般模式路線

**`POST /api/token/auth`**

確認 token 並持久化到 config。走 TokenAuth middleware — client 帶正確的 Bearer token 即可。

```
Header:   Authorization: Bearer purdex_xxx
Request:  {}（不需 body，token 已在 header）
Response: { "ok": true }
Error:    401 — token 不符（由 TokenAuth middleware 攔截）
          409 — 已有持久化 token（body: { "reason": "already_confirmed" }）
```

**409 判斷邏輯**：handler 以 `pairing state == normal` 作為判斷依據。state 為 `normal` 時回 409 `already_confirmed`。

**409 `already_confirmed` 的處理**：SPA 收到 409 `already_confirmed` 時應視為成功（token 已持久化），直接執行 `addHost`。這處理了 confirm 成功但 response 因網路中斷未收到的重試場景。

Confirm 成功後的行為：
1. 將 runtime token 寫入 `config.toml`（atomic write）
2. Runtime pairing state 切換為 `normal`
3. 後續行為與正常模式一致

### `/api/health` 擴充

回傳 daemon 當前模式，讓 SPA 能提前判斷狀態：

```json
{
  "ok": true,
  "mode": "pairing" | "pending" | "normal"
}
```

保留 `ok` 欄位（boolean）以維持與 Phase 3 ConnectionStateMachine 的向下相容。新增 `mode` 欄位。

- `pairing`：Quick 配對模式���等待配對碼配對
- `pending`：一般模式，daemon 有 runtime token 但尚未持久化
- `normal`：正常運作

**安全考量**：`/api/health` 不受 TokenAuth 保護（���在 outer mux），`mode` 欄位會洩漏 daemon 狀態。`pending` 狀態下 token 是 160-bit entropy，即使攻擊者得知 daemon 在等待，暴力猜測 token 在數學上不可行。

### SPA 配對 UI

**「新增主機」對話框**（完整重寫 `AddHostDialog.tsx`）：

兩個區塊互斥：上方配對碼（Quick 模式）、下方 Token 連線（一般模式）。

```
┌─ 新增主機 ──────────────────────────────┐
│                                          │
│  配對碼                                   │
│  [XXXX-XXXX-XXXXX      ]  [配對]         │
│                                          │
│  ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─   │
│                                          │
│  ☐ 使用 Token 連線（已有 Token）           │
│                                          │
│  主機   [                ]  (disabled)    │
│  連接埠 [7860            ]  (disabled)    │
│  Token  [                ] [🎲] (disabled)│
│                                          │
│              [取消]  [確認]               │
└──────────────────────────────────────────┘
```

**配對路線**：輸入 13 碼配對碼 → decode 出 IP/Port/Secret → 配對成功後自動帶入下方欄位
**Token 路線**：checkbox 勾選後啟用下方欄位 → 使用者手填 host/port/token

**Stage 狀態機**：

```
配對路線：idle → pairing → paired → saving → done
                  ↓ fail              ↓ fail
                error               error

Token 路線：idle → manual → saving → done
                             ↓ fail
                            error
```

| Stage | 配對碼欄位 | Host/Port/Token | 確認按鈕 |
|-------|-----------|----------------|---------|
| `idle` | enabled | disabled | disabled |
| `pairing` | disabled, loading | disabled | disabled |
| `paired` | disabled, ✓ | enabled, IP/Port/Token 自動帶入 | enabled |
| `manual` | disabled | enabled（checkbox checked） | enabled |
| `saving` | disabled | disabled, loading | disabled |
| `done` | — | — | 關閉 dialog |
| `error` | 見下方 | 見下方 | enabled（重試）|

**`paired` 狀態下欄位可編輯**：13 碼 decode 的 IP/Port 自動帶入，但使用者可修改（用於 NAT/proxy 位址修正）。Token 由 SPA 自動產生，使用者可修改。

**Error 回退規則**：
- `pairing → error`：回到 `idle`，配對碼清空，可重新輸入
- `saving → error`（配對路線）：setupSecret 可能已被消費。回到 `idle`，清空 setupSecret，需重新從 verify 開始。Error 訊息提示「請重新輸入配對碼」
- `saving → error`（Token 路線）：回到 `manual`，可直接重新嘗試

**Stage 退回規則**：
- Dialog 關閉即銷毀所有 local state��不持久化 stage、setupSecret 等）
- `manual → idle`：取消勾選 checkbox 即回到 `idle`，配對碼欄位重新啟用

**互動流程**：

1. **輸入配對碼** → 自動清理 `-`、`/`、空白
2. **按「配對」** → Stage `pairing` → decode 配對碼 → 用解碼的 IP/Port 打 `POST /api/pair/verify`
   - 成功：Stage `paired`，儲存 setupSecret，帶入 IP/Port，自動產生 `purdex_` token
   - 失敗：Stage `error`，顯示錯誤訊息
3. **checkbox「使用 Token 連線」** → Stage `manual`，配對碼 disabled，下方欄位 enabled
4. **按「確認」**：Stage `saving`
   - 配對路線：`POST /api/pair/setup`（setupSecret + token）→ 成功後 `addHost(host, token)` → Stage `done`
   - Token 路線：`POST /api/token/auth`（Bearer token）→ 成功或 409 `already_confirmed` → `addHost(host, token)` → Stage `done`

**addHost 順序**：API 成功 → `useHostStore.addHost({ name, ip, port, token })` → store 持久化 → 後續 hostFetch 使用 stored token。`name` 來源：以 IP 作預設值（如 `100.64.0.2`），使用者可在 UI 欄位中修改。

**Token 欄位**：
- 🎲 按鈕：產生 `purdex_` + 40 hex chars（與 CLI 格式一致，160-bit entropy）
- 使用者可自行輸入任意字串（最小長度 20 字元）
- 配對成功後（Stage `paired`）預設自動產生一組，使用者可修改

### 安全性

- 配對碼 secret：3 bytes = 16,777,216 組合 + 10 次失敗重置
- setupSecret：獨立 SetupSecretStore，32-char hex（128-bit），5 分鐘過期 + 一次性消費，每次 verify 清空舊 secret
- Token 格式驗證：非空 + 長度 >= 20 字元，避免空 token 導致 TokenAuth pass-through
- Quick 配對模式下非配對端點回 503，不洩漏任��資料
- 一般模式下 TokenAuth 已在 runtime 生效，無需 PairingGuard
- Token 不出現在 URL、log 或瀏覽器歷史
- Config 寫入使用 atomic write（`.tmp` → rename），避免 partial write
- **Threat model 邊界**：Quick 配對模式下 `/api/pair/*` 無認證（配對碼是唯一保護），CORS 為 `*`。任何能到達 daemon 的請求都可嘗試 verify。3 bytes secret + 10 次失敗重置被認為在私有網路（Tailscale/LAN）場景下是可接受的風險。daemon 日誌中的警告是重要的安全信號。

### CORS

`/api/pair/*` 和 `/api/token/auth` 皆掛在 inner mux，由 outer handler 的 CORS middleware 統一覆蓋。
Cloudflare SPA 的跨 origin preflight OPTIONS 請求正常處理。

---

## Phase 5b：WS Ticket 統一 + Auth Error UI（預覽，不在 5a 實作）

### Terminal WS Ticket 統一

- `/ws/terminal/{code}` 改用 ticket 認證（與 `/ws/host-events`、`/ws/cli-bridge-sub` 一致）
- SPA `SessionPaneContent.tsx` 連線前先 `POST /api/ws-ticket` 取 ticket

### HTTP 401 Auth Error UI

- ConnectionStateMachine 新增 `auth-error` 狀態（host 層級）
- 不自動重試（token 不會自己變對）
- UI 顯示「Token 無效，請重新設定」+ 引導至 host 設定頁
- HostSidebar / StatusBar / OverviewSection 對應顯示

### WS Upgrade 401 靜默處理

- Ticket 過期或已消費 → 靜默重拿 ticket 再試一次
- 不影響使用者，不顯示錯誤
- WS upgrade 401 的成因皆為暫時性（過期、已消費、daemon 重啟清空 in-memory tickets）
- **注意**：需區分 ticket 問題（暫時性）vs token 問題（永久性）。兩者在 WS upgrade 層面都是 401，daemon 應在 ticket 相關的 401 回應中附帶識別資訊（例如 response body 或 header），讓 SPA 判斷是否值得重試。

---

## 認證架構總覽

```
Client (SPA / Electron)                    Daemon
  │                                          │
  │  ═══ Quick 模���（配對碼）═══              │
  │  POST /api/pair/verify { secret }        │
  │  ──────────────────────────────────►     │  → 驗證 secret, 回傳 setupSecret
  │  POST /api/pair/setup { setupSecret,     │
  │                         token }          │
  │  ──────────────────────────────────►     │  → client token 寫入 config
  │                                          │
  │  ═══ 一般模式（Token）═══                 │
  │  POST /api/token/auth                    │
  │  Authorization: Bearer <token>           │
  │  ──────────────────────────────────►     │  → daemon token 持久化到 config
  │                                          │
  │  ═══ 正常運作 ═══                         │
  │  HTTP API                                │
  │  Authorization: Bearer <token>           │
  │  ──────────────────────────────────►     │  TokenAuth middleware
  │                                          │  constant-time compare
  │  WebSocket                               │
  │  POST /api/ws-ticket (Bearer)            │
  │  ──────────────────────────────────►     │  → 回傳 one-time ticket
  │  WS upgrade ?ticket=xxx                  │     (30s TTL, 一次性)
  │  ──────────────────────────────────►     │  → 消費 ticket, 建立連線
```

## Token vs Ticket 職責分離

| | Token | Ticket |
|--|-------|--------|
| 生命週期 | 永久（直到使用者更換） | 30 秒，一次性 |
| 用途 | HTTP API 認證 | WS 握手認證 |
| 傳輸方式 | `Authorization: Bearer` header | `?ticket=` query param |
| 安全考量 | 不出現在 URL | 短命 + 一次性，即使截獲也無法重用 |
| 失敗影響 | Per-host，需使用者介入 | Per-session，靜默重試 |

## 未來擴充（開 issue 追蹤）

- 多 token 支援：`tokens = [{ id, token, label, permissions }]`
- Per-client 權限：read-only / 限定 session / 全存取
- 配對碼不變，setup 時改為回傳 token ID
- bind IP 變更機制（`--quick` + 已有 token 時）

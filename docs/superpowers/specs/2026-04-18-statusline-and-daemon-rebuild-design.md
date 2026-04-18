# StatusLine Installer + Daemon Dev Rebuild — Design

> **Date**：2026-04-18
> **Status**：Brainstormed + subagent-reviewed, awaiting user sign-off
> **Scope**：兩個獨立 PR
> - PR-1：CC statusLine wrapper + Agents tab installer UI（end-to-end）
> - PR-2：Daemon dev rebuild + restart in `Settings → Development`
> **Background**：[`2026-04-18-cc-statusline-integration-design.md`](./2026-04-18-cc-statusline-integration-design.md)（OSC dead-end / hooks vs statusLine 對比 / 傳輸方案研究）
>
> **Prep refactors**（兩 PR 分別內含，非獨立 PR）：
> - PR-1：從 `spa/src/features/workspace/components/ActivityBarNarrow.tsx:85-87` 的 `ws-tooltip` 抽出共用元件 `<HoverTooltip>`（CSS-only group-hover tooltip），供 tab hover 與未來其他 hover 元件復用
> - PR-1：從 `internal/agent/cc/hooks.go` 抽出 `ccSettingsPath()` helper（目前 inline 在多處），供 statusline installer 共用
> - PR-2：對**既有** `/api/dev/update/*` 補 daemon 端 `PDX_DEV_UPDATE=1` gating（目前僅 Electron preload 端 gate，server-side 裸露是洞），同時套用於新 `/api/dev/daemon/*` endpoint

---

## 0. 範圍決定（從 brainstorm）

| 主題 | 決定 |
|---|---|
| Codex 端 statusLine | **不做**（Codex CLI 不支援，issue #17827 open 中）。Codex 維持 hook only |
| UI 擺放 | **Host → Agents tab**（per-agent 卡新增 Extensions 區），Hooks tab 維持原樣 |
| Wrap 衝突處理 | 偵測既有 statusLine → 對話框 **Wrap / Cancel**（**不提供 Replace**） |
| 顯示用途（PR-1 範圍） | **僅 tab name + bottom status bar 切換鈕左側**；cost / context / rate UI 後續 phase |
| Setting toggle | 沿用既有 `showOscTitle`（"Show agent dynamic title"），**不分 cc/codex** |
| Daemon rebuild 範圍 | **本機 only**；結構對齊 app update 為跨機 pull 留口、本 PR 不做 |
| PR 切法 | Approach 2：A+B 合在 PR-1，C 為獨立 PR-2 |

---

# PR-1：CC StatusLine Wrapper + Installer

## 1. 資料流總覽

```
Claude Code (跑在 tmux pane 裡)
  │ reads ~/.claude/settings.json:
  │   statusLine.command = "<pdx> statusline-proxy [--inner '<original>']"
  │ spawns subprocess every ≥300ms (CC 內建 debounce)
  ▼
pdx statusline-proxy (Go subcommand)
  - read stdin JSON (5s timeout)
  - if --inner present: exec inner, capture stdout
  - print stdout (CC 顯示用)
  - synchronous POST stdin JSON → daemon
    (config.Load() 拿 url+token, 2s timeout, fail silent)
  - exit 0 always
  │
  │ POST /api/agent/status
  ▼
Daemon (agent module)
  - 收 status payload
  - tmux_session_name → sessionId（沿用既有 mapping）
  - 廣播 WS event: agent.status (per-session)
  │
  │ WebSocket
  ▼
SPA useAgentStore
  - merge full snapshot into agent[hostId][sessionId].status
  - 觸發 tab title selector + bottom status bar selector
```

**設計不變式**
- **零 env 注入**：wrapper 走 `config.Load()` + `tmux display-message`，與 hook installer 對稱
- **POST 永不阻塞 CC**：fire-and-forget + 2s timeout + silent fail
- **wrapper exit 0 always**：CC 看不到 Purdex 線上與否
- **session 對應重用既有機制**

---

## 2. `pdx statusline-proxy` Subcommand

### CLI

```
pdx statusline-proxy [--inner "<original-command>"]
```

### 行為

| 階段 | 動作 |
|---|---|
| 1. Read stdin | 完整 stdin JSON，**5s 讀取 timeout**（避免 CC 的 bug 讓 wrapper 永遠卡住、阻塞下一次 debounce）。空或讀失敗 → `{}` |
| 2. Inner exec（有 `--inner`） | `sh -c "<inner>"`（用 `sh` 比 `bash` 更可攜），stdin 餵同一份 JSON，timeout 2s。stdout 收集；stderr 丟棄；exit code 忽略 |
| 3. Print stdout | 沒 `--inner`：印 default minimal；有 `--inner`：原樣印 inner stdout |
| 4. **Synchronous** POST | `POST /api/agent/status`，2s HTTP timeout，silent fail。**必須同步**：goroutine + `os.Exit(0)` 會在 POST 完成前殺掉 goroutine。2s 對 CC 300ms debounce 可承受（最差下一輪才 POST 新 snapshot） |
| 5. Exit | 永遠 `0` |

### Default minimal status

```
[pdx] {model.display_name|model.id} · ctx {used_percentage}% · ${total_cost_usd}
```
缺欄位省略；純文字無 ANSI（後續 PR 加色）。

### POST payload

```json
{
  "tmux_session": "abc-session-name",
  "agent_type": "cc",
  "raw_status": { /* CC 原 stdin JSON 完整原樣 */ }
}
```
與 hook payload 對稱（`tmux_session` + `agent_type` + `raw_*`）。

### 錯誤處理（hard rule）

- **daemon 不通（連線拒絕 / DNS 失敗 / 5xx）**：POST silent fail，**stdout 仍正常印 inner/default**；CC 看到的狀態列內容完全不受影響、只是 Purdex SPA 看不到 update
- binary panic、任何 IO 錯：**全部吞掉、exit 0、stdout 仍須印出 inner/default**

### 測試

- stdin 空 → `{}` POST
- 有 `--inner` → 印 inner stdout
- daemon 不通 → 仍印 stdout、exit 0
- 缺欄位 → minimal 模板正確 fallback

---

## 3. Daemon `/api/agent/status` Endpoint

### Route

```
POST /api/agent/status
```

新 endpoint，**不擴** `/api/agent/event`（status 是高頻狀態、event 是離散事件，handler 邏輯分岔）。

### Auth

Bearer token via `Authorization: Bearer <cfg.Token>` — **loopback 也必須帶**（防本機其他 user / process 偽造）。Wrapper 透過 `config.Load("")` 從 daemon config 讀取 token（與既有 hook 相同路徑）。

### Request

```go
type StatusPayload struct {
    TmuxSession string          `json:"tmux_session"`
    AgentType   string          `json:"agent_type"`  // "cc"
    RawStatus   json.RawMessage `json:"raw_status"`  // CC statusLine JSON 原樣
}
```

### 處理流程

1. 驗證 bearer + parse；`agent_type ∉ {"cc"}` → 400
2. `tmux_session` → `sessionId`，重用既有 hook mapping。**找不到回 200 不廣播**（避免 retry，statusLine 高頻、找不到通常是 session 剛關）
3. In-memory 儲存最新一份：`map[sessionId]*StatusPayload`，**不寫 DB**（高頻、純展示、重啟後下一份 update 自然取代）
4. 廣播 WS：對該 host 所有連線 SPA 推
   ```json
   { "type": "agent.status", "session_id": "...", "agent_type": "cc", "status": { /* raw_status */ } }
   ```
5. 回 `200 OK` + `{}`

### 新 WS 連線 snapshot replay

新 SPA 連線時（WebSocket subscription 建立），daemon 對該 host 所有有 cached snapshot 的 session 推一次 `agent.status` event，確保新連線立即看到最新狀態而不是等下一輪 CC tick。邏輯掛在既有 hook snapshot replay 旁（相同 WS subscribe entry point）。

### 卸載廣播（配合 Section 6）

`POST /api/agent/cc/statusline/setup { action: "remove" }` 成功後：
- daemon 清掉 `map[sessionId]*StatusPayload` 中所有**該 host** 的 entries
- 廣播 `agent.status.cleared` event：
  ```json
  { "type": "agent.status.cleared", "host_id": "...", "agent_type": "cc" }
  ```
- SPA 收到後清掉該 host 所有 session 的 `oscTitles` / `ccStatus`，避免殘留顯示

### Backpressure

CC 預設 debounce 300ms，正常頻率不高。不額外 server-side rate limit。日後失控加 per-session 100ms throttle。

### 錯誤處理

- bearer 錯：401（wrapper silent fail）
- payload malformed：400
- session 不對應：200（吞掉、不廣播）
- WS 廣播失敗：log warn、不影響 200 回應

### 測試

- valid payload → 200 + WS 廣播
- session 不對應 → 200 + 無廣播
- 缺 bearer → 401
- WS client 收 broadcast、format 正確

---

## 4. SPA Store + Tab/Status Bar Display

### 術語

- **tab name**：Purdex 顯示的 tab 名稱
- **cc session name**：CC `/rename` 設定的（statusLine `session_name` 欄位）
- **tmux session name**：tmux 的，現有 fallback

### Setting

沿用既有 `showOscTitle`（`useAgentStore` 中），UI label "Show agent dynamic title"，位於 Settings → Terminal。**不分 cc/codex**（Codex 不支援 statusLine、本 PR 也不為 Codex 增加任何資料來源，這條 toggle 對 Codex 永遠沒效果，是 Codex 自己的限制）。

### Store 改寫

把 statusLine 來源餵入既有 `oscTitles` field（OSC dead-end 後實際空著沒在用），語意改為「agent 動態 title 來源」。完整 statusLine raw payload 額外存（給後續 phase 用）：

```ts
type AgentState = {
  oscTitles: Record<string, string>  // 既有，現由 statusLine 餵入 cc session name
  ccStatus: Record<string, {
    receivedAt: number
    raw: Record<string, unknown>     // CC statusLine payload 原樣保留
  }>  // 新增
  // ...其他既有欄位不動
}
```

key 格式：`${hostId}:${sessionName}`（與既有 `oscTitles` 一致）。

### WS handler 寫入邏輯

新增 `agent.status` WS event handler（在既有 agent store 的 WS subscriber 旁）：

1. 收到 event → `ccStatus[key] = { receivedAt: Date.now(), raw: status }`
2. `setOscTitle(key, status.session_name ?? '')` — 既有 `setOscTitle` 傳空字串時**會自動移除** entry（見 `useAgentStore.ts:171-181`）；沒有獨立 `removeOscTitle` API

新增 `agent.status.cleared` WS event handler（配合後端卸載廣播）：

1. 收到 event → 該 host 所有 `oscTitles` / `ccStatus` entries 全清

### Tab Name 顯示規則

| 條件 | 顯示 |
|---|---|
| `showOscTitle == true` AND host 已裝 wrapper AND **cc session name 非空** | `{cc session name} - {tmux session name}` |
| 否則 | `{tmux session name}` |

`tmux session name` 不論預設或使用者 rename，都是同一個概念，無額外分支。

> ⚠️ **既有行為變更**：當前 `spa/src/components/InlineTab.tsx:60` 顯示**只** `oscTitle`（cc-only），組合字串僅在 tooltip 呈現。本 PR 改為 visible tab text 直接顯示 `{cc} - {tmux}`，**會使 tab 寬度受限時截斷得更早**。這是刻意的 UX 變更（配合 hover tooltip 反 truncate 的設計），需在 PR 描述中標注、加視覺 regression 測試 case。

### Tab Hover

使用 **`<HoverTooltip>` 元件**（PR-1 prep task 從 `ActivityBarNarrow.tsx:85-87` 的 `ws-tooltip` span 抽出：CSS-only、`group-hover:opacity-100`、定位 prop 可配置）。顯示**完整 tab name 字串**（即 `{cc session name} - {tmux session name}` 或 `{tmux session name}`），用於反 truncate（tab 寬度有限會截斷）。

**不使用 HTML 原生 `title=""`**（當前 `InlineTab.tsx:61` 使用、要一起換掉）。

### Bottom Status Bar 顯示

位置：bottom status bar 右側、terminal/stream 切換按鈕的**左邊**。

**重要：這個 span 已經存在**（`spa/src/components/StatusBar.tsx:200-208`，`data-testid="osc-title"`），目前由舊 `oscTitles` store 驅動（OSC dead-end 後實際空著）。**不新增元件**，只是 statusLine WS handler 接上 `setOscTitle` 後資料自動從新來源餵入，此 span 的顯示邏輯與位置完全不動。

| 條件 | 顯示 |
|---|---|
| `showOscTitle == true` AND host 已裝 wrapper AND **cc session name 非空** | `{cc session name}`（**只 cc，不串 tmux**） |
| 否則 | **整個元素隱藏**（既有 `{oscTitle && ...}` conditional 已是這個行為） |

無 tooltip。

### 降級

- wrapper 未裝 / setting 未開 / cc session name 空 → tab name 走 tmux fallback、status bar 元素隱藏
- WS 斷線 → 保留最後一份 cc session name 不主動清除（避免閃爍）

### 測試

- store：snapshot merge、key 格式、`receivedAt` 更新
- tab name selector：四種降級情境（未裝 / 未開 / 空 cc session name / 完整）字串輸出正確
- status bar component：條件正確 + 位置對（在 toggle button 左邊）
- tab title hover：tooltip 內容正確、用對元件
- 整合：mock `agent.status` WS event → tab title + status bar DOM 正確更新

---

## 5. Agents Tab UI — Extensions 區

### 位置與原則

擴展 `spa/src/components/hosts/AgentsSection.tsx` 的 agent 卡。**不動既有卡內容**（detection 結果），**附加** Extensions 區於下方。

### 渲染條件

- `cc` → 渲染 Extensions 區，含 Status integration row
- `codex` → **不渲染** Extensions 區（不顯示 "Not supported" 字樣）
- 未來其他 agent → 看是否有對應擴展，沒就不渲染

### 版型

```
┌──────────────────────────────────────────────────────────┐
│ ✓ Claude Code (claude)                       Installed   │
│   version: 2.1.90                                        │
│   path: /opt/homebrew/bin/claude                         │
│                                                          │
│   Extensions:                                            │
│   ─────────────────────────────────────────────────────  │
│   Status integration       [Installed (wrap)]  [Remove] │
└──────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────┐
│ ✓ Codex CLI (codex)                          Installed   │
│   version: 0.42.1                                        │
│   path: /opt/homebrew/bin/codex                          │
│                                                          │
│   (無 Extensions 區)                                     │
└──────────────────────────────────────────────────────────┘
```

### Status badge 與 action（CC）

| State | Badge | Action |
|---|---|---|
| `none` | "Not installed" | `[Install]` |
| `pdx` | "Installed" | `[Remove]` |
| `wrapped` | "Installed (wrap)" + tooltip 顯示 inner cmd | `[Remove]` |
| `error` | "Config error" + tooltip 顯示原因 | `[Retry]` |

### Install 流程

```
[Install] click
   ▼
GET /api/agent/cc/statusline/status
   │
   ├── current.mode == 'unmanaged'（已有別人裝的）
   │      ▼ Conflict Dialog
   │      ┌────────────────────────────────────────────┐
   │      │ Existing statusLine detected:              │
   │      │   command: <existing 全文>                 │
   │      │                                            │
   │      │ Wrap mode: pdx 會 call 既有 command,       │
   │      │ forward 它的 output 給 CC, 同時 background  │
   │      │ POST status 給 Purdex.                     │
   │      │                                            │
   │      │             [Cancel]   [Wrap]              │
   │      └────────────────────────────────────────────┘
   │      ├── Wrap → POST /setup { action:'install', mode:'wrap', inner:<existing> }
   │      └── Cancel → 關閉 dialog
   │
   └── 沒有 existing → POST /setup { action:'install', mode:'pdx' }
```

### Remove 流程

```
[Remove] click → Confirm "Remove pdx statusLine integration?"
   ├── Confirm → POST /setup { action:'remove' }
   │            （wrap 過的還原 inner、純 pdx 整個刪掉 statusLine 欄位）
   └── Cancel → 無動作
```

### SPA 元件

- `AgentsSection.tsx`（擴展） — 渲染 Extensions 區
- `AgentExtensionRow.tsx`（新） — 單一擴展 row，先承載 statusline，未來其他擴充也用同一個
- `StatuslineConflictDialog.tsx`（新） — 衝突對話框
- `useStatuslineInstall.ts`（新 hook） — GET/POST + state machine（idle / loading / installed / error）

### i18n

加 `hosts.extensions.*` namespace，依既有 pattern。

### 測試

- AgentsSection：CC 卡顯示 Extensions 區、Codex 卡不顯示
- AgentExtensionRow：四種 badge 狀態 → 對應按鈕正確
- StatuslineConflictDialog：渲染 + 按鈕觸發 callback
- useStatuslineInstall：mock fetch，install/uninstall/refresh 流程

---

## 6. Install/Uninstall Lifecycle — Daemon Endpoint + settings.json

### Endpoint

```
GET  /api/agent/cc/statusline/status
POST /api/agent/cc/statusline/setup   body: { action, mode?, inner? }
```

Namespace `/api/agent/`（不是 `/api/hooks/`，statusline 概念上不是 hook）。

### `GET /status` 回應

```json
{
  "mode": "none" | "pdx" | "wrapped" | "unmanaged",
  "installed": true,
  "innerCommand": "ccstatusline --format compact",
  "rawCommand": "/opt/homebrew/bin/pdx statusline-proxy",
  "settingsPath": "/Users/u/.claude/settings.json"
}
```

`innerCommand` 只在 `wrapped` / `unmanaged` 時出現。

### Mode 判定

讀 `~/.claude/settings.json` 的 `statusLine.command`：

| 內容 | mode |
|---|---|
| 欄位不存在、`statusLine` 為 null、或非物件（如 string） | `none`（視為可安全覆寫） |
| tokenize 後 argv[0] basename == `pdx` (或 `pdx.exe`) 且 argv[1] == `statusline-proxy` 且 argv 長度 2 | `pdx` |
| 同上但 argv[2] == `--inner` 且有 argv[3] | `wrapped`（`inner` = argv[3]） |
| 其他 | `unmanaged`（`inner` = 整段 command 原文） |

**用 `github.com/mattn/go-shellwords` tokenize**（不用正則）— 正則無法正確處理 POSIX single-quote escape `'\''`（含單引號的 inner 會被誤 parse）。

**不綁定當前 daemon executable 絕對路徑**（避免 binary 被移動後偵測不到），只要 argv[0] basename 是 `pdx` / `pdx.exe` 即視為我方安裝。寫入時則固定用 `os.Executable()` 取的絕對路徑。

### `POST /setup` 請求

```json
{ "action": "install" | "remove", "mode": "pdx" | "wrap", "inner": "..." }
```

#### Action `install`

- `mode=pdx`：寫 `{ "type": "command", "command": "<pdx-abs> statusline-proxy" }`
- `mode=wrap`：寫 `{ "type": "command", "command": "<pdx-abs> statusline-proxy --inner '<shell-quoted-inner>'" }`

#### Action `remove`

- 當前 `pdx` → 整個 `statusLine` key 刪掉
- 當前 `wrapped` → command 還原為 inner 原文，**保留** `type` 與既有 `padding` 欄位
- 當前 `unmanaged` → **拒絕**（409 + error message），避免誤刪使用者自裝的
- 當前 `none` → no-op，回 200

#### 回應

`200 OK` + 新的 status payload（與 GET 同形）；錯誤 `4xx` + `{ error: "..." }`。

### settings.json 操作細節

| 項目 | 行為 |
|---|---|
| 路徑 | `~/.claude/settings.json`（與既有 hook installer **共用**路徑解析函數） |
| 讀取 | 不存在視為空物件；parse 失敗回 500 + error |
| 寫入 | **atomic**：寫 temp file → `rename()`；保留檔案權限 |
| 其他欄位保護 | **不動** `hooks` / `mcp` / 其他欄位 |
| 缺檔時 install | 建立空物件 + statusLine 欄位後寫入 |
| Inner command quoting | POSIX shell single-quote escape（`'` → `'\''`） |

### Idempotency

- `install { mode: pdx }` 當前已是 pdx → no-op，回 200
- `remove` 當前是 none → no-op，回 200

### 併發防護

`POST /setup` handler 內用 **per-host file mutex**（read-modify-write settings.json 全程持有），避免兩個 SPA 同時 install 時 race。atomic rename 只保證檔案不半寫，不保證邏輯順序正確；mutex 保證 last-writer-wins 語意 + 兩邊都會看到 consistent status 回應。

### Go 模組位置

- `internal/agent/cc/paths.go`（新 — **PR-1 prep**）— 抽出 `ccSettingsPath()` helper（目前 `hooks.go` 多處 inline `filepath.Join(home, ".claude", "settings.json")`）
- `internal/agent/cc/statusline.go`（新） — reader / writer / 偵測 / mode 判斷
- `internal/agent/cc/hooks.go`（微調） — 改用 `ccSettingsPath()` helper
- `internal/module/agent/handler.go`（擴） — 兩個 route + per-host file mutex

### 測試

#### Unit（`internal/agent/cc/statusline_test.go`）

- mode 判定：none / pdx / wrapped / unmanaged 四個 case（含 `statusLine` 為 string / null / 非物件 → none）
- install pdx 於空 settings
- install pdx 於已有其他欄位（hooks / mcp 保留）
- install wrap：inner 含 space、單引號、`&` 等 shell 特殊字元 → quote 正確
- install wrap + remove round-trip：inner 含 `it's`, `a "b"`, `foo 'bar' baz` → 還原與原值 byte-perfect 相等（go-shellwords tokenize 驗證）
- remove pdx → statusLine 欄位消失、其他欄位保留
- remove wrapped → inner 還原、type/padding 保留
- remove unmanaged → 拒絕 + error
- 缺檔 install → 建立新檔
- atomic write：模擬中斷不留半寫檔
- 併發 install：兩個 goroutine 同時 call setup → 結果 consistent（mutex 驗證）

#### Integration（`internal/module/agent/handler_test.go`）

- GET/POST 四個 mode 組合
- bearer auth
- Round trip install → status → remove → status

---

# PR-2：Daemon Dev Rebuild

## 7. 範圍與目標

在 `Settings → Development` 新增 "Daemon" 區塊（既有只有 SPA / Electron app 兩區），支援**本機 rebuild + 安全 restart**。結構對齊既有 app update，**為未來跨機 pull 留口但本 PR 不做**。

## 8. Endpoint

```
GET  /api/dev/daemon/check      → { current_hash, latest_hash, available }
POST /api/dev/daemon/rebuild    → SSE stream of build log + final "restarting" event
```

Namespace `/api/dev/daemon/`，與既有 `/api/dev/update/`（app）並列。

### `GET /check`

比對 daemon baked-in hash 與 `git log -1 --format=%H` 最新 commit：

```json
{ "current_hash": "9dfe8fbf", "latest_hash": "a7c3d2e1", "available": true }
```

**CWD 假設**：daemon 必須在 repo root 啟動，或 config / env 指向 repo 路徑（與既有 `/api/dev/update/check` 相同要求）。

### `POST /rebuild`

SSE stream，事件類型：

- `log` — `{ "line": "..." }` 逐行 build output
- `error` — `{ "message": "..." }` build 失敗
- `success` — `{ "new_hash": "..." }` build 成功，即將 exec
- `restarting` — 最後一個事件，daemon exec self 前發送

## 9. Build + Restart 流程

```
1. Old daemon 收 POST /rebuild
2. 開啟 SSE stream；in-flight HTTP 不受影響
3. 執行 `go build -o bin/pdx.new ./cmd/pdx`（**5 分鐘 timeout**，鏡射既有 app build 的 `defaultBuild` 時間）
   ├─ 過程每行 stdout/stderr → SSE "log" event
   ├─ 失敗 / timeout → SSE "error" + 關閉 stream，老 daemon 繼續服務
   └─ 成功 → 下一步
4. `os.Rename("bin/pdx.new", "bin/pdx")` — 原子替換（同 FS）
5. SSE 推 "success" + "restarting" 事件，關閉 stream
6. Old daemon `syscall.Exec("bin/pdx", os.Args, os.Environ())`
7. New daemon 啟動、rebind port、接上既有 tmux sessions（tmux server 不受影響）
8. SPA WS 偵測斷線 → 既有 reconnect 機制重連（1-2s）
9. SPA 收新連線 hello 內含新 build hash → UI 顯示 "Daemon updated to <hash>"
```

## 10. 不變式

| 項目 | 保證 |
|---|---|
| tmux sessions | 完全不受影響（tmux server 外部獨立） |
| Active CC/Codex processes | 不受影響（跑在 tmux pane 裡） |
| SPA in-flight 操作 | WS 斷幾秒，既有 reconnect 邏輯處理 |
| Build 失敗 | Daemon **不重啟**，老 binary 繼續服務 |
| Rename 失敗 | Daemon **不重啟**（`pdx.new` 留著當訊號） |
| exec 失敗 | Daemon crash（運維層面：看 log） |

## 11. SPA UI（`DevEnvironmentSection.tsx` 擴展）

於既有 App 區之後新增 **Daemon** 區：

```
┌─ Daemon ────────────────────────────────────────────┐
│  Current hash: 9dfe8fbf                             │
│  Latest hash:  a7c3d2e1   [Update available]        │
│                                                     │
│  [Check Update]  [Rebuild & Restart]                │
│                                                     │
│  Build log (live):                                  │
│  ┌───────────────────────────────────────────────┐  │
│  │ > go build -o bin/pdx.new ./cmd/pdx           │  │
│  │ building pdx...                               │  │
│  │ ...                                           │  │
│  │ ✓ Build complete, restarting daemon...        │  │
│  └───────────────────────────────────────────────┘  │
│                                                     │
│  Status: Restarting — reconnecting WS...            │
└─────────────────────────────────────────────────────┘
```

**狀態機**：`idle → checking → available/up-to-date → rebuilding (streaming) → restarting → reconnected (back to checking)`

**Reconnect 偵測**：WS 重連後取 `/api/dev/daemon/check`，若 `current_hash` 變了顯示 toast "Daemon updated to <short-hash>"。

## 12. 安全 Guards

- **server-side gating**：daemon 啟動時讀取 `PDX_DEV_UPDATE=1` env var；未啟用時**整個 dev module 不註冊 route**（既有 `/api/dev/update/*` + 新的 `/api/dev/daemon/*` 一起覆蓋）
  - 當前現況：既有 `internal/module/dev/module.go` 沒有 server-side gate，僅 Electron preload 端 gate（`electron/preload.ts:122-123`）— **本 PR 一併補洞**
  - 行為：daemon 未帶 `PDX_DEV_UPDATE=1` 啟動 → 兩組 dev endpoint 都回 404
- bearer auth（與其他 daemon API 一致）
- 同時只允許一個 daemon rebuild 進行（per-process mutex）
- Build 不阻塞其他 request（goroutine）

## 13. Go 模組位置

- `internal/module/dev/daemon.go`（新） — build / rebuild 邏輯、SSE handler
- `internal/module/dev/module.go`（擴） — 新 route registration + **補 `PDX_DEV_UPDATE` server-side gate**（module register 時條件性掛 route）
- `cmd/pdx/daemon.go`（改） — 監聽 socket 明確設 `SO_REUSEADDR`（透過 `net.ListenConfig{ Control: setReuseAddr }`）+ 啟動時若遇 `EADDRINUSE` 做 exponential backoff retry bind（最多 5 次、間隔 200ms → 1s）— 處理 exec 後 kernel TIME_WAIT 等 race

**明確放棄的方案**：不用 listener fd 繼承（`extraFiles`）— 複雜度不符 MVP 收益。`SO_REUSEADDR` + retry loop 對 macOS dev 環境足夠；未來若遇真實生產 restart 再升級到 fd 繼承。

## 14. 測試

- Unit: `go build` 呼叫 wiring（mock `exec.Command`）
- Unit: hash 偵測（mock git 輸出）
- Unit: SSE event 格式
- Unit: 同時觸發兩個 rebuild → 第二個被拒
- **手動驗證**（PR 描述列出）：改一行 log → rebuild → daemon 新 log 出現 → SPA WS 重連

---

# Out of Scope（兩 PR 共同延後）

- Codex CLI statusline（Codex 不支援，等 OpenAI issue #17827）
- Cost / context / rate UI dashboard（PR-1 只做 tab name + status bar 切換鈕左側）
- ANSI color in default minimal status
- Cross-machine wrapper deployment（CC 是跑在 daemon 同 host 的 tmux 裡）
- 跨機 daemon binary push（PR-2 結構為此留口，本 PR 只做本機）
- statusline 寫進 DB / 持久化（PR-1 只 in-memory）
- Hooks tab 重構（不動既有 Hooks tab，承擔短期分裂）

---

# 後續 Plan

兩 PR 各自走既有開發流程：
1. PR-1 先做（user-facing 完整功能、無依賴）
2. PR-2 並行或後做（純 dev experience 改善、與 PR-1 正交）
3. 每個 PR 走 TDD + 兩輪 PR review（既有流程）

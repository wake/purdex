# Agent MCP Control Plane 設計規格

> 日期：2026-04-15
> 狀態：Draft（探索階段，尚未進入實作規劃）
> 最後更新：2026-04-17（review 討論後更新）

## 概述

為 Purdex 建立一套 MCP（Model Context Protocol）介面層，讓運行在 Purdex Tab 內的 AI Agent 能夠完整掌握整個系統——讀取所有 module 資料、操作所有 module 功能、取得 UI 視覺輸出。Agent 可以以「輔助管理」或「全權託管」模式運作，透過單一 MCP 連線控制跨 host、跨 workspace 的完整 Purdex 環境。

## 設計目標

1. **Agent 可讀取任何 module 的資料**——session 列表、agent 狀態、stream 歷史、第三方服務資料
2. **Agent 可操作任何 module 的功能**——建立 session、切換 mode、管理 workspace/tab、發送 tmux 指令
3. **Agent 可取得視覺輸出**——terminal 文字內容、UI 元件截圖、整頁截圖
4. **Module 自治**——每個 module 自行宣告暴露給 Agent 的能力，新增 module 時 Agent 能力自動擴展
5. **Multi-host 透通**——Agent 透過單一 MCP 連線即可操作所有已註冊的 host

## 核心架構

### 系統拓撲

```
┌─────────────────────────────────────────────────────────┐
│ Purdex Tab (CC instance)                                │
│   MCP config: http://<daemon>:7860/mcp/                 │
└──────────────┬──────────────────────────────────────────┘
               │ MCP (Streamable HTTP)
               ▼
┌─────────────────────────────────────────────────────────┐
│ Go Daemon (pdx)                                         │
│                                                         │
│  ┌────────────────────────────────────────────────────┐ │
│  │ MCP Endpoint (/mcp/)              via mcp-go       │ │
│  │  ← 從 Module Capability Registry 動態建立 tool 清單 │ │
│  └──────────────┬─────────────────────────────────────┘ │
│                 │                                        │
│  ┌──────────────▼─────────────────────────────────────┐ │
│  │ Module Capability Registry（動態）                   │ │
│  │  Actions[] / VisualOutputs[]                        │ │
│  └──────────────┬─────────────────────────────────────┘ │
│                 │                                        │
│  ┌──────────┐ ┌┴─────────┐ ┌──────────┐ ┌───────────┐  │
│  │ Session  │ │ Agent    │ │ Stream   │ │ 3rd-party │  │
│  │ Module   │ │ Module   │ │ Module   │ │ Module *  │  │
│  └──────────┘ └──────────┘ └──────────┘ └───────────┘  │
│                                                         │
│  ┌────────────────────────────────────────────────────┐ │
│  │ Multi-host Proxy                                    │ │
│  │  Local module (直接呼叫) / Remote host (REST proxy) │ │
│  │  Per-request timeout: 5s / Circuit breaker: 3 fails │ │
│  └────────────────────────────────────────────────────┘ │
│                                                         │
│  ┌────────────────────────────────────────────────────┐ │
│  │ SPA Dispatch Relay                                  │ │
│  │  Executor="spa" 的 action → WS 轉發給 primary SPA  │ │
│  │  Timeout: 10s / WS 斷線 = fail-fast all pending     │ │
│  └────────────────────────────────────────────────────┘ │
└──────────────┬──────────────────────────────────────────┘
               │ WS (雙向指令通道)
               ▼
┌─────────────────────────────────────────────────────────┐
│ SPA / Electron                                          │
│  ┌────────────────────────────────────────────────────┐ │
│  │ Dispatch Handler                                   │ │
│  │  ~10 個泛用 SPA 指令，不隨 module 增長              │ │
│  └────────────────────────────────────────────────────┘ │
│  ┌────────────────────────────────────────────────────┐ │
│  │ Capture Worker                                     │ │
│  │  Electron: webContents.capturePage()               │ │
│  │  SPA: html2canvas fallback                         │ │
│  └────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘

* 未來第三方 module 範例
```

### 設計決策紀錄

| 決策 | 選項 | 理由 |
|------|------|------|
| MCP server 位置 | Go daemon 內建（mcp-go）| Self-contained binary、無 subprocess 管理、直接呼叫 module method、增量僅 ~1000 行 |
| 曾考慮方案 | TS bridge subprocess | MCP TS SDK 較成熟、可與 SPA 共用 type。但 subprocess 運維成本過高（打包 Node.js runtime、crash recovery、port 衝突、log 整合），且 bridge 只做 JSON 轉發，type 共用優勢不成立 |
| MCP 傳輸 | Streamable HTTP | CC 原生支援 `--transport http`，推薦的遠端連線方式。CC 透過 `claude mcp add --transport http purdex http://<daemon>:7860/mcp/` 或 `.mcp.json` 設定 |
| 能力宣告模式 | Daemon module 單一宣告源（VS Code Extension Host 模式）| MCP endpoint 只需問 daemon 即可知道一切，SPA 是執行者不是宣告者 |
| MCP Resources | **不使用**——全部統一用 MCP Tools | MCP Resources 在生態系中採用率極低（大多數 server/client 只用 Tools），且 Agent 透過 Tool 主動查詢更直覺。唯讀查詢與有副作用的操作都是 Tool，在 description 裡標明即可 |
| SPA dispatch 指令 | ~10 個泛用指令，不隨 module 增長 | 避免 per-module 指令膨脹和 SPA 端 plugin 註冊管理問題。新增 module 使用 `view.open({ moduleType, params })` 等泛用指令 |
| SPA dispatch 通訊 | 全 WS 雙向（指令 + 回應同一條連線）| 避免混用 WS + REST 導致斷線時 correlation 遺失 |
| Dispatch contract | Daemon 送 high-level 語意參數，SPA adapter 翻譯成 store call | Daemon 不接觸 SPA 內部型別（Tab、PaneLayout 等），SPA adapter 是唯一接觸 Zustand store 的地方，TypeScript compiler 完整檢查 |

### 資料流模式

所有 Agent 操作走同一條路徑，不因操作類型而分歧：

```
Agent → MCP Streamable HTTP → Daemon /mcp/ → Module method
                                           → (spa action) WS → SPA → WS 回傳
```

兩種能力的執行端分佈：

| 能力類型 | MCP 對應 | 宣告端 | 執行端 |
|----------|----------|--------|--------|
| Actions（資料面）| MCP Tools | Daemon module | Daemon 直接執行 |
| Actions（UI 面）| MCP Tools | Daemon module | Daemon → WS → local SPA |
| Visual outputs | MCP Tools（回傳 image 或 text）| Daemon module | Daemon 直接（tmux）或 → WS → SPA |

## Module 能力註冊

### 設計原則

**Daemon module 是能力的單一宣告源**（VS Code Extension Host 模式）。SPA 是部分能力的執行者，但不自行宣告能力。MCP endpoint 從 capability registry 動態建立 tool 清單。

### Go 介面

```go
// AgentCapability 是 Module 可選實作的介面
// 未實作此介面的 module 不會暴露任何 Agent 能力
type AgentCapability interface {
    // Actions 回傳此 module 可執行的操作（含唯讀查詢）
    Actions() []ActionDef

    // VisualOutputs 回傳此 module 可產出的視覺輸出
    VisualOutputs() []VisualOutputDef
}

type ActionDef struct {
    Name        string    // e.g. "session.create"
    Description string    // 給 Agent 看的說明
    ParamType   any       // Go struct，init 時自動轉 JSON Schema
    Executor    string    // "daemon" | "spa"
}

type VisualOutputDef struct {
    Name        string    // e.g. "session.terminal_capture"
    Description string
    Executor    string    // "daemon"（tmux capture-pane）| "spa"（截圖）
}
```

> **Parameters 型別安全**：`ParamType` 是 Go struct（非 `json.RawMessage`），init 時透過 `github.com/invopop/jsonschema` 自動產生合法 JSON Schema。避免手寫 schema 字串的 drift 和格式錯誤。

### 動態能力發現

MCP endpoint **不在啟動時一次性 snapshot**，改為動態 registry。Module Ready 後自行註冊，支援遲到的 module（例如等遠端 API 連線的第三方 module）：

```go
// Module Init 完成後，自行註冊能力
func (m *OutlineModule) Init(core *Core) error {
    go func() {
        m.connectOutlineAPI()  // 等遠端連線就緒
        core.MCPRegistry().Register(m.Name(), m)
        core.MCPRegistry().NotifyToolsChanged()  // 通知已連線的 CC
    }()
    return nil
}
```

`NotifyToolsChanged()` 觸發 MCP `notifications/tools/list_changed`，已連線的 CC 自動重新取得 tool 清單。

### Module 範例：Session Module

```go
type CreateSessionParams struct {
    Name string `json:"name" jsonschema:"required"`
    Cwd  string `json:"cwd,omitempty"`
    Mode string `json:"mode,omitempty" jsonschema:"enum=terminal,enum=stream"`
}

type SendKeysParams struct {
    Session string `json:"session" jsonschema:"required"`
    Keys    string `json:"keys" jsonschema:"required"`
}

func (m *SessionModule) Actions() []ActionDef {
    return []ActionDef{
        {
            Name:        "session.list",
            Description: "列出所有 tmux session 及其狀態",
            Executor:    "daemon",
        },
        {
            Name:        "session.create",
            Description: "建立新的 tmux session",
            ParamType:   CreateSessionParams{},
            Executor:    "daemon",
        },
        {
            Name:        "session.send_keys",
            Description: "向 session 發送按鍵序列（⚠️ 非冪等）",
            ParamType:   SendKeysParams{},
            Executor:    "daemon",
        },
        {
            Name:        "session.open_tab",
            Description: "在指定 workspace 開啟此 session 的 tab",
            ParamType:   OpenTabParams{},
            Executor:    "spa",
        },
    }
}

func (m *SessionModule) VisualOutputs() []VisualOutputDef {
    return []VisualOutputDef{
        {
            Name:        "session.terminal_capture",
            Description: "取得 terminal 的文字內容（tmux capture-pane）",
            Executor:    "daemon",  // daemon 直接執行，不經 SPA
        },
    }
}
```

## MCP Endpoint

### 職責

1. **能力翻譯**——將 module capability registry 翻譯為 MCP protocol 的 `tools/list` 回應
2. **請求路由**——根據 `ActionDef.Executor` 決定直接呼叫 module method 還是轉發 SPA dispatch
3. **Multi-host 路由**——`Executor="daemon"` 的 action 自動注入 `host` 參數，非 local 的請求 proxy 到遠端 daemon
4. **Visual 中繼**——截圖請求透過 WS dispatch channel 觸發 SPA capture，等待回傳

### 技術選型

- **Library**：`mcp-go`（github.com/mark3labs/mcp-go）
- **傳輸**：Streamable HTTP，掛在 daemon 的 `/mcp/` path
- **生命週期**：隨 daemon 啟動/停止，與其他 endpoint 平行
- **CC 設定**：`claude mcp add --transport http purdex http://<daemon>:7860/mcp/` 或 `.mcp.json`

### Multi-host 路由

**僅 `Executor="daemon"` 的 action** 自動注入 `host` 參數（預設為 local host）。`Executor="spa"` 的 action 不注入 `host`——SPA 只有一個（使用者面前的那個），不存在遠端路由的語意。

> **⚠️ 待討論**：`Executor="spa"` 的 action 若需要知道「資料來自哪個 host」（例如 `session.open_tab` 要開遠端 session 的 tab），應由 module 自行在 ParamType 裡宣告明確欄位（如 `hostId`），不混用 `host` routing 參數。此決策尚未最終確認，見「討論進度」節。

```
Agent 呼叫: session.create({ host: "air-2019", name: "test" })
  → Executor="daemon" + host != local
  → proxy POST http://100.64.0.1:7860/api/sessions

Agent 呼叫: session.open_tab({ sessionCode: "test", hostId: "air-2019" })
  → Executor="spa"（無 host routing）
  → WS dispatch: view.open({ moduleType: "tmux-session", params: { hostId: "air-2019", code: "test" }})
  → Local SPA 開 tab 顯示遠端 session
```

### 可靠性機制

| 機制 | 規格 |
|------|------|
| SPA dispatch timeout | 10 秒，超時回傳 `spa_timeout` error |
| WS 斷線處理 | 立即 fail-fast 所有 pending dispatch，回傳 `spa_disconnected` |
| 多 SPA 連線 | WS 握手帶 `client_type`（electron/browser），daemon 維護 primary SPA 指標。Electron 優先。Dispatch unicast 到 primary |
| Remote proxy timeout | 5 秒 per-request |
| Circuit breaker | 連續 3 次 remote timeout → host 標記 `unreachable`，直接回錯不等待 |

## SPA Dispatch 層

### 設計原則

SPA 提供 **~10 個泛用指令**，不隨 module 增長。新增 module 使用泛用指令（如 `view.open`）搭配 `moduleType` 參數，不需要新增 SPA 端的指令或 handler。

### WS 雙向指令格式

擴展現有的 `/ws/host-events` WebSocket，新增 `dispatch` 和 `dispatch_result` 事件類型：

**Daemon → SPA（指令）：**

```json
{
  "type": "dispatch",
  "id": "req-uuid",
  "action": "view.open",
  "args": {
    "region": "primary-sidebar",
    "moduleType": "outline",
    "params": { "docId": "123" }
  }
}
```

**SPA → Daemon（回應，同一條 WS）：**

```json
{
  "type": "dispatch_result",
  "id": "req-uuid",
  "success": true,
  "data": { "viewId": "view-5" }
}
```

### 泛用 SPA 指令

```typescript
const spaCommands = {
  // View 操作（適用所有 module）
  'view.open':        // 在指定 region 開啟 module view
  'view.close':       // 關閉 view

  // Tab 操作
  'tab.open':         // 開 tab（帶 content type + params，SPA adapter 建構 Tab 物件）
  'tab.close':        // 關 tab
  'tab.focus':        // 切換 focus

  // Workspace 操作
  'workspace.create': // 建 workspace
  'workspace.switch': // 切 workspace

  // Layout 操作
  'layout.toggle':    // 開關 region
  'layout.split':     // 分割 pane

  // 查詢 & 截圖
  'ui.getState':      // 查詢 UI 狀態快照（JSON）
  'ui.capture':       // 截圖
}
```

每個指令內部有 adapter 負責翻譯成 Zustand store 操作：

```typescript
// tab.open 的 adapter — Daemon 只送業務參數，SPA 建構完整 Tab 物件
handlers['tab.open'] = (args: { sessionCode: string, hostId?: string, workspaceId?: string }) => {
  const tab = buildTabForSession(args.sessionCode, args.hostId)
  const wsId = args.workspaceId ?? useWorkspaceStore.getState().activeWorkspaceId
  useTabStore.getState().addTab(tab)
  return { tabId: tab.id }
}
```

### Visual Capture

截圖請求透過 `ui.capture` 指令：

```json
{
  "type": "dispatch",
  "id": "cap-uuid",
  "action": "ui.capture",
  "args": { "target": "fullpage" }
}
```

執行策略依環境與目標自動選擇：

| 目標 | 機制 | 保真度 | 經過 SPA |
|------|------|--------|---------|
| Terminal 文字 | `tmux capture-pane` | 純文字 | 否（daemon VisualOutput, Executor="daemon"）|
| UI 元件 | Electron `capturePage({ rect })` | 像素完美 | 是 |
| UI 元件（SPA） | html2canvas | CSS 部分失真 | 是 |
| 整頁 | Electron `capturePage()` | 像素完美 | 是 |

> **Image 回傳**：MCP tool result 支援 `{ type: "image", data: "<base64>", mimeType: "image/png" }`。CC 是否正確處理需實作前驗證（spike test）。Fallback：存檔案回傳路徑。

## Agent Sidebar

### 概念

一個跨 workspace 的全域 sidebar module，作為 Agent 的互動介面。使用者透過此 sidebar 與 Agent 對話，Agent 透過 MCP tools 操作 Purdex。

### 運作模式

| 模式 | 說明 |
|------|------|
| **輔助模式** | Agent 回答問題、提供建議，操作需使用者確認 |
| **託管模式** | Agent 獲得全權，可自主建立 workspace、啟動 session、部署工作流程 |

### 使用場景範例

```
使用者：「幫我建一個前後端開發環境」

Agent 執行（託管模式）：
1. session.create({ name: "fe-dev" })
2. session.create({ name: "be-dev" })
3. workspace.create({ name: "Frontend", icon: "browser" })
4. workspace.create({ name: "Backend", icon: "gear" })
5. tab.open({ sessionCode: "fe-dev", workspaceId: "Frontend" })
6. tab.open({ sessionCode: "be-dev", workspaceId: "Backend" })
7. session.send_keys({ session: "fe-dev", keys: "claude -p 'init react project'" })
8. session.send_keys({ session: "be-dev", keys: "claude -p 'init go api'" })
```

## 第三方 Module 支援

任何實作 `AgentCapability` 介面的 daemon module 都自動獲得 Agent 整合能力，無論資料來源是 tmux、本地檔案、或第三方 API。第三方 module 使用泛用 SPA 指令，不需要在 SPA 端註冊 per-module handler。

### 範例：Outline Module（文件管理）

```go
func (m *OutlineModule) Actions() []ActionDef {
    return []ActionDef{
        {Name: "outline.search", Description: "搜尋文件", ParamType: OutlineSearchParams{}, Executor: "daemon"},
        {Name: "outline.create_doc", Description: "建立新文件", ParamType: OutlineCreateParams{}, Executor: "daemon"},
        {Name: "outline.open_panel", Description: "在 sidebar 開啟文件面板", ParamType: OutlineOpenParams{}, Executor: "spa"},
    }
}
```

`outline.open_panel` 的 Executor="spa"，daemon 翻譯為泛用 SPA 指令：

```json
{ "action": "view.open", "args": { "region": "primary-sidebar", "moduleType": "outline", "params": { "docId": "123" } } }
```

Agent 操作 Outline 與操作 Session 走完全相同的路徑，零差異。

## 安全性備註（後續設計）

權限控制不在本 spec 的範圍內，但預留以下設計空間：

- Module 可為每個 ActionDef 標記 `permission_level`（read / write / admin）
- MCP endpoint 可實作 per-session 的能力白名單
- 輔助模式 vs 託管模式的差異可透過 MCP endpoint 層的 tool filtering 實現
- Multi-host 場景下，遠端 host 的操作可要求額外授權
- WS dispatch channel 需整合現有 ticket auth 機制

## 討論進度

### 已確認的 Review 修正

三輪 subagent review（MCP 協定層 / 分散式系統 / 開發者體驗）共提出 21 項質疑，逐項討論後的處理結果：

#### 架構前提驗證

| 項目 | 結論 |
|------|------|
| P1: CC 不支援 Streamable HTTP | **不成立**——CC 原生支援 `--transport http`，官方推薦的遠端連線方式 |
| P2: MCP Resources 對 Agent 無意義 | **部分成立**——Resources 在生態系採用率極低，全部改用 Tools |
| P3: CC 是否處理 tool result image | **不確定**——需 spike test 驗證，有 fallback（存檔案回傳路徑）|

#### 已納入 Spec 的修正

| 項目 | 修正內容 |
|------|---------|
| D1: WS dispatch 可靠性 | 新增 timeout (10s)、WS 斷線 fail-fast、primary SPA 策略 |
| D2: Multi-host proxy failure | 新增 per-request timeout (5s)、circuit breaker (3 fails) |
| D4: Dispatch contract 型別安全 | Daemon 送 high-level 語意參數，SPA adapter 翻譯成 store call |
| D5: Plugin handler 生命週期 | 改為 ~10 個泛用 SPA 指令，不需要 per-module plugin 註冊 |
| D7: Module 啟動順序 race | 改為動態 registry + `NotifyToolsChanged()` |
| D8: Parameters json.RawMessage | 改為 Go struct + jsonschema 自動產生 |

#### 已建立 Issue 追蹤

| 項目 | Issue |
|------|-------|
| D3: 非冪等操作 retry 風險 | [#383](https://github.com/wake/purdex/issues/383) — 探索階段暫不處理 |

#### 討論中（待繼續）

| 項目 | 狀態 |
|------|------|
| **D6: host 參數自動注入語意** | 傾向方案 B（按 Executor 區分：daemon action 注入 host，spa action 不注入），但 spa action 若需指定資料來源 host（如開遠端 session 的 tab），改用 module 自行宣告的欄位（如 `hostId`）。**尚未最終確認** |

#### 延後處理

| 項目 | 說明 |
|------|------|
| N1: MCP Sampling 缺失 | 「託管模式」的自主決策依賴 CC 自身的 agentic loop，Purdex 只提供 tools。若需 daemon 主動詢問 Agent，待後續設計 |
| N2: SPA/daemon 版本不一致 | 泛用指令減少了 contract surface，降低版本 drift 風險，但未完全解決 |
| N3: Capture 競態 | 使用者操作中截圖可能捕捉中間狀態，建議優先使用 tmux capture-pane 文字模式 |
| N4: Debug tracing | 需要 correlation ID 貫穿 MCP → daemon → WS → SPA 四層 log |
| N5: Dispatch 路由表穩定性 | 泛用指令 + adapter 層提高穩定性，但 adapter 仍與 store 簽名耦合 |

## 未解決問題

1. **Visual capture 的座標系統**——SPA 如何知道某個 module 渲染在哪個位置、尺寸多大
2. **Agent Sidebar 的 CC instance 管理**——如何自動設定 MCP config、啟動、重啟。CC 透過 `claude mcp add` 或 `.mcp.json` 設定，需與 session module 的生命週期整合
3. **Stream mode 整合**——Agent 是走 terminal mode 還是 stream mode？與現有 stream 功能的關係
4. **託管模式的 partial failure**——連續 action 中途失敗時，已執行步驟的副作用處理策略
5. **Image content 驗證**——CC 是否正確處理 MCP tool result 的 image content，需 spike test
6. **mcp-go 驗證**——Streamable HTTP server 能力、tools/list_changed notification 支援度，需 spike test

# Agent MCP Control Plane 設計規格

> 日期：2026-04-15
> 狀態：Draft（探索階段，尚未進入實作規劃）

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
│   claude --mcp-server http://<daemon>:7860/mcp/         │
└──────────────┬──────────────────────────────────────────┘
               │ MCP (Streamable HTTP)
               ▼
┌─────────────────────────────────────────────────────────┐
│ Go Daemon (pdx)                                         │
│                                                         │
│  ┌────────────────────────────────────────────────────┐ │
│  │ MCP Endpoint (/mcp/)              via mcp-go       │ │
│  │  ← 從 Module Capability Registry 建立 tool/resource │ │
│  └──────────────┬─────────────────────────────────────┘ │
│                 │                                        │
│  ┌──────────────▼─────────────────────────────────────┐ │
│  │ Module Capability Registry                          │ │
│  │  Actions[] / Resources[] / VisualOutputs[]          │ │
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
│  └────────────────────────────────────────────────────┘ │
│                                                         │
│  ┌────────────────────────────────────────────────────┐ │
│  │ SPA Dispatch Relay                                  │ │
│  │  Executor="spa" 的 action → WS 轉發給 SPA          │ │
│  └────────────────────────────────────────────────────┘ │
└──────────────┬──────────────────────────────────────────┘
               │ WS (雙向指令通道)
               ▼
┌─────────────────────────────────────────────────────────┐
│ SPA / Electron                                          │
│  ┌────────────────────────────────────────────────────┐ │
│  │ Dispatch Handler                                   │ │
│  │  內建 UI 原語 + Plugin 註冊機制                      │ │
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
| 能力宣告模式 | Daemon module 單一宣告源（VS Code Extension Host 模式）| Bridge 只需問 daemon 即可知道一切，SPA 是執行者不是宣告者 |
| SPA dispatch | 通用 UI 原語 + plugin 註冊 | 內建原語不隨 module 增長，第三方 module 透過 plugin 機制擴展 |
| UI action 的 host 語意 | 永遠在 local SPA 執行 | SPA 只有一個（使用者面前的那個），`Executor="spa"` 的 action 不隨 host 參數路由到遠端 |

### 資料流模式

所有 Agent 操作走同一條路徑，不因操作類型而分歧：

```
Agent → MCP Streamable HTTP → Daemon /mcp/ → Module method
                                           → (spa action) WS → SPA → WS 回傳
```

三種能力的執行端分佈：

| 能力類型 | MCP 對應 | 宣告端 | 執行端 |
|----------|----------|--------|--------|
| Actions（資料面）| MCP Tools | Daemon module | Daemon 直接執行 |
| Actions（UI 面）| MCP Tools | Daemon module | Daemon → WS → local SPA |
| Resources | MCP Resources | Daemon module | Daemon（或轉問 SPA）|
| Visual outputs | MCP Tools（回傳 image）| Daemon module | Daemon → WS → local SPA |

**UI action host 規則**：`Executor="spa"` 的 action 永遠由 local SPA 執行，不隨 `host` 參數路由。Agent 呼叫 `session.open_tab({ host: "air-2019", session: "test" })` 時，daemon 在 local SPA 開 tab 顯示遠端 session，而非要求遠端 host 的 SPA 開 tab。

## Module 能力註冊

### 設計原則

**Daemon module 是能力的單一宣告源**（VS Code Extension Host 模式）。SPA 是部分能力的執行者，但不自行宣告能力。MCP endpoint 從 capability registry 建立完整的 tool/resource 清單。

### Go 介面

```go
// AgentCapability 是 Module 可選實作的介面
// 未實作此介面的 module 不會暴露任何 Agent 能力
type AgentCapability interface {
    // Actions 回傳此 module 可執行的操作
    Actions() []ActionDef

    // Resources 回傳此 module 可提供的資料
    Resources() []ResourceDef

    // VisualOutputs 回傳此 module 可產出的視覺輸出
    VisualOutputs() []VisualOutputDef
}

type ActionDef struct {
    Name        string            // e.g. "session.create"
    Description string            // 給 Agent 看的說明
    Parameters  json.RawMessage   // JSON Schema
    Executor    string            // "daemon" | "spa"
}

type ResourceDef struct {
    URI         string            // e.g. "purdex://sessions"
    Name        string
    Description string
    MimeType    string            // "application/json" | "text/plain"
}

type VisualOutputDef struct {
    Name        string            // e.g. "session.terminal_capture"
    Description string
    Target      string            // 截圖目標描述
}
```

### 能力發現

MCP endpoint 啟動時遍歷所有 module，對實作 `AgentCapability` 介面者收集能力宣告，建立 MCP tool/resource 註冊表。

```go
for _, mod := range core.Modules() {
    if ac, ok := mod.(AgentCapability); ok {
        registry.RegisterActions(mod.Name(), ac.Actions())
        registry.RegisterResources(mod.Name(), ac.Resources())
        registry.RegisterVisualOutputs(mod.Name(), ac.VisualOutputs())
    }
}
```

### Module 範例：Session Module

```go
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
            Parameters:  schema(`{"name": "string", "command?": "string"}`),
            Executor:    "daemon",
        },
        {
            Name:        "session.send_keys",
            Description: "向 session 發送按鍵序列",
            Parameters:  schema(`{"session": "string", "keys": "string"}`),
            Executor:    "daemon",
        },
        {
            Name:        "session.open_tab",
            Description: "在指定 workspace 開啟此 session 的 tab",
            Parameters:  schema(`{"session": "string", "workspace?": "string"}`),
            Executor:    "spa",
        },
    }
}

func (m *SessionModule) Resources() []ResourceDef {
    return []ResourceDef{
        {
            URI:         "purdex://sessions",
            Name:        "Session list",
            Description: "所有 session 的狀態快照",
            MimeType:    "application/json",
        },
    }
}

func (m *SessionModule) VisualOutputs() []VisualOutputDef {
    return []VisualOutputDef{
        {
            Name:        "session.terminal_capture",
            Description: "取得 terminal 的文字內容（tmux capture-pane）",
        },
    }
}
```

## MCP Endpoint

### 職責

1. **能力翻譯**——將 module capability registry 翻譯為 MCP protocol 的 `tools/list` 和 `resources/list` 回應
2. **請求路由**——根據 `ActionDef.Executor` 決定直接呼叫 module method 還是轉發 SPA dispatch
3. **Multi-host 路由**——所有 tool 自動注入 `host` 參數（預設 local），非 local 的請求 proxy 到遠端 daemon
4. **Visual 中繼**——截圖請求透過 WS dispatch channel 觸發 SPA capture，等待回傳

### 技術選型

- **Library**：`mcp-go`（github.com/mark3labs/mcp-go）
- **傳輸**：Streamable HTTP，掛在 daemon 的 `/mcp/` path
- **生命週期**：隨 daemon 啟動/停止，與其他 endpoint 平行

### Multi-host 路由

所有 MCP tool 自動注入 `host` 參數（預設為 local host）：

```
Agent 呼叫: session.create({ host: "air-2019", name: "test" })

路由邏輯:
  if host == local → 直接呼叫 SessionModule.Create()
  if host != local → proxy POST http://100.64.0.1:7860/api/sessions

Agent 呼叫: session.open_tab({ host: "air-2019", session: "test" })

路由邏輯:
  Executor="spa" → 永遠 local SPA
  → WS dispatch: tab.addTab({ content: { type: "tmux-session", hostId: "air-2019", code: "test" }})
```

## SPA Dispatch 層

### 設計原則

SPA 提供一組**內建 UI 原語**處理核心 layout/tab/workspace 操作，同時提供 **plugin 註冊機制**讓第三方 module 擴展 dispatch 能力。

### WS 雙向指令格式

擴展現有的 `/ws/host-events` WebSocket，新增 `dispatch` 事件類型：

**Daemon → SPA（指令）：**

```json
{
  "type": "dispatch",
  "id": "req-uuid",
  "action": "tab.addTab",
  "args": {
    "workspaceId": "ws-1",
    "content": { "type": "tmux-session", "code": "fe-dev" }
  }
}
```

**SPA → Daemon（回應，同一條 WS）：**

```json
{
  "type": "dispatch_result",
  "id": "req-uuid",
  "success": true,
  "data": { "tabId": "tab-3" }
}
```

> **設計決策**：指令和回應都走同一條 WS 連線，避免混用 WS + REST 導致斷線時 correlation 遺失。

### 內建 UI 原語

核心 layout/tab/workspace 操作，不隨 module 增長：

```typescript
const builtinHandlers: Record<string, DispatchHandler> = {
  // Tab
  'tab.addTab':          (args) => useTabStore.getState().addTab(args),
  'tab.closeTab':        (args) => useTabStore.getState().closeTab(args),
  'tab.focusTab':        (args) => useTabStore.getState().setActiveTab(args),

  // Workspace
  'workspace.create':    (args) => useWorkspaceStore.getState().createWorkspace(args),
  'workspace.switch':    (args) => useWorkspaceStore.getState().setActiveWorkspace(args),

  // Layout
  'layout.toggleRegion': (args) => useLayoutStore.getState().toggleRegion(args),
  'layout.splitPane':    (args) => useTabStore.getState().splitPane(args),

  // View state
  'view.getState':       () => buildViewStateSnapshot(),
}
```

### Plugin 註冊機制

第三方 module 的 SPA 元件可註冊自己的 dispatch handler：

```typescript
// SPA 端的 dispatch plugin registry
const dispatchRegistry = {
  handlers: new Map<string, DispatchHandler>(),

  register(action: string, handler: DispatchHandler) {
    this.handlers.set(action, handler)
  },

  resolve(action: string): DispatchHandler | undefined {
    return builtinHandlers[action] ?? this.handlers.get(action)
  },
}

// 第三方 module 在自己的 SPA 元件中註冊
// e.g. OutlinePanel.tsx
dispatchRegistry.register('outline.open_panel', (args) => {
  useLayoutStore.getState().addView('primary-sidebar', {
    type: 'outline',
    docId: args.docId,
  })
})
```

這樣 daemon module 宣告 `Executor="spa"` 的 action 時，對應的 SPA 元件自己負責註冊 handler，dispatch 核心不需要知道第三方 module 的細節。

### Visual Capture

截圖請求同樣透過 WS dispatch channel：

```json
{
  "type": "dispatch",
  "id": "cap-uuid",
  "action": "capture",
  "args": {
    "target": "session.terminal_capture",
    "session": "fe-dev"
  }
}
```

執行策略依環境與目標自動選擇：

| 目標 | 機制 | 保真度 | 經過 SPA |
|------|------|--------|---------|
| Terminal 文字 | `tmux capture-pane` | 純文字 | 否（daemon 直接執行）|
| UI 元件 | Electron `capturePage({ rect })` | 像素完美 | 是 |
| UI 元件（SPA） | html2canvas | CSS 部分失真 | 是 |
| 整頁 | Electron `capturePage()` | 像素完美 | 是 |

> **未開啟的 Tab/View**：若目標 module 目前未渲染（Tab 未開啟），capture 應回傳明確錯誤而非空白圖片。Terminal 文字類的 capture 不受此限（daemon 直接走 tmux）。

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
5. tab.addTab({ workspace: "Frontend", content: tmux-session("fe-dev") })
6. tab.addTab({ workspace: "Backend", content: tmux-session("be-dev") })
7. session.send_keys({ session: "fe-dev", keys: "claude -p 'init react project'" })
8. session.send_keys({ session: "be-dev", keys: "claude -p 'init go api'" })
```

## 第三方 Module 支援

任何實作 `AgentCapability` 介面的 daemon module 都自動獲得 Agent 整合能力，無論資料來源是 tmux、本地檔案、或第三方 API。

### 範例：Outline Module（文件管理）

**Daemon 端**（Go）：

```go
func (m *OutlineModule) Actions() []ActionDef {
    return []ActionDef{
        {Name: "outline.search", Description: "搜尋文件", Executor: "daemon"},
        {Name: "outline.create_doc", Description: "建立新文件", Executor: "daemon"},
        {Name: "outline.open_panel", Description: "在 sidebar 開啟文件面板", Executor: "spa"},
    }
}

func (m *OutlineModule) Resources() []ResourceDef {
    return []ResourceDef{
        {URI: "purdex://outline/docs", Name: "Documents", MimeType: "application/json"},
    }
}
```

**SPA 端**（React）：

```typescript
// OutlinePanel.tsx — module 自己註冊 dispatch handler
useEffect(() => {
  dispatchRegistry.register('outline.open_panel', (args) => {
    useLayoutStore.getState().addView('primary-sidebar', {
      type: 'outline',
      docId: args.docId,
    })
  })
  return () => dispatchRegistry.unregister('outline.open_panel')
}, [])
```

Agent 操作 Outline 與操作 Session 走完全相同的路徑，零差異。

## 安全性備註（後續設計）

權限控制不在本 spec 的範圍內，但預留以下設計空間：

- Module 可為每個 ActionDef 標記 `permission_level`（read / write / admin）
- MCP endpoint 可實作 per-session 的能力白名單
- 輔助模式 vs 託管模式的差異可透過 MCP endpoint 層的 tool filtering 實現
- Multi-host 場景下，遠端 host 的操作可要求額外授權
- WS dispatch channel 需整合現有 ticket auth 機制

## 未解決問題

1. **Visual capture 的座標系統**——SPA 如何知道某個 module 渲染在哪個位置、尺寸多大。可能方案：SPA 主動回報可截圖區域的 registry，或 capture 時動態查詢 DOM
2. **Agent Sidebar 的 CC instance 管理**——如何自動設定 MCP server config（`--mcp-server http://localhost:7860/mcp/`）、啟動、重啟。需與現有 session module 的生命週期整合
3. **Stream mode 整合**——Agent 是走 terminal mode 還是 stream mode？與現有 stream 功能的關係
4. **能力變更的即時同步**——module 動態載入/卸載時如何更新 MCP tool 清單。MCP protocol 支援 `notifications/tools/list_changed`，但觸發時機需設計。短期可靠 daemon 重啟解決
5. **錯誤處理與 timeout**——SPA dispatch 無回應（SPA 未連線、tab 未開啟）時的降級策略
6. **託管模式的 partial failure**——連續 action 中途失敗時，已執行步驟的副作用處理策略

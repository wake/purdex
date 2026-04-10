# Quick Commands Module 設計

## 概述

Quick Commands 是一個可插拔的指令快捷系統。任何模組可以透過 `registerModule` 貢獻 commands，任何 UI 元件可以透過 `useCommands()` hook 取用並執行。

指令存儲分兩層：全域 + per-host（per-host 覆蓋全域同 ID 指令）。執行透過 daemon API `POST /api/sessions/{code}/send-keys` 送進 tmux session。

## 資料模型

```ts
interface QuickCommand {
  id: string
  name: string
  command: string          // 實際送進 terminal 的字串
  icon?: string            // Phosphor icon name
  category?: string        // 'agent' | 'shell' | 'custom' | ...
  hostOnly?: boolean       // true = 只在特定 host 顯示
}
```

## 儲存架構

```
QuickCommandStore (purdex persist)
├── global: QuickCommand[]
└── byHost: Record<hostId, QuickCommand[]>
```

合併邏輯：`byHost[hostId]` 有同 `id` 的指令 → 用 per-host 版本，否則 fallback 到 global。

### Store API

```ts
interface QuickCommandState {
  global: QuickCommand[]
  byHost: Record<string, QuickCommand[]>

  // CRUD
  addCommand: (cmd: QuickCommand, hostId?: string) => void
  updateCommand: (id: string, patch: Partial<QuickCommand>, hostId?: string) => void
  removeCommand: (id: string, hostId?: string) => void

  // 合併查詢
  getCommands: (hostId: string) => QuickCommand[]
}
```

## Extension Point：模組貢獻 commands

擴展現有 `registerModule` 的 `ModuleDefinition`：

```ts
interface ModuleDefinition {
  // ...existing fields (id, name, pane, views, workspaceConfig)
  commands?: CommandContribution[]
}

interface CommandContribution {
  id: string
  name: string
  command: string | ((ctx: CommandContext) => string)
  icon?: string
  category?: string
}

interface CommandContext {
  hostId: string
  workspaceId?: string | null
  moduleConfig?: Record<string, unknown>  // workspace module config，從 useWorkspaceStore 取得
}
```

動態 command 範例（files 模組根據工作目錄產生 cd 指令）：

```ts
registerModule({
  id: 'files',
  commands: [{
    id: 'cd-project',
    name: 'cd 專案目錄',
    command: (ctx) => `cd ${ctx.moduleConfig?.projectPath ?? '~'}`,
    icon: 'FolderOpen',
    category: 'shell',
  }],
})
```

## 消費端 Hook

```ts
function useCommands(filter: { hostId: string; workspaceId?: string | null }): ResolvedCommand[]

interface ResolvedCommand {
  id: string
  name: string
  command: string          // 已 resolve 完的最終字串
  icon?: string
  category?: string
  source: 'store' | string // 'store' 或 module id
}
```

Hook 內部做三件事：
1. 從 `QuickCommandStore.getCommands(hostId)` 取 store commands（source: `'store'`）
2. 從 module registry 收集所有 `commands` contribution，以當前 context resolve 動態 command（source: module id）
3. 合併為單一陣列，store commands 排前面。兩者各自獨立，不做 id 去重。

## 執行路徑

### 後端 API（主要路徑）

```
POST /api/sessions/{code}/send-keys
Body: { "keys": "claude -p ...\n" }
```

Handler 在 session module，呼叫 `tmux.SendKeysRaw(sessionName, keys...)`（不自動加 Enter）。前端負責在 command 末尾加 `\n`。這讓呼叫端完全控制是否執行指令。不需要 terminal pane 開著。

### 前端呼叫

```ts
async function executeCommand(hostId: string, sessionCode: string, command: string): Promise<void> {
  await hostFetch(hostId, `/api/sessions/${sessionCode}/send-keys`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ keys: command }),
  })
}
```

## 預設 commands

首次安裝提供兩個全域預設（放在 store 的 `createDefaultState` 中，hydration 後已有資料的使用者不受影響）：

| id | name | command | category |
|----|------|---------|----------|
| `start-cc` | Start Claude Code | `claude -p --verbose --output-format stream-json` | agent |
| `start-codex` | Start Codex | `codex` | agent |

## UI 觸發點

Quick Commands 不自己擁有獨立頁面，而是被各元件透過 `useCommands` 嵌入：

1. **Terminal pane header** — 下拉選單，選擇後送進當前 session
2. **Host > Sessions 列表** — 每個 session row 的 action 按鈕
3. **Workspace 資訊面板** — 顯示 workspace context 相關的指令（如 cd 專案目錄）
4. **未來擴展** — command palette、快捷鍵觸發

本 PR 實作 #1 和 #2。#3 和 #4 留待後續。

## 檔案結構

```
spa/src/
├── stores/useQuickCommandStore.ts     # 全域/per-host 儲存
├── hooks/useCommands.ts               # 合併 store + module contributions
├── lib/execute-command.ts             # executeCommand() helper
├── lib/module-registry.ts             # 擴展 ModuleDefinition (commands field)
└── components/
    └── QuickCommandMenu.tsx           # 共用下拉選單元件

internal/module/session/
└── handler.go                         # 新增 handleSendKeys
```

## 不做的事

- 不做 command 歷史記錄
- 不做 command 排程/定時執行
- 不做 command 參數化模板（`${variable}` 插值）— 動態 command 用 function 處理
- 不做獨立的 Quick Commands 設定頁面（設定整合在 Host > Agents 頁面）

# Phase 6: Hooks 統一架構設計

> 日期：2026-04-06
> 涉及 issues：#150, #109, #108, #103, #142, #127

## 目標

將 tmux hooks 與 agent hooks 的 API 和 UI 統一為模組化架構，使新增 hook module（如未來 codex）只需加一個 config，不需改動框架。

## 現況問題

1. Daemon API 不一致：tmux 用 `/api/hooks/*`（3 個端點），agent 用 `/api/agent/hook-*`（2 個端點）
2. HooksSection 只接 tmux hooks API，agent hooks 是 hardcoded stub
3. `useHookStatus` 孤兒 hook — 有完整 agent API 整合但無人 import
4. App.tsx 散落 hook-status init fetch，且 `hooksInstalled` 已無運行時消費者
5. 錯誤靜默吞掉，無 UI 回饋

---

## 設計

### 1. Daemon：統一 Hooks API

#### 新 URL Pattern

```
GET  /api/hooks/{module}/status
POST /api/hooks/{module}/setup    { "action": "install" | "remove" }
```

各 module 在自己的 `RegisterRoutes` 中註冊具體路徑（per-module explicit registration），不是 generic router dispatch。新增 module 時在該 module 的 `RegisterRoutes` 加路由即可。

| module | 歸屬 | 說明 |
|--------|------|------|
| `tmux` | SessionModule | tmux session lifecycle hooks |
| `cc` | AgentModule | Claude Code CLI hooks |
| `codex` | （未來） | Codex CLI hooks |

#### 向後相容

Alpha 階段不保留舊路由。SPA 與 daemon 同步升版，跨機場景（Air .app）透過 dev update 機制同步。舊路由直接移除。

#### SessionModule 變更

`hooks.go` 改動：

- **移除** `handleHooksInstall`、`handleHooksRemove` 兩個 handler
- **新增** `handleTmuxHookSetup`：接受 `{ "action": "install" | "remove" }`，統一入口；執行完後回傳完整 status 結構（同 `handleTmuxHookStatus`），使 SPA 可用模式 A 單次 round-trip 更新
- **修改** `handleHooksStatus` → `handleTmuxHookStatus`
  - 回傳格式對齊統一結構
  - 移除 `agent_hooks: false` stub
  - `installed` 語義為 **ALL**（3 個 tmux 事件全部安裝才回 true），與 CC module 一致
- **路由**：`GET /api/hooks/tmux/status` + `POST /api/hooks/tmux/setup`

#### AgentModule 變更

`handler.go` + `module.go` 改動：

- **修改** 路由：`GET /api/agent/hook-status` → `GET /api/hooks/cc/status`
- **修改** 路由：`POST /api/agent/hook-setup` → `POST /api/hooks/cc/setup`
- **保留** `POST /api/agent/event` 不動（event 接收不是 hooks 管理 API）
- 回傳格式不變（已符合統一結構；`agent_type` 為 CC-specific 擴充欄位，SPA 端忽略）

#### 統一回傳結構

```jsonc
{
  "installed": true,           // 全部事件皆已安裝（ALL 語義）
  "events": {                  // 各事件安裝狀態
    "event-name": {
      "installed": true,
      "command": "..."         // 選填，cc module 回傳實際 command
    }
  },
  "issues": []                 // 診斷訊息（選填）
}
```

tmux module 目前沒有 `issues`，初始回傳空陣列即可。各 module 可在此結構上擴充 module-specific 欄位（如 CC 的 `agent_type`），SPA 統一介面忽略擴充欄位。

### 2. SPA：HookModule 介面

```typescript
// spa/src/lib/hook-modules.ts

interface HookModuleEvent {
  installed: boolean
  command?: string | null
}

interface HookModuleStatus {
  installed: boolean
  events: Record<string, HookModuleEvent>
  issues?: string[]
}

interface HookModule {
  id: string                    // 'tmux' | 'cc'
  labelKey: string              // i18n key for display name
  descKey: string               // i18n key for description
  fetchStatus: (hostId: string) => Promise<HookModuleStatus>
  setup: (hostId: string, action: 'install' | 'remove') => Promise<HookModuleStatus>
  getLastTrigger?: (hostId: string) => Record<string, number> | null
}
```

`getLastTrigger` 為選填方法，各 module 自行決定如何取得最後觸發時間。CC module 從 `useAgentStore.events` 讀取 `broadcast_ts`，tmux module 回傳 `null`。這避免 `HookModuleCard` 直接依賴 CC-specific 的 agent store，保持抽象完整。

#### 模組定義

```typescript
// spa/src/lib/hook-modules.ts

async function hookFetch(hostId: string, path: string, init?: RequestInit): Promise<HookModuleStatus> {
  const res = await hostFetch(hostId, path, init)
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

const TMUX_HOOKS: HookModule = {
  id: 'tmux',
  labelKey: 'hosts.tmux_hooks',
  descKey: 'hosts.tmux_hooks_desc',
  fetchStatus: (hostId) => hookFetch(hostId, '/api/hooks/tmux/status'),
  setup: (hostId, action) =>
    hookFetch(hostId, '/api/hooks/tmux/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    }),
}

const CC_HOOKS: HookModule = {
  id: 'cc',
  labelKey: 'hosts.agent_hooks',
  descKey: 'hosts.agent_hooks_desc',
  fetchStatus: (hostId) => hookFetch(hostId, '/api/hooks/cc/status'),
  setup: (hostId, action) =>
    hookFetch(hostId, '/api/hooks/cc/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    }),
  getLastTrigger: (hostId) => {
    // 注意：此處非 React 上下文，須用 getState() 而非 hook 語法
    const events = useAgentStore.getState().events
    // 從 events 中按 event_name 歸類最近的 broadcast_ts
    // 只取 key 以 hostId: 開頭的 entries
    // 回傳 Record<eventName, timestamp>
  },
}

export const HOOK_MODULES: HookModule[] = [TMUX_HOOKS, CC_HOOKS]
```

Hooks API 的 fetch 邏輯內聯在 `hook-modules.ts` 而非 `host-api.ts`，因為 hooks API 與 module config 天然耦合，集中管理比分散兩層更直觀。新增 module 時只需在此檔案加一個 config 物件。

### 3. SPA：useModuleHook custom hook

```typescript
// spa/src/hooks/useModuleHook.ts

function useModuleHook(module: HookModule, hostId: string, refreshKey: number) {
  const [status, setStatus] = useState<HookModuleStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // fetch on mount + refreshKey/hostId 變更時重新 fetch
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    module.fetchStatus(hostId)
      .then(data => { if (!cancelled) setStatus(data) })
      .catch(err => { if (!cancelled) setError(err.message) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [module, hostId, refreshKey])

  // setup: 直接用回傳值更新 status（模式 A，單次 round-trip）
  const setup = async (action: 'install' | 'remove') => {
    setLoading(true)
    setError(null)
    try {
      const data = await module.setup(hostId, action)
      setStatus(data)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
    setLoading(false)
  }

  const lastTrigger = module.getLastTrigger?.(hostId) ?? null

  return { status, loading, error, setup, lastTrigger }
}
```

關鍵設計決策：

- **每次 mount = fresh fetch**：直接看伺服器狀態，不走 global store
- **`setup()` 用回傳值更新 status**（模式 A）：daemon 的 `handleHookSetup` / `handleTmuxHookSetup` 執行完後回傳最新 status，不需額外 fetch
- **`error` 暴露為 `string | null`**：不吞錯，UI 可顯示（解決 #103）
- **`cancelled` flag 防止 race condition**：hostId 切換或 unmount 時不回填 stale state
- **`refreshKey`**：由父元件控制，變更時觸發所有 card 重新 fetch

### 4. SPA：HooksSection 重構

```
HooksSection({ hostId })
├── refreshKey state (number)
├── 頁面標題 + 全局 Refresh 按鈕 (onClick: setRefreshKey(k => k + 1))
└── HOOK_MODULES.map(module =>
      <HookModuleCard key={module.id} module={module} hostId={hostId} refreshKey={refreshKey} />
    )

HookModuleCard({ module, hostId, refreshKey })
├── 使用 useModuleHook(module, hostId, refreshKey)
├── 標題 + StatusBadge (installed/not)
├── 描述文字
├── 事件清單（各事件安裝狀態 + lastTrigger 時間）
├── Install / Remove 按鈕
└── Error 訊息（如有）
```

- `StatusBadge` 從 HooksSection 提取為共用元件
- 每個 card 獨立管理自己的 loading/error 狀態
- 全局 Refresh 按鈕透過 `refreshKey` 觸發所有 card 重新 fetch

### 5. 清除死路徑

| 刪除項目 | 原因 |
|----------|------|
| `useAgentStore.hooksInstalled` 欄位 | 無運行時消費者 |
| `useAgentStore.setHooksInstalled()` | 同上 |
| `App.tsx` hook-status init useEffect | 被 HooksSection mount fetch 取代 |
| `spa/src/hooks/useHookStatus.ts` | 被 `useModuleHook` 取代 |
| `host-api.ts` 舊函式：`fetchHooksStatus`, `installHooks`, `removeHooks`, `fetchAgentHookStatus`, `setupAgentHook` | 被 `hook-modules.ts` �� `hookFetch` 取代 |

### 6. #127 修復：Agent label modelName 持久化

`useAgentStore.handleHookEvent` 中，當收到帶 `modelName` 的事件（通常是 `SessionStart`）時，將 model name 存入獨立欄位：

```typescript
interface AgentState {
  // 新增
  models: Record<string, string>  // composite key → model name
}
```

`models` 不 persist（和 `events`/`statuses` 一樣是 ephemeral 狀態），WS 重連時 snapshot replay 的 `SessionStart` 事件會重新填入。

`getAgentLabel()` 改為接受 composite key，從 store 直接讀 `models[key]`：

```typescript
// 舊：getAgentLabel(event: AgentHookEvent | undefined): string | null
// 新：getAgentLabel(key: string): string | null
export function getAgentLabel(key: string): string | null {
  const model = useAgentStore.getState().models[key]
  return model || null
}
```

呼叫端（`StatusBar.tsx` 等）改為傳 composite key 而非 event 物件。清除時機：`SessionEnd` 清除對應 `models[key]`，`removeHost()` 同樣過濾 `models`（與 `events`/`statuses`/`unread`/`activeSubagents` 一致）。

### 7. #142 hook 最後觸發時間

不需要改 daemon。`broadcast_ts` 已是 daemon 端事件發生時間（`time.Now().UnixNano()`），和 `agent_events.updated_at` 同源。

SPA 側由 `HookModule.getLastTrigger()` 提供：CC module 從 `useAgentStore.events` 中按 hostId 篩選，歸類各 event_name 的最近 `broadcast_ts`，回傳 `Record<eventName, timestamp>`。`HookModuleCard` 在事件清單旁顯示相對時間（如「3 分鐘前」）。

Tmux module 的 `getLastTrigger` 不實作（回傳 null），未來若需要可擴充。

---

## 不在範圍

- Codex module 實作（未來）
- `POST /api/agent/event` 路徑不變（event 接收歸 agent module 職責）
- Upload API 路徑不動
- #126 SubagentStop orphan（延後）

## 檔案變更清單

### Daemon (Go)

| 檔案 | 變更 |
|------|------|
| `internal/module/session/hooks.go` | 重構 handlers + 統一回傳格式 |
| `internal/module/session/module.go` | 路由改為 `/api/hooks/tmux/*` |
| `internal/module/agent/handler.go` | 路由改為 `/api/hooks/cc/*` |
| `internal/module/agent/module.go` | 更新路由註冊 |

### SPA (TypeScript)

| 檔案 | 變更 |
|------|------|
| `spa/src/lib/hook-modules.ts` | **新增** — HookModule 介面 + 模組定義 + hookFetch |
| `spa/src/hooks/useModuleHook.ts` | **新增** — 通用 hook module fetch hook |
| `spa/src/components/hosts/HookModuleCard.tsx` | **新增** — 單一 module 的 card 元件 |
| `spa/src/components/hosts/HooksSection.tsx` | 重構為遍歷 HOOK_MODULES + refreshKey |
| `spa/src/components/hosts/HooksSection.test.tsx` | 重寫測試（mock HookModule 注入） |
| `spa/src/hooks/useHookStatus.ts` | **刪除** |
| `spa/src/stores/useAgentStore.ts` | 移除 `hooksInstalled`、新增 `models`、修改 `getAgentLabel` |
| `spa/src/stores/useAgentStore.test.ts` | 更新測試 |
| `spa/src/App.tsx` | 移除 hook-status init useEffect + import |
| `spa/src/lib/host-api.ts` | 移除 5 個舊函式 |
| `spa/src/lib/host-api.test.ts` | 移除對應測試 |
| `spa/src/components/StatusBar.tsx` | `getAgentLabel` 呼叫方式更新 |
| `spa/src/components/StatusBar.test.tsx` | 移除 `hooksInstalled` fixture |
| `spa/src/locales/zh-TW.json` | 新增 error 相關 i18n key |
| `spa/src/locales/en.json` | 同上 |

### 測試策略

**Go 端**（現有 hooks handler 無測試，全部為新增）：
- `hooks_test.go`：`handleTmuxHookStatus` 回傳格式驗證、`handleTmuxHookSetup` install/remove
- `handler_test.go`：`handleHookStatus` 新路由 + `handleHookSetup` 新路由

**SPA 端**：
- `useModuleHook.test.ts`：
  - mount 時 fetch → status 更新
  - fetch 回 4xx/5xx → error 非 null，status 不更新
  - hostId 變更 → 舊 fetch 結果被 cancel 不影響新 state
  - setup() → 用回傳值更新 status
  - setup() 失敗 → error 顯示
- `HookModuleCard.test.tsx`：render test，注入 mock `HookModule` 物件
- `HooksSection.test.tsx`：integration test，驗證多 module 渲染 + refreshKey 觸發

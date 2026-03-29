# 1.6c-pre2 Notification System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Agent hook 事件觸發 Electron 系統通知，點擊跳轉對應 tab，支援 per-agent 通知設定與 hook 狀態檢視。

**Architecture:** SPA 判斷（useNotificationDispatcher）+ Electron 執行（IPC notification:show）。Daemon 新增 agent_type 欄位 + broadcast_ts 去重 + hook-status/hook-setup 端點。Agent Settings section 顯示通知開關 + hook 安裝狀態。

**Tech Stack:** Go (daemon) / React 19 + Zustand 5 (SPA) / Electron IPC / Vitest

**Spec:** `docs/superpowers/specs/2026-03-29-notification-system-design.md`

---

## File Structure

### New Files
| File | Responsibility |
|------|---------------|
| `spa/src/stores/useNotificationSettingsStore.ts` | Per-agent 通知設定（Zustand + persist） |
| `spa/src/stores/useNotificationSettingsStore.test.ts` | 設定 store 測試 |
| `spa/src/hooks/useNotificationDispatcher.ts` | 判斷是否通知 + 送出 IPC/Web Notification |
| `spa/src/hooks/useNotificationDispatcher.test.ts` | 判斷邏輯測試 |
| `spa/src/lib/notification-content.ts` | 從 raw_event 組裝通知 title/body |
| `spa/src/lib/notification-content.test.ts` | 內容組裝測試 |
| `spa/src/components/settings/AgentSection.tsx` | Agent Settings section 元件 |

### Modified Files
| File | Changes |
|------|---------|
| `cmd/tbox/hook.go` | 新增 `--agent` flag + hookPayload.AgentType |
| `cmd/tbox/hook_test.go` | `--agent` flag 測試 |
| `cmd/tbox/setup.go` | hook command 帶 `--agent cc` |
| `internal/store/agent_event.go` | AgentEvent.AgentType + DB migration |
| `internal/module/agent/handler.go` | EventRequest.AgentType + broadcast_ts |
| `internal/module/agent/module.go` | buildAgentEvent 帶 agent_type + broadcast_ts |
| `electron/main.ts` | notification:show / notification:clicked IPC |
| `electron/preload.ts` | showNotification / onNotificationClicked bridge |
| `spa/src/stores/useAgentStore.ts` | AgentHookEvent 加 agent_type + broadcast_ts |
| `spa/src/lib/platform.ts` | PlatformCapabilities 加 canNotification |
| `spa/src/lib/pane-tree.ts` | 新增 findTabBySessionCode utility |
| `spa/src/App.tsx` | 掛載 useNotificationDispatcher |
| `spa/src/lib/register-panes.tsx` | 註冊 agent settings section |
| `spa/src/locales/en.json` | Agent section i18n keys |
| `spa/src/locales/zh-TW.json` | Agent section i18n keys |

---

## Task 1: Daemon — agent_type 欄位

**Files:**
- Modify: `cmd/tbox/hook.go:17-45`
- Modify: `cmd/tbox/hook_test.go`
- Modify: `internal/store/agent_event.go:13-65`
- Modify: `internal/module/agent/handler.go:10-46`
- Modify: `internal/module/agent/module.go:67-74`

### Step 1.1: hookPayload + flag parsing 測試

- [ ] 在 `cmd/tbox/hook_test.go` 新增測試：

```go
func TestBuildHookPayload_WithAgentType(t *testing.T) {
	stdin := strings.NewReader(`{"hook_event_name":"Notification","message":"test"}`)
	p := buildHookPayload("mysession", "Notification", "cc", stdin)
	if p.AgentType != "cc" {
		t.Errorf("AgentType = %q, want %q", p.AgentType, "cc")
	}
	if p.EventName != "Notification" {
		t.Errorf("EventName = %q, want %q", p.EventName, "Notification")
	}
}

func TestBuildHookPayload_EmptyAgent(t *testing.T) {
	stdin := strings.NewReader(`{}`)
	p := buildHookPayload("mysession", "Stop", "", stdin)
	if p.AgentType != "" {
		t.Errorf("AgentType = %q, want empty", p.AgentType)
	}
}
```

- [ ] Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./cmd/tbox/ -run TestBuildHookPayload_With -v`
  Expected: FAIL — `buildHookPayload` 只接受 3 個參數

### Step 1.2: hookPayload struct + buildHookPayload 實作

- [ ] 修改 `cmd/tbox/hook.go`：

```go
type hookPayload struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	AgentType   string          `json:"agent_type"`
	RawEvent    json.RawMessage `json:"raw_event"`
}

// buildHookPayload constructs a hookPayload from the given parameters.
// If stdin is empty or cannot be read, raw_event defaults to {}.
func buildHookPayload(tmuxSession, eventName, agentType string, stdin io.Reader) hookPayload {
	raw, err := io.ReadAll(stdin)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	return hookPayload{
		TmuxSession: tmuxSession,
		EventName:   eventName,
		AgentType:   agentType,
		RawEvent:    json.RawMessage(raw),
	}
}
```

- [ ] 修改 `runHook` 加入 `--agent` flag 解析：

```go
func runHook(args []string) {
	if len(args) < 1 {
		os.Exit(0)
	}

	var agentType string
	var eventName string
	for i := 0; i < len(args); i++ {
		if args[i] == "--agent" && i+1 < len(args) {
			agentType = args[i+1]
			i++ // skip value
		} else if eventName == "" {
			eventName = args[i]
		}
	}
	if eventName == "" {
		os.Exit(0)
	}

	tmuxSession := queryTmuxSession()
	payload := buildHookPayload(tmuxSession, eventName, agentType, os.Stdin)

	cfg, err := config.Load("")
	var url, token string
	if err != nil {
		url = "http://127.0.0.1:7860/api/agent/event"
	} else {
		url = fmt.Sprintf("http://%s:%d/api/agent/event", cfg.Bind, cfg.Port)
		token = cfg.Token
	}

	_ = postHookEvent(url, token, payload)
}
```

- [ ] 修正既有測試 `TestBuildHookPayload` 呼叫改為 4 參數（加空字串 agentType）

- [ ] Run: `go test ./cmd/tbox/ -v`
  Expected: ALL PASS

### Step 1.3: AgentEvent struct + DB migration

- [ ] 修改 `internal/store/agent_event.go`：

AgentEvent struct 加欄位：
```go
type AgentEvent struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	AgentType   string          `json:"agent_type"`
	RawEvent    json.RawMessage `json:"raw_event"`
}
```

initDB 加 migration：
```go
func (s *AgentEventStore) initDB() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_events (
			tmux_session TEXT PRIMARY KEY,
			event_name   TEXT NOT NULL,
			agent_type   TEXT NOT NULL DEFAULT '',
			raw_event    TEXT NOT NULL,
			updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}
	// Migration: add agent_type column if missing (existing DBs)
	_, _ = s.db.Exec(`ALTER TABLE agent_events ADD COLUMN agent_type TEXT NOT NULL DEFAULT ''`)
	return nil
}
```

Set 方法加 agentType 參數：
```go
func (s *AgentEventStore) Set(tmuxSession, eventName, agentType string, rawEvent json.RawMessage) error {
	_, err := s.db.Exec(`
		INSERT INTO agent_events (tmux_session, event_name, agent_type, raw_event, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tmux_session) DO UPDATE SET
			event_name = excluded.event_name,
			agent_type = excluded.agent_type,
			raw_event  = excluded.raw_event,
			updated_at = CURRENT_TIMESTAMP
	`, tmuxSession, eventName, agentType, string(rawEvent))
	return err
}
```

Get 和 ListAll 的 SELECT 加 `agent_type`，Scan 加 `&ev.AgentType`。

### Step 1.4: handler + module 帶 agent_type + broadcast_ts

- [ ] 修改 `internal/module/agent/handler.go`：

```go
type EventRequest struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	AgentType   string          `json:"agent_type"`
	RawEvent    json.RawMessage `json:"raw_event"`
}
```

handleEvent 中 Set 呼叫改為：
```go
if err := m.events.Set(req.TmuxSession, req.EventName, req.AgentType, req.RawEvent); err != nil {
```

buildAgentEvent 呼叫改為帶 agentType：
```go
ev := m.buildAgentEvent(req.TmuxSession, req.EventName, req.AgentType, req.RawEvent)
```

- [ ] 修改 `internal/module/agent/module.go` 的 `buildAgentEvent`：

```go
func (m *Module) buildAgentEvent(tmuxSession, eventName, agentType string, rawEvent json.RawMessage) map[string]any {
	return map[string]any{
		"tmux_session": tmuxSession,
		"event_name":   eventName,
		"agent_type":   agentType,
		"raw_event":    rawEvent,
		"broadcast_ts":  time.Now().UnixNano(),
	}
}
```

加 `"time"` 到 import。

sendSnapshot 中也帶 agent_type + broadcast_ts（從 AgentEvent struct 讀）：
```go
for _, ev := range all {
	code, ok := nameToCode[ev.TmuxSession]
	if !ok {
		continue
	}
	enriched := map[string]any{
		"tmux_session": ev.TmuxSession,
		"event_name":   ev.EventName,
		"agent_type":   ev.AgentType,
		"raw_event":    ev.RawEvent,
		"broadcast_ts":  time.Now().UnixNano(),
	}
	payload, _ := json.Marshal(enriched)
	event := core.SessionEvent{Type: "hook", Session: code, Value: string(payload)}
	data, _ := json.Marshal(event)
	sub.Send(data)
}
```

- [ ] Run: `go build ./cmd/tbox/ && go test ./cmd/tbox/ -v && go test ./internal/... -v`
  Expected: ALL PASS（若 handler test 存在需一併更新 Set 呼叫）

### Step 1.5: setup.go 帶 --agent cc

- [ ] 修改 `cmd/tbox/setup.go` 的 `makeTboxEntry`：

```go
func makeTboxEntry(tboxPath, event string) map[string]any {
	return map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": fmt.Sprintf(`"%s" hook --agent cc %s`, tboxPath, event),
			},
		},
	}
}
```

- [ ] Run: `go test ./cmd/tbox/ -v`
  Expected: ALL PASS

### Step 1.6: Commit

- [ ] `git add cmd/tbox/hook.go cmd/tbox/hook_test.go cmd/tbox/setup.go internal/store/agent_event.go internal/module/agent/handler.go internal/module/agent/module.go`
- [ ] `git commit -m "feat(daemon): add agent_type field + broadcast_ts to hook events"`

---

## Task 2: Electron IPC — notification:show + notification:clicked

**Files:**
- Modify: `electron/main.ts:14-78`
- Modify: `electron/preload.ts`

### Step 2.1: preload.ts 新增 notification bridge

- [ ] 在 `electron/preload.ts` 的 `// Memory Monitor` 區塊後新增：

```ts
  // Notifications
  showNotification: (opts: { title: string; body: string; sessionCode: string; eventName: string; broadcastTs: number }) =>
    ipcRenderer.invoke('notification:show', JSON.stringify(opts)),
  onNotificationClicked: (callback: (payload: { sessionCode: string }) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, payload: { sessionCode: string }) =>
      callback(payload)
    ipcRenderer.on('notification:clicked', handler)
    return () => ipcRenderer.removeListener('notification:clicked', handler)
  },
  focusMyWindow: () => ipcRenderer.send('notification:focus-window'),
```

### Step 2.2: main.ts 新增 notification IPC handler

- [ ] 在 `electron/main.ts` 頂部 import 加入 `Notification`：

```ts
import { app, BrowserWindow, ipcMain, Menu, Notification } from 'electron'
```

- [ ] 在 `registerIpcHandlers()` 內 `// Memory Monitor` 區塊後新增：

```ts
  // Notifications
  const recentBroadcasts = new Set<number>()
  ipcMain.handle('notification:show', (_event, optsJson: string) => {
    const opts = JSON.parse(optsJson) as {
      title: string; body: string; sessionCode: string; eventName: string; broadcastTs: number
    }
    // Dedup: same broadcast received by multiple windows
    if (recentBroadcasts.has(opts.broadcastTs)) return
    recentBroadcasts.add(opts.broadcastTs)
    setTimeout(() => recentBroadcasts.delete(opts.broadcastTs), 5000)

    const notification = new Notification({ title: opts.title, body: opts.body })
    notification.on('click', () => {
      // Broadcast to all renderers — SPA decides which one has the tab
      for (const win of windowManager.getAllWindows()) {
        if (!win.isDestroyed()) {
          win.webContents.send('notification:clicked', { sessionCode: opts.sessionCode })
          win.show()
        }
      }
      // Focus the first available window (SPA with the right tab will activate it)
      const focused = windowManager.getAllWindows().find((w) => !w.isDestroyed())
      focused?.focus()
    })
    notification.show()
  })

  // SPA requests its window to be focused (after handling notification click)
  ipcMain.on('notification:focus-window', (event) => {
    const win = BrowserWindow.fromWebContents(event.sender)
    if (win && !win.isDestroyed()) {
      win.show()
      win.focus()
    }
  })
```

- [ ] Run: `cd /Users/wake/Workspace/wake/tmux-box && pnpm run electron:build` (驗證編譯通過)
  Expected: Build succeeds

### Step 2.3: Commit

- [ ] `git add electron/main.ts electron/preload.ts`
- [ ] `git commit -m "feat(electron): add notification:show + notification:clicked IPC"`

---

## Task 3: SPA — platform capability + AgentHookEvent 擴充

**Files:**
- Modify: `spa/src/lib/platform.ts`
- Modify: `spa/src/stores/useAgentStore.ts:8-12`
- Modify: `spa/src/stores/useAgentStore.test.ts`

### Step 3.1: platform.ts 加 canNotification

- [ ] 修改 `spa/src/lib/platform.ts`：

```ts
export interface PlatformCapabilities {
  isElectron: boolean
  canTearOffTab: boolean
  canMergeWindow: boolean
  canBrowserPane: boolean
  canSystemTray: boolean
  canNotification: boolean
  devUpdateEnabled: boolean
}

export function getPlatformCapabilities(): PlatformCapabilities {
  const isElectron = !!window.electronAPI
  const devUpdateEnabled = isElectron && !!window.electronAPI?.getAppInfo
  return {
    isElectron,
    canTearOffTab: isElectron,
    canMergeWindow: isElectron,
    canBrowserPane: isElectron,
    canSystemTray: isElectron,
    canNotification: isElectron,
    devUpdateEnabled,
  }
}
```

### Step 3.2: AgentHookEvent 加 agent_type + broadcast_ts

- [ ] 修改 `spa/src/stores/useAgentStore.ts`：

```ts
export interface AgentHookEvent {
  tmux_session: string
  event_name: string
  agent_type: string
  raw_event: Record<string, unknown>
  broadcast_ts: number
}
```

- [ ] 更新 `spa/src/stores/useAgentStore.test.ts` 既有測試的 event fixture 加上新欄位：

```ts
const event: AgentHookEvent = {
  tmux_session: 'dev',
  event_name: 'UserPromptSubmit',
  agent_type: 'cc',
  raw_event: { foo: 'bar' },
  broadcast_ts: 1000000,
}
```

（所有 test case 的 event 物件都加 `agent_type: 'cc'` 和 `broadcast_ts: Date.now()`）

- [ ] Run: `cd spa && npx vitest run src/stores/useAgentStore.test.ts`
  Expected: ALL PASS

### Step 3.3: Commit

- [ ] `git add spa/src/lib/platform.ts spa/src/stores/useAgentStore.ts spa/src/stores/useAgentStore.test.ts`
- [ ] `git commit -m "feat(spa): extend AgentHookEvent with agent_type + broadcast_ts"`

---

## Task 4: SPA — notification content 組裝

**Files:**
- Create: `spa/src/lib/notification-content.ts`
- Create: `spa/src/lib/notification-content.test.ts`

### Step 4.1: 寫測試

- [ ] 建立 `spa/src/lib/notification-content.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import { buildNotificationContent } from './notification-content'

describe('buildNotificationContent', () => {
  it('Notification event → uses raw_event.message', () => {
    const result = buildNotificationContent('Notification', {
      message: 'Claude needs your permission',
      hook_event_name: 'Notification',
    }, 'my-session')
    expect(result).toEqual({
      title: 'my-session',
      body: 'Claude needs your permission',
    })
  })

  it('PermissionRequest → shows tool_name', () => {
    const result = buildNotificationContent('PermissionRequest', {
      tool_name: 'Bash',
      hook_event_name: 'PermissionRequest',
    }, 'my-session')
    expect(result).toEqual({
      title: 'my-session',
      body: 'Permission required: Bash',
    })
  })

  it('Stop → uses last_assistant_message', () => {
    const result = buildNotificationContent('Stop', {
      last_assistant_message: 'I have completed the refactoring.',
      hook_event_name: 'Stop',
    }, 'my-session')
    expect(result).toEqual({
      title: 'my-session',
      body: 'I have completed the refactoring.',
    })
  })

  it('Stop without last_assistant_message → fallback', () => {
    const result = buildNotificationContent('Stop', {}, 'my-session')
    expect(result).toEqual({
      title: 'my-session',
      body: 'Task completed',
    })
  })

  it('unknown event → null', () => {
    const result = buildNotificationContent('SessionStart', {}, 'my-session')
    expect(result).toBeNull()
  })
})
```

- [ ] Run: `cd spa && npx vitest run src/lib/notification-content.test.ts`
  Expected: FAIL — module not found

### Step 4.2: 實作

- [ ] 建立 `spa/src/lib/notification-content.ts`：

```ts
interface NotificationContent {
  title: string
  body: string
}

export function buildNotificationContent(
  eventName: string,
  rawEvent: Record<string, unknown>,
  sessionName: string,
): NotificationContent | null {
  switch (eventName) {
    case 'Notification':
      return {
        title: sessionName,
        body: (rawEvent.message as string) || 'New notification',
      }
    case 'PermissionRequest':
      return {
        title: sessionName,
        body: `Permission required: ${(rawEvent.tool_name as string) || 'unknown tool'}`,
      }
    case 'Stop':
      return {
        title: sessionName,
        body: (rawEvent.last_assistant_message as string) || 'Task completed',
      }
    default:
      return null
  }
}
```

- [ ] Run: `cd spa && npx vitest run src/lib/notification-content.test.ts`
  Expected: ALL PASS

### Step 4.3: Commit

- [ ] `git add spa/src/lib/notification-content.ts spa/src/lib/notification-content.test.ts`
- [ ] `git commit -m "feat(spa): notification content builder from raw_event"`

---

## Task 5: SPA — useNotificationSettingsStore

**Files:**
- Create: `spa/src/stores/useNotificationSettingsStore.ts`
- Create: `spa/src/stores/useNotificationSettingsStore.test.ts`

### Step 5.1: 寫測試

- [ ] 建立 `spa/src/stores/useNotificationSettingsStore.test.ts`：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useNotificationSettingsStore } from './useNotificationSettingsStore'

beforeEach(() => {
  useNotificationSettingsStore.setState({ agents: {} })
})

describe('useNotificationSettingsStore', () => {
  it('getSettingsForAgent returns defaults for unknown agent', () => {
    const settings = useNotificationSettingsStore.getState().getSettingsForAgent('cc')
    expect(settings.enabled).toBe(true)
    expect(settings.notifyWithoutTab).toBe(false)
    expect(settings.reopenTabOnClick).toBe(false)
  })

  it('setAgentEnabled toggles enabled', () => {
    const { setAgentEnabled } = useNotificationSettingsStore.getState()
    setAgentEnabled('cc', false)
    expect(useNotificationSettingsStore.getState().agents['cc']?.enabled).toBe(false)
  })

  it('setEventEnabled toggles per-event', () => {
    const { setEventEnabled } = useNotificationSettingsStore.getState()
    setEventEnabled('cc', 'Stop', false)
    const settings = useNotificationSettingsStore.getState().getSettingsForAgent('cc')
    expect(settings.events['Stop']).toBe(false)
  })

  it('setNotifyWithoutTab toggles', () => {
    const { setNotifyWithoutTab } = useNotificationSettingsStore.getState()
    setNotifyWithoutTab('cc', true)
    expect(useNotificationSettingsStore.getState().getSettingsForAgent('cc').notifyWithoutTab).toBe(true)
  })

  it('setReopenTabOnClick toggles', () => {
    const { setReopenTabOnClick } = useNotificationSettingsStore.getState()
    setReopenTabOnClick('cc', true)
    expect(useNotificationSettingsStore.getState().getSettingsForAgent('cc').reopenTabOnClick).toBe(true)
  })
})
```

- [ ] Run: `cd spa && npx vitest run src/stores/useNotificationSettingsStore.test.ts`
  Expected: FAIL — module not found

### Step 5.2: 實作

- [ ] 建立 `spa/src/stores/useNotificationSettingsStore.ts`：

```ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export interface NotificationSettings {
  enabled: boolean
  events: Record<string, boolean>
  notifyWithoutTab: boolean
  reopenTabOnClick: boolean
}

const DEFAULT_SETTINGS: NotificationSettings = {
  enabled: true,
  events: {},
  notifyWithoutTab: false,
  reopenTabOnClick: false,
}

interface NotificationSettingsState {
  agents: Record<string, NotificationSettings>
  getSettingsForAgent: (agentType: string) => NotificationSettings
  setAgentEnabled: (agentType: string, enabled: boolean) => void
  setEventEnabled: (agentType: string, eventName: string, enabled: boolean) => void
  setNotifyWithoutTab: (agentType: string, value: boolean) => void
  setReopenTabOnClick: (agentType: string, value: boolean) => void
}

function getOrDefault(agents: Record<string, NotificationSettings>, agentType: string): NotificationSettings {
  return agents[agentType] ?? { ...DEFAULT_SETTINGS }
}

function updateAgent(
  agents: Record<string, NotificationSettings>,
  agentType: string,
  patch: Partial<NotificationSettings>,
): Record<string, NotificationSettings> {
  const current = getOrDefault(agents, agentType)
  return { ...agents, [agentType]: { ...current, ...patch } }
}

export const useNotificationSettingsStore = create<NotificationSettingsState>()(
  persist(
    (set, get) => ({
      agents: {},

      getSettingsForAgent: (agentType) => getOrDefault(get().agents, agentType),

      setAgentEnabled: (agentType, enabled) =>
        set((s) => ({ agents: updateAgent(s.agents, agentType, { enabled }) })),

      setEventEnabled: (agentType, eventName, enabled) =>
        set((s) => {
          const current = getOrDefault(s.agents, agentType)
          const events = { ...current.events, [eventName]: enabled }
          return { agents: updateAgent(s.agents, agentType, { events }) }
        }),

      setNotifyWithoutTab: (agentType, value) =>
        set((s) => ({ agents: updateAgent(s.agents, agentType, { notifyWithoutTab: value }) })),

      setReopenTabOnClick: (agentType, value) =>
        set((s) => ({ agents: updateAgent(s.agents, agentType, { reopenTabOnClick: value }) })),
    }),
    {
      name: 'tbox-notification-settings',
      partialize: (state) => ({ agents: state.agents }),
    },
  ),
)
```

- [ ] Run: `cd spa && npx vitest run src/stores/useNotificationSettingsStore.test.ts`
  Expected: ALL PASS

### Step 5.3: Commit

- [ ] `git add spa/src/stores/useNotificationSettingsStore.ts spa/src/stores/useNotificationSettingsStore.test.ts`
- [ ] `git commit -m "feat(spa): notification settings store with per-agent config"`

---

## Task 6: SPA — findTabBySessionCode utility

**Files:**
- Modify: `spa/src/lib/pane-tree.ts`
- Modify: existing pane-tree tests (if any), or inline test in dispatcher tests

### Step 6.1: 加入 utility

- [ ] 在 `spa/src/lib/pane-tree.ts` 末尾加：

```ts
/**
 * Find the tab ID that contains a session pane matching the given session code.
 * Returns the first match, or undefined if none found.
 */
export function findTabBySessionCode(
  tabs: Record<string, { layout: PaneLayout }>,
  sessionCode: string,
): string | undefined {
  for (const [tabId, tab] of Object.entries(tabs)) {
    const primary = getPrimaryPane(tab.layout)
    if (primary.content.kind === 'session' && primary.content.sessionCode === sessionCode) {
      return tabId
    }
  }
  return undefined
}
```

需在頂部 import 加入 `Tab` 型別（如果需要）。因為函式只用 `{ layout: PaneLayout }` 結構，不需要完整 Tab 型別。

### Step 6.2: Commit

- [ ] `git add spa/src/lib/pane-tree.ts`
- [ ] `git commit -m "feat(spa): findTabBySessionCode utility"`

---

## Task 7: SPA — useNotificationDispatcher hook

**Files:**
- Create: `spa/src/hooks/useNotificationDispatcher.ts`
- Create: `spa/src/hooks/useNotificationDispatcher.test.ts`
- Modify: `spa/src/App.tsx:46-50`

### Step 7.1: 寫測試

- [ ] 建立 `spa/src/hooks/useNotificationDispatcher.test.ts`：

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { shouldNotify } from '../hooks/useNotificationDispatcher'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'

const defaultSettings: NotificationSettings = {
  enabled: true,
  events: {},
  notifyWithoutTab: false,
  reopenTabOnClick: false,
}

describe('shouldNotify', () => {
  it('returns true for waiting event with matching tab', () => {
    expect(shouldNotify({
      derived: 'waiting',
      eventName: 'Notification',
      sessionCode: 'abc',
      focusedSession: null,
      hasTab: true,
      settings: defaultSettings,
    })).toBe(true)
  })

  it('returns true for idle event', () => {
    expect(shouldNotify({
      derived: 'idle',
      eventName: 'Stop',
      sessionCode: 'abc',
      focusedSession: null,
      hasTab: true,
      settings: defaultSettings,
    })).toBe(true)
  })

  it('returns false for running event', () => {
    expect(shouldNotify({
      derived: 'running',
      eventName: 'UserPromptSubmit',
      sessionCode: 'abc',
      focusedSession: null,
      hasTab: true,
      settings: defaultSettings,
    })).toBe(false)
  })

  it('returns false when focused on same session', () => {
    expect(shouldNotify({
      derived: 'waiting',
      eventName: 'Notification',
      sessionCode: 'abc',
      focusedSession: 'abc',
      hasTab: true,
      settings: defaultSettings,
    })).toBe(false)
  })

  it('returns false when no tab and notifyWithoutTab=false', () => {
    expect(shouldNotify({
      derived: 'waiting',
      eventName: 'Notification',
      sessionCode: 'abc',
      focusedSession: null,
      hasTab: false,
      settings: { ...defaultSettings, notifyWithoutTab: false },
    })).toBe(false)
  })

  it('returns true when no tab but notifyWithoutTab=true', () => {
    expect(shouldNotify({
      derived: 'waiting',
      eventName: 'Notification',
      sessionCode: 'abc',
      focusedSession: null,
      hasTab: false,
      settings: { ...defaultSettings, notifyWithoutTab: true },
    })).toBe(true)
  })

  it('returns false when agent disabled', () => {
    expect(shouldNotify({
      derived: 'waiting',
      eventName: 'Notification',
      sessionCode: 'abc',
      focusedSession: null,
      hasTab: true,
      settings: { ...defaultSettings, enabled: false },
    })).toBe(false)
  })

  it('returns false when event disabled', () => {
    expect(shouldNotify({
      derived: 'waiting',
      eventName: 'Notification',
      sessionCode: 'abc',
      focusedSession: null,
      hasTab: true,
      settings: { ...defaultSettings, events: { Notification: false } },
    })).toBe(false)
  })

  it('event defaults to true when not in events map', () => {
    expect(shouldNotify({
      derived: 'idle',
      eventName: 'Stop',
      sessionCode: 'abc',
      focusedSession: null,
      hasTab: true,
      settings: { ...defaultSettings, events: {} },
    })).toBe(true)
  })
})
```

- [ ] Run: `cd spa && npx vitest run src/hooks/useNotificationDispatcher.test.ts`
  Expected: FAIL — module not found

### Step 7.2: 實作 shouldNotify + hook

- [ ] 建立 `spa/src/hooks/useNotificationDispatcher.ts`：

```ts
import { useEffect, useRef } from 'react'
import { useAgentStore } from '../stores/useAgentStore'
import type { AgentHookEvent } from '../stores/useAgentStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { buildNotificationContent } from '../lib/notification-content'
import { findTabBySessionCode } from '../lib/pane-tree'
import { getPlatformCapabilities } from '../lib/platform'
import { deriveStatus } from '../stores/useAgentStore'

interface ShouldNotifyParams {
  derived: string | null
  eventName: string
  sessionCode: string
  focusedSession: string | null
  hasTab: boolean
  settings: NotificationSettings
}

export function shouldNotify(params: ShouldNotifyParams): boolean {
  const { derived, eventName, sessionCode, focusedSession, hasTab, settings } = params

  // Only notify for waiting or idle
  if (derived !== 'waiting' && derived !== 'idle') return false

  // Agent-level toggle
  if (!settings.enabled) return false

  // Per-event toggle (defaults to true if not explicitly set)
  if (settings.events[eventName] === false) return false

  // No tab and notifyWithoutTab disabled
  if (!hasTab && !settings.notifyWithoutTab) return false

  // Currently focused session — don't notify
  if (focusedSession === sessionCode) return false

  return true
}

/**
 * Subscribes to agent store events and dispatches system notifications.
 * Mount once in App.tsx.
 */
export function useNotificationDispatcher(): void {
  const prevEventsRef = useRef<Record<string, AgentHookEvent>>({})

  useEffect(() => {
    const unsubscribe = useAgentStore.subscribe((state, prevState) => {
      const prevEvents = prevState.events
      const currentEvents = state.events

      for (const [sessionCode, event] of Object.entries(currentEvents)) {
        const prev = prevEvents[sessionCode]
        // Only fire on new or changed events (check broadcast_ts)
        if (prev && prev.broadcast_ts === event.broadcast_ts) continue

        const derived = deriveStatus(event.event_name)
        const tabs = useTabStore.getState().tabs
        const hasTab = findTabBySessionCode(tabs, sessionCode) !== undefined
        const settings = useNotificationSettingsStore.getState().getSettingsForAgent(event.agent_type || '')
        const focusedSession = state.focusedSession

        if (!shouldNotify({ derived, eventName: event.event_name, sessionCode, focusedSession, hasTab, settings })) {
          continue
        }

        // Look up session name
        const sessions = useSessionStore.getState().sessions
        const session = sessions.find((s) => s.code === sessionCode)
        const sessionName = session?.name || sessionCode

        const content = buildNotificationContent(event.event_name, event.raw_event, sessionName)
        if (!content) continue

        const capabilities = getPlatformCapabilities()
        if (capabilities.canNotification && window.electronAPI?.showNotification) {
          window.electronAPI.showNotification({
            title: content.title,
            body: content.body,
            sessionCode,
            eventName: event.event_name,
            broadcastTs: event.broadcast_ts,
          })
        } else if ('Notification' in window && Notification.permission === 'granted') {
          const n = new Notification(content.title, { body: content.body })
          n.onclick = () => {
            handleNotificationClick(sessionCode)
          }
        }
      }
    })
    return unsubscribe
  }, [])

  // Listen for Electron notification clicks
  useEffect(() => {
    if (!window.electronAPI?.onNotificationClicked) return
    return window.electronAPI.onNotificationClicked((payload) => {
      handleNotificationClick(payload.sessionCode)
    })
  }, [])
}

function handleNotificationClick(sessionCode: string): void {
  const tabs = useTabStore.getState().tabs
  const tabId = findTabBySessionCode(tabs, sessionCode)
  const settings = useNotificationSettingsStore.getState()
  // Find the agent_type from the latest event for this session
  const event = useAgentStore.getState().events[sessionCode]
  const agentSettings = settings.getSettingsForAgent(event?.agent_type || '')

  if (tabId) {
    useTabStore.getState().setActiveTab(tabId)
    useAgentStore.getState().setFocusedSession(sessionCode)
  } else if (agentSettings.reopenTabOnClick) {
    // Create new tab in stream mode
    const { createTab } = await import('../types/tab')
    const newTab = createTab({ kind: 'session', sessionCode, mode: 'stream' })
    useTabStore.getState().addTab(newTab)
    useTabStore.getState().setActiveTab(newTab.id)
    useAgentStore.getState().setFocusedSession(sessionCode)
  }
}
```

Wait — `handleNotificationClick` 用了 `await import()`，但它不是 async。讓我修正：不需要動態 import，`createTab` 已經是同步 utility。把它改成同步 import + 呼叫：

```ts
import { createTab } from '../types/tab'
```

然後 `handleNotificationClick` 改為：

```ts
function handleNotificationClick(sessionCode: string): void {
  const tabs = useTabStore.getState().tabs
  const tabId = findTabBySessionCode(tabs, sessionCode)
  const event = useAgentStore.getState().events[sessionCode]
  const agentSettings = useNotificationSettingsStore.getState().getSettingsForAgent(event?.agent_type || '')

  let handled = false
  if (tabId) {
    useTabStore.getState().setActiveTab(tabId)
    useAgentStore.getState().setFocusedSession(sessionCode)
    handled = true
  } else if (agentSettings.reopenTabOnClick) {
    const newTab = createTab({ kind: 'session', sessionCode, mode: 'stream' })
    useTabStore.getState().addTab(newTab)
    useTabStore.getState().setActiveTab(newTab.id)
    useAgentStore.getState().setFocusedSession(sessionCode)
    handled = true
  }

  // Ask Electron to focus this window (the one that handled the click)
  if (handled && window.electronAPI?.focusMyWindow) {
    window.electronAPI.focusMyWindow()
  }
}
```

- [ ] Run: `cd spa && npx vitest run src/hooks/useNotificationDispatcher.test.ts`
  Expected: ALL PASS

### Step 7.3: export deriveStatus

- [ ] `useAgentStore.ts` 中 `deriveStatus` 目前是 module-level function。確認它已 export（如果沒有則加 `export`）：

```ts
export function deriveStatus(eventName: string): AgentStatus | null {
```

### Step 7.4: 掛載到 App.tsx

- [ ] 在 `spa/src/App.tsx` 加 import：

```ts
import { useNotificationDispatcher } from './hooks/useNotificationDispatcher'
```

- [ ] 在 `useShortcuts()` 後加：

```ts
  useNotificationDispatcher()
```

### Step 7.5: Commit

- [ ] `git add spa/src/hooks/useNotificationDispatcher.ts spa/src/hooks/useNotificationDispatcher.test.ts spa/src/stores/useAgentStore.ts spa/src/App.tsx`
- [ ] `git commit -m "feat(spa): notification dispatcher hook with click-to-navigate"`

---

## Task 8: SPA — Agent Settings section

**Files:**
- Create: `spa/src/components/settings/AgentSection.tsx`
- Modify: `spa/src/lib/register-panes.tsx:55-59`
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

### Step 8.1: i18n keys

- [ ] 在 `spa/src/locales/en.json` 的 `"settings.section.sync"` 之後加：

```json
  "settings.section.agent": "Agent",

  "settings.agent.title": "Agent",
  "settings.agent.desc": "Agent notification and hook settings",
  "settings.agent.notifications.enabled": "Enable Notifications",
  "settings.agent.notifications.enabled_desc": "Show system notifications for agent events",
  "settings.agent.notifications.notify_without_tab": "Notify Without Tab",
  "settings.agent.notifications.notify_without_tab_desc": "Receive notifications even when the session tab is closed",
  "settings.agent.notifications.reopen_tab": "Reopen Tab on Click",
  "settings.agent.notifications.reopen_tab_desc": "Clicking a notification reopens the session tab",
  "settings.agent.event.Notification": "Notification",
  "settings.agent.event.PermissionRequest": "Permission Request",
  "settings.agent.event.Stop": "Task Completed",
  "settings.agent.hook.status": "Hook Status",
  "settings.agent.hook.installed": "Installed",
  "settings.agent.hook.not_installed": "Not Installed",
  "settings.agent.hook.install": "Install Hook",
  "settings.agent.hook.remove": "Remove Hook",
  "settings.agent.no_agents": "No agents detected yet. Start a Claude Code session to see agent settings.",
```

- [ ] 在 `spa/src/locales/zh-TW.json` 對應位置加：

```json
  "settings.section.agent": "Agent",

  "settings.agent.title": "Agent",
  "settings.agent.desc": "Agent 通知與 Hook 設定",
  "settings.agent.notifications.enabled": "啟用通知",
  "settings.agent.notifications.enabled_desc": "顯示 Agent 事件的系統通知",
  "settings.agent.notifications.notify_without_tab": "無分頁時仍通知",
  "settings.agent.notifications.notify_without_tab_desc": "即使 Session 分頁已關閉，仍接收通知",
  "settings.agent.notifications.reopen_tab": "點擊通知重開分頁",
  "settings.agent.notifications.reopen_tab_desc": "點擊通知時重新開啟 Session 分頁",
  "settings.agent.event.Notification": "通知",
  "settings.agent.event.PermissionRequest": "權限請求",
  "settings.agent.event.Stop": "任務完成",
  "settings.agent.hook.status": "Hook 狀態",
  "settings.agent.hook.installed": "已安裝",
  "settings.agent.hook.not_installed": "未安裝",
  "settings.agent.hook.install": "安裝 Hook",
  "settings.agent.hook.remove": "移除 Hook",
  "settings.agent.no_agents": "尚未偵測到任何 Agent。啟動 Claude Code 後會出現設定。",
```

### Step 8.2: AgentSection 元件

- [ ] 建立 `spa/src/components/settings/AgentSection.tsx`：

```tsx
import { useI18nStore } from '../../stores/useI18nStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useNotificationSettingsStore } from '../../stores/useNotificationSettingsStore'
import { SettingItem } from './SettingItem'

const KNOWN_EVENTS = ['Notification', 'PermissionRequest', 'Stop']

export function AgentSection() {
  const t = useI18nStore((s) => s.t)
  const events = useAgentStore((s) => s.events)
  const agents = useNotificationSettingsStore((s) => s.agents)
  const getSettings = useNotificationSettingsStore((s) => s.getSettingsForAgent)
  const setAgentEnabled = useNotificationSettingsStore((s) => s.setAgentEnabled)
  const setEventEnabled = useNotificationSettingsStore((s) => s.setEventEnabled)
  const setNotifyWithoutTab = useNotificationSettingsStore((s) => s.setNotifyWithoutTab)
  const setReopenTabOnClick = useNotificationSettingsStore((s) => s.setReopenTabOnClick)

  // Collect known agent types from events
  const agentTypes = [...new Set(
    Object.values(events)
      .map((e) => e.agent_type)
      .filter((t): t is string => !!t)
  )]

  if (agentTypes.length === 0) {
    return (
      <div className="space-y-4">
        <div>
          <h3 className="text-sm font-medium text-text-primary">{t('settings.agent.title')}</h3>
          <p className="text-xs text-text-muted mt-1">{t('settings.agent.desc')}</p>
        </div>
        <p className="text-xs text-text-muted">{t('settings.agent.no_agents')}</p>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-sm font-medium text-text-primary">{t('settings.agent.title')}</h3>
        <p className="text-xs text-text-muted mt-1">{t('settings.agent.desc')}</p>
      </div>

      {agentTypes.map((agentType) => {
        const settings = getSettings(agentType)
        const label = agentType === 'cc' ? 'Claude Code' : agentType

        return (
          <div key={agentType} className="border border-border-default rounded-md p-3 space-y-3">
            <h4 className="text-xs font-medium text-text-primary">{label}</h4>

            {/* Agent-level toggle */}
            <SettingItem
              label={t('settings.agent.notifications.enabled')}
              description={t('settings.agent.notifications.enabled_desc')}
            >
              <input
                type="checkbox"
                checked={settings.enabled}
                onChange={(e) => setAgentEnabled(agentType, e.target.checked)}
                className="rounded"
              />
            </SettingItem>

            {/* Per-event toggles */}
            {settings.enabled && KNOWN_EVENTS.map((eventName) => (
              <SettingItem
                key={eventName}
                label={t(`settings.agent.event.${eventName}`)}
                description=""
              >
                <input
                  type="checkbox"
                  checked={settings.events[eventName] !== false}
                  onChange={(e) => setEventEnabled(agentType, eventName, e.target.checked)}
                  className="rounded"
                />
              </SettingItem>
            ))}

            {/* Notify without tab */}
            {settings.enabled && (
              <SettingItem
                label={t('settings.agent.notifications.notify_without_tab')}
                description={t('settings.agent.notifications.notify_without_tab_desc')}
              >
                <input
                  type="checkbox"
                  checked={settings.notifyWithoutTab}
                  onChange={(e) => setNotifyWithoutTab(agentType, e.target.checked)}
                  className="rounded"
                />
              </SettingItem>
            )}

            {/* Reopen tab on click (only when notifyWithoutTab is on) */}
            {settings.enabled && settings.notifyWithoutTab && (
              <SettingItem
                label={t('settings.agent.notifications.reopen_tab')}
                description={t('settings.agent.notifications.reopen_tab_desc')}
              >
                <input
                  type="checkbox"
                  checked={settings.reopenTabOnClick}
                  onChange={(e) => setReopenTabOnClick(agentType, e.target.checked)}
                  className="rounded"
                />
              </SettingItem>
            )}
          </div>
        )
      })}
    </div>
  )
}
```

### Step 8.3: 註冊 settings section

- [ ] 在 `spa/src/lib/register-panes.tsx` 加 import：

```ts
import { AgentSection } from '../components/settings/AgentSection'
```

- [ ] 在 terminal section 註冊之後加：

```ts
  registerSettingsSection({ id: 'agent', label: 'settings.section.agent', order: 2, component: AgentSection })
```

### Step 8.4: 驗證

- [ ] Run: `cd spa && pnpm run lint && npx vitest run`
  Expected: ALL PASS + no lint errors

### Step 8.5: Commit

- [ ] `git add spa/src/components/settings/AgentSection.tsx spa/src/lib/register-panes.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json`
- [ ] `git commit -m "feat(spa): agent settings section with per-event notification toggles"`

---

## Task 9: Daemon — hook-status + hook-setup 端點

**Files:**
- Modify: `internal/module/agent/module.go` (RegisterRoutes)
- Modify: `internal/module/agent/handler.go` (new handlers)

### Step 9.1: hook-status handler

- [ ] 在 `internal/module/agent/handler.go` 新增：

```go
// handleHookStatus returns the current hook installation status by reading
// Claude Code's settings.json.
func (m *Module) handleHookStatus(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, `{"error":"cannot find home dir"}`, http.StatusInternalServerError)
		return
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		// settings.json doesn't exist → hooks not installed
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"agent_type": "cc",
			"installed":  false,
			"events":     map[string]any{},
			"issues":     []string{"settings.json not found"},
		})
		return
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		http.Error(w, `{"error":"invalid settings.json"}`, http.StatusInternalServerError)
		return
	}

	hooks, _ := settings["hooks"].(map[string]any)
	events := map[string]any{}
	var issues []string
	allInstalled := true

	hookEvents := []string{"SessionStart", "UserPromptSubmit", "Stop", "Notification", "PermissionRequest", "SessionEnd"}
	for _, eventName := range hookEvents {
		entries, ok := hooks[eventName]
		if !ok {
			events[eventName] = map[string]any{"installed": false, "command": nil}
			issues = append(issues, eventName+" hook not installed")
			allInstalled = false
			continue
		}
		// Find tbox hook command in entries
		command := findTboxCommand(entries)
		events[eventName] = map[string]any{"installed": command != "", "command": command}
		if command == "" {
			issues = append(issues, eventName+" hook: tbox command not found")
			allInstalled = false
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"agent_type": "cc",
		"installed":  allInstalled,
		"events":     events,
		"issues":     issues,
	})
}

// findTboxCommand searches a hook event entry for a tbox hook command string.
func findTboxCommand(entries any) string {
	arr, ok := entries.([]any)
	if !ok {
		return ""
	}
	for _, entry := range arr {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hooksList, ok := entryMap["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range hooksList {
			hookMap, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hookMap["command"].(string)
			if strings.Contains(cmd, "tbox hook") {
				return cmd
			}
		}
	}
	return ""
}
```

加 import：`"os"`, `"path/filepath"`, `"strings"`

### Step 9.2: hook-setup handler

- [ ] 在 `internal/module/agent/handler.go` 新增：

```go
type hookSetupRequest struct {
	AgentType string `json:"agent_type"`
	Action    string `json:"action"` // "install" or "remove"
}

// handleHookSetup installs or removes tbox hooks by executing `tbox setup`.
func (m *Module) handleHookSetup(w http.ResponseWriter, r *http.Request) {
	var req hookSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Find tbox binary path
	tboxPath, err := os.Executable()
	if err != nil {
		http.Error(w, `{"error":"cannot find tbox binary"}`, http.StatusInternalServerError)
		return
	}

	var args []string
	switch req.Action {
	case "install":
		args = []string{"setup"}
	case "remove":
		args = []string{"setup", "--remove"}
	default:
		http.Error(w, `{"error":"action must be install or remove"}`, http.StatusBadRequest)
		return
	}

	cmd := exec.Command(tboxPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error":  "setup failed",
			"detail": string(output),
		})
		return
	}

	// Return updated status
	m.handleHookStatus(w, r)
}
```

加 import：`"os/exec"`

### Step 9.3: 註冊路由

- [ ] 在 `internal/module/agent/module.go` 的 `RegisterRoutes` 中加：

```go
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agent/event", m.handleEvent)
	mux.HandleFunc("GET /api/agent/hook-status", m.handleHookStatus)
	mux.HandleFunc("POST /api/agent/hook-setup", m.handleHookSetup)
}
```

### Step 9.4: 驗證

- [ ] Run: `go build ./cmd/tbox/ && go test ./... -v`
  Expected: ALL PASS

### Step 9.5: Commit

- [ ] `git add internal/module/agent/handler.go internal/module/agent/module.go`
- [ ] `git commit -m "feat(daemon): add hook-status and hook-setup API endpoints"`

---

## Task 10: SPA — Agent Settings hook 狀態顯示

**Files:**
- Modify: `spa/src/components/settings/AgentSection.tsx`

### Step 10.1: 加入 hook 狀態查詢

- [ ] 在 `AgentSection.tsx` 加入 hook status fetch 邏輯：

```tsx
import { useState, useEffect } from 'react'
import { useHostStore } from '../../stores/useHostStore'

// 在 AgentSection 元件內加：
const getDaemonBase = useHostStore((s) => s.getDaemonBase)
const daemonBase = getDaemonBase('local')

interface HookEventStatus {
  installed: boolean
  command: string | null
}

interface HookStatus {
  agent_type: string
  installed: boolean
  events: Record<string, HookEventStatus>
  issues: string[]
}

const [hookStatus, setHookStatus] = useState<HookStatus | null>(null)
const [hookLoading, setHookLoading] = useState(false)

useEffect(() => {
  fetch(`${daemonBase}/api/agent/hook-status`)
    .then((r) => r.json())
    .then((data) => setHookStatus(data as HookStatus))
    .catch(() => setHookStatus(null))
}, [daemonBase])

const handleInstallHook = async () => {
  setHookLoading(true)
  try {
    const res = await fetch(`${daemonBase}/api/agent/hook-setup`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ agent_type: 'cc', action: 'install' }),
    })
    const data = await res.json()
    setHookStatus(data as HookStatus)
  } catch { /* ignore */ }
  setHookLoading(false)
}

const handleRemoveHook = async () => {
  setHookLoading(true)
  try {
    const res = await fetch(`${daemonBase}/api/agent/hook-setup`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ agent_type: 'cc', action: 'remove' }),
    })
    const data = await res.json()
    setHookStatus(data as HookStatus)
  } catch { /* ignore */ }
  setHookLoading(false)
}
```

- [ ] 在每個 agent type 子區塊頂部加 hook 狀態顯示：

```tsx
{/* Hook status */}
{hookStatus && agentType === 'cc' && (
  <SettingItem
    label={t('settings.agent.hook.status')}
    description=""
  >
    <div className="flex items-center gap-2">
      <span className={`text-xs ${hookStatus.installed ? 'text-green-400' : 'text-yellow-400'}`}>
        {hookStatus.installed
          ? t('settings.agent.hook.installed')
          : t('settings.agent.hook.not_installed')}
      </span>
      <button
        onClick={hookStatus.installed ? handleRemoveHook : handleInstallHook}
        disabled={hookLoading}
        className="text-xs px-2 py-0.5 rounded border border-border-default hover:bg-surface-hover text-text-secondary"
      >
        {hookLoading ? '...' : hookStatus.installed
          ? t('settings.agent.hook.remove')
          : t('settings.agent.hook.install')}
      </button>
    </div>
  </SettingItem>
)}
```

### Step 10.2: 驗證

- [ ] Run: `cd spa && pnpm run lint && npx vitest run`
  Expected: ALL PASS

### Step 10.3: Commit

- [ ] `git add spa/src/components/settings/AgentSection.tsx`
- [ ] `git commit -m "feat(spa): hook status display and install/remove in agent settings"`

---

## Task 11: 整合驗證 + Lint 修正

### Step 11.1: 全量測試

- [ ] Run: `cd spa && npx vitest run`
  Expected: ALL PASS

- [ ] Run: `cd spa && pnpm run lint`
  Expected: No errors

- [ ] Run: `go test ./... -v`
  Expected: ALL PASS

- [ ] Run: `cd spa && pnpm run build`
  Expected: Build succeeds

### Step 11.2: 修正任何問題

- [ ] 修正 lint 錯誤或測試失敗

### Step 11.3: Final commit (if any fixes)

- [ ] `git add -A && git commit -m "fix: lint and test fixes for notification system"`

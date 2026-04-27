# Quick Commands v2 — Capability / Binding / Slot 設計

> 取代 `2026-04-10-quick-commands-design.md` 的舊架構（mount 行為散落於 consumer，無集中設定 UI）。
> 目標：降低 command 與 module 的雙向耦合，並把「在哪顯示」這個 user 意圖獨立成顯式 entity。

---

## 1. 動機

舊版 Quick Commands：
- Command 與「執行語意」綁死於消費端硬寫的 `onExecute`（`PaneLayoutRenderer` / `SessionsSection`）
- 沒有設定 UI；user 無法新增 / 編輯 commands
- Mount 行為由消費端決定，無法 user 自選位置
- 模組想 host quick-action 入口須各自實作

新版三層分離：
- **Capability**（command 是純資料：什麼能力）
- **Binding**（user 意圖：mount 到哪）
- **Slot**（模組決定：怎麼渲染、怎麼執行）

---

## 2. 資料模型

### 2.1 Capability（command）

```ts
interface QuickCommand {
  id: string                 // user-visible 唯一 id（含 settings 編輯時的 stable key）
  name: string
  command: string            // 送進 terminal 的字串。v2 不支援 function form（簡化）
  icon?: string              // Phosphor icon name；slot 可作為 hint，未必使用
  category?: string          // 自由分類（'agent' / 'shell' / 自訂）；UI 用於分群
}
```

**v2 移除動態 function command**：
- 舊版的 `(ctx) => string` 在 v2 移除，因為 binding 模型下「這個 command 在不同 slot 看到不同 ctx」會讓 function command 行為難預期
- 動態值（如 cwd）改由 slot 端的 executor 注入

### 2.2 Binding

```ts
type Bindings = Record<commandId, MountTarget[]>

type MountTarget = string  // 例：'workspace.actions' | 'host.actions'
```

- 多 mount：一個 command 可同時出現在多個 slot
- Binding 是純資料；新增 mount target 不需要動 schema，slot string 即可
- 沒有 binding 的 command 仍存在但不會出現在任何 slot（user 可保留草稿）

### 2.3 Store schema 變更

```ts
interface QuickCommandState {
  global: QuickCommand[]
  byHost: Record<string, QuickCommand[]>     // 保留（per-host override，Phase 1 UI 不暴露）
  bindings: Record<string, string[]>          // ★ 新增

  // Capability CRUD（沿用）
  addCommand: (cmd: QuickCommand, hostId?: string) => void
  updateCommand: (id, patch, hostId?) => void
  removeCommand: (id, hostId?) => void
  getCommands: (hostId: string) => QuickCommand[]

  // ★ Binding API（新增）
  setBinding: (commandId: string, targets: string[]) => void
  getBoundCommands: (mountTarget: string, hostId: string) => QuickCommand[]
}
```

`getBoundCommands` 是 SlotHost 主要查詢介面：給定 slot，回傳已 mount 的 commands（已套用 per-host override）。

### 2.4 預設值

- `DEFAULT_COMMANDS = []`（v2 不預埋）
- 既有 user 的 localStorage 已存的舊 defaults（`start-cc` / `start-codex`）不主動清除（依 alpha-no-migration 原則）
- Sync contributor 的 `DATA_FIELDS` 加入 `bindings`

---

## 3. Slot 模型

### 3.1 介面

```tsx
// 共用元件
<CommandSlot mountTo="workspace.actions" ctx={{ hostId, workspaceId }}>
  {(cmd, ctx) => (
    /* 模組可選：自訂渲染。不傳 children 則用 default renderer */
  )}
</CommandSlot>
```

```ts
// Default renderer 簽名
type SlotRenderer = (cmd: QuickCommand, ctx: SlotContext) => ReactNode

// Slot 端負責提供
interface SlotConfig {
  mountTo: string
  ctx: SlotContext
  executor: (cmd: QuickCommand, ctx: SlotContext) => Promise<void>
  render?: SlotRenderer  // 預設用內建 button
}
```

`CommandSlot` 內部：
1. 從 store 取 `getBoundCommands(mountTo, hostId)`
2. 對每個 command 呼叫 render（custom 或 default）
3. 點擊 → 呼叫 slot 提供的 executor

### 3.2 Slot 行為語意（v2 Phase 1 範圍）

| Slot | Ctx | Executor 行為 |
|---|---|---|
| `workspace.actions` | `{ hostId, workspaceId, cwd? }` | `POST /api/sessions` 對該 wks 的 host 建 session（cwd 取 wks default → host default fallback）→ `executeCommand(send-keys)` 送 cmd → 自動切到該 session |
| `host.actions` | `{ hostId, cwd? }` | `POST /api/sessions` 對該 host 建 session（cwd 取 host default）→ `executeCommand` → 自動切到該 session |

**失敗處理**（兩個 slot 共用）：
- 建 session 失敗 → toast 顯示錯誤
- 建 session 成功 + send-keys 失敗 → toast 顯示錯誤，**保留 session**（不自動 close）
- 切到 session 失敗（極少見）→ silent fail，session 仍存在

### 3.3 不在 Phase 1 範圍

- `pane.extra-actions`、`sessions.row-actions` 兩個既有整合保留現狀（直接 `useCommands` + 硬寫 onExecute）
- 後續 phase 再遷移為 `CommandSlot`，屆時把 binding 模型套用整個系統

---

## 4. UI

### 4.1 全域 Settings — Quick Commands tab

新增 tab，列表 + CRUD：

```
┌────────────────────────────────────────────────────────────┐
│ Quick Commands                                  [+ New]    │
├────────────────────────────────────────────────────────────┤
│ ⚡ Start Claude Code         agent                          │
│    claude -p --verbose --output-format stream-json          │
│    Mount: [Workspace] [Host]                       Edit Del │
│                                                              │
│ 🔧 Custom Build              shell                          │
│    pnpm run build                                           │
│    Mount: [Workspace]                              Edit Del │
└────────────────────────────────────────────────────────────┘
```

Edit dialog 欄位：
- Name（必填）
- Command（必填，textarea）
- Icon（Phosphor name，optional，含 picker）
- Category（自由文字，optional）
- Mount targets（multi-select chips：Workspace / Host；Phase 1 兩個選項）

空狀態：「No quick commands yet — Start by creating one.」

### 4.2 Workspace 入口

Workspace 主面板新增 quick actions 區塊（位置由實作時定，原則上靠近 workspace header）：

```
[ Workspace Name ]
[ ⚡ Start CC ] [ 🔧 Build ]    ← bindings mountTo: workspace.actions 的 commands
```

按鈕點擊 → executor 建 session + 送 keys + 切過去。

### 4.3 Host 詳情頁入口

Host 詳情頁（Sessions section 之外、靠近 host 標題的 quick actions 區）：

```
[ Host: Mini-Lab (online) ]
[ ⚡ Start CC ] [ 🔧 Build ]    ← bindings mountTo: host.actions 的 commands
```

按鈕點擊 → executor 對該 host 建 session + 送 keys + 切過去。

---

## 5. 後端

無新增 API。沿用：
- `POST /api/sessions` — 建 session（已存在）
- `POST /api/sessions/{code}/send-keys` — 送 keys（v1 已實作）

Executor 端整合兩個呼叫即可。

---

## 6. Phase 切分

### Phase 1a — 資料層 + Settings 頁面 + 模組註冊（一個 PR）

- Store schema 升級：加 `bindings` 欄位 + `setBinding` / `getBoundCommands` API
- `DEFAULT_COMMANDS = []`
- Sync contributor 加入 `bindings` 至 `DATA_FIELDS`
- Quick Commands Settings 頁面元件（list + edit dialog + multi-select mount chips）
- **`registerModule({ id: 'quick-commands', disableable: true, settings: [...] })`** 於 `register-modules.tsx`
- i18n keys（`modules.quick_commands.description` / `settings.section.quick_commands` / 空狀態 / 編輯 dialog labels）
- Tests：store + sync contributor + settings page component + module-enabled 整合

**驗收**：Settings 頁面可建立 / 編輯 / 刪除 commands 與 bindings；資料持久化 + sync 同步可用；視覺 OK 但目前還沒有 slot 消費 → bindings 設了沒效果（預期）。

### Phase 1b — Slot 渲染 + 兩個入口（一個 PR）

- `CommandSlot` 共用元件（內建 `isEnabled('quick-commands')` short-circuit）
- `executor` lib：建 session + 送 keys 一條龍 helper（含 toast 失敗處理）
- Workspace 入口（quick actions 區塊）
- Host 詳情頁入口
- Tests：CommandSlot（含 disable 隱藏）+ executor + 整合

**驗收**：Phase 1a 設定的 bindings 可在 workspace / host 對應位置看到按鈕；點擊正常建 session 並送 keys；失敗 toast 顯示。

### 不在範圍

- 遷移 `PaneLayoutRenderer` / `SessionsSection`（Phase 2+）
- per-host override UI（store 支援，UI 後續疊）
- Mode 設定（terminal/stream/jsonl）作為 binding 欄位（Phase 2+）
- 動態 function command
- Command palette / 快捷鍵
- 多語言全展開（Phase 1 用最小 zh-TW + en）

---

## 7. Module 註冊與啟用 / 停用

Quick Commands v2 是**獨立 module**，遵循專案統一的 module 啟用 / 停用規範（與 `editor` / `browser` / `memory-monitor` 等 disableable 模組同型）。

### 7.1 註冊

於 `register-modules.tsx` 註冊：

```ts
registerModule({
  id: 'quick-commands',
  name: 'Quick Commands',
  disableable: true,
  descriptionKey: 'modules.quick_commands.description',
  settings: [
    {
      localId: 'quick-commands',
      scope: 'purdex',
      order: ?,                                    // 與其他 purdex 設定 tab 排序
      labelKey: 'settings.section.quick_commands',
      component: QuickCommandsSettingsSection,
    },
  ],
})
```

### 7.2 啟用狀態查詢

統一走 `useModuleEnabledStore.isEnabled('quick-commands')`：
- **Settings tab**：由現有 settings contribution 機制自動處理，disable 時 tab 從 Settings 頁消失（與其他 disableable module 一致）
- **SlotHost (`CommandSlot`)**：內部呼叫 `isEnabled('quick-commands')`，false 時不渲染任何按鈕（無論是否有 binding）
- **Modules Switchboard**：自動列入清單（`disableable: true`）

### 7.3 停用後語意

- **資料保留**：停用 != 清除。capability + binding 資料完整保留於 store + sync。
- **UI 全面隱藏**：Settings tab + 所有 slot 按鈕一致消失。
- **重新啟用**：UI 恢復、原 binding 直接生效（無需重設）。
- **預設**：`enabled = true`（`useModuleEnabledStore` 預設值），user 須主動關閉。

### 7.4 Sync 行為

| 資料 | Sync? | 理由 |
|---|---|---|
| Capability (commands) | ✅ Yes（既有） | 指令庫應跨設備共用 |
| Binding | ✅ Yes（新增） | mount 偏好應跨設備共用 |
| 模組 enabled 狀態 | ❌ No | device-local（與既有 `useModuleEnabledStore` 一致：低資源 host 可關閉模組） |

亦即：在桌機建好的 commands + bindings 切到行動裝置會同步看到；但行動裝置可獨立關閉本模組不影響桌機。

### 7.5 Slot 元件契約

`CommandSlot` 須在 render 一開頭 short-circuit：

```tsx
function CommandSlot({ mountTo, ctx, executor, render }: Props) {
  const enabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  if (!enabled) return null
  // ... 後續渲染邏輯
}
```

避免每個 consumer 自己檢查；模組啟用狀態是 slot 內建語意。

---

## 8. Migration / 相容性

- **既有 module-contributed commands** (`module-registry.commands?`) — 暫保留，本次不動。它們不參與 binding 模型；v2 Phase 1 兩個新 slot 只看 store bindings。後續 phase 再決定是否統一。
- **舊 `useCommands` hook** — 保留（`PaneLayoutRenderer` / `SessionsSection` 還在用）。Phase 1 不刪。
- **localStorage** — 沒有 schema migration（alpha-no-migration），既有 user 的 `global: [start-cc, start-codex]` + `byHost: {}` + 沒有 `bindings` 欄位 → store hydrate 後 `bindings = {}`（沒 binding 等於不顯示，符合「不要預設」需求；user 想要就自己編輯加 binding）

---

## 9. 不做的事（強調）

- 不做動態 function command（v1 有，v2 移除）
- 不做 per-host bindings（bindings 是 global；per-host overrides 只有 capability 層；Phase 1 UI 不暴露 per-host）
- 不做 command 排程 / 歷史記錄 / 變數插值
- 不做 binding 的 `when` 條件（VS Code 那種 expression 系統）
- 不重構既有 module-registry.commands 模組貢獻擴充點

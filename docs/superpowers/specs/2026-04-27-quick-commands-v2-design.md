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
- 舊版的 `(ctx) => string` 在 v2 移除，因為 binding 模型下「同一 command 在不同 slot 看到不同 ctx」會讓行為難預期
- 動態值（如 cwd）改由 slot 端的 executor 注入

### 2.2 Slot 識別

slot id 用 typed const，Phase 1 兩個目標：

```ts
// spa/src/lib/quick-command-slots.ts
export const QUICK_COMMAND_SLOTS = {
  WORKSPACE_ACTIONS: 'workspace.actions',
  HOST_ACTIONS: 'host.actions',
} as const

export type QuickCommandSlotId =
  (typeof QUICK_COMMAND_SLOTS)[keyof typeof QUICK_COMMAND_SLOTS]
```

Settings UI、CommandSlot、executor 一律從這常數讀，禁止字串字面量。Phase 2 若開放外部模組註冊新 slot，再升級成 `registerQuickCommandSlot()` registry — 目前 YAGNI 不做。

### 2.3 Binding

```ts
type Bindings = Record<string /* commandId */, QuickCommandSlotId[]>
```

- 多 mount：一個 command 可同時出現在多個 slot
- Binding 是純資料；Phase 1 接受未知 slot id（為 forward compat），但 SlotHost 端只渲染目前已註冊的 slot
- 沒有 binding 的 command 仍存在但不會出現在任何 slot（user 可保留草稿）

### 2.4 Store schema 變更

```ts
interface QuickCommandState {
  global: QuickCommand[]
  byHost: Record<string, QuickCommand[]>     // 保留（per-host override，Phase 1 UI 不暴露）
  bindings: Bindings                          // ★ 新增

  // Capability CRUD（沿用）
  addCommand: (cmd: QuickCommand, hostId?: string) => void
  updateCommand: (id, patch, hostId?) => void
  removeCommand: (id, hostId?) => void
  getCommands: (hostId: string) => QuickCommand[]

  // ★ Binding API（新增）
  setBinding: (commandId: string, targets: QuickCommandSlotId[]) => void
  getBoundCommands: (mountTarget: QuickCommandSlotId, hostId: string) => QuickCommand[]
}
```

`getBoundCommands` 是 SlotHost 主要查詢介面：給定 slot，回傳已 mount 的 commands（已套用 per-host override + dangling 過濾，見 §2.5）。

### 2.5 Validation, Sanitization & Dangling Bindings

仿 `useModuleEnabledStore` AR-1（codex review #617），bindings 不可信任 hydrated payload。

**Sanitizer**（hydrate 與 sync deserialize 共用）：

```ts
function sanitizeBindings(raw: unknown): Bindings {
  if (raw === null || typeof raw !== 'object') return {}
  const out: Bindings = {}
  const UNSAFE = new Set(['__proto__', 'constructor', 'prototype'])
  for (const [cmdId, targets] of Object.entries(raw as Record<string, unknown>)) {
    if (typeof cmdId !== 'string' || cmdId.length === 0) continue
    if (UNSAFE.has(cmdId)) continue
    if (!Array.isArray(targets)) continue
    const cleaned = targets.filter((t): t is string => typeof t === 'string' && t.length > 0)
    if (cleaned.length > 0) out[cmdId] = cleaned
  }
  return out
}
```

**Dangling binding 處理**（command 已刪、binding 還指向）：

- `removeCommand(id, hostId?)` 必須同步呼叫 `setBinding(id, [])` 等價清理（global 刪除時清；per-host 刪除維持 binding 不動，因 binding 是 global 概念）
- `getBoundCommands(target, hostId)` 必須以「該 hostId 下實際存在的 command list」為基礎再 filter binding，不可單純遍歷 `bindings` key
- 渲染順序見 §4.4

**Sync field-merge**：

- `quick-commands` contributor `DATA_FIELDS` 加入 `bindings`
- field-merge 必須測試 cross-field dangling case：local `global = [A]` + remote `bindings = { B: [...] }` 套 `field-merge { global: 'local', bindings: 'remote' }` → `getBoundCommands` 回傳空（因 B 不在 local global）
- deserialize 時對 incoming `bindings` 也跑 sanitizer

### 2.6 預設值

- `DEFAULT_COMMANDS = []`（v2 不預埋）
- `bindings` 預設 `{}`
- 既有 user 的 localStorage 已存的舊 defaults（`start-cc` / `start-codex`）不主動清除（依 alpha-no-migration 原則）；舊 user 開新版看到舊 commands 但 `bindings = {}` → 不會顯示為按鈕，須自行設 binding

---

## 3. Slot 模型

### 3.1 介面

```tsx
// 共用元件
<CommandSlot mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS} ctx={{ hostId, workspaceId }} executor={...}>
  {(cmd, ctx) => (
    /* 模組可選：自訂渲染。不傳 children 則用 default renderer */
  )}
</CommandSlot>
```

```ts
type SlotRenderer = (cmd: QuickCommand, ctx: SlotContext) => ReactNode

interface SlotConfig {
  mountTo: QuickCommandSlotId
  ctx: SlotContext
  executor: (cmd: QuickCommand, ctx: SlotContext) => Promise<void>
  render?: SlotRenderer  // 預設用內建 button
}
```

`CommandSlot` 內部執行順序：
1. `isEnabled('quick-commands')` 為 false → 直接 `return null`（見 §7.5）
2. 從 store 取 `getBoundCommands(mountTo, ctx.hostId)`（已過濾 dangling）
3. 依 §4.4 排序規則渲染
4. 點擊 → 呼叫 slot 提供的 executor

### 3.2 Slot 行為語意（v2 Phase 1 範圍）

| Slot | Ctx | Executor 行為 |
|---|---|---|
| `WORKSPACE_ACTIONS` | `{ hostId, workspaceId, cwd? }` | `POST /api/sessions` 對該 wks 的 host 建 session（cwd 取 wks default → host default fallback）→ `executeCommand(send-keys)` 送 cmd → 切到該 session |
| `HOST_ACTIONS` | `{ hostId, cwd? }` | `POST /api/sessions` 對該 host 建 session（cwd 取 host default）→ `executeCommand` → 切到該 session |

### 3.3 失敗處理 UX

孤兒 session 是 user 困擾的主因。失敗時要讓 user 看到結果。

| 階段 | 失敗 | 行為 |
|---|---|---|
| 建 session | error | toast: "Failed to start session: <reason>"，不切焦點 |
| 建 session ✅ + send-keys | error | **仍切到該 session**，toast 帶 action button: "Session created, but command failed. <Retry> <Dismiss>" |
| 切到 session | error（罕見） | toast 提示，session 仍存在於 sessions 列表 |

關鍵：**send-keys 失敗也要切過去**，user 才知道 session 在那、可以手動跑 — 這比「保留 session 但不切」少一個孤兒問題。

### 3.4 不在 Phase 1 範圍

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
- Mount targets（multi-select chips，從 `QUICK_COMMAND_SLOTS` 取選項：Workspace / Host）

空狀態：「No quick commands yet — Start by creating one.」

### 4.2 Workspace 入口

Workspace 主面板新增 quick actions 區塊（位置由實作時定，原則上靠近 workspace header）：

```
[ Workspace Name ]
[ ⚡ Start CC ] [ 🔧 Build ]    ← bindings mountTo: WORKSPACE_ACTIONS 的 commands
```

按鈕點擊 → executor 建 session + 送 keys + 切過去。

### 4.3 Host 詳情頁入口

Host 詳情頁（Sessions section 之外、靠近 host 標題的 quick actions 區）：

```
[ Host: Mini-Lab (online) ]
[ ⚡ Start CC ] [ 🔧 Build ]    ← bindings mountTo: HOST_ACTIONS 的 commands
```

按鈕點擊 → executor 對該 host 建 session + 送 keys + 切過去。

### 4.4 渲染順序、a11y、Responsive

**渲染順序穩定性（重要）**：
- SlotHost 內部以「該 hostId 的 capability list」為基礎（即 `getCommands(hostId)` 的順序，merged global + host override），逐項判斷 `bindings[cmd.id]?.includes(slotId)` 來決定是否渲染
- **不可** 遍歷 `Object.keys(bindings)` — Record 順序在 sync 後不可預期，會造成跨裝置順序不一致
- Settings list 列出 commands 時同樣依 `getCommands(hostId)` 順序

**a11y / 鍵盤焦點**：
- Settings dialog 開啟時：focus trap、Esc 關閉、關閉時 focus 回 trigger
- Multi-select chips 支援 Space / Enter 切換、方向鍵移動
- Slot button 必有 `aria-label`（`name` + `category` 後綴）+ `title` tooltip 顯示 `command` 內容（debug 用）
- Slot button 群可 Tab 線性走訪
- Toast 採 `role="status"` 或 `role="alert"`

**Responsive / dark theme**：
- Slot button 群在窄寬度 wrap（不橫向 overflow）
- Settings list row 在窄寬度堆疊（name → command → mount）
- 全採用既有 `text-text-*` / `bg-surface-*` token，不寫死顏色

---

## 5. 後端

無新增 API。沿用：
- `POST /api/sessions` — 建 session（已存在）
- `POST /api/sessions/{code}/send-keys` — 送 keys（v1 已實作）

Executor 端整合兩個呼叫即可。

---

## 6. Phase 切分

調整原則：**不 ship 「設了沒效果」的中間態**。Settings UI 首次出現時必須至少有一個 slot 生效。

### Phase 1a — 純資料層（一個 PR）

- 新增 `spa/src/lib/quick-command-slots.ts`（`QUICK_COMMAND_SLOTS` + 型別）
- Store schema 升級：加 `bindings` 欄位 + `setBinding` / `getBoundCommands` API
- `DEFAULT_COMMANDS = []`
- `removeCommand` 同步清 binding
- `useQuickCommandStore` 加 `merge` hook + `sanitizeBindings`
- Sync contributor 加入 `bindings` 至 `DATA_FIELDS`，deserialize 跑 sanitizer
- `register-modules.tsx` 註冊 `quick-commands` module（disableable: true，但暫無 settings contribution，等 1b）
- i18n keys：`modules.quick_commands.description`（給 Modules Switchboard 顯示）
- Tests：store CRUD + sanitizer + dangling filter + sync field-merge cross-field + module-enabled 整合

**驗收**：資料層完整、sync OK、Modules Switchboard 看得到本模組可開關；UI 零改動，純基礎建設。

### Phase 1b — Settings UI + WORKSPACE_ACTIONS 入口（一個 PR，**綁定一起 ship**）

- `CommandSlot` 共用元件（含 `isEnabled` short-circuit + §4.4 順序規則 + a11y）
- `executor` lib：建 session + 送 keys 一條龍 helper（含 §3.3 失敗處理 UX）
- Quick Commands Settings 頁面元件（list + edit dialog + multi-select mount chips）
- `register-modules.tsx` 補上 `settings` contribution
- Workspace 入口（quick actions 區塊使用 `CommandSlot`）
- i18n keys：`settings.section.quick_commands` / 空狀態 / 編輯 dialog labels / 失敗 toast
- Tests：CommandSlot（含 disable 隱藏 + 順序穩定性）+ executor + Settings 頁面 + workspace 入口整合

**驗收**：user 能在 Settings 建 command + 設 mount = WORKSPACE，回到 workspace 立刻看到按鈕；點擊建 session + 送 keys + 切過去。失敗 toast 行為符合 §3.3。

### Phase 1c — HOST_ACTIONS 入口（小 PR）

- Host 詳情頁的 quick actions 區塊使用 `CommandSlot`
- Tests：host 入口整合
- i18n（如有）

**驗收**：user 能設 mount = HOST 並在 host 詳情頁看到按鈕；行為與 §3.2 一致。

### 不在範圍

- 遷移 `PaneLayoutRenderer` / `SessionsSection`（Phase 2+，見 §8 decision gate）
- per-host override UI（store 支援，UI 後續疊）
- Mode 設定（terminal/stream/jsonl）作為 binding 欄位（Phase 2+）
- 動態 function command
- Command palette / 快捷鍵
- 多語言全展開（Phase 1 用最小 zh-TW + en）

---

## 7. Module 註冊與啟用 / 停用

Quick Commands v2 是**獨立 module**，遵循專案統一的 module 啟用 / 停用規範（與 `editor` / `browser` / `memory-monitor` 等 disableable 模組同型）。

### 7.1 註冊

於 `register-modules.tsx` 註冊（Phase 1b 補完 settings contribution）：

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
      order: ?,                                    // 與其他 purdex 設定 tab 排序，實作時定
      labelKey: 'settings.section.quick_commands',
      component: QuickCommandsSettingsSection,
    },
  ],
})
```

### 7.2 啟用狀態查詢與切換語意

**Settings tab 顯示 / 隱藏**：
- 由現有 settings contribution 機制自動處理（`dispatchSettingsContributions` 於 dispatch 時依 `isEnabled` 過濾）
- 與 `useModuleEnabledStore` 既有 baseline 機制一致：toggle disable 後，**Modules Switchboard 顯示「Reload required」直到重新 dispatch / reload**；Settings tab 不會立即消失
- 此行為與 `editor` / `browser` 等其他 disableable module 完全相同，user 已熟悉
- spec 不要求改造為即時 redispatch（YAGNI；reload 模型穩定）

**SlotHost (`CommandSlot`)**：
- 內部呼叫 `isEnabled('quick-commands')`，false 時 `return null`（即時生效，無需 reload）
- 切換後 workspace / host 的 slot 按鈕**會即時消失或恢復**，因 React render 訂閱 store
- Settings tab 與 slot 行為的「即時 vs reload」差異是 codebase 既有模式，本 spec 不打破

**Modules Switchboard**：
- 自動列入清單（`disableable: true`）
- 文案不暗示「停用會影響其他裝置同步」（enabled 是 device-local，commands/bindings 仍 sync）

### 7.3 停用後語意

- **資料保留**：停用 != 清除。capability + binding 資料完整保留於 store + sync。
- **UI 隱藏**：所有 slot 按鈕即時消失；Settings tab 於 reload 後消失。
- **重新啟用**：slot 按鈕即時恢復；Settings tab reload 後出現；原 binding 直接生效。
- **預設**：`enabled = true`（`useModuleEnabledStore` 預設值），user 須主動關閉。

### 7.4 Sync 行為

| 資料 | Sync? | 理由 |
|---|---|---|
| Capability (commands) | ✅ Yes（既有） | 指令庫應跨設備共用 |
| Binding | ✅ Yes（新增） | mount 偏好應跨設備共用 |
| 模組 enabled 狀態 | ❌ No | device-local（與既有 `useModuleEnabledStore` 一致：低資源 host 可關閉模組） |

亦即：在桌機建好的 commands + bindings 切到行動裝置會同步看到；行動裝置可獨立關閉本模組不影響桌機資料 — 這是預期行為，UI 不需特別提示。

### 7.5 Slot 元件契約

`CommandSlot` 須在 render 一開頭 short-circuit：

```tsx
function CommandSlot({ mountTo, ctx, executor, render }: Props) {
  const enabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  if (!enabled) return null
  // ... 後續渲染邏輯（順序見 §4.4）
}
```

避免每個 consumer 自己檢查；模組啟用狀態是 slot 內建語意。

---

## 8. Migration / 相容性 + Phase 2 Decision Gate

### 8.1 Phase 1 相容性

- **既有 module-contributed commands** (`module-registry.commands?`) — Phase 1 暫保留，不參與 binding 模型；新 slot 只看 store bindings
- **舊 `useCommands` hook** — 保留（`PaneLayoutRenderer` / `SessionsSection` 還在用）。Phase 1 不刪
- **localStorage** — 沒有 schema migration（alpha-no-migration），既有 user hydrate 後 `bindings = {}` → 沒有按鈕顯示，須自行編輯設 binding

### 8.2 Phase 2 Decision Gate（必須在 Phase 1c merge 前討論）

避免雙軌長期共存變技術債，Phase 2 起需在以下兩條路擇一：

**Option A：適配為 read-only capability**
- `module-registry.commands?` 收合為 store 的「不可編輯 capability」（標記 `source: 'module'`）
- Settings UI 顯示但禁止編輯，仍可設 binding
- 統一只剩 binding 模型，舊 `useCommands` 廢棄

**Option B：標 legacy + 凍結 API**
- `module-registry.commands?` 標 `@deprecated`，禁止新模組註冊
- 舊 consumer (`PaneLayoutRenderer` / `SessionsSection`) 遷移用 `CommandSlot`
- 完成後刪除 `commands?` 欄位

決議由 Phase 1 完成後實際使用情況評估（看 module 貢獻 commands 的真實價值再決定）。本 spec 不預先選邊。

---

## 9. 不做的事（強調）

- 不做動態 function command（v1 有，v2 移除）
- 不做 per-host bindings（bindings 是 global；per-host overrides 只有 capability 層；Phase 1 UI 不暴露 per-host）
- 不做 command 排程 / 歷史記錄 / 變數插值
- 不做 binding 的 `when` 條件（VS Code 那種 expression 系統）
- 不重構既有 module-registry.commands 模組貢獻擴充點（Phase 1 範圍內）
- 不做 `registerQuickCommandSlot()` registry（YAGNI；Phase 2 視需求再考慮）
- 不改造 module enable/disable 為即時 redispatch（依現有 baseline reload 模型）

# Sync Phase P0 — 體質清理 Design Spec

**Date**: 2026-04-18
**Status**: Design approved, ready for plan
**Roadmap**: Phase P0 of `memory/project_sync_roadmap.md`

## 1. 目標

讓「Sync via Import/Export + Daemon」這條路徑達到「可以當主管道使用」的品質，為後續 Phase（P1 History / P2 Pairing / P3 File / P4 Cloud / P5 content-addressed / P6 Onboarding）解鎖。

具體：修掉 4 個已知 bug、把整個 `SyncSection` 走 i18n、補上衝突解決 UI（engine 已就緒但無 UI）、把「有衝突待處理」的提示 promote 到全域 TitleBar 讓使用者離開 Settings 也看得到。

## 2. Non-Goals

- **Daemon Pairing UI**（P2，gh #421）— 不在本 PR
- **Sync History / 本地 snapshot 還原**（P1）— 不在本 PR
- **FileProvider**（P3）— 不在本 PR
- **Cloud Provider**（P4，gh #422）— 不在本 PR
- **Content-addressed chunks**（P5）— 不在本 PR
- **Onboarding flow**（P6，gh #423）— 不在本 PR

## 3. 檔案改動概觀

**修改**：
- `spa/src/lib/sync/use-sync-store.ts` — 加 `pendingConflicts` 狀態
- `spa/src/lib/sync/providers/daemon-provider.ts` — URL encode（#394）
- `spa/src/lib/sync/providers/manual-provider.ts` — import guards（#396）
- `spa/src/components/settings/SyncSection.tsx` — i18n 遷移 + busy guard（#395）+ 接 banner
- `spa/src/components/TitleBar.tsx` — 加 warning icon
- `spa/src/components/SettingsPage.tsx` — URL deep link sync
- `spa/src/lib/route-utils.ts` — `/settings/<section>` 支援
- `spa/src/locales/en.json` + `zh-TW.json` — 新 i18n keys

**不改動但需驗證不受影響**：
- `spa/src/hooks/useRouteSync.ts` — `parseRoute` 擴 `section?` 欄位後仍走 settings kind 分支；`tabToUrl` 不帶 section，Tab→URL 與既有一致

**新增**：
- `spa/src/components/settings/SyncConflictBanner.tsx`
- `spa/src/components/settings/SyncConflictBanner.test.tsx`
- `spa/src/lib/object-depth.ts` + test（#396 util）

**測試影響**：
- `daemon-provider.test.ts` / `manual-provider.test.ts` / `SyncSection.test.tsx` / **擴充** `TitleBar.test.tsx` / `SettingsPage.test.tsx` 各加 case

## 4. SyncStore 改動

### 4.1 新增欄位

```ts
interface SyncStoreState {
  // ...既有
  pendingConflicts: ConflictItem[]
  pendingRemoteBundle: SyncBundle | null
  pendingConflictsAt: number | null  // Unix ms
  // actions
  setPendingConflicts(conflicts: ConflictItem[], remoteBundle: SyncBundle): void
  clearPendingConflicts(): void
}
```

- 三個欄位都走 `partialize`，進 localStorage persist
- `setPendingConflicts()` 寫入同時更新 `pendingConflictsAt = Date.now()`
- `clearPendingConflicts()` 三個欄位一起清
- `reset()` 新增把三個欄位回 initial

### 4.2 SyncSection 接線

```ts
// 原本：
if (result.kind === 'conflicts') {
  setLastSyncedBundle(result.partialBaseline)
}
// 改為：
if (result.kind === 'conflicts') {
  setLastSyncedBundle(result.partialBaseline)
  setPendingConflicts(result.conflicts, result.remoteBundle)
}
```

（`sync-actions.ts` 目前已回傳 `remoteBundle`，無需改介面）

**退役 `statusFromResult` 衝突分支**：現有 `statusFromResult()` 對 `conflicts` 分支寫死 `"Resolution UI coming soon; local data preserved."`。ConflictBanner 上線後此訊息多餘：

```ts
// 舊：
if (result.kind === 'conflicts') {
  return { tone: 'warn', message: `${result.conflicts.length} field conflict(s) detected. ...` }
}
// 新：衝突不再走 StatusLine；呼叫端不用再包 statusFromResult 的 conflict 分支。
// 若要保留最小回饋，status 設 IDLE 由 banner 接手；或簡潔顯示：
//   { tone: 'warn', message: t('settings.sync.status.conflictsPending', { count: result.conflicts.length }) }
```

新增 i18n key `settings.sync.status.conflictsPending`（見 §8）。

## 5. ConflictBanner 元件

### 5.1 Props

```ts
interface SyncConflictBannerProps {
  conflicts: ConflictItem[]
  remoteBundle: SyncBundle
  pendingAt: number
  onResolve(resolved: ResolvedFields): void   // ResolvedFields = Record<string, 'local' | 'remote'>
  onDismiss(): void
}
```

- `onResolve` 產出的 `ResolvedFields` 和 `engine.resolveConflicts()` 的第三參數型別一致（engine 已定義）。key 是 flat field name（非 compound），若兩 contributor 各自都有同名 field，兩邊吃到同一個 choice — 這是 engine 既有行為、本 banner 不修正。
- 內部 state 用 compound key `${contributor}::${field}` 追蹤 per-row 選擇，送出時 flatten 為 `ResolvedFields`（取 field name）。
- `onDismiss` 純 UI 收合，**不清 pendingConflicts**（保留在 store 直到 resolve）

### 5.2 視覺行為

**Collapsed（預設）**：

```
┌─────────────────────────────────────────────┐
│ ⚠  3 個欄位有衝突     [ 查看詳情 ] [ 收起 ] │
└─────────────────────────────────────────────┘
```

- 黃色 warning icon（Phosphor `Warning`，size 14，`text-yellow-500`）
- 「收起」= 把整個 banner 折疊成 icon-only pill；`pendingConflicts` 仍存
- `pendingConflictsAt` > 24h ago：下方加灰字「衝突資料已超過 24 小時，建議重新同步」

**Expanded**：

```
┌─────────────────────────────────────────────────────────┐
│ ⚠  3 個欄位有衝突                         [ 收起 ]      │
├─────────────────────────────────────────────────────────┤
│ preferences.theme                                        │
│   上次同步: "light"  (device: iPad @ 2026-04-17)         │
│   本地:    ◯ "dark"                                     │
│   遠端:    ◉ "solarized"  (from: MacBook)               │
│                                                          │
│ hosts.mini-lab.port                                      │
│   上次同步: 7860                                         │
│   本地:    ◯ 7861                                       │
│   遠端:    ◯ 7862  (from: MacBook)                      │
│   ...                                                    │
│                                                          │
│ [ 全部保留本地 ] [ 全部採用遠端 ]                        │
│                                                          │
│                          [ 取消 ] [ 套用（已選 2/3） ]   │
└─────────────────────────────────────────────────────────┘
```

- 每行一個 `ConflictItem`，兩顆 radio（local / remote）二選一，初始都不選
- 上下 `[全部保留本地] [全部採用遠端]` 批次快速選；再點其他 radio 會覆蓋
- `[套用]` **all-or-nothing**：**N = `conflicts.length`（row 數，非 unique field 數）**；每一 row 都要選過才 enabled；label 顯示 `(已選 N/總 N)`
- **Collision 行為**：若 row A 是 `preferences.theme`、row B 是 `layout.theme`，兩 row 個別可選但 flatten 為 `ResolvedFields` 時只剩一個 `theme` key — **後 flatten 的 row 蓋掉前面**（這是 engine 既有限制，不修正）。開發者若想穩定順序，flatten 迴圈依 `conflicts` 陣列順序走。UI 不顯額外提示（P0 scope 不擴展）；未來若問題顯現再 track 成 follow-up issue。
- `[取消]` = 收合 + 清除 UI local 選擇狀態（不動 store）
- 字串全部走 i18n（見 §8）

### 5.3 解決流程

**重要**：`syncEngine.resolveConflicts()` 的實際簽名是 `(remoteBundle, conflicts, resolved: ResolvedFields) => void`，**不回傳 bundle**，而是直接 side-effect 呼 `contributor.deserialize()` 把 merged data 套到本地 store。因此 SyncSection 不能 `const applied = ...`。

正確接線：

```ts
// SyncSection 收到 banner onResolve(resolved)：
syncEngine.resolveConflicts(pendingRemoteBundle, pendingConflicts, resolved)
// 本地 store 此時已是「non-conflicting 欄位 = remote」+「conflicting 欄位 = 使用者選擇」。
// 新 baseline = pendingRemoteBundle（衝突欄位已依 resolved 套到本地，若有衝突欄位使用者選 'local'，
// 則本地該欄 != remoteBundle 對應欄；下次 sync 時 three-way-merge 會把本地當新變更正確處理。
// 以 remoteBundle 當 baseline 語義正確：代表「已對齊這個遠端版本」）。
setLastSyncedBundle(pendingRemoteBundle)
clearPendingConflicts()
setStatus({ tone: 'success', message: t('settings.sync.conflict.resolved', { count: pendingConflicts.length }) })
```

**語義驗證（不變量）**：
- 若使用者全選 `remote`：本地 = remote，baseline = remote → 下次 pull 若 remote 未再變，`local == lastSynced` → 無衝突
- 若使用者全選 `local`：本地 contributor 保持舊值（engine `resolveConflicts` 對 `'local'` 不動），baseline = remote → 下次 push 時 local vs lastSynced 不同 → push 正確反映本地選擇
- 混合選擇：同上，per-field 獨立判斷

（如果未來要讓 `resolveConflicts` 回 bundle 以免 SyncSection 自己推論 baseline，屬 engine 改動、另外 issue 追；本 spec 依現有簽名定義流程。）

### 5.4 位置

掛在 `SyncSection` 頂端（在 provider selector 之前），條件：`pendingConflicts.length > 0` 且 `activeProviderId != null`。

不做成全域 banner（只讓 TitleBar icon 當全域提示）。

## 6. TitleBar Warning Icon

### 6.1 改動

在 `TitleBar.tsx` 的中央 title 區塊，title 文字後加一顆 icon：

```tsx
<div className="absolute inset-0 flex items-center justify-center pointer-events-none select-none px-2 gap-2">
  <span className="text-xs text-text-secondary truncate max-w-[calc(100%-26rem)]">{title}</span>
  {pendingCount > 0 && (
    <button
      className="pointer-events-auto flex items-center"
      style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      title={t('settings.sync.conflict.tooltip', { count: pendingCount })}
      onClick={() => setLocation('/settings/sync')}
    >
      <Warning size={14} className="text-yellow-500" />
    </button>
  )}
</div>
```

### 6.2 細節

- `pendingCount = useSyncStore((s) => s.pendingConflicts.length)`
- `t` from `useI18nStore((s) => s.t)`
- `setLocation` from `wouter` `useLocation`
- `pointer-events-auto` 必要（container 預設 `pointer-events-none` 讓 drag 穿透）
- `WebKitAppRegion: 'no-drag'` 必要（和既有 layout buttons 一樣）
- 顏色 `text-yellow-500`，不引入 css var（既有 pattern 用 tailwind color class）
- **寬度檢查**：既有 title 的 `max-w-[calc(100%-26rem)]` 是以右側 4+4 buttons + padding 計算。加入 icon 後，icon 也佔中央 flex 容器寬度。實作時驗證：
  - 長 workspace 名 + icon 顯示時 title 不溢出（ellipsis 生效）
  - Icon 不被 title 擠出右側 buttons 區
  - 若需微調：把 icon 放到 title `<span>` 同 container gap-2，必要時把 `max-w` 減到 `calc(100%-27rem)` 留出 icon 寬度（~1rem）

### 6.3 測試

- 渲染 0 conflicts → 不顯示 icon
- 渲染 N conflicts → 顯示 icon + tooltip 含 N
- click 呼叫 `setLocation('/settings/sync')`
- Electron drag 區測試（確認 no-drag 作用）

## 7. /settings/sync Deep-link

### 7.1 parseRoute 擴展

```ts
export type ParsedRoute =
  | { kind: 'history' }
  | { kind: 'hosts' }
  | { kind: 'settings'; scope: 'global'; section?: string }  // <-- new optional field
  // ...
```

```ts
// parseRoute
if (path === '/settings') return { kind: 'settings', scope: 'global' }
if (path.startsWith('/settings/')) {
  const section = path.slice('/settings/'.length)
  // 驗證 section id 合法（a-z0-9, max 32）
  if (/^[a-z0-9-]{1,32}$/.test(section)) {
    return { kind: 'settings', scope: 'global', section }
  }
  return { kind: 'settings', scope: 'global' }
}
```

### 7.2 GlobalSettingsPage 讀取 URL

```tsx
function GlobalSettingsPage() {
  const [location, setLocation] = useLocation()
  const sections = getSettingsSections()

  // Extract section from URL
  const urlSection = location.startsWith('/settings/')
    ? location.slice('/settings/'.length)
    : null

  const [activeSection, setActiveSection] = useState(
    () => {
      if (urlSection && sections.some((s) => s.id === urlSection)) return urlSection
      if (lastSection && sections.some((s) => s.id === lastSection)) return lastSection
      return sections.find((s) => s.component)?.id ?? ''
    },
  )

  // URL 變化時同步 activeSection（back/forward 支援）
  useEffect(() => {
    if (urlSection && sections.some((s) => s.id === urlSection) && urlSection !== activeSection) {
      setActiveSection(urlSection)
      lastSection = urlSection
    }
  }, [urlSection])

  const handleSelectSection = (id: string) => {
    lastSection = id
    setActiveSection(id)
    setLocation(`/settings/${id}`, { replace: true })
  }
  // ...
}
```

### 7.3 useRouteSync 影響

`tabToUrl` 對 settings tab 永遠 return `/settings`，不會自動帶 section。這樣 Tab→URL 不破壞既有行為；URL→Tab 已能接 `/settings/sync`（parseRoute 回 settings kind + section，useRouteSync 仍 open singleton settings tab，SettingsPage 再讀 URL）。

額外：handleSelectSection 用 `replace: true` 避免每點一次 section 就進一筆 history。

### 7.4 測試

- `parseRoute.test.ts`：`/settings/sync` → `{ kind: 'settings', scope: 'global', section: 'sync' }`
- `parseRoute.test.ts`：`/settings/bad..name` → 無 section（fallback）
- `SettingsPage.test.tsx`：URL `/settings/sync` 初始 → activeSection = 'sync'
- `SettingsPage.test.tsx`：sidebar click → setLocation 被呼叫，URL 變

**測試隔離策略（必要）**：`GlobalSettingsPage` 現在讀/寫 `wouter` location，測試若不隔離會污染 `window.location` 並在測試之間洩漏 state。兩種做法二選一（開發時統一用其一）：

1. **memoryLocation hook（推薦）**：
   ```tsx
   import { Router } from 'wouter'
   import { memoryLocation } from 'wouter/memory-location'
   const { hook } = memoryLocation({ path: '/settings/sync' })
   render(<Router hook={hook}><SettingsPage ... /></Router>)
   ```
2. **全 mock `wouter`**：`vi.mock('wouter', () => ({ useLocation: () => ['/settings/sync', mockSet] }))`

既有 `SettingsPage.test.tsx`（在改動前不依賴 wouter）必須加入上述 pattern 才能通過。**Plan 寫作時此步驟不得省略**。

## 8. i18n Keys

### 8.1 新增 keys（必要最小集合）

```
settings.sync.description
settings.sync.provider.label
settings.sync.provider.description
settings.sync.provider.off
settings.sync.provider.daemon
settings.sync.provider.file
settings.sync.host.label
settings.sync.host.description
settings.sync.host.placeholder
settings.sync.host.option  // "{name} ({ip}:{port})"
settings.sync.status.label
settings.sync.status.neverSynced
settings.sync.status.lastSynced  // "Last sync: {time}"
settings.sync.status.syncing
settings.sync.status.complete
settings.sync.status.exported
settings.sync.status.importApplied
settings.sync.status.importFailed  // "Import failed: {reason}"
settings.sync.status.onlyDaemon    // "Sync Now is only available with the Daemon provider for now."
settings.sync.status.selectHost    // "Select a sync host first."
settings.sync.status.conflictsPending  // "{count} conflict(s) pending — see banner above"
settings.sync.modules.label
settings.sync.modules.description
settings.sync.ioActions.label
settings.sync.ioActions.description
settings.sync.ioActions.exportAll
settings.sync.ioActions.import
settings.sync.syncNow
settings.sync.time.secondsAgo      // "{n}s ago"
settings.sync.time.minutesAgo      // "{n}m ago"
settings.sync.time.hoursAgo        // "{n}h ago"
settings.sync.time.daysAgo         // "{n}d ago"

settings.sync.conflict.banner      // "⚠ {count} field conflict(s)"
settings.sync.conflict.tooltip     // "{count} sync conflict(s) pending"  ← TitleBar
settings.sync.conflict.viewDetails
settings.sync.conflict.collapse
settings.sync.conflict.lastSynced  // "Last synced: {value} (device: {device} @ {time})"
settings.sync.conflict.local
settings.sync.conflict.remote      // "Remote ({device})"
settings.sync.conflict.keepAllLocal
settings.sync.conflict.useAllRemote
settings.sync.conflict.apply       // "Apply ({selected}/{total})"
settings.sync.conflict.cancel
settings.sync.conflict.resolved    // "Resolved {count} conflict(s)"
settings.sync.conflict.stale       // "Conflict data is over 24 hours old. Consider re-syncing."

settings.sync.import.error.tooLarge  // "File too large (max {mb} MB)"
settings.sync.import.error.tooDeep   // "Import structure too deep (max {depth} levels)"
```

### 8.2 處理規則

- `en.json` 和 `zh-TW.json` 同步加（`locale-completeness.test.ts` 會擋）
- 既有 `settings.section.sync: "Sync"` 保留
- SyncSection 全部 `t()` 化，移除所有 hardcoded string（`#397` 要求）

## 9. Bug Guards

### 9.1 `#394` DaemonProvider URL Encode

`daemon-provider.ts`：

```ts
async push(bundle: SyncBundle): Promise<void> {
  const qs = `?clientId=${encodeURIComponent(clientId)}`
  const res = await hostFetch(hostId, `/api/sync/push${qs}`, { ... })
  // ...
}
// 同上 pull / history
async listHistory(limit: number): Promise<SyncSnapshot[]> {
  if (!Number.isInteger(limit) || limit < 1) {
    throw new Error('listHistory: limit must be a positive integer')
  }
  const qs = `?clientId=${encodeURIComponent(clientId)}&limit=${limit}`
  // ...
}
```

### 9.2 `#395` Export Busy Guard

```ts
const handleExportAll = () => {
  if (busy) return  // ← 新增
  const clientId = getClientId()
  // ...
}
```

### 9.3 `#396` Import Size + Depth Limit

**typed error**（避免 UI 端靠 string match 判斷類型）：

```ts
// spa/src/lib/sync/providers/manual-provider.ts
export type ImportErrorCode = 'too-large' | 'too-deep' | 'invalid-json' | 'invalid-shape'
export class ImportError extends Error {
  constructor(public code: ImportErrorCode, message: string) {
    super(message)
  }
}
```

新 util `spa/src/lib/object-depth.ts`：

```ts
export function objectDepth(value: unknown, max = 32): number {
  if (value == null || typeof value !== 'object') return 0
  let depth = 0
  const stack: { val: unknown; d: number }[] = [{ val: value, d: 1 }]
  while (stack.length > 0) {
    const { val, d } = stack.pop()!
    if (d > max) throw new Error(`object depth exceeds ${max}`)
    depth = Math.max(depth, d)
    if (val && typeof val === 'object') {
      for (const child of Object.values(val as object)) {
        if (child && typeof child === 'object') stack.push({ val: child, d: d + 1 })
      }
    }
  }
  return depth
}
```

`manual-provider.ts::importFromText`：

```ts
const MAX_BYTES = 5 * 1024 * 1024
const MAX_DEPTH = 32

export function importFromText(text: string): SyncBundle {
  if (text.length > MAX_BYTES) {
    throw new ImportError('too-large', `bundle too large (${text.length} > ${MAX_BYTES})`)
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch (e) {
    throw new ImportError('invalid-json', (e as Error).message)
  }
  try {
    objectDepth(parsed, MAX_DEPTH)
  } catch {
    throw new ImportError('too-deep', `bundle depth exceeds ${MAX_DEPTH}`)
  }
  // ...既有 shape validation — 轉成 ImportError('invalid-shape', ...)
}
```

`SyncSection` `handleFileChange` catch 分支依 code 翻 i18n：

```ts
} catch (err) {
  let friendly: string
  if (err instanceof ImportError) {
    switch (err.code) {
      case 'too-large': friendly = t('settings.sync.import.error.tooLarge', { mb: 5 }); break
      case 'too-deep':  friendly = t('settings.sync.import.error.tooDeep', { depth: 32 }); break
      default:          friendly = t('settings.sync.status.importFailed', { reason: err.message })
    }
  } else {
    const msg = err instanceof Error ? err.message : String(err)
    friendly = t('settings.sync.status.importFailed', { reason: msg })
  }
  setStatus({ tone: 'error', message: friendly })
}
```

## 10. 測試策略

### 10.1 新測試

- `SyncConflictBanner.test.tsx`
  - collapsed 顯示 count
  - expanded 顯示 per-field rows
  - 所有 field 選完才 enable apply
  - 全部保留本地 / 全部採用遠端 批次選
  - 取消收合但不清 store
  - 套用呼叫 `onResolve` 含正確 resolved map
  - > 24h 顯示 stale warning
  - 同名 field collision（兩 row flatten 後 `ResolvedFields` 只剩 1 entry，值為後 row 的選擇）
- `TitleBar.test.tsx`（**既有檔，擴充**）
  - 0 conflicts 不顯示 icon
  - N conflicts 顯示 icon + tooltip 含 N
  - click 呼叫 setLocation('/settings/sync')
  - Banner guard：父層傳 pendingConflicts 空陣列 → `<SyncConflictBanner />` 不該掛載（由 SyncSection 條件渲染防禦）
- `object-depth.test.ts`
  - 淺 obj 回正確深度
  - 超過 max 丟 error
  - null / primitive 回 0
  - 循環引用保護（雖然 JSON.parse 不會產生，但 util 層面）

### 10.2 擴充測試

- `manual-provider.test.ts`：
  - oversized text 抛 `ImportError('too-large')`
  - too-deep object 抛 `ImportError('too-deep')`
  - **既有 JSON 錯誤 assertion 要更新**：從 `expect(...).toThrow(SyntaxError)` 改為 `expect(...).toThrow(ImportError)` 且 `err.code === 'invalid-json'`
  - 既有 shape validation 錯誤改成 `ImportError('invalid-shape')`
- `daemon-provider.test.ts`：clientId 含 `/&?=` 等字元的 URL encode；limit 非整數丟 error
- `SyncSection.test.tsx`：concurrent export + import 不衝突；衝突出現後 store pending 寫入；apply 成功後 store clear + baseline 等於 remoteBundle
- `parseRoute.test.ts`：`/settings/sync` → section 解析；惡意 section 字串 fallback
- `SettingsPage.test.tsx`：初始 URL `/settings/sync` 選中 sync；sidebar 點擊同步 URL（測試用 `wouter/memory-location`，見 §7.4）
- `use-sync-store.test.ts`：`setPendingConflicts` / `clearPendingConflicts` 行為 + persist

### 10.3 手動 integration

- 啟 dev server（`cd spa && pnpm run dev`）
- 造假衝突：用兩個 browser profile 開 SPA，各自改同一個 preference 然後互 sync
- 驗證：
  - 衝突出現 → banner 顯示 → TitleBar icon 顯示
  - 關 Settings → 開別的 tab → TitleBar icon 還在 → hover 看 tooltip
  - 點 icon → 跳 `/settings/sync` → banner 在頂
  - Expand → 逐欄選 → apply → banner 消失 → TitleBar icon 消失
  - 驗 localStorage：重新整理網頁後 pending 仍存（persist 正確）

## 11. Dependencies / 前置條件

- 無外部新依賴（Phosphor Warning icon 已在用、wouter 已在用、zustand persist 已在用）

## 12. 完成標準

- 所有新舊測試通過（`cd spa && npx vitest run`）
- Lint 過（`pnpm run lint`）
- Build 過（`pnpm run build`）
- PR 兩輪 review（code-review skill + 正反方三路 parallel agent）
- 手動 integration 過
- bump alpha 版本號
- 更新 CHANGELOG
- 更新 `memory/project_sync_architecture.md` 標記 P0 完成
- 更新 `memory/project_sync_roadmap.md` 把 P0 改 ✅、P1 改 🚧

## Related

- Parent spec: `docs/superpowers/specs/2026-04-16-sync-architecture-design.md` §6
- Parent plan: `docs/superpowers/plans/2026-04-16-sync-architecture.md` Task 12
- Roadmap: `memory/project_sync_roadmap.md`
- Deferred: gh #421（P2 Pairing） / gh #422（P4 Cloud） / gh #423（P6 Onboarding）
- Issues addressed: #394 / #395 / #396 / #397

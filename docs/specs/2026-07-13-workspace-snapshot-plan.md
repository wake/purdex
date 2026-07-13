# Plan — Workspace Snapshot（工作區快照 / 一鍵重建）

> **For agentic workers:** 依 `superpowers:subagent-driven-development` 每個 Task 派 fresh subagent（TDD：先寫測試→跑紅→實作→跑綠→commit），主 session 只做整合與 task 間 review。Steps 用 checkbox 追蹤。

實作定稿 spec `docs/specs/2026-07-13-workspace-snapshot-spec.md`（`3d9c69b`，codex R1–R3 全過）。純前端 SPA 功能，daemon 大概率零改動（唯一待探明：§8.1 撞同名 session，Phase 2 T3 探）。

**Goal:** 拍下當前整個工作區（workspace/tab/pane 結構 + 每個 tmux session 的 name/cwd），讓伺服器重開機 / tmux 重啟後依 name+cwd 一鍵把工作環境重建回來。

**Architecture:** 三層純邏輯（capture / storage / restore，不依賴 React，Vitest 直測）+ 一個 Settings section UI。restore 建立在兩個 primitive 上：`ensureSessions`（對帳 + 重建，回傳 `(hostId,oldCode)→RemapEntry` 複合鍵表）與 `remapLayoutSessions`（純函式改寫 layout 樹）。三個還原動作（重建所有 session / 還原 tab 佈局 / 全部還原）由這兩個 primitive 組合。

**Tech Stack:** React 19 / Zustand 5（既有 store，直接 `setState` merge-mode 整包取代）/ Vitest + RTL / Phosphor Icons。

## Global Constraints

- **Package manager `pnpm`**；測試 `cd spa && npx vitest run`、lint `pnpm run lint`、build `pnpm run build`。
- **複合鍵**：`sessionCode` 是 **host-local**，`sessionMeta` / `Remap` 一律 `Record<hostId, Record<sessionCode, …>>` 巢狀索引；禁用裸 code。
- **`restorable`**：cwd 有值且 host 可達才 `true`；`restorable === false` 者 restore **不呼叫 `createSession`**，直接標 terminated。
- **重建範圍＝產品決策（a 點，勿翻案）**：「重建所有 session」重建快照裡**所有** `restorable` 且對帳已死的 session，**不限**當前 tab 是否引用；orphan 為預期。`onlyTerminated` 只收窄 **remap 套用範圍**（改當前哪些 pane），與重建範圍獨立。
- **取代為 best-effort 一致、非跨視窗真原子**；失敗「不留半套」僅指前端 store（daemon 已建 session 是真副作用，揭露不自動刪除）。
- **每個 Task 獨立 commit**；不直推 main（走 PR）。

---

## What already exists（逐字簽章，do not rebuild）

**`spa/src/lib/host-api.ts`**
```ts
export interface Session {                          // :7-18
  code: string; name: string; cwd: string; mode: string
  cc_session_id: string; cc_model: string; has_relay: boolean
  current_command?: string; pane_title?: string; window_name?: string
}
export async function listSessions(hostId: string): Promise<Session[]>          // :94 (fail→throw)
export async function createSession(hostId: string, name: string, cwd: string, mode: string): Promise<Session>  // :100
export async function fetchSessionCwd(hostId: string, sessionCode: string, signal?: AbortSignal): Promise<string> // :157
```

**`spa/src/types/tab.ts`** — `Tab { id; pinned; locked; createdAt; layout: PaneLayout }`（:5）；`PaneLayout = { type:'leaf'; pane:Pane } | SplitLayout`（:14）；`Pane { id; content: PaneContent }`（:22）；`PaneContent` tmux-session 分支 = `{ kind:'tmux-session'; hostId; sessionCode; mode:'terminal'|'stream'; cachedName; tmuxInstance; terminated?: TerminatedReason }`（:35）；`TerminatedReason = 'session-closed'|'tmux-restarted'|'host-removed'`（:28）；`Workspace { id; name; icon?; iconWeight?; tabs: string[]; activeTabId: string|null; moduleConfig? }`（:53）；`createTab(content, opts?)`（:64）、`createWorkspace(name, icon?)`（:74）、`isStandaloneTab(tabId, workspaces)`（:85）。

**`spa/src/lib/pane-tree.ts`** — `scanPaneTree(layout, fn:(pane:Pane)=>void): void`（:41）；`collectLeaves(layout): Pane[]`（:136）；`updatePaneInLayout(layout, paneId, content): PaneLayout`（:24）。**注意** `findTabBySessionCode` 只比 primary pane，快照掃全樹一律用 `scanPaneTree`。

**`spa/src/stores/useTabStore.ts`** — `TabState { tabs: Record<string,Tab>; tabOrder: string[]; activeTabId: string|null; visitHistory: string[]; … }`（:127）。persist partialize 只存 `tabs/tabOrder/activeTabId`（**`visitHistory` 不 persist**，:454）。actions：`setTabLayout(tabId, layout)`（:349）、`markTerminated(hostId, sessionCode, reason)`（:426）、`updateSessionCache(hostId, sessionCode, cachedName)`（:408）。**整包取代無專用 action → `useTabStore.setState({ tabs, tabOrder, activeTabId, visitHistory })`**（merge-mode）。

**`spa/src/features/workspace/store.ts`** — `WorkspaceState { workspaces: Workspace[]; activeWorkspaceId: string|null; … }`（:9）。persist partialize `workspaces/activeWorkspaceId`（:292）。`importWorkspace(ws)` 只在 id 不存在時 append（:266）；`reset()` → `{ workspaces:[], activeWorkspaceId:null }`（:290）。整包取代 → `useWorkspaceStore.setState({ workspaces, activeWorkspaceId })`。關 tab 讀 `useTabStore.getState().visitHistory` 選下一個 tab（`closeTabInWorkspace` :186–219）。

**`spa/src/stores/useSessionStore.ts`** — `sessions: Record<string, Session[]>`（hostId→array，**不 persist**）；`replaceHost(hostId, sessions: Session[])`（:29，整包替換單 host）；`fetchHost(hostId)`（:23）。`SessionPaneContent` 以 `sessions[hostId].find(s=>s.code===sessionCode)` 取 live session（:27）。

**`spa/src/lib/composite-key.ts`** — `compositeKey(hostId, sessionCode): string` → `` `${hostId}:${sessionCode}` ``。

**`spa/src/lib/storage/`** — `browserStorage: StateStorage`（`getItem/setItem/removeItem`，`setItem` 有 `prev===value` 短路 + `syncManager.notify(name)`；browser-backend.ts:4）；`STORAGE_KEYS`（keys.ts，`as const`）。

**`spa/src/lib/settings-section-registry.ts`** — `registerSettingsSection({ id, label, order, component: React.ComponentType })`（:66，`label` 是 i18n key，`component===undefined` 會 warn+return）。註冊點 `spa/src/lib/register-modules/index.tsx:307+`（用 `SETTINGS_ORDER.*` 常數）。UI 範本 `spa/src/components/settings/SyncSection.tsx`（status tone toast + `SettingItem` + Phosphor icon 按鈕 + `busy` 並發保護）。

## Decisions（user + codex, this session）

1. **重建範圍 a 點**：見 Global Constraints（override codex R2 orphan 收窄）。
2. **PR / Phase 切分**：**Phase 1 資料模型+capture+storage** → **Phase 2 restore 引擎+三動作** → **Phase 3 Settings UI**，各獨立 PR + codex 兩輪 review。
3. **restore 失敗揭露（R3 B）**：orchestration 回傳 `rebuiltButUnattached: Array<{hostId,name,cwd}>`；UI toast + `console.warn` 揭露，不自動刪 daemon session。
4. **`validateSnapshotConsistency` 範圍界定（R3 C）**：只驗 tab/workspace 導航參照（五條），不驗 pane content 語意參照（`settings.scope.workspaceId`）。
5. **`ensureSessions` 加 `{ rebuild?: boolean }`**：「還原 tab 佈局」用 `rebuild:false`（死的一律 `failed`→terminated、不 createSession），統一 primitive（實作細化 spec §3.5「還原 tab 佈局：不重建」）。

---

## Phase 1 — 資料模型 + capture + 持久化（PR 1）

新目錄 `spa/src/lib/snapshot/`。純邏輯 TDD，mock 兩 store + `host-api`。

### T1 — 型別 + storage 讀寫（`spa/src/lib/snapshot/types.ts` + `storage.ts`）

**Files:** Create `types.ts`、`storage.ts`；Test `storage.test.ts`。

**Produces:**
```ts
// types.ts
export interface SessionMeta {
  hostId: string; sessionCode: string; name: string
  mode: 'terminal' | 'stream'
  cwd?: string; currentCommand?: string
  restorable: boolean
  captureError?: 'host-unreachable' | 'session-dead-at-capture'
}
export interface WorkspaceSnapshot {
  version: 1; capturedAt: number
  tabs: Record<string, Tab>; tabOrder: string[]; activeTabId: string | null
  workspaces: Workspace[]; activeWorkspaceId: string | null
  sessionMeta: Record<string, Record<string, SessionMeta>>   // [hostId][sessionCode]
}
export interface CaptureResult { total: number; resolved: number; unresolved: number }
import type { Session } from '../host-api'
export type RemapEntry =
  | { status: 'reattached'; newCode: string; session: Session }
  | { status: 'rebuilt';    newCode: string; session: Session }
  | { status: 'failed' }
export type Remap = Record<string, Record<string, RemapEntry>>  // [hostId][oldCode]
export interface EnsureReport { reattached: number; rebuilt: number; failed: number }
export interface RestoreReport extends EnsureReport {
  rebuiltButUnattached: Array<{ hostId: string; name: string; cwd: string }>  // R3 B
}
// 失敗契約（codex 複審 B2）：三動作成功 resolve RestoreReport；replaceTabSnapshot throw
// 時包成 RestoreError（帶已收集含 rebuiltButUnattached 的 report）reject，UI catch 後讀 e.report。
export class RestoreError extends Error {
  constructor(public report: RestoreReport, public cause?: unknown) { super('restore failed') }
}
// storage.ts (走 browserStorage，自己 JSON.stringify；key 常數見下)
export const SNAPSHOT_KEY = 'purdex-workspace-snapshot'
export const SNAPSHOT_PREV_KEY = 'purdex-workspace-snapshot-prev'
export function readSnapshot(): WorkspaceSnapshot | null
export function writeSnapshot(snap: WorkspaceSnapshot): void
export function readPrevSnapshot(): WorkspaceSnapshot | null
export function writePrevSnapshot(snap: WorkspaceSnapshot): void
```
`read*` 走 `browserStorage.getItem` + `JSON.parse`，parse 失敗 / `version !== 1` → 回 `null`（不拋）。`write*` 走 `browserStorage.setItem(key, JSON.stringify(snap))`（享 `syncManager.notify` 廣播，無 listener 亦安全）。

**Steps:** 寫 `storage.test.ts` 紅 → 實作 → 綠 → commit。
**Tests（`storage.test.ts`，`beforeEach` `localStorage.clear()`）:**
- `writeSnapshot` 後 `readSnapshot` 得等值物件（round-trip）。
- 空 key → `readSnapshot()` 回 `null`。
- localStorage 存入壞 JSON → `readSnapshot()` 回 `null`、不拋。
- `version` 非 1 → `readSnapshot()` 回 `null`。
- `writePrevSnapshot` / `readPrevSnapshot` 各自獨立 key，互不干擾。

### T2 — capture（`spa/src/lib/snapshot/capture.ts`）

**Files:** Create `capture.ts`；Test `capture.test.ts`。
**Consumes:** T1 型別 + `writeSnapshot`；`useTabStore`/`useWorkspaceStore` getState；`listSessions`；`scanPaneTree`。
**Produces（拆兩個 API，codex 複審 B1）:**
```ts
export async function buildSnapshot(now: number): Promise<WorkspaceSnapshot>  // 純建物件，不寫任何 storage
export async function captureSnapshot(now: number): Promise<CaptureResult>    // = buildSnapshot + writeSnapshot 正本 + 回統計
```

**`buildSnapshot(now)` 邏輯：**①`useTabStore.getState()` 取 `tabs/tabOrder/activeTabId`、`useWorkspaceStore.getState()` 取 `workspaces/activeWorkspaceId`。②對每個 tab.layout `scanPaneTree` 收集 `content.kind==='tmux-session'` 的 `{hostId, sessionCode, mode, cachedName}`，依 hostId 分組。③每個 host **呼叫一次** `listSessions(hostId)`：成功 → 對每個 code 在清單找 `s.code===sessionCode`，命中填 `{name:s.name, cwd:s.cwd, currentCommand:s.current_command, restorable:true}`；未命中（拍當下已死）→ `{name:cachedName, cwd:undefined, restorable:false, captureError:'session-dead-at-capture'}`。`listSessions` throw（host 不可達）→ 該 host 全部 code → `{name:cachedName, cwd:undefined, restorable:false, captureError:'host-unreachable'}`。④回 `WorkspaceSnapshot { version:1, capturedAt: now, … , sessionMeta }`（**不寫 storage**）。

**`captureSnapshot(now)` 邏輯：**`const snap = await buildSnapshot(now)` → `writeSnapshot(snap)`（寫正本）→ 回 `{ total, resolved(restorable=true 數), unresolved(restorable=false 數) }`（由 `snap.sessionMeta` 統計）。

> **B1 rationale**：`captureSnapshot` 會覆寫正本 snapshot；restore 前若要拍「當前態」寫 `-prev` 後悔藥（T7），**必須用 `buildSnapshot` 不碰正本**，否則會在還原前把正本覆寫成當前態、毀掉還原來源。
> `capturedAt` 由入參 `now` 帶入（決定性測試）；不在函式內 `Date.now()`。

**Tests（mock `host-api`：`vi.mock('../host-api', …{ …actual, listSessions: vi.fn() })`；`beforeEach` `setState` 植入兩 store + `localStorage.clear()`）:**
- 單 host 兩 tmux pane 都活著 → `sessionMeta[host][code]` 依複合鍵巢狀正確、`restorable=true`、`resolved=2`；`readSnapshot()` 存到、`capturedAt===now`。
- **跨 host 同 code**：host A、host B 各有相同 `sessionCode` 值 → `sessionMeta['A'][code]` 與 `sessionMeta['B'][code]` 各自獨立、不互蓋。
- host B `listSessions` reject → B 的 pane `restorable=false`+`captureError:'host-unreachable'`、A 照常；`unresolved` 計數正確、**不中斷**。
- pane code 不在 live 清單 → `captureError:'session-dead-at-capture'`、`cwd===undefined`。
- 無 tmux pane（純 editor/browser tab） → `total=0`、`sessionMeta` 為 `{}`、仍寫出快照。
- **`buildSnapshot` 不寫 storage（B1）**：先 `writeSnapshot(舊快照)` → `buildSnapshot(now)` 回新物件但 `readSnapshot()` 仍為**舊快照**（未被覆寫）；改呼叫 `captureSnapshot(now)` 後 `readSnapshot()` 才變新物件。

**Phase 1 done-criteria:** `npx vitest run`（新測試綠）；`pnpm run lint` + `pnpm run build` 綠。PR → codex 兩輪 review。

---

## Phase 2 — restore 引擎 + 三動作（PR 2）

`spa/src/lib/snapshot/restore.ts`。純邏輯 TDD。

### T3 — `ensureSessions`（restore.ts）

**Consumes:** `SessionMeta/Remap/EnsureReport`；`listSessions`/`createSession`。
**Produces:**
```ts
export async function ensureSessions(
  sessionMeta: Record<string, Record<string, SessionMeta>>,
  opts?: { rebuild?: boolean },        // rebuild default true
): Promise<{ remap: Remap; report: EnsureReport }>
```
每 host **一次** `listSessions`（成功=host 可達）。對 `sessionMeta[host][oldCode]`：live 清單含 oldCode → `reattached`（newCode=oldCode, session=該 live）；已死 且 `opts.rebuild!==false` 且 `restorable` → `createSession(host, name, cwd!, mode)` → `rebuilt`（newCode=回傳 `session.code`, session=回傳物件）；否則（host 離線 / `restorable===false` / `rebuild===false` / `createSession` throw）→ `failed`。逐 session 失敗隔離（單筆 throw 只記 `failed`，不中斷其他）。

**Tests（mock host-api）:**
- 全活 → 全 `reattached`、`createSession` 未被呼叫、`report.reattached` 正確。
- **重建範圍（a 點）**：3 筆 restorable 已死（含當前 tab 未引用的 orphan）+ 1 筆活著 → `createSession` 呼叫 **3** 次（= restorable 且已死數，活著走 reattached 不計入），**不因未引用而略過**。
- `restorable===false` 的已死筆 → `failed`、`createSession` **未**對它呼叫（不 `createSession('', …)`）。
- `rebuild:false` → 所有已死一律 `failed`、`createSession` 完全未呼叫；活著仍 `reattached`。
- `createSession` 對某筆 reject → 該筆 `failed`、其餘照常。
- **host 離線**（`listSessions` throw）→ 該 host 所有 entry `failed`、`createSession` 未對該 host 呼叫（spec §3.3「session 死但 host 活」vs「host 離線」以 `listSessions` 成敗區分）。
- **跨 host 同 code**：A、B 同 code，A 活 B 死 → `remap['A'][code].status==='reattached'`、`remap['B'][code].status==='rebuilt'`，不互污。
- **撞同名 session（§8.1，原 T8 併入，codex plan-review Important）**：`createSession` mock 回傳 `name`/`code` 與請求不同（模擬 daemon 自動改名）→ entry `status==='rebuilt'`、`newCode`=回傳 `session.code`、`session.name`=回傳實際 name（前端一律以 `createSession` **回傳物件**為準，非請求值）。**手動探明**：Phase 2 期間對 daemon 手打一次同名 `POST /api/sessions`，記錄實際行為（拒絕/改名/復用）回填 spec §8.1。

### T4 — `remapLayoutSessions`（restore.ts）

**Consumes:** `Remap`；`scanPaneTree`/`updatePaneInLayout`。
**Produces:** `export function remapLayoutSessions(layout: PaneLayout, remap: Remap, opts?: { onlyTerminated?: boolean }): PaneLayout`（純函式）

對每個 `tmux-session` pane 以 `(pane.hostId, pane.sessionCode)` 查 remap：`reattached`/`rebuilt` → 設 `sessionCode=newCode`、`cachedName=session.name`、刪 `terminated`；`failed` → 保留 code、設 `terminated:'tmux-restarted'`（**reason 定案，codex plan-review Minor**：restore 情境一律用 `'tmux-restarted'`，語意最貼近「session 因 tmux/host 重啟而失效需重建」；不臆測 `session-closed`/`host-removed`）；查無 → 原樣。`opts.onlyTerminated===true` → 只處理原本帶 `terminated` 的 pane，活 pane 完全不碰。

**Tests:**
- `rebuilt` entry → pane 的 `sessionCode` 換成 newCode、`cachedName` 更新、`terminated` 清除。
- `failed` → pane 標 `terminated`，**斷言 `terminated === 'tmux-restarted'`（釘死具體值，防回退，codex 複審 I1）**。
- **`onlyTerminated:true`**：一活 pane 的 (hostId,code) 恰等於某 remap 舊 code、但該 pane 非 terminated → **不被動到**（正確性防線，spec §3.5）。
- split 樹（巢狀 SplitLayout）多 tmux pane → 全部依複合鍵改寫。

### T5 — `validateSnapshotConsistency`（restore.ts）

**Produces:** `export function validateSnapshotConsistency(snap: WorkspaceSnapshot): { ok: true } | { ok: false; reason: string }`

五條（R2 C，全通過才 `ok`）：①`workspace.tabs` 每 id ∈ `tabs`；②`activeTabId` ∈ `tabs` 或 `null`；③各 `workspace.activeTabId` ∈ 該 `workspace.tabs`；④`activeWorkspaceId` ∈ `workspaces` 的 id 集合，或 `null`；⑤`tabOrder` 每 id ∈ `tabs`。**不驗** `settings.scope.workspaceId`（R3 C 範圍界定）。

**Tests:** 合法快照 → `ok:true`；分別造 5 種壞快照（workspace.tabs 幽靈 id / activeTabId 幽靈 / workspace.activeTabId 不在該 ws / activeWorkspaceId 不在 workspaces / tabOrder 幽靈）各回 `ok:false`；`settings` pane scope 指向不存在 workspace → 仍 `ok:true`（明示不驗）。

### T6 — `replaceTabState` + `replaceTabSnapshot`（restore.ts）

**Consumes:** T5；`useTabStore`/`useWorkspaceStore` setState。
**Produces:**
```ts
export function replaceTabState(tabs, tabOrder, activeTabId): void   // 僅 tab store，供「重建所有 session」
export function replaceTabSnapshot(snap: WorkspaceSnapshot): void    // 取代 tab + workspace（含 rollback）
```
`replaceTabState`：`useTabStore.setState({ tabs, tabOrder, activeTabId, visitHistory: <filtered> })`（`visitHistory` filter 成新 `tabOrder` 子集）。
`replaceTabSnapshot`：①`validateSnapshotConsistency`，不 ok → throw（不動 store）。②存兩 store 舊值（`useTabStore.getState()` / `useWorkspaceStore.getState()` 的相關欄位）。③`useTabStore.setState({ tabs, tabOrder, activeTabId, visitHistory: filtered })` → `useWorkspaceStore.setState({ workspaces, activeWorkspaceId })`。④任一步 throw → 用舊值 `setState` rollback 兩 store、再 rethrow。

**Tests（`setState` merge-mode harness）:**
- `replaceTabSnapshot` 後兩 store = 快照內容；`visitHistory` 不含已消失 tab（restore 後立刻 `closeTabInWorkspace` active tab → 選到的下一個 tab 正確）。
- 壞快照（validate 不過）→ throw、兩 store **完全未變**。
- mock 第二個 `setState`（workspace）throw → 兩 store 皆 rollback 回舊值。
- **mock 第一個 `setState`（tab）throw → workspace 完全未被呼叫、tab store rollback 回舊值、不留半套（codex 複審 I2，釘死「任一步 throw 全回滾」的第一分支）**。
- `replaceTabState` 只動 tab store、不碰 workspace。

### T7 — 三動作 orchestration + `-prev` + rebuiltButUnattached（restore.ts）

**Consumes:** T3–T6；`readSnapshot`/`writeSnapshot`/`readPrevSnapshot`/`writePrevSnapshot`/`captureSnapshot`；`useSessionStore.replaceHost`。
**Produces:**
```ts
export async function rebuildAllSessions(snap: WorkspaceSnapshot): Promise<RestoreReport>
export async function restoreTabLayout(snap: WorkspaceSnapshot): Promise<RestoreReport>
export async function restoreAll(snap: WorkspaceSnapshot): Promise<RestoreReport>
export async function undoLastRestore(): Promise<RestoreReport | null>   // 讀 -prev 走 restoreAll
function syncSessionStore(remap: Remap): void   // 依 remap 的 session 物件 replaceHost 各 host
```
- **rebuildAllSessions**（a 點）：`ensureSessions(snap.sessionMeta)`（**全部** restorable 已死者重建，含 orphan）→ `remapLayoutSessions(當前每個 tab.layout, remap, { onlyTerminated:true })` → `replaceTabState(當前 tabs 改寫後, 當前 tabOrder, 當前 activeTabId)` → `syncSessionStore`。**不寫 `-prev`**（非破壞性）。
- **restoreTabLayout**：`ensureSessions(snap.sessionMeta, { rebuild:false })`（只對帳活著、死的 failed→terminated）→ `remapLayoutSessions(snap 每個 tab.layout, remap, {})` → 先 `writePrevSnapshot(await captureSnapshot(now))` → `replaceTabSnapshot(改寫後 snap)` → `syncSessionStore`。
- **restoreAll**：`ensureSessions(snap.sessionMeta)`（全重建）→ `remapLayoutSessions(snap tabs, remap, {})` → `writePrevSnapshot(capture)` → `replaceTabSnapshot` → `syncSessionStore`。
- **rebuiltButUnattached（R3 B）**：`replaceTabSnapshot` throw 時，把本次 `remap` 中 `status==='rebuilt'` 的 `{hostId,name,cwd}` 收進 `RestoreReport.rebuiltButUnattached`、rethrow 給 UI 揭露（**不刪 daemon session**）。

> `now` 由 caller（UI）帶入 orchestration（決定性）：三動作簽章實測時可再收一個 `now` 參數或注入 `captureSnapshot`；plan 實作時以 deps 注入 `capture`/`now` 便於測試。

**Tests:**
- **rebuildAllSessions 範圍（a 點）**：snapshot 有 5 個 restorable 已死 session（其中 2 個當前 tab 未引用）+ 當前 tabs 有對應 terminated pane → `createSession` 呼叫 5 次（含 2 orphan）；當前 tab 結構未變（只 terminated pane 換 code）；**未寫 `-prev`（斷言 `readPrevSnapshot()===null`）**。
- **restoreTabLayout**：死的 pane 標 terminated（未 createSession）、活的接回；`-prev` 於執行前寫入；tab/workspace = snapshot。
- **restoreAll**：全重建 + 取代 = snapshot；`-prev` 寫入。
- **undoLastRestore**：先 restoreAll(A) 產生 `-prev`=舊態 → `undoLastRestore()` 還回舊態；無 `-prev` → 回 `null`。
- **rebuiltButUnattached（R3 B）**：mock `replaceTabSnapshot` throw（validate 不過）→ `restoreAll` reject、`RestoreReport.rebuiltButUnattached` 含所有 rebuilt 的 `{hostId,name,cwd}`；前端 store 未變（rollback）。
- **syncSessionStore**：`rebuilt` 後 `useSessionStore.sessions[host]` 含新 session、pane `cachedName`=daemon 回傳 name。
- **syncSessionStore 聚合不互蓋（codex plan-review Important）**：單一 host 有 2 個 session（1 reattached + 1 rebuilt）→ `replaceHost` 對該 host **只呼叫一次**、`sessions[host]` 含**兩個** session（驗逐筆 replaceHost 互蓋的陷阱已避開）。

> **§8.1 撞名探明併入 T3**（codex plan-review Important）：`ensureSessions` 的「以回傳物件為準」語意在 T3 完成時即被測試釘住，不另立晚於 T3 的 task。

**Phase 2 done-criteria:** `npx vitest run` 綠；`pnpm run lint` + `pnpm run build` 綠。§8.1 探明結果回填 spec。PR → codex 兩輪 review（攻擊方重點：rollback / rebuiltButUnattached / 複合鍵不互污）。

---

## Phase 3 — Settings「Snapshot」section UI（PR 3）

### T8 — `SnapshotSettingsSection` 骨架 + 健康度對帳

**Files:** Create `spa/src/components/settings/SnapshotSettingsSection.tsx`；Test `SnapshotSettingsSection.test.tsx`。
**Consumes:** `readSnapshot`；`listSessions`（掛載時對各 host 即時對帳）。範本 `SyncSection.tsx`（status tone toast + `SettingItem` + Phosphor icon 按鈕 + `busy` 並發保護 + `const t = useI18nStore(s=>s.t)`）。

結構：頂部列（拍快照鈕顯 `capturedAt` 相對時間、全部還原、復原上次還原）+ 區塊 1 Tmux（對帳表：`host/name/cwd/current_command/健康度`）+ 區塊 2 Tabs（樹狀 workspace→tab→pane）。**健康度四態**（掛載時各 host `listSessions` 對帳）：🟢 活著（live 清單含 code）/ 🔴 已死可重建（`restorable` 且 host 可達、live 無 code）/ ⚠️ 只能保留結構（`restorable===false`）/ ⚪ host 離線（`listSessions` throw）。

**Tests（RTL，mock `snapshot/storage` + `host-api`）:**
- 有快照 + host live 缺某 code → 該列渲染 🔴；live 含 → 🟢；`restorable:false` → ⚠️；host reject → 該 host 全列 ⚪。
- 區塊 2 渲染 workspace→tab→pane（terminal 顯 name、editor 顯 filePath、browser 顯 url）。
- 無快照 → empty state（只顯「拍下快照」鈕）。

### T9 — 三動作按鈕 wiring + toast

**Files:** Modify `SnapshotSettingsSection.tsx`；Test 續 `SnapshotSettingsSection.test.tsx`。
**Consumes:** T7 orchestration（`captureSnapshot`/`rebuildAllSessions`/`restoreTabLayout`/`restoreAll`/`undoLastRestore`）。

「拍下快照」→ `captureSnapshot(Date.now())` → toast「已拍快照：N 個終端機、其中 M 個無法記錄路徑」。「重建所有 session」（Tmux 區鈕）→ `rebuildAllSessions`。「還原 tab 佈局」（Tabs 區鈕）→ `restoreTabLayout`。「全部還原」→ `restoreAll`。「復原上次還原」→ `undoLastRestore`。`busy` 並發保護（`if(busy)return`→`setBusy(true)`→`finally setBusy(false)`）。toast 依 `RestoreReport` 彙總「X 接回 / Y 重建 / Z 無法重建」；**`rebuiltButUnattached` 非空 → warn tone toast 列 name/cwd/host（R3 B）+ `console.warn`**。無快照 → 還原鈕 disabled；無 `-prev` → 復原鈕 disabled。

**Tests:**
- 點各鈕呼叫對應 orchestration（mock restore 層）、toast 文案正確。
- `rebuiltButUnattached` 非空 → warn toast 含 name/cwd/host。
- `busy` 期間重複點擊只觸發一次。
- 無 `-prev` → 復原鈕 `disabled`。

### T10 — 註冊 section + i18n

**Files:** Modify `spa/src/lib/register-modules/index.tsx`（`registerSettingsSection({ id:'snapshot', label:'settings.section.snapshot', order: SETTINGS_ORDER.SNAPSHOT, component: SnapshotSettingsSection })`）+ `SETTINGS_ORDER` 加 `SNAPSHOT`（置既有之後）；i18n 檔加 `settings.section.snapshot` + 動作/健康度/toast 文案 key（en + zh-TW）。

**Tests:** `getSettingsSections()` 含 `id:'snapshot'`；i18n key en/zh-TW 皆存在（對齊既有 i18n 測試慣例）。

**Phase 3 done-criteria:** `npx vitest run` 綠；`pnpm run lint` + `pnpm run build` 綠。PR → codex 兩輪 review。

---

## Risks / notes

- **§8.1 撞同名 session**（唯一碰 daemon 點）：Phase 2 T3 探明（已併入），緩解已內建（以 `createSession` 回傳實際 code+name 為準）。
- **§8.3 大量 session 重建**：`ensureSessions` 對已死逐一 `createSession`；量大時的並行/上限 —— Phase 2 T3 實作時傾向「有上限並行（如 `p-limit` 語意，手寫 chunk）+ 逐一失敗隔離」，測試以序列 mock 驗語意即可，並行度為效能優化不改語意。
- **best-effort 取代跨視窗空窗**：`replaceTabSnapshot` 兩次 `setState` 各自 `syncManager.notify` 廣播，其他視窗短暫見中間態 —— spec §3.5 已明訂 best-effort、非承諾消除；不引入 batched key（超本次範圍）。
- **daemon 副作用非交易性（R3 B）**：restore 失敗 rollback 只還原前端 store；已建 session 用 `rebuiltButUnattached` 揭露、不自動刪（與「重建所有 session」刻意 orphan 一致，不造成資料遺失）。
- **`settings.scope.workspaceId`（R3 C）**：`validateSnapshotConsistency` 不驗；若指向已刪 workspace，既有 UI 顯示 `Workspace not found`（非致命）。
- **`Date.now()` 注入**：`capture`/orchestration 的 `now` 由 UI 帶入以利決定性測試；只有 T9 UI 層實際呼叫 `Date.now()`。

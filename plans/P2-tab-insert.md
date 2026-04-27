# P2 — Tab 插入改 append-current

> 對應 SPEC.md `# P2` 段。本檔吸收 PLAN 第二輪 4 份 codex review 與 P2 相關的修訂。

## v4 修訂指引（實作前必看，覆寫原 task 對應段）

| Task | 修訂 | 來源 |
|---|---|---|
| **2.1** | `find-browser-insert-target.ts` 改名 `find-insert-target.ts` 並**搬到 `spa/src/lib/tab-insert/find-insert-target.ts` 子目錄** | 體質 review #15 |
| **2.3** | **failing test 必須 seed `[s1, editor1, s2]` 三 tab**（用 `createTab` helper），設 `activeTabId='s1'`，assert 結果順序 `[s1, editor1, newEditor, s2]`；不是「assert tabOrder.length === 1」這種骨架 | 通用 review B1 |
| **2.3** | **`openSingletonTab` 內不要呼叫 `wsState.insertTab(...)`** — caller (terminal-link / register-modules / FileTreeView) 已自行 insert workspace；store 層只處理 tabOrder/active tab；workspace insertion 仍由 caller 決定 | 通用 review A3 |
| **2.3** | useWorkspaceStore 實際路徑：`features/workspace/store`，**不是 `features/workspace/store`** — 所有 import 改 `'../features/workspace/store'` 或對應相對 | 通用 review A2 |
| **2.4 / 2.5** | terminal-link / FileTreeView caller 走 `findInsertTarget` predicate `(c) => FILE_KINDS.includes(c.kind)` 自行算 `afterTabId` 後再 dispatch insert | A3 副作用 |
| **All** | commit message lowercase（`feat(spa): file tree opens files clustered with file-kind tabs`，不寫 CamelCase） | 通用 review C2 |
| **2.6** | Spec 引用改 `SPEC.md (rev 4, P2)` + verification gate 跑全 SPA + Go 測試 | 通用 review C1 |

---

PR 結束標準：所有 file 類分頁 + browser 都遵循同類聚集規則；既有 caller 不傳 `opts` 行為與重構前一致。

## Task 2.1 — 泛用化 `findInsertTarget`

**Files:**
- Rename: `spa/src/lib/find-browser-insert-target.ts` → `spa/src/lib/tab-insert/find-insert-target.ts`
- Rename: `spa/src/lib/find-browser-insert-target.test.ts` → `spa/src/lib/tab-insert/find-insert-target.test.ts`

- [ ] **Step 1: Rename file via git**

```bash
mkdir -p spa/src/lib/tab-insert
git mv spa/src/lib/find-browser-insert-target.ts spa/src/lib/tab-insert/find-insert-target.ts
git mv spa/src/lib/find-browser-insert-target.test.ts spa/src/lib/tab-insert/find-insert-target.test.ts
```

- [ ] **Step 2: Write failing test for new generic signature**

替換 `spa/src/lib/tab-insert/find-insert-target.test.ts` 內容：

```ts
import { describe, it, expect } from 'vitest'
import { findInsertTarget } from './tab-insert/find-insert-target'
import type { Tab, PaneContent } from '../types/tab'

const mkTab = (id: string, kind: PaneContent['kind']): Tab => ({
  id,
  layout: { id: 'p', content: { kind } as PaneContent } as never,
  locked: false,
})

describe('findInsertTarget', () => {
  const isBrowser = (c: PaneContent) => c.kind === 'browser'

  it('returns the nearest matching tab to the right of active', () => {
    const tabs: Record<string, Tab> = {
      'a': mkTab('a', 'tmux-session'),
      'b': mkTab('b', 'browser'),
      'c': mkTab('c', 'browser'),
    }
    expect(findInsertTarget(['a', 'b', 'c'], 'a', tabs, isBrowser)).toBe('b')
  })

  it('falls back to activeTabId when no matching tab found right of active', () => {
    const tabs: Record<string, Tab> = {
      'a': mkTab('a', 'browser'),
      'b': mkTab('b', 'tmux-session'),
    }
    expect(findInsertTarget(['a', 'b'], 'b', tabs, isBrowser)).toBe('b')
  })

  it('returns activeTabId when active not in order', () => {
    const tabs: Record<string, Tab> = { 'a': mkTab('a', 'tmux-session') }
    expect(findInsertTarget(['a'], 'missing', tabs, isBrowser)).toBe('missing')
  })

  it('predicate determines kind family — file kinds aggregate together', () => {
    const isFile = (c: PaneContent) =>
      c.kind === 'editor' || c.kind === 'image-preview' || c.kind === 'pdf-preview'
    const tabs: Record<string, Tab> = {
      'a': mkTab('a', 'tmux-session'),
      'b': mkTab('b', 'editor'),
      'c': mkTab('c', 'tmux-session'),
      'd': mkTab('d', 'image-preview'),
    }
    expect(findInsertTarget(['a', 'b', 'c', 'd'], 'a', tabs, isFile)).toBe('b')
    expect(findInsertTarget(['a', 'b', 'c', 'd'], 'c', tabs, isFile)).toBe('d')
  })
})
```

- [ ] **Step 3: Run test, expect FAIL**

`findInsertTarget` 尚未存在（檔案內仍是 `findBrowserInsertTarget`）。

- [ ] **Step 4: Rewrite the function**

替換 `spa/src/lib/tab-insert/find-insert-target.ts` 內容：

```ts
import type { Tab, PaneContent } from '../types/tab'
import { getPrimaryPane } from './pane-tree'

/**
 * Find the insertion target for a new tab, aggregating same-kind tabs.
 * Scans right from activeTabId for the nearest tab whose primary pane
 * content matches `isSameKind`. Returns that tab's ID (insert after it).
 * Falls back to activeTabId if no match found or if activeTabId is not
 * in `orderedTabIds`.
 */
export function findInsertTarget(
  orderedTabIds: string[],
  activeTabId: string,
  tabs: Record<string, Tab>,
  isSameKind: (content: PaneContent) => boolean,
): string {
  const activeIdx = orderedTabIds.indexOf(activeTabId)
  if (activeIdx === -1) return activeTabId

  for (let i = activeIdx + 1; i < orderedTabIds.length; i++) {
    const tab = tabs[orderedTabIds[i]]
    if (tab && isSameKind(getPrimaryPane(tab.layout).content)) {
      return orderedTabIds[i]
    }
  }
  return activeTabId
}
```

- [ ] **Step 5: Run test, expect PASS**

```
cd spa && npx vitest run src/lib/find-insert-target.test.ts
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/tab-insert/find-insert-target.ts spa/src/lib/tab-insert/find-insert-target.test.ts
git commit -m "refactor(spa): generalize findInsertTarget with predicate"
```

---

## Task 2.2 — `openBrowserTab` 改用泛用版

**Files:**
- Modify: `spa/src/lib/open-browser-tab.ts`

- [ ] **Step 1: Update import + call site**

替換檔案內容：

```ts
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { createTab } from '../types/tab'
import { findInsertTarget } from './tab-insert/find-insert-target'

export function openBrowserTab(url: string): void {
  const tab = createTab({ kind: 'browser', url })
  const tabState = useTabStore.getState()
  const wsState = useWorkspaceStore.getState()
  const activeTabId = tabState.activeTabId

  const wsId = wsState.activeWorkspaceId
  const ws = wsId ? wsState.workspaces.find((w) => w.id === wsId) : null
  const visibleOrder = ws ? ws.tabs.filter((id) => !!tabState.tabs[id]) : tabState.tabOrder

  const afterTabId = activeTabId
    ? findInsertTarget(visibleOrder, activeTabId, tabState.tabs, (c) => c.kind === 'browser')
    : undefined

  useTabStore.getState().addTab(tab, afterTabId)
  useTabStore.getState().setActiveTab(tab.id)

  if (wsId) {
    wsState.insertTab(tab.id, wsId, afterTabId)
  }
}
```

- [ ] **Step 2: Run existing browser tab tests, expect PASS**

```
cd spa && npx vitest run src/lib/open-browser-tab.test.ts
```

> 若任何 test 用到 `findBrowserInsertTarget` symbol → import 改名。`grep -rn "findBrowserInsertTarget" spa/src` 並修。

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/open-browser-tab.ts
git commit -m "refactor(spa): openBrowserTab uses generalized findInsertTarget"
```

---

## Task 2.3 — `openSingletonTab` 加 opts

**Files:**
- Modify: `spa/src/stores/useTabStore.ts`
- Test: `spa/src/stores/useTabStore.test.ts`

- [ ] **Step 1: Write failing test**

擴 `spa/src/stores/useTabStore.test.ts`（**完整 fixture，不是 skeleton**；通用 review B1）：

```ts
import { useTabStore, createTab } from './useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'

const mkContent = (kind: 'tmux-session' | 'editor' | 'browser', extra: Record<string, unknown> = {}) =>
  ({ kind, ...extra } as never)

describe('openSingletonTab with opts.isSameKind', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] } as never, false)
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null } as never, false)
  })

  it('inserts new editor tab right after nearest editor tab to the right of active', () => {
    // seed [s1, editor1, s2] in same workspace, active=s1
    const s1 = createTab(mkContent('tmux-session', { sessionCode: 's1' }))
    const editor1 = createTab(mkContent('editor', { source: { type: 'inapp' }, filePath: '/x.ts' }))
    const s2 = createTab(mkContent('tmux-session', { sessionCode: 's2' }))
    useTabStore.setState({
      tabs: { [s1.id]: s1, [editor1.id]: editor1, [s2.id]: s2 },
      tabOrder: [s1.id, editor1.id, s2.id],
      activeTabId: s1.id,
    } as never, false)
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w', name: 'W', tabs: [s1.id, editor1.id, s2.id], moduleConfig: {} }],
      activeWorkspaceId: 'w',
    } as never, false)

    const newId = useTabStore.getState().openSingletonTab(
      mkContent('editor', { source: { type: 'inapp' }, filePath: '/y.ts' }),
      { isSameKind: (c: { kind: string }) => ['editor', 'image-preview', 'pdf-preview'].includes(c.kind) },
    )
    // 結果順序必須是 [s1, editor1, newEditor, s2]
    expect(useTabStore.getState().tabOrder).toEqual([s1.id, editor1.id, newId, s2.id])
  })

  it('without opts, behaves like before (append last)', () => {
    useTabStore.getState().openSingletonTab(mkContent('editor', { source: { type: 'inapp' }, filePath: '/a' }))
    useTabStore.getState().openSingletonTab(mkContent('browser', { url: 'https://x' }))
    expect(useTabStore.getState().tabOrder.length).toBe(2)
  })
})
```

- [ ] **Step 2: Run test, expect FAIL（簽名不匹配 + 順序 mismatch）**

- [ ] **Step 3: Update store signature + impl**

在 `spa/src/stores/useTabStore.ts`：

```ts
import { findInsertTarget } from '../lib/tab-insert/find-insert-target'

interface OpenSingletonOpts {
  isSameKind?: (content: PaneContent) => boolean
}

interface TabState {
  // ... existing fields
  openSingletonTab: (content: PaneContent, opts?: OpenSingletonOpts) => string
}
```

實作改寫（**只動 tabOrder / addTab / setActiveTab**；**不再呼叫 `wsState.insertTab(...)`** — workspace insertion 由 caller 負責，per 通用 review A3）：

```ts
openSingletonTab: (content, opts) => {
  const state = get()
  // Singleton match (existing logic kept)
  for (const id of state.tabOrder) {
    const tab = state.tabs[id]
    if (!tab) continue
    const primary = getPrimaryPane(tab.layout)
    if (contentMatches(primary.content, content)) {
      get().setActiveTab(id)
      return id
    }
  }
  // Not found — create + insert after current within same workspace
  const tab = createTab(content)
  let afterTabId: string | undefined
  if (opts?.isSameKind && state.activeTabId) {
    // 取 active workspace 的 visibleOrder（dedup with state.tabs 確保都是 live tab）
    const wsState = useWorkspaceStore.getState()
    const wsId = wsState.activeWorkspaceId
    const ws = wsId ? wsState.workspaces.find((w) => w.id === wsId) : null
    const visibleOrder = ws ? ws.tabs.filter((tid) => !!state.tabs[tid]) : state.tabOrder
    afterTabId = findInsertTarget(visibleOrder, state.activeTabId, state.tabs, opts.isSameKind)
  }
  get().addTab(tab, afterTabId)
  get().setActiveTab(tab.id)
  // 注意：不在這裡呼叫 useWorkspaceStore.insertTab；caller (terminal-link / register-modules / FileTreeView) 已自行 insert workspace
  return tab.id
},
```

- [ ] **Step 4: 同步調整 caller — 補回 workspace insertion**

```bash
grep -rn "openSingletonTab" spa/src/ | grep -v test
```

對每個 caller 確認：若 caller 期望 store 自動 insert workspace，必須改成 caller 自己呼叫 `useWorkspaceStore.getState().insertTab(tab.id, wsId, afterTabId)`。**目前 terminal-link / register-modules / FileTreeView 已這樣做（per 通用 review A3 註明），無需動**；其他 caller 若有依賴須補。

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/stores/useTabStore.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.test.ts
git commit -m "feat(spa): opensingletontab supports same-kind insertion"
```

---

## Task 2.4 — Terminal-link file-path opener 帶 predicate

**Files:**
- Modify: `spa/src/lib/terminal-link/openers/file-path.ts`

- [ ] **Step 1: Find call site**

```bash
grep -n "openSingletonTab" spa/src/lib/terminal-link/openers/file-path.ts
```

- [ ] **Step 2: Add predicate + update call**

在檔案頂部加：

```ts
const FILE_KINDS = new Set<string>(['editor', 'image-preview', 'pdf-preview'])
const isFileKind = (c: { kind: string }) => FILE_KINDS.has(c.kind)
```

call site `deps.openSingletonTab(content)` → `deps.openSingletonTab(content, { isSameKind: isFileKind as never })`。

> Note: `openSingletonTab` 是透過 `deps` 注入；若 deps signature 仍只接 1 個參數，需擴 `FilePathOpenerDeps` 介面。

擴 `spa/src/lib/terminal-link/openers/file-path.ts` 的 deps 介面：

```ts
export interface FilePathOpenerDeps {
  // ... existing fields
  openSingletonTab(content: PaneContent, opts?: { isSameKind?: (c: PaneContent) => boolean }): string
}
```

- [ ] **Step 3: Update register-modules.tsx wiring**

在 `register-modules.tsx` 的 `registerBuiltinTerminalLinks` 呼叫處：

```tsx
filePathOpener: {
  // ... existing fields
  openSingletonTab: (content, opts) => useTabStore.getState().openSingletonTab(content, opts),
  // ...
},
```

- [ ] **Step 4: Run all link tests, expect PASS**

```
cd spa && npx vitest run src/lib/terminal-link/
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/terminal-link/openers/file-path.ts spa/src/lib/register-modules.tsx
git commit -m "feat(spa): terminal-link opens files clustered with file-kind tabs"
```

---

## Task 2.5 — FileTreeView 點擊帶 predicate

**Files:**
- Modify: `spa/src/components/FileTreeView.tsx`

- [ ] **Step 1: 找呼叫**

```bash
grep -n "openSingletonTab\|getDefaultOpener" spa/src/components/FileTreeView.tsx
```

- [ ] **Step 2: Add predicate + update call**

在點擊 handler 內：

```tsx
const FILE_KINDS = new Set(['editor', 'image-preview', 'pdf-preview'])
// ...
const opener = getDefaultOpener(fileInfo)
if (opener) {
  const content = opener.createContent(source, fileInfo)
  useTabStore.getState().openSingletonTab(content, {
    isSameKind: (c) => FILE_KINDS.has(c.kind),
  })
}
```

- [ ] **Step 3: Run test**

```
cd spa && npx vitest run src/components/FileTreeView.test.tsx
```

預期 PASS（既有 test 不檢查 insertion order；本次擴的 acceptance 在 Task 2.6 整合測試驗）。

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/FileTreeView.tsx
git commit -m "feat(spa): FileTreeView opens files clustered with file-kind tabs"
```

---

## Task 2.6 — Phase 2 verification + PR

- [ ] **Step 1: 全測 + lint + build**

```bash
cd spa && npx vitest run && pnpm run lint && pnpm run build
```

- [ ] **Step 2: 開 PR + 兩輪 codex review**

```bash
gh pr create --title "refactor(spa): tab insertion appends after current" --body "..."
node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" review --background
node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" adversarial-review --background "P2 tab insertion change"
```

---


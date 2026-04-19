# Terminal Link — 3-Mode Matchers + CJK Drift Fix + Pane CWD Opener

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 擴充 terminal link 偵測支援 3 種檔案路徑模式（絕對 / 相對含 `/` / 純檔名）並修正 CJK 寬字元造成的底線漂移；relative/bare 路徑點擊時即時向 tmux pane 查 cwd 拼絕對路徑再開啟。

**Architecture:**
- **Matchers**：把 `file-path.ts` 拆成 3 個獨立 matcher（absoluteFilePathMatcher、relativeSlashFilePathMatcher、bareFilenameFilePathMatcher），各自在 `provide()` 讀 `useUISettingsStore` 判斷是否啟用。所有 matcher 共用 `type: 'file'` + meta `{ path, line?, col? }`，由同一個 file-path opener 分派。
- **CJK 修正**：在 `xterm-provider.ts` 新增 `buildJsOffsetToCol(IBufferLine)` helper，利用 xterm buffer cells 將 matcher 回傳的 JS string offset 轉為 terminal cell column 再給 `ILink.range`；matcher API 不變（維持 JS offset）。
- **Pane CWD**：daemon 新增 `tmux.Executor.PaneCurrentPath(target string) (string, error)` 與 `GET /api/sessions/{code}/cwd` endpoint；SPA 新增 `fetchSessionCwd(hostId, code)` client；file-path opener 發現 path 非絕對時 `await fetchSessionCwd()` 拼絕對路徑再走既有 `buildFileInfo` 流程。

**Tech Stack:**
- Go 1.26 / net/http / 既有 `internal/tmux.Executor` 介面
- React 19 / Zustand 5 / Vitest（SPA）
- xterm.js 6 + @xterm/addon-unicode11（已安裝）

**Constraints:**
- TDD：先 fail 測試再寫實作
- 每 task 獨立 commit（feat/fix/test），訊息以 `scope(area):` 開頭
- pnpm / vitest（不是 npm）
- Matcher 只偵測，opener 負責解析 cwd；matcher API 回傳 JS offset，CJK 轉換發生在 xterm-provider 一個地方
- URL matcher 無需改動（CJK 修正在 provider 層，自動套用到所有 matcher）

---

## File Structure

### Create
- `spa/src/lib/terminal-link/matchers/file-path.test.ts` — 擴充原檔，為 3 matcher 各加測試
- `spa/src/lib/terminal-link/col-map.ts` — `buildJsOffsetToCol(IBufferLine): (offset: number) => number` helper
- `spa/src/lib/terminal-link/col-map.test.ts` — col-map unit tests（含 CJK / emoji）
- `internal/module/session/cwd_handler.go` — `GET /api/sessions/{code}/cwd` handler
- `internal/module/session/cwd_handler_test.go` — handler HTTP tests

### Modify
- `spa/src/stores/useUISettingsStore.ts` — 新增 3 個 link detection boolean 欄位 + setter
- `spa/src/stores/useUISettingsStore.test.ts` — 對應測試
- `spa/src/lib/terminal-link/matchers/file-path.ts` — 拆成 3 export（absolute/relativeSlash/bare），各自讀 settings gate
- `spa/src/lib/terminal-link/register.ts` — 改註冊 3 個 matcher；新增 `fetchPaneCwd` dependency 注入 opener
- `spa/src/lib/terminal-link/openers/file-path.ts` — relative path 時 await cwd 拼接
- `spa/src/lib/terminal-link/openers/file-path.test.ts` — 對應測試
- `spa/src/lib/terminal-link/xterm-provider.ts` — 使用 col-map 轉 offset
- `spa/src/lib/terminal-link/xterm-provider.test.ts` — 加 CJK drift 測試
- `spa/src/lib/terminal-link/index.ts` — export 新 matcher
- `spa/src/lib/register-modules.tsx` — 注入 `fetchPaneCwd` dep
- `spa/src/lib/host-api.ts` — 新增 `fetchSessionCwd(hostId, code)` 或對應位置
- `spa/src/components/settings/TerminalSection.tsx` — 新增 3 個 toggle
- `spa/src/components/settings/TerminalSection.test.tsx` — toggle 測試
- `spa/public/locales/*.json`（或專案 i18n 檔）— 新增 3 個翻譯 key
- `internal/tmux/executor.go` — `Executor` 介面加 `PaneCurrentPath(target string) (string, error)`；`RealExecutor` 實作
- `internal/tmux/fake_executor.go` — `FakeExecutor` 實作 + setter `SetPaneCwd`
- `internal/tmux/executor_test.go` — FakeExecutor 測試（既有模式）
- `internal/module/session/module.go` — 註冊新 route
- `internal/module/session/handler.go` 或 `cwd_handler.go` — 實作

---

## Task 1: Add link detection settings to useUISettingsStore

**Files:**
- Modify: `spa/src/stores/useUISettingsStore.ts`
- Modify: `spa/src/stores/useUISettingsStore.test.ts`

- [ ] **Step 1: Write failing test**

在 `spa/src/stores/useUISettingsStore.test.ts` 加入（文件末尾 describe 區塊內）：

```ts
describe('link detection settings', () => {
  it('defaults absolute=true, relative-slash=false, bare=false', () => {
    // 讀取最新 state；先 reset 避免前一個 test 污染
    useUISettingsStore.setState({
      linkDetectAbsolute: true,
      linkDetectRelativeSlash: false,
      linkDetectBareFilename: false,
    })
    const s = useUISettingsStore.getState()
    expect(s.linkDetectAbsolute).toBe(true)
    expect(s.linkDetectRelativeSlash).toBe(false)
    expect(s.linkDetectBareFilename).toBe(false)
  })

  it('setters update the flags', () => {
    useUISettingsStore.getState().setLinkDetectRelativeSlash(true)
    expect(useUISettingsStore.getState().linkDetectRelativeSlash).toBe(true)
    useUISettingsStore.getState().setLinkDetectBareFilename(true)
    expect(useUISettingsStore.getState().linkDetectBareFilename).toBe(true)
    useUISettingsStore.getState().setLinkDetectAbsolute(false)
    expect(useUISettingsStore.getState().linkDetectAbsolute).toBe(false)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/stores/useUISettingsStore.test.ts`
Expected: FAIL with "linkDetectAbsolute is not defined" 或類似。

- [ ] **Step 3: Add fields + setters to store**

在 `spa/src/stores/useUISettingsStore.ts` 的 `UISettings` interface 加：

```ts
  linkDetectAbsolute: boolean
  setLinkDetectAbsolute: (v: boolean) => void
  linkDetectRelativeSlash: boolean
  setLinkDetectRelativeSlash: (v: boolean) => void
  linkDetectBareFilename: boolean
  setLinkDetectBareFilename: (v: boolean) => void
```

在 `create<UISettings>()(persist((set) => ({ ... })))` 裡加（沿用其他欄位的慣例）：

```ts
      linkDetectAbsolute: true,
      setLinkDetectAbsolute: (v) => set({ linkDetectAbsolute: v }),
      linkDetectRelativeSlash: false,
      setLinkDetectRelativeSlash: (v) => set({ linkDetectRelativeSlash: v }),
      linkDetectBareFilename: false,
      setLinkDetectBareFilename: (v) => set({ linkDetectBareFilename: v }),
```

alpha 階段不需 migration（依 feedback_no_alpha_migration），新欄位 undefined 時 zustand persist 會沿用新 defaults，不寫 version bump。

- [ ] **Step 4: Run test to verify PASS**

Run: `cd spa && npx vitest run src/stores/useUISettingsStore.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add spa/src/stores/useUISettingsStore.ts spa/src/stores/useUISettingsStore.test.ts && \
git commit -m "feat(spa): add link detection mode settings (absolute/relative-slash/bare)"
```

---

## Task 2: Split filePathMatcher into 3 mode-gated matchers

**Files:**
- Modify: `spa/src/lib/terminal-link/matchers/file-path.ts`
- Modify: `spa/src/lib/terminal-link/matchers/file-path.test.ts`

- [ ] **Step 1: Write failing tests**

`spa/src/lib/terminal-link/matchers/file-path.test.ts` 完整替換（保留既有 absolute 測試 + 新增）：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import {
  absoluteFilePathMatcher,
  relativeSlashFilePathMatcher,
  bareFilenameFilePathMatcher,
} from './file-path'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'

function setFlags(opts: { abs?: boolean; rel?: boolean; bare?: boolean }) {
  useUISettingsStore.setState({
    linkDetectAbsolute: opts.abs ?? false,
    linkDetectRelativeSlash: opts.rel ?? false,
    linkDetectBareFilename: opts.bare ?? false,
  })
}

describe('absoluteFilePathMatcher', () => {
  beforeEach(() => setFlags({ abs: true }))

  it('matches /path/to/file.md', () => {
    const r = absoluteFilePathMatcher.provide('see /a/b/c.md here')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('/a/b/c.md')
    expect(r[0].meta).toEqual({ path: '/a/b/c.md' })
  })

  it('captures line/col suffix', () => {
    const r = absoluteFilePathMatcher.provide('at /x/y.ts:12:3!')
    expect(r[0].meta).toEqual({ path: '/x/y.ts', line: 12, col: 3 })
  })

  it('skips dotdir like /home/u/.config', () => {
    expect(absoluteFilePathMatcher.provide('go /home/u/.config')).toHaveLength(0)
  })

  it('skips path inside URL', () => {
    expect(absoluteFilePathMatcher.provide('https://a.com/b.md')).toHaveLength(0)
  })

  it('returns [] when absolute flag off', () => {
    setFlags({ abs: false })
    expect(absoluteFilePathMatcher.provide('/a/b.md')).toHaveLength(0)
  })
})

describe('relativeSlashFilePathMatcher', () => {
  beforeEach(() => setFlags({ rel: true }))

  it('matches src/App.tsx', () => {
    const r = relativeSlashFilePathMatcher.provide('edit src/App.tsx now')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('src/App.tsx')
    expect(r[0].meta).toEqual({ path: 'src/App.tsx' })
  })

  it('matches internal/agent/cc/extract.go:14', () => {
    const r = relativeSlashFilePathMatcher.provide('internal/agent/cc/extract.go:14')
    expect(r[0].meta).toEqual({ path: 'internal/agent/cc/extract.go', line: 14 })
  })

  it('does NOT match absolute path', () => {
    expect(relativeSlashFilePathMatcher.provide('/abs/x.md')).toHaveLength(0)
  })

  it('does NOT match bare filename', () => {
    expect(relativeSlashFilePathMatcher.provide('x.md')).toHaveLength(0)
  })

  it('skips URL-internal segments', () => {
    expect(relativeSlashFilePathMatcher.provide('https://a.com/b/c.md')).toHaveLength(0)
  })

  it('returns [] when flag off', () => {
    setFlags({ rel: false })
    expect(relativeSlashFilePathMatcher.provide('src/App.tsx')).toHaveLength(0)
  })
})

describe('bareFilenameFilePathMatcher', () => {
  beforeEach(() => setFlags({ bare: true }))

  it('matches bare package.json', () => {
    const r = bareFilenameFilePathMatcher.provide('see package.json')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('package.json')
    expect(r[0].meta).toEqual({ path: 'package.json' })
  })

  it('does NOT match segments inside a/b.md', () => {
    const r = bareFilenameFilePathMatcher.provide('a/b.md')
    // no match: b.md is preceded by '/' → lookbehind blocks
    expect(r).toHaveLength(0)
  })

  it('does NOT match absolute /a/b.md', () => {
    expect(bareFilenameFilePathMatcher.provide('/a/b.md')).toHaveLength(0)
  })

  it('captures line/col for bare name', () => {
    const r = bareFilenameFilePathMatcher.provide('see foo.ts:5:2')
    expect(r[0].meta).toEqual({ path: 'foo.ts', line: 5, col: 2 })
  })

  it('returns [] when flag off', () => {
    setFlags({ bare: false })
    expect(bareFilenameFilePathMatcher.provide('foo.md')).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/lib/terminal-link/matchers/file-path.test.ts`
Expected: FAIL (imports not exported)

- [ ] **Step 3: Replace `spa/src/lib/terminal-link/matchers/file-path.ts` with 3 matchers**

```ts
import type { LinkMatcher } from '../types'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'

// 絕對路徑：必須以 `/` 開頭 + 末段 name.ext
const ABS_RE = /(?<![\w/:])(\/(?:[\w.-]+\/)*[\w-]+\.[A-Za-z0-9]+)(?::(\d+)(?::(\d+))?)?/g

// 相對路徑（含至少一個 `/`）：不能以 `/` 開頭，至少一個中間段 + 末段
const REL_RE = /(?<![\w/:])((?:[\w.-]+\/)+[\w-]+\.[A-Za-z0-9]+)(?::(\d+)(?::(\d+))?)?/g

// 純檔名：無 `/`；lookbehind 阻擋 word/`/`/`:`，避免匹配路徑片段或 URL 內段
const BARE_RE = /(?<![\w/:.])([\w-]+\.[A-Za-z0-9]+)(?::(\d+)(?::(\d+))?)?/g

type MatchResult = {
  text: string
  range: { startCol: number; endCol: number }
  meta?: Record<string, unknown>
}

function runRegex(line: string, re: RegExp): MatchResult[] {
  const results: MatchResult[] = []
  for (const m of line.matchAll(re)) {
    const before = line.slice(0, m.index!)
    // 排除 URL 內的路徑：前方若有 http(s):// 且到此位置之間沒有空白，視為仍在 URL 中
    if (/https?:\/\/\S*$/.test(before)) continue
    const path = m[1]
    const lineNum = m[2] ? parseInt(m[2], 10) : undefined
    const colNum = m[3] ? parseInt(m[3], 10) : undefined
    const text = m[0]
    const startCol = m.index!
    const meta: Record<string, unknown> = { path }
    if (lineNum !== undefined) meta.line = lineNum
    if (colNum !== undefined) meta.col = colNum
    results.push({ text, range: { startCol, endCol: startCol + text.length }, meta })
  }
  return results
}

export const absoluteFilePathMatcher: LinkMatcher = {
  id: 'builtin:file-path-absolute',
  type: 'file',
  provide(line) {
    if (!useUISettingsStore.getState().linkDetectAbsolute) return []
    return runRegex(line, ABS_RE)
  },
}

export const relativeSlashFilePathMatcher: LinkMatcher = {
  id: 'builtin:file-path-relative-slash',
  type: 'file',
  provide(line) {
    if (!useUISettingsStore.getState().linkDetectRelativeSlash) return []
    return runRegex(line, REL_RE)
  },
}

export const bareFilenameFilePathMatcher: LinkMatcher = {
  id: 'builtin:file-path-bare',
  type: 'file',
  provide(line) {
    if (!useUISettingsStore.getState().linkDetectBareFilename) return []
    return runRegex(line, BARE_RE)
  },
}
```

**Note:** 舊 `filePathMatcher` export 已被移除；register.ts 會改為 import 3 個新 matcher（Task 3）。

- [ ] **Step 4: Run tests to verify PASS**

Run: `cd spa && npx vitest run src/lib/terminal-link/matchers/file-path.test.ts`
Expected: PASS (全部 case)

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add spa/src/lib/terminal-link/matchers/file-path.ts spa/src/lib/terminal-link/matchers/file-path.test.ts && \
git commit -m "feat(spa): split file-path matcher into 3 mode-gated matchers"
```

---

## Task 3: Update register.ts + index.ts to register 3 matchers

**Files:**
- Modify: `spa/src/lib/terminal-link/register.ts`
- Modify: `spa/src/lib/terminal-link/index.ts`
- Modify: `spa/src/lib/terminal-link/register.test.ts`

- [ ] **Step 1: Write failing test**

加到 `spa/src/lib/terminal-link/register.test.ts`（文件末尾 describe 區塊內）：

```ts
import {
  absoluteFilePathMatcher,
  relativeSlashFilePathMatcher,
  bareFilenameFilePathMatcher,
} from './matchers/file-path'

describe('registerBuiltinTerminalLinks — 3 file-path matchers', () => {
  beforeEach(() => __resetBuiltinTerminalLinks())

  it('registers all 3 file-path matchers', () => {
    registerBuiltinTerminalLinks({
      isElectron: false,
      openBrowserTab: () => {},
      openMiniWindow: () => {},
      getDefaultFileOpener: () => null,
      openSingletonTab: () => 'tab',
      insertTab: () => {},
      getActiveWorkspaceId: () => 'ws',
      fetchPaneCwd: async () => '/cwd',
    })
    const ids = terminalLinkRegistry.getMatchers().map((m) => m.id)
    expect(ids).toContain(absoluteFilePathMatcher.id)
    expect(ids).toContain(relativeSlashFilePathMatcher.id)
    expect(ids).toContain(bareFilenameFilePathMatcher.id)
  })
})
```

（依既有 register.test.ts 的 import；若還沒 import `__resetBuiltinTerminalLinks` / `terminalLinkRegistry` 請 import。）

- [ ] **Step 2: Run test — expect FAIL**

Run: `cd spa && npx vitest run src/lib/terminal-link/register.test.ts`
Expected: FAIL

- [ ] **Step 3: Update register.ts**

```ts
import type { FileInfo } from '../../types/fs'
import type { PaneContent } from '../../types/tab'
import type { FileOpener } from '../file-opener-registry'
import { terminalLinkRegistry } from './registry'
import { urlMatcher } from './matchers/url'
import {
  absoluteFilePathMatcher,
  relativeSlashFilePathMatcher,
  bareFilenameFilePathMatcher,
} from './matchers/file-path'
import { createUrlOpener } from './openers/url'
import { createFilePathOpener } from './openers/file-path'

export interface BuiltinTerminalLinksDeps {
  isElectron: boolean
  openBrowserTab: (url: string) => void
  openMiniWindow: (url: string) => void
  getDefaultFileOpener: (file: FileInfo) => FileOpener | null
  openSingletonTab: (content: PaneContent) => string
  insertTab: (tabId: string, wsId: string) => void
  getActiveWorkspaceId: () => string | null
  // 新增：供 relative/bare path 解析用
  fetchPaneCwd: (hostId: string, sessionCode: string) => Promise<string>
}

let registered = false

export function registerBuiltinTerminalLinks(deps: BuiltinTerminalLinksDeps): void {
  if (registered) return
  registered = true

  terminalLinkRegistry.registerMatcher(urlMatcher)
  terminalLinkRegistry.registerMatcher(absoluteFilePathMatcher)
  terminalLinkRegistry.registerMatcher(relativeSlashFilePathMatcher)
  terminalLinkRegistry.registerMatcher(bareFilenameFilePathMatcher)

  terminalLinkRegistry.registerOpener(createUrlOpener({
    isElectron: deps.isElectron,
    openBrowserTab: deps.openBrowserTab,
    openMiniWindow: deps.openMiniWindow,
  }))
  terminalLinkRegistry.registerOpener(createFilePathOpener({
    getDefaultOpener: deps.getDefaultFileOpener,
    openSingletonTab: deps.openSingletonTab,
    insertTab: deps.insertTab,
    getActiveWorkspaceId: deps.getActiveWorkspaceId,
    fetchPaneCwd: deps.fetchPaneCwd,
  }))
}

export function __resetBuiltinTerminalLinks(): void {
  registered = false
  terminalLinkRegistry.clear()
}
```

- [ ] **Step 4: Update index.ts re-exports**

在 `spa/src/lib/terminal-link/index.ts` 最下方追加：

```ts
export {
  absoluteFilePathMatcher,
  relativeSlashFilePathMatcher,
  bareFilenameFilePathMatcher,
} from './matchers/file-path'
```

- [ ] **Step 5: Run register.test.ts — expect PASS**

Run: `cd spa && npx vitest run src/lib/terminal-link/register.test.ts`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add spa/src/lib/terminal-link/register.ts spa/src/lib/terminal-link/register.test.ts spa/src/lib/terminal-link/index.ts && \
git commit -m "feat(spa): register 3 file-path matchers + accept fetchPaneCwd dep"
```

---

## Task 4: CJK col-map helper

**Files:**
- Create: `spa/src/lib/terminal-link/col-map.ts`
- Create: `spa/src/lib/terminal-link/col-map.test.ts`

- [ ] **Step 1: Write failing tests**

`spa/src/lib/terminal-link/col-map.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import type { IBufferLine, IBufferCell } from '@xterm/xterm'
import { buildJsOffsetToCol } from './col-map'

// 建 fake IBufferLine：cells 由 [chars, width] 陣列描述
function fakeLine(cells: Array<[string, number]>): IBufferLine {
  return {
    length: cells.reduce((n, [, w]) => n + (w === 0 ? 1 : w), 0),
    getCell(x: number): IBufferCell | undefined {
      // 把 [chars, width] 陣列展開成 cell 陣列（寬字後面補 width-0 continuation）
      const flat: Array<{ chars: string; width: number }> = []
      for (const [c, w] of cells) {
        flat.push({ chars: c, width: w })
        for (let i = 1; i < w; i++) flat.push({ chars: '', width: 0 })
      }
      const cell = flat[x]
      if (!cell) return undefined
      return {
        getWidth: () => cell.width,
        getChars: () => cell.chars,
      } as unknown as IBufferCell
    },
  } as unknown as IBufferLine
}

describe('buildJsOffsetToCol', () => {
  it('maps ASCII-only line 1:1', () => {
    const line = fakeLine([['h', 1], ['i', 1], [' ', 1], ['a', 1]])
    const map = buildJsOffsetToCol(line)
    expect(map(0)).toBe(0)
    expect(map(2)).toBe(2)
    expect(map(4)).toBe(4)
  })

  it('wide chars consume 2 cells per JS char', () => {
    // JS string: '完整 a'  → offsets 0,1,2,3 (JS len = 4)
    // Cells:     [完(2)][整(2)][ (1)][a(1)]  → width total = 6
    const line = fakeLine([['完', 2], ['整', 2], [' ', 1], ['a', 1]])
    const map = buildJsOffsetToCol(line)
    expect(map(0)).toBe(0)  // 完 starts at col 0
    expect(map(1)).toBe(2)  // 整 starts at col 2
    expect(map(2)).toBe(4)  // space at col 4
    expect(map(3)).toBe(5)  // 'a' at col 5
    expect(map(4)).toBe(6)  // end of line
  })

  it('mapping for URL after CJK prefix (the bug)', () => {
    // 完整路徑：https://x
    // JS: 完整路徑： = 6 chars, then 'https://x' = 9 chars → total 15
    // Cells: 6 wide (12 cells) + 9 narrow (9 cells) → 21
    const line = fakeLine([
      ['完', 2], ['整', 2], ['路', 2], ['徑', 2], ['：', 2],
      ['h', 1], ['t', 1], ['t', 1], ['p', 1], ['s', 1], [':', 1], ['/', 1], ['/', 1], ['x', 1],
    ])
    const map = buildJsOffsetToCol(line)
    // 'https' 在 JS offset 5，terminal col 應為 10（5 個寬字 × 2）
    expect(map(5)).toBe(10)
    expect(map(14)).toBe(19)  // 'x' end (inclusive) at col 19
    expect(map(15)).toBe(20)  // past-end → cell index 20 = line.length + something — implementation may return line.length
  })

  it('handles emoji surrogate pair (width 2, JS length 2)', () => {
    // 👍 is width 2 in terminal, JS string length 2 (surrogate pair)
    const line = fakeLine([['👍', 2], ['a', 1]])
    const map = buildJsOffsetToCol(line)
    expect(map(0)).toBe(0)
    expect(map(2)).toBe(2)  // after emoji, 'a' at col 2
  })

  it('offset past end returns sentinel (line.length-ish)', () => {
    const line = fakeLine([['a', 1], ['b', 1]])
    const map = buildJsOffsetToCol(line)
    expect(map(99)).toBe(2)  // clamped to end
  })
})
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `cd spa && npx vitest run src/lib/terminal-link/col-map.test.ts`
Expected: FAIL (helper missing)

- [ ] **Step 3: Implement `col-map.ts`**

```ts
import type { IBufferLine } from '@xterm/xterm'

/**
 * 把 translateToString 的 JS 字串 offset 轉成 terminal cell column。
 *
 * 問題背景：matcher 拿到的是 translateToString(true) 結果，其 index 是 JS UTF-16
 * offset；但 xterm ILink.range 要的是 terminal cell index。CJK / emoji 等寬字元
 * 在 terminal 佔 2 cell、在 JS 佔 1–2 char，兩者不一致。
 *
 * 作法：走訪 buffer cell（唯一權威），建立 (jsOffset → col) 的遞增表，
 * 再以 binary search 回答任意 offset。
 */
export function buildJsOffsetToCol(line: IBufferLine): (jsOffset: number) => number {
  // entries[i] = [jsOffset_at_start_of_cell, col_of_cell]，單調遞增
  const entries: Array<[number, number]> = []
  let js = 0
  for (let x = 0; x < line.length; x++) {
    const cell = line.getCell(x)
    // width 0 = 寬字元的第二格（continuation），跳過不記錄
    const w = cell?.getWidth() ?? 1
    if (w === 0) continue
    entries.push([js, x])
    const chars = cell?.getChars() || ' '
    js += chars.length
  }
  // 哨兵：end of line
  entries.push([js, line.length])

  return (jsOffset: number): number => {
    // Binary search：找第一個 entry[i].js >= jsOffset；目標 col = entry[i].col
    // 若找不到（jsOffset 超出），回傳哨兵的 col = line.length
    let lo = 0
    let hi = entries.length - 1
    while (lo < hi) {
      const mid = (lo + hi) >> 1
      if (entries[mid][0] < jsOffset) lo = mid + 1
      else hi = mid
    }
    return entries[lo][1]
  }
}
```

- [ ] **Step 4: Run test — expect PASS**

Run: `cd spa && npx vitest run src/lib/terminal-link/col-map.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add spa/src/lib/terminal-link/col-map.ts spa/src/lib/terminal-link/col-map.test.ts && \
git commit -m "feat(spa): add buildJsOffsetToCol helper for CJK-aware column mapping"
```

---

## Task 5: Wire col-map into xterm-provider

**Files:**
- Modify: `spa/src/lib/terminal-link/xterm-provider.ts`
- Modify: `spa/src/lib/terminal-link/xterm-provider.test.ts`

- [ ] **Step 1: Write failing test**

在 `spa/src/lib/terminal-link/xterm-provider.test.ts` 的 `describe('createXtermLinkProvider', ...)` 內加 case（並擴充既有 `makeTerm` helper 以支援 fake getCell）：

先在檔案頂端換掉 `makeTerm`（支援 optional cells 陣列）：

```ts
import { describe, it, expect, vi } from 'vitest'
import type { Terminal } from '@xterm/xterm'
import { createRegistry } from './registry'
import { createXtermLinkProvider } from './xterm-provider'

function makeTerm(
  lineText: string,
  opts?: { padded?: string; cells?: Array<[string, number]> },
): Terminal {
  const cells = opts?.cells ?? lineText.split('').map((c) => [c, 1] as [string, number])
  const flat: Array<{ chars: string; width: number }> = []
  for (const [c, w] of cells) {
    flat.push({ chars: c, width: w })
    for (let i = 1; i < w; i++) flat.push({ chars: '', width: 0 })
  }
  return {
    buffer: {
      active: {
        getLine: (y: number) => y !== 0 ? undefined : {
          length: flat.length,
          translateToString: (trimRight?: boolean) =>
            trimRight ? lineText : (opts?.padded ?? lineText),
          getCell: (x: number) => {
            const cell = flat[x]
            return cell ? { getWidth: () => cell.width, getChars: () => cell.chars } : undefined
          },
        },
      },
    },
  } as unknown as Terminal
}
```

（既有測試如 `makeTerm('foo', 'foo             ')` 會失效，改為 `makeTerm('foo', { padded: 'foo             ' })`。請同步更新檔內其他測試的 makeTerm 呼叫，改成 options 物件形式）

新增測試 case（放在 describe 結尾）：

```ts
  it('converts JS offset to terminal column for CJK prefix', () => {
    // 完整：https://x
    // JS offsets:   完(0) 整(1) ：(2) h(3) t(4) t(5) p(6) s(7) :(8) /(9) /(10) x(11)
    // Cells (cols): 完(0-1) 整(2-3) ：(4-5) h(6) t(7) t(8) p(9) s(10) :(11) /(12) /(13) x(14)
    const lineText = '完整：https://x'
    const cells: Array<[string, number]> = [
      ['完', 2], ['整', 2], ['：', 2],
      ['h', 1], ['t', 1], ['t', 1], ['p', 1], ['s', 1], [':', 1], ['/', 1], ['/', 1], ['x', 1],
    ]
    const registry = createRegistry()
    registry.registerMatcher({
      id: 'm', type: 'url',
      // matcher 回傳 JS offset（模擬 matchAll 的 m.index）
      provide: () => [{ text: 'https://x', range: { startCol: 3, endCol: 12 } }],
    })
    const provider = createXtermLinkProvider(registry, () => ({}), makeTerm(lineText, { cells }))
    const cb = vi.fn()
    provider.provideLinks(1, cb)
    const link = cb.mock.calls[0][0][0]
    // terminal col: 'h' 起點 col=6 → 1-indexed start x=7；'x' 末 col=14（exclusive endCol=15），1-indexed inclusive end=15
    expect(link.range).toEqual({
      start: { x: 7, y: 1 },
      end:   { x: 15, y: 1 },
    })
  })
```

- [ ] **Step 2: Run — expect FAIL**

Run: `cd spa && npx vitest run src/lib/terminal-link/xterm-provider.test.ts`
Expected: FAIL（目前實作用 JS offset 當 col，得到 start.x=4, end.x=12）

- [ ] **Step 3: Update `xterm-provider.ts`**

```ts
import type { Terminal, ILinkProvider, ILink } from '@xterm/xterm'
import type { TerminalLinkRegistry, LinkContext } from './types'
import { buildJsOffsetToCol } from './col-map'

export function createXtermLinkProvider(
  registry: TerminalLinkRegistry,
  getCtx: () => LinkContext,
  term: Terminal,
): ILinkProvider {
  return {
    provideLinks(bufferLineNumber: number, callback: (links: ILink[] | undefined) => void): void {
      const line = term.buffer.active.getLine(bufferLineNumber - 1)
      const text = line?.translateToString(true) ?? ''
      if (!text || !line) { callback([]); return }

      // CJK / emoji 寬字元修正：matcher 回傳的 startCol/endCol 是 JS 字串 offset，
      // xterm 需要的是 terminal cell column。這裡一次建表給後續所有 matcher 重用。
      const offsetToCol = buildJsOffsetToCol(line)

      const links: ILink[] = []
      for (const matcher of registry.getMatchers()) {
        for (const raw of matcher.provide(text)) {
          const startCol = offsetToCol(raw.range.startCol)
          const endCol = offsetToCol(raw.range.endCol)
          const token = {
            type: matcher.type,
            text: raw.text,
            range: { startCol, endCol },
            meta: raw.meta,
          }
          links.push({
            // xterm IBufferCellPosition 是 1-indexed inclusive；我們的 endCol 是 0-indexed exclusive，兩者數值相同
            range: {
              start: { x: startCol + 1, y: bufferLineNumber },
              end:   { x: endCol,       y: bufferLineNumber },
            },
            text: raw.text,
            activate: (event) => { registry.dispatch(token, getCtx(), event) },
          })
        }
      }
      callback(links)
    },
  }
}
```

- [ ] **Step 4: Run all terminal-link tests — expect PASS**

Run: `cd spa && npx vitest run src/lib/terminal-link/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add spa/src/lib/terminal-link/xterm-provider.ts spa/src/lib/terminal-link/xterm-provider.test.ts && \
git commit -m "fix(spa): convert JS offset to terminal cell column for CJK-aware link range"
```

---

## Task 6: Daemon — PaneCurrentPath executor method

**Files:**
- Modify: `internal/tmux/executor.go`
- Modify: `internal/tmux/fake_executor.go`
- Modify: `internal/tmux/executor_test.go`

- [ ] **Step 1: Write failing test**

在 `internal/tmux/executor_test.go` 加（沿用既有 FakeExecutor 風格）：

```go
func TestFakeExecutor_PaneCurrentPath(t *testing.T) {
	f := NewFakeExecutor()
	f.SetPaneCwd("sess-A", "/home/user/proj")
	got, err := f.PaneCurrentPath("sess-A")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "/home/user/proj" {
		t.Errorf("got %q, want /home/user/proj", got)
	}
}

func TestFakeExecutor_PaneCurrentPath_NotSet(t *testing.T) {
	f := NewFakeExecutor()
	_, err := f.PaneCurrentPath("sess-nope")
	if err == nil {
		t.Error("expected error for unset pane cwd")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && go test ./internal/tmux/...`
Expected: FAIL（undefined: SetPaneCwd / PaneCurrentPath）

- [ ] **Step 3: Extend Executor interface + RealExecutor**

在 `internal/tmux/executor.go` 的 `Executor` interface（行 22-43）加：

```go
	PaneCurrentPath(target string) (string, error)
```

於檔尾加實作：

```go
func (r *RealExecutor) PaneCurrentPath(target string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", target, "#{pane_current_path}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message pane_current_path: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
```

- [ ] **Step 4: Extend FakeExecutor**

在 `internal/tmux/fake_executor.go` 的 `FakeExecutor` struct 加欄位（與既有 map 並列）：

```go
	paneCwds map[string]string  // target → pane_current_path
```

於 `NewFakeExecutor()` 初始化：

```go
		paneCwds: map[string]string{},
```

加 setter 與 getter 方法：

```go
func (f *FakeExecutor) SetPaneCwd(target, cwd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneCwds[target] = cwd
}

func (f *FakeExecutor) PaneCurrentPath(target string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cwd, ok := f.paneCwds[target]
	if !ok {
		return "", fmt.Errorf("fake: no pane cwd set for %q", target)
	}
	return cwd, nil
}
```

（`f.mu` 已存在於 FakeExecutor；如果欄位名不同請對齊既有實作。必要時先 `grep` 檢查 lock 欄位名。）

- [ ] **Step 5: Run tmux tests — expect PASS**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && go test ./internal/tmux/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add internal/tmux/executor.go internal/tmux/fake_executor.go internal/tmux/executor_test.go && \
git commit -m "feat(daemon): add PaneCurrentPath to tmux.Executor"
```

---

## Task 7: Daemon — GET /api/sessions/{code}/cwd endpoint

**Files:**
- Create: `internal/module/session/cwd_handler.go`
- Create: `internal/module/session/cwd_handler_test.go`
- Modify: `internal/module/session/module.go`

- [ ] **Step 1: Write failing test**

`internal/module/session/cwd_handler_test.go`：

```go
package session

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

func newSessionModuleForCwdTest(t *testing.T) (*SessionModule, *tmux.FakeExecutor) {
	t.Helper()
	meta, err := store.NewMetaStore(":memory:")
	if err != nil {
		t.Fatalf("NewMetaStore: %v", err)
	}
	fake := tmux.NewFakeExecutor()
	c := &core.Core{Tmux: fake, Registry: core.NewRegistry(), Events: core.NewEventHub()}
	m := NewSessionModule(meta)
	if err := m.Init(c); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m, fake
}

func TestHandleSessionCwd_ReturnsCwd(t *testing.T) {
	m, fake := newSessionModuleForCwdTest(t)
	// 建立一個 session；handler 會把 code → tmux target 解析，先加一個已知 code
	fake.AddSession("code-X", "/tmp/initial")
	fake.SetPaneCwd("code-X", "/home/user/proj")

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/code-X/cwd")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body struct{ Cwd string `json:"cwd"` }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Cwd != "/home/user/proj" {
		t.Errorf("got %q, want /home/user/proj", body.Cwd)
	}
}

func TestHandleSessionCwd_NotFound(t *testing.T) {
	m, _ := newSessionModuleForCwdTest(t)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/missing/cwd")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	// 無此 session，tmux display-message 會回錯 → 500 或 404（handler 決定）
	if resp.StatusCode == http.StatusOK {
		t.Errorf("expected non-200, got 200")
	}
}
```

（若既有測試有共用 helper 可複用。檢查 `internal/module/session/handler_test.go` 或 `module_test.go` 看是否已有 `newSessionModuleForTest` 類 helper，若有就用它。）

- [ ] **Step 2: Run — expect FAIL (route + handler missing)**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && go test ./internal/module/session/...`
Expected: FAIL

- [ ] **Step 3: Implement handler**

`internal/module/session/cwd_handler.go`：

```go
package session

import (
	"encoding/json"
	"net/http"
)

// handleSessionCwd returns the current working directory of the tmux pane
// attached to the given session code. Used by the SPA terminal-link opener
// to resolve relative file paths at click time.
func (m *SessionModule) handleSessionCwd(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	cwd, err := m.tmux.PaneCurrentPath(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"cwd": cwd})
}
```

- [ ] **Step 4: Register route in module.go**

在 `internal/module/session/module.go` 的 `RegisterRoutes`（行 52-63）最後一個 `POST /api/hooks/tmux/setup` 之前加：

```go
	mux.HandleFunc("GET /api/sessions/{code}/cwd", m.handleSessionCwd)
```

- [ ] **Step 5: Run — expect PASS**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && go test ./internal/module/session/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add internal/module/session/cwd_handler.go internal/module/session/cwd_handler_test.go internal/module/session/module.go && \
git commit -m "feat(daemon): add GET /api/sessions/{code}/cwd endpoint"
```

---

## Task 8: SPA — host-api client binding for pane cwd

**Files:**
- Modify: `spa/src/lib/host-api.ts`（實際路徑以 `grep -n "sessions" spa/src/lib/host-api.ts` 確認；若結構不同，依既有 pattern 新增）

- [ ] **Step 1: Inspect existing pattern**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && grep -n "api/sessions" spa/src/lib/host-api.ts | head -20`

看既有 session GET fetch 長怎樣（如 `fetchSession`、`sendKeys` 等），依同樣的 host+code URL 組法、認證 header、錯誤處理新增 `fetchSessionCwd`。

- [ ] **Step 2: Write failing test**

在對應的 test 檔（如 `spa/src/lib/host-api.test.ts`）加：

```ts
it('fetchSessionCwd returns cwd string from /api/sessions/{code}/cwd', async () => {
  // 依既有 mock pattern — 如果用 msw 就 rest.get，如果手動 fake fetch 就依檔內做法
  const mockFetch = vi.fn(async () =>
    new Response(JSON.stringify({ cwd: '/home/user/x' }), { status: 200 }),
  )
  vi.stubGlobal('fetch', mockFetch)
  const cwd = await fetchSessionCwd('host-1', 'code-X')
  expect(cwd).toBe('/home/user/x')
  expect(mockFetch).toHaveBeenCalledWith(
    expect.stringContaining('/api/sessions/code-X/cwd'),
    expect.anything(),
  )
  vi.unstubAllGlobals()
})
```

（若檔內還沒有 `fetch` stub pattern，改用既有風格。先 grep 確認。）

- [ ] **Step 3: Run — expect FAIL**

Run: `cd spa && npx vitest run src/lib/host-api.test.ts`
Expected: FAIL

- [ ] **Step 4: Implement fetchSessionCwd**

依既有 pattern 在 host-api.ts 加：

```ts
export async function fetchSessionCwd(hostId: string, sessionCode: string): Promise<string> {
  const url = hostApiUrl(hostId, `/api/sessions/${encodeURIComponent(sessionCode)}/cwd`)
  const resp = await fetch(url, { credentials: 'include' })  // 依既有慣例
  if (!resp.ok) throw new Error(`fetchSessionCwd: ${resp.status}`)
  const body = await resp.json()
  return String(body.cwd ?? '')
}
```

（`hostApiUrl` / credentials / headers 實際寫法依 host-api.ts 現有 helper 為準。）

- [ ] **Step 5: Run — expect PASS**

Run: `cd spa && npx vitest run src/lib/host-api.test.ts`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add spa/src/lib/host-api.ts spa/src/lib/host-api.test.ts && \
git commit -m "feat(spa): add fetchSessionCwd host-api client"
```

---

## Task 9: Opener — cwd-resolve relative paths

**Files:**
- Modify: `spa/src/lib/terminal-link/openers/file-path.ts`
- Modify: `spa/src/lib/terminal-link/openers/file-path.test.ts`

- [ ] **Step 1: Write failing tests**

在 `spa/src/lib/terminal-link/openers/file-path.test.ts` 對應 describe 內加 cases（保留既有 absolute path 測試）：

```ts
import { describe, it, expect, vi } from 'vitest'
import { createFilePathOpener } from './file-path'

describe('createFilePathOpener — relative path', () => {
  const baseDeps = () => {
    const getDefaultOpener = vi.fn(() => ({
      createContent: vi.fn((source, file) => ({ kind: 'editor', source, filePath: file.path })),
    }))
    const openSingletonTab = vi.fn(() => 'tab-1')
    const insertTab = vi.fn()
    const getActiveWorkspaceId = vi.fn(() => 'ws-1')
    return { getDefaultOpener, openSingletonTab, insertTab, getActiveWorkspaceId }
  }

  it('relative path: fetches cwd and prepends before open', async () => {
    const deps = baseDeps()
    const fetchPaneCwd = vi.fn(async () => '/home/user/proj')
    const opener = createFilePathOpener({ ...deps, fetchPaneCwd })
    await opener.open(
      { type: 'file', text: 'src/App.tsx', range: { startCol: 0, endCol: 11 }, meta: { path: 'src/App.tsx' } },
      { hostId: 'h', sessionCode: 'c' },
      new MouseEvent('click'),
    )
    expect(fetchPaneCwd).toHaveBeenCalledWith('h', 'c')
    // 驗證傳給 default opener 的 file.path 已是絕對路徑
    const createContentCalls = (deps.getDefaultOpener.mock.results[0].value.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/home/user/proj/src/App.tsx')
  })

  it('absolute path: does NOT fetch cwd', async () => {
    const deps = baseDeps()
    const fetchPaneCwd = vi.fn(async () => '/nope')
    const opener = createFilePathOpener({ ...deps, fetchPaneCwd })
    await opener.open(
      { type: 'file', text: '/abs/x.md', range: { startCol: 0, endCol: 9 }, meta: { path: '/abs/x.md' } },
      { hostId: 'h', sessionCode: 'c' },
      new MouseEvent('click'),
    )
    expect(fetchPaneCwd).not.toHaveBeenCalled()
    const calls = (deps.getDefaultOpener.mock.results[0].value.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(calls[0][1].path).toBe('/abs/x.md')
  })

  it('relative path without sessionCode: no-op (cannot resolve)', async () => {
    const deps = baseDeps()
    const fetchPaneCwd = vi.fn(async () => '/x')
    const opener = createFilePathOpener({ ...deps, fetchPaneCwd })
    await opener.open(
      { type: 'file', text: 'a.md', range: { startCol: 0, endCol: 4 }, meta: { path: 'a.md' } },
      { hostId: 'h' /* no sessionCode */ },
      new MouseEvent('click'),
    )
    expect(fetchPaneCwd).not.toHaveBeenCalled()
    expect(deps.openSingletonTab).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run — expect FAIL**

Run: `cd spa && npx vitest run src/lib/terminal-link/openers/file-path.test.ts`
Expected: FAIL（signature 不合 / fetchPaneCwd 未宣告）

- [ ] **Step 3: Update `openers/file-path.ts`**

```ts
import type { LinkOpener } from '../types'
import type { FileInfo, FileSource } from '../../../types/fs'
import type { PaneContent } from '../../../types/tab'
import type { FileOpener } from '../../file-opener-registry'

export interface FilePathOpenerDeps {
  getDefaultOpener(file: FileInfo): FileOpener | null
  openSingletonTab(content: PaneContent): string
  insertTab(tabId: string, workspaceId: string): void
  getActiveWorkspaceId(): string | null
  fetchPaneCwd(hostId: string, sessionCode: string): Promise<string>
}

function buildFileInfo(path: string): FileInfo {
  const name = path.split('/').pop() ?? path
  const extension = name.includes('.') ? name.split('.').pop()! : ''
  return { name, path, extension, size: 0, isDirectory: false }
}

function joinCwd(cwd: string, rel: string): string {
  const trimmed = cwd.replace(/\/+$/, '')
  return `${trimmed}/${rel}`
}

export function createFilePathOpener(deps: FilePathOpenerDeps): LinkOpener {
  return {
    id: 'builtin:file-path',
    priority: 0,
    canOpen: (token) =>
      token.type === 'file' &&
      typeof (token.meta as { path?: unknown } | undefined)?.path === 'string',
    open: async (token, ctx) => {
      const rawPath = (token.meta as { path?: unknown } | undefined)?.path
      if (typeof rawPath !== 'string') return
      if (!ctx.hostId) return

      let path = rawPath
      if (!path.startsWith('/')) {
        // relative / bare: 即時向 tmux pane 查 cwd。若無 sessionCode 放棄（無法解析）
        if (!ctx.sessionCode) return
        try {
          const cwd = await deps.fetchPaneCwd(ctx.hostId, ctx.sessionCode)
          if (!cwd || !cwd.startsWith('/')) return
          path = joinCwd(cwd, path)
        } catch {
          return
        }
      }

      const file = buildFileInfo(path)
      const opener = deps.getDefaultOpener(file)
      if (!opener) return
      const source: FileSource = { type: 'daemon', hostId: ctx.hostId }
      const content = opener.createContent(source, file)
      const wsId = deps.getActiveWorkspaceId()
      if (!wsId) return
      const tabId = deps.openSingletonTab(content)
      deps.insertTab(tabId, wsId)
    },
  }
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `cd spa && npx vitest run src/lib/terminal-link/openers/file-path.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add spa/src/lib/terminal-link/openers/file-path.ts spa/src/lib/terminal-link/openers/file-path.test.ts && \
git commit -m "feat(spa): resolve relative file-path via pane cwd at click time"
```

---

## Task 10: Wire fetchPaneCwd in register-modules.tsx

**Files:**
- Modify: `spa/src/lib/register-modules.tsx`

- [ ] **Step 1: Modify `registerBuiltinTerminalLinks(...)` call**

在 `spa/src/lib/register-modules.tsx:339-347` 把 call 改成：

```tsx
  registerBuiltinTerminalLinks({
    isElectron: caps.isElectron,
    openBrowserTab,
    openMiniWindow: (url) => window.electronAPI?.browserViewOpenMiniWindow(url),
    getDefaultFileOpener: getDefaultOpener,
    openSingletonTab: (content) => useTabStore.getState().openSingletonTab(content),
    insertTab: (tabId, wsId) => useWorkspaceStore.getState().insertTab(tabId, wsId),
    getActiveWorkspaceId: () => useWorkspaceStore.getState().activeWorkspaceId,
    fetchPaneCwd: (hostId, sessionCode) => fetchSessionCwd(hostId, sessionCode),
  })
```

檔頂 import 追加：

```tsx
import { fetchSessionCwd } from './host-api'
```

（實際 import path 依 host-api.ts 位置為準。）

- [ ] **Step 2: Run SPA tests to catch regressions**

Run: `cd spa && npx vitest run`
Expected: PASS（全部）

- [ ] **Step 3: TypeScript type check**

Run: `cd spa && pnpm run lint`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add spa/src/lib/register-modules.tsx && \
git commit -m "feat(spa): wire fetchSessionCwd into terminal-link opener deps"
```

---

## Task 11: Settings UI — 3 toggles in TerminalSection

**Files:**
- Modify: `spa/src/components/settings/TerminalSection.tsx`
- Modify: `spa/src/components/settings/TerminalSection.test.tsx`
- Modify: i18n JSON 檔（依專案 pattern；執行 Step 1 前先 `ls spa/src/locales/` 或 `grep -rn "settings.terminal" spa/src` 確認位置）

- [ ] **Step 1: Locate i18n files**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && grep -rn "settings.terminal.renderer" spa/src | head -5`

找到 i18n JSON 位置後（通常在 `spa/src/locales/en.json` 或類似），在各語言檔加 keys：

```json
"settings.terminal.link_detect.title": "Terminal link detection",
"settings.terminal.link_detect.absolute": "Absolute paths (/path/to/file.md)",
"settings.terminal.link_detect.relative_slash": "Relative paths with / (src/App.tsx)",
"settings.terminal.link_detect.bare": "Bare filenames (foo.md) — high false-positive"
```

繁中對應：

```json
"settings.terminal.link_detect.title": "終端連結偵測",
"settings.terminal.link_detect.absolute": "絕對路徑（/path/to/file.md）",
"settings.terminal.link_detect.relative_slash": "含 / 的相對路徑（src/App.tsx）",
"settings.terminal.link_detect.bare": "純檔名（foo.md）— 容易誤判"
```

- [ ] **Step 2: Write failing test**

在 `TerminalSection.test.tsx` 加：

```tsx
it('renders 3 link detection toggles and they bind to store', () => {
  const user = userEvent.setup()
  useUISettingsStore.setState({
    linkDetectAbsolute: true,
    linkDetectRelativeSlash: false,
    linkDetectBareFilename: false,
  })
  render(<TerminalSection />)
  const absToggle = screen.getByLabelText(/Absolute paths|絕對路徑/)
  const relToggle = screen.getByLabelText(/Relative paths|含 \/ 的相對路徑/)
  const bareToggle = screen.getByLabelText(/Bare filenames|純檔名/)
  expect(absToggle).toBeChecked()
  expect(relToggle).not.toBeChecked()

  await user.click(relToggle)
  expect(useUISettingsStore.getState().linkDetectRelativeSlash).toBe(true)

  await user.click(bareToggle)
  expect(useUISettingsStore.getState().linkDetectBareFilename).toBe(true)
})
```

（import `userEvent`、`render`、`screen` 沿用檔內既有慣例。）

- [ ] **Step 3: Run — expect FAIL**

Run: `cd spa && npx vitest run src/components/settings/TerminalSection.test.tsx`
Expected: FAIL

- [ ] **Step 4: Add UI block to TerminalSection.tsx**

在 TerminalSection 回傳 JSX 內合適位置（例如 `</div>` 前）加：

```tsx
  const linkDetectAbsolute = useUISettingsStore((s) => s.linkDetectAbsolute)
  const setLinkDetectAbsolute = useUISettingsStore((s) => s.setLinkDetectAbsolute)
  const linkDetectRelativeSlash = useUISettingsStore((s) => s.linkDetectRelativeSlash)
  const setLinkDetectRelativeSlash = useUISettingsStore((s) => s.setLinkDetectRelativeSlash)
  const linkDetectBareFilename = useUISettingsStore((s) => s.linkDetectBareFilename)
  const setLinkDetectBareFilename = useUISettingsStore((s) => s.setLinkDetectBareFilename)
```

在 render block 加（沿用 `<SettingItem label={...}><ToggleSwitch .../></SettingItem>` 既有 pattern）：

```tsx
      <h3>{t('settings.terminal.link_detect.title')}</h3>
      <SettingItem label={t('settings.terminal.link_detect.absolute')}>
        <ToggleSwitch checked={linkDetectAbsolute} onChange={setLinkDetectAbsolute} />
      </SettingItem>
      <SettingItem label={t('settings.terminal.link_detect.relative_slash')}>
        <ToggleSwitch checked={linkDetectRelativeSlash} onChange={setLinkDetectRelativeSlash} />
      </SettingItem>
      <SettingItem label={t('settings.terminal.link_detect.bare')}>
        <ToggleSwitch checked={linkDetectBareFilename} onChange={setLinkDetectBareFilename} />
      </SettingItem>
```

（具體 HTML 結構依 TerminalSection 既有 SettingItem wrapper 用法對齊。）

- [ ] **Step 5: Run — expect PASS**

Run: `cd spa && npx vitest run src/components/settings/TerminalSection.test.tsx`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add spa/src/components/settings/TerminalSection.tsx spa/src/components/settings/TerminalSection.test.tsx spa/src/locales/ && \
git commit -m "feat(spa): add 3 link-detection toggles to Terminal settings"
```

---

## Task 12: Full regression run + manual verification

**Files:** none (verification only)

- [ ] **Step 1: Run full vitest**

Run: `cd spa && npx vitest run`
Expected: 全部 PASS

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`
Expected: 無 error（warning 可允許，但不能有新的）

- [ ] **Step 3: Run Go tests**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && go test ./...`
Expected: 全部 PASS

- [ ] **Step 4: Rebuild daemon**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && go build -o bin/pdx ./cmd/pdx`
Expected: 成功

- [ ] **Step 5: Manual E2E checklist**

在開發環境啟動 daemon + SPA，依序測試（打勾記錄結果）：

- [ ] Settings → Terminal 可看到 3 個 link detection toggle
- [ ] 僅開 absolute：`echo /path/to/file.md` → 有底線、click 能開
- [ ] 開 absolute + relative-slash：terminal `echo src/App.tsx` → 有底線、click 能開（cwd 來自 tmux pane）
- [ ] 開 bare：`echo package.json` → 有底線、click 能開
- [ ] CJK drift：`echo "完整路徑：/path/to/file.md"` → 底線精確覆蓋路徑本身，**無漂移**
- [ ] CJK drift URL 版：`echo "網址：https://example.com"` → 底線覆蓋 URL 本身
- [ ] `echo /home/u/.config` 仍不觸發（dotdir 排除）
- [ ] `echo https://a.com/b.md` URL click 整個 URL；`/b.md` **不**另外觸發
- [ ] relative path click 時若無 sessionCode（例如 Stream mode）不 crash
- [ ] 關閉某 toggle 後，對應類型 terminal link 立即停止 highlight（不需重連）

- [ ] **Step 6: Update VERSION + CHANGELOG**

bump `VERSION`（`1.0.0-alpha.183`）+ `package.json` + `spa/package.json`，在 `CHANGELOG.md` 加條目：

```md
## 1.0.0-alpha.183

- feat(spa): terminal link detection — 3 modes (absolute / relative with / / bare filename) gated by settings
- fix(spa): terminal link underline no longer drifts on lines with CJK / wide chars
- feat(daemon): GET /api/sessions/{code}/cwd — returns tmux `pane_current_path` for link resolution
```

- [ ] **Step 7: Final commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-debug && \
git add VERSION package.json spa/package.json CHANGELOG.md && \
git commit -m "chore: bump version to 1.0.0-alpha.183"
```

- [ ] **Step 8: PR**

依 `feedback_v1_dev_approach` 規則推 branch + 開 PR（不直推 main），走兩輪 review（code-review skill + 3-parallel-agent 攻守）。

---

## Self-Review 紀錄

- **Spec coverage**：3 matcher modes（T2/T3）✅ / CJK drift（T4/T5）✅ / 相對路徑 cwd（T6-T10）✅ / 設定 UI（T11）✅ / 驗證（T12）✅
- **URL matcher CJK**：因修正在 xterm-provider 層（所有 matcher 共用），URL 自動受惠 — Task 12 Step 5 包含 URL CJK 人工驗證項目
- **型別一致**：`fetchPaneCwd(hostId, sessionCode): Promise<string>` 從 register deps → file-path opener 全程同名同簽名；`linkDetect{Absolute,RelativeSlash,BareFilename}` 三欄位命名跨 store / UI / matcher 一致
- **不設計 cwd fallback chain**：按使用者決定「D 方案即時 tmux 查詢」；無 SessionInfo.cwd 備援（簡化範圍；若 tmux 查失敗 opener 直接 no-op 不 crash — Task 9 `try/catch` 覆蓋）
- **Alpha 無 migration**：store 新欄位依 feedback_no_alpha_migration 不寫 version bump

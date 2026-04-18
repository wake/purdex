# Terminal Link Registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立可擴充的 terminal link 架構 — xterm addon 提供 matcher/opener 註冊點，內建 URL 與 file-path 支援，file-path 橋接到既有 `FileOpener` registry，讓 Editor module（含未來的 Markdown preview 等）零修改就能接上。

**Architecture:**
三層：
1. `TerminalLinkRegistry` — matcher 註冊點（regex → token）、opener 註冊點（type → handler），支援 priority 與 dispose
2. `createXtermLinkProvider(registry, ctx)` — 把 registry 包成 xterm `ILinkProvider`，於 `term.registerLinkProvider()` 使用
3. 內建 URL matcher/opener（取代 `WebLinksAddon`）、File-path matcher/opener（橋接 `FileOpener` registry，Editor/Image/PDF 等既有 opener 自動生效）

**Tech Stack:** TypeScript / Vitest / @xterm/xterm 6 / Zustand（沿用現有 stores）。不新增第三方套件。

---

## File Structure

**新增：**
- `spa/src/lib/terminal-link/types.ts` — 公開型別（`LinkToken`、`LinkMatcher`、`LinkOpener`、`LinkContext`、`TerminalLinkRegistry`）
- `spa/src/lib/terminal-link/registry.ts` — registry 單例 + 實作
- `spa/src/lib/terminal-link/registry.test.ts`
- `spa/src/lib/terminal-link/matchers/url.ts` — URL matcher（`https?://…`）
- `spa/src/lib/terminal-link/matchers/url.test.ts`
- `spa/src/lib/terminal-link/matchers/file-path.ts` — 絕對路徑 / `path:line[:col]`
- `spa/src/lib/terminal-link/matchers/file-path.test.ts`
- `spa/src/lib/terminal-link/openers/url.ts` — 重用 `link-handler.ts` 的 `isElectron` 分支邏輯
- `spa/src/lib/terminal-link/openers/url.test.ts`
- `spa/src/lib/terminal-link/openers/file-path.ts` — 橋接 `FileOpener` registry
- `spa/src/lib/terminal-link/openers/file-path.test.ts`
- `spa/src/lib/terminal-link/xterm-provider.ts` — `createXtermLinkProvider(registry, ctx)`
- `spa/src/lib/terminal-link/xterm-provider.test.ts`
- `spa/src/lib/terminal-link/register.ts` — `registerBuiltinTerminalLinks()`（boot 時呼叫）
- `spa/src/lib/terminal-link/register.test.ts`
- `spa/src/lib/terminal-link/index.ts` — 公開 barrel

**修改：**
- `spa/src/hooks/useTerminal.ts` — 移除 `WebLinksAddon`，改用 `term.registerLinkProvider(createXtermLinkProvider(registry, ctx))`；`UseTerminalOptions.linkHandler` 移除，新增 `linkContext: LinkContext`
- `spa/src/components/TerminalView.tsx` — 移除 `createLinkHandler` 呼叫與 `linkHandler` prop；改傳 `linkContext={{ hostId, sessionCode }}`
- `spa/src/components/TerminalView.test.tsx` — 移除 `WebLinksAddon` mock；加上 link provider 相關斷言
- `spa/src/lib/register-modules.tsx` — `bootstrapRegistrations` 末段呼叫 `registerBuiltinTerminalLinks()`

**刪除：**
- `spa/src/lib/link-handler.ts`（邏輯搬進 `openers/url.ts`）
- `spa/src/lib/__tests__/link-handler.test.ts`（如存在）

**註：** 不移除 `@xterm/addon-web-links` 套件依賴（留給未來 URL matcher 如需對照回比），不在本 PR 動 `package.json`。

---

## Task 1: 定義型別與建立 registry 骨架

**Files:**
- Create: `spa/src/lib/terminal-link/types.ts`
- Create: `spa/src/lib/terminal-link/registry.ts`
- Test: `spa/src/lib/terminal-link/registry.test.ts`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/terminal-link/registry.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { createRegistry } from './registry'
import type { LinkMatcher, LinkOpener, LinkToken } from './types'

describe('terminal-link registry', () => {
  let registry: ReturnType<typeof createRegistry>
  beforeEach(() => { registry = createRegistry() })

  it('registers and lists matchers', () => {
    const m: LinkMatcher = { id: 'm1', type: 'url', provide: () => [] }
    const dispose = registry.registerMatcher(m)
    expect(registry.getMatchers()).toEqual([m])
    dispose()
    expect(registry.getMatchers()).toEqual([])
  })

  it('dispatches to first opener whose canOpen returns true', () => {
    const calls: string[] = []
    const o1: LinkOpener = {
      id: 'o1', priority: 0,
      canOpen: (t) => t.type === 'url',
      open: () => { calls.push('o1') },
    }
    const o2: LinkOpener = {
      id: 'o2', priority: 10,
      canOpen: (t) => t.type === 'url',
      open: () => { calls.push('o2') },
    }
    registry.registerOpener(o1)
    registry.registerOpener(o2)
    const token: LinkToken = { type: 'url', text: 'https://x', range: { startCol: 0, endCol: 9 } }
    registry.dispatch(token, {}, new MouseEvent('click'))
    expect(calls).toEqual(['o2'])
  })

  it('dispatch returns false when no opener matches', () => {
    const token: LinkToken = { type: 'unknown', text: 'x', range: { startCol: 0, endCol: 1 } }
    expect(registry.dispatch(token, {}, new MouseEvent('click'))).toBe(false)
  })

  it('clear() empties matchers and openers', () => {
    registry.registerMatcher({ id: 'm', type: 't', provide: () => [] })
    registry.registerOpener({ id: 'o', canOpen: () => true, open: () => {} })
    registry.clear()
    expect(registry.getMatchers()).toEqual([])
    const token: LinkToken = { type: 't', text: 'x', range: { startCol: 0, endCol: 1 } }
    expect(registry.dispatch(token, {}, new MouseEvent('click'))).toBe(false)
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/lib/terminal-link/registry.test.ts`
Expected: FAIL，`Cannot find module './registry'`

- [ ] **Step 3: 實作 types.ts**

```ts
// spa/src/lib/terminal-link/types.ts
export interface LinkRange {
  startCol: number  // 0-indexed inclusive
  endCol: number    // 0-indexed exclusive
}

export interface LinkToken {
  type: string
  text: string
  range: LinkRange
  meta?: Record<string, unknown>
}

export interface LinkContext {
  hostId?: string
  sessionCode?: string
}

export interface LinkMatcher {
  id: string
  type: string
  provide(line: string): Array<{
    text: string
    range: LinkRange
    meta?: Record<string, unknown>
  }>
}

export interface LinkOpener {
  id: string
  priority?: number  // higher runs first; default 0
  canOpen(token: LinkToken): boolean
  open(token: LinkToken, ctx: LinkContext, event: MouseEvent): void | Promise<void>
}

export interface TerminalLinkRegistry {
  registerMatcher(m: LinkMatcher): () => void
  registerOpener(o: LinkOpener): () => void
  getMatchers(): readonly LinkMatcher[]
  dispatch(token: LinkToken, ctx: LinkContext, event: MouseEvent): boolean
  clear(): void
}
```

- [ ] **Step 4: 實作 registry.ts**

```ts
// spa/src/lib/terminal-link/registry.ts
import type { LinkMatcher, LinkOpener, LinkToken, LinkContext, TerminalLinkRegistry } from './types'

export function createRegistry(): TerminalLinkRegistry {
  const matchers: LinkMatcher[] = []
  const openers: LinkOpener[] = []

  return {
    registerMatcher(m) {
      matchers.push(m)
      return () => {
        const i = matchers.indexOf(m)
        if (i >= 0) matchers.splice(i, 1)
      }
    },
    registerOpener(o) {
      openers.push(o)
      return () => {
        const i = openers.indexOf(o)
        if (i >= 0) openers.splice(i, 1)
      }
    },
    getMatchers() {
      return matchers
    },
    dispatch(token, ctx, event) {
      const sorted = [...openers].sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0))
      for (const o of sorted) {
        if (o.canOpen(token)) {
          void o.open(token, ctx, event)
          return true
        }
      }
      return false
    },
    clear() {
      matchers.length = 0
      openers.length = 0
    },
  }
}

// 全域 singleton — 供 boot 時註冊內建 matcher/opener
export const terminalLinkRegistry: TerminalLinkRegistry = createRegistry()
```

- [ ] **Step 5: 執行測試確認通過**

Run: `cd spa && npx vitest run src/lib/terminal-link/registry.test.ts`
Expected: PASS，4 tests

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/terminal-link/types.ts spa/src/lib/terminal-link/registry.ts spa/src/lib/terminal-link/registry.test.ts
git commit -m "feat(spa): add terminal link registry skeleton"
```

---

## Task 2: URL matcher

**Files:**
- Create: `spa/src/lib/terminal-link/matchers/url.ts`
- Test: `spa/src/lib/terminal-link/matchers/url.test.ts`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/terminal-link/matchers/url.test.ts
import { describe, it, expect } from 'vitest'
import { urlMatcher } from './url'

describe('url matcher', () => {
  it('matches https URLs', () => {
    const out = urlMatcher.provide('visit https://example.com for info')
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe('https://example.com')
    expect(out[0].range).toEqual({ startCol: 6, endCol: 25 })
  })

  it('matches http URLs', () => {
    const out = urlMatcher.provide('http://a.b/c?x=1')
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe('http://a.b/c?x=1')
  })

  it('matches multiple URLs on one line', () => {
    const out = urlMatcher.provide('a https://x.com and https://y.com z')
    expect(out.map((t) => t.text)).toEqual(['https://x.com', 'https://y.com'])
  })

  it('strips trailing punctuation', () => {
    const out = urlMatcher.provide('see https://example.com.')
    expect(out[0].text).toBe('https://example.com')
  })

  it('does not match non-URL text', () => {
    expect(urlMatcher.provide('just text, no url here')).toEqual([])
  })

  it('produces type "url"', () => {
    expect(urlMatcher.type).toBe('url')
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/lib/terminal-link/matchers/url.test.ts`
Expected: FAIL，`Cannot find module './url'`

- [ ] **Step 3: 實作 matcher**

```ts
// spa/src/lib/terminal-link/matchers/url.ts
import type { LinkMatcher } from '../types'

// 允許 URL 字元集（RFC 3986 + 常見實務），結尾剝除標點
const URL_RE = /https?:\/\/[^\s"'<>`]+/g
const TRAILING_PUNCT = /[.,;:!?)\]}>]+$/

export const urlMatcher: LinkMatcher = {
  id: 'builtin:url',
  type: 'url',
  provide(line) {
    const results: Array<{ text: string; range: { startCol: number; endCol: number } }> = []
    for (const m of line.matchAll(URL_RE)) {
      let text = m[0]
      const trailing = text.match(TRAILING_PUNCT)
      if (trailing) text = text.slice(0, -trailing[0].length)
      const startCol = m.index!
      results.push({ text, range: { startCol, endCol: startCol + text.length } })
    }
    return results
  },
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/lib/terminal-link/matchers/url.test.ts`
Expected: PASS，6 tests

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/terminal-link/matchers/url.ts spa/src/lib/terminal-link/matchers/url.test.ts
git commit -m "feat(spa): add terminal URL matcher"
```

---

## Task 3: File-path matcher

**Files:**
- Create: `spa/src/lib/terminal-link/matchers/file-path.ts`
- Test: `spa/src/lib/terminal-link/matchers/file-path.test.ts`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/terminal-link/matchers/file-path.test.ts
import { describe, it, expect } from 'vitest'
import { filePathMatcher } from './file-path'

describe('file-path matcher', () => {
  it('matches absolute Unix paths with extension', () => {
    const out = filePathMatcher.provide('error at /Users/x/a.ts now')
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe('/Users/x/a.ts')
    expect(out[0].meta).toEqual({ path: '/Users/x/a.ts' })
  })

  it('captures line:col suffix into meta', () => {
    const out = filePathMatcher.provide('at /a/b.ts:12:3')
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe('/a/b.ts:12:3')
    expect(out[0].meta).toEqual({ path: '/a/b.ts', line: 12, col: 3 })
  })

  it('captures line-only suffix', () => {
    const out = filePathMatcher.provide('see /a/b.md:42')
    expect(out[0].meta).toEqual({ path: '/a/b.md', line: 42 })
  })

  it('does not match paths without extension', () => {
    expect(filePathMatcher.provide('cd /usr/local/bin')).toEqual([])
  })

  it('does not match inside URLs', () => {
    expect(filePathMatcher.provide('https://x.com/a.md')).toEqual([])
  })

  it('produces type "file"', () => {
    expect(filePathMatcher.type).toBe('file')
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/lib/terminal-link/matchers/file-path.test.ts`
Expected: FAIL，`Cannot find module './file-path'`

- [ ] **Step 3: 實作 matcher**

```ts
// spa/src/lib/terminal-link/matchers/file-path.ts
import type { LinkMatcher } from '../types'

// 絕對路徑 + 需有副檔名（避免誤配 `/usr/local/bin`）
// 允許後接 :line 或 :line:col
const PATH_RE = /(?<![\w/:])(\/[\w./\-]+\.[A-Za-z0-9]+)(?::(\d+)(?::(\d+))?)?/g

export const filePathMatcher: LinkMatcher = {
  id: 'builtin:file-path',
  type: 'file',
  provide(line) {
    const results: Array<{
      text: string
      range: { startCol: number; endCol: number }
      meta?: Record<string, unknown>
    }> = []
    for (const m of line.matchAll(PATH_RE)) {
      // 排除 URL 內的路徑：前面緊鄰 `://` 則跳過
      const before = line.slice(0, m.index!)
      if (/:\/\/[\w./\-]*$/.test(before)) continue

      const path = m[1]
      const lineNum = m[2] ? parseInt(m[2], 10) : undefined
      const colNum = m[3] ? parseInt(m[3], 10) : undefined
      const text = m[0]
      const startCol = m.index!

      const meta: Record<string, unknown> = { path }
      if (lineNum !== undefined) meta.line = lineNum
      if (colNum !== undefined) meta.col = colNum

      results.push({
        text,
        range: { startCol, endCol: startCol + text.length },
        meta,
      })
    }
    return results
  },
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/lib/terminal-link/matchers/file-path.test.ts`
Expected: PASS，6 tests

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/terminal-link/matchers/file-path.ts spa/src/lib/terminal-link/matchers/file-path.test.ts
git commit -m "feat(spa): add terminal file-path matcher"
```

---

## Task 4: URL opener

**Files:**
- Create: `spa/src/lib/terminal-link/openers/url.ts`
- Test: `spa/src/lib/terminal-link/openers/url.test.ts`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/terminal-link/openers/url.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createUrlOpener } from './url'
import type { LinkToken } from '../types'

const token: LinkToken = {
  type: 'url',
  text: 'https://example.com',
  range: { startCol: 0, endCol: 19 },
}

describe('url opener', () => {
  beforeEach(() => { vi.restoreAllMocks() })

  it('canOpen true only for type url', () => {
    const o = createUrlOpener({ isElectron: false, openBrowserTab: vi.fn(), openMiniWindow: vi.fn() })
    expect(o.canOpen(token)).toBe(true)
    expect(o.canOpen({ ...token, type: 'file' })).toBe(false)
  })

  it('web: uses window.open with _blank', () => {
    const spy = vi.spyOn(window, 'open').mockImplementation(() => null)
    const o = createUrlOpener({ isElectron: false, openBrowserTab: vi.fn(), openMiniWindow: vi.fn() })
    o.open(token, {}, new MouseEvent('click'))
    expect(spy).toHaveBeenCalledWith('https://example.com', '_blank')
  })

  it('electron normal click: openBrowserTab', () => {
    const openBrowserTab = vi.fn()
    const openMiniWindow = vi.fn()
    const o = createUrlOpener({ isElectron: true, openBrowserTab, openMiniWindow })
    o.open(token, {}, new MouseEvent('click'))
    expect(openBrowserTab).toHaveBeenCalledWith('https://example.com')
    expect(openMiniWindow).not.toHaveBeenCalled()
  })

  it('electron shift+click: openMiniWindow', () => {
    const openBrowserTab = vi.fn()
    const openMiniWindow = vi.fn()
    const o = createUrlOpener({ isElectron: true, openBrowserTab, openMiniWindow })
    o.open(token, {}, new MouseEvent('click', { shiftKey: true }))
    expect(openMiniWindow).toHaveBeenCalledWith('https://example.com')
    expect(openBrowserTab).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/lib/terminal-link/openers/url.test.ts`
Expected: FAIL

- [ ] **Step 3: 實作 opener**

```ts
// spa/src/lib/terminal-link/openers/url.ts
import type { LinkOpener } from '../types'

export interface UrlOpenerDeps {
  isElectron: boolean
  openBrowserTab: (url: string) => void
  openMiniWindow: (url: string) => void
}

export function createUrlOpener(deps: UrlOpenerDeps): LinkOpener {
  return {
    id: 'builtin:url',
    priority: 0,
    canOpen: (token) => token.type === 'url',
    open: (token, _ctx, event) => {
      const uri = token.text
      if (deps.isElectron) {
        if (event.shiftKey) deps.openMiniWindow(uri)
        else deps.openBrowserTab(uri)
      } else {
        window.open(uri, '_blank')
      }
    },
  }
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/lib/terminal-link/openers/url.test.ts`
Expected: PASS，4 tests

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/terminal-link/openers/url.ts spa/src/lib/terminal-link/openers/url.test.ts
git commit -m "feat(spa): add terminal URL opener"
```

---

## Task 5: File-path opener（橋接 FileOpener registry）

**Files:**
- Create: `spa/src/lib/terminal-link/openers/file-path.ts`
- Test: `spa/src/lib/terminal-link/openers/file-path.test.ts`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/terminal-link/openers/file-path.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createFilePathOpener } from './file-path'
import type { LinkToken } from '../types'
import type { FileOpener } from '../../file-opener-registry'

const fileToken: LinkToken = {
  type: 'file',
  text: '/a/b.ts',
  range: { startCol: 0, endCol: 7 },
  meta: { path: '/a/b.ts' },
}

function makeDeps() {
  const openSingletonTab = vi.fn(() => 'tab-1')
  const insertTab = vi.fn()
  const paneContent = { kind: 'editor', source: { type: 'daemon', hostId: 'h1' }, filePath: '/a/b.ts' }
  const fakeOpener: FileOpener = {
    id: 'fake', label: '', icon: 'File',
    match: () => true, priority: 'default',
    createContent: vi.fn(() => paneContent as never),
  }
  const getDefaultOpener = vi.fn(() => fakeOpener)
  const getActiveWorkspaceId = vi.fn(() => 'ws-1')
  return { openSingletonTab, insertTab, getDefaultOpener, getActiveWorkspaceId, fakeOpener, paneContent }
}

describe('file-path opener', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('canOpen true for type file with path meta', () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    expect(o.canOpen(fileToken)).toBe(true)
    expect(o.canOpen({ ...fileToken, type: 'url' })).toBe(false)
    expect(o.canOpen({ ...fileToken, meta: undefined })).toBe(false)
  })

  it('requires hostId in ctx to open', () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    o.open(fileToken, {}, new MouseEvent('click'))
    expect(deps.getDefaultOpener).not.toHaveBeenCalled()
  })

  it('looks up FileOpener and opens singleton tab in active workspace', () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    o.open(fileToken, { hostId: 'h1' }, new MouseEvent('click'))

    expect(deps.getDefaultOpener).toHaveBeenCalledWith(expect.objectContaining({
      name: 'b.ts',
      path: '/a/b.ts',
      extension: 'ts',
      isDirectory: false,
    }))
    expect(deps.fakeOpener.createContent).toHaveBeenCalledWith(
      { type: 'daemon', hostId: 'h1' },
      expect.objectContaining({ path: '/a/b.ts' }),
    )
    expect(deps.openSingletonTab).toHaveBeenCalledWith(deps.paneContent)
    expect(deps.insertTab).toHaveBeenCalledWith('tab-1', 'ws-1')
  })

  it('no-op when no FileOpener matches', () => {
    const deps = makeDeps()
    deps.getDefaultOpener.mockReturnValue(null)
    const o = createFilePathOpener(deps)
    o.open(fileToken, { hostId: 'h1' }, new MouseEvent('click'))
    expect(deps.openSingletonTab).not.toHaveBeenCalled()
  })

  it('no-op when no active workspace', () => {
    const deps = makeDeps()
    deps.getActiveWorkspaceId.mockReturnValue(null)
    const o = createFilePathOpener(deps)
    o.open(fileToken, { hostId: 'h1' }, new MouseEvent('click'))
    expect(deps.insertTab).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/lib/terminal-link/openers/file-path.test.ts`
Expected: FAIL

- [ ] **Step 3: 實作 opener**

```ts
// spa/src/lib/terminal-link/openers/file-path.ts
import type { LinkOpener } from '../types'
import type { FileInfo, FileSource } from '../../../types/fs'
import type { PaneContent } from '../../../types/tab'
import type { FileOpener } from '../../file-opener-registry'

export interface FilePathOpenerDeps {
  getDefaultOpener(file: FileInfo): FileOpener | null
  openSingletonTab(content: PaneContent): string
  insertTab(tabId: string, workspaceId: string): void
  getActiveWorkspaceId(): string | null
}

function buildFileInfo(path: string): FileInfo {
  const name = path.split('/').pop() ?? path
  const extension = name.includes('.') ? name.split('.').pop()! : ''
  return { name, path, extension, size: 0, isDirectory: false }
}

export function createFilePathOpener(deps: FilePathOpenerDeps): LinkOpener {
  return {
    id: 'builtin:file-path',
    priority: 0,
    canOpen: (token) =>
      token.type === 'file' &&
      typeof (token.meta as { path?: unknown })?.path === 'string',
    open: (token, ctx) => {
      if (!ctx.hostId) return
      const path = (token.meta as { path: string }).path
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

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/lib/terminal-link/openers/file-path.test.ts`
Expected: PASS，5 tests

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/terminal-link/openers/file-path.ts spa/src/lib/terminal-link/openers/file-path.test.ts
git commit -m "feat(spa): add terminal file-path opener bridging FileOpener registry"
```

---

## Task 6: xterm link provider adapter

**Files:**
- Create: `spa/src/lib/terminal-link/xterm-provider.ts`
- Test: `spa/src/lib/terminal-link/xterm-provider.test.ts`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/terminal-link/xterm-provider.test.ts
import { describe, it, expect, vi } from 'vitest'
import type { Terminal } from '@xterm/xterm'
import { createRegistry } from './registry'
import { createXtermLinkProvider } from './xterm-provider'

function makeTerm(lineText: string): Terminal {
  return {
    buffer: {
      active: {
        getLine: (y: number) => (y === 0 ? { translateToString: () => lineText } : undefined),
      },
    },
  } as unknown as Terminal
}

describe('createXtermLinkProvider', () => {
  it('calls back with no links when no matcher produces results', () => {
    const registry = createRegistry()
    const provider = createXtermLinkProvider(registry, () => ({}), makeTerm('hello'))
    const cb = vi.fn()
    provider.provideLinks(1, cb)
    expect(cb).toHaveBeenCalledWith([])
  })

  it('builds ILink per matched token with 1-indexed range', () => {
    const registry = createRegistry()
    registry.registerMatcher({
      id: 'm', type: 'url',
      provide: () => [{ text: 'foo', range: { startCol: 2, endCol: 5 } }],
    })
    const provider = createXtermLinkProvider(registry, () => ({ hostId: 'h1' }), makeTerm('  foo'))
    const cb = vi.fn()
    provider.provideLinks(1, cb)
    expect(cb).toHaveBeenCalledTimes(1)
    const links = cb.mock.calls[0][0]
    expect(links).toHaveLength(1)
    expect(links[0].text).toBe('foo')
    expect(links[0].range).toEqual({
      start: { x: 3, y: 1 },
      end:   { x: 5, y: 1 },
    })
  })

  it('activate dispatches token to registry with ctx', () => {
    const registry = createRegistry()
    registry.registerMatcher({
      id: 'm', type: 'url',
      provide: () => [{ text: 'x', range: { startCol: 0, endCol: 1 } }],
    })
    const open = vi.fn()
    registry.registerOpener({ id: 'o', canOpen: () => true, open })
    const provider = createXtermLinkProvider(registry, () => ({ hostId: 'h2' }), makeTerm('x'))
    const cb = vi.fn()
    provider.provideLinks(1, cb)
    const link = cb.mock.calls[0][0][0]
    const event = new MouseEvent('click')
    link.activate(event, 'x')
    expect(open).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'url', text: 'x' }),
      { hostId: 'h2' },
      event,
    )
  })

  it('skips empty callback when terminal buffer line missing', () => {
    const registry = createRegistry()
    const term = { buffer: { active: { getLine: () => undefined } } } as unknown as Terminal
    const provider = createXtermLinkProvider(registry, () => ({}), term)
    const cb = vi.fn()
    provider.provideLinks(99, cb)
    expect(cb).toHaveBeenCalledWith([])
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/lib/terminal-link/xterm-provider.test.ts`
Expected: FAIL

- [ ] **Step 3: 實作 provider**

xterm `ILinkProvider` 的 `provideLinks` 需要讀取 terminal buffer 的內容。Factory 直接接 `Terminal`，於 `useTerminal` mount effect 中 term 建立後立即呼叫。ctx 走 getter 讓 props 更新不用重建 provider。

```ts
// spa/src/lib/terminal-link/xterm-provider.ts
import type { Terminal, ILinkProvider, ILink } from '@xterm/xterm'
import type { TerminalLinkRegistry, LinkContext } from './types'

export function createXtermLinkProvider(
  registry: TerminalLinkRegistry,
  getCtx: () => LinkContext,
  term: Terminal,
): ILinkProvider {
  return {
    provideLinks(bufferLineNumber: number, callback: (links: ILink[] | undefined) => void): void {
      const line = term.buffer.active.getLine(bufferLineNumber - 1)
      const text = line?.translateToString() ?? ''
      if (!text) { callback([]); return }

      const links: ILink[] = []
      for (const matcher of registry.getMatchers()) {
        for (const raw of matcher.provide(text)) {
          const token = {
            type: matcher.type,
            text: raw.text,
            range: raw.range,
            meta: raw.meta,
          }
          links.push({
            range: {
              start: { x: raw.range.startCol + 1, y: bufferLineNumber },
              end:   { x: raw.range.endCol, y: bufferLineNumber },
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

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/lib/terminal-link/xterm-provider.test.ts`
Expected: PASS，4 tests

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/terminal-link/xterm-provider.ts spa/src/lib/terminal-link/xterm-provider.test.ts
git commit -m "feat(spa): add xterm link provider adapter"
```

---

## Task 7: Boot 註冊器

**Files:**
- Create: `spa/src/lib/terminal-link/index.ts`
- Create: `spa/src/lib/terminal-link/register.ts`
- Test: `spa/src/lib/terminal-link/register.test.ts`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/terminal-link/register.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { terminalLinkRegistry } from './registry'
import { registerBuiltinTerminalLinks } from './register'

describe('registerBuiltinTerminalLinks', () => {
  beforeEach(() => terminalLinkRegistry.clear())

  it('registers both url and file-path matchers', () => {
    registerBuiltinTerminalLinks({
      isElectron: false,
      openBrowserTab: () => {},
      openMiniWindow: () => {},
      getDefaultFileOpener: () => null,
      openSingletonTab: () => 't',
      insertTab: () => {},
      getActiveWorkspaceId: () => null,
    })
    const types = terminalLinkRegistry.getMatchers().map((m) => m.type)
    expect(types).toContain('url')
    expect(types).toContain('file')
  })

  it('is idempotent — double call does not double-register', () => {
    const deps = {
      isElectron: false,
      openBrowserTab: () => {},
      openMiniWindow: () => {},
      getDefaultFileOpener: () => null,
      openSingletonTab: () => 't',
      insertTab: () => {},
      getActiveWorkspaceId: () => null,
    }
    registerBuiltinTerminalLinks(deps)
    registerBuiltinTerminalLinks(deps)
    const ids = terminalLinkRegistry.getMatchers().map((m) => m.id)
    const unique = new Set(ids)
    expect(unique.size).toBe(ids.length)
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/lib/terminal-link/register.test.ts`
Expected: FAIL

- [ ] **Step 3: 實作 `register.ts` 與 `index.ts`**

```ts
// spa/src/lib/terminal-link/register.ts
import type { FileInfo } from '../../types/fs'
import type { PaneContent } from '../../types/tab'
import type { FileOpener } from '../file-opener-registry'
import { terminalLinkRegistry } from './registry'
import { urlMatcher } from './matchers/url'
import { filePathMatcher } from './matchers/file-path'
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
}

let registered = false

export function registerBuiltinTerminalLinks(deps: BuiltinTerminalLinksDeps): void {
  if (registered) return
  registered = true

  terminalLinkRegistry.registerMatcher(urlMatcher)
  terminalLinkRegistry.registerMatcher(filePathMatcher)

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
  }))
}

// 測試專用 reset
export function __resetBuiltinTerminalLinks(): void {
  registered = false
}
```

```ts
// spa/src/lib/terminal-link/index.ts
export { terminalLinkRegistry, createRegistry } from './registry'
export { createXtermLinkProvider } from './xterm-provider'
export { registerBuiltinTerminalLinks } from './register'
export type {
  LinkToken,
  LinkMatcher,
  LinkOpener,
  LinkContext,
  LinkRange,
  TerminalLinkRegistry,
} from './types'
```

測試需要呼叫 reset：

```ts
// 更新 register.test.ts 的 beforeEach
import { __resetBuiltinTerminalLinks, registerBuiltinTerminalLinks } from './register'
// ...
beforeEach(() => {
  terminalLinkRegistry.clear()
  __resetBuiltinTerminalLinks()
})
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/lib/terminal-link/register.test.ts`
Expected: PASS，2 tests

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/terminal-link/register.ts spa/src/lib/terminal-link/register.test.ts spa/src/lib/terminal-link/index.ts
git commit -m "feat(spa): add builtin terminal link registration entry"
```

---

## Task 8: 接到 useTerminal，移除 WebLinksAddon

**Files:**
- Modify: `spa/src/hooks/useTerminal.ts`
- Modify: `spa/src/components/TerminalView.tsx`
- Modify: `spa/src/components/TerminalView.test.tsx`

- [ ] **Step 1: 寫失敗測試**

現行 `TerminalView.test.tsx` 在 `vi.hoisted()` 裡定義 `TerminalSpy`（line 8-33），用 `this._opts = opts` 並 return 一個方法物件。改法：

```ts
// spa/src/components/TerminalView.test.tsx 變更摘要

// (A) 在 TerminalSpy 的 return 物件中新增 registerLinkProvider
   const TerminalSpy = vi.fn(function (this: Record<string, unknown>, opts: Record<string, unknown>) {
     this._opts = opts
     return {
       loadAddon: vi.fn(),
       open: vi.fn(),
       write: vi.fn(),
       onData: vi.fn(() => ({ dispose: vi.fn() })),
       onResize: vi.fn(() => ({ dispose: vi.fn() })),
       onTitleChange: vi.fn(() => ({ dispose: vi.fn() })),
+      registerLinkProvider: vi.fn(() => ({ dispose: vi.fn() })),
+      buffer: { active: { getLine: () => ({ translateToString: () => '' }) } },
       dispose: vi.fn(),
       focus: vi.fn(),
       unicode: { activeVersion: '6' },
       cols: 80,
       rows: 24,
       _opts: opts,
     }
   })

// (B) 移除 web-links mock（第 61-65 行）：
-  vi.mock('@xterm/addon-web-links', () => ({
-    WebLinksAddon: vi.fn(function () {
-      return { dispose: vi.fn() }
-    }),
-  }))

// (C) 新增一個 test 斷言 registerLinkProvider 被呼叫：
+  it('registers terminal-link provider on mount', () => {
+    TerminalSpy.mockClear()
+    render(<TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />)
+    const termInstance = TerminalSpy.mock.results[0]!.value as { registerLinkProvider: ReturnType<typeof vi.fn> }
+    expect(termInstance.registerLinkProvider).toHaveBeenCalledTimes(1)
+  })
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/components/TerminalView.test.tsx`
Expected: FAIL（web-links 已移除 / registerLinkProvider 未被呼叫）

- [ ] **Step 3: 實作 useTerminal.ts 變更**

```ts
// spa/src/hooks/useTerminal.ts — 變更摘要

// 1) 移除 import
- import { WebLinksAddon } from '@xterm/addon-web-links'

// 2) 新增 import
+ import { createXtermLinkProvider } from '../lib/terminal-link/xterm-provider'
+ import { terminalLinkRegistry } from '../lib/terminal-link/registry'
+ import type { LinkContext } from '../lib/terminal-link/types'

// 3) 調整 UseTerminalOptions
 export interface UseTerminalOptions {
-  linkHandler?: (event: MouseEvent, uri: string) => void
+  linkContext?: LinkContext
   onTitle?: (title: string) => void
 }

// 4) 檔頂新增 ref 保持 ctx 最新（與 onTitleRef 同模式）
+  const linkCtxRef = useRef<LinkContext>(options.linkContext ?? {})
+  linkCtxRef.current = options.linkContext ?? {}

// 5) 在 mount effect 替換 WebLinksAddon 載入區塊
-    try {
-      const lh = options.linkHandler
-      term.loadAddon(lh ? new WebLinksAddon(lh) : new WebLinksAddon())
-    } catch { /* non-critical */ }
+    try {
+      term.registerLinkProvider(
+        createXtermLinkProvider(terminalLinkRegistry, () => linkCtxRef.current, term),
+      )
+    } catch { /* non-critical */ }
```

`linkCtxRef` 與 `onTitleRef` 一樣：每 render 都把最新值寫入 ref，link provider 的 `getCtx` 讀 ref，因此 mount effect 不需重綁（deps 維持 `[]`）。

- [ ] **Step 4: 實作 TerminalView.tsx 變更**

```ts
// spa/src/components/TerminalView.tsx — 變更摘要

// 1) 移除
- import { createLinkHandler } from '../lib/link-handler'
- import { getPlatformCapabilities } from '../lib/platform'
- import { openBrowserTab } from '../lib/open-browser-tab'

// 2) 移除 linkHandler useMemo 整段（createLinkHandler）
// 3) 移除 caps useMemo（若無他處使用）

// 4) useTerminal 呼叫改傳 linkContext
-  const { termRef, fitAddonRef, containerRef } = useTerminal({ linkHandler, onTitle: handleTitle })
+  const linkContext = useMemo(() => ({ hostId, sessionCode }), [hostId, sessionCode])
+  const { termRef, fitAddonRef, containerRef } = useTerminal({ linkContext, onTitle: handleTitle })
```

- [ ] **Step 5: 執行 TerminalView 測試確認通過**

Run: `cd spa && npx vitest run src/components/TerminalView.test.tsx src/hooks/useTerminal`
Expected: PASS

- [ ] **Step 6: 執行全域 lint + 測試**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: all PASS（除非有需修的型別，於此步驟修到零錯誤）

- [ ] **Step 7: Commit**

```bash
git add spa/src/hooks/useTerminal.ts spa/src/components/TerminalView.tsx spa/src/components/TerminalView.test.tsx
git commit -m "refactor(spa): replace WebLinksAddon with terminal-link registry provider"
```

---

## Task 9: 在 boot 註冊內建 matcher/opener，刪除 link-handler.ts

**Files:**
- Modify: `spa/src/lib/register-modules.tsx`
- Delete: `spa/src/lib/link-handler.ts`

- [ ] **Step 1: 確認 link-handler.ts 已無引用（grep）**

Run: `cd spa && rg "from.*link-handler" src`
Expected: 無結果（TerminalView 已於 Task 8 移除）

- [ ] **Step 2: 於 register-modules.tsx 加入呼叫**

Entry function: `registerBuiltinModules()`（`spa/src/lib/register-modules.tsx:79`），由 `main.tsx:15` boot 時呼叫一次。

```ts
// spa/src/lib/register-modules.tsx — 檔頂 import 加：
+ import { registerBuiltinTerminalLinks } from './terminal-link/register'
+ import { useTabStore } from '../stores/useTabStore'
+ import { useWorkspaceStore } from '../stores/useWorkspaceStore'
+ import { openBrowserTab } from './open-browser-tab'
// （`getDefaultOpener`、`getPlatformCapabilities` 若已 import 則不重複）

// 於 registerBuiltinModules() 函式末段（所有 registerFileOpener / registerModule 呼叫完之後）加：
+  const platformCaps = getPlatformCapabilities()
+  registerBuiltinTerminalLinks({
+    isElectron: platformCaps.isElectron,
+    openBrowserTab,
+    openMiniWindow: (url) => window.electronAPI?.browserViewOpenMiniWindow(url),
+    getDefaultFileOpener: getDefaultOpener,
+    openSingletonTab: (content) => useTabStore.getState().openSingletonTab(content),
+    insertTab: (tabId, wsId) => useWorkspaceStore.getState().insertTab(tabId, wsId),
+    getActiveWorkspaceId: () => useWorkspaceStore.getState().activeWorkspaceId,
+  })
```

注意：若 `register-modules.tsx` 原本已 import `getPlatformCapabilities` / `getDefaultOpener`，避免重複 import。`platformCaps` 變數名避免與檔內既有 `caps` 衝突（若有）。

- [ ] **Step 3: 刪除 link-handler.ts**

Run: `rm spa/src/lib/link-handler.ts`

若 `spa/src/lib/__tests__/link-handler.test.ts` 存在，一併 rm。

- [ ] **Step 4: 執行測試 + lint**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: all PASS

- [ ] **Step 5: Build 檢查**

Run: `cd spa && pnpm run build`
Expected: build 成功

- [ ] **Step 6: 手動驗證（UI）**

啟動開發環境，在 terminal 中：
1. `echo https://example.com` → 連結能 hover 變底線，click 開啟瀏覽器 tab（Electron）或新視窗（web）。shift+click 開啟 mini window。
2. `echo /Users/x/a.ts:12:3`（或實際存在的檔案）→ 連結可點，於 editor 模組開啟對應檔案 tab。

若未能 smoke-test，於 PR 描述中明確標註 UI 未驗證並說明原因。

- [ ] **Step 7: Commit**

```bash
git add spa/src/lib/register-modules.tsx
git rm spa/src/lib/link-handler.ts
git commit -m "feat(spa): wire terminal link registry at boot; drop link-handler module"
```

---

## 完成後

- 跑全域：`cd spa && pnpm run lint && npx vitest run && pnpm run build`
- 更新 `VERSION` + `CHANGELOG.md`（CLAUDE.md 要求：每個 PR merge 後）— 本 PR 於發 PR 階段處理，不在實作任務中。
- 依 CLAUDE.md「PR Review 兩輪制」發 PR 等 review。

## 未來擴充（out of scope）

- Markdown preview Module：新增 `MarkdownPreview` module + FileOpener（priority `default`，副檔名 md / markdown）— 本架構零修改即可生效。
- `issue #123` / `PR #45` matcher → VCS opener。
- Stacktrace matcher（多行）— 需 xterm `registerLinkProvider` 於 bufferLineNumber 掃多行或用 `decorations.pointerCursor`。
- `cwd` 相對路徑解析：terminal 透過 daemon `pane_current_path` API 取當前 cwd，於 opener 把相對路徑 resolve 成絕對路徑。

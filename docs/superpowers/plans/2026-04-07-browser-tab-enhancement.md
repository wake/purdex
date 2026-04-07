# Browser Tab Enhancement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add toolbar controls to browser tabs and support mini browser popup windows via Shift+click.

**Architecture:** Shared `BrowserToolbar` component (props-driven) used by both in-tab browser panes and standalone mini browser windows. Electron side extended with navigation IPC, state-update push, WebContentsView preload injection for shiftKey detection, and mini browser window lifecycle. Link handler unified as a factory function injected into terminal and browser pane contexts.

**Tech Stack:** React 19, Zustand 5, Phosphor Icons, Electron (WebContentsView, BrowserWindow, contextBridge), xterm.js 6 (WebLinksAddon), Vite multi-entry, Vitest

**Spec:** `docs/superpowers/specs/2026-04-07-browser-tab-enhancement-design.md`

---

## File Map

### New Files

| File | Responsibility |
|------|---------------|
| `spa/src/lib/url-utils.ts` | URL 正規化 + 驗證（shared utility） |
| `spa/src/components/BrowserToolbar.tsx` | 共用 toolbar UI（純 props-driven） |
| `spa/src/components/BrowserToolbarMenu.tsx` | ⋯ 更多選單下拉 |
| `spa/src/hooks/useBrowserViewState.ts` | 訂閱 Electron state-update IPC |
| `spa/src/lib/link-handler.ts` | `createLinkHandler` factory |
| `spa/src/components/MiniBrowserApp.tsx` | Mini browser 視窗的 React 根元件 |
| `spa/mini-browser.html` | Mini browser HTML entry |
| `spa/src/mini-browser.tsx` | Mini browser React entry |
| `electron/browser-view-ipc.ts` | 集中 browser-view IPC handler |
| `electron/browser-view-preload.ts` | WebContentsView 注入 preload |
| `electron/mini-browser-window.ts` | Mini browser BrowserWindow 管理 |

### Modified Files

| File | Changes |
|------|---------|
| `spa/src/components/BrowserPane.tsx` | 整合 toolbar + 調整 ResizeObserver |
| `spa/src/hooks/useTerminal.ts` | 接受 `linkHandler` 參數 |
| `spa/src/components/TerminalView.tsx` | 傳入 linkHandler |
| `spa/src/hooks/useTabWorkspaceActions.ts` | 新增 `openBrowserTab` |
| `spa/src/components/TabBar.tsx` | ICON_MAP 加入 `Globe` |
| `spa/src/lib/register-panes.tsx` | 更新 BrowserPane props（如需要） |
| `electron/browser-view-manager.ts` | 導航 API + state push + destroy + getEntryByWebContents + preload 注入 |
| `electron/preload.ts` | 暴露新 IPC 方法 |
| `electron/main.ts` | 委派 IPC 到 browser-view-ipc.ts |
| `electron.vite.config.ts` | 新增 preload + renderer entry |

### Test Files

| File | Covers |
|------|--------|
| `spa/src/lib/__tests__/url-utils.test.ts` | URL 正規化 + 驗證 |
| `spa/src/components/__tests__/BrowserToolbar.test.tsx` | Toolbar 渲染、按鈕狀態、選單 |
| `spa/src/hooks/__tests__/useBrowserViewState.test.ts` | State subscription + paneId 過濾 |
| `spa/src/lib/__tests__/link-handler.test.ts` | 分派邏輯 |
| `spa/src/hooks/__tests__/useTerminal.test.ts` | linkHandler 注入（更新現有測試） |

---

## Task 1: URL Utility

**Files:**
- Create: `spa/src/lib/url-utils.ts`
- Create: `spa/src/lib/__tests__/url-utils.test.ts`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/lib/__tests__/url-utils.test.ts
import { describe, it, expect } from 'vitest'
import { normalizeUrl } from '../url-utils'

describe('normalizeUrl', () => {
  it('returns valid https URL as-is', () => {
    expect(normalizeUrl('https://github.com')).toBe('https://github.com')
  })

  it('returns valid http URL as-is', () => {
    expect(normalizeUrl('http://localhost:3000')).toBe('http://localhost:3000')
  })

  it('prepends https:// when no scheme', () => {
    expect(normalizeUrl('github.com')).toBe('https://github.com')
  })

  it('prepends https:// for domain with path', () => {
    expect(normalizeUrl('github.com/wake/tmux-box')).toBe('https://github.com/wake/tmux-box')
  })

  it('returns null for javascript: scheme', () => {
    expect(normalizeUrl('javascript:alert(1)')).toBeNull()
  })

  it('returns null for file: scheme', () => {
    expect(normalizeUrl('file:///etc/passwd')).toBeNull()
  })

  it('returns null for ftp: scheme', () => {
    expect(normalizeUrl('ftp://example.com')).toBeNull()
  })

  it('returns null for empty string', () => {
    expect(normalizeUrl('')).toBeNull()
  })

  it('returns null for whitespace only', () => {
    expect(normalizeUrl('   ')).toBeNull()
  })

  it('trims whitespace', () => {
    expect(normalizeUrl('  https://github.com  ')).toBe('https://github.com')
  })

  it('returns null for malformed URL', () => {
    expect(normalizeUrl('not a url at all')).toBeNull()
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/lib/__tests__/url-utils.test.ts`
Expected: FAIL — module `../url-utils` not found

- [ ] **Step 3: Implement url-utils.ts**

```typescript
// spa/src/lib/url-utils.ts
const ALLOWED_SCHEMES = ['http:', 'https:']

export function normalizeUrl(input: string): string | null {
  const trimmed = input.trim()
  if (!trimmed) return null

  let urlStr = trimmed
  if (!trimmed.includes('://')) {
    urlStr = `https://${trimmed}`
  }

  try {
    const parsed = new URL(urlStr)
    if (!ALLOWED_SCHEMES.includes(parsed.protocol)) return null
    return parsed.href
  } catch {
    return null
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/lib/__tests__/url-utils.test.ts`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/url-utils.ts spa/src/lib/__tests__/url-utils.test.ts
git commit -m "feat(spa): add url-utils with normalizeUrl"
```

---

## Task 2: link-handler Factory

**Files:**
- Create: `spa/src/lib/link-handler.ts`
- Create: `spa/src/lib/__tests__/link-handler.test.ts`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/lib/__tests__/link-handler.test.ts
import { describe, it, expect, vi } from 'vitest'
import { createLinkHandler } from '../link-handler'

function makeMouseEvent(overrides: Partial<MouseEvent> = {}): MouseEvent {
  return { shiftKey: false, ...overrides } as MouseEvent
}

describe('createLinkHandler', () => {
  describe('Electron mode', () => {
    it('calls openBrowserTab on normal click', () => {
      const openBrowserTab = vi.fn()
      const openMiniWindow = vi.fn()
      const handler = createLinkHandler({
        isElectron: true,
        openBrowserTab,
        openMiniWindow,
      })

      handler(makeMouseEvent(), 'https://github.com')

      expect(openBrowserTab).toHaveBeenCalledWith('https://github.com')
      expect(openMiniWindow).not.toHaveBeenCalled()
    })

    it('calls openMiniWindow on shift+click', () => {
      const openBrowserTab = vi.fn()
      const openMiniWindow = vi.fn()
      const handler = createLinkHandler({
        isElectron: true,
        openBrowserTab,
        openMiniWindow,
      })

      handler(makeMouseEvent({ shiftKey: true }), 'https://github.com')

      expect(openMiniWindow).toHaveBeenCalledWith('https://github.com')
      expect(openBrowserTab).not.toHaveBeenCalled()
    })
  })

  describe('SPA mode', () => {
    it('calls window.open on any click', () => {
      const openSpy = vi.fn()
      vi.stubGlobal('open', openSpy)

      const handler = createLinkHandler({
        isElectron: false,
        openBrowserTab: vi.fn(),
        openMiniWindow: vi.fn(),
      })

      handler(makeMouseEvent(), 'https://github.com')

      expect(openSpy).toHaveBeenCalledWith('https://github.com', '_blank')

      vi.unstubAllGlobals()
    })

    it('calls window.open on shift+click too', () => {
      const openSpy = vi.fn()
      vi.stubGlobal('open', openSpy)

      const handler = createLinkHandler({
        isElectron: false,
        openBrowserTab: vi.fn(),
        openMiniWindow: vi.fn(),
      })

      handler(makeMouseEvent({ shiftKey: true }), 'https://github.com')

      expect(openSpy).toHaveBeenCalledWith('https://github.com', '_blank')

      vi.unstubAllGlobals()
    })
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/lib/__tests__/link-handler.test.ts`
Expected: FAIL — module `../link-handler` not found

- [ ] **Step 3: Implement link-handler.ts**

```typescript
// spa/src/lib/link-handler.ts
export interface LinkHandlerDeps {
  isElectron: boolean
  openBrowserTab: (url: string) => void
  openMiniWindow: (url: string) => void
}

export function createLinkHandler(deps: LinkHandlerDeps) {
  return (event: MouseEvent, uri: string): void => {
    if (deps.isElectron) {
      if (event.shiftKey) {
        deps.openMiniWindow(uri)
      } else {
        deps.openBrowserTab(uri)
      }
    } else {
      window.open(uri, '_blank')
    }
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/lib/__tests__/link-handler.test.ts`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/link-handler.ts spa/src/lib/__tests__/link-handler.test.ts
git commit -m "feat(spa): add createLinkHandler factory"
```

---

## Task 3: BrowserToolbar Component

**Files:**
- Create: `spa/src/components/BrowserToolbar.tsx`
- Create: `spa/src/components/BrowserToolbarMenu.tsx`
- Create: `spa/src/components/__tests__/BrowserToolbar.test.tsx`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/components/__tests__/BrowserToolbar.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { BrowserToolbar } from '../BrowserToolbar'

function makeProps(overrides = {}) {
  return {
    url: 'https://github.com',
    title: 'GitHub',
    canGoBack: true,
    canGoForward: false,
    isLoading: false,
    context: 'tab' as const,
    onGoBack: vi.fn(),
    onGoForward: vi.fn(),
    onReload: vi.fn(),
    onStop: vi.fn(),
    onNavigate: vi.fn(),
    onOpenExternal: vi.fn(),
    onCopyUrl: vi.fn(),
    ...overrides,
  }
}

describe('BrowserToolbar', () => {
  it('renders back, forward, reload buttons', () => {
    render(<BrowserToolbar {...makeProps()} />)
    expect(screen.getByLabelText('Go back')).toBeInTheDocument()
    expect(screen.getByLabelText('Go forward')).toBeInTheDocument()
    expect(screen.getByLabelText('Reload')).toBeInTheDocument()
  })

  it('disables forward button when canGoForward is false', () => {
    render(<BrowserToolbar {...makeProps({ canGoForward: false })} />)
    expect(screen.getByLabelText('Go forward')).toBeDisabled()
  })

  it('disables back button when canGoBack is false', () => {
    render(<BrowserToolbar {...makeProps({ canGoBack: false })} />)
    expect(screen.getByLabelText('Go back')).toBeDisabled()
  })

  it('shows stop button when isLoading is true', () => {
    render(<BrowserToolbar {...makeProps({ isLoading: true })} />)
    expect(screen.getByLabelText('Stop')).toBeInTheDocument()
    expect(screen.queryByLabelText('Reload')).not.toBeInTheDocument()
  })

  it('calls onGoBack when back button clicked', () => {
    const onGoBack = vi.fn()
    render(<BrowserToolbar {...makeProps({ onGoBack })} />)
    fireEvent.click(screen.getByLabelText('Go back'))
    expect(onGoBack).toHaveBeenCalledOnce()
  })

  it('calls onNavigate with normalized URL on Enter', () => {
    const onNavigate = vi.fn()
    render(<BrowserToolbar {...makeProps({ onNavigate })} />)
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value: 'example.com' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onNavigate).toHaveBeenCalledWith('https://example.com')
  })

  it('does not call onNavigate for invalid URL', () => {
    const onNavigate = vi.fn()
    render(<BrowserToolbar {...makeProps({ onNavigate })} />)
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value: 'javascript:alert(1)' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onNavigate).not.toHaveBeenCalled()
  })

  it('displays current URL in input', () => {
    render(<BrowserToolbar {...makeProps({ url: 'https://example.com/page' })} />)
    expect(screen.getByRole('textbox')).toHaveValue('https://example.com/page')
  })

  it('shows popOut in menu for tab context', () => {
    const onPopOut = vi.fn()
    render(<BrowserToolbar {...makeProps({ onPopOut })} />)
    fireEvent.click(screen.getByLabelText('More'))
    expect(screen.getByText(/mini browser/i)).toBeInTheDocument()
  })

  it('shows moveToTab in menu for mini-window context', () => {
    const onMoveToTab = vi.fn()
    render(<BrowserToolbar {...makeProps({ context: 'mini-window', onMoveToTab })} />)
    fireEvent.click(screen.getByLabelText('More'))
    expect(screen.getByText(/主視窗/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/components/__tests__/BrowserToolbar.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: Implement BrowserToolbarMenu.tsx**

```typescript
// spa/src/components/BrowserToolbarMenu.tsx
import { useEffect, useRef } from 'react'
import {
  ArrowSquareOut,
  Copy,
  ArrowSquareUpRight,
  ArrowLineRight,
} from '@phosphor-icons/react'
import { useTranslation } from 'react-i18next'

interface MenuItem {
  label: string
  icon: React.ReactNode
  onClick: () => void
  show: boolean
}

interface BrowserToolbarMenuProps {
  context: 'tab' | 'mini-window'
  onOpenExternal: () => void
  onCopyUrl: () => void
  onPopOut?: () => void
  onMoveToTab?: () => void
  onClose: () => void
}

export function BrowserToolbarMenu({
  context,
  onOpenExternal,
  onCopyUrl,
  onPopOut,
  onMoveToTab,
  onClose,
}: BrowserToolbarMenuProps) {
  const { t } = useTranslation()
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose()
    }
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('mousedown', handleClick)
    document.addEventListener('keydown', handleKey)
    return () => {
      document.removeEventListener('mousedown', handleClick)
      document.removeEventListener('keydown', handleKey)
    }
  }, [onClose])

  const items: MenuItem[] = [
    {
      label: t('browser.open_external'),
      icon: <ArrowSquareOut size={14} />,
      onClick: () => { onOpenExternal(); onClose() },
      show: true,
    },
    {
      label: t('browser.copy_url'),
      icon: <Copy size={14} />,
      onClick: () => { onCopyUrl(); onClose() },
      show: true,
    },
    {
      label: t('browser.pop_out'),
      icon: <ArrowSquareUpRight size={14} />,
      onClick: () => { onPopOut?.(); onClose() },
      show: context === 'tab' && !!onPopOut,
    },
    {
      label: t('browser.move_to_tab'),
      icon: <ArrowLineRight size={14} />,
      onClick: () => { onMoveToTab?.(); onClose() },
      show: context === 'mini-window' && !!onMoveToTab,
    },
  ]

  return (
    <div
      ref={ref}
      className="absolute right-0 top-full mt-1 z-50 min-w-48 rounded-md border border-border bg-popover py-1 shadow-lg"
    >
      {items.filter((i) => i.show).map((item) => (
        <button
          key={item.label}
          className="flex w-full items-center gap-2 px-3 py-1.5 text-sm text-popover-foreground hover:bg-accent"
          onClick={item.onClick}
        >
          {item.icon}
          {item.label}
        </button>
      ))}
    </div>
  )
}
```

- [ ] **Step 4: Implement BrowserToolbar.tsx**

```typescript
// spa/src/components/BrowserToolbar.tsx
import { useState, useRef, useEffect, useCallback } from 'react'
import {
  ArrowLeft,
  ArrowRight,
  ArrowClockwise,
  X,
  DotsThree,
} from '@phosphor-icons/react'
import { normalizeUrl } from '../lib/url-utils'
import { BrowserToolbarMenu } from './BrowserToolbarMenu'

export interface BrowserToolbarProps {
  url: string
  title: string
  canGoBack: boolean
  canGoForward: boolean
  isLoading: boolean
  context: 'tab' | 'mini-window'
  onGoBack: () => void
  onGoForward: () => void
  onReload: () => void
  onStop: () => void
  onNavigate: (url: string) => void
  onOpenExternal: () => void
  onCopyUrl: () => void
  onPopOut?: () => void
  onMoveToTab?: () => void
}

export function BrowserToolbar({
  url,
  canGoBack,
  canGoForward,
  isLoading,
  context,
  onGoBack,
  onGoForward,
  onReload,
  onStop,
  onNavigate,
  onOpenExternal,
  onCopyUrl,
  onPopOut,
  onMoveToTab,
}: BrowserToolbarProps) {
  const [inputValue, setInputValue] = useState(url)
  const [isEditing, setIsEditing] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  // Sync URL from prop when not editing
  useEffect(() => {
    if (!isEditing) setInputValue(url)
  }, [url, isEditing])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') {
        const normalized = normalizeUrl(inputValue)
        if (normalized) {
          onNavigate(normalized)
          setIsEditing(false)
          inputRef.current?.blur()
        }
      } else if (e.key === 'Escape') {
        setInputValue(url)
        setIsEditing(false)
        inputRef.current?.blur()
      }
    },
    [inputValue, url, onNavigate],
  )

  return (
    <div className="flex items-center gap-1 px-2 py-1 border-b border-border bg-surface-1">
      {/* Navigation buttons */}
      <button
        aria-label="Go back"
        className="p-1 rounded hover:bg-accent disabled:opacity-30"
        disabled={!canGoBack}
        onClick={onGoBack}
      >
        <ArrowLeft size={16} />
      </button>
      <button
        aria-label="Go forward"
        className="p-1 rounded hover:bg-accent disabled:opacity-30"
        disabled={!canGoForward}
        onClick={onGoForward}
      >
        <ArrowRight size={16} />
      </button>
      {isLoading ? (
        <button
          aria-label="Stop"
          className="p-1 rounded hover:bg-accent"
          onClick={onStop}
        >
          <X size={16} />
        </button>
      ) : (
        <button
          aria-label="Reload"
          className="p-1 rounded hover:bg-accent"
          onClick={onReload}
        >
          <ArrowClockwise size={16} />
        </button>
      )}

      {/* URL bar */}
      <input
        ref={inputRef}
        role="textbox"
        className="flex-1 mx-1 px-2 py-0.5 rounded bg-surface-2 text-xs font-mono text-foreground outline-none focus:ring-1 focus:ring-accent"
        value={inputValue}
        onChange={(e) => { setInputValue(e.target.value); setIsEditing(true) }}
        onFocus={() => { setIsEditing(true); inputRef.current?.select() }}
        onBlur={() => { setIsEditing(false); setInputValue(url) }}
        onKeyDown={handleKeyDown}
      />

      {/* More menu */}
      <div className="relative">
        <button
          aria-label="More"
          className="p-1 rounded hover:bg-accent"
          onClick={() => setMenuOpen((v) => !v)}
        >
          <DotsThree size={16} weight="bold" />
        </button>
        {menuOpen && (
          <BrowserToolbarMenu
            context={context}
            onOpenExternal={onOpenExternal}
            onCopyUrl={onCopyUrl}
            onPopOut={onPopOut}
            onMoveToTab={onMoveToTab}
            onClose={() => setMenuOpen(false)}
          />
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/components/__tests__/BrowserToolbar.test.tsx`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/BrowserToolbar.tsx spa/src/components/BrowserToolbarMenu.tsx spa/src/components/__tests__/BrowserToolbar.test.tsx
git commit -m "feat(spa): add BrowserToolbar and BrowserToolbarMenu components"
```

---

## Task 4: useBrowserViewState Hook

**Files:**
- Create: `spa/src/hooks/useBrowserViewState.ts`
- Create: `spa/src/hooks/__tests__/useBrowserViewState.test.ts`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/hooks/__tests__/useBrowserViewState.test.ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useBrowserViewState } from '../useBrowserViewState'
import type { BrowserViewState } from '../useBrowserViewState'

describe('useBrowserViewState', () => {
  let listeners: Array<(paneId: string, state: BrowserViewState) => void>
  let mockUnsubscribe: ReturnType<typeof vi.fn>

  beforeEach(() => {
    listeners = []
    mockUnsubscribe = vi.fn()
    vi.stubGlobal('electronAPI', {
      onBrowserViewStateUpdate: vi.fn((cb) => {
        listeners.push(cb)
        return mockUnsubscribe
      }),
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('returns initial empty state', () => {
    const { result } = renderHook(() => useBrowserViewState('pane-1'))
    expect(result.current).toEqual({
      url: '',
      title: '',
      canGoBack: false,
      canGoForward: false,
      isLoading: false,
    })
  })

  it('updates state when matching paneId received', () => {
    const { result } = renderHook(() => useBrowserViewState('pane-1'))

    act(() => {
      listeners[0]('pane-1', {
        url: 'https://github.com',
        title: 'GitHub',
        canGoBack: true,
        canGoForward: false,
        isLoading: false,
      })
    })

    expect(result.current.url).toBe('https://github.com')
    expect(result.current.title).toBe('GitHub')
    expect(result.current.canGoBack).toBe(true)
  })

  it('ignores state for different paneId', () => {
    const { result } = renderHook(() => useBrowserViewState('pane-1'))

    act(() => {
      listeners[0]('pane-OTHER', {
        url: 'https://other.com',
        title: 'Other',
        canGoBack: true,
        canGoForward: true,
        isLoading: true,
      })
    })

    expect(result.current.url).toBe('')
  })

  it('calls unsubscribe on unmount', () => {
    const { unmount } = renderHook(() => useBrowserViewState('pane-1'))
    unmount()
    expect(mockUnsubscribe).toHaveBeenCalledOnce()
  })

  it('returns empty state when electronAPI not available', () => {
    vi.stubGlobal('electronAPI', undefined)
    const { result } = renderHook(() => useBrowserViewState('pane-1'))
    expect(result.current.url).toBe('')
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/hooks/__tests__/useBrowserViewState.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement useBrowserViewState.ts**

```typescript
// spa/src/hooks/useBrowserViewState.ts
import { useState, useEffect } from 'react'

export interface BrowserViewState {
  url: string
  title: string
  canGoBack: boolean
  canGoForward: boolean
  isLoading: boolean
}

const INITIAL_STATE: BrowserViewState = {
  url: '',
  title: '',
  canGoBack: false,
  canGoForward: false,
  isLoading: false,
}

export function useBrowserViewState(paneId: string): BrowserViewState {
  const [state, setState] = useState<BrowserViewState>(INITIAL_STATE)

  useEffect(() => {
    const api = window.electronAPI
    if (!api?.onBrowserViewStateUpdate) return

    const unsubscribe = api.onBrowserViewStateUpdate(
      (id: string, update: BrowserViewState) => {
        if (id === paneId) setState(update)
      },
    )

    return unsubscribe
  }, [paneId])

  return state
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/hooks/__tests__/useBrowserViewState.test.ts`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add spa/src/hooks/useBrowserViewState.ts spa/src/hooks/__tests__/useBrowserViewState.test.ts
git commit -m "feat(spa): add useBrowserViewState hook"
```

---

## Task 5: BrowserViewManager Extensions

**Files:**
- Modify: `electron/browser-view-manager.ts`

This task extends the existing manager with navigation methods, state-update push, `destroy()`, `getEntryByWebContents()`, and preload script injection. No unit tests (Electron integration); verified in Task 11 integration.

- [ ] **Step 1: Add navigation methods to BrowserViewManager**

In `electron/browser-view-manager.ts`, after the existing `resize()` method (around line 121), add:

```typescript
goBack(paneId: string): void {
  const entry = this.views.get(paneId)
  if (entry?.view.webContents.canGoBack()) {
    entry.view.webContents.goBack()
  }
}

goForward(paneId: string): void {
  const entry = this.views.get(paneId)
  if (entry?.view.webContents.canGoForward()) {
    entry.view.webContents.goForward()
  }
}

reload(paneId: string): void {
  const entry = this.views.get(paneId)
  entry?.view.webContents.reload()
}

stop(paneId: string): void {
  const entry = this.views.get(paneId)
  entry?.view.webContents.stop()
}
```

- [ ] **Step 2: Add destroy() method**

After the `discard()` method, add:

```typescript
destroy(paneId: string): void {
  const entry = this.views.get(paneId)
  if (!entry) return
  try {
    entry.window.contentView.removeChildView(entry.view)
  } catch { /* already removed */ }
  try {
    entry.view.webContents.close()
  } catch { /* already closed */ }
  this.views.delete(paneId)
}
```

- [ ] **Step 3: Add getEntryByWebContents() method**

```typescript
getEntryByWebContents(wc: WebContents): ViewEntry | undefined {
  for (const entry of this.views.values()) {
    if (entry.view.webContents === wc) return entry
  }
  return undefined
}
```

Import `WebContents` from `electron` at the top of the file.

- [ ] **Step 4: Add getCurrentState() method for snapshot retrieval**

```typescript
getCurrentState(paneId: string): { url: string; title: string } | undefined {
  const entry = this.views.get(paneId)
  if (!entry) return undefined
  return {
    url: entry.view.webContents.getURL(),
    title: entry.view.webContents.getTitle(),
  }
}
```

- [ ] **Step 5: Add state-update push in open() method**

In the `open()` method, after `entry.view.webContents.loadURL(url)` and before adding to `this.views`, add event listeners that push state to the SPA:

```typescript
const wc = entry.view.webContents

const pushState = () => {
  try {
    entry.window.webContents.send('browser-view:state-update', paneId, {
      url: wc.getURL(),
      title: wc.getTitle(),
      canGoBack: wc.canGoBack(),
      canGoForward: wc.canGoForward(),
      isLoading: wc.isLoading(),
    })
  } catch { /* window may be closed */ }
}

wc.on('did-navigate', pushState)
wc.on('did-navigate-in-page', pushState)
wc.on('did-start-loading', pushState)
wc.on('did-stop-loading', pushState)
wc.on('page-title-updated', pushState)
```

- [ ] **Step 6: Inject preload script in open() method**

Modify the `WebContentsView` constructor in `open()` to include the preload path. Change:

```typescript
const view = new WebContentsView({
  webPreferences: {
    contextIsolation: true,
    sandbox: true,
  },
})
```

To:

```typescript
import { join } from 'node:path'

const view = new WebContentsView({
  webPreferences: {
    contextIsolation: true,
    sandbox: true,
    preload: join(__dirname, '../preload/browserViewPreload.js'),
  },
})
```

- [ ] **Step 7: Commit**

```bash
git add electron/browser-view-manager.ts
git commit -m "feat(electron): extend BrowserViewManager with navigation, state push, destroy, preload"
```

---

## Task 6: browser-view-preload.ts

**Files:**
- Create: `electron/browser-view-preload.ts`

- [ ] **Step 1: Create preload script**

```typescript
// electron/browser-view-preload.ts
import { ipcRenderer } from 'electron'

document.addEventListener(
  'click',
  (e: MouseEvent) => {
    const link = (e.target as HTMLElement).closest('a[href]') as HTMLAnchorElement | null
    if (!link) return
    const href = link.href
    if (!href || href.startsWith('javascript:')) return

    if (e.shiftKey || link.target === '_blank') {
      e.preventDefault()
      ipcRenderer.send('browser-view:link-click', {
        url: href,
        shiftKey: e.shiftKey,
        targetBlank: link.target === '_blank',
      })
    }
    // Normal links: don't intercept, let will-navigate handle
  },
  true,
)
```

- [ ] **Step 2: Commit**

```bash
git add electron/browser-view-preload.ts
git commit -m "feat(electron): add browser-view-preload for link click interception"
```

---

## Task 7: browser-view-ipc.ts + Main Integration

**Files:**
- Create: `electron/browser-view-ipc.ts`
- Modify: `electron/main.ts`

- [ ] **Step 1: Create browser-view-ipc.ts**

```typescript
// electron/browser-view-ipc.ts
import { ipcMain, BrowserWindow, shell } from 'electron'
import type { BrowserViewManager } from './browser-view-manager'
import type { MiniWindowManager } from './mini-browser-window'

export function registerBrowserViewIpc(
  manager: BrowserViewManager,
  miniWindowManager: MiniWindowManager,
): void {
  // Existing handlers (moved from main.ts)
  ipcMain.handle('browser-view:open', (event, url: string, paneId: string) => {
    const win = BrowserWindow.fromWebContents(event.sender)
    if (win) manager.open(win, url, paneId)
  })

  ipcMain.handle('browser-view:close', (_event, paneId: string) => {
    manager.background(paneId)
  })

  ipcMain.handle('browser-view:navigate', (_event, paneId: string, url: string) => {
    manager.navigate(paneId, url)
  })

  ipcMain.handle('browser-view:resize', (_event, paneId: string, boundsJson: string) => {
    const raw = JSON.parse(boundsJson)
    manager.resize(paneId, {
      x: Math.round(raw.x),
      y: Math.round(raw.y),
      width: Math.round(raw.width),
      height: Math.round(raw.height),
    })
  })

  // New navigation handlers
  ipcMain.handle('browser-view:go-back', (_event, paneId: string) => {
    manager.goBack(paneId)
  })

  ipcMain.handle('browser-view:go-forward', (_event, paneId: string) => {
    manager.goForward(paneId)
  })

  ipcMain.handle('browser-view:reload', (_event, paneId: string) => {
    manager.reload(paneId)
  })

  ipcMain.handle('browser-view:stop', (_event, paneId: string) => {
    manager.stop(paneId)
  })

  ipcMain.handle('browser-view:destroy', (_event, paneId: string) => {
    manager.destroy(paneId)
  })

  // Mini browser window
  ipcMain.handle('browser-view:open-mini-window', (event, url: string) => {
    const parentWin = BrowserWindow.fromWebContents(event.sender)
    if (parentWin) miniWindowManager.open(parentWin, url)
  })

  // Move to tab: mini window → parent window
  ipcMain.handle('browser-view:move-to-tab', (_event, paneId: string) => {
    miniWindowManager.moveToTab(paneId)
  })

  // Link click from WebContentsView preload
  ipcMain.on('browser-view:link-click', (event, data: { url: string; shiftKey: boolean; targetBlank: boolean }) => {
    const entry = manager.getEntryByWebContents(event.sender)
    if (!entry) return

    if (data.shiftKey) {
      miniWindowManager.open(entry.window, data.url)
    } else {
      // Notify parent window SPA to open new browser tab
      entry.window.webContents.send('browser-view:open-in-tab', data.url)
    }
  })
}
```

- [ ] **Step 2: Update main.ts — remove old browser-view handlers and delegate**

In `electron/main.ts`, replace the existing browser-view `ipcMain.handle(...)` blocks (around lines 34-55) with:

```typescript
import { registerBrowserViewIpc } from './browser-view-ipc'
import { MiniWindowManager } from './mini-browser-window'

// After browserViewManager and windowManager are created:
const miniWindowManager = new MiniWindowManager(browserViewManager, windowManager)
registerBrowserViewIpc(browserViewManager, miniWindowManager)
```

Remove the individual `ipcMain.handle('browser-view:open', ...)` etc. from main.ts since they are now in `browser-view-ipc.ts`.

- [ ] **Step 3: Commit**

```bash
git add electron/browser-view-ipc.ts electron/main.ts
git commit -m "feat(electron): centralize browser-view IPC in browser-view-ipc.ts"
```

---

## Task 8: Preload API Extensions

**Files:**
- Modify: `electron/preload.ts`

- [ ] **Step 1: Add new methods to preload contextBridge**

Add these to the `electronAPI` object in `electron/preload.ts`:

```typescript
// Navigation
browserViewGoBack: (paneId: string) =>
  ipcRenderer.invoke('browser-view:go-back', paneId),
browserViewGoForward: (paneId: string) =>
  ipcRenderer.invoke('browser-view:go-forward', paneId),
browserViewReload: (paneId: string) =>
  ipcRenderer.invoke('browser-view:reload', paneId),
browserViewStop: (paneId: string) =>
  ipcRenderer.invoke('browser-view:stop', paneId),

// Window operations
browserViewOpenMiniWindow: (url: string) =>
  ipcRenderer.invoke('browser-view:open-mini-window', url),
destroyBrowserView: (paneId: string) =>
  ipcRenderer.invoke('browser-view:destroy', paneId),
browserViewMoveToTab: (paneId: string) =>
  ipcRenderer.invoke('browser-view:move-to-tab', paneId),

// State subscription (Electron → SPA)
onBrowserViewStateUpdate: (
  callback: (paneId: string, state: unknown) => void,
) => {
  const handler = (_event: unknown, paneId: string, state: unknown) =>
    callback(paneId, state)
  ipcRenderer.on('browser-view:state-update', handler)
  return () => {
    ipcRenderer.removeListener('browser-view:state-update', handler)
  }
},

// Open in tab (from mini browser or WebContentsView link click)
onBrowserViewOpenInTab: (callback: (url: string) => void) => {
  const handler = (_event: unknown, url: string) => callback(url)
  ipcRenderer.on('browser-view:open-in-tab', handler)
  return () => {
    ipcRenderer.removeListener('browser-view:open-in-tab', handler)
  }
},
```

- [ ] **Step 2: Update TypeScript declarations**

Find the existing `electronAPI` type declaration (likely in `spa/src/types/electron.d.ts` or `spa/src/vite-env.d.ts`) and add the new methods. If there's no explicit type file, the SPA code uses `window.electronAPI?.` optional chaining pattern — verify that the new methods work with this pattern. The key is that each new method matches the preload's `contextBridge.exposeInMainWorld` definition exactly.

- [ ] **Step 3: Commit**

```bash
git add electron/preload.ts
git commit -m "feat(electron): extend preload API with navigation, state-update, mini-window IPC"
```

---

## Task 9: BrowserPane Integration

**Files:**
- Modify: `spa/src/components/BrowserPane.tsx`
- Modify: `spa/src/lib/register-panes.tsx`

- [ ] **Step 1: Update BrowserPane.tsx**

Rewrite `BrowserPane.tsx` to include `BrowserToolbar` and adjust `ResizeObserver` to observe the content area (not the full container):

```typescript
// spa/src/components/BrowserPane.tsx
import { useEffect, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { BrowserToolbar } from './BrowserToolbar'
import { useBrowserViewState } from '../hooks/useBrowserViewState'

interface Props {
  paneId: string
  url: string
}

export function BrowserPane({ paneId, url }: Props) {
  const { t } = useTranslation()
  const contentRef = useRef<HTMLDivElement>(null)
  const mountedRef = useRef(false)
  const state = useBrowserViewState(paneId)

  // Display URL: prefer live state, fallback to initial url prop
  const currentUrl = state.url || url

  // Open/close lifecycle
  useEffect(() => {
    if (!window.electronAPI) return
    window.electronAPI.openBrowserView(url, paneId)
    mountedRef.current = true
    return () => {
      window.electronAPI.closeBrowserView(paneId)
      mountedRef.current = false
    }
  }, [url, paneId])

  // ResizeObserver on content area (below toolbar)
  useEffect(() => {
    const el = contentRef.current
    if (!el || !window.electronAPI) return

    let rafId = 0
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(rafId)
      rafId = requestAnimationFrame(() => {
        const rect = el.getBoundingClientRect()
        if (!rect.width || !rect.height) return
        window.electronAPI.resizeBrowserView(
          paneId,
          JSON.stringify({
            x: rect.x,
            y: rect.y,
            width: rect.width,
            height: rect.height,
          }),
        )
      })
    })
    observer.observe(el)

    return () => {
      cancelAnimationFrame(rafId)
      observer.disconnect()
    }
  }, [paneId])

  // Toolbar callbacks
  const handleGoBack = useCallback(() => window.electronAPI?.browserViewGoBack(paneId), [paneId])
  const handleGoForward = useCallback(() => window.electronAPI?.browserViewGoForward(paneId), [paneId])
  const handleReload = useCallback(() => window.electronAPI?.browserViewReload(paneId), [paneId])
  const handleStop = useCallback(() => window.electronAPI?.browserViewStop(paneId), [paneId])
  const handleNavigate = useCallback(
    (newUrl: string) => window.electronAPI?.navigateBrowserView(paneId, newUrl),
    [paneId],
  )
  const handleOpenExternal = useCallback(() => window.open(currentUrl, '_blank'), [currentUrl])
  const handleCopyUrl = useCallback(() => navigator.clipboard.writeText(currentUrl), [currentUrl])
  const handlePopOut = useCallback(
    () => window.electronAPI?.browserViewOpenMiniWindow(currentUrl),
    [currentUrl],
  )

  // SPA fallback
  if (!window.electronAPI) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground">
        {t('browser.requires_app')}
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full" data-browser-pane={paneId}>
      <BrowserToolbar
        url={currentUrl}
        title={state.title}
        canGoBack={state.canGoBack}
        canGoForward={state.canGoForward}
        isLoading={state.isLoading}
        context="tab"
        onGoBack={handleGoBack}
        onGoForward={handleGoForward}
        onReload={handleReload}
        onStop={handleStop}
        onNavigate={handleNavigate}
        onOpenExternal={handleOpenExternal}
        onCopyUrl={handleCopyUrl}
        onPopOut={handlePopOut}
      />
      {/* Content area: WebContentsView overlays this div */}
      <div ref={contentRef} className="flex-1" />
    </div>
  )
}
```

- [ ] **Step 2: Verify register-panes.tsx doesn't need changes**

The browser pane registration in `register-panes.tsx` passes `paneId` and `url` props, which matches the updated `BrowserPane` interface. No changes needed.

- [ ] **Step 3: Run tests**

Run: `cd spa && npx vitest run`
Expected: All existing tests pass (BrowserPane is Electron-dependent, may not have unit tests)

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/BrowserPane.tsx
git commit -m "feat(spa): integrate BrowserToolbar into BrowserPane with ResizeObserver adjustment"
```

---

## Task 10: useTerminal linkHandler + TerminalView Wiring

**Files:**
- Modify: `spa/src/hooks/useTerminal.ts`
- Modify: `spa/src/components/TerminalView.tsx`

- [ ] **Step 1: Update useTerminal to accept linkHandler**

Change the function signature and `WebLinksAddon` instantiation:

```typescript
// spa/src/hooks/useTerminal.ts — changes only

export interface UseTerminalOptions {
  linkHandler?: (event: MouseEvent, uri: string) => void
}

export function useTerminal(options: UseTerminalOptions = {}): UseTerminalResult {
  // ... existing code ...

  useEffect(() => {
    // ... existing terminal creation ...

    // Replace: try { term.loadAddon(new WebLinksAddon()) } catch { /* non-critical */ }
    // With:
    try {
      const handler = options.linkHandler
      term.loadAddon(handler ? new WebLinksAddon(handler) : new WebLinksAddon())
    } catch { /* non-critical */ }

    // ... rest of effect ...
  }, []) // options.linkHandler is stable (created via useCallback in parent)
```

- [ ] **Step 2: Update TerminalView to create and pass linkHandler**

In `spa/src/components/TerminalView.tsx`, add linkHandler creation:

```typescript
import { useMemo } from 'react'
import { createLinkHandler } from '../lib/link-handler'
import { getPlatformCapabilities } from '../lib/platform'
import { useTabWorkspaceActions } from '../hooks/useTabWorkspaceActions'

// Inside the component:
const caps = useMemo(() => getPlatformCapabilities(), [])
const { openBrowserTab } = useTabWorkspaceActions()

const linkHandler = useMemo(
  () =>
    createLinkHandler({
      isElectron: caps.isElectron,
      openBrowserTab,
      openMiniWindow: (url) => window.electronAPI?.browserViewOpenMiniWindow(url),
    }),
  [caps.isElectron, openBrowserTab],
)

// Pass to useTerminal:
const { termRef, fitAddonRef, containerRef } = useTerminal({ linkHandler })
```

- [ ] **Step 3: Run tests**

Run: `cd spa && npx vitest run`
Expected: All pass. Existing useTerminal tests may need minor adjustments if they test the hook signature.

- [ ] **Step 4: Commit**

```bash
git add spa/src/hooks/useTerminal.ts spa/src/components/TerminalView.tsx
git commit -m "feat(spa): wire link handler into useTerminal and TerminalView"
```

---

## Task 11: TabBar ICON_MAP + openBrowserTab Action

**Files:**
- Modify: `spa/src/components/TabBar.tsx`
- Modify: `spa/src/hooks/useTabWorkspaceActions.ts`

- [ ] **Step 1: Add Globe to TabBar ICON_MAP**

In `spa/src/components/TabBar.tsx`, add import and map entry:

```typescript
import { Globe } from '@phosphor-icons/react'

// In ICON_MAP:
const ICON_MAP: Record<string, React.ComponentType<{ size: number; className?: string }>> = {
  Plus,
  TerminalWindow: TerminalWindowFill,
  ChatCircleDots,
  House,
  ClockCounterClockwise,
  GearSix,
  SmileySad,
  Globe,  // ← add
}
```

- [ ] **Step 2: Add openBrowserTab to useTabWorkspaceActions**

In `spa/src/hooks/useTabWorkspaceActions.ts`, add:

```typescript
import { createTab } from '../lib/pane-tree'  // or wherever createTab is

const openBrowserTab = useCallback(
  (url: string) => {
    const tab = createTab({ kind: 'browser', url })
    addTab(tab)
    setActiveTab(tab.id)
    if (activeWorkspaceId) {
      useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, tab.id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, tab.id)
    }
  },
  [addTab, setActiveTab, activeWorkspaceId],
)

// Include in the return value:
return { ..., openBrowserTab }
```

- [ ] **Step 3: Wire onBrowserViewOpenInTab in App.tsx**

In `spa/src/App.tsx`, add an effect to listen for `browser-view:open-in-tab` events from mini browser windows or WebContentsView link clicks:

```typescript
// In App component, near other electronAPI effects:
useEffect(() => {
  if (!window.electronAPI?.onBrowserViewOpenInTab) return
  return window.electronAPI.onBrowserViewOpenInTab((url: string) => {
    // Reuse the same workspace-aware logic
    const tab = createTab({ kind: 'browser', url })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().setActiveTab(tab.id)
    const wsId = useWorkspaceStore.getState().activeWorkspaceId
    if (wsId) {
      useWorkspaceStore.getState().addTabToWorkspace(wsId, tab.id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tab.id)
    }
  })
}, [])
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/TabBar.tsx spa/src/hooks/useTabWorkspaceActions.ts spa/src/App.tsx
git commit -m "feat(spa): add Globe to ICON_MAP, openBrowserTab action, open-in-tab listener"
```

---

## Task 12: mini-browser-window.ts

**Files:**
- Create: `electron/mini-browser-window.ts`

- [ ] **Step 1: Create MiniWindowManager**

```typescript
// electron/mini-browser-window.ts
import { BrowserWindow } from 'electron'
import { join } from 'node:path'
import type { BrowserViewManager } from './browser-view-manager'
import type { WindowManager } from './window-manager'

interface MiniWindowEntry {
  window: BrowserWindow
  paneId: string
  parentWindow: BrowserWindow
}

export class MiniWindowManager {
  private entries = new Map<string, MiniWindowEntry>()
  private nextId = 0

  constructor(
    private viewManager: BrowserViewManager,
    private windowManager: WindowManager,
  ) {}

  open(parentWindow: BrowserWindow, url: string): void {
    const paneId = `mini-${++this.nextId}`

    const win = new BrowserWindow({
      width: 800,
      height: 600,
      titleBarStyle: 'hiddenInset',
      trafficLightPosition: { x: 12, y: 12 },
      webPreferences: {
        contextIsolation: true,
        sandbox: true,
        nodeIntegration: false,
        preload: join(__dirname, '../preload/index.js'),
      },
    })

    this.entries.set(paneId, { window: win, paneId, parentWindow })

    // Load mini browser SPA entry
    const query = `?paneId=${encodeURIComponent(paneId)}`
    const devServer = WindowManager.DEV_SERVER
    fetch(devServer, { signal: AbortSignal.timeout(500) })
      .then(() => win.loadURL(`${devServer}/mini-browser.html${query}`))
      .catch(() => win.loadURL(`app://./mini-browser.html${query}`))

    // Once SPA is ready, open the WebContentsView
    win.webContents.once('did-finish-load', () => {
      this.viewManager.open(win, url, paneId)
    })

    // Cleanup on window close
    win.on('closed', () => {
      this.viewManager.destroy(paneId)
      this.entries.delete(paneId)
    })
  }

  moveToTab(paneId: string): void {
    const entry = this.entries.get(paneId)
    if (!entry) return

    // Get current URL from the view
    const state = this.viewManager.getCurrentState(paneId)
    const url = state?.url || ''

    // Notify parent window SPA to open new tab
    try {
      entry.parentWindow.webContents.send('browser-view:open-in-tab', url)
    } catch { /* parent window may be closed */ }

    // Close mini window (triggers 'closed' event → cleanup)
    entry.window.close()
  }

  closeAll(): void {
    for (const entry of this.entries.values()) {
      entry.window.close()
    }
  }
}
```

- [ ] **Step 2: Commit**

```bash
git add electron/mini-browser-window.ts
git commit -m "feat(electron): add MiniWindowManager for standalone browser windows"
```

---

## Task 13: Mini Browser SPA Entry

**Files:**
- Create: `spa/mini-browser.html`
- Create: `spa/src/mini-browser.tsx`

- [ ] **Step 1: Create mini-browser.html**

```html
<!-- spa/mini-browser.html -->
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Mini Browser</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/mini-browser.tsx"></script>
  </body>
</html>
```

- [ ] **Step 2: Create mini-browser.tsx**

```typescript
// spa/src/mini-browser.tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { MiniBrowserApp } from './components/MiniBrowserApp'

const params = new URLSearchParams(window.location.search)
const paneId = params.get('paneId') || ''

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MiniBrowserApp paneId={paneId} />
  </StrictMode>,
)
```

- [ ] **Step 3: Create MiniBrowserApp component**

```typescript
// spa/src/components/MiniBrowserApp.tsx
import { useEffect, useRef, useCallback } from 'react'
import { BrowserToolbar } from './BrowserToolbar'
import { useBrowserViewState } from '../hooks/useBrowserViewState'

interface Props {
  paneId: string
}

export function MiniBrowserApp({ paneId }: Props) {
  const contentRef = useRef<HTMLDivElement>(null)
  const state = useBrowserViewState(paneId)

  // ResizeObserver for content area
  useEffect(() => {
    const el = contentRef.current
    if (!el || !window.electronAPI) return

    let rafId = 0
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(rafId)
      rafId = requestAnimationFrame(() => {
        const rect = el.getBoundingClientRect()
        if (!rect.width || !rect.height) return
        window.electronAPI.resizeBrowserView(
          paneId,
          JSON.stringify({
            x: rect.x,
            y: rect.y,
            width: rect.width,
            height: rect.height,
          }),
        )
      })
    })
    observer.observe(el)

    return () => {
      cancelAnimationFrame(rafId)
      observer.disconnect()
    }
  }, [paneId])

  const handleGoBack = useCallback(() => window.electronAPI?.browserViewGoBack(paneId), [paneId])
  const handleGoForward = useCallback(() => window.electronAPI?.browserViewGoForward(paneId), [paneId])
  const handleReload = useCallback(() => window.electronAPI?.browserViewReload(paneId), [paneId])
  const handleStop = useCallback(() => window.electronAPI?.browserViewStop(paneId), [paneId])
  const handleNavigate = useCallback(
    (url: string) => window.electronAPI?.navigateBrowserView(paneId, url),
    [paneId],
  )
  const handleOpenExternal = useCallback(() => window.open(state.url, '_blank'), [state.url])
  const handleCopyUrl = useCallback(() => navigator.clipboard.writeText(state.url), [state.url])
  const handleMoveToTab = useCallback(
    () => window.electronAPI?.browserViewMoveToTab(paneId),
    [paneId],
  )

  return (
    <div className="flex flex-col h-screen bg-background text-foreground">
      {/* Electron title bar drag region */}
      <div
        className="h-10 flex items-end"
        style={{ WebkitAppRegion: 'drag' } as React.CSSProperties}
      />
      <BrowserToolbar
        url={state.url}
        title={state.title}
        canGoBack={state.canGoBack}
        canGoForward={state.canGoForward}
        isLoading={state.isLoading}
        context="mini-window"
        onGoBack={handleGoBack}
        onGoForward={handleGoForward}
        onReload={handleReload}
        onStop={handleStop}
        onNavigate={handleNavigate}
        onOpenExternal={handleOpenExternal}
        onCopyUrl={handleCopyUrl}
        onMoveToTab={handleMoveToTab}
      />
      <div ref={contentRef} className="flex-1" />
    </div>
  )
}
```

- [ ] **Step 4: Commit**

```bash
git add spa/mini-browser.html spa/src/mini-browser.tsx spa/src/components/MiniBrowserApp.tsx
git commit -m "feat(spa): add mini browser SPA entry point and MiniBrowserApp component"
```

---

## Task 14: Build Config

**Files:**
- Modify: `electron.vite.config.ts`

- [ ] **Step 1: Add preload entry for browser-view-preload**

In `electron.vite.config.ts`, change the preload section from single entry to multi-entry:

```typescript
preload: {
  build: {
    rollupOptions: {
      input: {
        index: resolve(__dirname, 'electron/preload.ts'),
        browserViewPreload: resolve(__dirname, 'electron/browser-view-preload.ts'),
      },
      output: { entryFileNames: '[name].js' },
    },
    outDir: 'out/preload',
  },
  // ... existing plugins
},
```

- [ ] **Step 2: Add renderer entry for mini-browser**

In the renderer section, change from single entry to multi-entry:

```typescript
renderer: {
  root: 'spa',
  build: {
    rollupOptions: {
      input: {
        index: resolve(__dirname, 'spa/index.html'),
        'mini-browser': resolve(__dirname, 'spa/mini-browser.html'),
      },
    },
    outDir: 'out/renderer',
  },
  // ... existing plugins
},
```

- [ ] **Step 3: Verify build**

Run: `cd spa && pnpm run build` (or the electron-vite build command)
Expected: Build succeeds, `out/preload/browserViewPreload.js` and `out/renderer/mini-browser.html` exist

- [ ] **Step 4: Commit**

```bash
git add electron.vite.config.ts
git commit -m "build: add browser-view-preload and mini-browser entries to electron-vite config"
```

---

## Task 15: i18n Keys

**Files:**
- Modify: `spa/src/locales/en.json` (and `zh-TW.json` if exists)

- [ ] **Step 1: Add translation keys**

Add these keys to the locale files:

```json
{
  "browser": {
    "open_external": "Open in browser",
    "copy_url": "Copy URL",
    "pop_out": "Open in mini browser",
    "move_to_tab": "Open in main window"
  }
}
```

And for zh-TW:

```json
{
  "browser": {
    "open_external": "在瀏覽器中開啟",
    "copy_url": "複製網址",
    "pop_out": "在 Mini Browser 開啟",
    "move_to_tab": "在主視窗重新開啟此網址"
  }
}
```

- [ ] **Step 2: Commit**

```bash
git add spa/src/locales/
git commit -m "feat(spa): add i18n keys for browser toolbar menu"
```

---

## Task 16: Browser Tab Close → Destroy Path

**Files:**
- Modify: `spa/src/hooks/useTabWorkspaceActions.ts` (or wherever `closeTab` flow is handled)
- Modify: `spa/src/components/BrowserPane.tsx`

The spec requires that when a user **actively closes** a browser tab, it calls `destroyBrowserView()` (not `closeBrowserView()`). The current flow: user closes tab → tab removed from store → BrowserPane unmounts → `closeBrowserView()` → `background()`. We need to intercept before unmount.

- [ ] **Step 1: Add destroy call before tab removal**

In the tab close flow (in `useTabWorkspaceActions.ts` or `handleContextAction`), detect if the closing tab contains a browser pane and call `destroyBrowserView` before removing the tab:

```typescript
// In the close tab handler, before calling removeTab/closeTab:
const tab = useTabStore.getState().tabs[tabId]
if (tab) {
  const primary = getPrimaryPane(tab.layout)
  if (primary.content.kind === 'browser') {
    window.electronAPI?.destroyBrowserView(primary.id)
  }
}
// Then proceed with normal closeTab
```

This way, `destroy()` runs before the BrowserPane unmounts. The subsequent `closeBrowserView()` from unmount will be a no-op (view already removed from manager).

- [ ] **Step 2: Run tests**

Run: `cd spa && npx vitest run`
Expected: All pass

- [ ] **Step 3: Commit**

```bash
git add spa/src/hooks/useTabWorkspaceActions.ts
git commit -m "feat(spa): call destroyBrowserView on active browser tab close"
```

**Note:** The recently-closed store (snapshot + restore UI) is deferred — the destroy path is the critical piece for correct cleanup. Recently-closed can be added as a follow-up with a keyboard shortcut (Cmd+Shift+T) trigger.

---

## Task 17: Integration Verification

- [ ] **Step 1: Run all tests**

Run: `cd spa && npx vitest run`
Expected: All tests pass

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`
Expected: No errors

- [ ] **Step 3: Run build**

Run: `cd spa && pnpm run build`
Expected: Build succeeds

- [ ] **Step 4: Manual smoke test checklist (Electron)**

- Open app → create browser tab → toolbar visible with ← → ↻ URL ⋯
- Navigate to a URL → URL bar updates, back button enables
- Click back → navigates back, forward enables
- Click ↻ → page reloads, button switches to ✕ during load
- Edit URL bar → Enter → navigates to new URL
- ⋯ menu → "Open in browser" → opens system browser
- ⋯ menu → "Copy URL" → clipboard has URL
- ⋯ menu → "Open in mini browser" → new window with same toolbar
- Mini browser → ⋯ → "Open in main window" → new tab in main, mini closes
- Terminal → click link → opens new browser tab
- Terminal → shift+click link → opens mini browser window
- Close browser tab → close works properly

- [ ] **Step 5: Final commit if any fixes needed**

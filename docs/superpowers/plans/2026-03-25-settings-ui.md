# Settings UI 重構 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the SettingsPanel overlay with a VSCode-style Settings pane (sidebar + content) rendered as a singleton tab.

**Architecture:** SettingsPage is a PaneContent renderer (like DashboardPage, HistoryPage). It reads the URL section segment internally and renders the appropriate settings section. Route sync is extended to handle `/settings/:section` without overwriting the section path.

**Tech Stack:** React 19, Zustand 5, wouter 3, Tailwind 4, Vitest, Phosphor Icons

**Spec:** `docs/superpowers/specs/2026-03-25-settings-ui-design.md`

**Test command:** `cd spa && npx vitest run`
**Lint command:** `cd spa && pnpm run lint`

---

### Task 1: Route — extend parseRoute for `/settings/:section`

**Files:**
- Modify: `spa/src/lib/route-utils.ts:18-21`
- Test: `spa/src/lib/route-utils.test.ts`

- [ ] **Step 1: Write failing tests**

Add to the `parseRoute` describe block in `route-utils.test.ts`:

```ts
it('parses /settings/appearance as global settings', () => {
  expect(parseRoute('/settings/appearance')).toEqual({ kind: 'settings', scope: 'global' })
})

it('parses /settings/terminal as global settings', () => {
  expect(parseRoute('/settings/terminal')).toEqual({ kind: 'settings', scope: 'global' })
})
```

Note: `/settings` exact match test already exists — don't duplicate.

- [ ] **Step 2: Run tests to verify failure**

Run: `cd spa && npx vitest run src/lib/route-utils.test.ts`
Expected: 2 new tests FAIL (`/settings/appearance` and `/settings/terminal` return `null`)

- [ ] **Step 3: Update parseRoute**

In `route-utils.ts`, replace the exact `/settings` match:

```ts
// Before:
if (path === '/settings') return { kind: 'settings', scope: 'global' }

// After:
if (path === '/settings' || path.startsWith('/settings/')) return { kind: 'settings', scope: 'global' }
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/lib/route-utils.test.ts`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/route-utils.ts spa/src/lib/route-utils.test.ts
git commit -m "feat(route): parseRoute accepts /settings/:section"
```

---

### Task 2: Route — useRouteSync startsWith prefix check

**Files:**
- Modify: `spa/src/hooks/useRouteSync.ts:34-37`
- Test: `spa/src/hooks/useRouteSync.test.ts`

- [ ] **Step 1: Write failing test**

Add to `useRouteSync.test.ts`:

```ts
it('does not overwrite /settings/terminal back to /settings', () => {
  const tab = makeTab('abc123', 'dashboard') // reuse helper
  // Create a settings tab manually
  const settingsTab: Tab = {
    id: 'set001',
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: 'pane-set001', content: { kind: 'settings', scope: 'global' } } },
  }
  resetStore({
    tabs: { set001: settingsTab },
    tabOrder: ['set001'],
    activeTabId: 'set001',
  })

  const mem = memoryLocation({ path: '/settings/terminal', record: true })
  renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

  // Tab→URL should NOT replace /settings/terminal with /settings
  // The last entry should still be /settings/terminal
  const lastPath = mem.history[mem.history.length - 1]
  expect(lastPath).toBe('/settings/terminal')
})
```

Note: Replace the `makeTab` helper in the test file to support `'settings'`:

```ts
function makeTab(id: string, contentKind: 'session' | 'dashboard' | 'history' | 'settings', mode?: 'terminal' | 'stream'): Tab {
  const content = contentKind === 'session'
    ? { kind: 'session' as const, sessionCode: 'test', mode: mode ?? 'terminal' as const }
    : contentKind === 'settings'
      ? { kind: 'settings' as const, scope: 'global' as const }
      : { kind: contentKind as 'dashboard' | 'history' }
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: `pane-${id}`, content } },
  }
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd spa && npx vitest run src/hooks/useRouteSync.test.ts`
Expected: New test FAILS (URL gets replaced to `/settings`)

- [ ] **Step 3: Update Tab→URL sync with startsWith check**

In `useRouteSync.ts`, change the Tab→URL effect (line 34-37):

```ts
// Before:
useEffect(() => {
  if (!hydrated) return
  if (activeUrl && location !== activeUrl) setLocation(activeUrl, { replace: true })
}, [activeUrl, hydrated])

// After:
// Note: location is intentionally excluded from deps to prevent loops.
// The startsWith check prevents overwriting sub-path sections (e.g. /settings/terminal).
useEffect(() => {
  if (!hydrated) return
  if (activeUrl && location !== activeUrl && !location.startsWith(activeUrl + '/')) {
    setLocation(activeUrl, { replace: true })
  }
}, [activeUrl, hydrated])
```

- [ ] **Step 4: Update URL→Tab "already in sync" guard**

In the same file, update the URL→Tab effect's early-return check (line 64-65):

```ts
// Before:
if (currentUrl === location) return // already in sync

// After:
if (currentUrl === location || location.startsWith(currentUrl + '/')) return // already in sync (includes sub-paths)
```

This prevents `openSingletonTab` from firing on every section navigation within Settings, which would flood the history store with duplicate visits.

- [ ] **Step 5: Run tests**

Run: `cd spa && npx vitest run src/hooks/useRouteSync.test.ts`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/hooks/useRouteSync.ts spa/src/hooks/useRouteSync.test.ts
git commit -m "feat(route): Tab→URL sync preserves sub-path sections"
```

---

### Task 3: SettingItem — generic setting wrapper component

**Files:**
- Create: `spa/src/components/settings/SettingItem.tsx`
- Test: `spa/src/components/settings/SettingItem.test.tsx`

- [ ] **Step 1: Write failing test**

Create `spa/src/components/settings/SettingItem.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SettingItem } from './SettingItem'

describe('SettingItem', () => {
  it('renders label and children', () => {
    render(
      <SettingItem label="My Setting">
        <input data-testid="ctrl" />
      </SettingItem>,
    )
    expect(screen.getByText('My Setting')).toBeTruthy()
    expect(screen.getByTestId('ctrl')).toBeTruthy()
  })

  it('renders description when provided', () => {
    render(
      <SettingItem label="X" description="Some help text">
        <span />
      </SettingItem>,
    )
    expect(screen.getByText('Some help text')).toBeTruthy()
  })

  it('applies disabled styling', () => {
    const { container } = render(
      <SettingItem label="X" disabled>
        <span />
      </SettingItem>,
    )
    const wrapper = container.firstChild as HTMLElement
    expect(wrapper.className).toContain('opacity-50')
  })
})
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd spa && npx vitest run src/components/settings/SettingItem.test.tsx`
Expected: FAIL (file not found)

- [ ] **Step 3: Implement SettingItem**

Create `spa/src/components/settings/SettingItem.tsx`:

```tsx
interface SettingItemProps {
  label: string
  description?: string
  disabled?: boolean
  children: React.ReactNode
}

export function SettingItem({ label, description, disabled, children }: SettingItemProps) {
  return (
    <div className={`flex items-center justify-between py-3 ${disabled ? 'opacity-50 pointer-events-none' : ''}`}>
      <div className="flex flex-col gap-0.5 mr-4">
        <span className="text-sm text-gray-300">{label}</span>
        {description && <span className="text-xs text-gray-500">{description}</span>}
      </div>
      <div className="flex-shrink-0">{children}</div>
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/settings/SettingItem.test.tsx`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/SettingItem.tsx spa/src/components/settings/SettingItem.test.tsx
git commit -m "feat(settings): add SettingItem wrapper component"
```

---

### Task 4: SettingsSidebar component

**Files:**
- Create: `spa/src/components/settings/SettingsSidebar.tsx`
- Test: `spa/src/components/settings/SettingsSidebar.test.tsx`

- [ ] **Step 1: Write failing test**

Create `spa/src/components/settings/SettingsSidebar.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SettingsSidebar } from './SettingsSidebar'

describe('SettingsSidebar', () => {
  it('renders all section items', () => {
    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    expect(screen.getByText('Appearance')).toBeTruthy()
    expect(screen.getByText('Terminal')).toBeTruthy()
    expect(screen.getByText('Workspace')).toBeTruthy()
    expect(screen.getByText('Sync')).toBeTruthy()
  })

  it('highlights active section', () => {
    render(<SettingsSidebar activeSection="terminal" onSelectSection={vi.fn()} />)
    const terminalItem = screen.getByText('Terminal').closest('[data-section]')
    expect(terminalItem?.getAttribute('data-active')).toBe('true')
  })

  it('calls onSelectSection for enabled items', () => {
    const onSelect = vi.fn()
    render(<SettingsSidebar activeSection="appearance" onSelectSection={onSelect} />)
    fireEvent.click(screen.getByText('Terminal'))
    expect(onSelect).toHaveBeenCalledWith('terminal')
  })

  it('does not call onSelectSection for reserved items', () => {
    const onSelect = vi.fn()
    render(<SettingsSidebar activeSection="appearance" onSelectSection={onSelect} />)
    fireEvent.click(screen.getByText('Workspace'))
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('shows coming soon badge on reserved items', () => {
    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    const badges = screen.getAllByText('coming soon')
    expect(badges.length).toBe(2) // Workspace + Sync
  })
})
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd spa && npx vitest run src/components/settings/SettingsSidebar.test.tsx`
Expected: FAIL

- [ ] **Step 3: Implement SettingsSidebar**

Create `spa/src/components/settings/SettingsSidebar.tsx`:

```tsx
export type SettingsSection = 'appearance' | 'terminal'

interface SidebarItem {
  id: string
  label: string
  enabled: boolean
}

const SECTIONS: SidebarItem[] = [
  { id: 'appearance', label: 'Appearance', enabled: true },
  { id: 'terminal', label: 'Terminal', enabled: true },
  { id: 'workspace', label: 'Workspace', enabled: false },
  { id: 'sync', label: 'Sync', enabled: false },
]

interface Props {
  activeSection: string
  onSelectSection: (section: SettingsSection) => void
}

export function SettingsSidebar({ activeSection, onSelectSection }: Props) {
  const enabledIds = SECTIONS.filter((s) => s.enabled).map((s) => s.id)
  const reservedStart = SECTIONS.findIndex((s) => !s.enabled)

  return (
    <div className="w-48 border-r border-gray-800 bg-[#0a0a1a] py-3 flex-shrink-0">
      <div className="px-4 mb-2 text-[10px] text-gray-600 uppercase tracking-wider">Settings</div>
      {SECTIONS.map((item, i) => {
        const isActive = item.id === activeSection
        const showDivider = i === reservedStart && reservedStart > 0

        return (
          <div key={item.id}>
            {showDivider && <div className="mx-3 my-2 border-t border-gray-800" />}
            <button
              data-section={item.id}
              data-active={isActive ? 'true' : undefined}
              onClick={() => {
                if (item.enabled) onSelectSection(item.id as SettingsSection)
              }}
              className={`w-full text-left px-4 py-2 text-sm flex items-center transition-colors ${
                !item.enabled
                  ? 'text-gray-600 cursor-not-allowed'
                  : isActive
                    ? 'bg-[#1e1e3e] text-gray-200 border-l-2 border-[#7a6aaa]'
                    : 'text-gray-400 cursor-pointer hover:bg-white/5'
              }`}
            >
              <span>{item.label}</span>
              {!item.enabled && (
                <span className="text-[10px] text-gray-600 ml-auto">coming soon</span>
              )}
            </button>
          </div>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/settings/SettingsSidebar.test.tsx`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/SettingsSidebar.tsx spa/src/components/settings/SettingsSidebar.test.tsx
git commit -m "feat(settings): add SettingsSidebar component"
```

---

### Task 5: AppearanceSection component

**Files:**
- Create: `spa/src/components/settings/AppearanceSection.tsx`
- Test: `spa/src/components/settings/AppearanceSection.test.tsx`

- [ ] **Step 1: Write failing test**

Create `spa/src/components/settings/AppearanceSection.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { AppearanceSection } from './AppearanceSection'

describe('AppearanceSection', () => {
  it('renders section title', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Appearance')).toBeTruthy()
  })

  it('renders theme setting as disabled', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Theme')).toBeTruthy()
    expect(screen.getByText('Dark')).toBeTruthy()
    expect(screen.getByText('Light')).toBeTruthy()
  })

  it('renders language setting as disabled', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Language')).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd spa && npx vitest run src/components/settings/AppearanceSection.test.tsx`
Expected: FAIL

- [ ] **Step 3: Implement AppearanceSection**

Create `spa/src/components/settings/AppearanceSection.tsx`:

```tsx
import { SettingItem } from './SettingItem'

export function AppearanceSection() {
  return (
    <div>
      <h2 className="text-lg text-gray-200">Appearance</h2>
      <p className="text-xs text-gray-500 mb-6">Visual preferences for the application</p>

      <SettingItem label="Theme" description="Application color scheme" disabled>
        <div className="flex gap-2">
          <button className="px-4 py-1.5 rounded-md border text-xs bg-[#1e1e3e] border-[#7a6aaa] text-gray-200">
            Dark
          </button>
          <button className="px-4 py-1.5 rounded-md border text-xs bg-transparent border-[#404040] text-gray-500">
            Light
          </button>
        </div>
      </SettingItem>

      <SettingItem label="Language" description="Interface language" disabled>
        <select
          disabled
          className="bg-[#2a2a2a] border border-[#404040] rounded-md text-gray-400 text-xs px-3 py-1.5 w-40"
          defaultValue="zh-TW"
        >
          <option value="zh-TW">繁體中文</option>
          <option value="en">English</option>
        </select>
      </SettingItem>
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/settings/AppearanceSection.test.tsx`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/AppearanceSection.tsx spa/src/components/settings/AppearanceSection.test.tsx
git commit -m "feat(settings): add AppearanceSection with disabled theme/language"
```

---

### Task 6: TerminalSection component

**Files:**
- Create: `spa/src/components/settings/TerminalSection.tsx`
- Test: `spa/src/components/settings/TerminalSection.test.tsx`

- [ ] **Step 1: Write failing tests**

Create `spa/src/components/settings/TerminalSection.test.tsx`:

```tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TerminalSection } from './TerminalSection'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

describe('TerminalSection', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      terminalRenderer: 'webgl',
      keepAliveCount: 0,
      keepAlivePinned: false,
      terminalRevealDelay: 300,
      terminalSettingsVersion: 0,
    })
  })

  it('renders section title', () => {
    render(<TerminalSection />)
    expect(screen.getByText('Terminal')).toBeTruthy()
  })

  it('toggles renderer and bumps version', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByText('DOM')) // RENDERER_LABELS maps 'dom' → 'DOM'
    const state = useUISettingsStore.getState()
    expect(state.terminalRenderer).toBe('dom')
    expect(state.terminalSettingsVersion).toBe(1)
  })

  it('updates keep-alive count', () => {
    render(<TerminalSection />)
    const input = screen.getByLabelText('Keep-alive Count')
    fireEvent.change(input, { target: { value: '3' } })
    expect(useUISettingsStore.getState().keepAliveCount).toBe(3)
  })

  it('clamps keep-alive count to 0-10', () => {
    render(<TerminalSection />)
    const input = screen.getByLabelText('Keep-alive Count')
    fireEvent.change(input, { target: { value: '15' } })
    expect(useUISettingsStore.getState().keepAliveCount).toBe(10)
  })

  it('toggles keep-alive pinned', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByLabelText('Keep-alive Pinned'))
    expect(useUISettingsStore.getState().keepAlivePinned).toBe(true)
  })

  it('updates reveal delay', () => {
    render(<TerminalSection />)
    const input = screen.getByLabelText('Reveal Delay')
    fireEvent.change(input, { target: { value: '500' } })
    expect(useUISettingsStore.getState().terminalRevealDelay).toBe(500)
  })
})
```

- [ ] **Step 2: Run tests to verify failure**

Run: `cd spa && npx vitest run src/components/settings/TerminalSection.test.tsx`
Expected: FAIL

- [ ] **Step 3: Implement TerminalSection**

Create `spa/src/components/settings/TerminalSection.tsx`:

```tsx
import { useUISettingsStore, type TerminalRenderer } from '../../stores/useUISettingsStore'
import { SettingItem } from './SettingItem'

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value))
}

export function TerminalSection() {
  const renderer = useUISettingsStore((s) => s.terminalRenderer)
  const setRenderer = useUISettingsStore((s) => s.setTerminalRenderer)
  const bumpVersion = useUISettingsStore((s) => s.bumpTerminalSettingsVersion)

  const keepAliveCount = useUISettingsStore((s) => s.keepAliveCount)
  const setKeepAliveCount = useUISettingsStore((s) => s.setKeepAliveCount)
  const keepAlivePinned = useUISettingsStore((s) => s.keepAlivePinned)
  const setKeepAlivePinned = useUISettingsStore((s) => s.setKeepAlivePinned)

  const revealDelay = useUISettingsStore((s) => s.terminalRevealDelay)
  const setRevealDelay = useUISettingsStore((s) => s.setTerminalRevealDelay)

  const handleRenderer = (r: TerminalRenderer) => {
    setRenderer(r)
    bumpVersion()
  }

  const renderers: TerminalRenderer[] = ['webgl', 'dom']
  const RENDERER_LABELS: Record<TerminalRenderer, string> = { webgl: 'WebGL', dom: 'DOM' }

  return (
    <div>
      <h2 className="text-lg text-gray-200">Terminal</h2>
      <p className="text-xs text-gray-500 mb-6">Terminal rendering and connection settings</p>

      <SettingItem label="Renderer" description="WebGL is faster but limited to ~16 instances. DOM has no limit.">
        <div className="flex">
          {renderers.map((r) => (
            <button
              key={r}
              onClick={() => handleRenderer(r)}
              className={`px-4 py-1.5 text-xs border transition-colors cursor-pointer ${
                r === renderer
                  ? 'bg-[#1e1e3e] border-[#7a6aaa] text-gray-200'
                  : 'bg-transparent border-[#404040] text-gray-500 hover:text-gray-300 hover:border-gray-600'
              } ${r === renderers[0] ? 'rounded-l-md' : ''} ${r === renderers[renderers.length - 1] ? 'rounded-r-md' : ''}`}
            >
              {RENDERER_LABELS[r]}
            </button>
          ))}
        </div>
      </SettingItem>

      <SettingItem label="Keep-alive Count" description="Number of background tabs to keep connected (0 = active only)">
        <input
          type="number"
          aria-label="Keep-alive Count"
          min={0}
          max={10}
          step={1}
          value={keepAliveCount}
          onChange={(e) => setKeepAliveCount(clamp(Number(e.target.value) || 0, 0, 10))}
          className="bg-[#2a2a2a] border border-[#404040] rounded-md text-gray-200 text-xs px-3 py-1.5 w-20 hover:border-gray-500 focus:border-[#7a6aaa] focus:outline-none"
        />
      </SettingItem>

      <SettingItem label="Keep-alive Pinned" description="Always keep pinned tabs connected">
        <button
          role="switch"
          aria-label="Keep-alive Pinned"
          aria-checked={keepAlivePinned}
          onClick={() => setKeepAlivePinned(!keepAlivePinned)}
          className={`w-9 h-5 rounded-full relative transition-all duration-150 cursor-pointer ${
            keepAlivePinned ? 'bg-[#7a6aaa]' : 'bg-gray-700'
          }`}
        >
          <span
            className={`absolute top-0.5 w-4 h-4 rounded-full transition-all duration-150 ${
              keepAlivePinned ? 'left-[18px] bg-white' : 'left-0.5 bg-gray-400'
            }`}
          />
        </button>
      </SettingItem>

      <SettingItem label="Reveal Delay" description="Delay before showing terminal content after connection (ms)">
        <input
          type="number"
          aria-label="Reveal Delay"
          min={0}
          max={2000}
          step={50}
          value={revealDelay}
          onChange={(e) => setRevealDelay(clamp(Number(e.target.value) || 0, 0, 2000))}
          className="bg-[#2a2a2a] border border-[#404040] rounded-md text-gray-200 text-xs px-3 py-1.5 w-20 hover:border-gray-500 focus:border-[#7a6aaa] focus:outline-none"
        />
      </SettingItem>
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/settings/TerminalSection.test.tsx`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/TerminalSection.tsx spa/src/components/settings/TerminalSection.test.tsx
git commit -m "feat(settings): add TerminalSection with store sync"
```

---

### Task 7: SettingsPage — rewrite stub with sidebar + content

**Files:**
- Modify: `spa/src/components/SettingsPage.tsx`
- Test: `spa/src/components/SettingsPage.test.tsx` (create new)

- [ ] **Step 1: Write failing tests**

Create `spa/src/components/SettingsPage.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { SettingsPage } from './SettingsPage'
import type { Pane } from '../types/tab'

const settingsPane: Pane = {
  id: 'pane-set',
  content: { kind: 'settings', scope: 'global' },
}

function createWrapper(mem: ReturnType<typeof memoryLocation>) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return createElement(Router, { hook: mem.hook, children })
  }
}

describe('SettingsPage', () => {
  it('renders sidebar and default appearance section', () => {
    const mem = memoryLocation({ path: '/settings', record: true })
    render(<SettingsPage pane={settingsPane} isActive />, { wrapper: createWrapper(mem) })
    expect(screen.getByText('Appearance')).toBeTruthy()
    expect(screen.getByText('Terminal')).toBeTruthy()
    // Default section content should be Appearance
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
  })

  it('switches to terminal section on sidebar click', () => {
    const mem = memoryLocation({ path: '/settings', record: true })
    render(<SettingsPage pane={settingsPane} isActive />, { wrapper: createWrapper(mem) })
    fireEvent.click(screen.getByText('Terminal'))
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
  })

  it('reads section from URL', () => {
    const mem = memoryLocation({ path: '/settings/terminal', record: true })
    render(<SettingsPage pane={settingsPane} isActive />, { wrapper: createWrapper(mem) })
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
  })

  it('updates URL on section switch', () => {
    const mem = memoryLocation({ path: '/settings', record: true })
    render(<SettingsPage pane={settingsPane} isActive />, { wrapper: createWrapper(mem) })
    fireEvent.click(screen.getByText('Terminal'))
    expect(mem.history).toContain('/settings/terminal')
  })
})
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd spa && npx vitest run src/components/SettingsPage.test.tsx`
Expected: FAIL

- [ ] **Step 3: Rewrite SettingsPage**

Replace `spa/src/components/SettingsPage.tsx`:

```tsx
import { useLocation } from 'wouter'
import type { PaneRendererProps } from '../lib/pane-registry'
import { SettingsSidebar, type SettingsSection } from './settings/SettingsSidebar'
import { AppearanceSection } from './settings/AppearanceSection'
import { TerminalSection } from './settings/TerminalSection'

const VALID_SECTIONS: SettingsSection[] = ['appearance', 'terminal']

function parseSectionFromPath(path: string): SettingsSection {
  const segment = path.replace(/^\/settings\/?/, '').split('/')[0]
  if (VALID_SECTIONS.includes(segment as SettingsSection)) return segment as SettingsSection
  return 'appearance'
}

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export function SettingsPage(_props: PaneRendererProps) {
  const [location, setLocation] = useLocation()
  const activeSection = parseSectionFromPath(location)

  const handleSelectSection = (section: SettingsSection) => {
    setLocation(`/settings/${section}`)
  }

  return (
    <div className="flex h-full">
      <SettingsSidebar activeSection={activeSection} onSelectSection={handleSelectSection} />
      <div className="flex-1 overflow-y-auto p-6">
        {activeSection === 'appearance' && <AppearanceSection />}
        {activeSection === 'terminal' && <TerminalSection />}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/SettingsPage.test.tsx`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/SettingsPage.tsx spa/src/components/SettingsPage.test.tsx
git commit -m "feat(settings): rewrite SettingsPage with sidebar + section routing"
```

---

### Task 8: App.tsx — wire up settings tab, remove SettingsPanel

**Files:**
- Modify: `spa/src/App.tsx`
- Delete: `spa/src/components/SettingsPanel.tsx`
- Delete: `spa/src/components/SettingsPanel.test.tsx`

- [ ] **Step 1: Modify App.tsx**

In `spa/src/App.tsx`, make these changes:

1. Remove `import SettingsPanel from './components/SettingsPanel'` (line 8)
2. Remove `const [settingsOpen, setSettingsOpen] = useState(false)` (line 24)
3. Change `onOpenSettings` handler (line 118):
   ```tsx
   // Before:
   onOpenSettings={() => setSettingsOpen(true)}
   // After:
   onOpenSettings={() => useTabStore.getState().openSingletonTab({ kind: 'settings', scope: 'global' })}
   ```
4. Remove the SettingsPanel JSX block (lines 144-149):
   ```tsx
   // Delete:
   {settingsOpen && (
     <SettingsPanel
       daemonBase={daemonBase}
       onClose={() => setSettingsOpen(false)}
     />
   )}
   ```
5. Update React import — `useState` is no longer used:
   ```ts
   // Before:
   import { useEffect, useState } from 'react'
   // After:
   import { useEffect } from 'react'
   ```

- [ ] **Step 2: Delete SettingsPanel files**

```bash
rm spa/src/components/SettingsPanel.tsx spa/src/components/SettingsPanel.test.tsx
```

- [ ] **Step 3: Run all tests**

Run: `cd spa && npx vitest run`
Expected: ALL PASS (SettingsPanel tests are gone, no broken imports)

- [ ] **Step 4: Run lint**

Run: `cd spa && pnpm run lint`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(settings): wire up settings tab, remove SettingsPanel overlay"
```

---

### Task 9: Smoke test — full integration verification

**Files:** None (verification only)

- [ ] **Step 1: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`
Expected: No errors

- [ ] **Step 3: Run build**

Run: `cd spa && pnpm run build`
Expected: Build succeeds with no errors

- [ ] **Step 4: Manual verification checklist (for human)**

List for the user to verify in browser at `http://100.64.0.2:5174`:

1. ActivityBar gear icon opens Settings tab (not overlay)
2. Settings tab shows sidebar with Appearance (active) + Terminal
3. Workspace and Sync show as gray with "coming soon"
4. Click Terminal → right side switches, URL becomes `/settings/terminal`
5. Browser back → returns to Appearance, URL `/settings/appearance`
6. Renderer toggle works (check useUISettingsStore in DevTools)
7. Keep-alive count respects 0-10 range
8. Toggle switch works for keep-alive pinned
9. Opening Settings from another tab doesn't create duplicate (singleton)
10. Closing Settings tab and reopening works

- [ ] **Step 5: Commit (if any fixes needed)**

Only if manual verification revealed issues that needed fixing.

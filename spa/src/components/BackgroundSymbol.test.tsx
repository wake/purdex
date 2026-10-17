// U1-3c-2: the corner symbol for background work (spec N6) on every tab surface.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { Terminal } from '@phosphor-icons/react'
import { BackgroundSymbol } from './BackgroundSymbol'
import type { FleetWaiter } from '../lib/fleet/types'
import { TabIcon } from './TabIcon'
import { renderInlineTabIcon } from '../features/workspace/lib/renderInlineTabIcon'
import type { BackgroundKind, SubagentRef } from '../stores/useAgentStore'
import type { TabIndicatorStyle } from '../stores/useUISettingsStore'
import { useI18nStore } from '../stores/useI18nStore'

const EMPTY: SubagentRef[] = []
const sym = () => screen.queryByTestId('tab-background-symbol')

beforeEach(() => {
  cleanup()
  act(() => { useI18nStore.getState().setLocale('en') })
})

describe('BackgroundSymbol', () => {
  it.each([
    ['workflow', 'Workflow running'],
    ['monitor', 'Monitor watching'],
    ['schedule', 'Scheduled wake-up'],
  ] as const)('%s draws one svg and names the kind', (kind, label) => {
    const { container } = render(<BackgroundSymbol kind={kind} />)
    expect(container.querySelectorAll('svg')).toHaveLength(1)
    const el = sym()!
    expect(el.getAttribute('data-kind')).toBe(kind)
    expect(el.getAttribute('aria-label')).toBe(label)
    expect(el.getAttribute('title')).toBe(label)
  })

  it('queued draws the hourglass; without waiters it names the kind', () => {
    render(<BackgroundSymbol kind="queued" />)
    expect(sym()!.getAttribute('data-kind')).toBe('queued')
    expect(sym()!.getAttribute('title')).toBe('Queued')
  })

  describe('the queue tooltip', () => {
    const waiter = (over: Partial<FleetWaiter> = {}): FleetWaiter =>
      ({ class: 'heavy', place: 1, command: 'go test', project: 'p', session: 's', since: Date.now() - 125_000, ...over })
    afterEach(() => { vi.useRealTimers() })

    it('one waiter: place, minutes, command, class', () => {
      render(<BackgroundSymbol kind="queued" waiters={[waiter({ place: 2 })]} />)
      expect(sym()!.getAttribute('title')).toBe('Queued: #2 · waiting 2 min · go test (heavy command)')
    })

    it('three waiters: the longest wait and the count of the others', () => {
      const now = Date.now()
      render(<BackgroundSymbol kind="queued" waiters={[waiter({ command: 'new', since: now - 1000 }), waiter({ command: 'old', since: now - 600_000, place: 3 }), waiter({ command: 'mid', since: now - 5000 })]} />)
      expect(sym()!.getAttribute('title')).toBe('Queued: #3 · waiting 10 min · old (heavy command) · 2 more')
    })

    it('the minutes advance with the clock', () => {
      vi.useFakeTimers()
      vi.setSystemTime(new Date('2026-10-17T10:00:30Z'))
      render(<BackgroundSymbol kind="queued" waiters={[waiter({ since: Date.now() - 60_000 })]} />)
      expect(sym()!.getAttribute('title')).toContain('waiting 1 min')
      act(() => { vi.advanceTimersByTime(60_000) })
      expect(sym()!.getAttribute('title')).toContain('waiting 2 min')
    })

    it('a symbol that is not queued holds no timer', () => {
      vi.useFakeTimers()
      render(<BackgroundSymbol kind="workflow" />)
      expect(vi.getTimerCount()).toBe(0)
    })
  })

  it('names the kind in zh-TW', () => {
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    render(<BackgroundSymbol kind="monitor" />)
    expect(sym()!.getAttribute('aria-label')).toBe('Monitor 監看中')
  })

  it('is static: no animation class on it or its icon', () => {
    const { container } = render(<BackgroundSymbol kind="schedule" />)
    expect(container.innerHTML).not.toMatch(/animate-/)
  })

  it('draws the kinds with different icons', () => {
    const paths = (['queued', 'workflow', 'monitor', 'schedule'] as const).map((k) => {
      const { container, unmount } = render(<BackgroundSymbol kind={k} />)
      const d = container.querySelector('svg')!.innerHTML
      unmount()
      return d
    })
    expect(new Set(paths).size).toBe(4)
  })
})

function tabIcon(style: TabIndicatorStyle, opts: { background?: BackgroundKind; awaitingApproval?: boolean; agentStatus?: 'running' | undefined } = {}) {
  return render(
    <TabIcon IconComponent={Terminal} agentStatus={'agentStatus' in opts ? opts.agentStatus : 'running'} tabIndicatorStyle={style}
      isActive={false} iconSize={14} subagentRefs={EMPTY} isUnread={false} awaitingApproval={opts.awaitingApproval} background={opts.background} />,
  )
}
function inlineIcon(style: TabIndicatorStyle, opts: { background?: BackgroundKind; awaitingApproval?: boolean; agentStatus?: 'running' | undefined } = {}) {
  return render(renderInlineTabIcon({
    IconComponent: Terminal, agentStatus: 'agentStatus' in opts ? opts.agentStatus : 'running', tabIndicatorStyle: style, isActive: false,
    subagentRefs: EMPTY, awaitingApproval: opts.awaitingApproval, background: opts.background,
  }))
}

describe.each([['TabIcon (top bar)', tabIcon], ['renderInlineTabIcon (activity bar)', inlineIcon]] as const)('%s', (_, draw) => {
  it.each(['badge', 'iconDot', 'dot'] as const)('%s: the symbol is drawn for each kind', (style) => {
    for (const kind of ['workflow', 'monitor', 'schedule'] as const) {
      const { unmount } = draw(style, { background: kind })
      expect(sym()?.getAttribute('data-kind')).toBe(kind)
      unmount()
    }
  })

  it.each(['badge', 'iconDot', 'dot'] as const)('%s: none without a background', (style) => {
    draw(style, {})
    expect(sym()).toBeNull()
  })

  it('icon (lights off): hidden, also while a worker awaits approval', () => {
    draw('icon', { background: 'monitor' })
    expect(sym()).toBeNull()
    cleanup()
    draw('icon', { background: 'monitor', awaitingApproval: true })
    expect(sym()).toBeNull()
  })

  it('no agent light: nothing', () => {
    draw('badge', { background: 'monitor', agentStatus: undefined })
    expect(sym()).toBeNull()
  })

  it('badge and iconDot put it on the ICON, dot at the dot', () => {
    // badge: shares its box with the agent icon and the overlay status dot
    let view = draw('badge', { background: 'workflow' })
    let box = sym()!.parentElement!
    expect(box.querySelector('[data-testid="tab-status-indicator"]')).toBeTruthy()
    expect(box.querySelectorAll('svg').length).toBe(2) // agent icon + the symbol's svg
    view.unmount()
    // iconDot: the symbol's box holds the agent icon, never the status-dot slot
    view = draw('iconDot', { background: 'workflow' })
    box = sym()!.parentElement!
    expect(box.querySelector('[data-testid="tab-status-indicator"]')).toBeNull()
    expect(box.querySelectorAll('svg').length).toBe(2)
    view.unmount()
    // dot: the symbol shares the slot with the status dot, and there is no agent icon at all
    view = draw('dot', { background: 'workflow' })
    box = sym()!.parentElement!
    expect(box.querySelector('[data-testid="tab-status-indicator"]')).toBeTruthy()
    expect(box.querySelectorAll('svg').length).toBe(1) // only the symbol's own
    view.unmount()
  })
})

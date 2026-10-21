// U1-3c-2: the corner symbol for background work (spec N6) on every tab surface.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, act, fireEvent } from '@testing-library/react'

vi.mock('../features/workspace/lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))
import { Terminal } from '@phosphor-icons/react'
import { BackgroundSymbol } from './BackgroundSymbol'
import { TabIcon } from './TabIcon'
import { renderInlineTabIcon } from '../features/workspace/lib/renderInlineTabIcon'
import type { BackgroundKind, SubagentRef } from '../stores/useAgentStore'
import type { TabIndicatorStyle } from '../stores/useUISettingsStore'
import { useI18nStore } from '../stores/useI18nStore'
import { openWatchArea } from '../features/workspace/lib/open-watch-area'
import { clearJobsViewMemory, readJobsMode, readSelectedJob, writeJobsMode } from '../lib/pool/jobs-view-memory'
import type { QueuedJobMark } from '../lib/watch/queued-sessions'

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
    const job = (over: Partial<QueuedJobMark> = {}): QueuedJobMark => ({ jobId: 'j1', origin: 'mlab', line: 'purdex spa-tsc', state: 'queued', place: 1, since: Date.now() - 125_000, worker: '', ...over })
    afterEach(() => { vi.useRealTimers() })

    it('one queued job: its place and the minutes waited', () => {
      render(<BackgroundSymbol kind="queued" waiters={[job({ place: 2 })]} />)
      expect(sym()!.getAttribute('title')).toContain('purdex spa-tsc')
      expect(sym()!.getAttribute('title')).toContain('#2')
      expect(sym()!.getAttribute('title')).toContain('2 min')
    })

    it('two jobs: the count and their names', () => {
      render(<BackgroundSymbol kind="queued" waiters={[job({ line: 'a' }), job({ jobId: 'j2', line: 'b' })]} />)
      expect(sym()!.getAttribute('title')).toContain('2')
      expect(sym()!.getAttribute('title')).toContain('a')
      expect(sym()!.getAttribute('title')).toContain('b')
    })

    it('the minutes advance with the clock', () => {
      vi.useFakeTimers()
      vi.setSystemTime(new Date('2026-10-17T10:00:30Z'))
      render(<BackgroundSymbol kind="queued" waiters={[job({ since: Date.now() - 60_000 })]} />)
      expect(sym()!.getAttribute('title')).toContain('1 min')
      act(() => { vi.advanceTimersByTime(60_000) })
      expect(sym()!.getAttribute('title')).toContain('2 min')
    })

    it('a symbol that is not queued holds no timer', () => {
      vi.useFakeTimers()
      render(<BackgroundSymbol kind="workflow" />)
      expect(vi.getTimerCount()).toBe(0)
    })
  })

  describe('the hourglass of a pool job opens the job queue (M34, M40)', () => {
    const mark = (over: Partial<QueuedJobMark> = {}): QueuedJobMark => ({ jobId: 'j1', origin: 'mlab', line: 'purdex test', state: 'running', place: 0, since: Date.now() - 1000, worker: 'mlab', ...over })
    beforeEach(() => { vi.mocked(openWatchArea).mockClear(); clearJobsViewMemory() })

    it('a press opens the task queue on the live view with the first job selected, and goes no further up (the tab is itself a button)', () => {
      writeJobsMode('history')
      const outer = vi.fn()
      render(<div onClick={outer}><BackgroundSymbol kind="queued" waiters={[mark(), mark({ jobId: 'j2', state: 'queued', place: 1 })]} /></div>)
      fireEvent.click(sym()!)
      expect(openWatchArea).toHaveBeenCalledWith({ tab: 'jobs' })
      expect(readJobsMode()).toBe('live')
      expect(readSelectedJob()).toBe('mlab/j1')
      expect(outer).not.toHaveBeenCalled()
    })

    it('pointerdown does not go up either (a tab starts a drag from it)', () => {
      const down = vi.fn()
      render(<div onPointerDown={down}><BackgroundSymbol kind="queued" waiters={[mark()]} /></div>)
      fireEvent.pointerDown(sym()!)
      expect(down).not.toHaveBeenCalled()
    })

    it('the first job is the one selected', () => {
      render(<BackgroundSymbol kind="queued" waiters={[mark({ jobId: 'j9', origin: 'mserv' }), mark({ jobId: 'j10' })]} />)
      fireEvent.click(sym()!)
      expect(readSelectedJob()).toBe('mserv/j9')
    })

    it('the other kinds are not pressable', () => {
      const outer = vi.fn()
      render(<div onClick={outer}><BackgroundSymbol kind="monitor" /></div>)
      fireEvent.click(sym()!)
      expect(openWatchArea).not.toHaveBeenCalled()
      expect(outer).toHaveBeenCalled()
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

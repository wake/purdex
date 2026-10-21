// #3806: the hourglass of a tab is not reachable from the keyboard as a control of its own (the plan forbids a button inside the tab),
// so the tab itself carries the way in: Alt+Enter on a tab that shows an hourglass for a pool job.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import { useI18nStore } from '../stores/useI18nStore'
import type { QueuedItem } from '../lib/watch/queued-sessions'

vi.mock('@dnd-kit/sortable', () => ({
  useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: vi.fn(), transform: null, transition: null, isDragging: false }),
}))
vi.mock('../features/workspace/lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))

let waiters: QueuedItem[] | undefined
let style = 'badge'
vi.mock('../hooks/useTabDisplay', () => ({
  useTabDisplay: () => ({
    displayTitle: 'tab one', IconComponent: undefined, agentStatus: 'running', isUnread: false, subagentRefs: [], tabIndicatorStyle: style,
    isHostOffline: false, isAwaitingApproval: false, background: waiters ? 'queued' : undefined, backgroundWaiters: waiters,
  }),
}))

const { SortableTab } = await import('./SortableTab')
const { openWatchArea } = await import('../features/workspace/lib/open-watch-area')
const { clearJobsViewMemory, readJobsMode, readSelectedJob, writeJobsMode } = await import('../lib/pool/jobs-view-memory')

const job = { jobId: 'j1', origin: 'mlab', line: 'purdex test', state: 'running' as const, place: 0, since: 1, worker: 'mlab' }
const job2 = { jobId: 'j2', origin: 'mserv', line: 'purdex lint', state: 'queued' as const, place: 1, since: 1, worker: '' }

function makeTab(pinned: boolean): Tab {
  return { ...createTab({ kind: 'tmux-session', hostId: 'h1', sessionCode: 'sc1', mode: 'terminal' as const, cachedName: '', tmuxInstance: '' }, { pinned }), id: 't1' }
}

function draw(pinned: boolean) {
  const onSelect = vi.fn()
  render(<SortableTab tab={makeTab(pinned)} isActive={false} pinned={pinned} onSelect={onSelect} onClose={vi.fn()} onMiddleClick={vi.fn()} onContextMenu={vi.fn()} />)
  return { onSelect, el: document.querySelector('[data-tab-id="t1"]') as HTMLElement }
}

describe.each([['wide tab', false], ['pinned tab', true]] as const)('SortableTab %s: Alt+Enter on the hourglass', (_, pinned) => {
  beforeEach(() => {
    cleanup()
    waiters = undefined
    style = 'badge'
    vi.mocked(openWatchArea).mockClear()
    clearJobsViewMemory()
    useI18nStore.getState().setLocale('en')
  })

  it('opens the live job queue with the first job selected, and does not select the tab', () => {
    waiters = [job, job2]
    writeJobsMode('history')
    const { el, onSelect } = draw(pinned)
    fireEvent.keyDown(el, { key: 'Enter', altKey: true })
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'jobs' })
    expect(readJobsMode()).toBe('live')
    expect(readSelectedJob()).toBe('mlab/j1')
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('announces the shortcut on the tab', () => {
    waiters = [job]
    expect(draw(pinned).el.getAttribute('aria-keyshortcuts')).toBe('Alt+Enter')
  })

  it('names the shortcut in the hourglass tooltip', () => {
    waiters = [job]
    draw(pinned)
    expect(screen.getByTestId('tab-background-symbol').getAttribute('title')).toContain('Alt+Enter')
  })

  it('without an hourglass: no shortcut, and Alt+Enter is what it was', () => {
    const { el, onSelect } = draw(pinned)
    expect(el.getAttribute('aria-keyshortcuts')).toBeNull()
    fireEvent.keyDown(el, { key: 'Enter', altKey: true })
    expect(openWatchArea).not.toHaveBeenCalled()
    if (!pinned) expect(onSelect).toHaveBeenCalledWith('t1')
  })

  it('lights off (icon): no hourglass is drawn, so no shortcut either', () => {
    waiters = [job]
    style = 'icon'
    const { el } = draw(pinned)
    expect(el.getAttribute('aria-keyshortcuts')).toBeNull()
    fireEvent.keyDown(el, { key: 'Enter', altKey: true })
    expect(openWatchArea).not.toHaveBeenCalled()
  })

  it('a plain Enter still selects the wide tab, not the queue', () => {
    waiters = [job]
    const { el, onSelect } = draw(pinned)
    fireEvent.keyDown(el, { key: 'Enter' })
    expect(openWatchArea).not.toHaveBeenCalled()
    if (!pinned) expect(onSelect).toHaveBeenCalledWith('t1')
  })
})

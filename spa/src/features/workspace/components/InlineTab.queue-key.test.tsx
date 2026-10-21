// #3806: Alt+Enter on a sidebar row that shows an hourglass for a pool job opens the job queue (see SortableTab.queue-key.test.tsx).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import type { Tab } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { QueuedItem } from '../../../lib/watch/queued-sessions'

vi.mock('@dnd-kit/sortable', () => ({
  useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: vi.fn(), transform: null, transition: null, isDragging: false }),
}))
vi.mock('../lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))

let waiters: QueuedItem[] | undefined
let style = 'badge'
vi.mock('../../../hooks/useTabDisplay', () => ({
  useTabDisplay: () => ({
    displayTitle: 'tab one', IconComponent: undefined, agentStatus: 'running', isUnread: false, subagentRefs: [], tabIndicatorStyle: style,
    isHostOffline: false, isAwaitingApproval: false, background: waiters ? 'queued' : undefined, backgroundWaiters: waiters,
  }),
}))

const { InlineTab } = await import('./InlineTab')
const { openWatchArea } = await import('../lib/open-watch-area')
const { clearJobsViewMemory, readJobsMode, readSelectedJob, writeJobsMode } = await import('../../../lib/pool/jobs-view-memory')

const job = { jobId: 'j1', origin: 'mlab', line: 'purdex test', state: 'running' as const, place: 0, since: 1, worker: 'mlab' }
const job2 = { jobId: 'j2', origin: 'mserv', line: 'purdex lint', state: 'queued' as const, place: 1, since: 1, worker: '' }

const tab = {
  id: 't1', kind: 'tmux-session', locked: false,
  layout: { type: 'leaf', pane: { id: 't1-pane', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'S1', terminated: false } } },
} as never as Tab

function draw() {
  const onSelect = vi.fn()
  render(<InlineTab tab={tab} isActive={false} onSelect={onSelect} onClose={vi.fn()} onMiddleClick={vi.fn()} onContextMenu={vi.fn()} />)
  return { onSelect, row: screen.getByTestId('inline-tab-row') }
}

describe('InlineTab: Alt+Enter on the hourglass', () => {
  beforeEach(() => {
    cleanup()
    waiters = undefined
    style = 'badge'
    vi.mocked(openWatchArea).mockClear()
    clearJobsViewMemory()
    useI18nStore.getState().setLocale('en')
  })

  it('opens the live job queue with the first job selected, and does not select the row', () => {
    waiters = [job, job2]
    writeJobsMode('history')
    const { row, onSelect } = draw()
    fireEvent.keyDown(row, { key: 'Enter', altKey: true })
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'jobs' })
    expect(readJobsMode()).toBe('live')
    expect(readSelectedJob()).toBe('mlab/j1')
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('announces the shortcut on the row and names it in the tooltip', () => {
    waiters = [job]
    const { row } = draw()
    expect(row.getAttribute('aria-keyshortcuts')).toBe('Alt+Enter')
    expect(screen.getByTestId('tab-background-symbol').getAttribute('title')).toContain('Alt+Enter')
  })

  it('without an hourglass: no shortcut, Alt+Enter is what it was', () => {
    for (const w of [undefined]) {
      cleanup()
      waiters = w
      const { row, onSelect } = draw()
      expect(row.getAttribute('aria-keyshortcuts')).toBeNull()
      fireEvent.keyDown(row, { key: 'Enter', altKey: true })
      expect(openWatchArea).not.toHaveBeenCalled()
      expect(onSelect).toHaveBeenCalledWith('t1')
    }
  })

  it('lights off (icon): no hourglass is drawn, so no shortcut either', () => {
    waiters = [job]
    style = 'icon'
    const { row } = draw()
    expect(row.getAttribute('aria-keyshortcuts')).toBeNull()
    fireEvent.keyDown(row, { key: 'Enter', altKey: true })
    expect(openWatchArea).not.toHaveBeenCalled()
  })

  it('a plain Enter still selects the row, not the queue', () => {
    waiters = [job]
    const { row, onSelect } = draw()
    fireEvent.keyDown(row, { key: 'Enter' })
    expect(openWatchArea).not.toHaveBeenCalled()
    expect(onSelect).toHaveBeenCalledWith('t1')
  })
})

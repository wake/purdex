// #3806: a tab that shows an hourglass for a pool job gets a context-menu row that does what a press on the hourglass does.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { createTab } from '../types/tab'
import { useI18nStore } from '../stores/useI18nStore'
import type { QueuedItem } from '../lib/watch/queued-sessions'

vi.mock('../features/workspace/lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))

let waiters: QueuedItem[] | undefined
let style = 'badge'
vi.mock('../hooks/useTabDisplay', () => ({
  useTabDisplay: () => ({ backgroundWaiters: waiters, tabIndicatorStyle: style, agentStatus: 'running', isAwaitingApproval: false }),
}))

const { TabContextMenu } = await import('./TabContextMenu')
const { openWatchArea } = await import('../features/workspace/lib/open-watch-area')
const { clearJobsViewMemory, readJobsMode, readSelectedJob, writeJobsMode } = await import('../lib/pool/jobs-view-memory')

const job = { jobId: 'j1', origin: 'mlab', line: 'purdex test', state: 'running' as const, place: 0, since: 1, worker: 'mlab' }
const gate = { class: 'heavy' as const, place: 1, command: 'go test', project: 'p', session: 's', since: 1 }

function draw() {
  const props = {
    tab: createTab({ kind: 'tmux-session', hostId: 'h1', sessionCode: 'sc1', mode: 'terminal' as const, cachedName: '', tmuxInstance: '' }),
    position: { x: 10, y: 10 }, onClose: vi.fn(), onAction: vi.fn(), hasOtherUnlocked: true, hasRightUnlocked: true,
  }
  render(<TabContextMenu {...props} />)
  return props
}

describe('TabContextMenu: open the job queue', () => {
  beforeEach(() => {
    cleanup()
    waiters = undefined
    style = 'badge'
    vi.mocked(openWatchArea).mockClear()
    clearJobsViewMemory()
    useI18nStore.getState().setLocale('en')
  })

  it('a tab with an hourglass for a pool job: the row opens the live queue on the first job, then closes the menu', () => {
    waiters = [gate, job]
    writeJobsMode('history')
    const { onClose, onAction } = draw()
    fireEvent.click(screen.getByText('Open job queue'))
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'jobs' })
    expect(readJobsMode()).toBe('live')
    expect(readSelectedJob()).toBe('mlab/j1')
    expect(onClose).toHaveBeenCalled()
    expect(onAction).not.toHaveBeenCalled()
  })

  it('no hourglass: no row', () => {
    draw()
    expect(screen.queryByText('Open job queue')).toBeNull()
  })

  it('gate waiters only (no pool job to open): no row', () => {
    waiters = [gate]
    draw()
    expect(screen.queryByText('Open job queue')).toBeNull()
  })

  it('lights off (icon): the hourglass is not drawn, so no row', () => {
    waiters = [job]
    style = 'icon'
    draw()
    expect(screen.queryByText('Open job queue')).toBeNull()
  })

  it('is named in zh-TW', () => {
    useI18nStore.getState().setLocale('zh-TW')
    waiters = [job]
    draw()
    expect(screen.getByText('打開任務佇列')).toBeInTheDocument()
  })
})

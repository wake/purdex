import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act, waitFor } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import type { ExecutionContent } from '../types/tab'
import { useUploadStore } from '../stores/useUploadStore'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import { executionKey, useExecutionStore } from '../stores/useExecutionStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { defaultExecutionState } from '../lib/nex/event-reducer'
import type { ExecutionSummary } from '../lib/nex/types'
import { HOST_ID, copyTextMock, setupStores, makeTab } from './StatusBar.test-helpers'

vi.mock('../lib/copy-text', () => ({ copyText: vi.fn(async () => {}) }))
// The worker bar reads the host quota through `fetchNexHost`; no test here may reach a real network.
vi.mock('../lib/nex/nex-api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/nex/nex-api')>()
  return { ...actual, fetchNexHost: vi.fn(async () => ({ active_account: 'a', quota: { five_hour_pct: 41, seven_day_pct: 72, resets_at: 0, source: 'usage_api' } })) }
})

beforeEach(() => {
  cleanup()
  setupStores()
})

// Shell cleanup P6 (spec §9.2): an `execution` target gets a worker bar — the host segment the tmux bar uses, the
// worker name and its cwd from the execution summary, then the controls block (the mode buttons land there).
describe('StatusBar worker bar', () => {
  const EXEC_ID = 'exc_1'
  const summary = (extra: Partial<ExecutionSummary> = {}): ExecutionSummary => ({
    id: EXEC_ID, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev',
    brief: 'Fix the login bug\nand more', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0,
    observers: 1, archived: false, ...extra,
  })
  const seedSummary = (s: ExecutionSummary | null, hostId = HOST_ID) => {
    useExecutionStore.setState({ executions: { [executionKey(hostId, EXEC_ID)]: { ...defaultExecutionState(), summary: s } } })
  }
  const workerTab = (extra: Partial<ExecutionContent> = {}) =>
    makeTab('t1', { kind: 'execution', executionId: EXEC_ID, host: HOST_ID, ...extra })

  beforeEach(() => {
    setupStores()
    usePaneFocusStore.setState({ recent: {} })
    useUploadStore.setState({ sessions: {} })
    useNexHostStore.setState({ byHost: {} })
    seedSummary(summary())
  })

  it('shows the host, the worker name and its cwd', () => {
    render(<StatusBar activeTab={workerTab()} />)
    expect(screen.getByTestId('status-seg-host').textContent).toBe('mlab')
    // The first line of the brief: the same primary the tab title uses, without the tab's cwd suffix (the cwd has
    // its own segment).
    expect(screen.getByTestId('status-seg-worker-name').textContent).toBe('Fix the login bug')
    expect(screen.getByTestId('status-seg-cwd').textContent).toBe('/Users/w/repo')
  })

  it('shows the peer id only when the summary carries an address, and copies the full address', async () => {
    render(<StatusBar activeTab={workerTab()} />)
    expect(screen.queryByTestId('status-seg-peer-id')).toBeNull()
    cleanup()
    seedSummary(summary({ peer_address: 'mlab/fix-login' }))
    render(<StatusBar activeTab={workerTab()} />)
    const seg = screen.getByTestId('status-seg-peer-id')
    expect(seg.textContent).toBe('fix-login')
    await act(async () => { fireEvent.click(seg) })
    expect(copyTextMock).toHaveBeenCalledWith('mlab/fix-login')
  })

  it('has no tmux-only segments: no session name, peer, refresh, status or upload', () => {
    render(<StatusBar activeTab={workerTab()} />)
    for (const id of ['status-seg-session-name', 'status-seg-peer-id', 'status-peer-refresh', 'status-seg-status', 'upload-status']) {
      expect(screen.queryByTestId(id), id).toBeNull()
    }
    expect(screen.queryByText('execution')).toBeNull()
  })

  it('lays out the same three containers, with exactly one ml-auto: the controls block, holding the mode buttons', () => {
    render(<StatusBar activeTab={workerTab()} />)
    const bar = screen.getByTestId('status-bar')
    expect(bar.querySelectorAll('.ml-auto').length).toBe(1)
    const controls = screen.getByTestId('status-controls')
    expect(controls.className).toContain('ml-auto')
    const modes = screen.getByTestId('status-mode-buttons')
    expect(controls).toContainElement(modes)
    expect(modes.className).toContain('max-[500px]:hidden')
    expect(screen.getByRole('button', { name: 'Worker room' }).getAttribute('aria-pressed')).toBe('true')
    expect(controls.className).toContain('shrink-0')
    expect(screen.getByTestId('status-segments').className).toContain('min-w-0')
    expect(screen.getByTestId('status-copy-feedback')).toBeInTheDocument()
  })

  it('prefers the handed-off terminal title over the brief', () => {
    render(<StatusBar activeTab={workerTab({ fromTitle: 'cc-session' })} />)
    expect(screen.getByTestId('status-seg-worker-name').textContent).toBe('cc-session')
  })

  it('no summary yet → the generic worker label and an empty cwd', () => {
    seedSummary(null)
    render(<StatusBar activeTab={workerTab()} />)
    expect(screen.getByTestId('status-seg-worker-name').textContent).toBe('Execution')
    const cwd = screen.getByTestId('status-seg-cwd')
    expect(cwd.textContent).toBe('—')
    expect(cwd).toBeDisabled()
  })

  it('a pane without a host hint reads the first host, like the worker pane itself', () => {
    render(<StatusBar activeTab={workerTab({ host: undefined })} />)
    expect(screen.getByTestId('status-seg-host').textContent).toBe('mlab')
    expect(screen.getByTestId('status-seg-cwd').textContent).toBe('/Users/w/repo')
  })

  it('follows the summary live', () => {
    render(<StatusBar activeTab={workerTab()} />)
    act(() => seedSummary(summary({ cwd: '/Users/w/other' })))
    expect(screen.getByTestId('status-seg-cwd').textContent).toBe('/Users/w/other')
  })

  it('the host segment double-clicks to host settings, like the tmux bar', () => {
    const onNavigateToHost = vi.fn()
    render(<StatusBar activeTab={workerTab()} onNavigateToHost={onNavigateToHost} />)
    fireEvent.doubleClick(screen.getByTestId('status-seg-host'))
    expect(onNavigateToHost).toHaveBeenCalledWith(HOST_ID)
  })

  it('clicking the cwd copies it and confirms in the feedback slot', async () => {
    render(<StatusBar activeTab={workerTab()} />)
    fireEvent.click(screen.getByTestId('status-seg-cwd'))
    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith('/Users/w/repo'))
    await waitFor(() => expect(screen.getByTestId('status-copy-feedback').textContent).toBe('copied: cwd'))
  })

  // Shell polish spec §4 (rule F): the worker bar's segments are the same CopySegment, so they keep focus too.
  it('a mouse press on the host or cwd segment keeps focus where it was; both stay in the tab order', () => {
    render(<StatusBar activeTab={workerTab()} />)
    for (const testId of ['status-seg-host', 'status-seg-cwd']) {
      const seg = screen.getByTestId(testId)
      expect(fireEvent.mouseDown(seg), testId).toBe(false)
      expect(seg.tabIndex, testId).toBeGreaterThanOrEqual(0)
    }
  })
})

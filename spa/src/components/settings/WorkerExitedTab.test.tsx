import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'

const history = vi.fn()
vi.mock('../../hooks/useExecutionHistory', () => ({ useExecutionHistory: (h: string) => history(h) }))
const hostExecutions = vi.fn()
vi.mock('../../hooks/useHostExecutions', () => ({ useHostExecutions: (h: string) => hostExecutions(h) }))
const openWorkerTab = vi.fn()
vi.mock('../../features/workspace/lib/open-worker-tab', () => ({ openWorkerTab: (c: unknown) => openWorkerTab(c) }))
import { WorkerExitedTab } from './WorkerExitedTab'
import { useTabStore } from '../../stores/useTabStore'
import type { ExecutionSummary } from '../../lib/nex/types'

const r = (o: Partial<ExecutionSummary>): ExecutionSummary => ({
  id: 'x', state: 'terminated', provider: 'cc', principal_id: 'p', cwd: '/w/proj', mount_kind: 'none', brief: '', labels: {},
  created_at: 1, updated_at: Date.now(), duration_ms: null, event_count: 0, observers: 0, archived: true, ...o,
})
const ready = (items: ExecutionSummary[], extra = {}) => ({ items, phase: 'ready', error: null, truncated: false, refetch: vi.fn(), ...extra })

const termTab = (rebuild: unknown) => ({
  t1: { id: 't1', pinned: false, locked: false, createdAt: 0, layout: { type: 'leaf', pane: { id: 'p1', content: {
    kind: 'tmux-session', hostId: 'h1', sessionCode: 'c', mode: 'terminal', cachedName: 'n', tmuxInstance: '', rebuild,
  } } } },
})

describe('WorkerExitedTab', () => {
  beforeEach(() => {
    openWorkerTab.mockReset()
    hostExecutions.mockReset()
    hostExecutions.mockReturnValue({ items: [], phase: 'ready', error: null, truncated: false, refetch: vi.fn(), refreshRevision: 0 })
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null } as never)
  })

  it('a session running in a terminal pane shows the badge and no rebuild button', () => {
    history.mockReturnValue(ready([r({ id: 'e1', session_id: 'S1', brief: 'one' }), r({ id: 'e2', session_id: 'S2', brief: 'two' })]))
    useTabStore.setState({
      tabs: termTab({ sessionName: 'n', tmuxInstance: '', agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 } }),
      tabOrder: ['t1'],
    } as never)
    render(<WorkerExitedTab hostId="h1" />)
    const rows = screen.getAllByTestId('worker-exited-row')
    expect(rows).toHaveLength(2)
    const one = rows.find((x) => x.textContent?.includes('one'))!
    expect(within(one).getByTestId('worker-exited-in-terminal')).toBeInTheDocument()
    expect(within(one).queryByTestId('worker-exited-rebuild')).toBeNull()
    const two = rows.find((x) => x.textContent?.includes('two'))!
    fireEvent.click(within(two).getByTestId('worker-exited-rebuild'))
    expect(openWorkerTab).toHaveBeenCalledWith({ kind: 'execution', executionId: 'e2', host: 'h1' })
  })

  it('an exited agent does not count as in a terminal', () => {
    history.mockReturnValue(ready([r({ id: 'e1', session_id: 'S1', brief: 'one' })]))
    useTabStore.setState({
      tabs: termTab({ sessionName: 'n', tmuxInstance: '', agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 }, agentExited: { at: 1, reason: 'session-end' } }),
      tabOrder: ['t1'],
    } as never)
    render(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-rebuild')).toBeInTheDocument()
  })

  it('search filters the rows', () => {
    history.mockReturnValue(ready([r({ id: 'e1', session_id: 'S1', brief: 'alpha' }), r({ id: 'e2', session_id: 'S2', brief: 'beta' })]))
    render(<WorkerExitedTab hostId="h1" />)
    fireEvent.change(screen.getByTestId('worker-exited-search'), { target: { value: 'BET' } })
    const rows = screen.getAllByTestId('worker-exited-row')
    expect(rows).toHaveLength(1)
    expect(rows[0].textContent).toContain('beta')
  })

  it('shows the truncation line, empty, loading and error states', () => {
    history.mockReturnValue(ready([], { truncated: true }))
    const { rerender } = render(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-truncated')).toBeInTheDocument()
    expect(screen.getByTestId('worker-exited-empty')).toBeInTheDocument()
    history.mockReturnValue(ready([], { phase: 'loading' }))
    rerender(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-loading')).toBeInTheDocument()
    history.mockReturnValue(ready([], { phase: 'error', error: 'boom' }))
    rerender(<WorkerExitedTab hostId="h1" />)
    expect(screen.getByTestId('worker-exited-error')).toBeInTheDocument()
    expect(screen.queryByTestId('worker-exited-empty')).toBeNull()
  })

  it('an entity with a live stint in the shared live list is not listed as exited', () => {
    history.mockReturnValue(ready([r({ id: 'e1', session_id: 'S1', brief: 'one' }), r({ id: 'e2', session_id: 'S2', brief: 'two' })]))
    hostExecutions.mockReturnValue({ items: [r({ id: 'l1', session_id: 'S1', state: 'idle', archived: false, created_at: 9 })], phase: 'ready', error: null, truncated: false, refetch: vi.fn(), refreshRevision: 0 })
    render(<WorkerExitedTab hostId="h1" />)
    const rows = screen.getAllByTestId('worker-exited-row')
    expect(rows).toHaveLength(1)
    expect(rows[0].textContent).toContain('two')
  })

  it('holds the host live-list subscription (so refreshRevision moves) while mounted', () => {
    history.mockReturnValue(ready([]))
    render(<WorkerExitedTab hostId="h1" />)
    expect(hostExecutions).toHaveBeenCalledWith('h1')
  })

  it('renders nothing without a host', () => {
    history.mockReturnValue(ready([]))
    const { container } = render(<WorkerExitedTab />)
    expect(container).toBeEmptyDOMElement()
  })
})

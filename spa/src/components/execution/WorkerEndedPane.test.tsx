import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { WorkerEndedPane, workerEndedKind } from './WorkerEndedPane'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { createTab } from '../../types/tab'
import { getPrimaryPane } from '../../lib/pane-tree'
import { HandoffApiError } from '../../lib/nex/handoff-api'
import { takeToTerminal } from '../../lib/nex/handoff'
import { rebuildAsWorker } from '../../lib/nex/worker-rebuild'
import { exitWorker } from '../../lib/nex/exit-worker'
import { useExecutionStore } from '../../stores/useExecutionStore'
import type { ExecutionSummary, NexCapabilities } from '../../lib/nex/types'

vi.mock('../../lib/nex/handoff', async (o) => ({ ...(await o<typeof import('../../lib/nex/handoff')>()), takeToTerminal: vi.fn() }))
vi.mock('../../lib/nex/exit-worker', async (o) => ({ ...(await o<typeof import('../../lib/nex/exit-worker')>()), exitWorker: vi.fn() }))
vi.mock('../../lib/nex/worker-rebuild', async (o) => ({ ...(await o<typeof import('../../lib/nex/worker-rebuild')>()), rebuildAsWorker: vi.fn() }))

const H = 'h', E = 'exc_1'
const caps = {
  phase: 'ga', host_id: H, verbs: [], providers: ['claude'], events: [], provider_events: [], transient_events: [],
  sandbox_profiles: ['default', 'handoff'], sandbox_default_profile: 'default', sandbox_max_profile: 'handoff',
  roots: [], lease: { ttl_seconds: 30, scope: 'x', renew: { method: 'POST', path: '/r' }, release: { method: 'DELETE', path: '/r' } },
  send: { delivery: ['text'], max_text_bytes: 1 }, delegate: { resume_session_id: true },
} as unknown as NexCapabilities
const entry = (over = {}) => ({ info: null, capabilities: caps, phase: 'ready', error: null, fetchedAt: 0, generation: 1, fingerprint: 'f', ...over })

const sum = (extra: Partial<ExecutionSummary> = {}): ExecutionSummary => ({
  id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '', mount_kind: 'dev', brief: 'b', labels: {},
  created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...extra,
} as ExecutionSummary)

let tabId = '', paneId = ''
function renderPane(summary: ExecutionSummary) {
  return render(<WorkerEndedPane hostId={H} executionId={E} summary={summary} tabId={tabId} paneId={paneId} />)
}

beforeEach(() => {
  vi.mocked(takeToTerminal).mockReset().mockResolvedValue({ result: {}, swapped: true } as never)
  vi.mocked(rebuildAsWorker).mockReset().mockResolvedValue({ result: { execution_id: 'n', state: 'running' }, swapped: true })
  vi.mocked(exitWorker).mockReset().mockResolvedValue({ exited: true, terminated: true, archived: true, state: 'terminated' })
  useExecutionStore.setState({ executions: {} })
  useNexHostStore.setState({ byHost: { [H]: entry() } } as never)
  const tab = createTab({ kind: 'execution', executionId: E, host: H })
  tabId = tab.id
  paneId = getPrimaryPane(tab.layout).id
  useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
})

describe('workerEndedKind', () => {
  it('classifies', () => {
    expect(workerEndedKind(sum({ state: 'terminated' }))).toBe('exited')
    expect(workerEndedKind(sum({ state: 'idle', archived: true }))).toBe('exited')
    expect(workerEndedKind(sum({ state: 'failed' }))).toBe('failed')
    expect(workerEndedKind(sum({ state: 'rejected' }))).toBe('failed')
    expect(workerEndedKind(sum({ state: 'idle' }))).toBeNull()
    expect(workerEndedKind(null)).toBeNull()
  })
})

describe('WorkerEndedPane', () => {
  it('exited: worker preselected; rebuild as worker replaces nothing', async () => {
    renderPane(sum({ state: 'terminated', archived: true, session_id: 'S', cwd: '/w', effective_profile: 'handoff' }))
    expect(screen.getByText('This worker has exited')).toBeInTheDocument()
    expect(screen.getByTestId('rebuild-mode-worker')).toHaveAttribute('aria-checked', 'true')
    fireEvent.click(screen.getByTestId('worker-rebuild'))
    await waitFor(() => expect(rebuildAsWorker).toHaveBeenCalledTimes(1))
    expect(rebuildAsWorker).toHaveBeenCalledWith(expect.objectContaining({ hostId: H, sessionId: 'S', cwd: '/w', profile: 'handoff', replaceExecutionId: undefined, tabId, paneId }))
  })

  it('failed: shows the reason and replaces the failed stint', async () => {
    renderPane(sum({ state: 'rejected', reject_reason: 'session_expired', resume_session_id: 'S', cwd: '/w' }))
    expect(screen.getByText('Failed to start')).toBeInTheDocument()
    expect(screen.getByText(/session_expired/)).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('worker-rebuild'))
    await waitFor(() => expect(rebuildAsWorker).toHaveBeenCalled())
    expect(rebuildAsWorker).toHaveBeenCalledWith(expect.objectContaining({ sessionId: 'S', replaceExecutionId: E }))
  })

  it('the expect guard matches only this execution on this host', async () => {
    renderPane(sum({ state: 'terminated', archived: true, session_id: 'S', cwd: '/w' }))
    fireEvent.click(screen.getByTestId('worker-rebuild'))
    await waitFor(() => expect(rebuildAsWorker).toHaveBeenCalled())
    const { expect: guard } = vi.mocked(rebuildAsWorker).mock.calls[0][0]
    expect(guard({ kind: 'execution', executionId: E, host: H })).toBe(true)
    expect(guard({ kind: 'execution', executionId: E })).toBe(true)
    expect(guard({ kind: 'execution', executionId: 'other', host: H })).toBe(false)
    expect(guard({ kind: 'execution', executionId: E, host: 'x' })).toBe(false)
  })

  it('terminal choice takes it to a new terminal', async () => {
    renderPane(sum({ state: 'terminated', archived: true, session_id: 'S', cwd: '/w' }))
    fireEvent.click(screen.getByTestId('rebuild-mode-terminal'))
    fireEvent.click(screen.getByTestId('worker-rebuild'))
    await waitFor(() => expect(takeToTerminal).toHaveBeenCalled())
    expect(takeToTerminal).toHaveBeenCalledWith(expect.objectContaining({ hostId: H, executionId: E, cwd: '/w', tabId, paneId }))
    expect(rebuildAsWorker).not.toHaveBeenCalled()
  })

  it('without nex ready the worker option is disabled and terminal is preselected', () => {
    useNexHostStore.setState({ byHost: { [H]: entry({ phase: 'unavailable', capabilities: null }) } } as never)
    renderPane(sum({ state: 'terminated', archived: true, session_id: 'S', cwd: '/w' }))
    expect(screen.getByTestId('rebuild-mode-worker')).toBeDisabled()
    expect(screen.getByTestId('rebuild-mode-terminal')).toHaveAttribute('aria-checked', 'true')
  })

  it('a non-claude provider cannot go to a terminal: worker stays selected', () => {
    renderPane(sum({ state: 'terminated', archived: true, provider: 'codex', session_id: 'S', cwd: '/w' }))
    expect(screen.getByTestId('rebuild-mode-terminal')).toBeDisabled()
    expect(screen.getByTestId('rebuild-mode-worker')).toHaveAttribute('aria-checked', 'true')
  })

  it('with neither mode available the button is disabled and says why', () => {
    useNexHostStore.setState({ byHost: { [H]: entry({ phase: 'unavailable', capabilities: null }) } } as never)
    renderPane(sum({ state: 'terminated', archived: true, provider: 'codex', session_id: 'S', cwd: '/w' }))
    expect(screen.getByTestId('worker-rebuild')).toBeDisabled()
    expect(screen.getByText(/not ready/)).toBeInTheDocument()
  })

  it('no session id: worker unavailable', () => {
    renderPane(sum({ state: 'terminated', archived: true, cwd: '/w' }))
    expect(screen.getByTestId('rebuild-mode-worker')).toBeDisabled()
  })

  it('shows an owner refusal inline', async () => {
    vi.mocked(rebuildAsWorker).mockRejectedValue(new HandoffApiError(409, 'session_owned', { owner: 'terminal' }))
    renderPane(sum({ state: 'terminated', archived: true, session_id: 'S', cwd: '/w' }))
    fireEvent.click(screen.getByTestId('worker-rebuild'))
    expect(await screen.findByTestId('worker-rebuild-error')).toHaveTextContent('This conversation is already open in a terminal')
  })

  it('shows a terminal-path refusal inline', async () => {
    vi.mocked(takeToTerminal).mockRejectedValue(new HandoffApiError(409, 'session_owned', { owner: 'worker' }))
    renderPane(sum({ state: 'terminated', archived: true, session_id: 'S', cwd: '/w' }))
    fireEvent.click(screen.getByTestId('rebuild-mode-terminal'))
    fireEvent.click(screen.getByTestId('worker-rebuild'))
    expect(await screen.findByTestId('worker-rebuild-error')).toBeInTheDocument()
  })

  describe('exit on a failed stint', () => {
    const failed = () => sum({ state: 'rejected', reject_reason: 'x', resume_session_id: 'S', cwd: '/w' })
    // Reads the summary from the store like ExecutionView does, so the patch re-renders the pane.
    function Live() {
      const s = useExecutionStore((x) => x.executions[`${H}:${E}`]?.summary)
      return <WorkerEndedPane hostId={H} executionId={E} summary={s as ExecutionSummary} tabId={tabId} paneId={paneId} />
    }

    it('failed offers exit; exited does not', () => {
      const { unmount } = renderPane(failed())
      expect(screen.getByTestId('worker-ended-exit')).toHaveTextContent('Exit')
      unmount()
      renderPane(sum({ state: 'terminated', archived: true, session_id: 'S', cwd: '/w' }))
      expect(screen.queryByTestId('worker-ended-exit')).toBeNull()
    })

    it('calls exitWorker with host and execution only, then turns into the exited screen', async () => {
      useExecutionStore.getState().setSummary(H, E, failed())
      render(<Live />)
      fireEvent.click(screen.getByTestId('worker-ended-exit'))
      await waitFor(() => expect(screen.getByText('This worker has exited')).toBeInTheDocument())
      expect(exitWorker).toHaveBeenCalledWith({ hostId: H, executionId: E })
      expect(screen.queryByTestId('worker-ended-exit')).toBeNull()
    })

    it('held_by names the principal inline', async () => {
      vi.mocked(exitWorker).mockRejectedValue(new HandoffApiError(409, 'held_by', { principal: 'ploom:agent-7' }))
      renderPane(failed())
      fireEvent.click(screen.getByTestId('worker-ended-exit'))
      expect(await screen.findByTestId('worker-rebuild-error')).toHaveTextContent('ploom:agent-7')
    })

    it('disables exit and rebuild while the exit is in flight', async () => {
      let done!: () => void
      vi.mocked(exitWorker).mockReturnValue(new Promise((r) => { done = () => r({ exited: true, terminated: true, archived: true, state: 'terminated' }) }))
      renderPane(failed())
      fireEvent.click(screen.getByTestId('worker-ended-exit'))
      await waitFor(() => expect(screen.getByTestId('worker-ended-exit')).toBeDisabled())
      expect(screen.getByTestId('worker-rebuild')).toBeDisabled()
      done()
      await waitFor(() => expect(screen.getByTestId('worker-ended-exit')).toBeEnabled())
    })
  })
})

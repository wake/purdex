import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { WorkerLiveTab } from './WorkerLiveTab'
import { resetExecutionListForTests, useExecutionListStore } from '../../stores/useExecutionListStore'
import { useNexHostStore, type NexHostEntry } from '../../stores/useNexHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { subscriptionSlots } from '../../lib/nex/subscription-slots'
import type { ExecutionSummary } from '../../lib/nex/types'

vi.mock('../../lib/nex/nex-api', () => ({ listExecutions: vi.fn().mockResolvedValue({ items: [], next_cursor: '' }), attachControl: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), archiveExecution: vi.fn() }))
vi.mock('../../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))

const H = 'host-a'
const row = (over: Partial<ExecutionSummary> & { id: string }): ExecutionSummary =>
  ({ state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'brief', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...over }) as ExecutionSummary
const entry = (daemonCapabilities?: string[]): NexHostEntry => ({
  info: { configured: true, mounted: true, ready: true, init_error: '', effective: null },
  capabilities: null, phase: 'ready', error: null, fetchedAt: 1, generation: 1, fingerprint: '1:1:t', daemonCapabilities,
})
const useCaps = (caps?: string[]) =>
  useNexHostStore.setState({ byHost: { [H]: entry(caps) }, ensure: vi.fn().mockResolvedValue(undefined) })

beforeEach(() => {
  subscriptionSlots.resetForTests()
  resetExecutionListForTests()
  useExecutionListStore.setState({
    byHost: { [H]: { items: [
      row({ id: 'N1', session_id: 'SN', cwd: '/Users/w/proj', brief: 'normal one' }),
      row({ id: 'T1', session_id: 'ST', cwd: '/tmp/x', brief: 'test one' }),
    ], phase: 'ready', error: null, lastSeq: null, refreshRevision: 0, truncated: false, complete: true } },
  })
  useShownHostsStore.setState({ ids: [H] })
})

describe('WorkerLiveTab', () => {
  it('on a conversations.scope.v1 host, hides test-cwd workers', () => {
    useCaps(['conversations.scope.v1'])
    render(<WorkerLiveTab hostId={H} />)
    expect(screen.getAllByTestId('executions-row')).toHaveLength(1)
    expect(screen.queryByText('test one')).toBeNull()
  })

  // codex R2: "capability not known yet" must not look like "an old daemon" — test rows would flash in Workers.
  it('while the host entry is missing or loading, test-cwd workers stay hidden', () => {
    useNexHostStore.setState({ byHost: {}, ensure: vi.fn().mockResolvedValue(undefined) })
    const { unmount } = render(<WorkerLiveTab hostId={H} />)
    expect(screen.queryByText('test one')).toBeNull()
    unmount()
    useNexHostStore.setState({ byHost: { [H]: { ...entry(), phase: 'loading', info: null } }, ensure: vi.fn().mockResolvedValue(undefined) })
    render(<WorkerLiveTab hostId={H} />)
    expect(screen.queryByText('test one')).toBeNull()
    expect(screen.getByText('normal one')).toBeInTheDocument()
  })

  it('a ready host without the capability (an older daemon) still lists every row', () => {
    useCaps(undefined)
    render(<WorkerLiveTab hostId={H} />)
    expect(screen.getAllByTestId('executions-row')).toHaveLength(2)
  })
})

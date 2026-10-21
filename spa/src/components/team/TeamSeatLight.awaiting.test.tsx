// #3830: the seat light over the real indicator hook and the real stores — a worker's list row carries the pending permission request, and the team panel draws what the tab bar draws (waiting dot + hand),
// before useWorkerAgentProjection has written any status, and the way the tab row treats the icon-only setting.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { TeamSeatLight } from './TeamSeatLight'
import { useAgentStore } from '../../stores/useAgentStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { emptyListCache } from '../../lib/nex/execution-list-effects'
import { execAgentCode } from '../../lib/nex/worker-agent-status'
import { compositeKey } from '../../lib/composite-key'
import type { ExecutionSummary } from '../../lib/nex/types'

const summary = (over: Partial<ExecutionSummary> = {}): ExecutionSummary =>
  ({ id: 'e1', state: 'running', provider: 'claude', principal_id: 'p', cwd: '/w/repo', mount_kind: 'dev', brief: 'Brief', labels: {},
    created_at: 1, updated_at: 5, duration_ms: null, event_count: 0, observers: 0, archived: false, ...over }) as ExecutionSummary
const pending = { request_id: 'r1', tool_name: 'Bash', since: 1 }
const listWith = (items: ExecutionSummary[]) => useExecutionListStore.setState({ byHost: { h1: { ...emptyListCache(), phase: 'ready', items } } })
const worker = execAgentCode('e1')

beforeEach(() => {
  useAgentStore.setState({ statuses: {}, agentTypes: {}, subagents: {}, unread: {} })
  useExecutionListStore.setState({ byHost: {} })
  useExecutionStore.setState({ executions: {} })
  useUISettingsStore.setState({ tabIndicatorStyle: 'badge' })
})
afterEach(() => cleanup())

describe('TeamSeatLight over the real hook', () => {
  it('a worker waiting on a permission request shows the waiting dot and the hand, though no status was written yet', () => {
    listWith([summary({ pending_permission: pending })])
    render(<TeamSeatLight hostId="h1" sessionCode={worker} />)
    expect(screen.getByTestId('team-panel-light-hand')).toBeInTheDocument()
    expect(screen.getByTestId('team-panel-light-dot').getAttribute('data-status')).toBe('waiting')
    expect(screen.getByTestId('team-panel-light-awaiting').getAttribute('aria-label')).toBe(useI18nStore.getState().t('executions.activity.awaiting_approval'))
  })

  it('the hand comes with the request and goes when it is answered, while the status is still running', () => {
    useAgentStore.setState({ statuses: { [compositeKey('h1', worker)]: 'running' } })
    listWith([summary()])
    render(<TeamSeatLight hostId="h1" sessionCode={worker} />)
    expect(screen.queryByTestId('team-panel-light-hand')).toBeNull()
    expect(screen.getByTestId('team-panel-light-dot').getAttribute('data-status')).toBe('running')
    act(() => listWith([summary({ pending_permission: pending })]))
    expect(screen.getByTestId('team-panel-light-hand')).toBeInTheDocument()
    expect(screen.getByTestId('team-panel-light-dot').getAttribute('data-status')).toBe('waiting')
    act(() => listWith([summary()]))
    expect(screen.queryByTestId('team-panel-light-hand')).toBeNull()
    expect(screen.getByTestId('team-panel-light-dot').getAttribute('data-status')).toBe('running')
  })

  it('lights off (icon-only): the awaiting worker still shows its light and hand, as in the tab row; a terminal session with nothing awaiting shows nothing', () => {
    useUISettingsStore.setState({ tabIndicatorStyle: 'icon' })
    useAgentStore.setState({ statuses: { [compositeKey('h1', 's1')]: 'running' } })
    listWith([summary({ pending_permission: pending })])
    const { unmount } = render(<TeamSeatLight hostId="h1" sessionCode={worker} />)
    expect(screen.getByTestId('team-panel-light-hand')).toBeInTheDocument()
    unmount()
    render(<TeamSeatLight hostId="h1" sessionCode="s1" />)
    expect(screen.queryByTestId('team-panel-light-dot')).toBeNull()
    expect(screen.queryByTestId('team-panel-light-hand')).toBeNull()
  })

  it('a plain terminal session never gets the hand', () => {
    useAgentStore.setState({ statuses: { [compositeKey('h1', 's1')]: 'waiting' } })
    listWith([summary({ pending_permission: pending })])
    render(<TeamSeatLight hostId="h1" sessionCode="s1" />)
    expect(screen.queryByTestId('team-panel-light-hand')).toBeNull()
    expect(screen.getByTestId('team-panel-light-dot').getAttribute('data-status')).toBe('waiting')
  })
})

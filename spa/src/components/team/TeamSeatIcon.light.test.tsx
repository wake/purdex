// spa/src/components/team/TeamSeatIcon.light.test.tsx — the icon of a panel row without its light (team panel v2): the agent icon alone, and the seat's subagent dots still beside it.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { TeamSeatIcon } from './TeamSeatIcon'
import { useAgentStore } from '../../stores/useAgentStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { compositeKey } from '../../lib/composite-key'

const ck = compositeKey('h1', 's1')
const ref = { id: 'a', type: 'cc', started_at: 1, source_pid: 0, source_start_time: '' }
const saved = useUISettingsStore.getState().tabIndicatorStyle

beforeEach(() => {
  useUISettingsStore.setState({ tabIndicatorStyle: 'badge' })
  useAgentStore.setState({ statuses: { [ck]: 'running' }, agentTypes: { [ck]: 'cc' }, subagents: { [ck]: [ref] } } as never)
})
afterEach(() => { cleanup(); useUISettingsStore.setState({ tabIndicatorStyle: saved }) })

describe('TeamSeatIcon light={false}', () => {
  it('draws no status light, and the subagent dots stay when asked for', () => {
    render(<TeamSeatIcon hostId="h1" sessionCode="s1" subagents light={false} />)
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
    expect(screen.getAllByTestId('subagent-dot')).toHaveLength(1)
  })
  it('without the subagents prop there are no dots, and lights off hides them too', () => {
    const { unmount } = render(<TeamSeatIcon hostId="h1" sessionCode="s1" light={false} />)
    expect(screen.queryAllByTestId('subagent-dot')).toHaveLength(0)
    unmount()
    useUISettingsStore.setState({ tabIndicatorStyle: 'icon' })
    render(<TeamSeatIcon hostId="h1" sessionCode="s1" subagents light={false} />)
    expect(screen.queryAllByTestId('subagent-dot')).toHaveLength(0)
  })
  it('with the light (the default) the status light is there as before', () => {
    render(<TeamSeatIcon hostId="h1" sessionCode="s1" subagents />)
    expect(screen.getByTestId('tab-status-indicator')).toBeTruthy()
  })
})

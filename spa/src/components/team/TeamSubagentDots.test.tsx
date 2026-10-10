// The beads and the ghost lead row draw the seat's subagent dots, like the sidebar's tab rows (spec §4.3, user 2026-10-10).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, within } from '@testing-library/react'
import { TeamMemberBeads } from './TeamMemberBeads'
import { TeamGhostLeadRow } from './TeamGhostLeadRow'
import type { TeamSeatView } from './team-display'
import { useAgentStore } from '../../stores/useAgentStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { compositeKey } from '../../lib/composite-key'

vi.mock('./TeamSidebarBlock', () => ({
  TeamSidebarBlock: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}))

const seat = (id: string, code: string): TeamSeatView => ({
  sessionId: id, title: id, hostId: 'h1', sessionCode: code, role: 'member', tabId: null, state: 'active', hostAlias: '', remote: false,
})
const ref = { id: 'a', type: 'cc', started_at: 1, source_pid: 0, source_start_time: '' }

describe('subagent dots on beads and the ghost lead', () => {
  const savedStyle = useUISettingsStore.getState().tabIndicatorStyle
  afterEach(() => useUISettingsStore.setState({ tabIndicatorStyle: savedStyle }))
  beforeEach(() => {
    useAgentStore.setState({
      statuses: { [compositeKey('h1', 's1')]: 'running', [compositeKey('h1', 's2')]: 'running' }, agentTypes: { [compositeKey('h1', 's1')]: 'cc', [compositeKey('h1', 's2')]: 'cc' },
      subagents: { [compositeKey('h1', 's1')]: [ref] },
    } as never)
  })

  const renderBeads = () => render(
    <TeamMemberBeads teamKey="t" members={[seat('m1', 's1'), seat('m2', 's2')]} activeTabId={null} withHost={false} onOpen={() => {}} onReorder={() => {}} onBlankClick={() => {}} />,
  )

  it.each(['badge', 'dot', 'iconDot'] as const)('a bead draws the dots of its own seat only (%s)', (style) => {
    useUISettingsStore.setState({ tabIndicatorStyle: style })
    const [b1, b2] = renderBeads().getAllByTestId('team-bead')
    expect(within(b1).queryAllByTestId('subagent-dot')).toHaveLength(1)
    expect(within(b2).queryAllByTestId('subagent-dot')).toHaveLength(0)
  })

  it('lights off (icon): no dots on any bead', () => {
    useUISettingsStore.setState({ tabIndicatorStyle: 'icon' })
    const [b1, b2] = renderBeads().getAllByTestId('team-bead')
    expect(within(b1).queryAllByTestId('subagent-dot')).toHaveLength(0)
    expect(within(b2).queryAllByTestId('subagent-dot')).toHaveLength(0)
  })

  it('a bead pads 6px left and 3px right', () => {
    const { getAllByTestId } = render(
      <TeamMemberBeads teamKey="t" members={[seat('m1', 's1')]} activeTabId={null} withHost={false} onOpen={() => {}} onReorder={() => {}} onBlankClick={() => {}} />,
    )
    const cls = getAllByTestId('team-bead')[0].className
    expect(cls).toContain('pl-1.5')
    expect(cls).toContain('pr-[3px]')
    expect(cls).not.toContain('px-1.5')
  })

  it('the ghost lead row draws the lead seat dots', () => {
    const lead = { ...seat('l', 's1'), role: 'lead' as const }
    const { queryAllByTestId } = render(
      <TeamGhostLeadRow ghost={{ lead, teamKey: 't', members: [], collapsed: false } as never} team={{ onOpenSeat: () => {} } as never} activeTabId={null} />,
    )
    expect(queryAllByTestId('subagent-dot')).toHaveLength(1)
  })
})

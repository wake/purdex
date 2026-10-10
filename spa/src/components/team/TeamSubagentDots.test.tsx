// The beads and the ghost lead row draw the seat's subagent dots, like the sidebar's tab rows (spec §4.3, user 2026-10-10).
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, within } from '@testing-library/react'
import { TeamMemberBeads } from './TeamMemberBeads'
import { TeamGhostLeadRow } from './TeamGhostLeadRow'
import type { TeamSeatView } from './team-display'
import { useAgentStore } from '../../stores/useAgentStore'
import { compositeKey } from '../../lib/composite-key'

vi.mock('./TeamSidebarBlock', () => ({
  TeamSidebarBlock: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}))

const seat = (id: string, code: string): TeamSeatView => ({
  sessionId: id, title: id, hostId: 'h1', sessionCode: code, role: 'member', tabId: null, state: 'active', hostAlias: '', remote: false,
})
const ref = { id: 'a', type: 'cc', started_at: 1, source_pid: 0, source_start_time: '' }

describe('subagent dots on beads and the ghost lead', () => {
  beforeEach(() => {
    useAgentStore.setState({
      statuses: { [compositeKey('h1', 's1')]: 'running', [compositeKey('h1', 's2')]: 'running' }, agentTypes: { [compositeKey('h1', 's1')]: 'cc', [compositeKey('h1', 's2')]: 'cc' },
      subagents: { [compositeKey('h1', 's1')]: [ref] },
    } as never)
  })

  it('a bead draws the dots of its own seat only', () => {
    const { getAllByTestId } = render(
      <TeamMemberBeads teamKey="t" members={[seat('m1', 's1'), seat('m2', 's2')]} activeTabId={null} withHost={false} onOpen={() => {}} onReorder={() => {}} onBlankClick={() => {}} />,
    )
    const [b1, b2] = getAllByTestId('team-bead')
    expect(within(b1).queryAllByTestId('subagent-dot')).toHaveLength(1)
    expect(within(b2).queryAllByTestId('subagent-dot')).toHaveLength(0)
  })

  it('the ghost lead row draws the lead seat dots', () => {
    const lead = { ...seat('l', 's1'), role: 'lead' as const }
    const { queryAllByTestId } = render(
      <TeamGhostLeadRow ghost={{ lead, teamKey: 't', members: [], collapsed: false } as never} team={{ onOpenSeat: () => {} } as never} activeTabId={null} />,
    )
    expect(queryAllByTestId('subagent-dot')).toHaveLength(1)
  })
})

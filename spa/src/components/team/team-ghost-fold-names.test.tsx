// spa/src/components/team/team-ghost-fold-names.test.tsx — the sidebar's ghost lead row and the collapsed team's fold dots name the persona (name pool v2 spec §9.3): the face and the
// name on the ghost row, the name in the tooltips; a seat that is no persona keeps its title; the DAEMON-RESOLVED address is the only thing that names a persona.
import { describe, it, expect, beforeEach } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { TeamGhostLeadRow } from './TeamGhostLeadRow'
import { TeamSidebarBlock } from './TeamSidebarBlock'
import type { TeamSeatView } from './team-display'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { makeCollection, makePersona } from '../../lib/names/__fixtures__/personas'

const seat = (over: Partial<TeamSeatView> = {}): TeamSeatView => ({
  sessionId: 'S', title: 'deploy', address: 'mlab/purdex-1f-kenji', hostId: 'h1', sessionCode: 's1', role: 'lead', tabId: null, state: 'active', hostAlias: '', remote: false, ...over,
})
const seedKenji = () => {
  const c = makeCollection(['kenji'])
  c.items[0] = makePersona('kenji', { name: 'Kenji', avatar_data: 'data:image/png;base64,AA==' })
  usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
}
const team = { beadHost: false, onOpenSeat: () => {}, onReorderMembers: () => {}, onToggleCollapse: () => {} } as never

beforeEach(() => {
  usePersonaStore.setState({ byHost: {} })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, subagents: {} } as never)
  useUISettingsStore.setState({ tabIndicatorStyle: 'badge', hostBadgeSidebarBox: 16 } as never)
  useHostLookStore.setState({ looks: {} })
  useI18nStore.getState().setLocale('zh-TW')
})

describe('the ghost lead row', () => {
  const ghost = (lead = seat()) => render(<TeamGhostLeadRow ghost={{ lead, teamKey: 't', members: [], collapsed: false, color: '#fff' } as never} team={team} activeTabId={null} />)
  const row = () => screen.getByTestId('team-ghost-lead').querySelector('[role="button"]') as HTMLElement

  it('shows the face and the name, and the name in the tooltip', () => {
    seedKenji()
    ghost()
    expect(row().textContent).toContain('Kenji')
    expect(row().textContent).not.toContain('deploy')
    expect(row().querySelector('img')).not.toBeNull()
    expect(row().getAttribute('title')).toContain('Kenji')
    expect(row().getAttribute('title')).not.toContain('deploy')
  })

  it('a lead that is no persona keeps its title in the row and the tooltip', () => {
    seedKenji()
    ghost(seat({ address: 'mlab/purdex-1f-ghost' }))
    expect(row().textContent).toContain('deploy')
    expect(row().getAttribute('title')).toContain('deploy')
    expect(row().querySelector('img')).toBeNull()
  })

  it('redraws when the persona collection arrives after the first draw', () => {
    ghost()
    expect(row().textContent).toContain('deploy')
    act(() => seedKenji())
    expect(row().textContent).toContain('Kenji')
    expect(row().getAttribute('title')).toContain('Kenji')
  })
})

describe('the collapsed team\'s fold dots', () => {
  const folded = (members: TeamSeatView[]) => render(
    <TeamSidebarBlock team={team} teamKey="t" collapsed members={members} activeTabId={null}><div /></TeamSidebarBlock>,
  )
  const dotTitles = () => screen.getAllByTestId('team-fold-dot').map((d) => d.getAttribute('title'))

  it('carry the persona\'s name as their tooltip, the title for a seat that is none', () => {
    seedKenji()
    folded([seat({ sessionId: 'a', role: 'member' }), seat({ sessionId: 'b', role: 'member', address: 'mlab/purdex-1f-ghost', title: 'ghosted' })])
    expect(dotTitles()).toEqual(['Kenji', 'ghosted'])
  })

  it('redraw when the persona collection arrives later', () => {
    folded([seat({ sessionId: 'a', role: 'member' })])
    expect(dotTitles()).toEqual(['deploy'])
    act(() => seedKenji())
    expect(dotTitles()).toEqual(['Kenji'])
  })
})

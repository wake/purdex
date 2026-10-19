// spa/src/components/team/team-seat-names.test.tsx — the team surfaces name a seat by its persona (name pool v2 spec §9.3): the tooltips and titles use the name, the
// persona behind the seat's DAEMON-RESOLVED address (never a string the session sets itself), and the surface redraws when the persona collection arrives later.
import { describe, it, expect, beforeEach } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { TeamCell } from './TeamCell'
import { TeamMemberBeads } from './TeamMemberBeads'
import type { TeamSeatView } from './team-display'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { makeCollection, makePersona } from '../../lib/names/__fixtures__/personas'

const seat = (over: Partial<TeamSeatView> = {}): TeamSeatView => ({
  sessionId: 'S', title: 'deploy', address: 'mlab/purdex-1f-kenji', hostId: 'h1', sessionCode: 's1', role: 'member', tabId: null, state: 'active', hostAlias: '', remote: false, ...over,
})
const seedKenji = () => {
  const c = makeCollection(['kenji'])
  c.items[0] = makePersona('kenji', { name: 'Kenji' })
  usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
}

beforeEach(() => {
  usePersonaStore.setState({ byHost: {} })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, subagents: {} } as never)
  useUISettingsStore.setState({ tabIndicatorStyle: 'badge', hostBadgeSidebarBox: 16 } as never)
  useHostLookStore.setState({ looks: {} })
})

describe('the cell\'s tooltip', () => {
  const cell = () => screen.getByTestId('team-panel-cell')
  const mount = (s = seat()) => render(<TeamCell teamKey={`h1\u0000t`} seat={s} isActive={false} onOpen={() => {}} />)

  it('starts with the persona\'s name, not the title', () => {
    seedKenji()
    mount()
    expect(cell().getAttribute('title')).toMatch(/^Kenji · /)
    expect(cell().getAttribute('title')).not.toContain('deploy')
  })

  it('keeps the title for a seat that is no persona, and for a forged address-less name', () => {
    seedKenji()
    mount(seat({ address: 'mlab/purdex-1f-ghost' }))
    expect(cell().getAttribute('title')).toMatch(/^deploy · /)
  })

  it('redraws when the persona collection arrives after the first draw', () => {
    mount()
    expect(cell().getAttribute('title')).toMatch(/^deploy · /)
    act(() => seedKenji())
    expect(cell().getAttribute('title')).toMatch(/^Kenji · /)
  })
})

describe('the beads\' tooltips', () => {
  const beads = (members: TeamSeatView[]) => render(
    <TeamMemberBeads teamKey="t" members={members} activeTabId={null} withHost={false} onOpen={() => {}} onReorder={() => {}} onBlankClick={() => {}} />,
  )

  it('name the persona, with the seat state after it', () => {
    seedKenji()
    beads([seat({ sessionId: 'm1' }), seat({ sessionId: 'm2', state: 'joining', address: 'mlab/purdex-1f-ghost', title: 'ghosted' })])
    const tips = screen.getAllByTestId('team-bead').map((b) => b.getAttribute('title') ?? '')
    expect(tips[0]).toBe('Kenji')
    expect(tips[1]).toMatch(/^ghosted · /)
  })

  it('redraw when the persona collection arrives later', () => {
    beads([seat({ sessionId: 'm1' })])
    expect(screen.getAllByTestId('team-bead')[0].getAttribute('title')).toBe('deploy')
    act(() => seedKenji())
    expect(screen.getAllByTestId('team-bead')[0].getAttribute('title')).toBe('Kenji')
  })
})

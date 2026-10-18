// spa/src/components/team/TeamPanel.rowV2.test.tsx — the team panel's row, v2 (name pool v2 spec §9.5, plan NA-0 design, NA-4a): a 44 px avatar spanning the row's lines with
// the light as its corner badge; line 1 is the muted agent icon (no light), the host badge, the NAME (semibold) and the title (muted, the first to give way). A seat with no
// persona keeps the layout: initials on the host colour in the same 44 px, and the title as its name. Real stores; the icon and the host badge are mocked as in the other panel tests.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { vi } from 'vitest'

vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: ({ light }: { light?: boolean }) => <span data-testid="seat-icon" data-light={String(light !== false)} />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

import { TeamPanelArea } from './TeamPanelArea'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { makeCollection, makePersona } from '../../lib/names/__fixtures__/personas'
import { compositeKey } from '../../lib/composite-key'

const row = (sessionId: string) => screen.getAllByTestId('team-panel-row').find((r) => r.getAttribute('data-session-id') === sessionId)!
const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  usePersonaStore.setState({ byHost: {} })
  useJournalStore.getState().reset()
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('zh-TW')
  seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }], activeTabId: 'lead' })
})
afterEach(() => { cleanup() })

describe('a row of the full panel (v2)', () => {
  it('the avatar is 44 px and holds the light as its corner badge; the icon on line 1 has no light', () => {
    mount()
    const a = row('A')
    const avatar = within(a).getByTestId('team-panel-avatar')
    expect(avatar.className).toMatch(/w-11/)
    expect(within(avatar).getByTestId('team-panel-light')).toBeTruthy()
    expect(within(a).getByTestId('seat-icon')).toHaveAttribute('data-light', 'false')
    expect(within(a).getAllByTestId('team-panel-light')).toHaveLength(1) // and it is the only one
  })

  it('a seat with no persona keeps the layout: initials on the avatar, the title in the name\'s place, no second title', () => {
    mount()
    const a = row('A')
    const avatar = within(a).getByTestId('team-panel-avatar')
    expect(avatar.querySelector('[role="img"]')).not.toBeNull() // the initials
    expect(within(a).getByTestId('team-panel-seat-name')).toHaveTextContent('title A')
    expect(within(a).queryByTestId('team-panel-seat-title')).toBeNull()
  })

  it('the light shows the seat\'s status, with the ring in the row\'s colour', () => {
    mount()
    useUISettingsStore.setState({ tabIndicatorStyle: 'overlay' })
    const ck = compositeKey(HOST, 'code-a-tm') // the fixture's session codes are `code-<tmux name>`
    act(() => { useAgentStore.setState((s) => ({ statuses: { ...s.statuses, [ck]: 'running' } })) })
    const dot = within(row('A')).getByTestId('team-panel-light-dot')
    expect(dot).toHaveAttribute('data-status', 'running')
  })

  it('the name never gives way (no truncate, no shrink); the title is the one that truncates (design: 300 px)', () => {
    const c = makeCollection(['xx'])
    c.items[0] = makePersona('xx', { name: 'A Very Long Persona Name' })
    usePersonaStore.setState({ byHost: { [HOST]: { status: 'ready', collection: c } } })
    mount()
    const name = within(row('A')).getByTestId('team-panel-seat-name')
    expect(name.className).toMatch(/flex-shrink-0/)
    expect(name.className).toMatch(/whitespace-nowrap/)
    expect(name.className).not.toMatch(/truncate/)
    expect(within(row('A')).getByTestId('team-panel-seat-title').className).toMatch(/truncate/)
  })

  it('the light\'s ring follows the row\'s own background: normal, hover, active', () => {
    mount()
    expect(row('A').className).toContain('[--seat-ring:var(--surface-secondary)]')
    expect(row('A').className).toContain('hover:[--seat-ring:var(--surface-hover)]')
    expect(row('L').className).toContain('[--seat-ring:var(--surface-active)]') // the lead's tab is the active one in this scene
  })

  it('the avatar is decoration for a screen reader: the name is read once, from the row', () => {
    mount()
    expect(within(row('A')).getByTestId('team-panel-avatar')).toHaveAttribute('aria-hidden', 'true')
  })

  it('a seat with a persona: the persona\'s name (semibold) and the title after it (muted)', () => {
    const c = makeCollection(['xx'])
    c.items[0] = makePersona('xx', { name: 'Yuki', avatar_data: 'data:image/png;base64,AA==' }) // the fixture's seats are `mlab/<id>-xx`
    usePersonaStore.setState({ byHost: { [HOST]: { status: 'ready', collection: c } } })
    mount()
    const a = row('A')
    expect(within(a).getByTestId('team-panel-seat-name')).toHaveTextContent('Yuki')
    expect(within(a).getByTestId('team-panel-seat-name').className).toMatch(/font-semibold/)
    expect(within(a).getByTestId('team-panel-seat-title')).toHaveTextContent('title A')
    expect(within(a).getByTestId('team-panel-seat-title').className).toMatch(/text-text-muted/)
    expect(within(a).getByTestId('team-panel-avatar').querySelector('img')).toHaveAttribute('src', 'data:image/png;base64,AA==')
  })
})

describe('the lead\'s second line (NA-0 design)', () => {
  const hold = (text: string) => useJournalStore.setState({
    byHost: { [HOST]: { units: { 'team:t': { entries: [{ id: 'e1', text: 'older' }, { id: 'e2', text }] } as never }, unitOfSession: { L: 'team:t' } } },
  } as never)

  it('is the first line of the newest entry the journal holds for the lead\'s unit', () => {
    hold('wired the login\nand more')
    mount()
    expect(within(row('L')).getByTestId('team-panel-lead-line')).toHaveTextContent('wired the login')
    expect(within(row('L')).getByTestId('team-panel-lead-line').textContent).not.toMatch(/and more/)
  })
  it('is absent when the journal holds nothing for the lead, and a member never has it', () => {
    mount()
    expect(within(row('L')).queryByTestId('team-panel-lead-line')).toBeNull()
    hold('x')
    expect(within(row('A')).queryByTestId('team-panel-lead-line')).toBeNull()
  })
})

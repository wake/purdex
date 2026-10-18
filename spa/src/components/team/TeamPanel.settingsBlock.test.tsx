// spa/src/components/team/TeamPanel.settingsBlock.test.tsx — the team settings block at the bottom of the team panel (name pool v2 spec §9.5, NA-0 design): always shown in the
// full panel, on the lead's own team only (the lead's tab is the one in view), and only where the lead's host offers something to set; never in the one-line panel
// nor the title-bar strip. Real stores, provider and panel area.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { TitleBar } from '../TitleBar'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useI18nStore } from '../../stores/useI18nStore'

vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const scene = (activeTabId: string | null = 'lead') => seedScene({
  members: [['A', 'a-tm'], ['B', 'b-tm']],
  tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]],
  workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }],
  activeTabId,
  teamName: 'Release train',
})
/** The roster carries a cap and the host lists `team.max_members.v1`: the block has something to show. */
const withCap = () => act(() => {
  const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
  roster[0].max_members = 3
  roster[0].in_use = 2
  useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
  useUnattendedStore.getState().setMaxMembersSupport(HOST, 'yes')
})
const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
const block = () => screen.queryByTestId('team-settings-block')
const setActive = (id: string | null) => act(() => { useTabStore.setState({ activeTabId: id }) })

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useUnattendedStore.getState().reset()
  useTeamUiStore.setState({ panel: { width: 412 }, teamDrill: {} })
  useShownHostsStore.setState({ ids: [HOST] })
  clearModuleRegistry()
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => { cleanup(); act(() => useI18nStore.getState().setLocale('en')) })

describe('the settings block on the panel', () => {
  it('on the lead\'s tab, in the full panel: at the panel\'s bottom, after the rows, always shown (no gear to open it)', () => {
    scene()
    withCap()
    mount()
    const panel = screen.getByTestId('team-panel')
    expect(block()).not.toBeNull()
    expect(panel.lastElementChild).toBe(block())
    expect(within(block()!).getByTestId('cap-stepper')).toHaveTextContent('成員上限')
    expect(screen.queryByTestId('team-settings-gear')).toBeNull()
    expect(screen.queryByTestId('team-settings-panel')).toBeNull()
  })

  it('a member\'s tab of the same team shows the panel without the block (the lead\'s own team only)', () => {
    scene('ma')
    withCap()
    mount()
    expect(screen.getByTestId('team-panel')).toBeInTheDocument()
    expect(block()).toBeNull()
  })

  it('a tab of no team (solo) has none', () => {
    scene('x')
    withCap()
    mount()
    expect(block()).toBeNull()
  })

  it('none while the lead\'s host offers nothing to set (no cap capability, no relay quota)', () => {
    scene()
    mount()
    expect(screen.getByTestId('team-panel')).toBeInTheDocument()
    expect(block()).toBeNull()
    act(() => useUnattendedStore.getState().setQuotaSupport(HOST, 'yes')) // the relay quota alone is something to set
    expect(block()).not.toBeNull()
  })

  it('moving from the lead\'s tab to a member\'s takes the block away, and back brings it', () => {
    scene()
    withCap()
    mount()
    expect(block()).not.toBeNull()
    setActive('ma')
    expect(block()).toBeNull()
    setActive('lead')
    expect(block()).not.toBeNull()
  })

  it('the one-line panel and the title-bar strip have none', () => {
    scene()
    withCap()
    render(<TeamDisplayProvider><TitleBar title="Purdex" /><TeamPanelArea /></TeamDisplayProvider>)
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    expect(screen.getByTestId('team-panel')).toHaveAttribute('data-mode', 'line')
    expect(block()).toBeNull()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
    expect(screen.getByTestId('team-title-strip')).toBeInTheDocument()
    expect(block()).toBeNull()
  })

  it('the cap follows the roster: a roster event moves the number the block shows', () => {
    scene()
    withCap()
    mount()
    expect(within(block()!).getByTestId('cap-value')).toHaveTextContent('3')
    act(() => {
      const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
      roster[0].max_members = 5
      useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
    })
    expect(within(block()!).getByTestId('cap-value')).toHaveTextContent('5')
  })
})

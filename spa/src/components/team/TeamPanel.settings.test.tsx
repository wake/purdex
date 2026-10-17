// spa/src/components/team/TeamPanel.settings.test.tsx — the team settings gear on the team panel's title (journal v5 §3.9, U14):
// leads only — it shows on the lead's tab, at the right of the header before ⌃, in full and in line; a member's tab of the
// same team, a tab of no team and the title-bar strip have none. It opens 「團隊設定・{team name}」 under it. Real stores,
// provider and panel area.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
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
/** The roster carries a cap and the host lists `team.max_members.v1`: the settings have something to show. */
const withCap = () => act(() => {
  const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
  roster[0].max_members = 3
  roster[0].in_use = 2
  useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
  useUnattendedStore.getState().setMaxMembersSupport(HOST, 'yes')
})
const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
const gear = () => screen.queryByTestId('team-settings-gear')
const settings = () => screen.queryByTestId('team-settings-panel')
const mode = () => screen.getByTestId('team-panel').getAttribute('data-mode')
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

describe('the gear', () => {
  it('on the lead\'s tab: at the right of the header, right before ⌃, in full and in line', () => {
    scene()
    withCap()
    mount()
    for (const m of ['full', 'line'] as const) {
      act(() => useTeamUiStore.getState().setPanelMode(KEY, m))
      expect(mode()).toBe(m)
      const g = gear()!
      expect(within(screen.getByTestId('team-panel-header')).getByTestId('team-settings-gear')).toBe(g)
      expect(g.nextElementSibling).toBe(screen.getByTestId('team-panel-to-titlebar'))
      expect(g).toHaveAttribute('aria-label', '團隊設定')
    }
  })

  it('a click opens 「團隊設定・{team name}」 with the cap, a second click closes it; the header does not toggle', () => {
    scene()
    withCap()
    mount()
    fireEvent.click(gear()!)
    expect(settings()).not.toBeNull()
    expect(within(settings()!).getByText('團隊設定・Release train')).toBeInTheDocument()
    expect(within(settings()!).getByTestId('cap-stepper')).toBeInTheDocument()
    expect(gear()).toHaveAttribute('aria-expanded', 'true')
    expect(mode()).toBe('full') // the gear is a button: no full ⇄ line
    fireEvent.mouseDown(gear()!) // the anchor's own press is not an outside click
    fireEvent.click(gear()!)
    expect(settings()).toBeNull()
    expect(gear()).toHaveAttribute('aria-expanded', 'false')
  })

  it('a member\'s tab of the same team shows the same panel without a gear (leads only)', () => {
    scene('ma')
    withCap()
    mount()
    expect(screen.getByTestId('team-panel')).toBeInTheDocument()
    expect(gear()).toBeNull()
  })

  it('moving from the lead\'s tab to a member\'s closes open settings', () => {
    scene()
    withCap()
    mount()
    fireEvent.click(gear()!)
    expect(settings()).not.toBeNull()
    setActive('ma')
    expect(settings()).toBeNull()
    setActive('lead')
    expect(settings()).toBeNull() // not brought back
    expect(gear()).not.toBeNull()
  })

  it('a tab of no team (solo) has none', () => {
    scene('x')
    withCap()
    mount()
    expect(gear()).toBeNull()
  })

  it('none while the lead\'s host offers nothing to set (no cap capability, no roots, no relay quota)', () => {
    scene()
    mount()
    expect(screen.getByTestId('team-panel')).toBeInTheDocument()
    expect(gear()).toBeNull()
    act(() => useUnattendedStore.getState().setQuotaSupport(HOST, 'yes')) // the relay quota alone is something to set
    expect(gear()).not.toBeNull()
  })

  it('the title-bar strip has none', () => {
    scene()
    withCap()
    render(<TeamDisplayProvider><TitleBar title="Purdex" /><TeamPanelArea /></TeamDisplayProvider>)
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
    expect(screen.getByTestId('team-title-strip')).toBeInTheDocument()
    expect(gear()).toBeNull()
  })
})

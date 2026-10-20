// spa/src/features/workspace/components/HomeRow.viewToggle.test.tsx — the sidebar's view toggle, split off the Home button (name pool v2 spec §9.6, NA-5): a 30 px square button left of Home that switches
// between the workspace view (the profile icon) and the task-group view (the team icon).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import { HomeRow } from './HomeRow'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useTeamUiStore } from '../../../stores/useTeamUiStore'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useGroupViewStore } from '../../../stores/useGroupViewStore'
import { useManualGroupsStore } from '../../../stores/useManualGroupsStore'
import { useWorkspaceStore } from '../store'
import { manualGroupKey } from '../../../lib/task-groups/group-key'

const WORLD = { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null }
const toggle = () => screen.getByTestId('sidebar-view-toggle')
const logoIn = (el: HTMLElement) => el.querySelector('img[src="/icons/logo-transparent.png"]')
const renderRow = (onSelectHome = vi.fn()) => { render(<HomeRow isActive={false} onSelectHome={onSelectHome} />); return onSelectHome }

beforeEach(() => {
  cleanup()
  useI18nStore.getState().setLocale('zh-TW')
  useLayoutStore.setState(useLayoutStore.getInitialState())
  useTeamUiStore.setState({ sidebarView: 'workspace' })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
  useProfileSwitcherStore.getState().setOpen(false)
})

describe('the toggle', () => {
  it('is a 30 px square button before the Home button, and the profile icon leaves Home for it', () => {
    renderRow()
    expect(screen.getAllByRole('button').map((b) => b.getAttribute('data-testid'))).toEqual(['sidebar-view-toggle', 'home-button', 'watch-shield'])
    expect(toggle().className).toContain('w-[30px]')
    expect(toggle().className).toContain('h-[30px]')
    expect(logoIn(toggle())).not.toBeNull() // the Purdex logo: the master has no icon of its own
    expect(logoIn(screen.getByTestId('home-button'))).toBeNull()
  })

  it('the workspace view shows the profile icon of the profile on screen', () => {
    useLocalProfilesStore.getState().setProfileAppearance('master', { icon: 'Star' })
    renderRow()
    expect(within(toggle()).getByTestId('profile-icon')).toHaveAttribute('data-icon', 'Star')
    expect(toggle()).toHaveAttribute('aria-pressed', 'false')
  })

  it('a click switches to the task-group view (the flag is the store\'s), and the icon becomes the team icon', () => {
    renderRow()
    fireEvent.click(toggle())
    expect(useTeamUiStore.getState().sidebarView).toBe('groups')
    expect(toggle()).toHaveAttribute('aria-pressed', 'true')
    const icon = within(toggle()).getByTestId('sidebar-view-team-icon')
    expect(icon).toHaveAttribute('data-icon', 'UsersThree')
  })

  it('wears no frame in either view, and the same hover in both: the state is the icon and aria-pressed', () => {
    renderRow()
    const workspaceView = toggle().className
    fireEvent.click(toggle())
    const groupView = toggle().className
    for (const cls of [workspaceView, groupView]) {
      expect(cls.split(/\s+/).filter((c) => c.startsWith('ring') || c.startsWith('outline') || c.startsWith('border'))).toEqual([]) // no frame of its own: only the keyboard focus ring (focus-visible:ring-1) is allowed
      expect(cls).toContain('hover:bg-surface-hover')
    }
    expect(groupView).toBe(workspaceView)
  })

  it('the team icon is the profile\'s own when it chose one; a second click goes back to the workspace view', () => {
    useLocalProfilesStore.getState().setProfileAppearance('master', { teamIcon: 'Rocket' })
    useTeamUiStore.setState({ sidebarView: 'groups' })
    renderRow()
    expect(within(toggle()).getByTestId('sidebar-view-team-icon')).toHaveAttribute('data-icon', 'Rocket')
    fireEvent.click(toggle())
    expect(useTeamUiStore.getState().sidebarView).toBe('workspace')
    expect(within(toggle()).queryByTestId('sidebar-view-team-icon')).toBeNull()
  })

  it('a click restores the view\'s last place: to the task-group view the last group and its tab, back to the workspace view the last workspace and its tab (spec §6.3)', () => {
    const tabOf = (id: string, group?: string) => ({ id, kind: 'new-tab', locked: false, pinned: false, createdAt: 1, layout: { type: 'leaf', pane: { id: `${id}-p`, content: { kind: 'dashboard' } } }, ...(group ? { group } : {}) })
    const G = manualGroupKey('aaaaaaaa')
    useManualGroupsStore.setState({ order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 } } })
    useTabStore.setState({ tabs: { g1: tabOf('g1', G), g2: tabOf('g2', G), w1: tabOf('w1') }, activeTabId: 'w1' } as never)
    useWorkspaceStore.setState({ workspaces: [{ id: 'wa', name: 'A', tabs: ['g1', 'g2'], activeTabId: 'g1', moduleConfig: {} }, { id: 'wb', name: 'B', tabs: ['w1'], activeTabId: 'w1', moduleConfig: {} }], activeWorkspaceId: 'wb' } as never)
    useGroupViewStore.setState({ selectedGroup: null, lastActiveTab: { [G]: 'g2' }, expanded: {}, lastByView: { workspace: 'wa', group: G } })
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'top' })
    renderRow()
    fireEvent.click(toggle()) // workspace view -> task-group view
    expect(useTeamUiStore.getState().sidebarView).toBe('groups')
    expect([useGroupViewStore.getState().selectedGroup, useTabStore.getState().activeTabId]).toEqual([G, 'g2'])
    fireEvent.click(toggle()) // and back
    expect(useTeamUiStore.getState().sidebarView).toBe('workspace')
    expect([useWorkspaceStore.getState().activeWorkspaceId, useTabStore.getState().activeTabId]).toEqual(['wa', 'g2'])
  })

  it('the team icon wears its own weight, standing alone (the default UsersThree too)', () => {
    useLocalProfilesStore.getState().setProfileAppearance('master', { teamIconWeight: 'fill' })
    useTeamUiStore.setState({ sidebarView: 'groups' })
    renderRow()
    const icon = within(toggle()).getByTestId('sidebar-view-team-icon')
    expect(icon).toHaveAttribute('data-icon', 'UsersThree')
    expect(icon).toHaveAttribute('data-weight', 'fill')
  })

  it('the team icon is in teamColor; with none set it follows the profile colour; with neither it has no colour of its own', () => {
    useTeamUiStore.setState({ sidebarView: 'groups' })
    const icon = () => screen.getByTestId('sidebar-view-team-icon')
    useLocalProfilesStore.getState().setProfileAppearance('master', { icon: 'Star', color: '#ff0000', teamColor: '#00ff00' })
    renderRow()
    expect(icon().style.color).toBe('rgb(0, 255, 0)')
    cleanup()
    useLocalProfilesStore.getState().setProfileAppearance('master', { teamColor: null })
    renderRow()
    expect(icon().style.color).toBe('rgb(255, 0, 0)') // the profile's colour: the same as its profile icon
    cleanup()
    useLocalProfilesStore.getState().setProfileAppearance('master', { color: null })
    renderRow()
    expect(icon().style.color).toBe('')
  })

  it('has one name whichever view is on, and aria-pressed says which', () => {
    renderRow()
    expect(toggle()).toHaveAccessibleName('任務編組檢視')
    expect(toggle()).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(toggle())
    expect(toggle()).toHaveAccessibleName('任務編組檢視')
    expect(toggle()).toHaveAttribute('aria-pressed', 'true')
  })

  it('does not touch Home: a click on the toggle selects nothing', () => {
    const onSelectHome = renderRow()
    fireEvent.click(toggle())
    expect(onSelectHome).not.toHaveBeenCalled()
  })

  // The regression the split must not cause: the icon used to be part of the Home button, so a click anywhere on it opened the menu of a device with a slave profile.
  it('on a device with a slave profile, a click on Home still opens the profile menu', () => {
    useLocalProfilesStore.getState().addSlave('Lab', WORLD)
    const onSelectHome = renderRow()
    fireEvent.click(screen.getByTestId('home-button'))
    expect(screen.getByTestId('profile-switcher-menu')).toBeInTheDocument()
    expect(onSelectHome).not.toHaveBeenCalled()
  })

  it('on a device with no slave profile, a click on Home still selects Home', () => {
    const onSelectHome = renderRow()
    fireEvent.click(screen.getByTestId('home-button'))
    expect(onSelectHome).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('profile-switcher-menu')).toBeNull()
  })
})

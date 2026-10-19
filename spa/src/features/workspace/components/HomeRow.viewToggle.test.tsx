// spa/src/features/workspace/components/HomeRow.viewToggle.test.tsx — the sidebar's view toggle, split off the Home button (name pool v2 spec §9.6, NA-5): a 30 px square button left of Home that switches
// between the workspace view (the profile icon) and the task-group view (the team icon, pink outline). It is behind the NA-5 → NA-6 gate: closed, the row is exactly today's.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import { HomeRow } from './HomeRow'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useTeamUiStore } from '../../../stores/useTeamUiStore'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { taskGroupView } from '../lib/task-group-view'

const WORLD = { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null }
const toggle = () => screen.getByTestId('sidebar-view-toggle')
const logoIn = (el: HTMLElement) => el.querySelector('img[src="/icons/logo-transparent.png"]')
const renderRow = (onSelectHome = vi.fn()) => { render(<HomeRow isActive={false} onSelectHome={onSelectHome} />); return onSelectHome }

// The gate as shipped: the closed-gate tests rely on it, they do not set it.
const SHIPPED = taskGroupView.enabled

beforeEach(() => {
  cleanup()
  useI18nStore.getState().setLocale('zh-TW')
  useLayoutStore.setState(useLayoutStore.getInitialState())
  useTeamUiStore.setState({ sidebarView: 'workspace' })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
  useProfileSwitcherStore.getState().setOpen(false)
})
afterEach(() => { taskGroupView.enabled = SHIPPED })

describe('the gate is closed (until NA-6)', () => {
  it('there is no toggle, and the profile icon stays inside the Home button as it always was', () => {
    renderRow()
    expect(screen.queryByTestId('sidebar-view-toggle')).toBeNull()
    expect(logoIn(screen.getByTestId('home-button'))).not.toBeNull()
  })
})

describe('the toggle', () => {
  beforeEach(() => { taskGroupView.enabled = true })

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

  it('a click switches to the task-group view (the flag is the store\'s), and the icon becomes the team icon with a pink outline', () => {
    renderRow()
    fireEvent.click(toggle())
    expect(useTeamUiStore.getState().sidebarView).toBe('groups')
    expect(toggle()).toHaveAttribute('aria-pressed', 'true')
    const icon = within(toggle()).getByTestId('sidebar-view-team-icon')
    expect(icon).toHaveAttribute('data-icon', 'UsersThree')
    expect(toggle().className).toContain('ring-pink')
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

  it('says what a click does, in words', () => {
    renderRow()
    expect(toggle()).toHaveAccessibleName('切換到任務編組檢視')
    fireEvent.click(toggle())
    expect(toggle()).toHaveAccessibleName('切換到工作區檢視')
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

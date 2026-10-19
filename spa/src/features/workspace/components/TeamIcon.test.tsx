// spa/src/features/workspace/components/TeamIcon.test.tsx — the one drawing of a profile's team icon (759 acceptance): the sidebar's view toggle and the appearance editor's preview both
// render <TeamIcon>. What reaches the icon itself is checked here with a stand-in for WorkspaceIcon, because the outer element's data-* only say what was asked, not what was drawn.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { TeamIcon } from './ProfileSwitcher'
import { HomeRow } from './HomeRow'
import { ProfileAppearanceEditor } from '../../../components/settings/profile/ProfileAppearanceEditor'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useTeamUiStore } from '../../../stores/useTeamUiStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'
import { useI18nStore } from '../../../stores/useI18nStore'

vi.mock('./WorkspaceIcon', () => ({
  WorkspaceIcon: ({ icon, weight, size }: { icon: string; weight?: string; size: number }) => (
    <i data-testid="drawn-icon" data-icon={icon} data-weight={weight ?? 'none'} data-size={size} />
  ),
}))

const drawn = (el: HTMLElement) => el.querySelector('[data-testid="drawn-icon"]') as HTMLElement

beforeEach(() => {
  cleanup()
  useI18nStore.getState().setLocale('zh-TW')
  useLayoutStore.setState(useLayoutStore.getInitialState())
  useTeamUiStore.setState({ sidebarView: 'groups' })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
  useProfileSwitcherStore.getState().setOpen(false)
})
afterEach(() => { cleanup() })

describe('TeamIcon', () => {
  it('draws the chosen icon (UsersThree when none) at the team icon weight, and passes no weight when none is set', () => {
    const { rerender } = render(<TeamIcon appearance={{}} size={16} />)
    expect(drawn(screen.getByTestId('team-icon'))).toHaveAttribute('data-icon', 'UsersThree')
    expect(drawn(screen.getByTestId('team-icon'))).toHaveAttribute('data-weight', 'none')
    rerender(<TeamIcon appearance={{ teamIcon: 'Rocket', teamIconWeight: 'fill', iconWeight: 'thin' }} size={16} />)
    expect(drawn(screen.getByTestId('team-icon'))).toHaveAttribute('data-icon', 'Rocket')
    expect(drawn(screen.getByTestId('team-icon'))).toHaveAttribute('data-weight', 'fill') // the team icon's, never the profile icon's
  })

  it('the default icon wears the weight too: it stands alone', () => {
    render(<TeamIcon appearance={{ teamIconWeight: 'duotone' }} size={16} />)
    expect(drawn(screen.getByTestId('team-icon'))).toHaveAttribute('data-icon', 'UsersThree')
    expect(drawn(screen.getByTestId('team-icon'))).toHaveAttribute('data-weight', 'duotone')
  })

  it('is in teamColor on the outer element; with none it is in the profile colour; with neither it sets none', () => {
    const { rerender } = render(<TeamIcon appearance={{ color: '#ff0000', teamColor: '#00ff00' }} size={16} />)
    expect(screen.getByTestId('team-icon').style.color).toBe('rgb(0, 255, 0)')
    rerender(<TeamIcon appearance={{ color: '#ff0000' }} size={16} />)
    expect(screen.getByTestId('team-icon').style.color).toBe('rgb(255, 0, 0)')
    rerender(<TeamIcon appearance={{}} size={16} />)
    expect(screen.getByTestId('team-icon').style.color).toBe('')
  })
})

describe('the two places draw the same', () => {
  const appearance = { icon: 'Star', color: '#ff0000', teamIcon: 'Rocket', teamIconWeight: 'fill' as const, teamColor: '#00ff00' }

  it('the sidebar toggle and the editor preview each draw exactly what <TeamIcon> draws for the profile', () => {
    useLocalProfilesStore.getState().setProfileAppearance('master', appearance)
    const direct = render(<TeamIcon appearance={appearance} size={16} testId="x" />)
    const reference = (direct.container.querySelector('[data-testid="x"]') as HTMLElement).outerHTML.replace('data-testid="x"', '')
    cleanup()

    render(<HomeRow isActive={false} onSelectHome={() => {}} />)
    const inToggle = screen.getByTestId('sidebar-view-team-icon')
    expect(inToggle.outerHTML.replace('data-testid="sidebar-view-team-icon"', '')).toBe(reference)
    cleanup()

    render(<ProfileAppearanceEditor id="master" />)
    const inEditor = screen.getByTestId('profile-edit-team-icon-glyph')
    // the editor draws it a little bigger (18): only the size may differ
    expect(inEditor.outerHTML.replace('data-testid="profile-edit-team-icon-glyph"', '')).toBe(reference.replace('data-size="16"', 'data-size="18"'))
    expect(drawn(inEditor)).toHaveAttribute('data-weight', 'fill')
    expect(inEditor.style.color).toBe('rgb(0, 255, 0)')
  })
})

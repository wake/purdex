// spa/src/components/settings/profile/ProfileAppearanceEditor.teamIcon.test.tsx — the team-icon row of a profile's appearance (name pool v2 spec §9.6, NA-5): the icon the sidebar's view toggle wears in the
// task-group view.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { ProfileAppearanceEditor } from './ProfileAppearanceEditor'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useI18nStore } from '../../../stores/useI18nStore'

// A stand-in with the picker's contract (it needs a layout engine, and has its own tests).
vi.mock('../../../features/workspace/components/WorkspaceIconPicker', () => ({
  WorkspaceIconPicker: ({ onSelect, currentIcon }: { onSelect: (n: string) => void; currentIcon?: string }) => (
    <div data-testid="icon-picker-stub" data-current={currentIcon ?? ''}>
      <button data-testid="stub-pick-rocket" onClick={() => onSelect('Rocket')}>Rocket</button>
      <button data-testid="stub-pick-junk" onClick={() => onSelect('NotAnIcon')}>junk</button>
    </div>
  ),
}))

const state = () => useLocalProfilesStore.getState()

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
})
afterEach(() => { cleanup() })

describe('the team-icon row', () => {
  it('shows the default (UsersThree) until one is chosen, and the reset button is off then', () => {
    render(<ProfileAppearanceEditor id="master" />)
    expect(screen.getByTestId('profile-edit-team-icon')).toHaveAttribute('data-icon', 'UsersThree')
    expect(screen.getByTestId('profile-edit-team-icon-default')).toBeDisabled()
  })

  it('picking an icon writes teamIcon, leaves the profile icon alone, and closes the picker', () => {
    state().setProfileAppearance('master', { icon: 'Star' })
    render(<ProfileAppearanceEditor id="master" />)
    fireEvent.click(screen.getByTestId('profile-edit-team-icon'))
    expect(screen.getByTestId('icon-picker-stub')).toHaveAttribute('data-current', 'UsersThree')
    fireEvent.click(screen.getByTestId('stub-pick-rocket'))
    expect(state().master).toMatchObject({ icon: 'Star', teamIcon: 'Rocket' })
    expect(screen.queryByTestId('icon-picker-stub')).toBeNull()
    expect(screen.getByTestId('profile-edit-team-icon')).toHaveAttribute('data-icon', 'Rocket')
  })

  it('the reset button goes back to the default', () => {
    state().setProfileAppearance('master', { teamIcon: 'Rocket' })
    render(<ProfileAppearanceEditor id="master" />)
    fireEvent.click(screen.getByTestId('profile-edit-team-icon-default'))
    expect(Object.hasOwn(state().master, 'teamIcon')).toBe(false)
    expect(screen.getByTestId('profile-edit-team-icon')).toHaveAttribute('data-icon', 'UsersThree')
  })

  it('a name the store refuses is said in words and changes nothing', () => {
    render(<ProfileAppearanceEditor id="master" />)
    fireEvent.click(screen.getByTestId('profile-edit-team-icon'))
    fireEvent.click(screen.getByTestId('stub-pick-junk'))
    expect(state().master.teamIcon).toBeUndefined()
    expect(screen.getByTestId('profile-edit-error')).toHaveAttribute('data-reason', 'bad-team-icon')
    expect(screen.getByTestId('profile-edit-error').textContent).not.toBe('')
  })
})

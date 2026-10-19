// spa/src/components/settings/profile/ProfileAppearanceEditor.teamIcon.test.tsx — the team-icon row of a profile's appearance (name pool v2 spec §9.6, NA-5): the icon the sidebar's view toggle wears in the
// task-group view.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { ProfileAppearanceEditor } from './ProfileAppearanceEditor'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { HOST_COLOR_PRESETS } from '../../../lib/host-color'
import { useI18nStore } from '../../../stores/useI18nStore'

// A stand-in with the picker's contract (it needs a layout engine, and has its own tests).
vi.mock('../../../features/workspace/components/WorkspaceIconPicker', () => ({
  WorkspaceIconPicker: ({ onSelect, currentIcon, currentWeight, onWeightChange }: { onSelect: (n: string) => void; currentIcon?: string; currentWeight?: string; onWeightChange?: (w: string) => void }) => (
    <div data-testid="icon-picker-stub" data-current={currentIcon ?? ''} data-weight={currentWeight ?? ''}>
      <button data-testid="stub-weight-fill" onClick={() => onWeightChange?.('fill')}>fill</button>
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

  it('the reset button goes back to the default icon and its default weight, and leaves the colour alone', () => {
    state().setProfileAppearance('master', { teamIcon: 'Rocket', teamIconWeight: 'fill', teamColor: '#112233' })
    render(<ProfileAppearanceEditor id="master" />)
    fireEvent.click(screen.getByTestId('profile-edit-team-icon-default'))
    expect(Object.hasOwn(state().master, 'teamIcon')).toBe(false)
    expect(Object.hasOwn(state().master, 'teamIconWeight')).toBe(false)
    expect(state().master.teamColor).toBe('#112233')
    expect(screen.getByTestId('profile-edit-team-icon')).toHaveAttribute('data-icon', 'UsersThree')
  })

  it('the picker gets the weight, and a chosen weight is saved with no team icon chosen (and the reset button wakes up for it)', () => {
    render(<ProfileAppearanceEditor id="master" />)
    expect(screen.getByTestId('profile-edit-team-icon-default')).toBeDisabled()
    fireEvent.click(screen.getByTestId('profile-edit-team-icon'))
    fireEvent.click(screen.getByTestId('stub-weight-fill'))
    expect(state().master).toMatchObject({ teamIconWeight: 'fill' })
    expect(state().master.teamIcon).toBeUndefined()
    expect(state().master.icon).toBeUndefined()
    expect(screen.getByTestId('icon-picker-stub')).toHaveAttribute('data-weight', 'fill')
    expect(screen.getByTestId('profile-edit-team-icon-glyph')).toHaveAttribute('data-weight', 'fill')
    expect(screen.getByTestId('profile-edit-team-icon-default')).not.toBeDisabled()
  })

  it('the preview is drawn as the sidebar toggle draws it: the same weight, and teamColor over the profile colour', () => {
    state().setProfileAppearance('master', { icon: 'Star', color: '#ff0000', teamIconWeight: 'duotone' })
    render(<ProfileAppearanceEditor id="master" />)
    const glyph = () => screen.getByTestId('profile-edit-team-icon-glyph')
    expect(glyph()).toHaveAttribute('data-weight', 'duotone')
    expect(glyph().style.color).toBe('rgb(255, 0, 0)') // follows the profile colour
    cleanup()
    state().setProfileAppearance('master', { teamColor: '#00ff00' })
    render(<ProfileAppearanceEditor id="master" />)
    expect(glyph().style.color).toBe('rgb(0, 255, 0)')
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

describe('the team-icon colour row', () => {
  const hex = HOST_COLOR_PRESETS[0]

  it('offers the same presets as the profile colour, and a preset is saved as teamColor, not as the profile colour', () => {
    state().setProfileAppearance('master', { icon: 'Star' })
    render(<ProfileAppearanceEditor id="master" />)
    fireEvent.click(screen.getByTestId(`profile-edit-team-color-${hex}`))
    expect(state().master.teamColor).toBe(hex)
    expect(state().master.color).toBeUndefined()
    expect(screen.getByTestId(`profile-edit-team-color-${hex}`)).toHaveAttribute('aria-pressed', 'true')
    for (const p of HOST_COLOR_PRESETS) expect(screen.getByTestId(`profile-edit-team-color-${p}`)).toBeInTheDocument()
  })

  it('works with no profile icon (the profile colour is switched off then; the team icon is always an icon)', () => {
    render(<ProfileAppearanceEditor id="master" />)
    expect(screen.getByTestId(`profile-edit-team-color-${hex}`)).not.toBeDisabled()
  })

  it('"follow the profile colour" clears teamColor, and is off while it already follows', () => {
    render(<ProfileAppearanceEditor id="master" />)
    expect(screen.getByTestId('profile-edit-team-color-follow')).toBeDisabled()
    fireEvent.click(screen.getByTestId(`profile-edit-team-color-${hex}`))
    expect(screen.getByTestId('profile-edit-team-color-follow')).not.toBeDisabled()
    fireEvent.click(screen.getByTestId('profile-edit-team-color-follow'))
    expect(Object.hasOwn(state().master, 'teamColor')).toBe(false)
  })
})

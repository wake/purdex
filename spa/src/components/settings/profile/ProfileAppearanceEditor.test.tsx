import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import en from '../../../locales/en.json'
import { ProfileAppearanceEditor } from './ProfileAppearanceEditor'
import { useLocalProfilesStore, type LocalProfile } from '../../../stores/useLocalProfilesStore'
import { HOST_COLOR_PRESETS } from '../../../lib/host-color'

// A stand-in with the picker's contract (it needs a layout engine, and has its own tests).
vi.mock('../../../features/workspace/components/WorkspaceIconPicker', () => ({
  WorkspaceIconPicker: ({ onSelect, onWeightChange, currentIcon }: { onSelect: (n: string) => void; onWeightChange?: (w: string) => void; currentIcon?: string }) => (
    <div data-testid="icon-picker-stub" data-current={currentIcon ?? ''}>
      <button data-testid="stub-pick-star" onClick={() => onSelect('Star')}>Star</button>
      <button data-testid="stub-pick-junk" onClick={() => onSelect('NotAnIcon')}>junk</button>
      <button data-testid="stub-weight-fill" onClick={() => onWeightChange?.('fill')}>fill</button>
    </div>
  ),
}))

const slave = (extra: Partial<LocalProfile> = {}): LocalProfile => ({
  id: 's1',
  name: 'Scratch',
  createdAt: 1,
  world: { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null },
  ...extra,
})

const state = () => useLocalProfilesStore.getState()
const nameInput = () => screen.getByTestId('profile-edit-name') as HTMLInputElement
const type = (value: string) => fireEvent.change(nameInput(), { target: { value } })
const enter = () => fireEvent.keyDown(nameInput(), { key: 'Enter' })

beforeEach(() => {
  useLocalProfilesStore.setState({ slaves: { s1: slave() }, slaveOrder: ['s1'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('name', () => {
  it('a slave: Enter saves what the store keeps, and the input shows THAT', () => {
    render(<ProfileAppearanceEditor id="s1" />)
    expect(nameInput().value).toBe('Scratch')
    type('  Client\u200B work  ')
    // Before it is sent: what it will be saved as.
    expect(screen.getByTestId('profile-edit-name-preview')).toHaveTextContent('Client work')
    enter()
    expect(state().slaves.s1.name).toBe('Client work')
    expect(nameInput().value).toBe('Client work')
    expect(screen.queryByTestId('profile-edit-name-preview')).toBeNull()
  })

  it('no preview while what is typed is what would be kept', () => {
    render(<ProfileAppearanceEditor id="s1" />)
    type('Plain')
    expect(screen.queryByTestId('profile-edit-name-preview')).toBeNull()
  })

  it('a slave cannot be left without a name: said inline, nothing saved', () => {
    render(<ProfileAppearanceEditor id="s1" />)
    type('   ')
    fireEvent.click(screen.getByTestId('profile-edit-name-save'))
    expect(state().slaves.s1.name).toBe('Scratch')
    expect(screen.getByTestId('profile-edit-error')).toHaveTextContent(en['settings.profile.local.error.bad_name'])
    // The next good name clears it.
    type('Again')
    enter()
    expect(screen.queryByTestId('profile-edit-error')).toBeNull()
    expect(state().slaves.s1.name).toBe('Again')
  })

  it('the master can: cleared, it is Home again', () => {
    useLocalProfilesStore.setState({ master: { name: 'Work' } })
    render(<ProfileAppearanceEditor id="master" />)
    expect(nameInput().value).toBe('Work')
    expect(nameInput().placeholder).toBe(en['nav.home'])
    type('')
    enter()
    expect(state().master.name).toBeNull()
    expect(nameInput().value).toBe('')
    expect(screen.queryByTestId('profile-edit-error')).toBeNull()
  })

  it('Escape drops the draft', () => {
    render(<ProfileAppearanceEditor id="s1" />)
    type('Nope')
    fireEvent.keyDown(nameInput(), { key: 'Escape' })
    expect(nameInput().value).toBe('Scratch')
    expect(state().slaves.s1.name).toBe('Scratch')
  })
})

describe('icon', () => {
  it('picking one stores it; the default button clears it (and its weight)', () => {
    render(<ProfileAppearanceEditor id="s1" />)
    fireEvent.click(screen.getByTestId('profile-edit-icon'))
    fireEvent.click(screen.getByTestId('stub-pick-star'))
    expect(state().slaves.s1.icon).toBe('Star')
    fireEvent.click(screen.getByTestId('profile-edit-icon'))
    fireEvent.click(screen.getByTestId('stub-weight-fill'))
    expect(state().slaves.s1.iconWeight).toBe('fill')
    fireEvent.click(screen.getByTestId('profile-edit-icon-default'))
    expect(state().slaves.s1.icon).toBeUndefined()
    expect(state().slaves.s1.iconWeight).toBeUndefined()
  })

  it('a weight with no icon chosen changes nothing (there is no icon for it to be the weight of)', () => {
    render(<ProfileAppearanceEditor id="s1" />)
    fireEvent.click(screen.getByTestId('profile-edit-icon'))
    fireEvent.click(screen.getByTestId('stub-weight-fill'))
    expect(state().slaves.s1.icon).toBeUndefined()
    expect(state().slaves.s1.iconWeight).toBeUndefined()
  })

  it('the default button is disabled while the logo shows', () => {
    render(<ProfileAppearanceEditor id="s1" />)
    expect(screen.getByTestId('profile-edit-icon-default')).toBeDisabled()
  })

  it('bad-icon has its own sentence', () => {
    render(<ProfileAppearanceEditor id="s1" />)
    fireEvent.click(screen.getByTestId('profile-edit-icon'))
    fireEvent.click(screen.getByTestId('stub-pick-junk'))
    expect(screen.getByTestId('profile-edit-error')).toHaveTextContent(en['settings.profile.local.error.bad_icon'])
    expect(state().slaves.s1.icon).toBeUndefined()
  })

  it('works for the master too', () => {
    render(<ProfileAppearanceEditor id="master" />)
    fireEvent.click(screen.getByTestId('profile-edit-icon'))
    fireEvent.click(screen.getByTestId('stub-pick-star'))
    expect(state().master.icon).toBe('Star')
  })
})

describe('colour', () => {
  const preset = HOST_COLOR_PRESETS[0]

  it('THE RULE: with the logo the colour has nothing to tint — disabled, said so, and a stored colour is kept', () => {
    useLocalProfilesStore.setState({ slaves: { s1: slave({ color: '#22c55e' }) } })
    render(<ProfileAppearanceEditor id="s1" />)
    expect(screen.getByTestId('profile-edit-color-needs-icon')).toHaveTextContent(en['settings.profile.local.color_needs_icon'])
    expect(screen.getByTestId(`profile-edit-color-${preset}`)).toBeDisabled()
    expect(screen.getByTestId('profile-edit-color-none')).toBeDisabled()
    expect(screen.getByTestId('profile-edit-color-hex')).toBeDisabled()
    fireEvent.click(screen.getByTestId(`profile-edit-color-${preset}`))
    expect(state().slaves.s1.color).toBe('#22c55e')
  })

  it('with an icon: a preset stores it, "no colour" clears it', () => {
    useLocalProfilesStore.setState({ slaves: { s1: slave({ icon: 'Star' }) } })
    render(<ProfileAppearanceEditor id="s1" />)
    expect(screen.queryByTestId('profile-edit-color-needs-icon')).toBeNull()
    fireEvent.click(screen.getByTestId(`profile-edit-color-${preset}`))
    expect(state().slaves.s1.color).toBe(preset)
    expect(screen.getByTestId(`profile-edit-color-${preset}`)).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(screen.getByTestId('profile-edit-color-none'))
    expect(state().slaves.s1.color).toBeUndefined()
  })

  it('a hex typed in is normalised; a bad one is said and not stored', () => {
    useLocalProfilesStore.setState({ slaves: { s1: slave({ icon: 'Star' }) } })
    render(<ProfileAppearanceEditor id="s1" />)
    const hex = screen.getByTestId('profile-edit-color-hex') as HTMLInputElement
    fireEvent.change(hex, { target: { value: 'ABCDEF' } })
    fireEvent.keyDown(hex, { key: 'Enter' })
    expect(state().slaves.s1.color).toBe('#abcdef')
    expect(hex.value).toBe('#abcdef')
    fireEvent.change(hex, { target: { value: 'red' } })
    fireEvent.keyDown(hex, { key: 'Enter' })
    expect(state().slaves.s1.color).toBe('#abcdef')
    expect(screen.getByTestId('profile-edit-error')).toHaveTextContent(en['settings.profile.local.error.bad_color'])
  })

  it('clearing the icon keeps the colour, and disables the control again', () => {
    useLocalProfilesStore.setState({ slaves: { s1: slave({ icon: 'Star', color: '#22c55e' }) } })
    render(<ProfileAppearanceEditor id="s1" />)
    fireEvent.click(screen.getByTestId('profile-edit-icon-default'))
    expect(state().slaves.s1.color).toBe('#22c55e')
    expect(screen.getByTestId('profile-edit-color-none')).toBeDisabled()
  })
})

describe('a profile that is gone', () => {
  it('renders nothing', () => {
    render(<ProfileAppearanceEditor id="nope" />)
    expect(screen.queryByTestId('profile-edit-nope')).toBeNull()
  })

  it('deleted under the editor (another window): the save is refused in words, not thrown', () => {
    const { rerender } = render(<ProfileAppearanceEditor id="s1" />)
    type('Late')
    const gone = vi.spyOn(useLocalProfilesStore.getState(), 'setProfileAppearance').mockReturnValue({ ok: false, reason: 'not-found' })
    enter()
    rerender(<ProfileAppearanceEditor id="s1" />)
    expect(gone).toHaveBeenCalled()
    expect(screen.getByTestId('profile-edit-error')).toHaveTextContent(en['settings.profile.local.error.not_found'])
  })
})
